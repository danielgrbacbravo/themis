package app

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/alecthomas/chroma/v2"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"themis-cli/internal/filepreview"
	"themis-cli/internal/state"
	"themis-cli/internal/tui/textutil"
	"themis-cli/internal/tui/theme"
)

type PreviewExecutor func(context.Context, state.AssetRef) (filepreview.Content, error)
type previewResult struct {
	doc filepreview.Document
	err error
}
type previewState struct {
	url              string
	generation       uint64
	cancel           context.CancelFunc
	loading, focused bool
	offset           int
	result           previewResult
	cache            map[string]previewResult
	order            []string
}
type previewStartMsg struct {
	generation uint64
	asset      state.AssetRef
}
type previewFinishedMsg struct {
	generation uint64
	url        string
	result     previewResult
}

func (m *Model) cancelPreview() {
	if m.preview.cancel != nil {
		m.preview.cancel()
		m.preview.cancel = nil
	}
	m.preview.loading = false
	m.preview.generation++
}

// Called after cursor normalization, so even asynchronous download progress or
// window changes cannot fetch an out-of-range or previously selected asset.
func (m *Model) syncPreview(previousMode string) tea.Cmd {
	if m.mode != "download" {
		if m.preview.url != "" {
			m.cancelPreview()
			m.preview.url = ""
		}
		return nil
	}
	if previousMode != "download" {
		m.cancelPreview()
		m.preview.url, m.preview.focused = "", false
		m.preview.cache, m.preview.order = map[string]previewResult{}, nil
	}
	assets := m.selectedNodeAssets()
	if len(assets) == 0 {
		m.cancelPreview()
		m.preview.url = ""
		return nil
	}
	asset := assets[clamp(m.downloadCursor, 0, len(assets)-1)]
	if asset.URL == m.preview.url {
		return nil
	}
	m.cancelPreview()
	m.preview.url, m.preview.offset = asset.URL, 0
	m.preview.result = previewResult{}
	if result, ok := m.preview.cache[asset.URL]; ok {
		m.preview.result = result
		return nil
	}
	if m.previewExecutor == nil {
		m.preview.result.err = fmt.Errorf("preview is unavailable")
		return nil
	}
	m.preview.loading = true
	generation := m.preview.generation
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return previewStartMsg{generation, asset} })
}

func (m Model) startPreview(msg previewStartMsg) (tea.Model, tea.Cmd) {
	if m.mode != "download" || msg.generation != m.preview.generation || msg.asset.URL != m.preview.url || !m.preview.loading {
		return m, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	m.preview.cancel = cancel
	exec := m.previewExecutor
	return m, func() tea.Msg {
		defer cancel()
		content, err := exec(ctx, msg.asset)
		result := previewResult{err: err}
		if err == nil {
			if err = ctx.Err(); err != nil {
				result.err = err
			} else {
				result.doc, result.err = filepreview.PrepareContext(ctx, previewName(msg.asset), content)
			}
		}
		return previewFinishedMsg{msg.generation, msg.asset.URL, result}
	}
}

func (m Model) finishPreview(msg previewFinishedMsg) (tea.Model, tea.Cmd) {
	if m.mode != "download" || msg.generation != m.preview.generation || msg.url != m.preview.url {
		return m, nil
	}
	m.preview.loading = false
	m.preview.cancel = nil
	m.preview.result = msg.result
	// Bound cached documents, and only cache successful reads. Retry stays useful.
	if msg.result.err == nil {
		if m.preview.cache == nil {
			m.preview.cache = map[string]previewResult{}
		}
		if len(m.preview.order) >= 8 {
			delete(m.preview.cache, m.preview.order[0])
			m.preview.order = m.preview.order[1:]
		}
		m.preview.cache[msg.url] = msg.result
		m.preview.order = append(m.preview.order, msg.url)
	}
	return m, nil
}

func previewName(asset state.AssetRef) string {
	if strings.TrimSpace(asset.Name) != "" {
		return safePreviewName(asset.Name)
	}
	if u, err := url.Parse(asset.URL); err == nil {
		return safePreviewName(path.Base(u.Path))
	}
	return "File"
}

func safePreviewName(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '�'
		}
		return r
	}, name)
}

func tokenStyle(t chroma.TokenType) lipgloss.Style {
	switch {
	case t.InCategory(chroma.Keyword):
		return titleStyle
	case t.InSubCategory(chroma.LiteralString):
		return passStyle
	case t.InSubCategory(chroma.LiteralNumber):
		return lipgloss.NewStyle().Foreground(theme.Literal)
	case t.InCategory(chroma.Comment):
		return lipgloss.NewStyle().Italic(true) // Keep comments readable in light themes.
	case t == chroma.NameFunction || t == chroma.NameClass || t == chroma.NameBuiltin:
		return infoStyle
	case t == chroma.Error:
		return errorStyle
	default:
		return lipgloss.NewStyle()
	}
}

func (m Model) previewRows() int { return maxInt(1, m.panelContentHeight()-4) }

func (m Model) pdfPreviewLines(width int) [][]chroma.Token {
	var lines [][]chroma.Token
	for _, line := range m.preview.result.doc.Lines {
		var text strings.Builder
		for _, token := range line {
			text.WriteString(token.Value)
		}
		for _, wrapped := range strings.Split(textutil.Wrap(text.String(), maxInt(1, width)), "\n") {
			lines = append(lines, []chroma.Token{{Type: chroma.Text, Value: wrapped}})
		}
	}
	return lines
}

