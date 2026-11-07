package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"themis-cli/internal/discovery"
	"themis-cli/internal/tui/textutil"
	"themis-cli/internal/workspace"
)

type destinationUI struct {
	service                   *workspace.Service
	root, dir, phase, message string
	folders                   []string
	cursor, offset            int
	input                     textinput.Model
	busy                      bool
	generation                uint64
	files                     []workspace.File
}
type destinationListedMsg struct {
	generation uint64
	listing    workspace.Listing
	message    string
	err        error
}
type destinationReviewedMsg struct {
	generation uint64
	files      []workspace.File
	err        error
}

func (m Model) openDestination() (tea.Model, tea.Cmd) {
	if m.destination.service == nil {
		return m.startDownload()
	}
	if m.downloadInFlight {
		m.statusText = "Download already in progress"
		return m, nil
	}
	if len(m.selectedAssets()) == 0 {
		m.statusText = "Select files first"
		return m, nil
	}
	root := m.browser.project.LinkDir
	if root == "" {
		root = m.browser.project.WorkingDir
	}
	if root == "" {
		m.statusText = "Download folder unavailable: no working directory"
		return m, nil
	}
	dir := m.downloadDirs[m.selectedNodeID]
	if dir != "" && !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	if dir == "" && len(m.downloadDirs) == 0 {
		dir = m.defaultDownloadDir
	}
	if dir == "" {
		dir = root
	}
	m.destination.root = root
	m.destination.dir = dir
	m.destination.phase = "select"
	m.destination.message = ""
	m.destination.files = nil
	m.destination.folders = nil
	m.destination.cursor = 0
	m.destination.offset = 0
	m.browser.overlay = "destination"
	return m.loadDestination(dir, "", true)
}
func (m Model) loadDestination(dir, create string, fallback bool) (tea.Model, tea.Cmd) {
	d := &m.destination
	d.busy = true
	d.generation++
	generation, service, root, current := d.generation, d.service, d.root, d.dir
	return m, func() tea.Msg {
		var listing workspace.Listing
		var err error
		if create != "" {
			listing, err = service.Create(root, current, create)
		} else {
			listing, err = service.List(root, dir)
		}
		message := ""
		if err != nil && fallback {
			message = "Saved folder unavailable; choose a destination."
			listing, err = service.List(root, root)
		}
		return destinationListedMsg{generation, listing, message, err}
	}
}
func (m Model) reviewDestination() (tea.Model, tea.Cmd) {
	d := &m.destination
	d.busy = true
	d.generation++
	generation, service, root, dir := d.generation, d.service, d.root, d.dir
	paths := discovery.AssetRelativePaths(m.selectedAssets())
	return m, func() tea.Msg {
		files, err := service.Review(root, dir, paths)
		return destinationReviewedMsg{generation, files, err}
	}
}
func (m Model) destinationMessage(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case destinationListedMsg:
		d := &m.destination
		if msg.generation != d.generation || m.browser.overlay != "destination" {
			return m, nil, true
		}
		d.busy = false
		if msg.err != nil {
			d.message = msg.err.Error()
			return m, nil, true
		}
		d.root, d.dir, d.folders = msg.listing.Root, msg.listing.Directory, msg.listing.Folders
		d.cursor = 0
		d.offset = 0
		d.phase = "select"
		d.message = msg.message
		return m, nil, true
	case destinationReviewedMsg:
		d := &m.destination
		if msg.generation != d.generation || m.browser.overlay != "destination" {
			return m, nil, true
		}
		d.busy = false
		if msg.err != nil {
			d.message = msg.err.Error()
			return m, nil, true
		}
		d.files = msg.files
		d.phase = "review"
		d.offset = 0
		d.message = ""
		return m, nil, true
	}
	return m, nil, false
}
func (m Model) updateDestination(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := &m.destination
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	if key == "esc" {
		if d.phase == "select" || d.busy {
			d.generation++
			d.busy = false
			m.browser.overlay = ""
		} else {
			d.phase = "select"
			d.message = ""
		}
		return m, nil
	}
	if d.busy {
		return m, nil
	}
	if d.phase == "path" || d.phase == "create" {
		if key == "enter" {
			value := strings.TrimSpace(d.input.Value())
			if value == "" {
				d.message = "Enter a folder name or path"
				return m, nil
			}
			if d.phase == "create" {
				return m.loadDestination("", value, false)
			}
			if !filepath.IsAbs(value) {
				value = filepath.Join(d.dir, value)
			}
			return m.loadDestination(value, "", false)
		}
		return m.updateDestinationInput(msg)
	}
	if d.phase == "review" {
		switch key {
		case "enter":
			m.defaultDownloadDir = d.dir
			m.downloadPlan = append([]workspace.File(nil), d.files...)
			m.downloadRoot = d.root
			m.browser.overlay = ""
			return m.startDownload()
		case "up", "k":
			d.offset = maxInt(0, d.offset-1)
		case "down", "j":
			d.offset++
		case "pgup":
			d.offset = maxInt(0, d.offset-10)
		case "pgdown":
			d.offset += 10
		}
		return m, nil
	}
	switch key {
	case "up", "k":
		d.cursor = maxInt(0, d.cursor-1)
	case "down", "j":
		d.cursor = clamp(d.cursor+1, 0, len(d.folders))
	case "pgup":
		d.cursor = maxInt(0, d.cursor-10)
	case "pgdown":
		d.cursor = clamp(d.cursor+10, 0, len(d.folders))
	case "g", "home":
		d.cursor = 0
	case "G", "end":
		d.cursor = len(d.folders)
	case "enter":
		if d.cursor == 0 {
			return m.reviewDestination()
		}
		return m.loadDestination(filepath.Join(d.dir, d.folders[d.cursor-1]), "", false)
	case "right", "l":
		if d.cursor > 0 {
			return m.loadDestination(filepath.Join(d.dir, d.folders[d.cursor-1]), "", false)
		}
	case "left", "h", "backspace":
		if d.dir != d.root {
			return m.loadDestination(filepath.Dir(d.dir), "", false)
		}
	case "n", "/":
		d.phase = "path"
		if key == "n" {
			d.phase = "create"
		}
		d.input = textinput.New()
		d.input.Prompt = "> "
		d.input.PromptStyle = titleStyle
		d.input.CharLimit = 1024
		d.input.Width = maxInt(1, m.width-14)
		d.input.SetCursorMode(textinput.CursorStatic)
		d.input.Focus()
		d.message = ""
	case "r":
		return m.loadDestination(d.dir, "", false)
	}
	return m, nil
}

