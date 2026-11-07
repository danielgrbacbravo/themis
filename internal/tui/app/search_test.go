package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"themis-cli/internal/state"
)

func searchFixture(t *testing.T) (Model, map[string]string) {
	t.Helper()
	st := state.NewEmptyState()
	st.BaseURL = "https://example.test"
	ids := map[string]string{}
	add := func(key, path, title, kind string) {
		id := seedNode(&st, st.BaseURL+"/course/"+path, title, kind)
		n := st.Nodes[id]
		n.ChildrenHydrated = true
		n.Status = state.StatusOK
		n.LastSuccessAt = func() *time.Time { v := time.Now(); return &v }()
		st.Nodes[id] = n
		ids[key] = id
	}
	add("old", "2025-2026/parcomp", "Parallel Computing", "course")
	add("pc", "2026-2027/parcomp", "Parallel Computing", "course")
	add("os", "2026-2027/os", "Operating Systems", "course")
	add("ads", "2026-2027/ads", "Algorithms and Data Structures (for CS)", "course")
	add("vector", "2026-2027/parcomp/lab1/vector_sum", "Exercise 1: Vëctor—Sum", "assignment")
	add("reduce", "2026-2027/parcomp/lab1/reduce", "Exercise 2: Reductions", "assignment")
	add("oldvector", "2025-2026/parcomp/lab1/vector_sum", "Exercise 1: Vëctor—Sum", "assignment")
	for _, key := range []string{"vector", "reduce", "oldvector"} {
		n := st.Nodes[ids[key]]
		verdict := "passed"
		if key == "reduce" {
			verdict = "failed"
		}
		n.Details = map[string]any{"stats": map[string]any{"summary": map[string]any{"status": verdict}}}
		st.Nodes[n.ID] = n
	}
	m, err := NewModel(Config{State: st, RootNodeID: ids["pc"], DisableAutoRefresh: true, ReducedMotion: true})
	if err != nil {
		t.Fatal(err)
	}
	m = resize(m, 100, 30)
	m.beginSearch()
	return m, ids
}
func TestSearchNormalizesCourseAcronymsAndMetadata(t *testing.T) {
	m, ids := searchFixture(t)
	cases := []struct {
		query string
		want  []string
	}{
		{"PC", []string{"pc", "old", "vector", "reduce", "oldvector"}},
		{"P.C.", []string{"pc", "old", "vector", "reduce", "oldvector"}},
		{"course:pc type:course", []string{"pc", "old"}},
		{"course:pc year:26/27 type:exercise status:passed", []string{"vector"}},
		{"pc status:failed", []string{"reduce"}},
		{"ADS", []string{"ads"}},
		{"os", []string{"os"}},
		{"vector_sum", []string{"vector", "oldvector"}},
		{"VÉCTOR SUM", []string{"vector", "oldvector"}},
		{"vectrsum", []string{"vector", "oldvector"}},
		{"pc lab1 2", []string{"reduce"}},
		{"pc year:2025-26 vector", []string{"oldvector"}},
		{"type:course year:2026", []string{"ads", "os", "pc", "old"}}, // year endpoints are searchable
		{"title:PC", nil},
		{"not-a-real-course-xyz", nil},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			m.browser.query = tc.query
			entries := m.browserEntries()
			want := map[string]bool{}
			for _, key := range tc.want {
				want[ids[key]] = true
			}
			if len(entries) != len(want) {
				t.Fatalf("query %q: got %+v, want %v", tc.query, entries, tc.want)
			}
			for _, entry := range entries {
				if !want[entry.id] {
					t.Fatalf("unexpected %s", entry.label)
				}
			}
		})
	}
	if m.st.Nodes[ids["vector"]].Title != "Exercise 1: Vëctor—Sum" {
		t.Fatal("search rewrote an official title")
	}
}
func TestSearchRanksRelevanceThenNewerYears(t *testing.T) {
	m, ids := searchFixture(t)
	for _, query := range []string{"PC", "parallel computing", "vector sum"} {
		m.browser.query = query
		entries := m.browserEntries()
		want := ids["pc"]
		if query == "vector sum" {
			want = ids["vector"]
		}
		if len(entries) == 0 || entries[0].id != want {
			t.Fatalf("query %s: newest equivalent match not first: %+v", query, entries)
		}
	}
	// Relevance beats year: an exact older exercise title should outrank a new
	// course that only includes the same letters as a loose subsequence.
	old := m.st.Nodes[ids["oldvector"]]
	old.Title = "Reduction"
	m.st.Nodes[old.ID] = old
	newer := m.st.Nodes[ids["pc"]]
	newer.Title = "Reduced instruction"
	m.st.Nodes[newer.ID] = newer
	m.browser.searchDocuments = nil
	m.browser.query = "reduction"
	entries := m.browserEntries()
	if len(entries) == 0 || entries[0].id != old.ID {
		t.Fatalf("year outranked relevance: %+v", entries)
	}
}
func TestCatalogYearsNewestFirst(t *testing.T) {
	m, _ := searchFixture(t)
	catalog := m.catalogID()
	root := m.st.Nodes[catalog]
	for _, year := range []string{"2024-2025", "2026-2027", "2025-2026"} {
		root.ChildIDs = append(root.ChildIDs, seedNode(&m.st, m.st.BaseURL+"/course/"+year, year, "year"))
	}
	m.st.Nodes[catalog] = root
	m.browser.overlay = "catalog"
	m.browser.container = catalog
	entries := m.browserEntries()
	if len(entries) != 3 || m.st.Nodes[entries[0].id].Title != "2026-2027" {
		t.Fatalf("years not newest first: %+v", entries)
	}
}
func TestSearchEditingAndEscapePreserveBrowsing(t *testing.T) {
	m, _ := searchFixture(t)
	selected, root := m.selectedNodeID, m.rootNodeID
	m, _ = browserKey(m, "pcx")
	m, _ = browserKey(m, "backspace")
	if m.browser.query != "pc" {
		t.Fatalf("backspace: %q", m.browser.query)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = next.(Model)
	m, _ = browserKey(m, "a")
	if m.browser.query != "pac" {
		t.Fatalf("mid-query editing failed: %q", m.browser.query)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = next.(Model)
	// Ctrl+U removes before the insertion point, preserving the suffix.
	if m.browser.query != "c" {
		t.Fatalf("unexpected editing semantics: %q", m.browser.query)
	}
	m, _ = browserKey(m, "q")
	if m.browser.overlay != "search" {
		t.Fatal("typing q quit search")
	}
	m, _ = browserKey(m, "esc")
	if m.browser.overlay != "" || m.selectedNodeID != selected || m.rootNodeID != root {
		t.Fatal("escape changed browsing context")
	}
}
func TestBrowserSurfaceKeepsHierarchyAndExitVisible(t *testing.T) {
	m, _ := searchFixture(t)
	m.browser.query = "pc"
	for _, overlay := range []string{"search", "catalog", "recent", "project"} {
		m.browser.overlay = overlay
		for _, size := range [][2]int{{100, 30}, {50, 16}, {24, 8}} {
			m = resize(m, size[0], size[1])
			view := m.View()
			assertFits(t, view, size[0], size[1])
			if strings.Contains(view, "Course Summary") {
				t.Fatal("unrelated course summary competes with focused browser task")
			}
			if !strings.Contains(view, "Esc") {
				t.Fatalf("exit hidden for %s at %v", overlay, size)
			}
		}
	}
	m = resize(m, 100, 30)
	m.browser.overlay = "search"
	t.Logf("Search surface:\n%s", m.View())
	narrow := resize(m, 50, 16)
	t.Logf("Narrow search surface:\n%s", narrow.View())
	view := m.renderBrowser(98, 20)
	if !strings.Contains(view, "Parallel Computing") || !strings.Contains(view, "2026-2027") || strings.Contains(view, "Filters:") {
		t.Fatalf("missing result hierarchy:\n%s", view)
	}
	for _, clutter := range []string{"Not submitted", "Not loaded", "Cached", "Relevance first", "Linked:"} {
		if strings.Contains(m.View(), clutter) {
			t.Fatalf("redundant search content: %s", clutter)
		}
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyF1})
	m = updated.(Model)
	if !strings.Contains(m.renderBrowser(98, 20), "Filters:") {
		t.Fatal("filter help unavailable")
	}
	m.browser.query = "course:"
	view = m.renderBrowser(98, 20)
	if !strings.Contains(view, "Add a value") {
		t.Fatal("incomplete filter has no recovery hint")
	}
}
func TestSearchInputViewportShowsInsertionPoint(t *testing.T) {
	m, _ := searchFixture(t)
	m, _ = browserKey(m, strings.Repeat("verylong ", 20)+"suffix")
	m = resize(m, 30, 16)
	view := m.renderBrowser(28, 10)
	if !strings.Contains(view, "suffix") || lipgloss.Width(view) > 28 {
		t.Fatalf("query cursor clipped:\n%s", view)
	}
}
func BenchmarkRankedCatalogSearch(b *testing.B) {
	st := state.NewEmptyState()
	st.BaseURL = "https://example.test"
	seedNode(&st, st.BaseURL+"/course/2026-2027/pc", "Parallel Computing", "course")
	for i := 0; i < 2000; i++ {
		seedNode(&st, fmt.Sprintf("%s/course/2026-2027/pc/lab%d/task", st.BaseURL, i), fmt.Sprintf("Exercise %d: Vector sum", i), "assignment")
	}
	m, _ := NewModel(Config{State: st, DisableAutoRefresh: true})
	m.beginSearch()
	m.browser.query = "pc vector"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.browserEntries()
	}
}

