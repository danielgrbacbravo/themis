package app

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"themis-cli/internal/browserprefs"
	"themis-cli/internal/discovery"
	"themis-cli/internal/projectlink"
	"themis-cli/internal/state"
)

func browserKey(m Model, key string) (Model, tea.Cmd) {
	var msg tea.KeyMsg
	switch key {
	case "enter":
		msg.Type = tea.KeyEnter
	case "esc":
		msg.Type = tea.KeyEsc
	case "backspace":
		msg.Type = tea.KeyBackspace
	case "tab":
		msg.Type = tea.KeyTab
	case "right":
		msg.Type = tea.KeyRight
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}
func browserFixture(t *testing.T) Model {
	t.Helper()
	st := baseStateForTUI(time.Now())
	st.BaseURL = "https://themis.housing.rug.nl"
	st.CatalogRootURL = st.BaseURL + "/course"
	m, err := NewModel(Config{State: st, RootNodeID: "url:root", LinkedRootNodeID: "url:root", DefaultDownloadDir: "/workspace", ProjectContext: projectlink.Context{WorkingDir: "/workspace", LinkDir: "/workspace", ConfigPath: "/workspace/.themis/project.json"}})
	if err != nil {
		t.Fatal(err)
	}
	m.width = 100
	m.height = 30
	return m
}
func TestCatalogSearchBackAndLinkedNavigation(t *testing.T) {
	m := browserFixture(t)
	m.expanded["url:lab1"] = true
	m.treeOffset = 1
	m.selectedNodeID = "url:lab1"
	expanded := copyExpanded(m.expanded)
	m, _ = browserKey(m, "/")
	m, _ = browserKey(m, "Lab 2")
	if entries := m.browserEntries(); len(entries) != 1 || !strings.Contains(entries[0].label, "2025-2026") {
		t.Fatalf("search: %+v", entries)
	}
	m, _ = browserKey(m, "enter")
	if m.rootNodeID != "url:lab2" || m.defaultDownloadDir != "/workspace" || m.linkedRootNodeID != "url:root" {
		t.Fatal("browsing changed local association")
	}
	m, _ = browserKey(m, "backspace")
	if m.rootNodeID != "url:root" || m.selectedNodeID != "url:lab1" || m.expanded["url:lab1"] != expanded["url:lab1"] {
		t.Fatal("back failed to restore browsing state")
	}
	opened, _ := m.openLocation("url:lab2")
	m = opened.(Model)
	m, _ = browserKey(m, "p")
	if m.rootNodeID != "url:root" {
		t.Fatal("p cannot escape subtree")
	}
	m, _ = browserKey(m, "c")
	if m.browser.overlay != "catalog" {
		t.Fatal("missing picker")
	}
	m, _ = browserKey(m, "tab")
	if m.browser.overlay != "recent" || len(m.browserEntries()) == 0 {
		t.Fatal("missing recents")
	}
}
func TestEmptyCatalogLoadsShallowly(t *testing.T) {
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Courses</title></head><body><h3>Courses</h3><div class="subsec round shade ass-children"><ul class="round"><li><span class="ass-link"><a href="/course/2026-2027">2026-2027</a></span></li></ul></div></body></html>`))
	}))
	defer server.Close()
	st := state.NewEmptyState()
	st.BaseURL = server.URL
	m, err := NewModel(Config{State: st, RefreshExecutor: func(st state.State, req RefreshRequest) RefreshOutcome {
		if req.Depth != 0 {
			t.Errorf("catalog depth=%d", req.Depth)
		}
		_, err := discovery.NewService(server.URL).RefreshNode(server.Client(), &st, req.TargetURL, req.Depth)
		return RefreshOutcome{State: st, TargetNodeID: req.TargetNodeID, Err: err}
	}})
	if err != nil {
		t.Fatal(err)
	}
	next, cmd := m.Update(refreshOnOpenMsg{})
	m = next.(Model)
	if !m.refreshInFlight || cmd == nil {
		t.Fatal("no startup load")
	}
	// Call the executor directly: Update wraps commands with animation batches.
	out := m.refreshExecutor(m.st, RefreshRequest{TargetNodeID: m.rootNodeID, TargetURL: m.st.Nodes[m.rootNodeID].CanonicalURL})
	next, _ = m.Update(refreshFinishedMsg{Outcome: out})
	m = next.(Model)
	if len(paths) != 1 || paths[0] != "/course" || (!m.st.Nodes[m.rootNodeID].ChildrenHydrated || len(m.st.Nodes[m.rootNodeID].ChildIDs) != 1) {
		t.Fatalf("unexpected crawl %v", paths)
	}
}
func TestLazyFailureRetryAndRefreshPreservesNavigation(t *testing.T) {
	m := browserFixture(t)
	m.refreshExecutor = func(st state.State, r RefreshRequest) RefreshOutcome {
		return RefreshOutcome{TargetNodeID: r.TargetNodeID, Err: errors.New("offline")}
	}
	next, _ := m.loadBranch("url:lab1")
	m = next.(Model)
	if m.refreshTarget != "url:lab1" {
		t.Fatal("no lazy request")
	}
	out := m.refreshExecutor(m.st, RefreshRequest{TargetNodeID: "url:lab1"})
	next, _ = m.Update(refreshFinishedMsg{Outcome: out})
	m = next.(Model)
	if m.st.Nodes["url:lab1"].Status != state.StatusError {
		t.Fatal("failure not exposed")
	}
	next, _ = m.openLocation("url:lab2")
	m = next.(Model)
	// Complete the second request after navigating back; selection must remain restored.
	m = m.goBack()
	snapshot, _ := snapshotState(m.st)
	next, _ = m.Update(refreshFinishedMsg{Outcome: RefreshOutcome{State: snapshot, TargetNodeID: "url:lab2"}})
	m = next.(Model)
	if m.rootNodeID != "url:root" {
		t.Fatal("refresh changed root")
	}
	m.browser.overlay = "catalog"
	m.browser.container = "url:lab1"
	m, _ = browserKey(m, "r")
	if m.refreshTarget != "url:lab1" {
		t.Fatal("retry missing")
	}
}
func TestProjectFailureAndNarrowLayouts(t *testing.T) {
	m := browserFixture(t)
	m.browser.manageLink = func(projectlink.Change) (projectlink.Context, error) {
		return projectlink.Context{}, errors.New("read only")
	}
	m, _ = browserKey(m, "P")
	m, _ = browserKey(m, "l")
	if !m.browser.confirming || !strings.Contains(m.renderBrowser(90, 20), "/workspace/.themis/project.json") {
		t.Fatal("missing concrete review")
	}
	m, _ = browserKey(m, "enter")
	if !strings.Contains(m.statusText, "read only") || m.browser.project.LinkDir != "/workspace" {
		t.Fatal("failed mutation changed displayed association")
	}
	for _, overlay := range []string{"project", "catalog", "recent", "search"} {
		for _, size := range [][2]int{{100, 30}, {50, 16}, {24, 8}} {
			m.browser.overlay = overlay
			m.width = size[0]
			m.height = size[1]
			view := m.View()
			if lipgloss.Height(view) > size[1] || lipgloss.Width(view) > size[0] {
				t.Fatalf("overflow %s %v", overlay, size)
			}
		}
	}
}
func TestRecentPersistenceFailureAndDuplicateYears(t *testing.T) {
	m := browserFixture(t)
	id := seedNode(&m.st, "https://themis.housing.rug.nl/course/2024-2025/os", "Operating Systems", "course")
	m.browser.overlay = "search"
	m.browser.query = "Operating Systems"
	if len(m.browserEntries()) < 2 {
		t.Fatal("search hides same title in different years")
	}
	m.browser.savePreferences = func(browserprefs.Preferences) error { return errors.New("disk full") }
	next, _ := m.openLocation(id)
	m = next.(Model)
	if !strings.Contains(m.statusText, "disk full") || len(m.browser.preferences.Recent) != 0 {
		t.Fatal("persistence failure hidden")
	}
}

// Exercise the accepted workflow using the real discovery parser and link store.
func TestBrowseLinkExploreAndReturn(t *testing.T) {
	paths := map[string]string{
		"/course":              "/course/2026-2027",
		"/course/2026-2027":    "/course/2026-2027/os",
		"/course/2026-2027/os": "/course/2026-2027/os/lab1",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected mutation: %s", r.Method)
			http.Error(w, "read only", 405)
			return
		}
		child := paths[r.URL.Path]
		fmt.Fprintf(w, `<html><head><title>Assignment: %s - Themis</title></head><body><div class="subsec round shade ass-children"><ul class="round">`, filepath.Base(r.URL.Path))
		if child != "" {
			fmt.Fprintf(w, `<li><span class="ass-link"><a href="%s">%s</a></span></li>`, child, filepath.Base(child))
		}
		fmt.Fprint(w, `</ul></div></body></html>`)
	}))
	defer server.Close()
	dir := t.TempDir()
	svc := projectlink.Service{WorkingDir: dir, BaseURL: server.URL}
	ctx, err := svc.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	st := state.NewEmptyState()
	st.BaseURL = server.URL
	m, err := NewModel(Config{State: st, ProjectContext: ctx, ManageLink: svc.Apply, DefaultDownloadDir: dir, ReducedMotion: true, RefreshExecutor: func(st state.State, req RefreshRequest) RefreshOutcome {
		_, err := discovery.NewService(server.URL).RefreshNode(server.Client(), &st, req.TargetURL, 0)
		return RefreshOutcome{State: st, TargetNodeID: req.TargetNodeID, Err: err}
	}})
	if err != nil {
		t.Fatal(err)
	}
	finish := func() {
		t.Helper()
		if !m.refreshInFlight {
			return
		}
		id := m.refreshTarget
		out := m.refreshExecutor(m.st, RefreshRequest{TargetNodeID: id, TargetURL: m.st.Nodes[id].CanonicalURL})
		next, _ := m.Update(refreshFinishedMsg{Outcome: out})
		m = next.(Model)
	}
	next, _ := m.Update(refreshOnOpenMsg{})
	m = next.(Model)
	finish()
	m, _ = browserKey(m, "c")
	m, _ = browserKey(m, "enter")
	finish() // year
	m, _ = browserKey(m, "enter")
	finish() // course
	if m.st.Nodes[m.rootNodeID].Kind != "course" {
		t.Fatal("course did not open")
	}
	m, _ = browserKey(m, "j")
	m, _ = browserKey(m, "P")
	m, _ = browserKey(m, "l")
	m, _ = browserKey(m, "enter")
	linked := m.linkedRootNodeID
	if linked == "" || m.browser.project.LinkDir != dir {
		t.Fatalf("link failed: %s", m.statusText)
	}
	if _, err := os.Stat(projectlink.ConfigPathFromRepoRoot(dir)); err != nil {
		t.Fatal(err)
	}
	m, _ = browserKey(m, "esc")
	m, _ = browserKey(m, "c")
	m, _ = browserKey(m, "enter")
	finish()
	m, _ = browserKey(m, "enter")
	finish() // course, then return to lab link outside course root
	m, _ = browserKey(m, "p")
	finish()
	if m.rootNodeID != linked || m.defaultDownloadDir != dir {
		t.Fatal("failed to return to linked assignment with unchanged file destination")
	}
	for _, size := range [][2]int{{100, 30}, {50, 16}} {
		m = resize(m, size[0], size[1])
		assertFits(t, m.View(), size[0], size[1])
	}
}

func TestRefreshRetainsNewlyOpenedStub(t *testing.T) {
	m := browserFixture(t)
	snapshot, _ := snapshotState(m.st)
	m.refreshInFlight = true
	m.refreshTarget = m.rootNodeID
	id := seedNode(&m.st, "https://themis.housing.rug.nl/course/2026-2027/new", "New Course", "course")
	next, _ := m.openLocation(id)
	m = next.(Model)
	next, _ = m.Update(refreshFinishedMsg{Outcome: RefreshOutcome{State: snapshot, TargetNodeID: "url:root"}})
	m = next.(Model)
	if m.rootNodeID != id || m.selectedNodeID != id {
		t.Fatal("in-flight snapshot discarded newly opened location")
	}
}
