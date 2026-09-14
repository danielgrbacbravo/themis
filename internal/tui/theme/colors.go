// Package theme uses semantic ANSI palette slots. The terminal owns their
// actual colors, so live appearance/palette changes require no application
// theme detection or cached light/dark colors. Body text and backgrounds use
// terminal defaults; selection uses a pointer and bold text without a background.
package theme

import "github.com/charmbracelet/lipgloss"

const (
	Error   lipgloss.Color = "1"
	Success lipgloss.Color = "2"
	Warning lipgloss.Color = "3"
	Accent  lipgloss.Color = "4"
	Literal lipgloss.Color = "5"
	Info    lipgloss.Color = "6"
	Muted   lipgloss.Color = "8"
)
