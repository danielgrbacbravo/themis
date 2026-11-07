package app

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/text/unicode/norm"

	"themis-cli/internal/projectlink"
	"themis-cli/internal/state"
)

// Search aliases are derived from source metadata; official titles stay intact.
// Normalize punctuation, accents, and letter/number boundaries consistently for
// both queries and documents (lab1, Lab 1, and lab_1 become equivalent).
func normalizeSearch(raw string) string {
	var out strings.Builder
	var previous rune
	for _, r := range norm.NFKD.String(raw) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if previous != 0 && ((unicode.IsLetter(previous) && unicode.IsDigit(r)) || (unicode.IsDigit(previous) && unicode.IsLetter(r)) || (unicode.IsLower(previous) && unicode.IsUpper(r))) {
				out.WriteByte(' ')
			}
			out.WriteRune(unicode.ToLower(r))
			previous = r
		} else {
			out.WriteByte(' ')
			previous = 0
		}
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

func courseInitials(title string) string {
	// Audience qualifiers should not turn "Algorithms and Data Structures (for
	// CS)" into ADSFCS. The URL course code remains a separate exact alias.
	var plain strings.Builder
	depth := 0
	for _, r := range title {
		if r == '(' {
			depth++
			continue
		}
		if r == ')' {
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth == 0 {
			plain.WriteRune(r)
		}
	}
	var initials strings.Builder
	for _, word := range strings.Fields(normalizeSearch(plain.String())) {
		switch word {
		case "and", "of", "the", "for", "to", "in", "a", "an":
			continue
		}
		for _, r := range word {
			initials.WriteRune(r)
			break
		}
	}
	return initials.String()
}

type searchField struct {
	scope, text, compact string
	weight               int
}
type searchDocument struct {
	year            int
	id, label, kind string
	fields          []searchField
}
type searchTerm struct{ scope, text string }

func parseSearch(query string) ([]searchTerm, string) {
	terms := []searchTerm{}
	for _, word := range strings.Fields(query) {
		scope, value := "", word
		if colon := strings.IndexByte(word, ':'); colon >= 0 {
			candidate := strings.ToLower(word[:colon])
			switch candidate {
			case "course", "year", "type", "status", "path", "title", "name":
				scope, value = candidate, word[colon+1:]
				if scope == "name" {
					scope = "title"
				}
				if normalizeSearch(value) == "" {
					return nil, "Add a value after " + candidate + ":"
				}
			}
		}
		if text := normalizeSearch(value); text != "" {
			terms = append(terms, searchTerm{scope, text})
		}
	}
	return terms, ""
}

func (m Model) buildSearchDocuments() []searchDocument {
	byURL := map[string]state.Node{}
	for _, n := range m.st.Nodes {
		byURL[strings.TrimRight(n.CanonicalURL, "/")] = n
	}
	docs := make([]searchDocument, 0, len(m.st.Nodes))
	for id, n := range m.st.Nodes {
		if n.Kind != "course" && n.Kind != "assignment" {
			continue
		}
		if m.st.BaseURL != "" && projectlink.ValidateTarget(m.st.BaseURL, n.CanonicalURL) != nil {
			continue
		}
		d := searchDocument{id: id, label: m.entryLabel(id), kind: n.Kind}
		add := func(scope, text string, weight int) {
			normalized := normalizeSearch(text)
			if normalized != "" {
				d.fields = append(d.fields, searchField{scope, normalized, strings.ReplaceAll(normalized, " ", ""), weight})
			}
		}
		add("title", n.Title, 60)
		add("path", n.CanonicalURL, 5)
		u, err := url.Parse(n.CanonicalURL)
		if err == nil {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) >= 3 && parts[0] == "course" {
				year, code := parts[1], parts[2]
				d.year = academicYear(n.CanonicalURL)
				add("year", year, 15)
				years := strings.Split(year, "-")
				if len(years) == 2 && len(years[0]) == 4 && len(years[1]) == 4 {
					add("year", years[0][2:]+" "+years[1][2:], 15)
					add("year", years[0]+" "+years[1][2:], 15)
				}
				courseURL := *u
				courseURL.Path = "/course/" + year + "/" + code
				courseURL.RawPath = ""
				course := byURL[courseURL.String()]
				weight := 30
				if n.Kind == "course" {
					weight = 55
				}
				add("course", code, weight)
				add("course", course.Title, weight)
				if initials := courseInitials(course.Title); len([]rune(initials)) >= 2 {
					add("course", initials, weight)
				}
				if n.Kind == "course" {
					add("course", courseInitials(n.Title), weight)
				}
				// Titles of intermediate assignments make "system diagnostics health"
				// useful even when the exercise title itself omits the lab name.
				prefix := courseURL.Path
				for _, part := range parts[3:] {
					prefix += "/" + part
					ancestorURL := *u
					ancestorURL.Path = prefix
					ancestorURL.RawPath = ""
					if ancestor, ok := byURL[ancestorURL.String()]; ok {
						add("path", ancestor.Title, 10)
					}
					add("path", part, 20)
				}
			}
		}
		add("type", n.Kind, 10)
		if n.Kind == "assignment" && n.ChildrenHydrated && len(n.ChildIDs) == 0 {
			add("type", "exercise task", 10)
		}
		result := m.nodeResultLabel(n)
		add("status", result+" "+resultLabel(result), 10)
		switch normalizeStatsKey(result) {
		case "passed", "accepted":
			add("status", "passed accepted complete", 10)
		case "failing", "failed", "rejected":
			add("status", "failed failing rejected needs attention", 10)
		case "not_submitted", "not_started", "never", "unknown", "ok", "":
			add("status", "unsubmitted not submitted ready", 10)
		}
		docs = append(docs, d)
	}
	return docs
}

