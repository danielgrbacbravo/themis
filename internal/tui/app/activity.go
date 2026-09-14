package app

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type refreshOnOpenMsg struct{}
type summaryTickMsg time.Time
type activityTickMsg struct{ generation uint64 }

func summaryTick() tea.Cmd {
	return tea.Tick(time.Minute, func(t time.Time) tea.Msg { return summaryTickMsg(t) })
}

func activityTick(generation uint64) tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return activityTickMsg{generation} })
}

func (m Model) animating() bool {
	return m.refreshInFlight || m.downloadInFlight || m.preview.loading || m.now.Before(m.successUntil)
}

func (m Model) activityIcon() string {
	if m.refreshInFlight || m.downloadInFlight || m.preview.loading {
		if m.reducedMotion {
			return infoStyle.Render("↻")
		}
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		return infoStyle.Render(frames[m.animationFrame%len(frames)])
	}
	if m.now.Before(m.successUntil) {
		return okStyle.Render("✓")
	}
	return mutedStyle.Render("·")
}

func (m Model) renderDownloadMarker(url string) string {
	if m.downloadStatus[url] == "active" {
		return m.activityIcon()
	}
	return colorDownloadMarker(m.downloadMarker(url))
}
