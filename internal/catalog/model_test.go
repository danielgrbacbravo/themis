package catalog

import (
	"testing"
	"themis-cli/internal/state"
)

func TestCoverageAndUnknownSubmissionAreExplicit(t *testing.T) {
	st := state.NewEmptyState()
	st.BaseURL = "https://example.test"
	st.CatalogRootURL = st.BaseURL + "/course"
	n := state.Node{ID: "ex", CanonicalURL: st.BaseURL + "/course/2026-2027/pc/lab7/e01", Kind: "assignment", ChildrenHydrated: true, Details: map[string]any{"links": map[string]string{"status_page": st.BaseURL + "/stats/e01"}}}
	st.Nodes[n.ID] = n
	if GradingState(n) != "unknown" {
		t.Fatal("missing grading data became unsubmitted")
	}
	if CoverageFor(st, st.BaseURL+"/course/2026-2027/pc").Complete {
		t.Fatal("unknown course root presented as fully discovered")
	}
	if Describe(st, state.Node{Kind: "assignment"}).Role != "unknown" {
		t.Fatal("unloaded node presented as exercise")
	}
}
