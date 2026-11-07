package app

import (
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"themis-cli/internal/browserprefs"
	"themis-cli/internal/projectlink"
	"themis-cli/internal/state"
)

type location struct {
	root, selected            string
	expanded                  map[string]bool
	treeOffset, detailsOffset int
	focused                   bool
}
type browserState struct {
	overlay, container, query string
	searchDocuments           []searchDocument
	searchInput               textinput.Model
	searchHelp                bool
	cursor, offset            int
	history                   []location
	project                   projectlink.Context
	manageLink                func(projectlink.Change) (projectlink.Context, error)
	preferences               browserprefs.Preferences
	savePreferences           func(browserprefs.Preferences) error
	directory, target         string
	notice                    string
	originalDirectory         string
	change                    projectlink.Change
	editing, confirming       bool
}
type browserEntry struct{ id, label string }

func seedNode(st *state.State, raw, title, kind string) string {
	id, canonical, err := state.NodeIDFromURL(raw)
	if err != nil {
		return ""
	}
	if _, exists := st.Nodes[id]; !exists {
		if u, err := url.Parse(canonical); err == nil {
			switch len(strings.Split(strings.Trim(u.Path, "/"), "/")) {
			case 1:
				kind = "catalog"
			case 2:
				kind = "year"
			case 3:
				kind = "course"
			default:
				kind = "assignment"
			}
		}
		st.Nodes[id] = state.Node{ID: id, CanonicalURL: canonical, Title: title, Kind: kind, Status: state.StatusNever}
	}
	return id
}
func (m *Model) catalogID() string { return seedNode(&m.st, m.st.CatalogRootURL, "Courses", "catalog") }
func (m Model) locationLabel(id string) string {
	n := m.st.Nodes[id]
	u, err := url.Parse(n.CanonicalURL)
	if err != nil {
		return n.Title
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) <= 1 {
		return "Courses"
	}
	// The URL supplies context even when its parent pages are not cached.
	labels := []string{"Courses"}
	prefix := "/course"
	for _, part := range parts[1:] {
		prefix += "/" + part
		parent := *u
		parent.Path, parent.RawPath = prefix, ""
		label := part
		canonical, err := state.CanonicalizeURL(parent.String())
		if err == nil {
			if node, ok := m.st.Nodes[state.NodeIDFromCanonicalURL(canonical)]; ok && node.Title != "" {
				label = node.Title
			}
		}
		labels = append(labels, label)
	}
	labels[len(labels)-1] = displayTitle(n)
	return strings.Join(labels, " / ")
}

