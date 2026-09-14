package app

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"themis-cli/internal/state"
)

func baseStateForTUI(now time.Time) state.State {
	st := state.NewEmptyState()
	st.Nodes = map[string]state.Node{
		"url:root": {
			ID:           "url:root",
			Title:        "Operating Systems",
			Kind:         "course",
			CanonicalURL: "https://themis.housing.rug.nl/course/2025-2026/os",
			ChildIDs:     []string{"url:lab1", "url:lab2"},
			Assets: []state.AssetRef{
				{Name: "1.in", URL: "https://themis.housing.rug.nl/file/course/%40tests/1.in", Kind: "file"},
				{Name: "1.out", URL: "https://themis.housing.rug.nl/file/course/%40tests/1.out", Kind: "file"},
			},
			Status: state.StatusOK,
			LastSuccessAt: func() *time.Time {
				t := now
				return &t
			}(),
		},
		"url:lab1": {
			ID:           "url:lab1",
			Title:        "Lab 1",
			Kind:         "assignment",
			CanonicalURL: "https://themis.housing.rug.nl/course/2025-2026/os/lab1",
			ParentIDs:    []string{"url:root"},
			Status:       state.StatusOK,
			LastSuccessAt: func() *time.Time {
				t := now
				return &t
			}(),
		},
		"url:lab2": {
			ID:           "url:lab2",
			Title:        "Lab 2",
			Kind:         "assignment",
			CanonicalURL: "https://themis.housing.rug.nl/course/2025-2026/os/lab2",
			ParentIDs:    []string{"url:root"},
			Status:       state.StatusOK,
			LastSuccessAt: func() *time.Time {
				t := now
				return &t
			}(),
		},
	}
	st.Roots = []state.RootRef{{NodeID: "url:root", CanonicalURL: st.Nodes["url:root"].CanonicalURL, UpdatedAt: now}}
	return st
}

