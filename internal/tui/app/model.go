package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"themis-cli/internal/state"
	"themis-cli/internal/tui/textutil"
	"themis-cli/internal/tui/theme"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(theme.Accent)
	// Secondary text keeps the terminal foreground: ANSI bright black can be
	// too faint on light themes. Reserve muted color for decorative marks.
	secondaryStyle = lipgloss.NewStyle()
	mutedStyle     = lipgloss.NewStyle().Foreground(theme.Muted)
	okStyle        = lipgloss.NewStyle().Foreground(theme.Success)
	staleStyle     = lipgloss.NewStyle().Foreground(theme.Warning)
	errorStyle     = lipgloss.NewStyle().Foreground(theme.Error)
	neverStyle     = lipgloss.NewStyle()
	passStyle      = lipgloss.NewStyle().Foreground(theme.Success)
	failStyle      = lipgloss.NewStyle().Foreground(theme.Error)
	noneStyle      = lipgloss.NewStyle()
	infoStyle      = lipgloss.NewStyle().Foreground(theme.Info)
	selectedStyle  = lipgloss.NewStyle().Bold(true)
)

type RefreshScope string

const (
	RefreshScopeNode    RefreshScope = "node"
	RefreshScopeSubtree RefreshScope = "subtree"
	RefreshScopeFull    RefreshScope = "full"
)

type RefreshRequest struct {
	Scope        RefreshScope
	TargetNodeID string
	TargetURL    string
	Depth        int
}

type RefreshOutcome struct {
	State        state.State
	Scope        RefreshScope
	TargetNodeID string
	UpdatedNodes int
	DurationMs   int64
	Warnings     []string
	Err          error
}

type RefreshExecutor func(st state.State, req RefreshRequest) RefreshOutcome

type DownloadRequest struct {
	NodeID    string
	TargetDir string
	Assets    []state.AssetRef
}

type DownloadedFile struct {
	Name string
	URL  string
	Path string
}

type DownloadOutcome struct {
	NodeID     string
	TargetDir  string
	Downloaded int
	Files      []DownloadedFile
	DurationMs int64
	Err        error
}

type DownloadExecutor func(st state.State, req DownloadRequest) DownloadOutcome
type PersistChoicesFunc func(nodeID string, assetURLs []string, targetDir string) error

type refreshFinishedMsg struct {
	Outcome RefreshOutcome
}

type downloadFileFinishedMsg struct {
	Index    int
	BatchID  uint64
	AssetURL string
	Outcome  DownloadOutcome
}

type Model struct {
	st                  state.State
	rootNodeID          string
	linkedRootNodeID    string
	subtreeRefreshDepth int
	refreshExecutor     RefreshExecutor
	downloadExecutor    DownloadExecutor
	preview             previewState
	previewExecutor     PreviewExecutor
	persistChoices      PersistChoicesFunc
	defaultDownloadDir  string
	recentAssetChoices  map[string][]string
	downloadSelection   map[string]bool
	downloadCursor      int
	downloadOffset      int
	downloadInFlight    bool
	downloadQueue       []state.AssetRef
	downloadTargetDir   string
	downloadCurrent     int
	downloadDone        int
	downloadFailed      int
	downloadStatus      map[string]string
	downloadErrorByURL  map[string]string
	downloadStartedAt   time.Time
	downloadNodeID      string
	downloadState       state.State
	downloadBatchID     uint64
	courseSummary       state.CourseSummary
	refreshInFlight     bool
	expanded            map[string]bool
	flat                []treeRow
	selectedIndex       int
	treeOffset          int
	detailsOffset       int
	detailsFocused      bool
	showHelp            bool
	helpOffset          int
	selectedNodeID      string
	width               int
	height              int
	mode                string
	filter              string
	statusText          string
	autoRefreshPending  bool
	reducedMotion       bool
	animationFrame      int
	animationGeneration uint64
	animationRunning    bool
	successUntil        time.Time
	showSource          bool
	now                 time.Time
}

type treeRow struct {
	NodeID      string
	Depth       int
	Title       string
	URL         string
	Status      state.Status
	ResultLabel string
	HasChildren bool
	Expanded    bool
	ParentID    string
}

func NewModel(cfg Config) (Model, error) {
	st := cfg.State
	if len(st.Nodes) == 0 {
		return Model{}, fmt.Errorf("state has no nodes; run discovery first")
	}

	resolvedRootID := cfg.RootNodeID
	if resolvedRootID == "" {
		resolvedRootID = defaultRootNodeID(st)
	}
	if resolvedRootID == "" {
		return Model{}, fmt.Errorf("could not resolve root node for tui")
	}
	if _, ok := st.Nodes[resolvedRootID]; !ok {
		return Model{}, fmt.Errorf("resolved root node %s not found in state", resolvedRootID)
	}

	now := time.Now().UTC()
	_, _ = state.ApplyStateStalePolicy(&st, now, 2*time.Hour)

	depth := cfg.SubtreeRefreshDepth
	if depth <= 0 {
		depth = 1
	}

	m := Model{
		st:                  st,
		autoRefreshPending:  !cfg.DisableAutoRefresh && cfg.RefreshExecutor != nil,
		reducedMotion:       cfg.ReducedMotion,
		now:                 now,
		rootNodeID:          resolvedRootID,
		linkedRootNodeID:    strings.TrimSpace(cfg.LinkedRootNodeID),
		subtreeRefreshDepth: depth,
		refreshExecutor:     cfg.RefreshExecutor,
		downloadExecutor:    cfg.DownloadExecutor,
		previewExecutor:     cfg.PreviewExecutor,
		persistChoices:      cfg.PersistChoices,
		defaultDownloadDir:  strings.TrimSpace(cfg.DefaultDownloadDir),
		recentAssetChoices:  cloneChoiceMap(cfg.RecentAssetChoices),
		downloadSelection:   map[string]bool{},
		downloadQueue:       []state.AssetRef{},
		downloadStatus:      map[string]string{},
		downloadErrorByURL:  map[string]string{},
		downloadOffset:      0,
		courseSummary:       state.ComputeCourseSummary(st, resolvedRootID, now),
		expanded: map[string]bool{
			resolvedRootID: true,
		},
		selectedNodeID: resolvedRootID,
		selectedIndex:  0,
		width:          0,
		height:         0,
		mode:           "browse",
		filter:         "",
		statusText:     "Cached course · r to refresh",
	}
	m.rebuildFlat()
	return m, nil
}

