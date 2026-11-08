// Package setup plans and applies assignment files inside an existing workspace.
package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"themis-cli/internal/catalog"
	"themis-cli/internal/discovery"
	"themis-cli/internal/projectlink"
	"themis-cli/internal/state"
	"themis-cli/internal/workspace"
)

type File struct {
	Asset       catalog.Asset `json:"asset"`
	Destination string        `json:"destination"`
	Action      string        `json:"action"`
	BeforeSHA   string        `json:"before_sha256,omitempty"`
}
type Source struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	Fingerprint string `json:"fingerprint"`
}
type Plan struct {
	Version      int               `json:"schema_version"`
	ID           string            `json:"id"`
	Mode         string            `json:"mode"`
	BaseURL      string            `json:"base_url"`
	Root         string            `json:"root"`
	TargetID     string            `json:"target_id"`
	Sources      []Source          `json:"sources"`
	Files        []File            `json:"files"`
	Mappings     map[string]string `json:"assignment_directories"`
	ConfigPath   string            `json:"project_config"`
	ConfigBefore string            `json:"project_config_before_sha256"`
	LinkURL      string            `json:"linked_course_url"`
}
type Options struct {
	Mode, Root, Into string
	Mappings         map[string]string
	AssetIDs         []string
	Overwrite        bool
	MaxNodes         int
}
type Owned struct {
	URL string `json:"url"`
	SHA string `json:"sha256"`
}
type journal struct {
	Version   int              `json:"schema_version"`
	Files     map[string]Owned `json:"files"`
	Completed map[string]bool  `json:"completed_plans"`
}
type Item struct {
	Path  string `json:"path"`
	State string `json:"state"`
	SHA   string `json:"sha256,omitempty"`
	Error string `json:"error,omitempty"`
}
type Result struct {
	PlanID        string `json:"plan_id"`
	Items         []Item `json:"items"`
	Complete      bool   `json:"complete"`
	ProjectConfig string `json:"project_config,omitempty"`
}
type Downloader func(context.Context, catalog.Asset, string) error

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func Fingerprint(n state.Node) string {
	data, _ := json.Marshal(struct {
		URL, Title, Description string
		Children                []string
		Loaded                  bool
		Assets                  []state.AssetRef
		Config                  map[string]any
	}{n.CanonicalURL, n.Title, fmt.Sprint(n.Details["description"]), n.ChildIDs, n.ChildrenHydrated, n.Assets, state.AssignmentConfig(n.Details)})
	return digest(data)
}
func (p Plan) Seal() string { p.ID = ""; data, _ := json.Marshal(p); return digest(data) }
func loadJournal(root string) (journal, error) {
	j := journal{Version: 1, Files: map[string]Owned{}, Completed: map[string]bool{}}
	data, err := os.ReadFile(filepath.Join(root, ".themis", "setup-manifest.json"))
	if os.IsNotExist(err) {
		return j, nil
	}
	if err != nil {
		return j, err
	}
	if err := json.Unmarshal(data, &j); err != nil {
		return j, err
	}
	if j.Version != 1 || j.Files == nil || j.Completed == nil {
		return j, fmt.Errorf("invalid setup manifest")
	}
	return j, nil
}
func Save(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".themis-plan-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func Read(path string) (Plan, error) {
	var p Plan
	f, err := os.Open(path)
	if err != nil {
		return p, err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 8*1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return p, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return p, fmt.Errorf("plan must contain exactly one JSON object")
	}
	if p.Version != 1 || p.ID != p.Seal() {
		return p, fmt.Errorf("unsupported or edited plan; create a new plan")
	}
	return p, nil
}
func descendantNodes(st state.State, root state.Node, max int) ([]state.Node, error) {
	result := []state.Node{}
	seen := map[string]bool{}
	queue := []string{root.ID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		n, ok := st.Nodes[id]
		if !ok || !n.ChildrenHydrated {
			return nil, fmt.Errorf("INCOMPLETE_CATALOG: refresh the target subtree before planning")
		}
		if !catalog.Within(n.CanonicalURL, root.CanonicalURL) {
			return nil, fmt.Errorf("target graph contains an unrelated child")
		}
		result = append(result, n)
		if len(result) > max {
			return nil, fmt.Errorf("setup exceeds %d nodes; select a smaller target", max)
		}
		for _, childID := range n.ChildIDs {
			child, ok := st.Nodes[childID]
			if !ok || child.CanonicalURL == n.CanonicalURL || !catalog.Within(child.CanonicalURL, n.CanonicalURL) {
				return nil, fmt.Errorf("INCOMPLETE_CATALOG: invalid or cyclic assignment hierarchy")
			}
		}
		queue = append(queue, n.ChildIDs...)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CanonicalURL < result[j].CanonicalURL })
	return result, nil
}
func Create(st state.State, target state.Node, opts Options) (Plan, error) {
	service := workspace.Service{}
	listing, err := service.List(opts.Root, opts.Root)
	if err != nil {
		return Plan{}, err
	}
	root := listing.Root
	if opts.Into == "" {
		return Plan{}, fmt.Errorf("--into is required: choose a folder inside the existing workspace")
	}
	if filepath.IsAbs(opts.Into) {
		return Plan{}, fmt.Errorf("--into must be relative to the workspace")
	}
	if _, _, err := service.Bound(root, opts.Into); err != nil {
		return Plan{}, err
	}
	// Prevent setup output from replacing its own metadata.
	if strings.Split(filepath.Clean(opts.Into), string(filepath.Separator))[0] == ".themis" || strings.Split(filepath.Clean(opts.Into), string(filepath.Separator))[0] == ".git" {
		return Plan{}, fmt.Errorf("reserved setup directory")
	}
	if opts.MaxNodes <= 0 {
		opts.MaxNodes = 200
	}
	nodes := []state.Node{target}
	if opts.Mode == "setup" {
		nodes, err = descendantNodes(st, target, opts.MaxNodes)
		if err != nil {
			return Plan{}, err
		}
	} else if !target.ChildrenHydrated {
		return Plan{}, fmt.Errorf("INCOMPLETE_CATALOG: refresh the target before planning")
	}
	if target.Kind != "assignment" {
		return Plan{}, fmt.Errorf("select an assignment or lab, not a course/year")
	}
	ctx, err := (projectlink.Service{WorkingDir: root, BaseURL: st.BaseURL}).Resolve()
	if err != nil {
		return Plan{}, err
	}
	course := catalog.CourseFor(st, target)
	if course == nil {
		return Plan{}, fmt.Errorf("target has no course identity")
	}
	if ctx.ConfigPath != "" && !catalog.Within(target.CanonicalURL, ctx.Config.LinkedRootURL) {
		return Plan{}, fmt.Errorf("PROJECT_MISMATCH: target lies outside this directory's linked scope")
	}
	p := Plan{Version: 1, Mode: opts.Mode, BaseURL: st.BaseURL, Root: root, TargetID: target.ID, Files: []File{}, Mappings: map[string]string{}, LinkURL: course.URL}
	p.ConfigPath = ctx.ConfigPath
	if p.ConfigPath == "" {
		p.ConfigPath = projectlink.ConfigPathFromRepoRoot(root)
	}
	configRoot := ctx.LinkDir
	if configRoot == "" {
		configRoot = root
	}
	if _, _, err := service.Bound(configRoot, p.ConfigPath); err != nil {
		return Plan{}, err
	}
	if hash, err := fileHash(p.ConfigPath); err == nil {
		p.ConfigBefore = hash
	} else if !os.IsNotExist(err) {
		return Plan{}, err
	}
	if _, _, err := service.Bound(root, filepath.Join(root, ".themis", "setup-manifest.json")); err != nil {
		return Plan{}, err
	}
	j, err := loadJournal(root)
	if err != nil {
		return Plan{}, err
	}
	sourceSet := map[string]state.Node{}
	used := map[string]bool{}
	seenDocuments := map[string]bool{}
	requested := map[string]bool{}
	for _, id := range opts.AssetIDs {
		requested[id] = false
	}
	for _, n := range nodes {
		directory := filepath.Clean(opts.Into)
		if n.ID != target.ID {
			tail := strings.TrimPrefix(n.CanonicalURL, strings.TrimRight(target.CanonicalURL, "/")+"/")
			directory = filepath.Join(directory, filepath.FromSlash(tail))
		}
		if explicit, ok := opts.Mappings[n.ID]; ok {
			directory = explicit
		}
		directory = filepath.Clean(directory)
		if filepath.IsAbs(directory) {
			return Plan{}, fmt.Errorf("assignment mappings must be relative")
		}
		if _, _, err := service.Bound(root, directory); err != nil {
			return Plan{}, err
		}
		if directory == ".themis" || strings.HasPrefix(directory, ".themis/") || directory == ".git" || strings.HasPrefix(directory, ".git/") {
			return Plan{}, fmt.Errorf("reserved assignment directory")
		}
		p.Mappings[n.ID] = directory
		sourceSet[n.ID] = n
		assets := catalog.Assets(st, n, n.ID == target.ID)
		chosen := []catalog.Asset{}
		refs := []state.AssetRef{}
		for _, a := range assets {
			if a.Kind == "document" && seenDocuments[a.URL] {
				continue
			}
			if a.Inherited && a.Kind != "document" && len(requested) == 0 {
				continue
			}
			if len(requested) > 0 {
				if _, ok := requested[a.ID]; !ok {
					continue
				}
				requested[a.ID] = true
			}
			if err := ValidateAsset(st.BaseURL, a.URL); err != nil {
				return Plan{}, err
			}
			if a.Kind == "document" {
				seenDocuments[a.URL] = true
			}
			chosen = append(chosen, a)
			refs = append(refs, a.Ref())
			sourceSet[a.SourceID] = st.Nodes[a.SourceID]
		}
		paths := discovery.AssetRelativePaths(refs)
		for i, a := range chosen {
			rel := filepath.Join(directory, paths[i])
			if used[rel] {
				return Plan{}, fmt.Errorf("two assets map to %s; supply explicit exercise mappings", rel)
			}
			used[rel] = true
			_, absolute, err := service.Bound(root, rel)
			if err != nil {
				return Plan{}, err
			}
			f := File{Asset: a, Destination: rel, Action: "create"}
			info, err := os.Stat(absolute)
			if err == nil {
				if !info.Mode().IsRegular() {
					return Plan{}, fmt.Errorf("destination is not a regular file: %s", rel)
				}
				f.BeforeSHA, err = fileHash(absolute)
				if err != nil {
					return Plan{}, err
				}
				previous, owned := j.Files[rel]
				if owned && previous.URL == a.URL && previous.SHA == f.BeforeSHA && (a.SHA256 == "" || strings.TrimPrefix(a.SHA256, "sha256:") == f.BeforeSHA) {
					f.Action = "skip"
				} else if opts.Overwrite {
					f.Action = "replace"
				} else {
					f.Action = "conflict"
				}
			} else if !os.IsNotExist(err) {
				return Plan{}, err
			}
			p.Files = append(p.Files, f)
			if len(p.Files) > 2000 {
				return Plan{}, fmt.Errorf("setup exceeds 2000 files; choose a smaller target or specific assets")
			}
		}
	}
	for id, found := range requested {
		if !found {
			return Plan{}, fmt.Errorf("asset %s does not belong to the selected target", id)
		}
	}
	for id := range opts.Mappings {
		if _, ok := p.Mappings[id]; !ok {
			return Plan{}, fmt.Errorf("mapping target %s is outside this setup", id)
		}
	}
	for _, n := range sourceSet {
		p.Sources = append(p.Sources, Source{n.ID, n.CanonicalURL, Fingerprint(n)})
	}
	sort.Slice(p.Sources, func(i, j int) bool { return p.Sources[i].ID < p.Sources[j].ID })
	sort.Slice(p.Files, func(i, j int) bool { return p.Files[i].Destination < p.Files[j].Destination })
	p.ID = p.Seal()
	return p, nil
}
func ValidateAsset(base, raw string) error {
	// Asset reads use the same instance and ordinary file/download routes.
	b, err := state.CanonicalizeURL(base)
	if err != nil {
		return err
	}
	u, err := state.CanonicalizeURL(raw)
	if err != nil {
		return err
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return err
	}
	if parsed.User != nil {
		return fmt.Errorf("credentials are not allowed in asset URLs")
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "." || segment == ".." || strings.Contains(segment, "\\") {
			return fmt.Errorf("unsafe asset URL path")
		}
	}
	if !strings.HasPrefix(u, strings.TrimRight(b, "/")+"/file/") && !strings.HasPrefix(u, strings.TrimRight(b, "/")+"/download/") {
		return fmt.Errorf("unsupported asset URL outside the instance's file/download routes")
	}
	return nil
}

