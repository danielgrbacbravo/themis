// Package catalog exposes platform identities and search independently of the TUI.
package catalog

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"themis-cli/internal/state"
)

type Course struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	Code  string `json:"code"`
	Title string `json:"title"`
	Year  string `json:"year"`
}
type Target struct {
	ID                string         `json:"id"`
	URL               string         `json:"url"`
	Title             string         `json:"title"`
	Kind              string         `json:"kind"`
	Role              string         `json:"role"`
	Course            *Course        `json:"course,omitempty"`
	Parents           []string       `json:"parent_ids"`
	Children          []string       `json:"child_ids"`
	ChildCount        int            `json:"child_count"`
	ChildIDsTruncated bool           `json:"child_ids_truncated"`
	ChildrenLoaded    bool           `json:"children_loaded"`
	Grading           string         `json:"grading_state"`
	LastSuccess       *time.Time     `json:"last_success_at"`
	FetchState        state.Status   `json:"fetch_state"`
	LastError         string         `json:"last_error,omitempty"`
	Description       string         `json:"description"`
	Requirements      map[string]any `json:"requirements"`
}
type Asset struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Path      string `json:"path,omitempty"`
	Kind      string `json:"kind"`
	SourceID  string `json:"source_id"`
	SourceURL string `json:"source_url"`
	Inherited bool   `json:"inherited"`
	SHA256    string `json:"sha256,omitempty"`
	Size      int64  `json:"size_bytes,omitempty"`
}

