package discovery

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"

	"themis-cli/internal/state"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testClientFromMap(t *testing.T, pages map[string]string, hits map[string]int) *http.Client {
	t.Helper()
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			url := req.URL.String()
			hits[url]++
			body, ok := pages[url]
			if !ok {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader("not found")),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(body)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}
}

func TestRefreshNode_DeepNodeDoesNotFetchRoot(t *testing.T) {
	base := "https://themis.housing.rug.nl"
	deep := base + "/course/2025-2026/os/lab3"
	child := base + "/course/2025-2026/os/lab3/task"
	root := base + "/course"

	pages := map[string]string{
		deep: `<html><body>
		<section class="assignment"><div class="sec-heading"><h3 class="sec-title">/ <a href="/course/">Courses</a> / <a href="/course/2025-2026">2025-2026</a> / <a href="/course/2025-2026/os/lab3">Lab 3</a></h3></div></section>
		<div class="subsec round shade ass-children"><ul class="round">
		<li><span class="ass-link"><a href="/course/2025-2026/os/lab3/task" data-navhref="/api/navigation/2025-2026/os/lab3/task">Task</a></span></li>
		</ul></div>
		</body></html>`,
		child: `<html><body>
		<section class="assignment"><div class="sec-heading"><h3 class="sec-title">/ <a href="/course/">Courses</a> / <a href="/course/2025-2026">2025-2026</a> / <a href="/course/2025-2026/os/lab3/task">Task</a></h3></div></section>
		<div class="subsec round shade ass-children"><ul class="round"></ul></div>
		</body></html>`,
		root: `<html><body><div class="subsec round shade ass-children"><ul class="round"></ul></div></body></html>`,
	}
	hits := map[string]int{}
	client := testClientFromMap(t, pages, hits)
	service := NewService(base)
	progressURLs := []string{}
	service.Progress = func(progress RefreshProgress) {
		progressURLs = append(progressURLs, progress.URL)
	}
	st := state.NewEmptyState()

	result, err := service.RefreshNode(client, &st, deep, 1)
	if err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	if result.FetchedNodes != 2 {
		t.Fatalf("unexpected fetched nodes: %d", result.FetchedNodes)
	}
	if len(progressURLs) != 2 || progressURLs[0] != deep || progressURLs[1] != child {
		t.Fatalf("unexpected progress URLs: %#v", progressURLs)
	}
	if hits[root] != 0 {
		t.Fatalf("expected root not to be fetched, hits=%d", hits[root])
	}
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %#v", result.Errors)
	}
}