func (m Model) Init() tea.Cmd {
	if m.autoRefreshPending {
		return tea.Batch(func() tea.Msg { return refreshOnOpenMsg{} }, summaryTick())
	}
	return summaryTick()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Normalize viewports after every transition, including asynchronous results.
	updated, cmd := m.update(msg)
	next := updated.(Model)
	if next.selectedNodeID != m.selectedNodeID {
		next.detailsOffset = 0
	}
	if _, tick := msg.(activityTickMsg); !tick {
		next.adjustViewports()
	}
	if previewCmd := next.syncPreview(m.mode); previewCmd != nil {
		cmd = tea.Batch(cmd, previewCmd)
	}
	if !next.reducedMotion && next.animating() && !next.animationRunning {
		next.animationRunning = true
		next.animationGeneration++
		cmd = tea.Batch(cmd, activityTick(next.animationGeneration))
	}
	return next, cmd
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case previewStartMsg:
		return m.startPreview(msg)
	case previewFinishedMsg:
		return m.finishPreview(msg)
	case refreshOnOpenMsg:
		if !m.autoRefreshPending {
			return m, nil
		}
		m.autoRefreshPending = false
		if m.refreshInFlight || m.downloadInFlight || m.mode == "download" {
			return m, nil
		}
		scope, depth := RefreshScopeSubtree, maxInt(2, m.subtreeRefreshDepth)
		if kind := m.st.Nodes[m.rootNodeID].Kind; kind == "catalog" || kind == "year" {
			scope, depth = RefreshScopeNode, 0
		}
		return m.startRefreshAt(m.rootNodeID, scope, depth)
	case summaryTickMsg:
		m.now = time.Time(msg)
		m.courseSummary = state.ComputeCourseSummary(m.st, m.rootNodeID, m.now)
		return m, summaryTick()
	case activityTickMsg:
		if msg.generation != m.animationGeneration {
			return m, nil
		}
		m.now = time.Now()
		if m.reducedMotion || !m.animating() {
			m.animationRunning = false
			return m, nil
		}
		m.animationFrame = (m.animationFrame + 1) % 60
		return m, activityTick(m.animationGeneration)
	case tea.WindowSizeMsg:
		m.width = maxInt(0, msg.Width)
		m.height = maxInt(0, msg.Height)
		if m.mode == "download" {
			m.adjustDownloadOffset()
		}
		return m, nil
	case refreshFinishedMsg:
		m.refreshInFlight = false
		out := msg.Outcome
		if out.Err != nil {
			m.statusText = fmt.Sprintf("refresh failed (%s): %v", out.Scope, out.Err)
			return m, nil
		}
		if _, ok := out.State.Nodes[m.rootNodeID]; !ok {
			m.statusText = "refresh failed: refreshed state is missing the current root"
			return m, nil
		}
		m.now = time.Now()
		m.successUntil = m.now.Add(900 * time.Millisecond)
		m.st = out.State
		m.courseSummary = state.ComputeCourseSummary(m.st, m.rootNodeID, time.Now())
		// Preserve the user's current selection while a refresh is running.
		m.ensureVisible(m.selectedNodeID)
		m.rebuildFlat()
		m.statusText = fmt.Sprintf("Updated %d items · %.1fs", out.UpdatedNodes, float64(out.DurationMs)/1000)
		if len(out.Warnings) > 0 {
			m.statusText += fmt.Sprintf(" warnings=%d", len(out.Warnings))
		}
		return m, nil
	case downloadFileFinishedMsg:
		if !m.downloadInFlight || msg.BatchID != m.downloadBatchID || msg.Index != m.downloadCurrent || m.downloadCurrent >= len(m.downloadQueue) || msg.AssetURL != m.downloadQueue[m.downloadCurrent].URL {
			return m, nil
		}
		m.handleDownloadFileResult(msg.AssetURL, msg.Outcome)
		if m.downloadCurrent < len(m.downloadQueue) {
			return m, m.downloadNextCmd()
		}
		return m.finalizeDownloadBatch(), nil
	case tea.KeyMsg:
		key := msg.String()
		if key == "ctrl+c" || key == "q" {
			m.cancelPreview()
			return m, tea.Quit
		}
		if key == "?" {
			m.helpOffset = 0
			m.showHelp = !m.showHelp
			return m, nil
		}
		if m.showHelp {
			switch key {
			case "esc":
				m.showHelp = false
			case "j", "down":
				m.helpOffset++
			case "k", "up":
				m.helpOffset--
			case "pgdown":
				m.helpOffset += maxInt(1, m.panelContentHeight()-1)
			case "pgup":
				m.helpOffset -= maxInt(1, m.panelContentHeight()-1)
			case "g", "home":
				m.helpOffset = 0
			case "G", "end":
				m.helpOffset = len(m.helpLines())
			}
			return m, nil
		}
		if m.mode == "download" {
			return m.updateDownloadKey(key)
		}
		if key == "i" {
			m.showSource = !m.showSource
			m.detailsOffset = 0
			m.detailsFocused = true
			return m, nil
		}
		if key == "tab" || key == "shift+tab" {
			m.detailsFocused = !m.detailsFocused
			return m, nil
		}
		if key == "esc" {
			m.detailsFocused = false
			return m, nil
		}
		if m.detailsFocused {
			switch key {
			case "up", "k":
				m.detailsOffset--
			case "down", "j":
				m.detailsOffset++
			case "pgup":
				m.detailsOffset -= maxInt(1, m.panelContentHeight()-2)
			case "pgdown":
				m.detailsOffset += maxInt(1, m.panelContentHeight()-2)
			case "g", "home":
				m.detailsOffset = 0
			case "G", "end":
				m.detailsOffset = len(m.detailsLines(m.layout().detailsWidth - 2))
			}
			if key != "r" && key != "R" && key != "f" && key != "d" && key != "p" {
				return m, nil
			}
		}
		switch key {
		case "up", "k":
			m.selectedIndex--
			m.syncSelectedNodeID()
		case "down", "j":
			m.selectedIndex++
			m.syncSelectedNodeID()
		case "left", "h":
			m.collapseOrMoveToParent()
		case "right", "l", "enter":
			m.expandSelection()
		case "g", "home":
			m.selectedIndex = 0
			m.syncSelectedNodeID()
		case "G", "end":
			m.selectedIndex = len(m.flat) - 1
			m.syncSelectedNodeID()
		case "pgup", "pgdown":
			step := maxInt(1, m.panelContentHeight()-2)
			if key == "pgup" {
				step = -step
			}
			m.selectedIndex += step
			m.syncSelectedNodeID()
		case "p":
			if m.linkedRootNodeID == "" {
				m.statusText = "no linked project root configured"
				return m, nil
			}
			if !m.ensureVisible(m.linkedRootNodeID) {
				m.statusText = "linked project root not found in current tree"
				return m, nil
			}
			m.selectedNodeID = m.linkedRootNodeID
			m.rebuildFlat()
			m.detailsFocused = false
			m.statusText = "jumped to linked project root"
		case "d":
			return m.openDownloadMode()
		case "r":
			return m.startRefresh(RefreshScopeNode, 0)
		case "R":
			return m.startRefresh(RefreshScopeSubtree, m.subtreeRefreshDepth)
		case "f":
			return m.startRefresh(RefreshScopeFull, m.subtreeRefreshDepth)
		}

	}
	return m, nil
}