func (a Asset) Ref() state.AssetRef {
	return state.AssetRef{Name: a.Name, URL: a.URL, Path: a.Path, Kind: a.Kind, SHA256: a.SHA256, SizeBytes: a.Size}
}
func GradingState(n state.Node) string {
	// A status-page link alone is not evidence that no submission exists.
	stats, _ := n.Details["stats"].(map[string]any)
	summary, _ := stats["summary"].(map[string]any)
	raw, _ := summary["status"].(string)
	if raw == "" {
		raw, _ = summary["status_text"].(string)
	}
	text := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(raw), " ", "_"))
	switch {
	case text == "accepted" || strings.HasPrefix(text, "passed"):
		return "passed"
	case strings.Contains(text, "failed") || strings.Contains(text, "failing") || strings.Contains(text, "error") || strings.Contains(text, "wrong") || strings.Contains(text, "rejected"):
		return "failed"
	case text == "not_submitted" || text == "never":
		return "not_submitted"
	case text != "":
		return text
	}
	return "unknown"
}
func CourseFor(st state.State, n state.Node) *Course {
	u, err := url.Parse(n.CanonicalURL)
	if err != nil {
		return nil
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "course" {
		return nil
	}
	u.Path = "/course/" + parts[1] + "/" + parts[2]
	u.RawPath = ""
	raw := u.String()
	id := state.NodeIDFromCanonicalURL(raw)
	title := parts[2]
	for _, parent := range st.Nodes {
		if strings.TrimRight(parent.CanonicalURL, "/") == raw {
			id = parent.ID
			title = parent.Title
			break
		}
	}
	return &Course{ID: id, URL: raw, Code: parts[2], Title: title, Year: parts[1]}
}
func Describe(st state.State, n state.Node) Target {
	role := n.Kind
	if n.Kind == "assignment" {
		role = "unknown"
		if len(n.ChildIDs) > 0 {
			role = "assignment_group"
		} else if n.ChildrenHydrated {
			role = "exercise"
		}
	}
	description, _ := n.Details["description"].(string)
	children := n.ChildIDs
	truncated := len(children) > 200
	if truncated {
		children = children[:200]
	}
	return Target{ID: n.ID, URL: n.CanonicalURL, Title: n.Title, Kind: n.Kind, Role: role, Course: CourseFor(st, n), Parents: append([]string{}, n.ParentIDs...), Children: append([]string{}, children...), ChildCount: len(n.ChildIDs), ChildIDsTruncated: truncated, ChildrenLoaded: n.ChildrenHydrated, Grading: GradingState(n), LastSuccess: n.LastSuccessAt, FetchState: n.Status, LastError: n.LastError, Description: description, Requirements: state.AssignmentConfig(n.Details)}
}
func Label(st state.State, id string) string {
	n := st.Nodes[id]
	label := n.Title
	if c := CourseFor(st, n); c != nil {
		label += " · " + c.Year
		if n.Kind != "course" {
			label += " / " + c.Title
		}
	}
	return label
}
func Within(raw, scope string) bool {
	return scope == "" || raw == strings.TrimRight(scope, "/") || strings.HasPrefix(raw, strings.TrimRight(scope, "/")+"/")
}
func Resolve(st state.State, target string) (state.Node, error) {
	if n, ok := st.Nodes[target]; ok {
		return n, nil
	}
	canonical, err := state.CanonicalizeURL(target)
	if err != nil {
		return state.Node{}, fmt.Errorf("use a canonical target ID or URL returned by search")
	}
	for _, n := range st.Nodes {
		if n.CanonicalURL == canonical {
			return n, nil
		}
	}
	return state.Node{}, fmt.Errorf("target is not cached; refresh its URL first")
}
func Assets(st state.State, target state.Node, inherited bool) []Asset {
	nodes := []state.Node{target}
	if inherited {
		// URL ancestry also finds shared lab statements when parent graph edges were
		// not hydrated by an earlier targeted refresh.
		candidates := []state.Node{}
		for _, n := range st.Nodes {
			if n.ID != target.ID && Within(target.CanonicalURL, n.CanonicalURL) {
				candidates = append(candidates, n)
			}
		}
		sort.Slice(candidates, func(i, j int) bool { return len(candidates[i].CanonicalURL) > len(candidates[j].CanonicalURL) })
		nodes = append(nodes, candidates...)
	}
	result := []Asset{}
	seen := map[string]bool{}
	for _, n := range nodes {
		for _, a := range n.Assets {
			if a.URL == "" || state.IsSiteInfoURL(a.URL) || seen[a.URL] {
				continue
			}
			seen[a.URL] = true
			kind := "file"
			lower := strings.ToLower(a.URL)
			if strings.Contains(lower, "@tests/") || strings.Contains(lower, "%40tests/") {
				kind = "test"
			} else if strings.HasSuffix(strings.ToLower(a.Name), ".pdf") {
				kind = "document"
			}
			owner := n
			best := 0
			for _, candidate := range st.Nodes {
				filePrefix := strings.Replace(candidate.CanonicalURL, "/course/", "/file/", 1) + "/"
				if !Within(n.CanonicalURL, candidate.CanonicalURL) || !strings.HasPrefix(a.URL, filePrefix) || len(candidate.CanonicalURL) <= best {
					continue
				}
				for _, known := range candidate.Assets {
					if known.URL == a.URL {
						owner = candidate
						best = len(candidate.CanonicalURL)
						break
					}
				}
			}
			result = append(result, Asset{ID: "asset:" + strings.TrimPrefix(state.NodeIDFromCanonicalURL(a.URL), "url:"), Name: a.Name, URL: a.URL, Path: a.Path, Kind: kind, SourceID: owner.ID, SourceURL: owner.CanonicalURL, Inherited: owner.ID != target.ID, SHA256: a.SHA256, Size: a.SizeBytes})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

type Coverage struct {
	Scope     string `json:"scope"`
	Known     int    `json:"known_nodes"`
	Unloaded  int    `json:"unloaded_nodes"`
	Complete  bool   `json:"complete"`
	CacheOnly bool   `json:"cache_only"`
}

// MissingMaterialAncestors reports course/lab pages whose shared attachments
// have not been fetched. A complete exercise page alone cannot prove that all
// shared statements are known.
func MissingMaterialAncestors(st state.State, n state.Node) []string {
	missing := []string{}
	u, err := url.Parse(n.CanonicalURL)
	if err != nil {
		return missing
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for depth := 3; depth < len(parts); depth++ {
		parent := *u
		parent.Path = "/" + strings.Join(parts[:depth], "/")
		parent.RawPath = ""
		found := false
		for _, candidate := range st.Nodes {
			if candidate.CanonicalURL == parent.String() && candidate.ChildrenHydrated && candidate.Status != state.StatusError {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, parent.String())
		}
	}
	return missing
}

func CoverageFor(st state.State, scope string) Coverage {
	c := Coverage{Scope: scope, CacheOnly: true}
	found := false
	rootFound := false
	rootURL := scope
	if rootURL == "" {
		rootURL = st.CatalogRootURL
	}
	for _, n := range st.Nodes {
		if !Within(n.CanonicalURL, scope) {
			continue
		}
		found = true
		if n.CanonicalURL == strings.TrimRight(rootURL, "/") {
			rootFound = true
		}
		c.Known++
		missingChild := false
		for _, id := range n.ChildIDs {
			if child, ok := st.Nodes[id]; !ok || child.ID == n.ID {
				missingChild = true
			}
		}
		if !n.ChildrenHydrated || n.Status == state.StatusError || missingChild {
			c.Unloaded++
		}
	}
	c.Complete = found && rootFound && c.Unloaded == 0
	return c
}

// Summary keeps discovery responses small; show is the detailed inspection API.
type Summary struct {
	ID             string       `json:"id"`
	URL            string       `json:"url"`
	Title          string       `json:"title"`
	Kind           string       `json:"kind"`
	Role           string       `json:"role"`
	Course         *Course      `json:"course,omitempty"`
	ChildCount     int          `json:"child_count"`
	ChildrenLoaded bool         `json:"children_loaded"`
	Grading        string       `json:"grading_state"`
	FetchState     state.Status `json:"fetch_state"`
	LastSuccess    *time.Time   `json:"last_success_at"`
}

func Brief(st state.State, n state.Node) Summary {
	d := Describe(st, n)
	return Summary{ID: d.ID, URL: d.URL, Title: d.Title, Kind: d.Kind, Role: d.Role, Course: d.Course, ChildCount: len(n.ChildIDs), ChildrenLoaded: d.ChildrenLoaded, Grading: d.Grading, FetchState: d.FetchState, LastSuccess: d.LastSuccess}
}
