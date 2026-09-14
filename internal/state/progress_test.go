package state

import (
	"testing"
	"time"
)

func nestedTaskState() State {
	st := NewEmptyState()
	node := func(id string, children []string, status string) Node {
		return Node{ID: id, Title: id, Kind: "assignment", ChildIDs: children, Details: map[string]any{
			"stats":  map[string]any{"summary": map[string]any{"status": status}},
			"config": map[string]any{"deadline_iso": "2026-09-10T07:00:00Z"},
		}}
	}
	st.Nodes["course"] = node("course", []string{"lab", "pending"}, "not_submitted")
	course := st.Nodes["course"]
	course.Kind = "course"
	st.Nodes[course.ID] = course
	st.Nodes["lab"] = node("lab", []string{"a", "nested"}, "not_submitted")
	st.Nodes["nested"] = node("nested", []string{"b"}, "not_submitted")
	st.Nodes["a"] = node("a", nil, "passed")
	st.Nodes["b"] = node("b", nil, "accepted")
	st.Nodes["pending"] = node("pending", nil, "not_submitted")
	return st
}

func TestParentsCompleteFromSubtasksAndOnlyLeavesCount(t *testing.T) {
	st := nestedTaskState()
	for _, id := range []string{"lab", "nested"} {
		p := ComputeTaskProgress(st, id)
		if !p.Complete || p.Passed != p.Total {
			t.Fatalf("%s not complete: %+v", id, p)
		}
	}
	p := ComputeTaskProgress(st, "course")
	if p.Total != 3 || p.Submitted != 2 || p.Passed != 2 || p.Complete {
		t.Fatalf("wrong course progress: %+v", p)
	}
	summary := ComputeCourseSummary(st, "course", time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC))
	if summary.TotalAssignments != 3 || summary.PassedAssignments != 2 || summary.CompletionPercent != 67 {
		t.Fatalf("parents included in summary: %+v", summary)
	}
	if len(summary.OverdueAssignments) != 1 || summary.OverdueAssignments[0].NodeID != "pending" {
		t.Fatalf("completed parent generated overdue warning: %+v", summary.OverdueAssignments)
	}
	if got, _, _ := assignmentSubmissionState(st.Nodes["lab"].Details); got != "not_submitted" {
		t.Fatal("derived completion overwrote server data")
	}
}

func TestParentCompletionReactsToLeafResultChanges(t *testing.T) {
	st := nestedTaskState()
	leaf := st.Nodes["b"]
	leaf.Details = map[string]any{"stats": map[string]any{"summary": map[string]any{"status": "failed"}}}
	st.Nodes[leaf.ID] = leaf
	if ComputeTaskProgress(st, "lab").Complete {
		t.Fatal("failed subtask marked parent complete")
	}
	leaf.Details = map[string]any{"stats": map[string]any{"summary": map[string]any{"grade": "17.5"}}}
	st.Nodes[leaf.ID] = leaf
	if ComputeTaskProgress(st, "lab").Complete {
		t.Fatal("grade without pass result marked parent complete")
	}
}

func TestProgressHandlesSharedLeavesAndIncompleteGraphs(t *testing.T) {
	st := nestedTaskState()
	lab := st.Nodes["lab"]
	lab.ChildIDs = append(lab.ChildIDs, "b")
	st.Nodes[lab.ID] = lab
	if p := ComputeTaskProgress(st, "lab"); p.Total != 2 || !p.Complete {
		t.Fatalf("shared leaf counted twice: %+v", p)
	}
	for _, ref := range []string{"missing", "lab"} {
		t.Run(ref, func(t *testing.T) {
			copy := nestedTaskState()
			lab := copy.Nodes["lab"]
			lab.ChildIDs = append(lab.ChildIDs, ref)
			copy.Nodes[lab.ID] = lab
			if p := ComputeTaskProgress(copy, "lab"); p.Complete || p.Total != 2 {
				t.Fatalf("incomplete graph considered complete: %+v", p)
			}
		})
	}
	empty := st.Nodes["course"]
	empty.ChildIDs = nil
	st.Nodes[empty.ID] = empty
	if p := ComputeTaskProgress(st, "course"); p.Total != 0 || p.Complete {
		t.Fatalf("empty course counted as task: %+v", p)
	}
}
