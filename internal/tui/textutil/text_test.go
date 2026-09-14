package textutil

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

func TestTruncatePreservesANSIAndGraphemeBoundaries(t *testing.T) {
	cases := []struct {
		text  string
		width int
		want  string
	}{
		{"abcdef", 4, "abc…"},
		{"界界界", 4, "界…"},
		{"e\u0301clair", 3, "e\u0301c…"},
		{"👩‍💻hello", 2, "…"},
		{"\x1b[31mabcdef\x1b[0m", 4, "\x1b[31mabc…\x1b[0m"},
		{"abc", 0, ""},
		{"abc", 1, "…"},
	}
	for _, tc := range cases {
		got := Truncate(tc.text, tc.width)
		if got != tc.want {
			t.Errorf("Truncate(%q, %d) = %q; want %q", tc.text, tc.width, got, tc.want)
		}
		if !utf8.ValidString(got) || lipgloss.Width(got) > tc.width {
			t.Errorf("invalid truncation %q", got)
		}
	}
}

func TestWrapKeepsLongURLsAndExplicitLines(t *testing.T) {
	text := "Path: https://example.org/" + strings.Repeat("abc", 30) + "\nlast line"
	got := Wrap(text, 18)
	for _, line := range strings.Split(got, "\n") {
		if lipgloss.Width(line) > 18 {
			t.Fatalf("unbounded line: %s", line)
		}
	}
	if !strings.Contains(got, "last line") || strings.Join(strings.Fields(got), "") != strings.Join(strings.Fields(text), "") {
		t.Fatal("wrapping lost content")
	}
}