func (m Model) updateDestinationInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.destination.input, cmd = m.destination.input.Update(msg)
	return m, cmd
}
func (m Model) renderDestination(width, height int) string {
	d := m.destination
	if width <= 0 || height <= 0 {
		return ""
	}
	header := "Download to folder"
	footer := "↑↓ select   Enter open / use   ← parent   n new folder   / path   Esc back"
	rel, err := filepath.Rel(d.root, d.dir)
	if err != nil {
		rel = d.dir
	}
	lines := []string{titleStyle.Render(header), textutil.Truncate(d.root, width), literalSearchStyle.Render(filepath.ToSlash(rel)), ""}
	if height < 9 {
		lines = lines[:1]
	}
	body := []string{}
	switch d.phase {
	case "path", "create":
		prompt := "Go to folder (inside this project)"
		if d.phase == "create" {
			prompt = "Create folder · nested paths are supported"
		}
		input := d.input
		resizeSearchInput(&input, maxInt(1, width-3))
		body = append(body, prompt, input.View())
		footer = "Enter confirm   Esc cancel"
	case "review":
		lines[0] = titleStyle.Render(fmt.Sprintf("Review download · %d files", len(d.files)))
		if len(lines) > 2 {
			lines[2] = literalSearchStyle.Render(filepath.ToSlash(rel))
		} else {
			body = append(body, "To: "+inputTail(d.dir, maxInt(1, width-4)))
		}
		for _, file := range d.files {
			label := filepath.ToSlash(file.Relative)
			if file.Exists {
				label += " · " + staleStyle.Render("replace existing")
			}
			body = append(body, label)
		}
		footer = "Enter download   ↑↓ scroll   Esc change folder"
	default:
		entries := append([]string{"Use this folder"}, d.folders...)
		visible := maxInt(1, height-len(lines)-2)
		start := visibleOffset(0, d.cursor, len(entries), visible)
		for i := start; i < len(entries) && i < start+visible; i++ {
			label := entries[i]
			if i > 0 {
				label += "/"
			}
			prefix := "  "
			if i == d.cursor {
				prefix = titleStyle.Render("> ")
				label = titleStyle.Render(label)
			}
			body = append(body, prefix+label)
		}
	}
	if d.busy {
		body = []string{infoStyle.Render("Loading…")}
	}
	available := maxInt(1, height-len(lines)-2)
	if d.phase == "review" {
		start := clamp(d.offset, 0, maxInt(0, len(body)-available))
		body = body[start:]
	}
	if len(body) > available {
		body = body[:available]
	}
	lines = append(lines, body...)
	for len(lines) < height-2 {
		lines = append(lines, "")
	}
	lines = append(lines, textutil.Truncate(errorStyle.Render(d.message), width))
	if width < 58 {
		footer = "Enter confirm · n new · ← · Esc"
		if d.phase == "review" {
			footer = "Enter download · Esc back"
		}
		if d.phase == "path" || d.phase == "create" {
			footer = "Enter confirm · Esc cancel"
		}
	}
	if width < 30 {
		footer = "Enter · n new · Esc"
		if d.phase == "review" {
			footer = "Enter download · Esc"
		}
		if d.phase == "path" || d.phase == "create" {
			footer = "Enter · Esc"
		}
	}
	lines = append(lines, textutil.Truncate(browserKeyHints(footer), width))
	return textutil.Fit(strings.Join(lines, "\n"), width, height)
}