func (m Model) startRefresh(scope RefreshScope, depth int) (tea.Model, tea.Cmd) {
	return m.startRefreshAt(m.selectedNodeID, scope, depth)
}

func (m Model) startRefreshAt(nodeID string, scope RefreshScope, depth int) (tea.Model, tea.Cmd) {
	if m.downloadInFlight {
		m.statusText = "wait for the download to finish before refreshing"
		return m, nil
	}
	if m.refreshInFlight {
		m.statusText = "refresh already in progress"
		return m, nil
	}
	if m.refreshExecutor == nil {
		m.statusText = "refresh unavailable (read-only mode)"
		return m, nil
	}
	node, exists := m.st.Nodes[nodeID]
	if !exists {
		m.statusText = "no selected node"
		return m, nil
	}
	targetID := node.ID
	targetURL := node.CanonicalURL
	if scope == RefreshScopeFull {
		targetID = m.rootNodeID
		targetNode := m.st.Nodes[targetID]
		targetURL = targetNode.CanonicalURL
	}
	if depth < 0 {
		depth = 0
	}

	stSnapshot, err := snapshotState(m.st)
	if err != nil {
		m.statusText = fmt.Sprintf("refresh failed: %v", err)
		return m, nil
	}
	m.refreshInFlight = true
	m.statusText = "Updating " + displayTitle(node) + "…"
	exec := m.refreshExecutor
	req := RefreshRequest{
		Scope:        scope,
		TargetNodeID: targetID,
		TargetURL:    targetURL,
		Depth:        depth,
	}

	cmd := func() tea.Msg {
		out := exec(stSnapshot, req)
		return refreshFinishedMsg{Outcome: out}
	}
	return m, cmd
}

func (m Model) openDownloadMode() (tea.Model, tea.Cmd) {
	if m.refreshInFlight {
		m.statusText = "wait for the refresh to finish before choosing downloads"
		return m, nil
	}
	if m.downloadInFlight {
		m.ensureVisible(m.downloadNodeID)
		m.selectedNodeID = m.downloadNodeID
		m.rebuildFlat()
		m.mode = "download"
		if m.downloadCurrent < len(m.downloadQueue) {
			m.focusDownloadAsset(m.downloadQueue[m.downloadCurrent].URL)
		}
		return m, nil
	}
	assets := m.selectedNodeAssets()
	if len(assets) == 0 {
		m.statusText = "selected node has no assets"
		return m, nil
	}
	m.mode = "download"
	m.downloadCursor = 0
	m.downloadOffset = 0
	m.downloadSelection = map[string]bool{}
	m.resetDownloadProgress()

	if recent, ok := m.recentAssetChoices[m.selectedNodeID]; ok && len(recent) > 0 {
		for _, url := range recent {
			m.downloadSelection[url] = true
		}
	} else {
		for _, asset := range assets {
			m.downloadSelection[asset.URL] = true
		}
	}

	m.adjustDownloadOffset()
	m.statusText = fmt.Sprintf("download mode: %d assets selected", len(m.selectedAssetURLs()))
	return m, nil
}

func (m *Model) toggleDownloadSelectionAtCursor() {
	assets := m.selectedNodeAssets()
	if len(assets) == 0 {
		return
	}
	if m.downloadCursor < 0 {
		m.downloadCursor = 0
	}
	if m.downloadCursor >= len(assets) {
		m.downloadCursor = len(assets) - 1
	}
	asset := assets[m.downloadCursor]
	if m.downloadSelection[asset.URL] {
		delete(m.downloadSelection, asset.URL)
	} else {
		m.downloadSelection[asset.URL] = true
	}
}