func TestRefreshNode_ReplacesChildrenAndTracksRemovedEdges(t *testing.T) {
	base := "https://themis.housing.rug.nl"
	course := base + "/course/2025-2026/os"
	oldChild := base + "/course/2025-2026/os/lab1"
	newChild := base + "/course/2025-2026/os/lab2"

	service := NewService(base)
	st := state.NewEmptyState()

	pages1 := map[string]string{
		course: `<html><body>
		<section class="assignment"><div class="sec-heading"><h3 class="sec-title">/ <a href="/course/">Courses</a> / <a href="/course/2025-2026">2025-2026</a> / <a href="/course/2025-2026/os">Operating Systems</a></h3></div></section>
		<div class="subsec round shade ass-children"><ul class="round"><li><span class="ass-link"><a href="/course/2025-2026/os/lab1" data-navhref="/api/navigation/2025-2026/os/lab1">Lab 1</a></span></li></ul></div>
		</body></html>`,
		oldChild: `<html><body><section class="assignment"><div class="sec-heading"><h3 class="sec-title">/ <a href="/course/2025-2026/os/lab1">Lab 1</a></h3></div></section><div class="subsec round shade ass-children"><ul class="round"></ul></div></body></html>`,
	}
	hits1 := map[string]int{}
	_, err := service.RefreshNode(testClientFromMap(t, pages1, hits1), &st, course, 1)
	if err != nil {
		t.Fatalf("first refresh failed: %v", err)
	}

	pages2 := map[string]string{
		course: `<html><body>
		<section class="assignment"><div class="sec-heading"><h3 class="sec-title">/ <a href="/course/">Courses</a> / <a href="/course/2025-2026">2025-2026</a> / <a href="/course/2025-2026/os">Operating Systems</a></h3></div></section>
		<div class="subsec round shade ass-children"><ul class="round"><li><span class="ass-link"><a href="/course/2025-2026/os/lab2" data-navhref="/api/navigation/2025-2026/os/lab2">Lab 2</a></span></li></ul></div>
		</body></html>`,
		newChild: `<html><body><section class="assignment"><div class="sec-heading"><h3 class="sec-title">/ <a href="/course/2025-2026/os/lab2">Lab 2</a></h3></div></section><div class="subsec round shade ass-children"><ul class="round"></ul></div></body></html>`,
	}
	hits2 := map[string]int{}
	result, err := service.RefreshNode(testClientFromMap(t, pages2, hits2), &st, course, 1)
	if err != nil {
		t.Fatalf("second refresh failed: %v", err)
	}
	if result.RemovedEdges != 1 {
		t.Fatalf("expected removed edge count 1, got %d", result.RemovedEdges)
	}

	courseID, _, _ := state.NodeIDFromURL(course)
	oldID, _, _ := state.NodeIDFromURL(oldChild)
	newID, _, _ := state.NodeIDFromURL(newChild)

	if strings.Join(st.Nodes[courseID].ChildIDs, ",") != newID {
		t.Fatalf("course children not replaced: %#v", st.Nodes[courseID].ChildIDs)
	}
	if contains(st.Nodes[oldID].ParentIDs, courseID) {
		t.Fatalf("old child still has parent edge")
	}
	if !contains(st.Nodes[newID].ParentIDs, courseID) {
		t.Fatalf("new child missing parent edge")
	}
	tombsAny, ok := st.Nodes[courseID].Details[state.TombstonesDetailsKey]
	if !ok {
		t.Fatalf("expected tombstones in parent details")
	}
	tombs, ok := tombsAny.([]state.Tombstone)
	if !ok || len(tombs) == 0 || tombs[len(tombs)-1].ChildID != oldID {
		t.Fatalf("unexpected tombstones payload: %#v", tombsAny)
	}
}

func TestRefreshNode_ExtractsMetadata(t *testing.T) {
	base := "https://themis.housing.rug.nl"
	course := base + "/course/2025-2026/os"
	pages := map[string]string{
		course: `<html><body>
		<section class="assignment"><div class="sec-heading"><h3 class="sec-title">/ <a href="/course/">Courses</a> / <a href="/course/2025-2026">2025-2026</a> / <a href="/course/2025-2026/os">Operating Systems</a></h3></div></section>
		<div class="subsec round help shade"><a href="/stats/2025-2026/os" class="iconize status">Status</a></div>
		<div class="subsec round shade ass-children"><ul class="round"></ul></div>
		<div class="cfg-container round ass-config">
		<div class="cfg-line"><span class="cfg-key">Leading Submission:</span><span class="cfg-val">latest</span></div>
		<div class="cfg-line"><span class="cfg-key">End:</span><span class="cfg-val"><span class="tip-text">2026-08-31T21:59:59.000Z</span>Mon Aug 31 2026 23:59:59 GMT+0200</span></div>
		</div>
		<div><a href="/file/course/%40tests/1.in">1.in</a></div>
		<div><a href="/file/course/%40tests/1.out">1.out</a></div>
		<div><a href="/download/os/lab3.zip">lab3.zip</a></div>
		<p class="ass-description">Course summary</p>
        <footer><a href="/about">About Themis</a>
        <a href="https://www.rug.nl/info/privacy" target="_blank">Privacy Policy</a></footer>
		</body></html>`,
	}

	service := NewService(base)
	hits := map[string]int{}
	client := testClientFromMap(t, pages, hits)
	st := state.NewEmptyState()

	_, err := service.RefreshNode(client, &st, course, 0)
	if err != nil {
		t.Fatalf("refresh failed: %v", err)
	}

	nodeID, _, _ := state.NodeIDFromURL(course)
	node := st.Nodes[nodeID]
	if node.NavAPIURL == "" {
		t.Fatalf("expected nav_api_url")
	}
	if node.Details == nil {
		t.Fatalf("expected details")
	}
	configAny, ok := node.Details["config"]
	if !ok {
		t.Fatalf("expected config details")
	}
	config, ok := configAny.(map[string]any)
	if !ok {
		t.Fatalf("invalid config type: %T", configAny)
	}
	if config["leading_submission"] != "latest" {
		t.Fatalf("unexpected leading_submission: %#v", config["leading_submission"])
	}
	linksAny, ok := node.Details["links"]
	if !ok {
		t.Fatalf("expected links details")
	}
	links := linksAny.(map[string]string)
	if links["status_page"] != "https://themis.housing.rug.nl/stats/2025-2026/os" {
		t.Fatalf("unexpected status_page: %#v", links["status_page"])
	}
	if len(node.Assets) != 3 {
		t.Fatalf("expected 3 assets, got %d", len(node.Assets))
	}
	if node.Assets[0].URL != "https://themis.housing.rug.nl/file/course/%40tests/1.in" || node.Assets[0].Kind != "file" {
		t.Fatalf("unexpected first asset: %#v", node.Assets[0])
	}
	if node.Assets[2].Kind != "archive" {
		t.Fatalf("unexpected archive asset kind: %#v", node.Assets[2])
	}
}

