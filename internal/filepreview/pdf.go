package filepreview

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"path"
	"strings"
	"time"
)

const MaxPDFBytes = 8 * 1024 * 1024
const maxPDFPages = 20

func IsPDF(name string, data []byte) bool {
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("%PDF-")) {
		return true
	}
	if u, err := url.Parse(name); err == nil {
		name = u.Path
	}
	return strings.EqualFold(path.Ext(name), ".pdf")
}

// limitedCapture bounds both text and diagnostics while allowing the process
// to finish. The context independently bounds extraction time and cancellation.
type limitedCapture struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (w *limitedCapture) Write(p []byte) (int, error) {
	n := len(p)
	remaining := w.limit - w.buf.Len()
	if len(p) > remaining {
		p = p[:remaining]
		w.truncated = true
	}
	_, _ = w.buf.Write(p)
	return n, nil
}

func preparePDF(ctx context.Context, content Content) (Document, error) {
	if content.Truncated || len(content.Data) > MaxPDFBytes {
		return Document{}, fmt.Errorf("PDF preview requires the complete file (limit 8 MiB); download to inspect")
	}
	executable, err := exec.LookPath("pdftotext")
	if err != nil {
		return Document{}, fmt.Errorf("PDF preview needs Poppler's pdftotext; install poppler (macOS) or poppler-utils (Linux)")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Read one extra page to know whether the displayed page limit truncated it.
	cmd := exec.CommandContext(ctx, executable, "-layout", "-enc", "UTF-8", "-eol", "unix", "-f", "1", "-l", fmt.Sprint(maxPDFPages+1), "-", "-")
	cmd.Stdin = bytes.NewReader(content.Data)
	stdout := &limitedCapture{limit: MaxBytes}
	stderr := &limitedCapture{limit: 2048}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Document{}, fmt.Errorf("PDF preview: %w", ctx.Err())
		}
		return Document{}, fmt.Errorf("cannot extract PDF text (it may be damaged or password-protected); download to inspect")
	}
	pages := strings.Split(strings.TrimSuffix(stdout.buf.String(), "\f"), "\f")
	truncated := stdout.truncated || len(pages) > maxPDFPages
	if len(pages) > maxPDFPages {
		pages = pages[:maxPDFPages]
	}
	var text strings.Builder
	foundText := false
	for i, page := range pages {
		page = strings.TrimSpace(page)
		if page != "" {
			foundText = true
		}
		if i > 0 {
			text.WriteString("\n\n")
		}
		fmt.Fprintf(&text, "── Page %d ──\n\n", i+1)
		if page == "" {
			text.WriteString("[No extractable text on this page]")
		} else {
			text.WriteString(page)
		}
	}
	if !foundText {
		return Document{}, fmt.Errorf("no extractable text in the first %d pages; this PDF may need OCR", len(pages))
	}
	doc, err := PrepareContext(ctx, "pdf-text.txt", Content{Data: []byte(text.String()), Truncated: truncated})
	if err != nil {
		return Document{}, err
	}
	doc.Language, doc.Bytes, doc.Pages = "PDF text", len(content.Data), len(pages)
	return doc, nil
}