func (m Model) startDownload() (tea.Model, tea.Cmd) {
	if m.downloadInFlight {
		m.statusText = "download already in progress"
		return m, nil
	}
	if m.downloadExecutor == nil {
		m.statusText = "download unavailable"
		return m, nil
	}
	node := m.selectedNode()
	if node == nil {
		m.statusText = "no selected node"
		return m, nil
	}

	selected := m.selectedAssets()
	if len(selected) == 0 {
		m.statusText = "no assets selected"
		return m, nil
	}

	targetDir := m.defaultDownloadDir
	if strings.TrimSpace(targetDir) == "" {
		targetDir = filepath.Join(".", "tests")
	}

	snapshot, err := snapshotState(m.st)
	if err != nil {
		m.statusText = fmt.Sprintf("download failed: %v", err)
		return m, nil
	}
	m.downloadState = snapshot
	m.downloadNodeID = node.ID
	m.downloadBatchID++
	m.downloadInFlight = true
	m.downloadQueue = append([]state.AssetRef{}, selected...)
	m.downloadTargetDir = targetDir
	m.downloadCurrent = 0
	m.downloadDone = 0
	m.downloadFailed = 0
	m.downloadStartedAt = time.Now()
	m.downloadStatus = map[string]string{}
	m.downloadErrorByURL = map[string]string{}
	for _, a := range m.downloadQueue {
		m.downloadStatus[a.URL] = "pending"
	}
	if len(m.downloadQueue) > 0 {
		m.downloadStatus[m.downloadQueue[0].URL] = "active"
	}
	m.statusText = fmt.Sprintf("downloading 0/%d to %s", len(selected), targetDir)
	return m, m.downloadNextCmd()
}

func (m Model) selectedNodeAssets() []state.AssetRef {
	node := m.selectedNode()
	if node == nil {
		return []state.AssetRef{}
	}
	assets := make([]state.AssetRef, 0, len(node.Assets))
	for _, asset := range node.Assets {
		if !state.IsSiteInfoURL(asset.URL) {
			assets = append(assets, asset)
		}
	}
	return assets
}

func (m Model) selectedAssets() []state.AssetRef {
	all := m.selectedNodeAssets()
	out := make([]state.AssetRef, 0, len(all))
	for _, asset := range all {
		if m.downloadSelection[asset.URL] {
			out = append(out, asset)
		}
	}
	return out
}

func (m Model) selectedAssetURLs() []string {
	selected := m.selectedAssets()
	out := make([]string, 0, len(selected))
	for _, asset := range selected {
		out = append(out, asset.URL)
	}
	return out
}

func (m *Model) rebuildFlat() {
	rows := make([]treeRow, 0)
	visited := map[string]bool{}

	var walk func(nodeID string, parentID string, depth int)
	walk = func(nodeID string, parentID string, depth int) {
		node, ok := m.st.Nodes[nodeID]
		if !ok {
			return
		}
		row := treeRow{
			NodeID:      nodeID,
			Depth:       depth,
			Title:       displayTitle(node),
			URL:         node.CanonicalURL,
			Status:      node.Status,
			ResultLabel: m.nodeResultLabel(node),
			HasChildren: len(node.ChildIDs) > 0,
			Expanded:    m.expanded[nodeID],
			ParentID:    parentID,
		}
		rows = append(rows, row)

		if !m.expanded[nodeID] {
			return
		}

		for _, childID := range sortedNodeChildren(m.st, node.ChildIDs) {
			if visited[childID] {
				continue
			}
			visited[childID] = true
			walk(childID, nodeID, depth+1)
		}
	}

	visited[m.rootNodeID] = true
	walk(m.rootNodeID, "", 0)
	m.flat = rows
	m.syncSelectedIndex()
}

// Follow actual child edges from the displayed root. Parent metadata may be
// missing, cyclic, or describe a different root in the cached graph.
func (m *Model) ensureVisible(nodeID string) bool {
	if _, ok := m.st.Nodes[nodeID]; !ok {
		return false
	}
	parents := map[string]string{m.rootNodeID: ""}
	queue := []string{m.rootNodeID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if id == nodeID {
			for parent := parents[id]; parent != ""; parent = parents[parent] {
				m.expanded[parent] = true
			}
			return true
		}
		for _, child := range m.st.Nodes[id].ChildIDs {
			if _, seen := parents[child]; seen {
				continue
			}
			if _, exists := m.st.Nodes[child]; !exists {
				continue
			}
			parents[child] = id
			queue = append(queue, child)
		}
	}
	return false
}

func (m *Model) syncSelectedIndex() {
	if len(m.flat) == 0 {
		m.selectedIndex = 0
		m.selectedNodeID = ""
		return
	}
	if m.selectedNodeID != "" {
		for i := range m.flat {
			if m.flat[i].NodeID == m.selectedNodeID {
				m.selectedIndex = i
				return
			}
		}
	}
	if m.selectedIndex < 0 {
		m.selectedIndex = 0
	}
	if m.selectedIndex >= len(m.flat) {
		m.selectedIndex = len(m.flat) - 1
	}
	m.selectedNodeID = m.flat[m.selectedIndex].NodeID
}

func (m *Model) syncSelectedNodeID() {
	if len(m.flat) == 0 {
		m.selectedNodeID = ""
		return
	}
	if m.selectedIndex < 0 {
		m.selectedIndex = 0
	}
	if m.selectedIndex >= len(m.flat) {
		m.selectedIndex = len(m.flat) - 1
	}
	m.selectedNodeID = m.flat[m.selectedIndex].NodeID
}

func (m *Model) selectedRow() *treeRow {
	if len(m.flat) == 0 || m.selectedIndex < 0 || m.selectedIndex >= len(m.flat) {
		return nil
	}
	row := m.flat[m.selectedIndex]
	return &row
}

func (m *Model) selectedNode() *state.Node {
	row := m.selectedRow()
	if row == nil {
		return nil
	}
	node, ok := m.st.Nodes[row.NodeID]
	if !ok {
		return nil
	}
	return &node
}

func (m *Model) expandSelection() {
	row := m.selectedRow()
	if row == nil || !row.HasChildren {
		return
	}
	m.expanded[row.NodeID] = true
	m.rebuildFlat()
}