func TestRefreshNode_AssignmentStatsSummaryAndFallback(t *testing.T) {
	base := "https://themis.housing.rug.nl"
	assignment := base + "/course/2025-2026/os/lab5/5_file_writer"
	statsURL := base + "/stats/2025-2026/os/lab5/5_file_writer"

	assignmentPage := `<html><body>
	<section class="assignment"><div class="sec-heading"><h3 class="sec-title">/ <a href="/course/">Courses</a> / <a href="/course/2025-2026">2025-2026</a> / <a href="/course/2025-2026/os">Operating Systems</a> / <a href="/course/2025-2026/os/lab5/5_file_writer">Exercise 5: File Writer</a></h3></div></section>
	<div class="subsec round help shade"><a href="/stats/2025-2026/os/lab5/5_file_writer" class="iconize status">Status</a></div>
	<div class="subsec round shade ass-children"><ul class="round"></ul></div>
	</body></html>`

	statsPage := `<html><body>
	<section class="status border passed">
		<div class="sec-heading fill passed round"><h3 class="sec-title fill status-icon passed">Status: <a class="fill passed" href="/stats/2025-2026/os/lab5/5_file_writer/@submissions/s5482585">Exercise 5: File Writer</a></h3></div>
		<div class="sec-body round">
			<div class="cfg-container round">
				<div class="cfg-line"><span class="cfg-key">Assignment:</span><span class="cfg-val"><a href="/course/2025-2026/os/lab5/5_file_writer">Exercise 5: File Writer</a></span></div>
				<div class="cfg-line"><span class="cfg-key">Group:</span><span class="cfg-val">D. Grbac Bravo</span></div>
				<div class="cfg-line"><span class="cfg-key">Status:</span><span class="cfg-val"><i class="status-icon passed"></i><strong>passed</strong>: Passed all test cases</span></div>
				<div class="cfg-line"><span class="cfg-key">Grade:</span><span class="cfg-val">20.00</span></div>
				<div class="cfg-group-title">Counts</div>
				<div class="cfg-line"><span class="cfg-key">Total:</span><span class="cfg-val">8</span></div>
				<div class="cfg-line"><span class="cfg-key">Passed:</span><span class="cfg-val">1</span></div>
				<div class="cfg-group-title">Submissions</div>
				<div class="cfg-line"><span class="cfg-key">Leading:</span><span class="cfg-val"><i class="icon passed"></i><a href="/submission/2025-2026/os/lab5/5_file_writer/@submissions/s5482585/s5482585-7">Exercise 5: File Writer / s5482585-7</a></span></div>
			</div>
		</div>
		<div class="subsec-act"><a class="button iconize download fill accent round" href="/download/2025-2026/os/lab5/5_file_writer/@submissions/s5482585?whose=group&group=s5482585">Download</a></div>
	</section>
	</body></html>`

	st := state.NewEmptyState()
	service := NewService(base)

	hits1 := map[string]int{}
	pages1 := map[string]string{
		assignment: assignmentPage,
		statsURL:   statsPage,
	}
	if _, err := service.RefreshNode(testClientFromMap(t, pages1, hits1), &st, assignment, 0); err != nil {
		t.Fatalf("first refresh failed: %v", err)
	}

	nodeID, _, _ := state.NodeIDFromURL(assignment)
	node := st.Nodes[nodeID]
	links := node.Details["links"].(map[string]string)
	if links["status_page"] != statsURL {
		t.Fatalf("unexpected status page: %#v", links["status_page"])
	}
	statsAny, ok := node.Details["stats"]
	if !ok {
		t.Fatalf("expected stats details")
	}
	statsDetails, ok := statsAny.(map[string]any)
	if !ok {
		t.Fatalf("invalid stats type: %T", statsAny)
	}
	if statsDetails["status_page"] != statsURL {
		t.Fatalf("unexpected stats status page: %#v", statsDetails["status_page"])
	}
	counts, ok := statsDetails["counts"].(map[string]any)
	if !ok {
		t.Fatalf("invalid counts type: %T", statsDetails["counts"])
	}
	if counts["total"] != 8 || counts["passed"] != 1 {
		t.Fatalf("unexpected counts: %#v", counts)
	}

	// Stats fetch should be best effort: assignment refresh still succeeds and
	// previously cached stats should remain available.
	hits2 := map[string]int{}
	pages2 := map[string]string{
		assignment: assignmentPage,
	}
	result, err := service.RefreshNode(testClientFromMap(t, pages2, hits2), &st, assignment, 0)
	if err != nil {
		t.Fatalf("second refresh failed: %v", err)
	}
	if len(result.Errors) == 0 {
		t.Fatalf("expected stats warning on second refresh")
	}
	node = st.Nodes[nodeID]
	if _, ok := node.Details["stats"]; !ok {
		t.Fatalf("expected cached stats to be preserved")
	}
}