// Exact codes and title words outrank prefixes, substrings, and subsequences.
// Fuzzy fallback is restricted to unscoped terms so filters stay predictable.
func searchFieldScore(term searchTerm, field searchField) (int, bool) {
	if term.scope != "" && term.scope != field.scope {
		return 0, false
	}
	compact := strings.ReplaceAll(term.text, " ", "")
	if field.compact == compact {
		return 140 + field.weight, true
	}
	for _, word := range strings.Fields(field.text) {
		if word == term.text {
			return 120 + field.weight, true
		}
	}
	numeric := true
	for _, r := range compact {
		if !unicode.IsDigit(r) {
			numeric = false
			break
		}
	}
	if numeric {
		return 0, false
	}
	if strings.HasPrefix(field.text, term.text) {
		return 105 + field.weight, true
	}
	for _, word := range strings.Fields(field.text) {
		if strings.HasPrefix(word, compact) {
			return 90 + field.weight, true
		}
	}
	if strings.Contains(field.compact, compact) {
		return 70 + field.weight, true
	}
	if term.scope != "" || (field.scope != "title" && field.scope != "course") || len([]rune(compact)) < 3 {
		return 0, false
	}
	needle, haystack := []rune(compact), []rune(field.compact)
	next, first, last := 0, -1, 0
	for i, r := range haystack {
		if r == needle[next] {
			if first < 0 {
				first = i
			}
			last = i
			next++
			if next == len(needle) {
				gap := last - first + 1 - len(needle)
				// Avoid matching a few scattered letters across a long description/path.
				if gap > len(needle)*3 {
					return 0, false
				}
				score := 45 + field.weight - gap
				if first == 0 {
					score += 8
				}
				return score, true
			}
		}
	}
	return 0, false
}

func (m Model) searchEntries() []browserEntry {
	terms, hint := parseSearch(m.browser.query)
	if hint != "" {
		return nil
	}
	docs := m.browser.searchDocuments
	if docs == nil {
		docs = m.buildSearchDocuments()
	}
	type ranked struct {
		entry browserEntry
		score int
		url   string
		year  int
	}
	matches := []ranked{}
	for _, d := range docs {
		score := 0
		matched := true
		for _, term := range terms {
			best, found := 0, false
			for _, field := range d.fields {
				if value, ok := searchFieldScore(term, field); ok && (!found || value > best) {
					best, found = value, true
				}
			}
			if !found {
				matched = false
				break
			}
			score += best
		}
		if !matched {
			continue
		}
		if d.kind == "course" {
			score += 5
		}
		if len(terms) == 0 {
			for i, item := range m.browser.preferences.Recent {
				if item.URL == m.st.Nodes[d.id].CanonicalURL {
					score += 40 - i
					break
				}
			}
		}
		matches = append(matches, ranked{browserEntry{d.id, d.label}, score, m.st.Nodes[d.id].CanonicalURL, d.year})
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		if matches[i].year != matches[j].year {
			return matches[i].year > matches[j].year
		}
		if matches[i].entry.label != matches[j].entry.label {
			return matches[i].entry.label < matches[j].entry.label
		}
		return matches[i].url < matches[j].url
	})
	entries := make([]browserEntry, len(matches))
	for i, item := range matches {
		entries[i] = item.entry
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

// Academic years are source metadata, so ranking does not depend on the clock.
func academicYear(raw string) int {
	u, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "course" {
		return 0
	}
	years := strings.Split(parts[1], "-")
	if len(years) != 2 || len(years[0]) != 4 {
		return 0
	}
	start, err := strconv.Atoi(years[0])
	if err != nil {
		return 0
	}
	return start
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
