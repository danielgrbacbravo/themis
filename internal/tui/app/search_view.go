package app

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"themis-cli/internal/state"
	"themis-cli/internal/tui/textutil"
)

const searchPanelWidth, searchPanelHeight = 108, 28

func (m Model) searchSize() (int, int) {
	return clamp(m.width, 0, searchPanelWidth), clamp(m.height, 0, searchPanelHeight)
}

func searchColumnWidths(width int) (title, course, year int) {
	if width >= 62 {
		return width - 28, 12, 10
	}
	if width >= 36 {
		return width - 14, 0, 10
	}
	return maxInt(1, width-2), 0, 0
}
func padSearch(s string, width int) string {
	s = textutil.Truncate(s, width)
	return s + strings.Repeat(" ", maxInt(0, width-lipgloss.Width(s)))
}
func (m Model) searchRowContext(n state.Node) (string, string, string) {
	u, err := url.Parse(n.CanonicalURL)
	if err != nil {
		return "", "", ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 3 {
		return "", "", ""
	}
	courseURL := *u
	courseURL.Path = "/course/" + parts[1] + "/" + parts[2]
	courseURL.RawPath = ""
	course := m.st.Nodes[state.NodeIDFromCanonicalURL(courseURL.String())]
	if course.ID == "" {
		for _, candidate := range m.st.Nodes {
			if strings.TrimRight(candidate.CanonicalURL, "/") == courseURL.String() {
				course = candidate
				break
			}
		}
	}
	code := strings.ToUpper(courseInitials(course.Title))
	if len(code) < 2 {
		code = parts[2]
	}
	return code, parts[1], courseURL.String()
}
func (m Model) selectedSearchDetails(n state.Node) string {
	context := strings.TrimPrefix(m.locationLabel(n.ID), "Courses / ")
	if n.Status == state.StatusError {
		return context + " · " + errorStyle.Render("Sync failed · r retries")
	}
	if m.refreshTarget == n.ID {
		return context + " · " + infoStyle.Render("Loading…")
	}
	// Unknown/unloaded data is not evidence of an unsubmitted assignment.
	result := m.nodeResultLabel(n)
	switch normalizeStatsKey(result) {
	case "passed", "accepted":
		context += " · " + passStyle.Render("✓ Passed")
	case "failing", "failed", "rejected":
		context += " · " + failStyle.Render("× Needs attention")
	}
	return context
}
func (m Model) searchListRows(width, height int) int {
	header := 1
	if m.browser.searchHelp && height >= 9 {
		header += 2
	}
	if width >= 36 && height >= 8 {
		header += 2
	}
	footer := 2
	if m.browser.notice != "" && height >= 9 {
		footer++
	}
	return maxInt(1, height-header-footer)
}
func (m Model) renderSearch(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	entries := m.browserEntries()
	cursor := clamp(m.browser.cursor, 0, maxInt(0, len(entries)-1))
	count := fmt.Sprintf("%d results", len(entries))
	if width < 62 {
		count = fmt.Sprint(len(entries))
	}
	input := m.browser.searchInput
	if !input.Focused() || input.Value() != m.browser.query {
		input = newSearchInput(m.browser.query)
	}
	resizeSearchInput(&input, maxInt(1, width-11-lipgloss.Width(count)))
	header := padSearch(input.View(), maxInt(1, width-lipgloss.Width(count)-2)) + "  " + infoStyle.Render(count)
	lines := []string{header}
	if m.browser.searchHelp && height >= 9 {
		lines = append(lines, "Filters: "+literalSearchStyle.Render("course: year: type: status: path: title:"), "Example: course:pc year:26/27")
	}
	titleWidth, courseWidth, yearWidth := searchColumnWidths(width)
	if width >= 36 && height >= 8 {
		labels := "  " + padSearch("Title", titleWidth)
		if courseWidth > 0 {
			labels += "  " + padSearch("Course", courseWidth)
		}
		labels += "  " + padSearch("Year", yearWidth)
		lines = append(lines, labels, mutedStyle.Render(strings.Repeat("─", width)))
	}
	rows := m.searchListRows(width, height)
	lines = append(lines, m.renderPickerRows(entries, cursor, rows, width, titleWidth, courseWidth, yearWidth)...)
	if len(entries) == 0 {
		message := "No matches. Try a course acronym or fewer words."
		if _, hint := parseSearch(m.browser.query); hint != "" {
			message = hint
		}
		lines = append(lines, textutil.Truncate(message, width))
	}
	footerLines := 2
	if m.browser.notice != "" && height >= 9 {
		footerLines++
	}
	for len(lines) < height-footerLines {
		lines = append(lines, "")
	}
	details := "Search downloaded courses and assignments"
	if len(entries) > 0 {
		details = m.selectedSearchDetails(m.st.Nodes[entries[cursor].id])
	}
	lines = append(lines, textutil.Truncate(details, width))
	if footerLines == 3 {
		lines = append(lines, textutil.Truncate(errorStyle.Render(m.browser.notice), width))
	}
	hints := "↑↓ select   Enter open   F1 filters   Esc close"
	if width < 50 {
		hints = "↑↓ select · Enter · F1 · Esc"
	}
	if width < 30 {
		hints = "↑↓ · Enter · Esc"
	}
	lines = append(lines, textutil.Truncate(browserKeyHints(hints), width))
	return textutil.Fit(strings.Join(lines, "\n"), width, height)
}

// Search, catalog, and recents share row spacing, selection, and context rules.
func (m Model) renderPickerRows(entries []browserEntry, cursor, rows, width, titleWidth, courseWidth, yearWidth int) []string {
	lines := []string{}
	start := visibleOffset(0, cursor, len(entries), rows)
	previousContext := ""
	for i := start; i < len(entries) && i < start+rows; i++ {
		entry := entries[i]
		n := m.pickerNode(entry)
		title := browserTitle(n)
		title = padSearch(title, titleWidth)
		prefix := "  "
		if i == cursor {
			prefix = titleStyle.Render("> ")
			title = titleStyle.Render(title)
		}
		row := prefix + title
		code, year, context := m.searchRowContext(n)
		// Show context at the start of a run and on the selected row, rather
		// than repeating the same course/year beside every exercise.
		if i != start && i != cursor && context == previousContext {
			code, year = "", ""
		}
		previousContext = context
		if courseWidth > 0 {
			row += "  " + literalSearchStyle.Render(padSearch(code, courseWidth))
		}
		if yearWidth > 0 {
			row += "  " + padSearch(year, yearWidth)
		}
		lines = append(lines, textutil.Truncate(row, width))
	}
	return lines
}
func (m Model) pickerNode(entry browserEntry) state.Node {
	if n, ok := m.st.Nodes[entry.id]; ok {
		return n
	}
	for _, item := range m.browser.preferences.Recent {
		id, canonical, err := state.NodeIDFromURL(item.URL)
		if err == nil && id == entry.id {
			return state.Node{ID: id, Title: item.Title, CanonicalURL: canonical}
		}
	}
	return state.Node{ID: entry.id, Title: entry.label}
}
