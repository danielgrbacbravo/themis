package app

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"themis-cli/internal/browserprefs"
	"themis-cli/internal/state"
)

func TestCatalogPickerUsesBranchContextOnce(t *testing.T) {
	m, ids := searchFixture(t)
	year := seedNode(&m.st, m.st.BaseURL+"/course/2026-2027", "2026-2027", "year")
	node := m.st.Nodes[year]
	node.ChildrenHydrated = true
	node.ChildIDs = []string{ids["pc"], ids["os"], ids["ads"]}
	m.st.Nodes[year] = node
	m.browser.overlay = "catalog"
	m.browser.container = year
	for _, size := range [][2]int{{160, 60}, {100, 30}, {50, 16}, {24, 8}} {
		m = resize(m, size[0], size[1])
		view := m.View()
		assertFits(t, view, size[0], size[1])
		if !strings.Contains(view, "Esc") {
			t.Fatal("catalog lost its exit hint")
		}
		for _, clutter := range []string{"Cached", "Not loaded", "Not submitted", "Relevance first", "Linked:"} {
			if strings.Contains(view, clutter) {
				t.Fatalf("catalog contains repeated operational metadata: %s", clutter)
			}
		}
	}
	m = resize(m, 100, 30)
	t.Logf("Catalog picker:\n%s", m.View())
	entries := m.browserEntries()
	if len(entries) != 3 {
		t.Fatal("catalog entries changed")
	}
	for i := 0; i < 50; i++ {
		id := seedNode(&m.st, fmt.Sprintf("%s/course/2026-2027/course%d", m.st.BaseURL, i), fmt.Sprintf("Course %02d", i), "course")
		node.ChildIDs = append(node.ChildIDs, id)
	}
	m.st.Nodes[year] = node
	m = resize(m, 160, 60)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = next.(Model)
	w, h := m.searchSize()
	if m.browser.cursor != m.catalogListRows(w-2, h-2) {
		t.Fatal("catalog paging does not match visible rows")
	}
	selected := m.browserEntries()[m.browser.cursor]
	if !strings.Contains(m.View(), browserTitle(m.pickerNode(selected))) {
		t.Fatal("catalog selection scrolled out of view")
	}
}
func TestCatalogLoadStatesAndUncachedRecentsRemainVisible(t *testing.T) {
	m, _ := searchFixture(t)
	root := m.catalogID()
	m.browser.overlay = "catalog"
	m.browser.container = root
	for _, test := range []struct {
		status            state.Status
		hydrated, loading bool
		want              string
	}{
		{state.StatusNever, false, false, "Press r"},
		{state.StatusNever, false, true, "Loading this folder"},
		{state.StatusError, false, false, "retry"},
		{state.StatusOK, true, false, "empty"},
	} {
		node := m.st.Nodes[root]
		node.Status = test.status
		node.ChildrenHydrated = test.hydrated
		m.st.Nodes[root] = node
		m.refreshTarget = ""
		if test.loading {
			m.refreshTarget = root
		}
		if view := m.renderCatalog(80, 20); !strings.Contains(view, test.want) {
			t.Fatalf("missing %q:\n%s", test.want, view)
		}
	}
	m.browser.overlay = "recent"
	m.browser.preferences = browserprefs.Preferences{Recent: []browserprefs.Target{{URL: m.st.BaseURL + "/course/2026-2027/unknown", Title: "Previously opened course"}}}
	if view := m.renderCatalog(80, 20); !strings.Contains(view, "Previously opened course") || !strings.Contains(view, "Open to load details") {
		t.Fatalf("uncached recent lost its identity:\n%s", view)
	}
}
