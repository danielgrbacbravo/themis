package app

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"themis-cli/internal/filepreview"
	"themis-cli/internal/state"
	"themis-cli/internal/tui/textutil"
)

func (m Model) previewAssets() []state.AssetRef {
	if m.mode == "upload" {
		entry := m.uploadEntry()
		if entry == nil {
			return nil
		}
		kind := "file"
		if entry.dir {
			kind = "directory"
		}
		u := url.URL{Scheme: "file", Path: entry.path}
		return []state.AssetRef{{Name: entry.name, Path: entry.path, URL: u.String(), Kind: kind}}
	}
	if m.mode == "results" {
		artifacts := m.submit.result.Artifacts
		if len(artifacts) == 0 {
			return nil
		}
		a := artifacts[clamp(m.submit.resultCursor, 0, len(artifacts)-1)]
		u, _ := url.Parse(a.URL)
		name := a.Name
		if u != nil {
			parts := strings.Split(u.Path, "/")
			name = parts[len(parts)-1]
		}
		return []state.AssetRef{{Name: name, URL: a.URL, Kind: "file"}}
	}
	return m.selectedNodeAssets()
}
func (m Model) previewCursor() int {
	if m.mode == "upload" || m.mode == "results" {
		return 0
	}
	return m.downloadCursor
}
func readLocalPreview(ctx context.Context, asset state.AssetRef) (filepreview.Content, error) {
	info, err := os.Lstat(asset.Path)
	if err != nil {
		return filepreview.Content{}, err
	}
	if !info.Mode().IsRegular() {
		return filepreview.Content{}, fmt.Errorf("preview requires a regular file")
	}
	file, err := os.Open(asset.Path)
	if err != nil {
		return filepreview.Content{}, err
	}
	defer file.Close()
	limit := filepreview.MaxBytes
	if filepreview.IsPDF(asset.Name, nil) {
		limit = filepreview.MaxPDFBytes
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit+1)))
	if err != nil {
		return filepreview.Content{}, err
	}
	if err = ctx.Err(); err != nil {
		return filepreview.Content{}, err
	}
	truncated := len(data) > limit
	if truncated {
		data = data[:limit]
	}
	return filepreview.Content{Data: data, Truncated: truncated}, nil
}
func (m Model) submissionKeys() string {
	switch m.mode {
	case "upload":
		return "space select · l/h folder · / path · L language · enter review · tab preview"
	case "review":
		return "j/k scroll · L language · tab Back/Submit · enter activate · esc back"
	case "sending":
		return "Upload in progress · esc stops transfer (sent bytes cannot be undone)"
	case "results":
		return "j/k artifact · pgup/pgdown results · tab preview · r resume · esc browse"
	}
	return ""
}
func (m Model) renderSubmissionBody(l screenLayout) string {
	height := l.bodyHeight - 2
	if m.mode == "review" || m.mode == "sending" {
		return renderPanel(m.renderReview(l.width-2, height), l.width, l.bodyHeight, true)
	}
	if l.width < 80 {
		content := m.renderSubmissionList(l.width-2, height)
		if m.preview.focused {
			content = m.renderPreview(l.width-2, height)
		}
		return renderPanel(content, l.width, l.bodyHeight, true)
	}
	leftWidth := clamp(l.width/2, 32, 60)
	rightWidth := l.width - leftWidth
	return lipgloss.JoinHorizontal(lipgloss.Top, renderPanel(m.renderSubmissionList(leftWidth-2, height), leftWidth, l.bodyHeight, !m.preview.focused), renderPanel(m.renderPreview(rightWidth-2, height), rightWidth, l.bodyHeight, m.preview.focused))
}
func (m Model) renderSubmissionList(width, height int) string {
	if height <= 0 {
		return ""
	}
	if m.mode == "results" {
		return m.renderResultList(width, height)
	}
	if m.submit.formError != "" {
		return textutil.Fit(titleStyle.Render("Submission unavailable")+"\n"+textutil.Wrap(m.submit.formError, width)+"\n\nEsc returns to browsing", width, height)
	}
	headerCount, footerCount := downloadChrome(height)
	header := []string{titleStyle.Render("Upload · local files"), safePreviewName(m.submit.dir), fmt.Sprintf("Language: %s · L change", m.submit.language), "Space selects · Enter reviews", ""}
	if m.submit.editing {
		header[0] = titleStyle.Render("Choose directory")
		if headerCount < 2 {
			return textutil.Fit(m.submit.input.View(), width, height)
		}
		header[1] = m.submit.input.View()
	}
	lines := append([]string{}, header[:headerCount]...)
	rows := maxInt(1, height-headerCount-footerCount)
	start := visibleOffset(m.submit.offset, m.submit.cursor, len(m.submit.entries), rows)
	end := clamp(start+rows, 0, len(m.submit.entries))
	for i := start; i < end; i++ {
		entry := m.submit.entries[i]
		cursor := " "
		if i == m.submit.cursor {
			cursor = ">"
		}
		mark := " "
		if _, ok := m.submit.selected[entry.path]; ok {
			mark = "x"
		}
		icon := renderFileIcon(state.AssetRef{Name: entry.name})
		name := safePreviewName(entry.name)
		if entry.dir {
			icon = infoStyle.Render("▸ ")
			name += "/"
		}
		lines = append(lines, fmt.Sprintf("%s [%s] %s %s", cursor, mark, icon, name))
	}
	if len(m.submit.entries) == 0 {
		lines = append(lines, "No local files")
	}
	if footerCount > 0 {
		lines = append(lines, fmt.Sprintf("Selected: %d files/folders", len(m.submit.selected)))
	}
	return textutil.Fit(strings.Join(lines, "\n"), width, height)
}
func (m Model) renderReview(width, height int) string {
	if height <= 0 {
		return ""
	}
	p := m.submit.prepared
	if p == nil {
		return textutil.Fit("No prepared submission", width, height)
	}
	title := "Review submission"
	if m.mode == "sending" {
		title = "Uploading reviewed files"
	}
	lines := []string{headingStyle.Render(p.Form.Title), safePreviewName(p.Form.AssignmentURL), "Account: " + p.Form.Account, "Group: " + p.Form.Group, "Language: " + m.submit.language, fmt.Sprintf("%d files · %d bytes", len(p.Files), p.TotalBytes)}
	if p.Form.MaxFiles > 0 {
		lines = append(lines, fmt.Sprintf("Limit: %d files", p.Form.MaxFiles))
	}
	if p.Form.MaxBytes > 0 {
		lines = append(lines, fmt.Sprintf("Combined size limit: %d bytes", p.Form.MaxBytes))
	}
	for _, key := range []string{"start", "deadline", "end", "submitting cooldown", "submission limit"} {
		if v := p.Form.Dates[key]; v != "" {
			lines = append(lines, key+": "+v)
		}
	}
	lines = append(lines, "", headingStyle.Render("Exact files to upload"))
	for _, f := range p.Files {
		lines = append(lines, renderFileIcon(state.AssetRef{Name: f.Name})+" "+safePreviewName(f.Name)+fmt.Sprintf("  %d B", f.Size))
	}
	if m.mode == "sending" {
		sent := int64(0)
		if m.submit.sent != nil {
			sent = m.submit.sent.Load()
		}
		return textutil.Fit(titleStyle.Render(title)+"\n"+fmt.Sprintf("Transferred %d/%d file bytes\nWaiting for server acceptance…", sent, p.TotalBytes), width, height)
	}
	wrapped := strings.Split(textutil.Wrap(strings.Join(lines, "\n"), width), "\n")
	rows := maxInt(1, height-2)
	start := clamp(m.submit.offset, 0, maxInt(0, len(wrapped)-rows))
	end := clamp(start+rows, 0, len(wrapped))
	result := []string{titleStyle.Render(title)}
	result = append(result, wrapped[start:end]...)
	buttons := "> Back     Submit"
	if m.submit.armed {
		buttons = "  Back   > Submit"
	}
	result = append(result, selectedStyle.Render(buttons))
	return textutil.Fit(strings.Join(result, "\n"), width, height)
}
func (m Model) renderResultList(width, height int) string {
	r := m.submit.result
	if r.SubmissionURL == "" {
		lines := []string{titleStyle.Render("Submission results"), m.statusText}
		if m.submit.receipt.StatusURL != "" {
			lines = append(lines, m.submit.receipt.StatusURL, "r Inspect status without resubmitting")
		}
		return textutil.Fit(textutil.Wrap(strings.Join(lines, "\n"), width), width, height)
	}
	lines := []string{headingStyle.Render(r.Title), "Status: " + r.RawStatus, r.Message}
	if m.submit.receipt.State == "unknown" {
		lines = append([]string{"Upload outcome unknown; this result may be an earlier submission.", ""}, lines...)
	}
	if r.Score != "" {
		lines = append(lines, "Score: "+r.Score)
	}
	if r.KnownTotal > 0 {
		lines = append(lines, fmt.Sprintf("Tests complete: %d/%d", r.Completed, r.KnownTotal))
	} else {
		lines = append(lines, "Test progress: not available")
	}
	lines = append(lines, "", headingStyle.Render("Tests"))
	for _, test := range r.Tests {
		lines = append(lines, taskIcon(test.Status, state.StatusOK)+" "+test.Name+" · "+test.RawStatus)
		if test.Hint != "" {
			lines = append(lines, "  "+test.Hint)
		}
		if test.Message != "" {
			lines = append(lines, "  "+test.Message)
		}
	}
	lines = append(lines, "", headingStyle.Render("Logs and output · Tab previews"))
	artifactStart := len(lines)
	for i, a := range r.Artifacts {
		cursor := "  "
		if i == m.submit.resultCursor {
			cursor = "> "
		}
		lines = append(lines, cursor+a.Name)
	}
	rows := maxInt(1, height-1)
	start := clamp(m.submit.resultOffset, 0, maxInt(0, len(lines)-rows))
	// Artifact navigation brings the selected artifact into view; page keys can
	// independently scroll the test summary above it.
	if m.submit.artifactNavigation && len(r.Artifacts) > 0 {
		start = visibleOffset(start, artifactStart+m.submit.resultCursor, len(lines), rows)
	}
	end := clamp(start+rows, 0, len(lines))
	out := []string{titleStyle.Render("Submission results · r resume")}
	out = append(out, lines[start:end]...)
	return textutil.Fit(strings.Join(out, "\n"), width, height)
}
