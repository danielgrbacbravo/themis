package app

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"themis-cli/internal/state"
)

func TestStartupRefreshUsesDisplayedRootAndPreservesNavigation(t *testing.T) {
	var request RefreshRequest
	m, err := NewModel(Config{State: baseStateForTUI(time.Now()), RefreshExecutor: func(st state.State, req RefreshRequest) RefreshOutcome {
		request = req
		return RefreshOutcome{State: st, Scope: req.Scope, TargetNodeID: req.TargetNodeID}
	}})
	if err != nil {
		t.Fatal(err)
	}
	m = resize(m, 80, 24)
	cached := m.View()
	if cached == "" {
		t.Fatal("cache unavailable before network work")
	}
	startup := workMessage(m.Init())
	m, _ = sendKey(m, "j")
	selected := m.selectedNodeID
	updated, cmd := m.Update(startup)
	m = updated.(Model)
	if !m.refreshInFlight || !m.animationRunning {
		t.Fatal("startup did not begin background activity")
	}
	result := workMessage(cmd)
	if request.TargetNodeID != "url:root" || request.Scope != RefreshScopeSubtree || request.Depth < 2 {
		t.Fatalf("wrong startup scope: %+v", request)
	}
	updated, _ = m.Update(result)
	m = updated.(Model)
	if m.selectedNodeID != selected {
		t.Fatal("startup refresh moved selection")
	}
	_, duplicate := m.Update(refreshOnOpenMsg{})
	if duplicate != nil {
		t.Fatal("startup refresh ran twice")
	}
}

func TestStartupRefreshCanBeDisabledAndCatalogIsShallow(t *testing.T) {
	exec := func(st state.State, req RefreshRequest) RefreshOutcome {
		return RefreshOutcome{State: st, Scope: req.Scope, TargetNodeID: req.TargetNodeID}
	}
	for _, cfg := range []Config{
		{State: baseStateForTUI(time.Now()), RefreshExecutor: exec, DisableAutoRefresh: true},
		{State: baseStateForTUI(time.Now())},
	} {
		m, err := NewModel(cfg)
		if err != nil {
			t.Fatal(err)
		}
		_, cmd := m.Update(refreshOnOpenMsg{})
		if m.autoRefreshPending || cmd != nil {
			t.Fatal("unexpected startup network request")
		}
	}
	st := baseStateForTUI(time.Now())
	root := st.Nodes["url:root"]
	root.Kind = "catalog"
	st.Nodes[root.ID] = root
	m, _ := NewModel(Config{State: st, RefreshExecutor: func(st state.State, req RefreshRequest) RefreshOutcome {
		if req.Scope != RefreshScopeNode || req.Depth != 0 {
			t.Fatal("startup attempted a catalog crawl")
		}
		return exec(st, req)
	}})
	_, cmd := m.Update(refreshOnOpenMsg{})
	_ = workMessage(cmd)
}

func TestAnimationTicksStopWhenIdleAndReducedMotionIsStatic(t *testing.T) {
	m := resize(stressModel(t), 80, 24)
	m.refreshInFlight = true
	updated, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	if !m.animationRunning || cmd == nil {
		t.Fatal("no activity timer")
	}
	generation := m.animationGeneration
	frame := m.animationFrame
	updated, _ = m.Update(activityTickMsg{generation: generation})
	m = updated.(Model)
	if m.animationFrame == frame {
		t.Fatal("activity did not animate")
	}
	if m.selectedNodeID != "url:root" || m.detailsOffset != 0 {
		t.Fatal("animation moved navigation")
	}
	m.refreshInFlight = false
	m.successUntil = time.Time{}
	updated, cmd = m.Update(activityTickMsg{generation: generation})
	m = updated.(Model)
	if m.animationRunning || cmd != nil {
		t.Fatal("animation kept ticking while idle")
	}
	m.reducedMotion = true
	m.refreshInFlight = true
	updated, cmd = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	if cmd != nil || m.animationRunning || !strings.Contains(m.activityIcon(), "↻") {
		t.Fatal("reduced motion scheduled animation")
	}
}