// entryLabel puts the target first so long parent paths do not hide it.
func (m Model) entryLabel(id string) string {
	full := strings.TrimPrefix(m.locationLabel(id), "Courses / ")
	parts := strings.Split(full, " / ")
	if len(parts) < 2 {
		return full
	}
	return parts[len(parts)-1] + " · " + strings.Join(parts[:len(parts)-1], " / ")
}
func (m Model) loadBranch(id string) (tea.Model, tea.Cmd) {
	node, ok := m.st.Nodes[id]
	if !ok || node.ChildrenHydrated || node.Status == state.StatusError {
		return m, nil
	}
	return m.requestLoad(id)
}
func (m Model) requestLoad(id string) (tea.Model, tea.Cmd) {
	if id == "" {
		m.statusText = "Catalog unavailable: no base URL configured"
		return m, nil
	}
	if m.refreshInFlight || m.downloadInFlight {
		if m.refreshTarget != id {
			found := false
			for _, pending := range m.pendingLoads {
				if pending == id {
					found = true
				}
			}
			if !found {
				m.pendingLoads = append(m.pendingLoads, id)
			}
		}
		return m, nil
	}
	return m.startRefreshAt(id, RefreshScopeNode, 0)
}
func copyExpanded(src map[string]bool) map[string]bool {
	dst := map[string]bool{}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
func (m Model) openLocation(id string) (tea.Model, tea.Cmd) {
	n, ok := m.st.Nodes[id]
	if ok && m.st.BaseURL != "" {
		if err := projectlink.ValidateTarget(m.st.BaseURL, n.CanonicalURL); err != nil {
			m.statusText = err.Error()
			return m, nil
		}
	}
	if !ok {
		m.statusText = "Location is not available"
		return m, nil
	}
	if m.rootNodeID != id {
		m.browser.history = append(m.browser.history, location{m.rootNodeID, m.selectedNodeID, copyExpanded(m.expanded), m.treeOffset, m.detailsOffset, m.detailsFocused})
	}
	m.rootNodeID, m.selectedNodeID = id, id
	m.expanded = map[string]bool{id: true}
	m.treeOffset, m.detailsOffset = 0, 0
	m.detailsFocused = false
	m.browser.overlay = ""
	m.courseSummary = state.ComputeCourseSummary(m.st, id, m.now)
	m.rebuildFlat()
	if n.Kind == "course" || n.Kind == "assignment" {
		next := m.browser.preferences.Open(browserprefs.Target{URL: n.CanonicalURL, Title: n.Title})
		if m.browser.savePreferences != nil {
			if err := m.browser.savePreferences(next); err != nil {
				m.browser.notice = "Could not save recent locations: " + err.Error()
				m.statusText = m.browser.notice
			} else {
				m.browser.preferences = next
				m.browser.notice = ""
			}
		} else {
			m.browser.preferences = next
		}
	}
	updated, cmd := m.loadBranch(id)
	next := updated.(Model)
	if next.browser.notice != "" {
		next.statusText = next.browser.notice
	}
	return next, cmd
}
func (m Model) goBack() Model {
	if len(m.browser.history) == 0 {
		m.statusText = "No previous browsing location"
		return m
	}
	i := len(m.browser.history) - 1
	old := m.browser.history[i]
	m.browser.history = m.browser.history[:i]
	m.rootNodeID, m.selectedNodeID, m.expanded = old.root, old.selected, old.expanded
	m.treeOffset, m.detailsOffset, m.detailsFocused = old.treeOffset, old.detailsOffset, old.focused
	m.courseSummary = state.ComputeCourseSummary(m.st, m.rootNodeID, m.now)
	m.rebuildFlat()
	return m
}
func (m Model) openLinked() (tea.Model, tea.Cmd) {
	id := m.linkedRootNodeID
	if id == "" {
		m.statusText = "No linked directory; P to manage links"
		return m, nil
	}
	if _, ok := m.st.Nodes[id]; !ok {
		raw := m.browser.project.Config.LinkedRootURL
		if raw == "" {
			m.statusText = "Linked target unavailable"
			return m, nil
		}
		id = seedNode(&m.st, raw, raw, "assignment")
	}
	return m.openLocation(id)
}
func (m Model) browserEntries() []browserEntry {
	entries := []browserEntry{}
	switch m.browser.overlay {
	case "recent":
		for _, item := range m.browser.preferences.Recent {
			if projectlink.ValidateTarget(m.st.BaseURL, item.URL) != nil {
				continue
			}
			id, _, err := state.NodeIDFromURL(item.URL)
			if err != nil {
				continue
			}
			label := item.Title + " · " + item.URL
			if _, ok := m.st.Nodes[id]; ok {
				label = m.entryLabel(id)
			}
			entries = append(entries, browserEntry{id, label})
		}
	case "search":
		return m.searchEntries()
	default:
		for _, id := range sortedNodeChildren(m.st, m.st.Nodes[m.browser.container].ChildIDs) {
			entries = append(entries, browserEntry{id, m.entryLabel(id)})
		}
	}
	if m.browser.overlay == "catalog" {
		sort.SliceStable(entries, func(i, j int) bool {
			a, b := m.st.Nodes[entries[i].id], m.st.Nodes[entries[j].id]
			if (a.Kind == "year") != (b.Kind == "year") {
				return a.Kind == "year"
			}
			if a.Kind == "year" && b.Kind == "year" {
				return academicYear(a.CanonicalURL) > academicYear(b.CanonicalURL)
			}
			return false
		})
	}
	return entries
}
func (m *Model) openProject() {
	m.browser.overlay = "project"
	m.browser.offset = 0
	m.browser.confirming = false
	m.browser.editing = false
	m.browser.directory = m.browser.project.WorkingDir
	if m.browser.directory == "" {
		m.browser.directory = "."
	}
	if n := m.selectedNode(); n != nil {
		m.browser.target = n.CanonicalURL
	}
}
func (m Model) updateBrowserKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	b := &m.browser
	if b.overlay == "destination" {
		return m.updateDestination(msg)
	}
	if b.overlay == "project" {
		return m.updateProjectKey(msg)
	}
	if key == "esc" {
		b.overlay = ""
		return m, nil
	}
	if b.overlay == "search" {
		if key == "f1" {
			b.searchHelp = !b.searchHelp
			return m, nil
		}
		if b.searchDocuments == nil {
			b.searchDocuments = m.buildSearchDocuments()
		}
		switch key {
		case "up", "down", "pgup", "pgdown", "enter", "ctrl+n", "ctrl+p":
			// These keys select results; all other editing belongs to the input.
		default:
			return m.updateSearchInput(msg)
		}
	} else {
		if key == "q" {
			return m, tea.Quit
		}
		if key == "tab" {
			if b.overlay == "recent" {
				b.overlay = "catalog"
			} else {
				b.overlay = "recent"
			}
			b.cursor = 0
			return m, nil
		}
		if key == "/" {
			m.beginSearch()
			return m, nil
		}
	}
	entries := m.browserEntries()
	b.cursor = clamp(b.cursor, 0, maxInt(0, len(entries)-1))
	switch key {
	case "up", "k", "ctrl+p":
		b.cursor = maxInt(0, b.cursor-1)
	case "down", "j", "ctrl+n":
		b.cursor = clamp(b.cursor+1, 0, maxInt(0, len(entries)-1))
	case "pgup":
		step := maxInt(1, m.panelContentHeight()-5)
		if b.overlay == "search" || b.overlay == "catalog" || b.overlay == "recent" {
			w, h := m.searchSize()
			if b.overlay == "search" {
				step = m.searchListRows(w-2, h-2)
			} else {
				step = m.catalogListRows(w-2, h-2)
			}
		}
		b.cursor = maxInt(0, b.cursor-step)
	case "pgdown":
		step := maxInt(1, m.panelContentHeight()-5)
		if b.overlay == "search" || b.overlay == "catalog" || b.overlay == "recent" {
			w, h := m.searchSize()
			if b.overlay == "search" {
				step = m.searchListRows(w-2, h-2)
			} else {
				step = m.catalogListRows(w-2, h-2)
			}
		}
		b.cursor = clamp(b.cursor+step, 0, maxInt(0, len(entries)-1))
	case "backspace", "ctrl+h", "left", "h":
		if b.overlay == "catalog" {
			node := m.st.Nodes[b.container]
			u, err := url.Parse(node.CanonicalURL)
			if err == nil && strings.TrimRight(u.Path, "/") != "/course" {
				u.Path = filepath.ToSlash(filepath.Dir(u.Path))
				u.RawPath = ""
				b.container = seedNode(&m.st, u.String(), filepath.Base(u.Path), "year")
				b.cursor = 0
				return m.loadBranch(b.container)
			}
		}
	case "r":
		id := b.container
		if len(entries) > 0 {
			id = entries[b.cursor].id
		}
		if b.overlay == "catalog" && m.st.Nodes[b.container].Status == state.StatusError {
			id = b.container
		}
		return m.requestLoad(id)
	case "enter", "right", "l":
		if len(entries) == 0 {
			return m, nil
		}
		id := entries[b.cursor].id
		if _, ok := m.st.Nodes[id]; !ok {
			for _, item := range b.preferences.Recent {
				candidate, _, _ := state.NodeIDFromURL(item.URL)
				if candidate == id {
					seedNode(&m.st, item.URL, item.Title, "assignment")
				}
			}
		}
		node := m.st.Nodes[id]
		if b.overlay == "catalog" && (key != "enter" || node.Kind == "year" || node.Kind == "catalog") {
			b.container = id
			b.cursor = 0
			return m.loadBranch(id)
		}
		return m.openLocation(id)
	}
	return m, nil
}
func (m Model) updateProjectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	b := &m.browser
	key := msg.String()
	if b.editing {
		switch key {
		case "esc":
			b.directory = b.originalDirectory
			b.editing = false
		case "enter":
			b.editing = false
		case "backspace", "ctrl+h":
			r := []rune(b.directory)
			if len(r) > 0 {
				b.directory = string(r[:len(r)-1])
			}
		case "ctrl+u":
			b.directory = ""
		default:
			if msg.Type == tea.KeyRunes {
				b.directory += string(msg.Runes)
			} else if msg.Type == tea.KeySpace {
				b.directory += " "
			}
		}
		return m, nil
	}
	if key == "esc" {
		if b.confirming {
			b.confirming = false
		} else {
			b.overlay = ""
		}
		return m, nil
	}
	if b.confirming {
		if key != "enter" {
			if key == "j" || key == "down" {
				b.offset++
			}
			if key == "k" || key == "up" {
				b.offset = maxInt(0, b.offset-1)
			}
			return m, nil
		}
		if b.manageLink == nil {
			m.statusText = "Link management unavailable"
			return m, nil
		}
		ctx, err := b.manageLink(b.change)
		b.confirming = false
		if err != nil {
			m.statusText = "Project link failed: " + err.Error()
			return m, nil
		}
		b.project = ctx
		m.linkedRootNodeID = ctx.Config.LinkedRootNodeID
		m.recentAssetChoices = cloneChoiceMap(ctx.Config.RecentAssetChoices)
		m.downloadDirs = cloneDirectories(ctx.Config.DownloadDirs)
		// An explicit link change may change defaults; merely browsing never does.
		m.defaultDownloadDir = ctx.Config.LastDownloadDir
		if m.defaultDownloadDir == "" {
			m.defaultDownloadDir = ctx.WorkingDir
		} else if !filepath.IsAbs(m.defaultDownloadDir) {
			m.defaultDownloadDir = filepath.Join(ctx.WorkingDir, m.defaultDownloadDir)
		}
		m.subtreeRefreshDepth = ctx.Config.Preferences.DefaultRefreshDepth
		if m.subtreeRefreshDepth < 1 {
			m.subtreeRefreshDepth = 1
		}
		m.statusText = "Project link updated; effective association shown below"
		return m, nil
	}
	switch key {
	case "q":
		return m, tea.Quit
	case "j", "down":
		b.offset++
	case "k", "up":
		b.offset = maxInt(0, b.offset-1)
	case "/":
		b.originalDirectory = b.directory
		b.editing = true
	case "p":
		b.overlay = ""
		return m.openLinked()
	case "l", "c", "x":
		if m.downloadInFlight {
			m.statusText = "Wait for downloads before changing their project context"
			return m, nil
		}
		directory := b.directory
		if directory == "" {
			m.statusText = "Choose a link directory first"
			return m, nil
		}
		if key == "c" || key == "x" {
			directory = b.project.LinkDir
			if directory == "" {
				m.statusText = "No effective link to change or remove"
				return m, nil
			}
		}
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(b.project.WorkingDir, directory)
		}
		b.change = projectlink.Change{Directory: filepath.Clean(directory), TargetURL: b.target, Remove: key == "x"}
		b.confirming = true
		b.offset = 0
	}
	return m, nil
}
func (m Model) contextLine() string {
	label := "No directory link"
	if m.browser.project.LinkDir != "" {
		label = "Linked: " + m.browser.project.LinkDir
	} else if m.linkedRootNodeID != "" {
		label = "Linked: " + displayTitle(m.st.Nodes[m.linkedRootNodeID])
	}
	return m.locationLabel(m.selectedNodeID) + " · " + label
}
func (m Model) downloadContext() string {
	if m.linkedRootNodeID == "" {
		return ""
	}
	linked := m.st.Nodes[m.linkedRootNodeID].CanonicalURL
	if linked == "" {
		linked = m.browser.project.Config.LinkedRootURL
	}
	selected := m.st.Nodes[m.selectedNodeID].CanonicalURL
	if selected != linked && !strings.HasPrefix(selected, strings.TrimRight(linked, "/")+"/") {
		return " · outside linked target"
	}
	return ""
}
