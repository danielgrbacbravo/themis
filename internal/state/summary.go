package state

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

func ComputeCourseSummary(st State, rootNodeID string, now time.Time) CourseSummary {
	summary := CourseSummary{RootNodeID: rootNodeID}
	if rootNodeID == "" || len(st.Nodes) == 0 {
		return summary
	}

	now = now.UTC()
	assignments := make([]AssignmentDeadline, 0)
	walkTaskLeaves(st, rootNodeID, func(node Node) {
		item := assignmentDeadlineFromNode(node)
		assignments = append(assignments, item)
		summary.TotalAssignments++
		if item.Submitted {
			summary.SubmittedAssignments++
		}
		if item.Passed {
			summary.PassedAssignments++
		}
	})

	if summary.TotalAssignments > 0 {
		summary.CompletionPercent = int(math.Round(float64(summary.SubmittedAssignments) * 100 / float64(summary.TotalAssignments)))
	}

	groups := map[int][]AssignmentDeadline{}
	groupDates := map[int]string{}
	for _, item := range assignments {
		if item.DueAt.IsZero() {
			continue
		}
		if item.DueAt.Before(now) {
			if !item.Submitted {
				summary.OverdueAssignments = append(summary.OverdueAssignments, item)
			}
			continue
		}
		days := item.DaysUntil(now)
		groups[days] = append(groups[days], item)
		if groupDates[days] == "" {
			groupDates[days] = item.DueAt.Local().Format("2006-01-02")
		}
	}

	days := make([]int, 0, len(groups))
	for day := range groups {
		days = append(days, day)
	}
	sort.Ints(days)
	for _, day := range days {
		items := groups[day]
		sort.SliceStable(items, func(i, j int) bool {
			return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
		})
		summary.UpcomingDeadlines = append(summary.UpcomingDeadlines, DeadlineGroup{
			DaysUntil:   day,
			DueDate:     groupDates[day],
			Assignments: items,
		})
	}
	sort.SliceStable(summary.OverdueAssignments, func(i, j int) bool {
		return summary.OverdueAssignments[i].DueAt.Before(summary.OverdueAssignments[j].DueAt)
	})

	return summary
}

func (a AssignmentDeadline) DaysUntil(now time.Time) int {
	if a.DueAt.IsZero() {
		return 0
	}
	dueDate := dateOnly(a.DueAt.Local())
	nowDate := dateOnly(now.Local())
	return int(dueDate.Sub(nowDate).Hours() / 24)
}

func isSummaryAssignment(node Node) bool {
	if len(node.ChildIDs) > 0 {
		return false
	}
	switch node.Kind {
	case "course", "year", "catalog":
		return false
	}
	if node.Kind == "assignment" {
		return true
	}
	if statusPageLinkFromDetails(node.Details) != "" {
		return true
	}
	if _, ok := node.Details["stats"]; ok {
		return true
	}
	return false
}

func assignmentDeadlineFromNode(node Node) AssignmentDeadline {
	status, submitted, passed := assignmentSubmissionState(node.Details)
	dueAt, dueDisplay := assignmentDueDate(node.Details)
	return AssignmentDeadline{
		NodeID:     node.ID,
		Title:      displayTitle(node),
		URL:        node.CanonicalURL,
		DueAt:      dueAt,
		DueDisplay: dueDisplay,
		Status:     status,
		Submitted:  submitted,
		Passed:     passed,
	}
}

func assignmentDueDate(details map[string]any) (time.Time, string) {
	cfg := AssignmentConfig(details)
	// Deadline is the on-time submission deadline; End is when submissions close.
	if due, display := ConfigDate(cfg, "deadline"); !due.IsZero() {
		return due, display
	}
	return ConfigDate(cfg, "end")
}

// AssignmentConfig also recognizes keys produced by older scrapers, which
// included hidden tooltip descriptions after the visible label's colon.
// Return a copy so rendering never mutates the cached state.
func AssignmentConfig(details map[string]any) map[string]any {
	raw := mapValue(details, "config")
	cfg := make(map[string]any, len(raw))
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		label := normalizeStatus(strings.SplitN(key, ":", 2)[0])
		if _, exists := cfg[label]; !exists || !strings.Contains(key, ":") {
			cfg[label] = raw[key]
		}
	}
	return cfg
}

// ConfigDate accepts the structured fields from new scrapes and mixed visible
// text/ISO timestamps from old caches, preferring the timestamp with its zone.
func ConfigDate(cfg map[string]any, key string) (time.Time, string) {
	display := stringValue(cfg, key+"_display")
	if display == "" {
		display = stringValue(cfg, key)
	}
	display = strings.TrimSpace(strings.SplitN(display, "\n", 2)[0])
	for _, candidate := range []string{stringValue(cfg, key+"_iso"), stringValue(cfg, key+"_display"), stringValue(cfg, key)} {
		if parsed, ok := parseDueTime(candidate); ok {
			if display == "" {
				display = parsed.Local().Format("2006-01-02 15:04 MST")
			}
			return parsed, display
		}
	}
	return time.Time{}, display
}

func assignmentSubmissionState(details map[string]any) (string, bool, bool) {
	summary := mapValue(mapValue(details, "stats"), "summary")
	status := normalizeStatus(stringValue(summary, "status"))
	if status == "" {
		status = normalizeStatus(stringValue(summary, "status_text"))
	}
	grade := strings.TrimSpace(stringValue(summary, "grade"))
	if status == "" && grade != "" {
		status = "graded"
	}
	if status == "" && statusPageLinkFromDetails(details) != "" {
		status = "not_submitted"
	}
	passed := status == "passed" || status == "accepted"
	submitted := false
	switch status {
	case "", "not_submitted", "not submitted", "never":
		submitted = false
	default:
		submitted = true
	}
	if grade != "" {
		submitted = true
	}
	return status, submitted, passed
}

func parseDueTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
		"02 Jan 2006 15:04",
		"02 January 2006 15:04",
		"Jan 2, 2006 15:04",
		"January 2, 2006 15:04",
		"Mon Jan 02 2006 15:04:05 GMT-0700",
		"Mon Jan 2 2006 15:04:05 GMT-0700",
	}
	// Legacy values contain a visible date plus a hidden RFC3339 tooltip.
	for _, token := range strings.Fields(raw) {
		if parsed, err := time.Parse(time.RFC3339, token); err == nil {
			return parsed, true
		}
	}
	raw = strings.TrimSpace(strings.SplitN(raw, " (", 2)[0])
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	// Compare calendar dates independent of 23/25-hour daylight-saving days.
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func displayTitle(node Node) string {
	if title := strings.TrimSpace(node.Title); title != "" {
		return title
	}
	if url := strings.TrimSpace(node.CanonicalURL); url != "" {
		return url
	}
	return node.ID
}

func normalizeStatus(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	raw = strings.ReplaceAll(raw, "-", "_")
	raw = strings.Join(strings.Fields(raw), "_")
	return raw
}

func statusPageLinkFromDetails(details map[string]any) string {
	links := mapValue(details, "links")
	return stringValue(links, "status_page")
}

func mapValue(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	for k, v := range m {
		if normalizeStatus(k) != normalizeStatus(key) {
			continue
		}
		switch vv := v.(type) {
		case map[string]any:
			return vv
		case map[string]string:
			out := make(map[string]any, len(vv))
			for kk, val := range vv {
				out[kk] = val
			}
			return out
		}
	}
	return nil
}

func stringValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	for k, v := range m {
		if normalizeStatus(k) != normalizeStatus(key) {
			continue
		}
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}
