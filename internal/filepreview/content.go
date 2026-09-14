// Package filepreview prepares bounded, terminal-safe previews without writing files.
package filepreview

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

const MaxBytes = 64 * 1024
const MaxLines = 500
const maxLineRunes = 4096

type Content struct {
	Data      []byte
	Truncated bool
}

type Document struct {
	Lines     [][]chroma.Token
	Language  string
	Truncated bool
	Bytes     int
	Pages     int
}

// Prepare tokenizes once in a background command. Renderers only style the
// visible lines, keeping resize and animation frames independent of file size.
func Prepare(name string, content Content) (Document, error) {
	return PrepareContext(context.Background(), name, content)
}

func PrepareContext(ctx context.Context, name string, content Content) (doc Document, err error) {
	defer func() {
		if r := recover(); r != nil {
			doc = Document{}
			err = fmt.Errorf("cannot highlight this file: %v", r)
		}
	}()
	if err := ctx.Err(); err != nil {
		return doc, err
	}
	if IsPDF(name, content.Data) {
		return preparePDF(ctx, content)
	}
	data := content.Data
	if len(data) > MaxBytes {
		data = data[:MaxBytes]
		content.Truncated = true
	}
	doc.Bytes, doc.Truncated = len(data), content.Truncated
	if content.Truncated && len(data) > 0 {
		// Only drop an incomplete final rune, never an invalid encoded byte.
		start := len(data) - 1
		for start > 0 && !utf8.RuneStart(data[start]) {
			start--
		}
		if !utf8.FullRune(data[start:]) {
			data = data[:start]
		}
	}

	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return doc, fmt.Errorf("binary or non-UTF-8 file; download to inspect")
	}
	kind := http.DetectContentType(data)
	if strings.HasPrefix(kind, "image/") || strings.HasPrefix(kind, "audio/") || strings.HasPrefix(kind, "video/") || kind == "application/pdf" || kind == "application/zip" || kind == "application/x-gzip" {
		return doc, fmt.Errorf("%s file; download to inspect", strings.Split(kind, ";")[0])
	}
	source := strings.ReplaceAll(string(data), "\r\n", "\n")
	source = strings.ReplaceAll(source, "\r", "\n")
	source = strings.Map(func(r rune) rune {
		if r == '\x1b' {
			return '␛'
		}
		if r != '\n' && r != '\t' && (unicode.IsControl(r) || unicode.In(r, unicode.Cf)) {
			return '�'
		}
		return r
	}, source)
	source = strings.ReplaceAll(source, "\t", "    ")
	lines := strings.Split(strings.TrimSuffix(source, "\n"), "\n")
	if len(lines) > MaxLines {
		lines = lines[:MaxLines]
		doc.Truncated = true
	}
	for i, line := range lines {
		if utf8.RuneCountInString(line) > maxLineRunes {
			lines[i] = string([]rune(line)[:maxLineRunes]) + "…"
			doc.Truncated = true
		}
	}
	source = strings.Join(lines, "\n")
	lexer := lexers.Match(name)
	// Test output must remain literal data, even if its contents resemble code.
	switch strings.ToLower(path.Ext(name)) {
	case ".in", ".out", ".ans", ".expected", ".txt":
		lexer = nil
	}
	doc.Language = "Plain text"
	if lexer == nil {
		for _, line := range lines {
			doc.Lines = append(doc.Lines, []chroma.Token{{Type: chroma.Text, Value: line}})
		}
		return doc, nil
	}
	doc.Language = lexer.Config().Name
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, source)
	if err != nil {
		return doc, fmt.Errorf("highlight %s: %w", doc.Language, err)
	}
	doc.Lines = [][]chroma.Token{{}}
	for token := iterator(); token != chroma.EOF; token = iterator() {
		parts := strings.Split(token.Value, "\n")
		for i, part := range parts {
			if i > 0 {
				doc.Lines = append(doc.Lines, []chroma.Token{})
			}
			if part != "" {
				j := len(doc.Lines) - 1
				doc.Lines[j] = append(doc.Lines[j], chroma.Token{Type: token.Type, Value: part})
			}
		}
	}
	// Some lexers ensure a trailing newline; it is not an extra source line.
	if len(doc.Lines) > len(lines) {
		doc.Lines = doc.Lines[:len(lines)]
	}
	return doc, nil
}