func TestDetailsHideTechnicalURLsUntilRequested(t *testing.T) {
	m := resize(stressModel(t), 100, 30)
	for id, node := range m.st.Nodes {
		if id != m.rootNodeID {
			node.Kind = "assignment"
			m.st.Nodes[id] = node
		}
	}
	view := m.renderDetails(48, 0)
	if strings.Contains(view, "https://") || strings.Contains(view, "Name:") || strings.Contains(view, "Freshness:") {
		t.Fatalf("details still dump raw metadata: %s", view)
	}
	for _, section := range []string{"PROGRESS", "FILES", "ABOUT"} {
		if !strings.Contains(view, section) {
			t.Fatalf("missing %s section", section)
		}
	}
	m, _ = sendKey(m, "i")
	if !m.detailsFocused || !m.showSource {
		t.Fatal("source toggle did not reveal details")
	}
	if !strings.Contains(m.renderDetails(120, 0), "https://") {
		t.Fatal("source URL is inaccessible")
	}
	m, _ = sendKey(m, "i")
	if m.showSource {
		t.Fatal("source did not close")
	}
}

func TestSummaryClockUpdatesDeadlineWithoutFetching(t *testing.T) {
	st := baseStateForTUI(time.Now())
	node := st.Nodes["url:lab1"]
	node.Details = map[string]any{"config": map[string]any{"deadline_iso": "2026-09-17T07:00:00Z"}}
	st.Nodes[node.ID] = node
	m, _ := NewModel(Config{State: st})
	updated, cmd := m.Update(summaryTickMsg(time.Date(2026, 9, 17, 8, 0, 0, 0, time.UTC)))
	m = updated.(Model)
	if len(m.courseSummary.OverdueAssignments) != 1 || cmd == nil {
		t.Fatal("summary clock did not advance deadline state")
	}
}

func TestAppUsesTerminalPaletteWithoutSelectionBackground(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(profile)
	m := resize(stressModel(t), 100, 30)
	view := m.View()
	if strings.Contains(view, "38;") || strings.Contains(view, "48;") {
		t.Fatal("app emitted fixed extended-palette or RGB colors")
	}
	// A row may contain independently styled icons. Reverse video or a
	// background on the enclosing row produces disjoint bands around them.
	for _, match := range regexp.MustCompile(`\x1b\[([0-9;]*)m`).FindAllStringSubmatch(view, -1) {
		for _, code := range strings.Split(match[1], ";") {
			n, _ := strconv.Atoi(code)
			if n == 7 || (n >= 40 && n <= 48) || (n >= 100 && n <= 107) {
				t.Fatalf("app emitted a selection background: %q", match[0])
			}
		}
	}
	m.selectedIndex = 1
	m.syncSelectedNodeID()
	tree := m.renderTreeForSize(60, 10)
	for _, line := range strings.Split(tree, "\n") {
		if strings.Contains(line, "> ") {
			if lipgloss.Width(line) >= 60 || !strings.Contains(line, "\x1b[1m") {
				t.Fatalf("selected row must be bold without full-width padding: %q", line)
			}
			return
		}
	}
	t.Fatal("selection pointer missing")
}

func TestDetailsKeepTextInDefaultForeground(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(profile)
	m := resize(stressModel(t), 100, 30)
	for _, line := range strings.Split(m.renderDetails(48, 0), "\n") {
		// Gray is allowed for decoration, but never for readable text.
		for _, span := range regexp.MustCompile(`\x1b\[90m([^\x1b]*)`).FindAllStringSubmatch(line, -1) {
			if strings.Trim(span[1], " ─↳·") != "" {
				t.Fatalf("detail text uses low-contrast muted foreground: %q", span[1])
			}
		}
	}
}