func TestNewModelAndNavigation(t *testing.T) {
	now := time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC)
	m, err := NewModel(Config{State: baseStateForTUI(now)})
	if err != nil {
		t.Fatalf("new model failed: %v", err)
	}
	if len(m.flat) != 3 {
		t.Fatalf("expected 3 visible rows, got %d", len(m.flat))
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(Model)
	if m.selectedNodeID == "url:root" {
		t.Fatalf("expected selection to move down")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m = updated.(Model)
	if m.selectedNodeID != "url:root" {
		t.Fatalf("expected selection to move to parent")
	}
}

func TestCollapseAndExpand(t *testing.T) {
	now := time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC)
	m, err := NewModel(Config{State: baseStateForTUI(now), RootNodeID: "url:root"})
	if err != nil {
		t.Fatalf("new model failed: %v", err)
	}
	if len(m.flat) != 3 {
		t.Fatalf("expected expanded root with 3 rows, got %d", len(m.flat))
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m = updated.(Model)
	if len(m.flat) != 1 {
		t.Fatalf("expected collapsed root with 1 row, got %d", len(m.flat))
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	m = updated.(Model)
	if len(m.flat) != 3 {
		t.Fatalf("expected expanded root with 3 rows after expand, got %d", len(m.flat))
	}
}

func TestRefreshKeyFlowAndJumpProjectRoot(t *testing.T) {
	now := time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC)
	executorCalled := false
	exec := func(st state.State, req RefreshRequest) RefreshOutcome {
		executorCalled = true
		node := st.Nodes[req.TargetNodeID]
		node.Title = node.Title + " *"
		st.Nodes[req.TargetNodeID] = node
		return RefreshOutcome{State: st, Scope: req.Scope, TargetNodeID: req.TargetNodeID, UpdatedNodes: 1, DurationMs: 5}
	}

	m, err := NewModel(Config{State: baseStateForTUI(now), LinkedRootNodeID: "url:root", RefreshExecutor: exec})
	if err != nil {
		t.Fatalf("new model failed: %v", err)
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("expected refresh command")
	}
	msg := workMessage(cmd)
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if !executorCalled {
		t.Fatalf("expected executor to run")
	}
	if m.st.Nodes["url:root"].Title != "Operating Systems *" {
		t.Fatalf("expected refreshed state applied")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	m = updated.(Model)
	if m.selectedNodeID != "url:root" {
		t.Fatalf("expected jump to linked root")
	}
}

func TestRefreshFailureStatus(t *testing.T) {
	now := time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC)
	exec := func(st state.State, req RefreshRequest) RefreshOutcome {
		return RefreshOutcome{State: st, Scope: req.Scope, TargetNodeID: req.TargetNodeID, Err: errors.New("boom")}
	}

	m, err := NewModel(Config{State: baseStateForTUI(now), RefreshExecutor: exec})
	if err != nil {
		t.Fatalf("new model failed: %v", err)
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("expected refresh command")
	}
	msg := workMessage(cmd)
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if m.statusText == "" || m.statusText == "Cached view (refresh actions enabled)" {
		t.Fatalf("expected failure status text")
	}
}

func TestDownloadFlow(t *testing.T) {
	now := time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC)
	exec := func(st state.State, req DownloadRequest) DownloadOutcome {
		return DownloadOutcome{
			NodeID:     req.NodeID,
			TargetDir:  req.TargetDir,
			Downloaded: len(req.Assets),
			DurationMs: 9,
			Files: []DownloadedFile{
				{Name: "1.in", URL: "https://themis.housing.rug.nl/file/course/%40tests/1.in", Path: "/tmp/tests/1.in"},
			},
		}
	}

	persisted := false
	persist := func(nodeID string, urls []string, targetDir string) error {
		persisted = true
		if nodeID != "url:root" || targetDir == "" || len(urls) == 0 {
			t.Fatalf("unexpected persist payload")
		}
		return nil
	}

	m, err := NewModel(Config{
		State:              baseStateForTUI(now),
		DownloadExecutor:   exec,
		DefaultDownloadDir: "/tmp/tests",
		PersistChoices:     persist,
	})
	if err != nil {
		t.Fatalf("new model failed: %v", err)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updated.(Model)
	if m.mode != "download" {
		t.Fatalf("expected download mode")
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("expected download command")
	}

	for cmd != nil {
		msg := workMessage(cmd)
		updated, next := m.Update(msg)
		m = updated.(Model)
		cmd = next
	}

	if m.mode != "download" {
		t.Fatalf("expected download mode to remain for progress summary")
	}
	if !persisted {
		t.Fatalf("expected persisted choices callback")
	}
}

func TestDownloadViewportKeepsHeaderAndCursorVisible(t *testing.T) {
	now := time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC)
	st := baseStateForTUI(now)
	root := st.Nodes["url:root"]
	root.Assets = make([]state.AssetRef, 0, 40)
	for i := 1; i <= 40; i++ {
		root.Assets = append(root.Assets, state.AssetRef{
			Name: fmt.Sprintf("%d.in", i),
			URL:  fmt.Sprintf("https://themis.housing.rug.nl/file/course/%%40tests/%d.in", i),
			Kind: "file",
		})
	}
	st.Nodes["url:root"] = root

	m, err := NewModel(Config{State: st})
	if err != nil {
		t.Fatalf("new model failed: %v", err)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updated.(Model)
	m.downloadCursor = 39
	m.adjustDownloadOffset()

	view := m.renderDetailsForSize(48, 14)
	if !strings.Contains(view, "Download") {
		t.Fatalf("expected download header visible")
	}
	if !strings.Contains(view, "> [x] ⇥  40.in") {
		t.Fatalf("expected cursor line for last item visible")
	}
}

func TestRenderDetails_ShowsStatsSummary(t *testing.T) {
	now := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
	st := baseStateForTUI(now)
	lab := st.Nodes["url:lab1"]
	lab.Details = map[string]any{
		"links": map[string]any{
			"status_page": "https://themis.housing.rug.nl/stats/2025-2026/os/lab1",
		},
		"stats": map[string]any{
			"status_page": "https://themis.housing.rug.nl/stats/2025-2026/os/lab1",
			"summary": map[string]any{
				"status": "passed",
				"grade":  "20.00",
				"group":  "D. Grbac Bravo",
			},
			"counts": map[string]any{
				"total":  8,
				"passed": 1,
			},
			"submission_refs": map[string]any{
				"leading": map[string]any{
					"title": "Exercise 1 / s5482585-7",
					"url":   "https://themis.housing.rug.nl/submission/2025-2026/os/lab1/s5482585-7",
				},
			},
		},
	}
	st.Nodes["url:lab1"] = lab

	m, err := NewModel(Config{State: st})
	if err != nil {
		t.Fatalf("new model failed: %v", err)
	}
	// Move selection from root to lab1.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(Model)

	m.showSource = true
	view := m.renderDetails(120, 0)
	assertContains := func(needle string) {
		if !strings.Contains(view, needle) {
			t.Fatalf("expected details view to contain %q\nview:\n%s", needle, view)
		}
	}

	assertContains("SUBMISSION")
	assertContains("https://themis.housing.rug.nl/stats/2025-2026/os/lab1")
	assertContains("Passed")
	assertContains("20.00")
	assertContains("D. Grbac Bravo")
	assertContains("1 / 8 passed")
	assertContains("Exercise 1 / s5482585-7")
}

func TestRenderDetails_ShowsStatsLoadHintWhenOnlyStatusPageExists(t *testing.T) {
	now := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
	st := baseStateForTUI(now)
	lab := st.Nodes["url:lab1"]
	lab.Details = map[string]any{
		"links": map[string]any{
			"status_page": "https://themis.housing.rug.nl/stats/2025-2026/os/lab1",
		},
	}
	st.Nodes["url:lab1"] = lab

	m, err := NewModel(Config{State: st})
	if err != nil {
		t.Fatalf("new model failed: %v", err)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(Model)

	m.showSource = true
	view := m.renderDetails(120, 0)
	if !strings.Contains(view, "Status page  https://themis.housing.rug.nl/stats/2025-2026/os/lab1") {
		t.Fatalf("expected status page in details view\nview:\n%s", view)
	}
	if strings.Contains(view, "SUBMISSION") {
		t.Fatalf("unexpected empty submission section in details view\nview:\n%s", view)
	}
}

func TestRenderTree_AssignmentResultLabelsAndFreshnessIndicator(t *testing.T) {
	now := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
	st := baseStateForTUI(now)

	root := st.Nodes["url:root"]
	root.ChildIDs = []string{"url:lab1", "url:lab2", "url:lab3", "url:lab4"}
	st.Nodes["url:root"] = root

	lab1 := st.Nodes["url:lab1"]
	lab1.Details = map[string]any{
		"links": map[string]any{"status_page": "https://themis.housing.rug.nl/stats/2025-2026/os/lab1"},
		"stats": map[string]any{"summary": map[string]any{"status": "passed"}},
	}
	lab1.Status = state.StatusOK
	st.Nodes["url:lab1"] = lab1

	lab2 := st.Nodes["url:lab2"]
	lab2.Details = map[string]any{
		"links": map[string]any{"status_page": "https://themis.housing.rug.nl/stats/2025-2026/os/lab2"},
		"stats": map[string]any{"summary": map[string]any{"status": "failed"}},
	}
	lab2.Status = state.StatusStale
	st.Nodes["url:lab2"] = lab2

	st.Nodes["url:lab3"] = state.Node{
		ID:           "url:lab3",
		Title:        "Lab 3",
		Kind:         "assignment",
		CanonicalURL: "https://themis.housing.rug.nl/course/2025-2026/os/lab3",
		ParentIDs:    []string{"url:root"},
		Status:       state.StatusOK,
		Details: map[string]any{
			"links": map[string]any{"status_page": "https://themis.housing.rug.nl/stats/2025-2026/os/lab3"},
		},
	}
	st.Nodes["url:lab4"] = state.Node{
		ID:           "url:lab4",
		Title:        "Lab 4",
		Kind:         "assignment",
		CanonicalURL: "https://themis.housing.rug.nl/course/2025-2026/os/lab4",
		ParentIDs:    []string{"url:root"},
		Status:       state.StatusOK,
		Details: map[string]any{
			"links": map[string]any{"status_page": "https://themis.housing.rug.nl/stats/2025-2026/os/lab4"},
			"stats": map[string]any{
				"summary": map[string]any{
					"grade": "17.50",
				},
			},
		},
	}

	m, err := NewModel(Config{State: st})
	if err != nil {
		t.Fatalf("new model failed: %v", err)
	}
	tree := m.renderTree()

	assertContains := func(needle string) {
		if !strings.Contains(tree, needle) {
			t.Fatalf("expected tree to contain %q\ntree:\n%s", needle, tree)
		}
	}
	assertContains("✓ Lab 1")
	assertContains("× Lab 2")
	if strings.Contains(tree, "(stale)") {
		t.Fatal("tree contains verbose cache tag")
	}
	assertContains("· Lab 3")
	assertContains("◇ Lab 4")
}

func TestRenderCourseSummarySeparateFromAssignmentStats(t *testing.T) {
	now := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
	m, err := NewModel(Config{State: baseStateForTUI(now)})
	if err != nil {
		t.Fatalf("new model failed: %v", err)
	}
	m.courseSummary = state.CourseSummary{
		TotalAssignments:     3,
		SubmittedAssignments: 1,
		PassedAssignments:    1,
		CompletionPercent:    33,
		UpcomingDeadlines: []state.DeadlineGroup{
			{
				DaysUntil: 4,
				Assignments: []state.AssignmentDeadline{
					{NodeID: "url:lab1", Title: "Lab 1", Submitted: true, Passed: true},
					{NodeID: "url:lab2", Title: "Lab 2"},
					{NodeID: "url:lab3", Title: "Lab 3"},
				},
			},
		},
	}

	view := m.renderCourseSummary(120, 5)
	assertContains := func(needle string) {
		if !strings.Contains(view, needle) {
			t.Fatalf("expected course summary to contain %q\nview:\n%s", needle, view)
		}
	}
	assertContains("Course Summary")
	assertContains("Submitted 1/3 (33%), passed 1")
	assertContains("4 days until deadline for these 3: Lab 1, Lab 2, Lab 3")
	if strings.Contains(view, "Stats:") {
		t.Fatalf("course summary should not render per-assignment stats label\nview:\n%s", view)
	}
}

func TestDownloadExcludesCachedSiteInfoAndRecentChoices(t *testing.T) {
	st := baseStateForTUI(time.Now())
	node := st.Nodes["url:root"]
	node.Assets = append(node.Assets,
		state.AssetRef{Name: "About Themis", URL: "https://themis.housing.rug.nl/about", Kind: "asset"},
		state.AssetRef{Name: "Privacy Policy", URL: "https://www.rug.nl/info/privacy", Kind: "asset"},
	)
	st.Nodes[node.ID] = node
	m, err := NewModel(Config{State: st, RecentAssetChoices: map[string][]string{node.ID: {node.Assets[0].URL, node.Assets[2].URL, node.Assets[3].URL}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.renderDetailsForSize(100, 30), "FILES · 2") {
		t.Fatal("file count includes site info")
	}
	m, _ = sendKey(m, "d")
	view := m.renderDetailsForSize(100, 30)
	if strings.Contains(view, "About Themis") || strings.Contains(view, "Privacy Policy") {
		t.Fatalf("site info visible in picker: %s", view)
	}
	if got := m.selectedAssetURLs(); len(got) != 1 || got[0] != node.Assets[0].URL {
		t.Fatalf("cached choices included site info: %v", got)
	}
	m, _ = sendKey(m, "a")
	if len(m.selectedAssets()) != 2 {
		t.Fatal("select all included site info")
	}
}

func TestCourseSummaryKeepsUpcomingDeadlineVisibleAlongsideOverdue(t *testing.T) {
	m, err := NewModel(Config{State: baseStateForTUI(time.Now())})
	if err != nil {
		t.Fatal(err)
	}
	m.courseSummary = state.CourseSummary{TotalAssignments: 11, SubmittedAssignments: 4, PassedAssignments: 4, CompletionPercent: 36,
		OverdueAssignments: []state.AssignmentDeadline{{Title: "Lab 1"}},
		UpcomingDeadlines:  []state.DeadlineGroup{{DaysUntil: 3, Assignments: []state.AssignmentDeadline{{Title: "Lab 2"}}}},
	}
	view := m.renderCourseSummary(78, 3)
	if !strings.Contains(view, "3 days until deadline") || !strings.Contains(view, "1 overdue") {
		t.Fatalf("upcoming or overdue deadline missing: %s", view)
	}
}

func TestConfigurationDistinguishesDeadlineFromSubmissionCutoff(t *testing.T) {
	details := map[string]any{"config": map[string]any{
		"deadline:_you_can_still_submit.": "Thu Sep 17 2026 09:00:00 GMT+0200\n2026-09-17T07:00:00.000Z",
		"end:_you_can_no_longer_submit.":  "Fri Sep 18 2026 09:00:00 GMT+0200\n2026-09-18T07:00:00.000Z",
	}}
	view := strings.Join(configSummaryLines(details), "\n")
	if !strings.Contains(view, "Deadline: Thu Sep 17") || !strings.Contains(view, "Submissions close: Fri Sep 18") {
		t.Fatalf("date labels missing: %s", view)
	}
	if strings.Contains(view, ".000Z") {
		t.Fatal("hidden tooltip duplicated in date display")
	}
}

func TestParentCompletionMatchesTreeDetailsAndSummary(t *testing.T) {
	st := baseStateForTUI(time.Now())
	root := st.Nodes["url:root"]
	root.Kind = "assignment"
	root.Details = map[string]any{"stats": map[string]any{"summary": map[string]any{"status": "not_submitted"}}}
	st.Nodes[root.ID] = root
	for _, id := range root.ChildIDs {
		node := st.Nodes[id]
		node.Details = map[string]any{"stats": map[string]any{"summary": map[string]any{"status": "passed"}}}
		st.Nodes[id] = node
	}
	m, err := NewModel(Config{State: st})
	if err != nil {
		t.Fatal(err)
	}
	if m.courseSummary.TotalAssignments != 2 || m.courseSummary.PassedAssignments != 2 {
		t.Fatalf("wrong leaf totals: %+v", m.courseSummary)
	}
	if !strings.Contains(m.renderTree(), "▾ ✓ Operating Systems") {
		t.Fatalf("parent completion absent from tree: %s", m.renderTree())
	}
	details := m.renderDetails(80, 0)
	if !strings.Contains(details, "Completed · all subtasks passed") || !strings.Contains(details, "2 / 2 passed") {
		t.Fatalf("parent details disagree: %s", details)
	}
	m, _ = sendKey(m, "h")
	if !strings.Contains(m.renderTree(), "▸ ✓ Operating Systems") {
		t.Fatal("collapsing children hid derived completion")
	}
	// Refreshing a child's result must also update its ancestors.
	leaf := st.Nodes["url:lab2"]
	leaf.Details = map[string]any{"stats": map[string]any{"summary": map[string]any{"status": "failed"}}}
	st.Nodes[leaf.ID] = leaf
	updated, _ := m.Update(refreshFinishedMsg{Outcome: RefreshOutcome{State: st, TargetNodeID: leaf.ID}})
	m = updated.(Model)
	if strings.Contains(m.renderTree(), "✓ Operating Systems") || strings.Contains(m.renderDetails(80, 0), "Completed ·") {
		t.Fatal("parent stayed complete after subtask failed")
	}
	if !strings.Contains(m.renderDetails(80, 0), "1 / 2 passed") {
		t.Fatal("parent progress excluded an extra task")
	}
}