func TestSearchShowsContextAndStatusOnlyWhereUseful(t *testing.T) {
	m, _ := searchFixture(t)
	m.browser.query = "course:pc year:26/27 type:exercise"
	view := m.renderSearch(98, 20)
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "Exercise 2: Reductions") && strings.Contains(line, "2026-2027") {
			t.Fatal("same course/year repeated on consecutive unselected rows")
		}
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	view = m.renderSearch(98, 20)
	found := false
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "Exercise 2: Reductions") {
			found = true
			if !strings.Contains(line, "2026-2027") {
				t.Fatal("selected result lost its year")
			}
		}
	}
	if !found || strings.Count(view, "Needs attention") != 1 || strings.Contains(view, "Passed") {
		t.Fatalf("selected status should appear exactly once:\n%s", view)
	}
}

func TestSearchPagingUsesTheVisiblePicker(t *testing.T) {
	m, _ := searchFixture(t)
	for i := 0; i < 60; i++ {
		seedNode(&m.st, fmt.Sprintf("%s/course/2026-2027/parcomp/task%d", m.st.BaseURL, i), fmt.Sprintf("Task %02d", i), "assignment")
	}
	m = resize(m, 160, 60)
	m.beginSearch()
	m.browser.query = "course:pc"
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = next.(Model)
	width, height := m.searchSize()
	want := m.searchListRows(width-2, height-2)
	if m.browser.cursor != want {
		t.Fatalf("page moved %d rows, expected %d", m.browser.cursor, want)
	}
	entries := m.browserEntries()
	title := browserTitle(m.st.Nodes[entries[m.browser.cursor].id])
	if !strings.Contains(m.View(), title) {
		t.Fatal("selected result scrolled out of the picker")
	}
	assertFits(t, m.View(), 160, 60)
}
