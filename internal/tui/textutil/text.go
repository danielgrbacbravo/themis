// Package textutil bounds terminal content before it reaches the renderer.
package textutil

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"
	"github.com/muesli/reflow/wrap"
	"github.com/rivo/uniseg"
)

var sgr = regexp.MustCompile(`^\x1b\[[0-9;:]*m`)

// Truncate preserves complete color sequences and grapheme clusters. Widths
// use the same cell measurement as the panel renderer, never bytes or runes.
func Truncate(content string, width int) string {
	if width <= 0 {
		return ""
	}
	line := strings.Split(content, "\n")[0]
	line = strings.ReplaceAll(line, "\t", "    ")
	line = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\x1b' {
			return -1
		}
		return r
	}, line)
	if lipgloss.Width(line) <= width {
		return line
	}
	var out strings.Builder
	remaining := width - 1
	for len(line) > 0 {
		if seq := sgr.FindString(line); seq != "" {
			out.WriteString(seq)
			line = line[len(seq):]
			continue
		}
		end := strings.IndexByte(line, '\x1b')
		if end < 0 {
			end = len(line)
		}
		if end == 0 {
			break
		} // Never emit a partial/unknown escape sequence.
		clusters := uniseg.NewGraphemes(line[:end])
		for clusters.Next() {
			cluster := clusters.Str()
			cells := lipgloss.Width(cluster)
			if cells > remaining {
				return finish(out.String())
			}
			out.WriteString(cluster)
			remaining -= cells
		}
		line = line[end:]
	}
	return finish(out.String())
}

func finish(s string) string {
	s += "…"
	if strings.Contains(s, "\x1b[") {
		s += "\x1b[0m"
	}
	return s
}

// Wrap performs both word wrapping and hard wrapping of long paths/URLs.
func Wrap(content string, width int) string {
	if width <= 0 {
		return ""
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", " ")
	content = strings.ReplaceAll(content, "\t", "    ")
	return wrap.String(wordwrap.String(content, width), width)
}

func Fit(content string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		lines[i] = Truncate(lines[i], width)
	}
	return strings.Join(lines, "\n")
}