func TestRefreshNode_AssignmentChildGetsDerivedStatusPage(t *testing.T) {
	base := "https://themis.housing.rug.nl"
	course := base + "/course/2025-2026/os"
	child := base + "/course/2025-2026/os/lab5/5_file_writer"

	pages := map[string]string{
		course: `<html><body>
		<section class="assignment"><div class="sec-heading"><h3 class="sec-title">/ <a href="/course/">Courses</a> / <a href="/course/2025-2026">2025-2026</a> / <a href="/course/2025-2026/os">Operating Systems</a></h3></div></section>
		<div class="subsec round shade ass-children"><ul class="round"><li><span class="ass-link"><a href="/course/2025-2026/os/lab5/5_file_writer">Exercise 5: File Writer</a></span></li></ul></div>
		</body></html>`,
	}

	service := NewService(base)
	st := state.NewEmptyState()

	if _, err := service.RefreshNode(testClientFromMap(t, pages, map[string]int{}), &st, course, 0); err != nil {
		t.Fatalf("refresh failed: %v", err)
	}

	childID, _, _ := state.NodeIDFromURL(child)
	childNode := st.Nodes[childID]
	if childNode.Kind != "assignment" {
		t.Fatalf("expected assignment child kind, got %q", childNode.Kind)
	}
	links := childNode.Details["links"].(map[string]string)
	wantStatus := "https://themis.housing.rug.nl/stats/2025-2026/os/lab5/5_file_writer"
	if links["status_page"] != wantStatus {
		t.Fatalf("unexpected derived status page: %#v", links["status_page"])
	}
}

