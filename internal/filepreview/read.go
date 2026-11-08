package filepreview

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

type ReadOptions struct{ StartPage, Pages, StartLine, Lines int }
type Page struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
}
type ReadResult struct {
	Kind            string `json:"kind"`
	Text            string `json:"text,omitempty"`
	Pages           []Page `json:"pages,omitempty"`
	StartLine       int    `json:"start_line,omitempty"`
	NextLine        *int   `json:"next_line,omitempty"`
	NextPage        *int   `json:"next_page,omitempty"`
	SourceTruncated bool   `json:"source_truncated"`
	Complete        bool   `json:"complete"`
}

func ReadContext(ctx context.Context, name string, content Content, opts ReadOptions) (ReadResult, error) {
	r := ReadResult{SourceTruncated: content.Truncated}
	if opts.StartPage < 1 || opts.Pages < 1 || opts.Pages > 20 || opts.StartLine < 1 || opts.Lines < 1 || opts.Lines > 2000 {
		return r, fmt.Errorf("invalid document range: pages 1..20, lines 1..2000, start positions >=1")
	}
	if IsPDF(name, content.Data) {
		if content.Truncated || len(content.Data) > MaxPDFBytes {
			return r, fmt.Errorf("complete PDF required (8 MiB limit)")
		}
		executable, err := exec.LookPath("pdftotext")
		if err != nil {
			return r, fmt.Errorf("PDF reading requires Poppler pdftotext")
		}
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, executable, "-layout", "-enc", "UTF-8", "-eol", "unix", "-f", fmt.Sprint(opts.StartPage), "-l", fmt.Sprint(opts.StartPage+opts.Pages), "-", "-")
		command.Stdin = bytes.NewReader(content.Data)
		stdout := &limitedCapture{limit: 1024 * 1024}
		stderr := &limitedCapture{limit: 2048}
		command.Stdout = stdout
		command.Stderr = stderr
		if err := command.Run(); err != nil {
			if ctx.Err() != nil {
				return r, ctx.Err()
			}
			return r, fmt.Errorf("PDF text extraction failed; the PDF may be damaged, encrypted, or the requested page is outside the document")
		}
		if stdout.truncated {
			return r, fmt.Errorf("PDF range exceeds 1 MiB text limit; request fewer pages")
		}
		pages := strings.Split(stdout.buf.String(), "\f")
		if len(pages) > 0 && strings.TrimSpace(pages[len(pages)-1]) == "" {
			pages = pages[:len(pages)-1]
		}
		more := len(pages) > opts.Pages
		if more {
			pages = pages[:opts.Pages]
			next := opts.StartPage + len(pages)
			r.NextPage = &next
		}
		hasText := false
		r.Kind = "pdf"
		r.Pages = []Page{}
		for i, text := range pages {
			text = strings.TrimSpace(text)
			if text != "" {
				hasText = true
			}
			r.Pages = append(r.Pages, Page{Number: opts.StartPage + i, Text: text})
		}
		if !hasText {
			return r, fmt.Errorf("no extractable text in this page range; the document may require OCR")
		}
		r.Complete = opts.StartPage == 1 && !more
		return r, nil
	}
	if bytes.IndexByte(content.Data, 0) >= 0 || !utf8.Valid(content.Data) {
		return r, fmt.Errorf("binary or non-UTF-8 asset; download it instead")
	}
	text := strings.ReplaceAll(string(content.Data), "\r\n", "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	start := opts.StartLine - 1
	if start > len(lines) {
		return r, fmt.Errorf("line range is outside the document")
	}
	end := start + opts.Lines
	if end > len(lines) {
		end = len(lines)
	}
	r.Kind = "text"
	r.StartLine = opts.StartLine
	r.Text = strings.Join(lines[start:end], "\n")
	if len(r.Text) > 1024*1024 {
		return r, fmt.Errorf("text range exceeds 1 MiB; request fewer lines")
	}
	if end < len(lines) {
		next := end + 1
		r.NextLine = &next
	}
	r.Complete = start == 0 && end == len(lines) && !content.Truncated
	return r, nil
}
