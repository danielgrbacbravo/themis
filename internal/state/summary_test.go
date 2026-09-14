package state

import (
	"testing"
	"time"
)

func TestComputeCourseSummaryAggregatesDeadlinesAndSubmissionState(t *testing.T) {
	now := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	st := NewEmptyState()
	st.Nodes = map[string]Node{
		"url:course": {
			ID:       "url:course",
			Title:    "ICGV",
			Kind:     "course",
			ChildIDs: []string{"url:a", "url:b", "url:c"},
		},
		"url:a": {
			ID:        "url:a",
			Title:     "Ray Tracer",
			Kind:      "assignment",
			ParentIDs: []string{"url:course"},
			Details: map[string]any{
				"config": map[string]any{"end_iso": "2026-06-01", "end_display": "2026-06-01 23:59"},
				"stats":  map[string]any{"summary": map[string]any{"status": "passed"}},
			},
		},
		"url:b": {
			ID:        "url:b",
			Title:     "Volume Data",
			Kind:      "assignment",
			ParentIDs: []string{"url:course"},
			Details: map[string]any{
				"config": map[string]any{"end_iso": "2026-06-01"},
				"links":  map[string]any{"status_page": "https://themis.housing.rug.nl/stats/icgv/b"},
			},
		},
		"url:c": {
			ID:        "url:c",
			Title:     "Old Task",
			Kind:      "assignment",
			ParentIDs: []string{"url:course"},
			Details: map[string]any{
				"config": map[string]any{"end_iso": "2026-05-20T23:59:00Z"},
				"links":  map[string]any{"status_page": "https://themis.housing.rug.nl/stats/icgv/c"},
			},
		},
	}

	summary := ComputeCourseSummary(st, "url:course", now)
	if summary.TotalAssignments != 3 {
		t.Fatalf("total assignments = %d, want 3", summary.TotalAssignments)
	}
	if summary.SubmittedAssignments != 1 || summary.PassedAssignments != 1 {
		t.Fatalf("submitted/passed = %d/%d, want 1/1", summary.SubmittedAssignments, summary.PassedAssignments)
	}
	if summary.CompletionPercent != 33 {
		t.Fatalf("completion = %d, want 33", summary.CompletionPercent)
	}
	if len(summary.UpcomingDeadlines) != 1 {
		t.Fatalf("upcoming groups = %d, want 1", len(summary.UpcomingDeadlines))
	}
	group := summary.UpcomingDeadlines[0]
	if group.DaysUntil != 4 || len(group.Assignments) != 2 {
		t.Fatalf("deadline group = %d days/%d assignments, want 4/2", group.DaysUntil, len(group.Assignments))
	}
	if len(summary.OverdueAssignments) != 1 || summary.OverdueAssignments[0].Title != "Old Task" {
		t.Fatalf("unexpected overdue assignments: %#v", summary.OverdueAssignments)
	}
}

func TestAssignmentDueDateReadsLegacyTooltipCache(t *testing.T) {
	cfg := map[string]any{
		"deadline:_you_can_still_submit_after_this_date,_but_you_may_be_penalized.": "Thu Sep 17 2026 09:00:00 GMT+0200\n\t\t2026-09-17T07:00:00.000Z",
		"end:_you_can_no_longer_submit_after_this_date.":                            "Fri Sep 18 2026 09:00:00 GMT+0200\n\t2026-09-18T07:00:00.000Z",
	}
	due, display := assignmentDueDate(map[string]any{"config": cfg})
	if due.UTC().Format(time.RFC3339) != "2026-09-17T07:00:00Z" {
		t.Fatalf("legacy deadline not recovered: %v", due)
	}
	if display != "Thu Sep 17 2026 09:00:00 GMT+0200" {
		t.Fatalf("display contains tooltip: %q", display)
	}
	if _, changed := cfg["deadline"]; changed {
		t.Fatal("rendering mutated cached config")
	}
}

func TestDeadlineParsingFallbacksAndTimeZones(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Local
	time.Local = loc
	defer func() { time.Local = old }()
	for _, tc := range []struct {
		name     string
		config   map[string]any
		expected string
	}{
		{"deadline wins", map[string]any{"deadline_iso": "2026-09-17T07:00:00Z", "end_iso": "2026-09-18T07:00:00Z"}, "2026-09-17T07:00:00Z"},
		{"invalid iso uses display", map[string]any{"deadline_iso": "invalid", "deadline_display": "Thu Sep 17 2026 09:00:00 GMT+0200"}, "2026-09-17T07:00:00Z"},
		{"end only", map[string]any{"end_iso": "2026-09-18T07:00:00Z"}, "2026-09-18T07:00:00Z"},
		{"unparseable deadline uses cutoff", map[string]any{"deadline_iso": "none", "end_iso": "2026-09-18T07:00:00Z"}, "2026-09-18T07:00:00Z"},
		{"zone-less display is local", map[string]any{"deadline_display": "2026-09-17 09:00"}, "2026-09-17T07:00:00Z"},
		{"unavailable", map[string]any{"deadline_display": "None"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := assignmentDueDate(map[string]any{"config": tc.config})
			if tc.expected == "" {
				if !got.IsZero() {
					t.Fatal(got)
				}
				return
			}
			if got.UTC().Format(time.RFC3339) != tc.expected {
				t.Fatalf("got %v, want %s", got, tc.expected)
			}
		})
	}
}

func TestDaysUntilUsesCalendarDaysAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Local
	time.Local = loc
	defer func() { time.Local = old }()
	now := time.Date(2026, 3, 29, 0, 0, 0, 0, loc)
	item := AssignmentDeadline{DueAt: time.Date(2026, 3, 30, 9, 0, 0, 0, loc)}
	if got := item.DaysUntil(now); got != 1 {
		t.Fatalf("spring-forward day counted as %d days", got)
	}
	if got := (AssignmentDeadline{DueAt: now}).DaysUntil(item.DueAt); got != -1 {
		t.Fatalf("previous day counted as %d days", got)
	}
}

func TestDeadlineEarlierTodayIsOverdue(t *testing.T) {
	now := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	st := NewEmptyState()
	st.Nodes["lab"] = Node{ID: "lab", Kind: "assignment", Details: map[string]any{"config": map[string]any{"deadline_iso": "2026-09-17T07:00:00Z"}}}
	summary := ComputeCourseSummary(st, "lab", now)
	if len(summary.OverdueAssignments) != 1 || len(summary.UpcomingDeadlines) != 0 {
		t.Fatalf("elapsed deadline still upcoming: %+v", summary)
	}
}