func (m Model) previewLineCount() int {
	if m.preview.result.doc.Pages == 0 {
		return len(m.preview.result.doc.Lines)
	}
	width := m.width - 2
	if m.width >= 80 {
		width -= clamp(m.width/3, 28, 44)
	}
	return len(m.pdfPreviewLines(width))
}

func (m Model) renderPreview(width, height int) string {
	if height <= 0 {
		return ""
	}
	title := "Preview · Tab to scroll"
	if m.preview.focused {
		title = "Preview · Tab to files"
	}
	assets := m.selectedNodeAssets()
	name := "No file selected"
	kind := fileType{label: "File"}
	if len(assets) > 0 {
		asset := assets[clamp(m.downloadCursor, 0, len(assets)-1)]
		name = previewName(asset)
		kind = assetFileType(asset)
	}
	lines := []string{titleStyle.Render(title), headingStyle.Render(kind.icon + " " + name)}
	doc := m.preview.result.doc
	switch {
	case m.preview.loading:
		lines = append(lines, "", m.activityIcon()+" Loading preview…")
	case m.preview.result.err != nil:
		lines = append(lines, "", textutil.Wrap(m.preview.result.err.Error(), width), "", "r Retry preview")
	case doc.Bytes == 0:
		lines = append(lines, "", "Empty file")
	default:
		language := doc.Language
		if language == "Plain text" && kind.label != "File" {
			language = kind.label
		}
		meta := fmt.Sprintf("%s · %d lines · %d B", language, len(doc.Lines), doc.Bytes)
		if doc.Pages > 0 {
			meta = fmt.Sprintf("PDF text · %d pages · %d B", doc.Pages, doc.Bytes)
		}
		if doc.Truncated {
			meta += " · truncated"
		}
		lines = append(lines, meta)
		displayLines := doc.Lines
		if doc.Pages > 0 {
			displayLines = m.pdfPreviewLines(width)
		}
		rows := maxInt(1, height-4)
		start := clamp(m.preview.offset, 0, maxInt(0, len(displayLines)-rows))
		end := clamp(start+rows, 0, len(displayLines))
		digits := len(fmt.Sprint(len(doc.Lines)))
		for i := start; i < end; i++ {
			var code strings.Builder
			for _, token := range displayLines[i] {
				code.WriteString(tokenStyle(token.Type).Render(token.Value))
			}
			if doc.Pages > 0 {
				lines = append(lines, code.String())
				continue
			}
			gutter := fmt.Sprintf("%*d │ ", digits, i+1)
			lines = append(lines, gutter+textutil.Truncate(code.String(), maxInt(0, width-lipgloss.Width(gutter))))
		}
		footer := fmt.Sprintf("%d–%d / %d · j/k scroll", start+1, end, len(displayLines))
		if doc.Truncated {
			footer = "Partial preview · download for full file"
		}
		lines = append(lines, footer)
	}
	return textutil.Fit(strings.Join(lines, "\n"), width, height)
}

// Downloading gets the full body width: a compact file list next to the file
// itself, or one focusable pane when both would be too narrow to read.
func (m Model) renderDownloadBody(l screenLayout) string {
	h := l.bodyHeight - 2
	if l.width < 80 {
		content := m.renderPreview(l.width-2, h)
		if !m.preview.focused {
			if node := m.selectedNode(); node != nil {
				content = m.renderDownloadPanel(*node, l.width-2, h)
			}
		}
		return renderPanel(content, l.width, l.bodyHeight, true)
	}
	listWidth := clamp(l.width/3, 28, 44)
	list := "No files"
	if node := m.selectedNode(); node != nil {
		list = m.renderDownloadPanel(*node, listWidth-2, h)
	}
	left := renderPanel(list, listWidth, l.bodyHeight, !m.preview.focused)
	right := renderPanel(m.renderPreview(l.width-listWidth-2, h), l.width-listWidth, l.bodyHeight, m.preview.focused)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m *Model) previewKey(key string) bool {
	switch key {
	case "tab", "shift+tab":
		m.preview.focused = !m.preview.focused
		return true
	case "r":
		delete(m.preview.cache, m.preview.url)
		// Remove from eviction order too, avoiding duplicate cache entries on retry.
		for i, key := range m.preview.order {
			if key == m.preview.url {
				m.preview.order = append(m.preview.order[:i], m.preview.order[i+1:]...)
				break
			}
		}
		m.cancelPreview()
		m.preview.url = ""
		return true
	}
	if !m.preview.focused {
		return false
	}
	rows := m.previewRows()
	switch key {
	case "j", "down":
		m.preview.offset++
	case "k", "up":
		m.preview.offset--
	case "pgdown":
		m.preview.offset += rows
	case "pgup":
		m.preview.offset -= rows
	case "g", "home":
		m.preview.offset = 0
	case "G", "end":
		m.preview.offset = m.previewLineCount()
	default:
		return false
	}
	m.preview.offset = clamp(m.preview.offset, 0, maxInt(0, m.previewLineCount()-rows))
	return true
}
