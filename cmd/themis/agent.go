package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"themis-cli/internal/catalog"
	"themis-cli/internal/discovery"
	"themis-cli/internal/filepreview"
	"themis-cli/internal/projectlink"
	"themis-cli/internal/setup"
	"themis-cli/internal/state"
	"themis-cli/internal/themis"
	"themis-cli/internal/workspace"
)

type agentError struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Recovery string `json:"recovery,omitempty"`
}
type agentOutput struct {
	Version  int         `json:"schema_version"`
	Status   string      `json:"status"`
	Data     any         `json:"data,omitempty"`
	Warnings []string    `json:"warnings"`
	Error    *agentError `json:"error,omitempty"`
}
type agentOptions struct {
	common                                                                                     *commonFlags
	dir, statePath, target, parent, course, query, kind, year, asset, into, out, plan, mapping string
	refresh, all, overwrite, requirements                                                      bool
	depth, limit, offset, maxNodes, startPage, pages, startLine, lines                         int
	timeout                                                                                    time.Duration
	assetIDs                                                                                   pathsFlag
}

// The standard flag package stops at a positional argument. These commands
// accept flags on either side of the search query, as documented.
func parseAgentFlags(fs *flag.FlagSet, args []string) error {
	flags, positionals := []string{}, []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flags = append(flags, arg)
			name := strings.TrimLeft(strings.SplitN(arg, "=", 2)[0], "-")
			f := fs.Lookup(name)
			if f != nil && !strings.Contains(arg, "=") {
				if b, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok || !b.IsBoolFlag() {
					if i+1 >= len(args) {
						return fmt.Errorf("flag needs an argument: %s", arg)
					}
					i++
					flags = append(flags, args[i])
				}
			}
		} else {
			positionals = append(positionals, arg)
		}
	}
	return fs.Parse(append(flags, append([]string{"--"}, positionals...)...))
}
func runAgentCLI(command string, args []string) int {
	return executeAgentCLI(context.Background(), command, args, os.Stdout)
}
func executeAgentCLI(parent context.Context, command string, args []string, output io.Writer) int {
	action := ""
	switch command {
	case "project", "auth", "catalog", "assets", "setup", "downloads":
		if len(args) == 0 {
			return writeAgent(output, true, nil, nil, fmt.Errorf("INVALID_ARGUMENT: missing %s action", command))
		}
		action = args[0]
		args = args[1:]
	}
	fs := newFlagSet(command + " " + action)
	o := agentOptions{}
	o.common = addCommonFlags(fs)
	fs.StringVar(&o.dir, "dir", ".", "Existing workspace directory")
	fs.StringVar(&o.statePath, "state-file", "", "Override the local catalog cache")
	fs.StringVar(&o.target, "target", "", "Canonical target ID or course URL")
	fs.StringVar(&o.parent, "parent", "", "Canonical parent ID or URL")
	fs.StringVar(&o.course, "course", "", "Canonical course ID or URL")
	fs.StringVar(&o.query, "query", "", "Search query (or one positional argument)")
	fs.StringVar(&o.kind, "kind", "", "Filter course, assignment, assignment_group, or exercise")
	fs.StringVar(&o.year, "year", "", "Filter academic year")
	fs.StringVar(&o.asset, "asset", "", "Asset ID or known asset URL")
	fs.Var(&o.assetIDs, "asset-id", "Select an asset for a download plan (repeatable)")
	fs.StringVar(&o.into, "into", "", "Destination relative to the existing workspace")
	fs.StringVar(&o.out, "out", "", "Write the returned plan to this file")
	fs.StringVar(&o.plan, "plan", "", "Path of a reviewed plan to apply")
	fs.StringVar(&o.mapping, "mapping", "", "JSON object mapping target IDs to relative directories")
	fs.BoolVar(&o.refresh, "refresh", false, "Refresh only the selected scope")
	fs.BoolVar(&o.all, "all", false, "Search the whole cached catalog rather than the linked course")
	fs.BoolVar(&o.overwrite, "overwrite", false, "Plan explicit replacement of existing files")
	fs.BoolVar(&o.requirements, "requirements", false, "Read the live submission form during show")
	fs.IntVar(&o.depth, "depth", 0, "Targeted refresh depth (setup plan defaults to 8)")
	fs.IntVar(&o.limit, "limit", 50, "Maximum returned items, up to 200")
	fs.IntVar(&o.offset, "offset", 0, "Result offset")
	fs.IntVar(&o.maxNodes, "max-nodes", 200, "Maximum nodes fetched or included in setup")
	fs.IntVar(&o.startPage, "start-page", 1, "First PDF page")
	fs.IntVar(&o.pages, "pages", 5, "PDF pages per read, up to 20")
	fs.IntVar(&o.startLine, "start-line", 1, "First text line")
	fs.IntVar(&o.lines, "lines", 200, "Text lines per read, up to 2000")
	fs.DurationVar(&o.timeout, "timeout", time.Minute, "Maximum command network/processing duration")
	if err := parseAgentFlags(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(output, "Usage: themis", command, action, "[flags]")
			fs.SetOutput(output)
			fs.PrintDefaults()
			return 0
		}
		return writeAgent(output, true, nil, nil, fmt.Errorf("INVALID_ARGUMENT: %w", err))
	}
	if fs.NArg() > 0 {
		if command != "search" || fs.NArg() != 1 || o.query != "" {
			return writeAgent(output, o.common.jsonOutput, nil, nil, fmt.Errorf("INVALID_ARGUMENT: unexpected positional arguments"))
		}
		o.query = fs.Arg(0)
	}
	if o.limit < 1 || o.limit > 200 || o.offset < 0 || o.depth < 0 || o.depth > 12 || o.maxNodes < 1 || o.maxNodes > 1000 || o.timeout <= 0 || o.startPage < 1 || o.pages < 1 || o.pages > 20 || o.startLine < 1 || o.lines < 1 || o.lines > 2000 {
		return writeAgent(output, o.common.jsonOutput, nil, nil, fmt.Errorf("INVALID_ARGUMENT: invalid limit, offset, depth, max-nodes, or timeout"))
	}
	if o.kind != "" && o.kind != "course" && o.kind != "assignment" && o.kind != "assignment_group" && o.kind != "exercise" && o.kind != "unknown" {
		return writeAgent(output, o.common.jsonOutput, nil, nil, fmt.Errorf("INVALID_ARGUMENT: unsupported --kind"))
	}
	valid := map[string]bool{"capabilities ": true, "search ": true, "show ": true, "project inspect": true, "project unlink": true, "auth status": true, "catalog list": true, "assets list": true, "assets read": true, "setup plan": true, "setup apply": true, "downloads plan": true, "downloads apply": true}
	if !valid[command+" "+action] {
		return writeAgent(output, o.common.jsonOutput, nil, nil, fmt.Errorf("INVALID_ARGUMENT: unsupported command/action"))
	}
	if command == "capabilities" {
		return writeAgent(output, o.common.jsonOutput, map[string]any{"commands": []string{"project inspect", "project unlink", "auth status", "catalog list", "search", "show", "assets list", "assets read", "setup plan", "setup apply", "downloads plan", "downloads apply"}, "target_identity": "canonical ID or URL; fuzzy matches are returned only by search", "setup_scope": "existing local workspace; no repository creation or code submission", "schema_version": 1, "max_pdf_pages": 20, "max_text_lines": 2000, "noninteractive": true, "authentication": map[string]any{"automatic_session_renewal": "opt-in saved username, password and TOTP key", "login": "themis login --non-interactive --json", "configure_totp": "themis auth --totp-secret-stdin", "clear_totp": "themis auth --clear-totp-secret"}, "plan_actions": []string{"create", "skip", "conflict", "replace"}}, nil, nil)
	}
	ctx, cancel := signal.NotifyContext(parent, os.Interrupt)
	defer cancel()
	ctx, timeoutCancel := context.WithTimeout(ctx, o.timeout)
	defer timeoutCancel()
	data, warnings, err := agentOperation(ctx, command, action, &o, fs)
	return writeAgent(output, o.common.jsonOutput, data, warnings, err)
}
func writeAgent(w io.Writer, compact bool, data any, warnings []string, err error) int {
	if warnings == nil {
		warnings = []string{}
	}
	result := agentOutput{Version: 1, Status: "ok", Data: data, Warnings: warnings}
	code := 0
	if err != nil {
		errorCode := "OPERATION_FAILED"
		message := err.Error()
		recovery := ""
		for _, known := range []string{"INVALID_ARGUMENT", "INCOMPLETE_CATALOG", "DESTINATION_CONFLICT", "PLAN_CHANGED", "PROJECT_MISMATCH", "WORKSPACE_BUSY", "PARTIAL_FAILURE", "TARGET_NOT_FOUND", "AMBIGUOUS_TARGET", "INSTANCE_MISMATCH", "AUTH_REQUIRED", "UNREADABLE_DOCUMENT"} {
			if strings.HasPrefix(message, known+":") {
				errorCode = known
				message = strings.TrimSpace(strings.TrimPrefix(message, known+":"))
				break
			}
		}
		if errors.Is(err, themis.ErrNotAuthenticated) || errors.Is(err, themis.ErrSessionExpired) || errors.Is(err, themis.ErrMissingCredentials) || errors.Is(err, themis.ErrInvalidMFA) || errors.Is(err, themis.ErrInvalidCredentials) {
			errorCode = "AUTH_REQUIRED"
		}
		switch errorCode {
		case "AUTH_REQUIRED":
			recovery = "Sign in with themis login, then retry; interactive authentication is never opened by these commands."
		case "INCOMPLETE_CATALOG", "TARGET_NOT_FOUND":
			recovery = "Refresh the exact scope with --refresh, then inspect coverage before planning."
		case "AMBIGUOUS_TARGET":
			recovery = "Choose an explicit canonical ID or URL from the candidates."
		case "PLAN_CHANGED":
			recovery = "Generate and review a new plan."
		case "DESTINATION_CONFLICT":
			recovery = "Choose a different destination or explicitly plan --overwrite; modified files are preserved by default."
		case "PARTIAL_FAILURE":
			recovery = "Inspect per-file results and reapply the same plan to resume."
		}
		result.Status = "error"
		code = 1
		if errorCode == "PARTIAL_FAILURE" {
			code = 3
			result.Status = "partial"
		}
		if errors.Is(err, context.Canceled) {
			code = 130
			errorCode = "CANCELED"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			errorCode = "TIMEOUT"
		}
		result.Error = &agentError{errorCode, message, recovery}
	}
	encoder := json.NewEncoder(w)
	if !compact {
		encoder.SetIndent("", "  ")
	}
	if encodeErr := encoder.Encode(result); encodeErr != nil {
		return 1
	}
	return code
}