func safeOutput(service workspace.Service, root, rel string) (string, error) {
	cleaned := filepath.Clean(rel)
	first := strings.Split(cleaned, string(filepath.Separator))[0]
	if filepath.IsAbs(rel) || cleaned == "." || first == ".themis" || first == ".git" {
		return "", fmt.Errorf("invalid setup output path")
	}
	_, path, err := service.Bound(root, cleaned)
	return path, err
}
func Apply(ctx context.Context, st state.State, p Plan, download Downloader) (Result, error) {
	result := Result{PlanID: p.ID, Items: []Item{}}
	if p.Version != 1 || p.ID != p.Seal() || p.BaseURL != st.BaseURL || (p.Mode != "setup" && p.Mode != "downloads") {
		return result, fmt.Errorf("PLAN_CHANGED: invalid plan or different Themis instance")
	}
	service := workspace.Service{}
	listing, err := service.List(p.Root, p.Root)
	if err != nil {
		return result, err
	}
	if listing.Root != p.Root {
		return result, fmt.Errorf("PLAN_CHANGED: workspace root moved")
	}
	target, ok := st.Nodes[p.TargetID]
	if !ok {
		return result, fmt.Errorf("PLAN_CHANGED: target no longer cached")
	}
	course := catalog.CourseFor(st, target)
	if course == nil || course.URL != p.LinkURL {
		return result, fmt.Errorf("PLAN_CHANGED: course identity changed")
	}
	for _, source := range p.Sources {
		n, ok := st.Nodes[source.ID]
		if !ok || n.CanonicalURL != source.URL || Fingerprint(n) != source.Fingerprint {
			return result, fmt.Errorf("PLAN_CHANGED: assignment metadata changed; create a new plan")
		}
	}
	link, err := (projectlink.Service{WorkingDir: p.Root, BaseURL: p.BaseURL}).Resolve()
	if err != nil {
		return result, err
	}
	configPath := link.ConfigPath
	if configPath == "" {
		configPath = projectlink.ConfigPathFromRepoRoot(p.Root)
	}
	if configPath != p.ConfigPath {
		return result, fmt.Errorf("PLAN_CHANGED: effective project link changed")
	}
	if link.ConfigPath != "" && !catalog.Within(target.CanonicalURL, link.Config.LinkedRootURL) {
		return result, fmt.Errorf("PROJECT_MISMATCH: effective project scope changed")
	}
	configRoot := link.LinkDir
	if configRoot == "" {
		configRoot = p.Root
	}
	if _, _, err := service.Bound(configRoot, configPath); err != nil {
		return result, err
	}
	// Validate every mapping/asset before creating directories or fetching data.
	for id, rel := range p.Mappings {
		n, ok := st.Nodes[id]
		if !ok || !catalog.Within(n.CanonicalURL, target.CanonicalURL) {
			return result, fmt.Errorf("invalid assignment mapping")
		}
		if rel == "." {
			continue
		}
		if _, err := safeOutput(service, p.Root, rel); err != nil {
			return result, err
		}
	}
	for _, file := range p.Files {
		if file.Action != "create" && file.Action != "replace" && file.Action != "skip" && file.Action != "conflict" {
			return result, fmt.Errorf("invalid plan action")
		}
		if _, err := safeOutput(service, p.Root, file.Destination); err != nil {
			return result, err
		}
		if err := ValidateAsset(p.BaseURL, file.Asset.URL); err != nil {
			return result, err
		}
		source, ok := st.Nodes[file.Asset.SourceID]
		if !ok || (!catalog.Within(source.CanonicalURL, target.CanonicalURL) && !catalog.Within(target.CanonicalURL, source.CanonicalURL)) {
			return result, fmt.Errorf("PLAN_CHANGED: missing or unrelated asset source")
		}
		found := false
		for _, asset := range catalog.Assets(st, source, false) {
			if asset.ID == file.Asset.ID && asset.URL == file.Asset.URL {
				found = true
				break
			}
		}
		if !found {
			return result, fmt.Errorf("PLAN_CHANGED: asset no longer belongs to the assignment")
		}
		if file.Action == "conflict" {
			return result, fmt.Errorf("DESTINATION_CONFLICT: existing file %s; choose another layout or explicitly plan replacement", file.Destination)
		}
	}
	metadata := filepath.Join(p.Root, ".themis")
	if _, _, err := service.Bound(p.Root, metadata); err != nil {
		return result, err
	}
	if err := os.MkdirAll(metadata, 0755); err != nil {
		return result, err
	}
	lockPath := filepath.Join(metadata, "setup.lock")
	if _, _, err := service.Bound(p.Root, lockPath); err != nil {
		return result, err
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return result, err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return result, fmt.Errorf("WORKSPACE_BUSY: another setup is running")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	manifest := filepath.Join(metadata, "setup-manifest.json")
	if _, _, err := service.Bound(p.Root, manifest); err != nil {
		return result, err
	}
	j, err := loadJournal(p.Root)
	if err != nil {
		return result, err
	}
	currentConfig, err := fileHash(configPath)
	if err != nil && !os.IsNotExist(err) {
		return result, err
	}
	if !j.Completed[p.ID] && currentConfig != p.ConfigBefore {
		return result, fmt.Errorf("PLAN_CHANGED: project configuration changed")
	}
	// Preflight all existing files before any download so predictable conflicts
	// do not cause a partially applied setup.
	for _, file := range p.Files {
		path, _ := safeOutput(service, p.Root, file.Destination)
		sha, err := fileHash(path)
		if err == nil {
			owned := j.Files[file.Destination]
			if owned.URL == file.Asset.URL && owned.SHA == sha {
				continue
			}
			if file.Action != "replace" || sha != file.BeforeSHA {
				return result, fmt.Errorf("DESTINATION_CONFLICT: %s changed since planning", file.Destination)
			}
		} else if !os.IsNotExist(err) {
			return result, err
		} else if file.Action == "replace" || file.Action == "skip" {
			return result, fmt.Errorf("PLAN_CHANGED: expected file disappeared: %s", file.Destination)
		}
	}
	for _, rel := range p.Mappings {
		path := p.Root
		var err error
		if rel != "." {
			path, err = safeOutput(service, p.Root, rel)
		}
		if err != nil {
			return result, err
		}
		if err := os.MkdirAll(path, 0755); err != nil {
			return result, err
		}
	}
	failed := false
	for _, file := range p.Files {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		path, err := safeOutput(service, p.Root, file.Destination)
		if err != nil {
			return result, err
		}
		sha, hashErr := fileHash(path)
		owned := j.Files[file.Destination]
		if hashErr == nil && owned.URL == file.Asset.URL && owned.SHA == sha {
			result.Items = append(result.Items, Item{Path: path, State: "skipped", SHA: sha})
			continue
		}
		item := Item{Path: path, State: "failed"}
		// Download into a private sibling directory. Only publish a complete file.
		tempDir, err := os.MkdirTemp(metadata, ".download-*")
		if err == nil {
			temp := filepath.Join(tempDir, "payload")
			err = download(ctx, file.Asset, temp)
			if err == nil {
				sha, err = fileHash(temp)
			}
			if err == nil && file.Asset.SHA256 != "" && strings.TrimPrefix(file.Asset.SHA256, "sha256:") != sha {
				err = fmt.Errorf("download checksum differs from published checksum")
			}
			if err == nil {
				if _, checkErr := safeOutput(service, p.Root, file.Destination); checkErr != nil {
					err = checkErr
				}
				if err == nil {
					err = os.MkdirAll(filepath.Dir(path), 0755)
				}
				if err == nil && file.Action == "replace" {
					current, checkErr := fileHash(path)
					if checkErr != nil || current != file.BeforeSHA {
						err = fmt.Errorf("destination changed during download")
					} else {
						err = os.Rename(temp, path)
					}
				} else if err == nil {
					err = os.Link(temp, path)
				} // exclusive publication; never clobber a new file
			}
			cleanupErr := os.RemoveAll(tempDir)
			if err == nil && cleanupErr != nil {
				err = cleanupErr
			}
		}
		if err != nil {
			item.Error = err.Error()
			failed = true
		} else {
			item.State = "downloaded"
			item.SHA = sha
			j.Files[file.Destination] = Owned{file.Asset.URL, sha}
			if err := Save(manifest, j); err != nil {
				result.Items = append(result.Items, item)
				return result, fmt.Errorf("PARTIAL_FAILURE: downloaded file but could not save its receipt: %w", err)
			}
		}
		result.Items = append(result.Items, item)
	}
	if failed {
		return result, fmt.Errorf("PARTIAL_FAILURE: some downloads failed; reapply the same plan to resume")
	}
	// Preserve existing link preferences, and save relative assignment mappings
	// against the effective link directory rather than the process cwd.
	latest, err := (projectlink.Service{WorkingDir: p.Root, BaseURL: p.BaseURL}).Resolve()
	if err != nil {
		return result, fmt.Errorf("PARTIAL_FAILURE: files ready but project context could not be rechecked: %w", err)
	}
	if latest.ConfigPath != link.ConfigPath || latest.Config.LinkedRootURL != link.Config.LinkedRootURL {
		return result, fmt.Errorf("PLAN_CHANGED: files are ready, but the project link changed during download; replan to save mappings")
	}
	cfg := latest.Config
	if link.ConfigPath == "" {
		cfg = projectlink.Config{BaseURL: p.BaseURL, LinkedRootURL: p.LinkURL, Preferences: projectlink.DefaultPreferences()}
		configRoot = p.Root
	}
	if cfg.DownloadDirs == nil {
		cfg.DownloadDirs = map[string]string{}
	}
	for id, rel := range p.Mappings {
		absolute := filepath.Join(p.Root, rel)
		mapping, err := filepath.Rel(configRoot, absolute)
		if err != nil {
			return result, err
		}
		cfg.DownloadDirs[id] = mapping
	}
	if err := projectlink.Save(configPath, cfg); err != nil {
		return result, fmt.Errorf("PARTIAL_FAILURE: files ready but saving project mappings failed: %w", err)
	}
	j.Completed[p.ID] = true
	if err := Save(manifest, j); err != nil {
		return result, err
	}
	result.Complete = true
	result.ProjectConfig = configPath
	return result, nil
}
