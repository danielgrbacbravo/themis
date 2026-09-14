package state

// TaskProgress counts unique leaf assignments. Containers have no independent
// completion requirement: they complete when every known subtask has passed.
// Missing references or cycles prevent an incomplete graph from looking done.
type TaskProgress struct {
	Total     int
	Submitted int
	Passed    int
	Complete  bool
}

func ComputeTaskProgress(st State, rootNodeID string) TaskProgress {
	progress := TaskProgress{}
	completeGraph := walkTaskLeaves(st, rootNodeID, func(node Node) {
		_, submitted, passed := assignmentSubmissionState(node.Details)
		progress.Total++
		if submitted {
			progress.Submitted++
		}
		if passed {
			progress.Passed++
		}
	})
	progress.Complete = completeGraph && progress.Total > 0 && progress.Passed == progress.Total
	return progress
}

func walkTaskLeaves(st State, rootNodeID string, visit func(Node)) bool {
	seen, active := map[string]bool{}, map[string]bool{}
	complete := true
	var walk func(string)
	walk = func(id string) {
		if active[id] {
			complete = false
			return
		}
		if seen[id] {
			return
		}
		seen[id] = true
		node, exists := st.Nodes[id]
		if !exists {
			complete = false
			return
		}
		if len(node.ChildIDs) == 0 {
			if isSummaryAssignment(node) {
				visit(node)
			}
			return
		}
		active[id] = true
		for _, child := range node.ChildIDs {
			walk(child)
		}
		delete(active, id)
	}
	walk(rootNodeID)
	return complete
}
