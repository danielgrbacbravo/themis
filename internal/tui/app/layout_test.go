package app

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"themis-cli/internal/state"
)

func stressModel(t *testing.T) Model {
	t.Helper()
	st := baseStateForTUI(time.Now())
	root := st.Nodes["url:root"]
	root.ChildIDs = nil
	root.Assets = nil
	root.Title = "Course 界 é 👩‍💻 " + strings.Repeat("long title ", 20)
	root.Details = map[string]any{"description": strings.Repeat("A long description with 界 and é.\n", 60)}
	root.LastError = strings.Repeat("server error\twith context ", 20)
	for i := 0; i < 80; i++ {
		id := fmt.Sprintf("node-%03d", i)
		root.ChildIDs = append(root.ChildIDs, id)
		root.Assets = append(root.Assets, state.AssetRef{Name: fmt.Sprintf("file-%03d.in", i), URL: "https://example.org/" + id, Kind: "file"})
		st.Nodes[id] = state.Node{ID: id, Title: fmt.Sprintf("Item %03d 界", i), ParentIDs: []string{root.ID}, Status: state.StatusOK}
	}
	st.Nodes[root.ID] = root
	m, err := NewModel(Config{State: st})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func sendKey(m Model, key string) (Model, tea.Cmd) {
	types := map[string]tea.KeyType{"enter": tea.KeyEnter, "esc": tea.KeyEsc, "tab": tea.KeyTab, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "home": tea.KeyHome, "end": tea.KeyEnd}
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	if typ, ok := types[key]; ok {
		msg = tea.KeyMsg{Type: typ}
	}
	updated, cmd := m.Update(msg)
	return updated.(Model), cmd
}

func resize(m Model, width, height int) Model {
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return updated.(Model)
}

func assertFits(t *testing.T, view string, width, height int) {
	t.Helper()
	if !utf8.ValidString(view) {
		t.Fatal("invalid UTF-8")
	}
	if width <= 0 || height <= 0 {
		if view != "" {
			t.Fatalf("expected empty view before valid size: %q", view)
		}
		return
	}
	if lipgloss.Width(view) > width || lipgloss.Height(view) > height {
		t.Fatalf("view %dx%d exceeds terminal %dx%d:\n%s", lipgloss.Width(view), lipgloss.Height(view), width, height, view)
	}
}

func TestViewsStayWithinTerminalAcrossResizes(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(oldProfile)
	m := stressModel(t)
	widths := []int{0, 1, 2, 10, 23, 24, 25, 39, 40, 71, 72, 73, 80, 119, 120, 180}
	heights := []int{0, 1, 2, 7, 8, 9, 11, 12, 13, 14, 17, 18, 19, 24, 40, 60}
	for _, mode := range []string{"tree", "details", "download", "help"} {
		m.mode = "browse"
		m.showHelp, m.detailsFocused = mode == "help", mode == "details"
		if mode == "download" {
			m.selectedIndex = 0
			m.syncSelectedNodeID()
			out, _ := m.openDownloadMode()
			m = out.(Model)
			m.downloadCursor = 79
		}
		if mode == "tree" {
			m.selectedIndex = len(m.flat) - 1
			m.syncSelectedNodeID()
		}
		for _, width := range widths {
			for _, height := range heights {
				m = resize(m, width, height)
				view := m.View()
				assertFits(t, view, width, height)
				if width >= minWidth && height >= minHeight {
					if lipgloss.Width(view) != width || lipgloss.Height(view) != height {
						t.Fatalf("%s did not fill %dx%d", mode, width, height)
					}
					l := m.layout()
					assertFits(t, renderPanel(m.renderDetailsForSize(l.detailsWidth-2, l.bodyHeight-2), l.detailsWidth, l.bodyHeight, false), l.detailsWidth, l.bodyHeight)
					if mode == "tree" && !strings.Contains(m.renderTreeForSize(l.treeWidth-2, l.bodyHeight-2), "> ") {
						t.Fatalf("lost tree cursor at %dx%d", width, height)
					}
					if mode == "download" && (!strings.Contains(m.renderDetailsForSize(l.detailsWidth-2, l.bodyHeight-2), "> [x]") || !strings.Contains(m.renderDetailsForSize(l.detailsWidth-2, l.bodyHeight-2), "file-079")) {
						t.Fatalf("lost download cursor at %dx%d", width, height)
					}
				}
			}
		}
	}
}

func TestTreeScrollAndSelectionSurviveShrinkingAndGrowing(t *testing.T) {
	m := resize(stressModel(t), 100, 24)
	m, _ = sendKey(m, "G")
	selected := m.selectedNodeID
	for _, size := range [][2]int{{80, 12}, {24, 8}, {5, 2}, {72, 18}, {180, 60}, {100, 24}} {
		m = resize(m, size[0], size[1])
		if m.selectedNodeID != selected {
			t.Fatal("resize changed selection")
		}
		if size[0] >= minWidth && size[1] >= minHeight {
			tree := m.renderTreeForSize(m.layout().treeWidth-2, m.panelContentHeight())
			if !strings.Contains(tree, "> ") {
				t.Fatal("selected row scrolled offscreen")
			}
		}
	}
	before := m.treeOffset
	m, _ = sendKey(m, "k")
	if m.treeOffset != before {
		t.Fatal("viewport jumped even though selection stayed visible")
	}
	m, _ = sendKey(m, "g")
	if m.treeOffset != 0 {
		t.Fatal("home did not restore top of tree")
	}
	m, _ = sendKey(m, "pgdown")
	if m.selectedIndex <= 1 {
		t.Fatal("page down did not move a page")
	}
	m, _ = sendKey(m, "h")
	if m.selectedNodeID != "url:root" || m.treeOffset != 0 {
		t.Fatal("moving to parent did not normalize selection and viewport")
	}
}

func TestDetailsScrollWrappedContentAndResetOnSelectionChange(t *testing.T) {
	m := resize(stressModel(t), 80, 18)
	m, _ = sendKey(m, "tab")
	m, _ = sendKey(m, "G")
	if m.detailsOffset == 0 {
		t.Fatal("details did not scroll")
	}
	details := m.renderDetailsForSize(38, m.panelContentHeight())
	if !strings.Contains(details, "description") {
		t.Fatalf("cannot reach long description: %s", details)
	}
	m = resize(m, 120, 40)
	assertFits(t, m.View(), 120, 40)
	m, _ = sendKey(m, "tab")
	m, _ = sendKey(m, "j")
	if m.detailsOffset != 0 {
		t.Fatal("new selection inherited old details scroll")
	}
}

func TestRefreshSnapshotIsIsolatedAndPreservesNavigation(t *testing.T) {
	m := resize(stressModel(t), 80, 24)
	started, done := make(chan struct{}), make(chan tea.Msg, 1)
	m.refreshExecutor = func(st state.State, req RefreshRequest) RefreshOutcome {
		close(started)
		for i := 0; i < 150; i++ {
			root := st.Nodes[req.TargetNodeID]
			root.Title = "Changed"
			root.Assets[0].Name = "changed.in"
			root.Details["description"] = fmt.Sprintf("refresh %d", i)
			st.Nodes[root.ID] = root
		}
		return RefreshOutcome{State: st, TargetNodeID: req.TargetNodeID, Scope: req.Scope}
	}
	m, cmd := sendKey(m, "r")
	if cmd == nil {
		t.Fatal("no refresh command")
	}
	go func() { done <- workMessage(cmd) }()
	<-started
	for i := 0; i < 30; i++ {
		_ = m.View()
	}
	if m.st.Nodes["url:root"].Title == "Changed" || m.st.Nodes["url:root"].Assets[0].Name == "changed.in" {
		t.Fatal("executor mutated live state")
	}
	m, _ = sendKey(m, "j")
	selected := m.selectedNodeID
	updated, _ := m.Update(<-done)
	m = updated.(Model)
	if m.selectedNodeID != selected {
		t.Fatal("refresh stole selection")
	}
	if m.st.Nodes["url:root"].Title != "Changed" {
		t.Fatal("refresh was not applied")
	}
}

func TestDownloadContinuesAfterClosingWithOriginalNodeAndSelection(t *testing.T) {
	m, err := NewModel(Config{State: baseStateForTUI(time.Now())})
	if err != nil {
		t.Fatal(err)
	}
	var requests []DownloadRequest
	m.downloadExecutor = func(st state.State, req DownloadRequest) DownloadOutcome {
		requests = append(requests, req)
		return DownloadOutcome{NodeID: req.NodeID, TargetDir: req.TargetDir, Downloaded: 1}
	}
	m.persistChoices = func(id string, urls []string, dir string) error {
		if id != "url:root" || len(urls) != 2 {
			t.Fatalf("persisted wrong batch: %s %v", id, urls)
		}
		return errors.New("disk full")
	}
	m = resize(m, 80, 24)
	m, _ = sendKey(m, "d")
	m, cmd := sendKey(m, "enter")
	if cmd == nil {
		t.Fatal("no download command")
	}
	for _, key := range []string{"c", "a", " ", "r", "f", "p", "l"} {
		m, _ = sendKey(m, key)
		if m.selectedNodeID != "url:root" || len(m.selectedAssets()) != 2 {
			t.Fatalf("key %q mutated active batch", key)
		}
	}
	m, _ = sendKey(m, "esc")
	m, _ = sendKey(m, "j")
	if !m.downloadInFlight || m.mode != "browse" {
		t.Fatal("closing panel canceled batch")
	}
	firstResult := workMessage(cmd)
	updated, next := m.Update(firstResult)
	m = updated.(Model)
	// Duplicate completion must not skip the second queued file.
	duplicate, ignored := m.Update(firstResult)
	if ignored != nil || duplicate.(Model).downloadCurrent != 1 {
		t.Fatal("duplicate result advanced batch")
	}
	if next == nil {
		t.Fatal("closing panel dropped remaining files")
	}
	m, _ = sendKey(m, "d")
	if m.mode != "download" || m.selectedNodeID != "url:root" {
		t.Fatal("could not reopen original batch")
	}
	updated, next = m.Update(workMessage(next))
	m = updated.(Model)
	if next != nil || m.downloadInFlight || len(requests) != 2 {
		t.Fatal("batch did not complete")
	}
	for _, req := range requests {
		if req.NodeID != "url:root" {
			t.Fatal("navigation changed download node")
		}
	}
	if !strings.Contains(m.statusText, "saving choices failed: disk full") {
		t.Fatal("persistence error was swallowed")
	}
	// A stale message from a previous batch cannot affect a new batch.
	m, cmd = sendKey(m, "enter")
	if cmd == nil {
		t.Fatal("cannot start next batch")
	}
	updated, ignored = m.Update(firstResult)
	if ignored != nil || updated.(Model).downloadCurrent != 0 {
		t.Fatal("stale completion affected new batch")
	}
}

func TestJumpFindsReachableNodeWithoutTrustingParentCycles(t *testing.T) {
	st := baseStateForTUI(time.Now())
	child := st.Nodes["url:lab1"]
	child.ParentIDs = []string{child.ID}
	child.ChildIDs = []string{"url:leaf", "url:root"}
	st.Nodes[child.ID] = child
	st.Nodes["url:leaf"] = state.Node{ID: "url:leaf", Title: "Leaf"}
	m, err := NewModel(Config{State: st, LinkedRootNodeID: "url:leaf"})
	if err != nil {
		t.Fatal(err)
	}
	m, _ = sendKey(m, "p")
	if m.selectedNodeID != "url:leaf" {
		t.Fatal("project jump did not expand/rebuild path")
	}
	if m.ensureVisible("missing") {
		t.Fatal("missing node was treated as reachable")
	}
}

func TestInvalidRefreshRetainsUsableState(t *testing.T) {
	m := resize(stressModel(t), 80, 24)
	selected := m.selectedNodeID
	updated, _ := m.Update(refreshFinishedMsg{Outcome: RefreshOutcome{State: state.NewEmptyState()}})
	m = updated.(Model)
	if m.selectedNodeID != selected || len(m.flat) == 0 {
		t.Fatal("invalid refresh replaced cached tree")
	}
	if !strings.Contains(m.statusText, "refresh failed") {
		t.Fatal("missing failure status")
	}
}

func TestTemporaryTinyTerminalPreservesDetailsScroll(t *testing.T) {
	m := resize(stressModel(t), 80, 24)
	m, _ = sendKey(m, "tab")
	m, _ = sendKey(m, "pgdown")
	offset := m.detailsOffset
	for _, size := range [][2]int{{0, 0}, {1, 1}, {10, 3}, {80, 24}} {
		m = resize(m, size[0], size[1])
		if m.detailsOffset != offset {
			t.Fatal("temporary resize lost details position")
		}
	}
}

func TestHelpCanReachEveryKeyInSmallTerminal(t *testing.T) {
	m := resize(stressModel(t), 24, 8)
	m, _ = sendKey(m, "?")
	m, _ = sendKey(m, "G")
	if !strings.Contains(m.View(), "ctrl+c") {
		t.Fatal("cannot scroll to last help entry")
	}
	m, _ = sendKey(m, "g")
	if m.helpOffset != 0 {
		t.Fatal("help home did not return to beginning")
	}
	m, _ = sendKey(m, "esc")
	if m.showHelp {
		t.Fatal("escape did not close help")
	}
}

func TestDuplicateAssetURLsStillRejectDuplicateCompletions(t *testing.T) {
	m := stressModel(t)
	node := m.st.Nodes[m.rootNodeID]
	node.Assets = []state.AssetRef{node.Assets[0], node.Assets[0]}
	m.st.Nodes[node.ID] = node
	m.downloadExecutor = func(st state.State, req DownloadRequest) DownloadOutcome { return DownloadOutcome{Downloaded: 1} }
	m, _ = sendKey(m, "d")
	m, cmd := sendKey(m, "enter")
	result := workMessage(cmd)
	updated, next := m.Update(result)
	m = updated.(Model)
	if next == nil {
		t.Fatal("expected remaining asset")
	}
	updated, cmd = m.Update(result)
	if cmd != nil || updated.(Model).downloadCurrent != 1 {
		t.Fatal("duplicate completion advanced identical-URL queue")
	}
}

// Work commands may be batched with animation timers. Execute the actual work
// while animation timing is tested independently with explicit tick messages.
func workMessage(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			if result := workMessage(child); result != nil {
				return result
			}
		}
		return nil
	}
	switch msg.(type) {
	case activityTickMsg, summaryTickMsg:
		return nil
	}
	return msg
}