func (m *Model) collapseOrMoveToParent() {
	row := m.selectedRow()
	if row == nil {
		return
	}
	if row.HasChildren && m.expanded[row.NodeID] {
		delete(m.expanded, row.NodeID)
		m.rebuildFlat()
		return
	}
	if row.ParentID != "" {
		m.selectedNodeID = row.ParentID
		m.rebuildFlat()
	}
}

func (m Model) renderCourseSummary(maxWidth int, maxLines int) string {
	summary := m.courseSummary
	lines := []string{titleStyle.Render("Course Summary")}
	if summary.TotalAssignments == 0 {
		lines = append(lines, secondaryStyle.Render("No assignments with deadlines or status data in this cached tree."))
		return textutil.Fit(strings.Join(lines, "\n"), maxWidth, maxLines)
	}

	completion := fmt.Sprintf("Submitted %d/%d (%d%%), passed %d",
		summary.SubmittedAssignments,
		summary.TotalAssignments,
		summary.CompletionPercent,
		summary.PassedAssignments,
	)
	if len(summary.OverdueAssignments) > 0 {
		completion += fmt.Sprintf(" - warning: %d overdue unsubmitted", len(summary.OverdueAssignments))
	} else if dueSoonUnsubmitted(summary, 3) > 0 {
		completion += fmt.Sprintf(" - warning: %d due soon unsubmitted", dueSoonUnsubmitted(summary, 3))
	}
	if maxWidth >= 70 {
		completion = progressBar(summary.SubmittedAssignments, summary.TotalAssignments, 10) + "  " + completion
	}
	lines = append(lines, completion)

	// Keep the next deadline visible even when an older task is overdue;
	// the completion line already reports the overdue warning.
	if len(summary.UpcomingDeadlines) > 0 {
		group := summary.UpcomingDeadlines[0]
		unit := "days"
		if group.DaysUntil == 1 {
			unit = "day"
		}
		if group.DaysUntil == 0 {
			lines = append(lines, fmt.Sprintf("Due today for these %d: %s", len(group.Assignments), assignmentNameList(group.Assignments, 4)))
		} else {
			lines = append(lines, fmt.Sprintf("%d %s until deadline for these %d: %s", group.DaysUntil, unit, len(group.Assignments), assignmentNameList(group.Assignments, 4)))
		}
	} else if len(summary.OverdueAssignments) > 0 {
		lines = append(lines, fmt.Sprintf("Overdue: %s", assignmentNameList(summary.OverdueAssignments, 4)))
	} else {
		lines = append(lines, secondaryStyle.Render("No upcoming deadlines found in cached assignment metadata."))
	}

	return textutil.Fit(strings.Join(lines, "\n"), maxWidth, maxLines)
}

func dueSoonUnsubmitted(summary state.CourseSummary, maxDays int) int {
	count := 0
	for _, group := range summary.UpcomingDeadlines {
		if group.DaysUntil > maxDays {
			continue
		}
		for _, item := range group.Assignments {
			if !item.Submitted {
				count++
			}
		}
	}
	return count
}

func assignmentNameList(assignments []state.AssignmentDeadline, limit int) string {
	if len(assignments) == 0 {
		return "none"
	}
	if limit <= 0 || limit > len(assignments) {
		limit = len(assignments)
	}
	names := make([]string, 0, limit+1)
	for i := 0; i < limit; i++ {
		name := strings.TrimSpace(assignments[i].Title)
		if name == "" {
			name = assignments[i].NodeID
		}
		names = append(names, name)
	}
	if len(assignments) > limit {
		names = append(names, fmt.Sprintf("+%d more", len(assignments)-limit))
	}
	return strings.Join(names, ", ")
}

func defaultRootNodeID(st state.State) string {
	if len(st.Roots) > 0 {
		if _, ok := st.Nodes[st.Roots[0].NodeID]; ok {
			return st.Roots[0].NodeID
		}
	}

	candidates := make([]string, 0)
	for id, node := range st.Nodes {
		if len(node.ParentIDs) == 0 {
			candidates = append(candidates, id)
		}
	}
	if len(candidates) == 0 {
		for id := range st.Nodes {
			candidates = append(candidates, id)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left := st.Nodes[candidates[i]]
		right := st.Nodes[candidates[j]]
		lt := strings.ToLower(displayTitle(left))
		rt := strings.ToLower(displayTitle(right))
		if lt != rt {
			return lt < rt
		}
		return left.CanonicalURL < right.CanonicalURL
	})
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0]
}

func sortedNodeChildren(st state.State, childIDs []string) []string {
	ids := make([]string, 0, len(childIDs))
	for _, id := range childIDs {
		if _, ok := st.Nodes[id]; ok {
			ids = append(ids, id)
		}
	}
	sort.SliceStable(ids, func(i, j int) bool {
		left := st.Nodes[ids[i]]
		right := st.Nodes[ids[j]]
		lt := strings.ToLower(displayTitle(left))
		rt := strings.ToLower(displayTitle(right))
		if lt != rt {
			return lt < rt
		}
		return left.CanonicalURL < right.CanonicalURL
	})
	return ids
}