type agentRuntime struct {
	base, dir, path string
	st              state.State
	project         projectlink.Context
	client          *http.Client
	warnings        []string
	candidates      []catalog.Summary
	identity        themis.UserData
}

func newAgentRuntime(o *agentOptions, fs *flag.FlagSet, loadCatalog bool) (*agentRuntime, error) {
	dir, err := filepath.Abs(o.dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("--dir must be an existing workspace")
	}
	baseExplicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "base-url" {
			baseExplicit = true
		}
	})
	if !baseExplicit && os.Getenv("THEMIS_BASE_URL") == "" {
		if cfg, _, linkErr := projectlink.ResolveByCWD(dir); linkErr == nil {
			o.common.baseURL = cfg.BaseURL
		} else if !errors.Is(linkErr, projectlink.ErrNotLinked) {
			return nil, linkErr
		}
	}
	base, err := themis.NormalizeBaseURL(o.common.baseURL)
	if err != nil {
		return nil, err
	}
	parsedBase, _ := url.Parse(base)
	if parsedBase.User != nil {
		return nil, fmt.Errorf("INVALID_ARGUMENT: credentials are not supported in --base-url")
	}
	o.common.baseURL = base
	project, err := (projectlink.Service{WorkingDir: dir, BaseURL: base}).Resolve()
	if err != nil {
		return nil, err
	}
	path := o.statePath
	if path == "" {
		path, err = state.DefaultStatePath()
		if err != nil {
			return nil, err
		}
	}
	st := state.NewEmptyState()
	if !loadCatalog {
		st.BaseURL = base
		st.CatalogRootURL = base + "/course"
		return &agentRuntime{base: base, dir: dir, path: path, st: st, project: project, warnings: []string{}}, nil
	}
	if _, err := os.Stat(path); err == nil {
		st, err = state.Load(path)
		if err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if st.BaseURL != "" && st.BaseURL != base {
		return nil, fmt.Errorf("INSTANCE_MISMATCH: this cache belongs to another instance; choose --state-file")
	}
	st.BaseURL = base
	st.CatalogRootURL = base + "/course"
	return &agentRuntime{base: base, dir: dir, path: path, st: st, project: project, warnings: []string{}}, nil
}
func (r *agentRuntime) authenticated(ctx context.Context, o *agentOptions) (*themis.Session, error) {
	session, err := themis.NewSessionWithAuthConfig(r.base, themis.AuthConfig{SessionFile: o.common.sessionFile, Context: ctx})
	if err != nil {
		return nil, err
	}
	transport := session.Client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	session.Client.Transport = requestContextTransport{ctx, transport}
	session.Client.Timeout = 20 * time.Second
	user, err := session.ValidateAuthentication()
	if err != nil {
		return nil, err
	}
	r.identity = user
	r.client = session.Client
	return session, nil
}
func (r *agentRuntime) refresh(ctx context.Context, o *agentOptions, raw string, depth int) error {
	if err := projectlink.ValidateTarget(r.base, raw); err != nil {
		return err
	}
	if r.client == nil {
		if _, err := r.authenticated(ctx, o); err != nil {
			return err
		}
	}
	type entry struct {
		url   string
		depth int
	}
	queue := []entry{{raw, 0}}
	seen := map[string]bool{}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		current := queue[0]
		queue = queue[1:]
		if seen[current.url] {
			continue
		}
		seen[current.url] = true
		if len(seen) > o.maxNodes {
			r.warnings = append(r.warnings, "Refresh reached --max-nodes; catalog coverage is partial.")
			break
		}
		result, err := discovery.NewService(r.base).RefreshNode(r.client, &r.st, current.url, 0)
		if err != nil {
			return err
		}
		r.warnings = append(r.warnings, result.Errors...)
		id, _, _ := state.NodeIDFromURL(current.url)
		n, ok := r.st.Nodes[id]
		if !ok || !n.ChildrenHydrated || n.Status == state.StatusError {
			return fmt.Errorf("refresh failed for %s", current.url)
		}
		if current.depth < depth {
			for _, child := range n.ChildIDs {
				if c, ok := r.st.Nodes[child]; ok {
					if err := projectlink.ValidateTarget(r.base, c.CanonicalURL); err != nil {
						return err
					}
					queue = append(queue, entry{c.CanonicalURL, current.depth + 1})
				}
			}
		}
	}
	return state.SaveAtomic(r.path, r.st, true)
}
func (r *agentRuntime) resolve(raw string) (state.Node, error) {
	if n, err := catalog.Resolve(r.st, raw); err == nil {
		if err := projectlink.ValidateTarget(r.base, n.CanonicalURL); err != nil {
			return state.Node{}, err
		}
		return n, nil
	}
	if _, err := state.CanonicalizeURL(raw); err == nil {
		return state.Node{}, fmt.Errorf("TARGET_NOT_FOUND: target URL is not cached; pass --refresh")
	}
	r.candidates = []catalog.Summary{}
	for _, match := range catalog.Rank(catalog.BuildSearch(r.st), raw, nil) {
		r.candidates = append(r.candidates, catalog.Brief(r.st, r.st.Nodes[match.ID]))
		if len(r.candidates) == 20 {
			break
		}
	}
	return state.Node{}, fmt.Errorf("AMBIGUOUS_TARGET: use search and choose an explicit canonical ID or URL")
}
func (r *agentRuntime) target(ctx context.Context, o *agentOptions, raw string, depth int) (state.Node, error) {
	if raw == "" {
		raw = r.project.Config.LinkedRootURL
	}
	if raw == "" {
		return state.Node{}, fmt.Errorf("INVALID_ARGUMENT: provide --target or link the workspace")
	}
	if o.refresh {
		url := raw
		if n, ok := r.st.Nodes[raw]; ok {
			url = n.CanonicalURL
		}
		if _, err := state.CanonicalizeURL(url); err != nil {
			return r.resolve(raw)
		}
		if err := r.refresh(ctx, o, url, depth); err != nil {
			return state.Node{}, err
		}
	}
	return r.resolve(raw)
}
func agentOperation(ctx context.Context, command, action string, o *agentOptions, fs *flag.FlagSet) (any, []string, error) {
	r, err := newAgentRuntime(o, fs, command != "project" && command != "auth")
	if err != nil {
		return nil, nil, err
	}
	finish := func(data any, err error) (any, []string, error) {
		if err != nil && data == nil && len(r.candidates) > 0 {
			data = map[string]any{"candidates": r.candidates}
		}
		return data, r.warnings, err
	}
	if command == "project" {
		if action == "unlink" {
			if r.project.ConfigPath == "" {
				return finish(nil, fmt.Errorf("project is not linked"))
			}
			updated, err := (projectlink.Service{WorkingDir: r.dir, BaseURL: r.base}).Apply(projectlink.Change{Directory: r.project.LinkDir, Remove: true})
			if err != nil {
				return finish(nil, err)
			}
			r.project = updated
		}
		listing, listErr := (workspace.Service{}).List(r.dir, r.dir)
		directories := []string{}
		truncated := false
		if listErr != nil {
			r.warnings = append(r.warnings, "Could not list local directories: "+listErr.Error())
		} else {
			directories = listing.Folders
			if len(directories) > 200 {
				directories = directories[:200]
				truncated = true
			}
		}
		return finish(map[string]any{"directories": directories, "directories_truncated": truncated, "directory": r.dir, "base_url": r.base, "linked": r.project.ConfigPath != "", "link_directory": r.project.LinkDir, "project_config": r.project.ConfigPath, "linked_target": r.project.Config.LinkedRootURL, "assignment_directories": r.project.Config.DownloadDirs, "last_download_directory": r.project.Config.LastDownloadDir}, nil)
	}
	if command == "auth" {
		session, err := r.authenticated(ctx, o)
		if err != nil {
			var authenticated any
			if errors.Is(err, themis.ErrNotAuthenticated) || errors.Is(err, themis.ErrSessionExpired) || errors.Is(err, themis.ErrMissingCredentials) || errors.Is(err, themis.ErrInvalidMFA) || errors.Is(err, themis.ErrInvalidCredentials) {
				authenticated = false
			}
			return finish(map[string]any{"authenticated": authenticated, "base_url": r.base}, err)
		}
		return finish(map[string]any{"authenticated": session != nil, "base_url": r.base, "user": map[string]string{"name": r.identity.FullName, "email": r.identity.Email}}, nil)
	}
	if (command == "setup" || command == "downloads") && action == "apply" {
		if o.plan == "" {
			return finish(nil, fmt.Errorf("INVALID_ARGUMENT: --plan is required"))
		}
		plan, err := setup.Read(o.plan)
		if err != nil {
			return finish(nil, fmt.Errorf("PLAN_CHANGED: %w", err))
		}
		if plan.Root != mustPhysical(r.dir) {
			return finish(nil, fmt.Errorf("PLAN_CHANGED: --dir must match the planned workspace root"))
		}
		if plan.Mode != command {
			return finish(nil, fmt.Errorf("PLAN_CHANGED: plan belongs to %s, not %s", plan.Mode, command))
		}
		if o.refresh {
			for _, source := range plan.Sources {
				if err := r.refresh(ctx, o, source.URL, 0); err != nil {
					return finish(nil, err)
				}
			}
		}
		download := func(ctx context.Context, asset catalog.Asset, path string) error {
			if r.client == nil {
				if _, err := r.authenticated(ctx, o); err != nil {
					return err
				}
			}
			_, err := discovery.DownloadAssetToPath(r.client, asset.Ref(), path, false)
			return err
		}
		result, err := setup.Apply(ctx, r.st, plan, download)
		return finish(result, err)
	}
	if command == "search" {
		scope := ""
		if o.course != "" {
			n, err := r.target(ctx, o, o.course, o.depth)
			if err != nil {
				return finish(nil, err)
			}
			if n.Kind != "course" {
				return finish(nil, fmt.Errorf("INVALID_ARGUMENT: --course must select a course"))
			}
			scope = n.CanonicalURL
		} else if !o.all && r.project.ConfigPath != "" {
			scope = r.project.Config.LinkedRootURL
			if n, err := r.resolve(scope); err == nil {
				if c := catalog.CourseFor(r.st, n); c != nil {
					scope = c.URL
				}
			} else {
				u, _ := url.Parse(scope)
				parts := strings.Split(strings.Trim(u.Path, "/"), "/")
				if len(parts) >= 3 {
					u.Path = "/course/" + parts[1] + "/" + parts[2]
					u.RawPath = ""
					scope = u.String()
				}
			}
		}
		if o.refresh && o.course == "" {
			raw := scope
			if raw == "" {
				raw = r.st.CatalogRootURL
			}
			if err := r.refresh(ctx, o, raw, o.depth); err != nil {
				return finish(nil, err)
			}
		}
		query := o.query
		if o.year != "" {
			query += " year:" + o.year
		}
		if _, hint := catalog.ParseSearch(query); hint != "" {
			return finish(nil, fmt.Errorf("INVALID_ARGUMENT: %s", hint))
		}
		matches := catalog.Rank(catalog.BuildSearch(r.st), query, nil)
		type match struct {
			catalog.Summary
			Score int `json:"score"`
		}
		results := []match{}
		for _, item := range matches {
			n := r.st.Nodes[item.ID]
			if !catalog.Within(n.CanonicalURL, scope) {
				continue
			}
			t := catalog.Describe(r.st, n)
			if o.kind != "" && t.Role != o.kind && t.Kind != o.kind {
				continue
			}
			results = append(results, match{catalog.Brief(r.st, n), item.Score})
		}
		total := len(results)
		start, end := pageBounds(total, o.offset, o.limit)
		coverage := catalog.CoverageFor(r.st, scope)
		if !coverage.Complete {
			r.warnings = append(r.warnings, "Search covers only discovered nodes; refresh a specific course or lab to expand coverage.")
		}
		return finish(map[string]any{"items": results[start:end], "total": total, "offset": start, "next_offset": nextOffset(total, end), "coverage": coverage}, nil)
	}
	if command == "catalog" {
		raw := o.parent
		if raw == "" {
			raw = r.st.CatalogRootURL
		}
		n, err := r.target(ctx, o, raw, o.depth)
		if err != nil {
			return finish(nil, err)
		}
		items := []catalog.Summary{}
		for _, id := range n.ChildIDs {
			if child, ok := r.st.Nodes[id]; ok {
				items = append(items, catalog.Brief(r.st, child))
			}
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].Kind == "year" && items[j].Kind == "year" {
				return catalog.AcademicYear(items[i].URL) > catalog.AcademicYear(items[j].URL)
			}
			return items[i].Title < items[j].Title
		})
		start, end := pageBounds(len(items), o.offset, o.limit)
		return finish(map[string]any{"parent": catalog.Describe(r.st, n), "items": items[start:end], "total": len(items), "next_offset": nextOffset(len(items), end), "coverage": catalog.CoverageFor(r.st, n.CanonicalURL)}, nil)
	}
	if command == "assets" && action == "read" {
		if o.asset == "" {
			return finish(nil, fmt.Errorf("INVALID_ARGUMENT: --asset is required"))
		}
		candidates := []catalog.Asset{}
		if o.target != "" {
			n, err := r.target(ctx, o, o.target, o.depth)
			if err != nil {
				return finish(nil, err)
			}
			candidates = catalog.Assets(r.st, n, true)
		} else {
			for _, n := range r.st.Nodes {
				candidates = append(candidates, catalog.Assets(r.st, n, false)...)
			}
			sort.Slice(candidates, func(i, j int) bool { return candidates[i].SourceURL < candidates[j].SourceURL })
		}
		var asset *catalog.Asset
		for _, candidate := range candidates {
			if candidate.ID == o.asset || candidate.URL == o.asset {
				a := candidate
				asset = &a
				break
			}
		}
		if asset == nil {
			return finish(nil, fmt.Errorf("TARGET_NOT_FOUND: asset is not in the downloaded catalog"))
		}
		if err := setup.ValidateAsset(r.base, asset.URL); err != nil {
			return finish(nil, err)
		}
		if _, err := r.authenticated(ctx, o); err != nil {
			return finish(nil, err)
		}
		content, err := discovery.ReadAsset(ctx, r.client, asset.Ref(), filepreview.MaxPDFBytes)
		if err != nil {
			return finish(nil, err)
		}
		document, err := filepreview.ReadContext(ctx, asset.Name, content, filepreview.ReadOptions{StartPage: o.startPage, Pages: o.pages, StartLine: o.startLine, Lines: o.lines})
		if err != nil {
			return finish(map[string]any{"asset": asset}, fmt.Errorf("UNREADABLE_DOCUMENT: %w", err))
		}
		return finish(map[string]any{"asset": asset, "document": document}, nil)
	}
	depth := o.depth
	if command == "setup" && o.refresh {
		explicit := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "depth" {
				explicit = true
			}
		})
		if !explicit {
			depth = 8
		}
	}
	if command == "setup" && o.refresh {
		raw := o.target
		if raw == "" {
			raw = r.project.Config.LinkedRootURL
		}
		if n, ok := r.st.Nodes[raw]; ok {
			raw = n.CanonicalURL
		}
		u, err := url.Parse(raw)
		if err != nil || len(strings.Split(strings.Trim(u.Path, "/"), "/")) < 4 {
			return finish(nil, fmt.Errorf("INVALID_ARGUMENT: setup requires a lab or exercise target, not a course"))
		}
	}
	n, err := r.target(ctx, o, o.target, depth)
	if err != nil {
		return finish(nil, err)
	}
	if command == "show" {
		children := []catalog.Summary{}
		for _, id := range n.ChildIDs {
			if child, ok := r.st.Nodes[id]; ok {
				children = append(children, catalog.Brief(r.st, child))
			}
		}
		assets := catalog.Assets(r.st, n, true)
		childStart, childEnd := pageBounds(len(children), o.offset, o.limit)
		assetStart, assetEnd := pageBounds(len(assets), o.offset, o.limit)
		data := map[string]any{"target": catalog.Describe(r.st, n), "children": children[childStart:childEnd], "children_total": len(children), "children_next_offset": nextOffset(len(children), childEnd), "assets": assets[assetStart:assetEnd], "assets_total": len(assets), "assets_next_offset": nextOffset(len(assets), assetEnd), "coverage": catalog.CoverageFor(r.st, n.CanonicalURL), "missing_material_ancestors": catalog.MissingMaterialAncestors(r.st, n)}
		if o.requirements {
			form, err := submissionService(o.common).ReadForm(ctx, n.CanonicalURL)
			if err != nil {
				return finish(data, err)
			}
			data["submission_requirements"] = form
		}
		return finish(data, nil)
	}
	if command == "assets" {
		assets := catalog.Assets(r.st, n, true)
		start, end := pageBounds(len(assets), o.offset, o.limit)
		return finish(map[string]any{"target": catalog.Brief(r.st, n), "assets": assets[start:end], "total": len(assets), "next_offset": nextOffset(len(assets), end), "missing_material_ancestors": catalog.MissingMaterialAncestors(r.st, n)}, nil)
	}

	if command == "setup" || command == "downloads" {
		mappings := map[string]string{}
		if o.mapping != "" {
			data, err := os.ReadFile(o.mapping)
			if err != nil {
				return finish(nil, err)
			}
			if err := json.Unmarshal(data, &mappings); err != nil {
				return finish(nil, err)
			}
		}
		normalizedMappings := map[string]string{}
		for raw, directory := range mappings {
			mapped, err := r.resolve(raw)
			if err != nil {
				return finish(nil, err)
			}
			if previous, exists := normalizedMappings[mapped.ID]; exists && previous != directory {
				return finish(nil, fmt.Errorf("INVALID_ARGUMENT: conflicting mappings for one assignment"))
			}
			normalizedMappings[mapped.ID] = directory
		}
		mappings = normalizedMappings
		if missing := catalog.MissingMaterialAncestors(r.st, n); len(missing) > 0 {
			r.warnings = append(r.warnings, "Shared parent materials may be incomplete; inspect/refresh these exact pages: "+strings.Join(missing, ", "))
		}
		plan, err := setup.Create(r.st, n, setup.Options{Mode: command, Root: r.dir, Into: o.into, Mappings: mappings, AssetIDs: o.assetIDs, Overwrite: o.overwrite, MaxNodes: o.maxNodes})
		if err != nil {
			return finish(nil, err)
		}
		if o.out != "" {
			if err := setup.Save(o.out, plan); err != nil {
				return finish(nil, err)
			}
		}
		return finish(plan, nil)
	}
	return finish(nil, fmt.Errorf("INVALID_ARGUMENT: unsupported operation"))
}
func mustPhysical(path string) string {
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return physical
}
func pageBounds(total, offset, limit int) (int, int) {
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return offset, end
}
func nextOffset(total, end int) *int {
	if end >= total {
		return nil
	}
	return &end
}
