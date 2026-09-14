package app

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"themis-cli/internal/state"
	"themis-cli/internal/tui/textutil"
)

var (
	headingStyle = lipgloss.NewStyle().Bold(true)
	sectionStyle = lipgloss.NewStyle().Bold(true)
)

func progressBar(done, total, width int) string {
	width = maxInt(1, width)
	filled := 0
	if total > 0 {
		filled = clamp(done*width/total, 0, width)
	}
	return okStyle.Render(strings.Repeat("━", filled)) + mutedStyle.Render(strings.Repeat("─", width-filled))
}

func taskIcon(result string, freshness state.Status) string {
	if freshness == state.StatusError {
		return errorStyle.Render("!")
	}
	switch normalizeStatsKey(result) {
	case "passed", "accepted":
		return passStyle.Render("✓")
	case "failing", "failed", "rejected":
		return failStyle.Render("×")
	case "", "ok", "not_submitted", "not_started", "never", "unknown":
		return secondaryStyle.Render("·")
	default:
		return infoStyle.Render("◇")
	}
}

func resultLabel(raw string) string {
	result := normalizeStatsKey(raw)
	switch result {
	case "ok":
		return "Ready"
	case "in_progress":
		return "In progress"
	case "not_started":
		return "Not started"
	case "not_submitted", "never", "":
		return "Not submitted"
	case "passed", "accepted":
		return "Passed"
	case "failing", "failed", "rejected":
		return "Needs attention"
	default:
		return strings.ReplaceAll(raw, "_", " ")
	}
}

func (m Model) updatedLabel(node state.Node) string {
	if m.refreshInFlight {
		return "Updating…"
	}
	if node.LastSuccessAt == nil {
		return "Not synced yet"
	}
	age := m.now.Sub(*node.LastSuccessAt)
	if age < time.Minute {
		return "Updated just now"
	}
	if age < time.Hour {
		return fmt.Sprintf("Updated %dm ago", int(age.Minutes()))
	}
	if age < 24*time.Hour {
		return fmt.Sprintf("Updated %dh ago", int(age.Hours()))
	}
	return "Updated " + node.LastSuccessAt.Local().Format("2 Jan")
}

func detailSection(title string, width int) string {
	label := strings.ToUpper(title) + " "
	return sectionStyle.Render(label) + mutedStyle.Render(strings.Repeat("─", maxInt(0, width-lipgloss.Width(label))))
}

func detailPair(label, value string, width int) string {
	if width < 32 {
		return secondaryStyle.Render(label+"  ") + value
	}
	return secondaryStyle.Render(fmt.Sprintf("%-12s", label)) + value
}