func displayTitle(node state.Node) string {
	title := strings.TrimSpace(node.Title)
	if title != "" {
		return title
	}
	if node.CanonicalURL != "" {
		return node.CanonicalURL
	}
	return node.ID
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func cloneChoiceMap(in map[string][]string) map[string][]string {
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func (m *Model) adjustDownloadOffset() {
	count := len(m.selectedNodeAssets())
	m.downloadCursor = clamp(m.downloadCursor, 0, maxInt(0, count-1))
	m.downloadOffset = visibleOffset(m.downloadOffset, m.downloadCursor, count, m.downloadListHeight())
}

func (m Model) downloadNextCmd() tea.Cmd {
	if !m.downloadInFlight || m.downloadCurrent >= len(m.downloadQueue) {
		return nil
	}
	if m.downloadExecutor == nil {
		return nil
	}
	asset := m.downloadQueue[m.downloadCurrent]
	stSnapshot := m.downloadState
	batchID := m.downloadBatchID
	index := m.downloadCurrent
	exec := m.downloadExecutor
	req := DownloadRequest{
		NodeID:    m.downloadNodeID,
		TargetDir: m.downloadTargetDir,
		Assets:    []state.AssetRef{asset},
	}
	return func() tea.Msg {
		out := exec(stSnapshot, req)
		return downloadFileFinishedMsg{Index: index, BatchID: batchID, AssetURL: asset.URL, Outcome: out}
	}
}

func (m *Model) handleDownloadFileResult(assetURL string, out DownloadOutcome) {
	if out.Err != nil {
		m.downloadStatus[assetURL] = "error"
		m.downloadErrorByURL[assetURL] = out.Err.Error()
		m.downloadFailed++
	} else {
		m.downloadStatus[assetURL] = "done"
		m.downloadDone++
	}
	m.downloadCurrent++
	if m.downloadCurrent < len(m.downloadQueue) {
		nextURL := m.downloadQueue[m.downloadCurrent].URL
		m.downloadStatus[nextURL] = "active"
		m.focusDownloadAsset(nextURL)
	}
	m.statusText = fmt.Sprintf("downloading %d/%d to %s", m.downloadDone+m.downloadFailed, len(m.downloadQueue), m.downloadTargetDir)
	m.adjustDownloadOffset()
}

func (m Model) finalizeDownloadBatch() Model {
	m.downloadInFlight = false
	m.now = time.Now()
	m.successUntil = m.now.Add(900 * time.Millisecond)
	m.downloadState = state.State{}
	duration := time.Since(m.downloadStartedAt).Milliseconds()
	m.defaultDownloadDir = m.downloadTargetDir

	if m.downloadFailed > 0 {
		m.statusText = fmt.Sprintf("download finished: %d ok, %d failed in %dms", m.downloadDone, m.downloadFailed, duration)
	} else {
		m.statusText = fmt.Sprintf("download finished: %d files in %dms", m.downloadDone, duration)
	}
	selected := make([]string, 0, len(m.downloadQueue))
	for _, asset := range m.downloadQueue {
		selected = append(selected, asset.URL)
	}
	m.recentAssetChoices[m.downloadNodeID] = selected
	if m.persistChoices != nil {
		if err := m.persistChoices(m.downloadNodeID, selected, m.downloadTargetDir); err != nil {
			m.statusText += fmt.Sprintf("; saving choices failed: %v", err)
		}
	}

	return m
}

func (m *Model) resetDownloadProgress() {
	m.downloadInFlight = false
	m.downloadQueue = []state.AssetRef{}
	m.downloadTargetDir = ""
	m.downloadCurrent = 0
	m.downloadDone = 0
	m.downloadFailed = 0
	m.downloadStatus = map[string]string{}
	m.downloadErrorByURL = map[string]string{}
	m.downloadStartedAt = time.Time{}
	m.downloadState = state.State{}
	m.downloadNodeID = ""
}

func (m Model) downloadMarker(assetURL string) string {
	if status, ok := m.downloadStatus[assetURL]; ok {
		switch status {
		case "active":
			return "…"
		case "done":
			return "✓"
		case "error":
			return "✗"
		case "pending":
			return "·"
		}
	}
	if m.downloadSelection[assetURL] {
		return "x"
	}
	return " "
}

func (m Model) downloadListHeight() int {
	header, footer := downloadChrome(m.panelContentHeight())
	return maxInt(1, m.panelContentHeight()-header-footer)
}

func (m Model) panelContentHeight() int { return maxInt(1, m.layout().bodyHeight-2) }

func colorStatusTag(s state.Status) string {
	tag := fmt.Sprintf("[%s]", s)
	switch s {
	case state.StatusOK:
		return okStyle.Render(tag)
	case state.StatusStale:
		return staleStyle.Render(tag)
	case state.StatusError:
		return errorStyle.Render(tag)
	case state.StatusNever:
		return neverStyle.Render(tag)
	default:
		return secondaryStyle.Render(tag)
	}
}

func colorResultTag(result string, freshness state.Status) string {
	tag := fmt.Sprintf("[%s]", result)
	switch normalizeStatsKey(result) {
	case "passed":
		return passStyle.Render(tag)
	case "failing":
		return failStyle.Render(tag)
	case "not_submitted":
		return noneStyle.Render(tag)
	case "ok":
		return okStyle.Render(tag)
	case "stale":
		return staleStyle.Render(tag)
	case "error":
		return errorStyle.Render(tag)
	case "never":
		return neverStyle.Render(tag)
	case "unknown":
		if freshness == state.StatusError {
			return errorStyle.Render(tag)
		}
		return secondaryStyle.Render(tag)
	default:
		if isGradeLike(result) {
			return infoStyle.Render(tag)
		}
		return secondaryStyle.Render(tag)
	}
}

func colorFreshnessTag(s state.Status) string {
	switch s {
	case state.StatusStale:
		return staleStyle.Render("(stale)")
	case state.StatusError:
		return errorStyle.Render("(error)")
	case state.StatusNever:
		return neverStyle.Render("(never)")
	default:
		return secondaryStyle.Render("")
	}
}

func colorStatusWord(s state.Status) string {
	word := strings.ToUpper(string(s))
	switch s {
	case state.StatusOK:
		return okStyle.Render(word)
	case state.StatusStale:
		return staleStyle.Render(word)
	case state.StatusError:
		return errorStyle.Render(word)
	case state.StatusNever:
		return neverStyle.Render(word)
	default:
		return secondaryStyle.Render(word)
	}
}

func colorResultWord(result string) string {
	word := strings.ToUpper(strings.ReplaceAll(result, "_", " "))
	if isGradeLike(result) {
		return infoStyle.Render(strings.TrimSpace(result))
	}
	switch normalizeStatsKey(result) {
	case "passed":
		return passStyle.Render(word)
	case "failing":
		return failStyle.Render(word)
	case "not_submitted":
		return noneStyle.Render(word)
	default:
		return infoStyle.Render(word)
	}
}

func colorDownloadMarker(mark string) string {
	switch mark {
	case "✓":
		return okStyle.Render(mark)
	case "✗":
		return errorStyle.Render(mark)
	case "…":
		return selectedStyle.Render(mark)
	case "·":
		return secondaryStyle.Render(mark)
	default:
		return mark
	}
}

func (m *Model) focusDownloadAsset(assetURL string) {
	if assetURL == "" {
		return
	}
	assets := m.selectedNodeAssets()
	for i := range assets {
		if assets[i].URL == assetURL {
			m.downloadCursor = i
			return
		}
	}
}

func readableKind(kind string) string {
	switch strings.TrimSpace(strings.ToLower(kind)) {
	case "catalog":
		return "Catalog"
	case "year":
		return "Academic Year"
	case "course":
		return "Course"
	case "assignment":
		return "Assignment"
	default:
		if kind == "" {
			return "Unknown"
		}
		k := strings.TrimSpace(kind)
		if k == "" {
			return "Unknown"
		}
		return strings.ToUpper(k[:1]) + k[1:]
	}
}

func (m Model) nodeResultLabel(node state.Node) string {
	if len(node.ChildIDs) > 0 {
		progress := state.ComputeTaskProgress(m.st, node.ID)
		if progress.Complete {
			return "passed"
		}
		if progress.Submitted > 0 {
			return "in_progress"
		}
		return "not_started"
	}
	return nodeResultLabel(node)
}

func nodeResultLabel(node state.Node) string {
	if strings.TrimSpace(strings.ToLower(node.Kind)) != "assignment" {
		switch node.Status {
		case state.StatusOK:
			return "ok"
		case state.StatusStale:
			return "stale"
		case state.StatusError:
			return "error"
		case state.StatusNever:
			return "never"
		default:
			return "unknown"
		}
	}

	summary := assignmentStatsSummary(node.Details)
	status := normalizeStatsKey(stringAnyMapGetString(summary, "status"))
	statusText := normalizeStatsKey(stringAnyMapGetString(summary, "status_text"))
	combined := strings.TrimSpace(status + " " + statusText)

	if status == "accepted" || statusText == "accepted" || containsAny(combined, "passed", "pass") {
		return "passed"
	}
	if containsAny(combined, "failed", "failing", "wrong", "error", "timeout", "diff", "runtime") {
		return "failing"
	}
	if grade := strings.TrimSpace(stringAnyMapGetString(summary, "grade")); grade != "" {
		return grade
	}
	if statusPageURL(node.Details) != "" {
		return "not_submitted"
	}
	return "unknown"
}

func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

func isGradeLike(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	raw = strings.ReplaceAll(raw, ",", ".")
	if _, err := strconv.ParseFloat(raw, 64); err == nil {
		return true
	}
	return false
}

func breadcrumbString(details map[string]any) string {
	raw, ok := details["breadcrumb"]
	if !ok {
		return ""
	}
	switch v := raw.(type) {
	case []string:
		return strings.Join(v, " > ")
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				parts = append(parts, strings.TrimSpace(s))
			}
		}
		return strings.Join(parts, " > ")
	default:
		return ""
	}
}

