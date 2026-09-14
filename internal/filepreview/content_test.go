package filepreview

import (
	"github.com/alecthomas/chroma/v2"
	"strings"
	"testing"
	"unicode/utf8"
)

func plain(doc Document) string {
	lines := []string{}
	for _, line := range doc.Lines {
		var b strings.Builder
		for _, t := range line {
			b.WriteString(t.Value)
		}
		lines = append(lines, b.String())
	}
	return strings.Join(lines, "\n")
}
func TestPrepareHighlightsCodeAndPreservesTestData(t *testing.T) {
	code := "// comment\nint main(void) {\n    return 42;\n}"
	doc, err := Prepare("main.c", Content{Data: []byte(code)})
	if err != nil || doc.Language != "C" || plain(doc) != code {
		t.Fatalf("code changed: %+v %v %q", doc, err, plain(doc))
	}
	keyword, number := false, false
	for _, line := range doc.Lines {
		for _, token := range line {
			keyword = keyword || token.Type.InCategory(chroma.Keyword)
			number = number || token.Type.InSubCategory(chroma.LiteralNumber)
		}
	}
	if !keyword || !number {
		t.Fatal("source was not tokenized")
	}
	for _, name := range []string{"1.in", "1.out", "expected.ans", "unknown.extension"} {
		doc, err := Prepare(name, Content{Data: []byte("  1  2\n\n3  ")})
		if err != nil || doc.Language != "Plain text" || plain(doc) != "  1  2\n\n3  " {
			t.Fatalf("test whitespace changed: %q %v", plain(doc), err)
		}
	}
}
func TestPreviewLimitsAndTerminalSafety(t *testing.T) {
	doc, err := Prepare("test.txt", Content{Data: []byte("\x1b[2J\x1b]52;c;secret\a\r\n\tvalue")})
	if err != nil || strings.ContainsAny(plain(doc), "\x1b\a\r\t") || !strings.Contains(plain(doc), "\n    value") {
		t.Fatalf("unsafe text: %q %v", plain(doc), err)
	}
	for _, data := range [][]byte{[]byte("\x00abc"), {0xff, 0xfe}, []byte("%PDF-1.4\nexample")} {
		if _, err := Prepare("file", Content{Data: data}); err == nil {
			t.Fatal("expected unsupported file error")
		}
	}
	doc, err = Prepare("many.txt", Content{Data: []byte(strings.Repeat("line\n", 1000))})
	if err != nil || len(doc.Lines) != MaxLines || !doc.Truncated {
		t.Fatal("line bound missing")
	}
	doc, err = Prepare("long.txt", Content{Data: []byte(strings.Repeat("x", MaxBytes*2))})
	if err != nil || !doc.Truncated || doc.Bytes != MaxBytes || len(plain(doc)) > maxLineRunes+3 {
		t.Fatal("size bound missing")
	}
	doc, err = Prepare("unicode.txt", Content{Data: []byte("abc\xe7\x95"), Truncated: true})
	if err != nil || !utf8.ValidString(plain(doc)) || plain(doc) != "abc" {
		t.Fatalf("partial character handling failed: %q %v", plain(doc), err)
	}
}

func TestTruncationDoesNotHideInvalidEncoding(t *testing.T) {
	if _, err := Prepare("file.txt", Content{Data: []byte("abc\xff"), Truncated: true}); err == nil {
		t.Fatal("invalid byte was treated as a partial UTF-8 rune")
	}
}
