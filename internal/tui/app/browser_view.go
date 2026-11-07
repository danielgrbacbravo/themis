package app

import (
	"net/url"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"themis-cli/internal/projectlink"
	"themis-cli/internal/state"
	"themis-cli/internal/tui/textutil"
	"themis-cli/internal/tui/theme"
)

var literalSearchStyle = lipgloss.NewStyle().Foreground(theme.Literal)

// Browser tasks own the surface while open. A stable header and footer keep
// input, selection, and the escape route visible as the results scroll.
func (m Model) renderBrowser(width, height int) string {
	if m.browser.overlay == "destination" {
		return m.renderDestination(width, height)
	}
	if m.browser.overlay == "search" {
		return m.renderSearch(width, height)
	}
	if m.browser.overlay == "project" {
		return m.renderProject(width, height)
	}
	return m.renderCatalog(width, height)
}

func inputTail(s string, width int) string {
	// Show the insertion point rather than clipping the newly typed text.
	runes := []rune(strings.Join(strings.Fields(s), " "))
	clipped := false
	for len(runes) > 0 && lipgloss.Width(string(runes)) > width {
		runes = runes[1:]
		clipped = true
	}
	if clipped && width > 1 {
		for len(runes) > 0 && lipgloss.Width(string(runes)) > width-1 {
			runes = runes[1:]
		}
		return "…" + string(runes)
	}
	return string(runes)
}
func browserTitle(n state.Node) string {
	title := strings.Join(strings.Fields(n.Title), " ")
	if title != "" && title != n.CanonicalURL {
		return title
	}
	u, err := url.Parse(n.CanonicalURL)
	if err != nil {
		return title
	}
	title = filepath.Base(u.Path)
	if n.Kind == "course" {
		return strings.ToUpper(title)
	}
	return strings.NewReplacer("_", " ", "-", " ").Replace(title)
}
func browserKeyHints(hints string) string {
	words := strings.Split(hints, " ")
	for i, word := range words {
		switch word {
		case "↑↓", "Enter", "Esc", "←", "/", "F1":
			words[i] = titleStyle.Render(word)
		case "·":
			words[i] = mutedStyle.Render(word)
		}
	}
	return strings.Join(words, " ")
}

func (m Model) renderProject(width, height int) string {
	b := m.browser
	title := "Project directory link"
	lines := []string{}
	add := func(label, value string) {
		if value == "" {
			value = "None"
		}
		lines = append(lines, label, secondaryStyle.Render(value), "")
	}
	if b.confirming {
		title = "Review link change"
		action := "Link selected target"
		if b.change.Remove {
			action = "Remove directory link"
		}
		lines = append(lines, sectionStyle.Render(action), "")
		if !b.change.Remove {
			add("Target", b.change.TargetURL)
		}
		add("Configuration", projectlink.ConfigPathFromRepoRoot(b.change.Directory))
		if b.change.Remove {
			lines = append(lines, "Only this file is removed. A parent link may become effective.")
		}
	} else if b.editing {
		title = "Choose link directory"
		lines = append(lines, "Existing directory", "", "> "+inputTail(b.directory, maxInt(1, width-3))+"│", "", "Enter keeps this directory.", "Escape restores the previous value.", "Ctrl+U clears the field.")
	} else {
		lines = append(lines, sectionStyle.Render("Local workspace"), "")
		add("Working directory", b.project.WorkingDir)
		add("Download destination", m.defaultDownloadDir)
		association := "No directory link"
		if b.project.ConfigPath != "" {
			association = "Directory link"
			if b.project.LinkDir != b.project.WorkingDir {
				association = "Inherited directory link"
			}
		}
		lines = append(lines, sectionStyle.Render(association), "")
		if b.project.ConfigPath != "" {
			add("Linked target", b.project.Config.LinkedRootURL)
			add("Configuration", b.project.ConfigPath)
		}
		add("Selected target", b.target)
		add("New link directory", b.directory)
		lines = append(lines, sectionStyle.Render("Actions"), "l  Link selected target in the new directory", "c  Change the effective link", "x  Remove the effective link", "/  Choose the new link directory", "p  Return to linked target", "", "A child directory link overrides its nearest parent link.")
	}
	footer := "j/k scroll · Esc close"
	if b.editing {
		footer = "Enter keep · Esc cancel"
	} else if b.confirming {
		footer = "Enter confirm · Esc cancel · j/k scroll"
	}
	header := []string{titleStyle.Render(title)}
	if height >= 8 {
		header = append(header, "")
	}
	available := maxInt(1, height-len(header)-1)
	wrapped := strings.Split(textutil.Wrap(strings.Join(lines, "\n"), width), "\n")
	offset := clamp(b.offset, 0, maxInt(0, len(wrapped)-available))
	if b.editing {
		offset = 0
	}
	end := clamp(offset+available, 0, len(wrapped))
	content := append(header, wrapped[offset:end]...)
	for len(content) < height-1 {
		content = append(content, "")
	}
	content = append(content, textutil.Truncate(footer, width))
	return textutil.Fit(strings.Join(content, "\n"), width, height)
}
