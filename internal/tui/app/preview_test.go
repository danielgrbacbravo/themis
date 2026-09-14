package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"themis-cli/internal/filepreview"
	"themis-cli/internal/state"
)

func previewModelForTest(t *testing.T) Model {
	m := resize(stressModel(t), 120, 36)
	m.reducedMotion = true
	m.previewExecutor = func(ctx context.Context, asset state.AssetRef) (filepreview.Content, error) {
		return filepreview.Content{Data: []byte("int main(void) {\n    return 42;\n}")}, nil
	}
	// Use code names to exercise highlighting in the final view too.
	root := m.st.Nodes[m.selectedNodeID]
	for i := range root.Assets {
		root.Assets[i].Name = fmt.Sprintf("file-%03d.c", i)
	}
	m.st.Nodes[root.ID] = root
	return m
}
func finishCurrentPreview(t *testing.T, m Model) Model {
	t.Helper()
	asset := m.selectedNodeAssets()[m.downloadCursor]
	out, cmd := m.Update(previewStartMsg{m.preview.generation, asset})
	if cmd == nil {
		t.Fatal("preview fetch was not scheduled")
	}
	m = out.(Model)
	out, _ = m.Update(cmd())
	return out.(Model)
}
func TestPreviewCancelsAndIgnoresStaleResponses(t *testing.T) {
	m := previewModelForTest(t)
	m, _ = sendKey(m, "d")
	first := m.selectedNodeAssets()[0]
	old := m.preview.generation
	started := make(chan context.Context, 1)
	finished := make(chan tea.Msg, 1)
	m.previewExecutor = func(c context.Context, _ state.AssetRef) (filepreview.Content, error) {
		started <- c
		<-c.Done()
		return filepreview.Content{}, c.Err()
	}
	out, cmd := m.Update(previewStartMsg{old, first})
	m = out.(Model)
	go func() { finished <- cmd() }()
	ctx := <-started
	// The actual context is canceled when leaving the selection; a stale
	// completion must not replace the next file's loading state or cache.
	m, _ = sendKey(m, "j")
	if ctx.Err() == nil {
		t.Fatal("old request context not canceled")
	}
	var oldResult tea.Msg
	select {
	case oldResult = <-finished:
	case <-time.After(time.Second):
		t.Fatal("preview command did not cancel")
	}
	out, _ = m.Update(oldResult)
	m = out.(Model)
	if !m.preview.loading || m.preview.result.doc.Bytes != 0 || len(m.preview.cache) != 0 {
		t.Fatal("stale result replaced current preview")
	}
	out, cmd = m.Update(previewStartMsg{old, first})
	if cmd != nil {
		t.Fatal("stale debounce started network work")
	}
	m = out.(Model)
	m, _ = sendKey(m, "esc")
	if m.preview.loading || m.preview.url != "" {
		t.Fatal("closing picker did not clear preview")
	}
}
func TestPreviewCacheRetryAndFocus(t *testing.T) {
	m := previewModelForTest(t)
	m, _ = sendKey(m, "d")
	m = finishCurrentPreview(t, m)
	if m.preview.result.doc.Language != "C" {
		t.Fatal("code preview missing")
	}
	first := m.preview.result.doc.Bytes
	m, _ = sendKey(m, "j")
	m = finishCurrentPreview(t, m)
	m, cmd := sendKey(m, "k")
	if cmd != nil || m.preview.loading || m.preview.result.doc.Bytes != first {
		t.Fatal("previous file was not cached")
	}
	m, _ = sendKey(m, "tab")
	cursor := m.downloadCursor
	m, _ = sendKey(m, "j")
	if m.downloadCursor != cursor || !m.preview.focused {
		t.Fatal("scrolling preview moved file cursor")
	}
	m, _ = sendKey(m, "r")
	if !m.preview.loading {
		t.Fatal("retry did not reload preview")
	}
	for i := 0; i < 10; i++ {
		if m.preview.loading {
			m = finishCurrentPreview(t, m)
		}
		m.preview.focused = false
		m, _ = sendKey(m, "j")
	}
	if len(m.preview.cache) > 8 || len(m.preview.order) > 8 {
		t.Fatal("preview cache grew without a bound")
	}
	m, _ = sendKey(m, "esc")
	m, _ = sendKey(m, "d")
	if len(m.preview.cache) != 0 {
		t.Fatal("reopened picker kept stale cached files")
	}
}
func TestPreviewLayoutFitsBothPanesAndTerminalPalette(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(profile)
	m := previewModelForTest(t)
	m, _ = sendKey(m, "d")
	m = finishCurrentPreview(t, m)
	for _, focused := range []bool{false, true} {
		m.preview.focused = focused
		for _, w := range []int{10, 24, 40, 71, 72, 79, 80, 100, 120, 200} {
			for _, h := range []int{3, 8, 12, 18, 24, 36} {
				m = resize(m, w, h)
				view := m.View()
				assertFits(t, view, w, h)
				if strings.Contains(view, "38;") || strings.Contains(view, "48;") || strings.Contains(view, "\x1b[7m") {
					t.Fatal("preview ignored terminal palette")
				}
				if w >= 80 && h >= 18 && (!strings.Contains(view, "Download") || !strings.Contains(view, "Preview")) {
					t.Fatal("wide picker must show both panes")
				}
				if w >= 24 && w < 80 && h >= 18 {
					if focused && !strings.Contains(view, "Preview") {
						t.Fatal("narrow preview not visible")
					}
					if !focused && !strings.Contains(view, "Download") {
						t.Fatal("narrow file list not visible")
					}
				}
			}
		}
	}
}
func TestPreviewErrorCanRetry(t *testing.T) {
	m := previewModelForTest(t)
	m.previewExecutor = func(context.Context, state.AssetRef) (filepreview.Content, error) {
		return filepreview.Content{}, fmt.Errorf("session expired")
	}
	m, _ = sendKey(m, "d")
	m = finishCurrentPreview(t, m)
	if m.preview.loading || !strings.Contains(m.View(), "session expired") {
		t.Fatal("preview failure not shown")
	}
	m, _ = sendKey(m, "r")
	if !m.preview.loading {
		t.Fatal("retry did not start")
	}
}

func TestPDFPreviewWrapsAndScrollsToTheEnd(t *testing.T) {
	m := previewModelForTest(t)
	m, _ = sendKey(m, "d")
	doc, err := filepreview.Prepare("extracted.txt", filepreview.Content{Data: []byte("── Page 1 ──\n" + strings.Repeat("Read all these words carefully. ", 30) + "FINAL_WORD")})
	if err != nil {
		t.Fatal(err)
	}
	doc.Pages, doc.Language = 1, "PDF text"
	m.preview.result = previewResult{doc: doc}
	m.preview.loading = false
	m = resize(m, 40, 18)
	m, _ = sendKey(m, "tab")
	m, _ = sendKey(m, "G")
	view := m.View()
	assertFits(t, view, 40, 18)
	if !strings.Contains(view, "FINAL_WORD") {
		t.Fatalf("PDF wrapping/scrolling lost final text: %s", view)
	}
}
