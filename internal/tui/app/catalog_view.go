package app

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"themis-cli/internal/state"
	"themis-cli/internal/tui/textutil"
)

// Within one catalog branch the breadcrumb already supplies course and year.
// Recents can mix branches, so it retains search's identifying columns.
func (m Model) catalogColumns(width int) (title, code, year int) {
	if m.browser.overlay == "recent" {
		return searchColumnWidths(width)
	}
	if m.st.Nodes[m.browser.container].Kind == "year" && width >= 48 {
		return width - 18, 14, 0
	}
	return maxInt(1, width-2), 0, 0
}
func (m Model) catalogListRows(width, height int) int {
	header := 1
	if m.browser.overlay == "catalog" && height >= 7 {
		header++
	}
	if width >= 36 && height >= 9 {
		header += 2
	}
	footer := 2
	if m.browser.notice != "" && height >= 9 {
		footer++
	}
	return maxInt(1, height-header-footer)
}
func (m Model) renderCatalog(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	entries := m.browserEntries()
	cursor := clamp(m.browser.cursor, 0, maxInt(0, len(entries)-1))
	recent := m.browser.overlay == "recent"
	tabs := titleStyle.Render("[Catalog]") + "   Recent"
	if recent {
		tabs = "Catalog   " + titleStyle.Render("[Recent]")
	}
	count := fmt.Sprint(len(entries))
	lines := []string{padSearch(tabs, maxInt(1, width-lipgloss.Width(count)-2)) + "  " + infoStyle.Render(count)}
	if !recent && height >= 7 {
		lines = append(lines, textutil.Truncate(m.locationLabel(m.browser.container), width))
	}
	titleWidth, codeWidth, yearWidth := m.catalogColumns(width)
	if width >= 36 && height >= 9 {
		titleLabel := "Title"
		if !recent {
			switch m.st.Nodes[m.browser.container].Kind {
			case "catalog":
				titleLabel = "Academic year"
			case "year":
				titleLabel = "Course"
			default:
				titleLabel = "Assignment"
			}
		}
		labels := "  " + padSearch(titleLabel, titleWidth)
		if codeWidth > 0 {
			label := "Code"
			if recent {
				label = "Course"
			}
			labels += "  " + padSearch(label, codeWidth)
		}
		if yearWidth > 0 {
			labels += "  " + padSearch("Year", yearWidth)
		}
		lines = append(lines, labels, mutedStyle.Render(strings.Repeat("─", width)))
	}
	rows := m.catalogListRows(width, height)
	lines = append(lines, m.renderPickerRows(entries, cursor, rows, width, titleWidth, codeWidth, yearWidth)...)
	container := m.st.Nodes[m.browser.container]
	if len(entries) == 0 {
		message := "This folder is empty."
		switch {
		case recent:
			message = "No recent locations. Tab opens Catalog."
		case container.Status == state.StatusError:
			message = "Could not load this folder. Press r to retry."
		case m.refreshTarget == container.ID:
			message = "Loading this folder…"
		case !container.ChildrenHydrated:
			message = "Not loaded. Press r to load this folder."
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
	details := "Tab switches Catalog / Recent"
	if len(entries) > 0 {
		entry := entries[cursor]
		if _, ok := m.st.Nodes[entry.id]; ok {
			details = m.selectedSearchDetails(m.st.Nodes[entry.id])
		} else {
			details = m.pickerNode(entry).CanonicalURL + " · Open to load details"
		}
	}
	if !recent {
		if container.Status == state.StatusError {
			details = errorStyle.Render("Folder refresh failed · r retries")
		} else if m.refreshTarget == container.ID {
			details = infoStyle.Render("Loading this folder…")
		}
	}
	lines = append(lines, textutil.Truncate(details, width))
	if footerLines == 3 {
		lines = append(lines, textutil.Truncate(errorStyle.Render(m.browser.notice), width))
	}
	hints := "↑↓ select   Enter open   ← parent   Tab recent   / search   Esc close"
	if recent {
		hints = "↑↓ select   Enter open   Tab catalog   / search   Esc close"
	}
	if width < 76 {
		hints = "↑↓ select · Enter · ← · Tab · / · Esc"
	}
	if width < 40 {
		hints = "↑↓ · Enter · Tab · / · Esc"
	}
	if width < 28 {
		hints = "↑↓ · Enter · Tab · Esc"
	}
	lines = append(lines, textutil.Truncate(browserKeyHints(hints), width))
	return textutil.Fit(strings.Join(lines, "\n"), width, height)
}
