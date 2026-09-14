package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"themis-cli/internal/state"
	"themis-cli/internal/tui/textutil"
	"themis-cli/internal/tui/theme"
)

const minWidth, minHeight = 24, 8

type screenLayout struct {
	width, height                           int
	summaryHeight, statusHeight, bodyHeight int
	treeWidth, detailsWidth                 int
	single, tooSmall                        bool
}

// Every panel and viewport uses this budget, including all borders.
func (m Model) layout() screenLayout {
	l := screenLayout{width: m.width, height: m.height, single: m.width < 72, tooSmall: m.width < minWidth || m.height < minHeight}
	if l.tooSmall {
		return l
	}
	l.statusHeight = 3
	if m.height >= 14 {
		l.statusHeight = 4
	}
	if m.height >= 18 {
		l.summaryHeight = 5
	} else if m.height >= 12 {
		l.summaryHeight = 3
	}
	l.bodyHeight = m.height - l.summaryHeight - l.statusHeight
	l.treeWidth = m.width / 2
	l.detailsWidth = m.width - l.treeWidth
	if l.single {
		l.treeWidth, l.detailsWidth = m.width, m.width
	}
	return l
}

func (m Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	l := m.layout()
	if l.tooSmall {
		return textutil.Fit(fmt.Sprintf("Terminal too small\nResize to %d x %d\nCurrent: %d x %d\nq quit", minWidth, minHeight, m.width, m.height), m.width, m.height)
	}
	panels := make([]string, 0, 3)
	if l.summaryHeight > 0 {
		panels = append(panels, renderPanel(m.renderCourseSummary(l.width-2, l.summaryHeight-2), l.width, l.summaryHeight, false))
	}
	h := l.bodyHeight - 2
	var body string
	if m.showHelp {
		body = renderPanel(m.renderHelp(l.width-2, h), l.width, l.bodyHeight, true)
	} else if m.mode == "download" {
		body = m.renderDownloadBody(l)
	} else if l.single {
		content := m.renderTreeForSize(l.treeWidth-2, h)
		if m.detailsFocused || m.mode == "download" {
			content = m.renderDetailsForSize(l.detailsWidth-2, h)
		}
		body = renderPanel(content, l.width, l.bodyHeight, true)
	} else {
		tree := renderPanel(m.renderTreeForSize(l.treeWidth-2, h), l.treeWidth, l.bodyHeight, !m.detailsFocused && m.mode != "download")
		details := renderPanel(m.renderDetailsForSize(l.detailsWidth-2, h), l.detailsWidth, l.bodyHeight, m.detailsFocused || m.mode == "download")
		body = lipgloss.JoinHorizontal(lipgloss.Top, tree, details)
	}
	panels = append(panels, body, renderPanel(m.renderStatusForSize(l.width-2, l.statusHeight-2), l.width, l.statusHeight, false))
	return textutil.Fit(lipgloss.JoinVertical(lipgloss.Left, panels...), l.width, l.height)
}