func configSummaryLines(details map[string]any) []string {
	cfg := state.AssignmentConfig(details)
	lines := make([]string, 0, 5)
	if v, ok := cfg["leading_submission"]; ok {
		lines = append(lines, fmt.Sprintf("- Leading submission: %v", v))
	}
	for _, field := range []struct{ key, label string }{{"start", "Opens"}, {"deadline", "Deadline"}, {"end", "Submissions close"}} {
		if date, display := state.ConfigDate(cfg, field.key); !date.IsZero() {
			lines = append(lines, fmt.Sprintf("- %s: %s", field.label, display))
		}
	}
	if v, ok := cfg["sort"]; ok {
		lines = append(lines, fmt.Sprintf("- Sorted: %v", v))
	}
	return lines
}

func statusPageURL(details map[string]any) string {
	raw, ok := details["links"]
	if !ok {
		return ""
	}
	switch links := raw.(type) {
	case map[string]string:
		return strings.TrimSpace(links["status_page"])
	case map[string]any:
		if v, ok := links["status_page"]; ok {
			if s, ok := v.(string); ok {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func statsSummaryLines(details map[string]any) []string {
	raw, ok := details["stats"]
	if !ok {
		return nil
	}
	stats, ok := raw.(map[string]any)
	if !ok {
		return nil
	}

	lines := make([]string, 0, 8)
	if page := stringAnyMapGetString(stats, "status_page"); page != "" {
		lines = append(lines, fmt.Sprintf("- Page: %s", page))
	}

	if summary := assignmentStatsSummary(details); summary != nil {
		if status := stringAnyMapGetString(summary, "status"); status != "" {
			lines = append(lines, fmt.Sprintf("- Status: %s", status))
		} else if statusText := stringAnyMapGetString(summary, "status_text"); statusText != "" {
			lines = append(lines, fmt.Sprintf("- Status: %s", statusText))
		}
		if grade := stringAnyMapGetString(summary, "grade"); grade != "" {
			lines = append(lines, fmt.Sprintf("- Grade: %s", grade))
		}
		if group := stringAnyMapGetString(summary, "group"); group != "" {
			lines = append(lines, fmt.Sprintf("- Group: %s", group))
		}
	}

	if counts := anyMapToStringAnyMap(stats["counts"]); counts != nil {
		total := stringAnyMapGetInt(counts, "total")
		passed := stringAnyMapGetInt(counts, "passed")
		if total >= 0 || passed >= 0 {
			parts := make([]string, 0, 2)
			if total >= 0 {
				parts = append(parts, fmt.Sprintf("total=%d", total))
			}
			if passed >= 0 {
				parts = append(parts, fmt.Sprintf("passed=%d", passed))
			}
			lines = append(lines, fmt.Sprintf("- Counts: %s", strings.Join(parts, ", ")))
		}
	}

	if refs := stringAnyMapGetMap(stats, "submission_refs"); refs != nil {
		refKeys := []string{"leading", "best", "latest", "first_pass", "last_pass"}
		for _, key := range refKeys {
			ref, ok := pickSubmissionRef(refs, key)
			if !ok {
				continue
			}
			refMap, ok := ref.(map[string]any)
			if !ok {
				continue
			}
			title := strings.TrimSpace(stringAnyMapGetString(refMap, "title"))
			if title == "" {
				title = strings.TrimSpace(stringAnyMapGetString(refMap, "url"))
			}
			if title == "" {
				continue
			}
			label := strings.ReplaceAll(key, "_", " ")
			lines = append(lines, fmt.Sprintf("- %s: %s", capitalizeFirst(label), title))
		}
	}

	return lines
}

func assignmentStatsSummary(details map[string]any) map[string]any {
	raw, ok := details["stats"]
	if !ok {
		return nil
	}
	stats, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return stringAnyMapGetMap(stats, "summary")
}

func stringAnyMapGetMap(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	v, ok := mapLookupNormalized(m, key)
	if !ok {
		return nil
	}
	out, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return out
}

func anyMapToStringAnyMap(v any) map[string]any {
	switch vv := v.(type) {
	case map[string]any:
		return vv
	case map[string]int:
		out := make(map[string]any, len(vv))
		for k, val := range vv {
			out[k] = val
		}
		return out
	case map[string]string:
		out := make(map[string]any, len(vv))
		for k, val := range vv {
			out[k] = val
		}
		return out
	default:
		return nil
	}
}

func statsDebugLines(details map[string]any) []string {
	if !statsDebugEnabled() {
		return nil
	}
	raw, ok := details["stats"]
	if !ok {
		return []string{"- stats key missing"}
	}
	stats, ok := raw.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("- stats type: %T", raw)}
	}
	lines := []string{
		fmt.Sprintf("- has summary: %t", stringAnyMapGetMap(stats, "summary") != nil),
		fmt.Sprintf("- has counts: %t", anyMapToStringAnyMap(stats["counts"]) != nil),
		fmt.Sprintf("- has refs: %t", stringAnyMapGetMap(stats, "submission_refs") != nil),
	}
	if b, err := json.Marshal(stats); err == nil {
		lines = append(lines, "- raw: "+string(b))
	} else {
		lines = append(lines, fmt.Sprintf("- raw marshal error: %v", err))
	}
	return lines
}

func statsDebugEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("THEMIS_DEBUG_STATS")))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func stringAnyMapGetString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := mapLookupNormalized(m, key)
	if !ok {
		return ""
	}
	switch vv := v.(type) {
	case string:
		return strings.TrimSpace(vv)
	default:
		return ""
	}
}

