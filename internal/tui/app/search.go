package app

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"themis-cli/internal/catalog"
)

type searchDocument = catalog.SearchDocument

var courseInitials = catalog.CourseInitials
var academicYear = catalog.AcademicYear
var parseSearch = catalog.ParseSearch

func (m Model) buildSearchDocuments() []searchDocument { return catalog.BuildSearch(m.st) }
func (m Model) searchEntries() []browserEntry {
	docs := m.browser.searchDocuments
	if docs == nil {
		docs = m.buildSearchDocuments()
	}
	recent := []string{}
	for _, item := range m.browser.preferences.Recent {
		recent = append(recent, item.URL)
	}
	matches := catalog.Rank(docs, m.browser.query, recent)
	entries := make([]browserEntry, len(matches))
	for i, item := range matches {
		entries[i] = browserEntry{item.ID, item.Label}
	}
	return entries
}
func (m *Model) beginSearch() {
	m.browser.overlay = "search"
	m.browser.query = ""
	m.browser.searchHelp = false
	m.browser.cursor = 0
	m.browser.searchDocuments = m.buildSearchDocuments()
	m.browser.searchInput = newSearchInput("")
}

func newSearchInput(query string) textinput.Model {
	input := textinput.New()
	input.Prompt = "Search  "
	input.PromptStyle = titleStyle
	input.TextStyle = infoStyle.Bold(true)
	input.Placeholder = "PC, os lab1, course:pc"
	input.PlaceholderStyle = secondaryStyle
	input.CharLimit = 512
	input.SetCursorMode(textinput.CursorStatic)
	input.Cursor.Style = titleStyle
	input.SetValue(query)
	input.CursorEnd()
	input.Focus()
	return input
}
func (m Model) updateSearchInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	if !m.browser.searchInput.Focused() {
		m.browser.searchInput = newSearchInput(m.browser.query)
	}
	resizeSearchInput(&m.browser.searchInput, maxInt(1, m.width-11))
	var cmd tea.Cmd
	m.browser.searchInput, cmd = m.browser.searchInput.Update(msg)
	query := m.browser.searchInput.Value()
	if query != m.browser.query {
		m.browser.query = query
		m.browser.cursor = 0
	}
	return m, cmd
}

func resizeSearchInput(input *textinput.Model, width int) {
	if input.Width == width {
		return
	}
	value, position := input.Value(), input.Position()
	input.Width = width
	// Reset the input viewport using its public API, then restore the insertion
	// point. Merely setting Width leaves stale offsets after a terminal resize.
	input.SetValue("")
	input.SetValue(value)
	input.SetCursor(position)
}
