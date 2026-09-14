package theme

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestPaletteColorsRemainANSIReferencesEvenInTrueColorTerminals(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(profile)
	for _, c := range []struct {
		color    lipgloss.Color
		sequence string
	}{
		{Error, "\x1b[31m"}, {Success, "\x1b[32m"}, {Warning, "\x1b[33m"},
		{Literal, "\x1b[35m"}, {Accent, "\x1b[34m"}, {Info, "\x1b[36m"}, {Muted, "\x1b[90m"},
	} {
		out := lipgloss.NewStyle().Foreground(c.color).Render("text")
		if !strings.Contains(out, c.sequence) {
			t.Fatalf("color %s resolved to a fixed color: %q", c.color, out)
		}
	}
}