func TestRefreshNode_DepthZeroFetchesDerivedStatsForTargetAssignment(t *testing.T) {
	base := "https://themis.housing.rug.nl"
	assignment := base + "/course/2025-2026/os/lab5/4_recursive_traversal"
	statsURL := base + "/stats/2025-2026/os/lab5/4_recursive_traversal"

	pages := map[string]string{
		assignment: `<html><body>
		<section class="assignment"><div class="sec-heading"><h3 class="sec-title">/ <a href="/course/">Courses</a> / <a href="/course/2025-2026">2025-2026</a> / <a href="/course/2025-2026/os">Operating Systems</a> / <a href="/course/2025-2026/os/lab5/4_recursive_traversal">Exercise 4: Recursive Directory Traversal</a></h3></div></section>
		<div class="subsec round shade ass-children"><ul class="round"></ul></div>
		</body></html>`,
		statsURL: `<html><body>
		<section class="status border passed">
			<div class="sec-heading fill passed round"><h3 class="sec-title fill status-icon passed">Status: <a class="fill passed" href="/stats/2025-2026/os/lab5/4_recursive_traversal/@submissions/s5482585">Exercise 4: Recursive Directory Traversal</a></h3></div>
			<div class="sec-body round">
				<div class="cfg-container round">
					<div class="cfg-line"><span class="cfg-key">Status:</span><span class="cfg-val"><i class="status-icon passed"></i><strong>passed</strong>: Passed all test cases</span></div>
					<div class="cfg-line"><span class="cfg-key">Grade:</span><span class="cfg-val">20.00</span></div>
					<div class="cfg-group-title">Counts</div>
					<div class="cfg-line"><span class="cfg-key">Total:</span><span class="cfg-val">8</span></div>
					<div class="cfg-line"><span class="cfg-key">Passed:</span><span class="cfg-val">8</span></div>
				</div>
			</div>
		</section>
		</body></html>`,
	}

	service := NewService(base)
	st := state.NewEmptyState()

	result, err := service.RefreshNode(testClientFromMap(t, pages, map[string]int{}), &st, assignment, 0)
	if err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected warnings/errors: %#v", result.Errors)
	}

	nodeID, _, _ := state.NodeIDFromURL(assignment)
	node := st.Nodes[nodeID]
	statsAny, ok := node.Details["stats"]
	if !ok {
		t.Fatalf("expected stats to be fetched for depth-0 target assignment")
	}
	stats := statsAny.(map[string]any)
	if stats["status_page"] != statsURL {
		t.Fatalf("unexpected status_page: %#v", stats["status_page"])
	}
}

func contains(list []string, target string) bool {
	for _, v := range list {
		if v == target {
			return true
		}
	}
	return false
}

func TestExtractDetailsDatesWithTooltipLabels(t *testing.T) {
	html := `<div class="ass-config">
 <div class="cfg-line"><span class="cfg-key"><span class="tip">Deadline:</span><span role="tooltip" class="tip-text">You can still submit after this date, but may be penalized.</span></span>
 <span class="cfg-val"><span class="tip" data-title="2026-09-17T07:00:00.000Z">Thu Sep 17 2026 09:00:00 GMT+0200</span><span class="tip-text">2026-09-17T07:00:00.000Z</span></span></div>
 <div class="cfg-line"><span class="cfg-key">End:<span class="tip-text">You can no longer submit after this date.</span></span>
 <span class="cfg-val"><span data-title="2026-09-18T07:00:00.000Z">Fri Sep 18 2026 09:00:00 GMT+0200</span></span></div>
 <div class="cfg-line"><span class="cfg-key">Start:<span class="tip-text">You can start submitting after this date.</span></span>
 <span class="cfg-val"><time datetime="2026-09-10T09:00:00Z">Thu Sep 10 2026 11:00:00 GMT+0200</time></span></div>
 <div class="cfg-line"><span class="cfg-key">Leading Submission:<span class="tip-text">The submission used for grading.</span></span><span class="cfg-val">latest</span></div>
 </div>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	details := extractDetails(doc, "https://themis.housing.rug.nl/course/example/lab2")
	cfg := details["config"].(map[string]any)
	for key, expected := range map[string]string{
		"deadline_iso":       "2026-09-17T07:00:00.000Z",
		"deadline_display":   "Thu Sep 17 2026 09:00:00 GMT+0200",
		"end_iso":            "2026-09-18T07:00:00.000Z",
		"start_iso":          "2026-09-10T09:00:00Z",
		"leading_submission": "latest",
	} {
		if cfg[key] != expected {
			t.Errorf("%s = %v, want %s", key, cfg[key], expected)
		}
	}
	for key := range cfg {
		if strings.Contains(key, ":") {
			t.Errorf("tooltip in config key: %s", key)
		}
	}
	st := state.NewEmptyState()
	st.Nodes["lab2"] = state.Node{ID: "lab2", Kind: "assignment", Details: details}
	summary := state.ComputeCourseSummary(st, "lab2", time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC))
	if len(summary.UpcomingDeadlines) != 1 || summary.UpcomingDeadlines[0].DaysUntil != 3 {
		t.Fatalf("extracted deadline did not reach summary: %+v", summary)
	}
	if got := summary.UpcomingDeadlines[0].Assignments[0].DueAt.UTC().Format(time.RFC3339); got != "2026-09-17T07:00:00Z" {
		t.Fatalf("used cutoff instead of deadline: %s", got)
	}
}