func stringAnyMapGetInt(m map[string]any, key string) int {
	if m == nil {
		return -1
	}
	v, ok := mapLookupNormalized(m, key)
	if !ok {
		return -1
	}
	switch vv := v.(type) {
	case int:
		return vv
	case int64:
		return int(vv)
	case float64:
		return int(vv)
	case float32:
		return int(vv)
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(vv))
		if err == nil {
			return i
		}
	}
	return -1
}

func capitalizeFirst(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	runes := []rune(s)
	first := strings.ToUpper(string(runes[0]))
	if len(runes) == 1 {
		return first
	}
	return first + string(runes[1:])
}

func mapLookupNormalized(m map[string]any, key string) (any, bool) {
	if m == nil {
		return nil, false
	}
	if v, ok := m[key]; ok {
		return v, true
	}
	target := normalizeStatsKey(key)
	for k, v := range m {
		if normalizeStatsKey(k) == target {
			return v, true
		}
	}
	return nil, false
}

func normalizeStatsKey(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return ""
	}
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return ""
	}
	out := strings.Join(parts, "_")
	out = strings.ReplaceAll(out, "-", "_")
	out = strings.Trim(out, "_:")
	return out
}

func pickSubmissionRef(refs map[string]any, target string) (any, bool) {
	if refs == nil {
		return nil, false
	}
	if v, ok := mapLookupNormalized(refs, target); ok {
		return v, true
	}
	normTarget := normalizeStatsKey(target)
	aliases := map[string][]string{
		"leading":    {"counts_towards_grade", "submission_that_counts_towards_the_grade"},
		"best":       {"latest_submission_with_the_best_result"},
		"latest":     {"most_recent_submission"},
		"first_pass": {"first_submission_that_passed"},
		"last_pass":  {"last_submission_to_pass_before_the_deadline"},
	}
	for k, v := range refs {
		nk := normalizeStatsKey(k)
		if strings.Contains(nk, normTarget) {
			return v, true
		}
		for _, alias := range aliases[normTarget] {
			if strings.Contains(nk, alias) {
				return v, true
			}
		}
	}
	return nil, false
}

func clipTopLines(content string, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	lines := strings.Split(content, "\n")
	if len(lines) <= maxLines {
		return content
	}
	return strings.Join(lines[:maxLines], "\n")
}

func clipLines(lines []string, maxLines int) []string {
	if maxLines <= 0 {
		return []string{}
	}
	if len(lines) <= maxLines {
		return lines
	}
	return lines[:maxLines]
}

func wrapLines(lines []string, maxWidth int) string {
	return textutil.Wrap(strings.Join(lines, "\n"), maxWidth)
}

func truncateOneLine(content string, maxWidth int) string {
	return textutil.Truncate(content, maxWidth)
}

// State contains nested maps and slices; a shallow copy races with View while
// the executor updates the cache. Its persisted JSON representation provides
// an isolated snapshot and validates unsupported values before dispatch.
func snapshotState(st state.State) (state.State, error) {
	data, err := json.Marshal(st)
	if err != nil {
		return state.State{}, fmt.Errorf("snapshot state: %w", err)
	}
	var snapshot state.State
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return state.State{}, fmt.Errorf("snapshot state: %w", err)
	}
	return snapshot, nil
}