func renderPanel(content string, width, height int, focused bool) string {
	style := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Align(lipgloss.Left, lipgloss.Top)
	if focused {
		style = style.BorderForeground(theme.Accent)
	}
	// Lip Gloss Width/Height are minimum sizes. Fit before rendering so wrapping
	// cannot silently grow a panel and push prior frames into terminal scrollback.
	return style.Width(width - 2).Height(height - 2).Render(textutil.Fit(content, width-2, height-2))
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func visibleOffset(offset, cursor, count, height int) int {
	height = maxInt(1, height)
	offset = clamp(offset, 0, maxInt(0, count-height))
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+height {
		offset = cursor - height + 1
	}
	return maxInt(0, offset)
}

func (m *Model) adjustViewports() {
	m.syncSelectedIndex()
	if m.layout().tooSmall {
		return
	}
	h := m.panelContentHeight()
	if m.showHelp {
		m.helpOffset = clamp(m.helpOffset, 0, maxInt(0, len(m.helpLines())-maxInt(1, h-1)))
	}
	m.treeOffset = visibleOffset(m.treeOffset, m.selectedIndex, len(m.flat), maxInt(1, h-1))
	m.adjustDownloadOffset()
	if m.detailsOffset > 0 {
		count := len(m.detailsLines(m.layout().detailsWidth - 2))
		m.detailsOffset = clamp(m.detailsOffset, 0, maxInt(0, count-maxInt(1, h-1)))
	} else {
		m.detailsOffset = 0
	}
}

func (m Model) renderTree() string { return m.renderTreeForSize(10000, len(m.flat)+1) }
func (m Model) renderTreeForHeight(height int) string {
	return m.renderTreeForSize(maxInt(1, m.layout().treeWidth-2), height)
}

func (m Model) renderTreeForSize(width, height int) string {
	if height <= 0 {
		return ""
	}
	if len(m.flat) == 0 {
		return textutil.Fit("Tree\n(no nodes)", width, height)
	}
	rows := maxInt(1, height-1)
	start := visibleOffset(m.treeOffset, m.selectedIndex, len(m.flat), rows)
	end := clamp(start+rows, 0, len(m.flat))
	lines := []string{}
	if height > 1 {
		title := "Tree"
		if end-start < len(m.flat) {
			title += fmt.Sprintf(" %d-%d/%d", start+1, end, len(m.flat))
		}
		lines = append(lines, titleStyle.Render(title))
	}
	for i := start; i < end; i++ {
		row := m.flat[i]
		prefix := "  "
		if i == m.selectedIndex {
			prefix = titleStyle.Render("> ")
		}
		indent := strings.Repeat(" ", clamp(row.Depth*2, 0, maxInt(0, width/4)))
		icon := taskIcon(row.ResultLabel, row.Status)
		if row.HasChildren {
			icon = secondaryStyle.Render("▸")
			if row.Expanded {
				icon = secondaryStyle.Render("▾")
			}
			if row.ResultLabel == "passed" {
				icon += " " + passStyle.Render("✓")
			}
		}
		line := prefix + indent + icon + " " + strings.Join(strings.Fields(row.Title), " ")
		line = truncateOneLine(line, width)
		if i == m.selectedIndex {
			line = selectedStyle.Render(line)
		}
		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

func (m Model) detailsLines(width int) []string {
	content := m.renderDetails(width, 0)
	lines := strings.Split(content, "\n")
	if len(lines) > 0 {
		lines = lines[1:]
	}
	return strings.Split(textutil.Wrap(strings.Join(lines, "\n"), width), "\n")
}

func (m Model) renderDetailsForSize(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if m.mode == "download" {
		if node := m.selectedNode(); node != nil {
			return m.renderDownloadPanel(*node, width, height)
		}
	}
	lines := m.detailsLines(width)
	rows := maxInt(1, height-1)
	start := clamp(m.detailsOffset, 0, maxInt(0, len(lines)-rows))
	end := clamp(start+rows, 0, len(lines))
	title := "Overview"
	if len(lines) > rows {
		title += fmt.Sprintf(" · %d/%d  tab to scroll", start+1, len(lines))
	}
	visible := []string{}
	if height > 1 {
		visible = append(visible, titleStyle.Render(title))
	}
	visible = append(visible, lines[start:end]...)
	return textutil.Fit(strings.Join(visible, "\n"), width, height)
}

func (m Model) renderStatusForSize(width, height int) string {
	mode := strings.ToUpper(m.mode)
	if m.mode != "download" && m.detailsFocused {
		mode = "DETAILS"
	}
	message := m.activityIcon() + " " + titleStyle.Render(mode) + "  " + m.statusText
	keys := "j/k move  h/l fold  d files  tab details  ? help  q quit"
	if m.detailsFocused {
		keys = "j/k scroll  pgup/pgdown page  tab tree  i source  ? help  q quit"
	}
	if m.mode == "download" {
		keys = "j/k move  tab preview  space toggle  enter download  esc close"
		if m.preview.focused {
			keys = "j/k scroll  pgup/pgdown page  tab files  r retry  esc close"
		}
	}
	if m.downloadInFlight && m.mode != "download" {
		keys = "d progress  ? help  q quit"
	}
	if width < 60 {
		keys = "? help  tab pane  q quit"
	}
	if m.showHelp {
		keys = "j/k scroll  esc close  q quit"
	}
	if height == 1 {
		return truncateOneLine(mode+" | ? help | "+m.statusText, width)
	}
	return textutil.Fit(message+"\n"+secondaryStyle.Render(keys), width, height)
}

func (m Model) helpLines() []string {
	content := "j/k or arrows: move / scroll\nh/l or left/right: fold / open tree\ng/G or home/end: first / last\npgup/pgdown: page\ntab: tree / details or files / preview\ni: show / hide source details\nr/R/f: refresh node / subtree / all\nd: files / live progress\np: linked project\nspace: toggle file\na/c: select all / clear\nenter: open / download\nesc/h/d: close files (active downloads continue)\nq / ctrl+c: quit"
	return strings.Split(textutil.Wrap(content, maxInt(1, m.width-2)), "\n")
}

func (m Model) renderHelp(width, height int) string {
	lines := m.helpLines()
	rows := maxInt(1, height-1)
	start := clamp(m.helpOffset, 0, maxInt(0, len(lines)-rows))
	end := clamp(start+rows, 0, len(lines))
	content := append([]string{titleStyle.Render("Keys · j/k scroll · esc close")}, lines[start:end]...)
	return textutil.Fit(strings.Join(content, "\n"), width, height)
}

// Keep at least one selectable row, dropping secondary header/footer lines as
// space runs out. Rendering and scrolling must use precisely the same budget.
func downloadChrome(height int) (header, footer int) {
	if height >= 8 {
		return 5, 1
	}
	if height >= 4 {
		return 1, 1
	}
	if height >= 2 {
		return 1, 0
	}
	return 0, 0
}

func (m Model) renderDownloadPanel(node state.Node, width, height int) string {
	assets := m.selectedNodeAssets()
	headerCount, footerCount := downloadChrome(height)
	target := m.defaultDownloadDir
	if target == "" {
		target = "./tests"
	}
	if m.downloadInFlight {
		target = m.downloadTargetDir
	}
	header := []string{titleStyle.Render("Download"), "Node: " + displayTitle(node), "Target dir: " + target, secondaryStyle.Render("tab preview · space select"), ""}
	lines := append([]string{}, header[:headerCount]...)
	listHeight := maxInt(1, height-headerCount-footerCount)
	start := visibleOffset(m.downloadOffset, m.downloadCursor, len(assets), listHeight)
	end := clamp(start+listHeight, 0, len(assets))
	for i := start; i < end; i++ {
		asset := assets[i]
		cursor := " "
		if i == m.downloadCursor {
			cursor = ">"
		}
		name := strings.Join(strings.Fields(asset.Name), " ")
		if name == "" {
			name = asset.URL
		}
		line := fmt.Sprintf("%s [%s] %s %s", cursor, m.renderDownloadMarker(asset.URL), renderFileIcon(asset), name)
		lines = append(lines, truncateOneLine(line, width))
	}
	if len(assets) == 0 {
		lines = append(lines, "(no assets available)")
	}
	if footerCount > 0 {
		footer := fmt.Sprintf("Selected: %d/%d", len(m.selectedAssetURLs()), len(assets))
		if m.downloadInFlight || m.downloadDone+m.downloadFailed > 0 {
			footer = progressBar(m.downloadDone+m.downloadFailed, len(m.downloadQueue), clamp(width/4, 4, 12)) + fmt.Sprintf("  %d/%d done · %d failed", m.downloadDone, len(m.downloadQueue), m.downloadFailed)
		}
		lines = append(lines, footer)
	}
	return textutil.Fit(strings.Join(lines, "\n"), width, height)
}

func (m Model) updateDownloadKey(key string) (tea.Model, tea.Cmd) {
	if m.previewKey(key) {
		return m, nil
	}
	switch key {
	case "esc", "h", "left", "d":
		m.mode = "browse"
		if m.downloadInFlight {
			m.statusText = "download continues in background; d shows progress"
		} else {
			m.statusText = "download mode closed"
			m.resetDownloadProgress()
		}
	case "up", "k":
		m.downloadCursor--
	case "down", "j":
		m.downloadCursor++
	case "pgup":
		m.downloadCursor -= m.downloadListHeight()
	case "pgdown":
		m.downloadCursor += m.downloadListHeight()
	case "g", "home":
		m.downloadCursor = 0
	case "G", "end":
		m.downloadCursor = len(m.selectedNodeAssets()) - 1
	case " ", "a", "c", "enter":
		if m.downloadInFlight {
			m.statusText = "download already in progress"
			return m, nil
		}
		switch key {
		case " ":
			m.toggleDownloadSelectionAtCursor()
		case "a":
			for _, asset := range m.selectedNodeAssets() {
				m.downloadSelection[asset.URL] = true
			}
		case "c":
			m.downloadSelection = map[string]bool{}
		case "enter":
			return m.startDownload()
		}
	}
	return m, nil
}