func (m Model) renderDetails(width, height int) string {
	node := m.selectedNode()
	if node == nil {
		return "Details\nNo selection"
	}
	if m.mode == "download" {
		return m.renderDownloadPanel(*node, width, height)
	}
	inset := ""
	if width >= 28 {
		inset = " "
	}
	innerWidth := maxInt(1, width-2*len(inset))
	lines := []string{headingStyle.Render(textutil.Wrap(displayTitle(*node), innerWidth))}
	assets := m.selectedNodeAssets()
	meta := readableKind(node.Kind)
	progress := state.ComputeTaskProgress(m.st, node.ID)
	if len(node.ChildIDs) > 0 {
		noun := "tasks"
		if progress.Total == 1 {
			noun = "task"
		}
		meta += fmt.Sprintf("  ·  %d %s", progress.Total, noun)
	}
	if len(assets) > 0 {
		noun := "files"
		if len(assets) == 1 {
			noun = "file"
		}
		meta += fmt.Sprintf("  ·  %d %s", len(assets), noun)
	}
	lines = append(lines, secondaryStyle.Render(meta), "")
	result := m.nodeResultLabel(*node)
	label := resultLabel(result)
	if len(node.ChildIDs) > 0 && progress.Complete {
		label = "Completed · all subtasks passed"
	}
	lines = append(lines, taskIcon(result, node.Status)+" "+label)
	lines = append(lines, m.activityIcon()+" "+secondaryStyle.Render(m.updatedLabel(*node)))
	if node.LastError != "" {
		lines = append(lines, errorStyle.Render("! "+node.LastError))
	}

	cfg := state.AssignmentConfig(node.Details)
	deadline, _ := state.ConfigDate(cfg, "deadline")
	cutoff, _ := state.ConfigDate(cfg, "end")
	if deadline.IsZero() {
		deadline = cutoff
	}
	if !deadline.IsZero() {
		days := (state.AssignmentDeadline{DueAt: deadline}).DaysUntil(m.now)
		remaining := fmt.Sprintf("%d days remaining", days)
		switch {
		case deadline.Before(m.now):
			remaining = "Deadline passed"
		case days == 0:
			remaining = "Due today"
		case days == 1:
			remaining = "Due tomorrow"
		}
		lines = append(lines, "", detailSection("◷ Deadline", innerWidth),
			infoStyle.Render("│ ")+headingStyle.Render(deadline.Local().Format("Mon 2 Jan · 15:04")),
			infoStyle.Render("│ ")+secondaryStyle.Render(remaining))
		if !cutoff.IsZero() && !cutoff.Equal(deadline) {
			lines = append(lines, secondaryStyle.Render("Closes "+cutoff.Local().Format("Mon 2 Jan · 15:04")))
		}
	}

	if len(node.ChildIDs) > 0 && progress.Total > 0 {
		lines = append(lines, "", detailSection("Progress", innerWidth),
			progressBar(progress.Passed, progress.Total, clamp(innerWidth-16, 6, 24))+fmt.Sprintf("  %d / %d passed", progress.Passed, progress.Total))
	}

	stats := statsSummaryLines(node.Details)
	facts := []string{}
	if statsMap := stringAnyMapGetMap(node.Details, "stats"); statsMap != nil {
		counts := anyMapToStringAnyMap(statsMap["counts"])
		passed, total := stringAnyMapGetInt(counts, "passed"), stringAnyMapGetInt(counts, "total")
		if passed >= 0 && total >= 0 {
			facts = append(facts, detailPair("Tests", fmt.Sprintf("%d / %d passed", passed, total), innerWidth))
		}
	}
	for _, line := range stats {
		if strings.HasPrefix(line, "- Page:") || strings.HasPrefix(line, "- Status:") || strings.HasPrefix(line, "- Counts:") {
			continue
		}
		line = strings.TrimPrefix(line, "- ")
		pair := strings.SplitN(line, ": ", 2)
		if len(pair) == 2 {
			line = detailPair(pair[0], pair[1], innerWidth)
		}
		facts = append(facts, line)
	}
	if len(facts) > 0 {
		lines = append(lines, "", detailSection("Submission", innerWidth))
		lines = append(lines, facts...)
	} else if statusPageURL(node.Details) != "" && node.LastSuccessAt == nil {
		lines = append(lines, "", secondaryStyle.Render("Submission details will appear after syncing."))
	}

	if len(assets) > 0 {
		lines = append(lines, "", detailSection(fmt.Sprintf("Files · %d", len(assets)), innerWidth))
		limit := len(assets)
		if limit > 4 {
			limit = 4
		}
		for _, asset := range assets[:limit] {
			name := strings.Join(strings.Fields(asset.Name), " ")
			if name == "" {
				name = path.Base(asset.URL)
			}
			lines = append(lines, mutedStyle.Render("↳ ")+textutil.Truncate(name, innerWidth-2))
		}
		hint := "d  Choose files to download"
		if len(assets) > limit {
			hint = fmt.Sprintf("+%d more  ·  d to choose files", len(assets)-limit)
		}
		lines = append(lines, secondaryStyle.Render(hint))
	}

	if description, ok := node.Details["description"].(string); ok && strings.TrimSpace(description) != "" {
		lines = append(lines, "", detailSection("About", innerWidth), strings.TrimSpace(description))
	}
	if m.showSource {
		lines = append(lines, "", detailSection("Source", innerWidth))
		if node.CanonicalURL != "" {
			lines = append(lines, "Assignment  "+node.CanonicalURL)
		}
		if url := statusPageURL(node.Details); url != "" {
			lines = append(lines, "Status page  "+url)
		}
		if breadcrumb := breadcrumbString(node.Details); breadcrumb != "" {
			lines = append(lines, "Location  "+breadcrumb)
		}
		if config := configSummaryLines(node.Details); len(config) > 0 {
			lines = append(lines, config...)
		}
		if debug := statsDebugLines(node.Details); len(debug) > 0 {
			lines = append(lines, debug...)
		}
	}
	lines = append(lines, "", secondaryStyle.Render("i  "+map[bool]string{true: "Hide source details", false: "Show source details"}[m.showSource]))
	wrapped := strings.Split(textutil.Wrap(strings.Join(lines, "\n"), innerWidth), "\n")
	for i := range wrapped {
		wrapped[i] = inset + textutil.Truncate(wrapped[i], innerWidth)
	}
	return "Details\n" + strings.Join(wrapped, "\n")
}
