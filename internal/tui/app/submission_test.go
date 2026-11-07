package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"themis-cli/internal/filepreview"
	"themis-cli/internal/state"
	"themis-cli/internal/submission"
)

func uploadTestService(t *testing.T) (*submission.Service, *atomic.Int32) {
	t.Helper()
	posts := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			http.Redirect(w, r, "/submission/2025-2026/os/@submissions/group/1", 303)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/submission/") {
			fmt.Fprint(w, `<section class="submission passed" id="/2025-2026/os/@submissions/group/1"><div class="sec-title"><a>Exercise</a></div></section>`)
			return
		}
		fmt.Fprint(w, `<h1>Exercise</h1><a href="/user">Student</a><div class="cfg-line"><span class="cfg-key">Group</span><span class="cfg-val">Group</span></div><form data-submitform method="post" action="/submit/2025-2026/os?_csrf=fake" data-files="50" data-filesize="500000" data-languages="c"><input type="file" name="files[]"><input type="file" name="folders[]"><input type="hidden" name="judgeLanguage" value="c"></form>`)
	}))
	t.Cleanup(server.Close)
	return &submission.Service{BaseURL: server.URL, Client: func() (*http.Client, error) { return server.Client(), nil }, Store: &submission.Store{Path: filepath.Join(t.TempDir(), "submissions.json")}}, posts
}
func uploadTestModel(t *testing.T, s *submission.Service) Model {
	t.Helper()
	st := baseStateForTUI(time.Now())
	n := st.Nodes["url:root"]
	n.CanonicalURL = s.BaseURL + "/course/2025-2026/os"
	st.Nodes[n.ID] = n
	m, e := NewModel(Config{State: st, SubmissionService: s, ReducedMotion: true})
	if e != nil {
		t.Fatal(e)
	}
	return resize(m, 110, 32)
}
func applySubmissionWork(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	msg := workMessage(cmd)
	if msg == nil {
		t.Fatal("missing work")
	}
	next, _ := m.Update(msg)
	return next.(Model)
}

func TestUploadRequiresExplicitReviewAction(t *testing.T) {
	s, posts := uploadTestService(t)
	m := uploadTestModel(t, s)
	defer m.stopSubmission()
	m, cmd := sendKey(m, "u")
	if m.mode != "upload" || posts.Load() != 0 {
		t.Fatal("u sent files")
	}
	m = applySubmissionWork(t, m, cmd)
	dir := t.TempDir()
	path := filepath.Join(dir, "main.c")
	if e := os.WriteFile(path, []byte("int main(void) { return 0; }"), 0600); e != nil {
		t.Fatal(e)
	}
	m.submit.entries = []localEntry{{path: path, name: "main.c"}}
	m.submit.cursor = 0
	m.submit.dir = dir
	m, _ = sendKey(m, " ")
	m, cmd = sendKey(m, "enter")
	m = applySubmissionWork(t, m, cmd)
	if m.mode != "review" || m.submit.armed || posts.Load() != 0 || m.submit.prepared == nil {
		t.Fatal("review skipped")
	}
	if !strings.Contains(m.renderReview(100, 50), "main.c") {
		t.Fatal("manifest missing")
	}
	// Enter defaults to Back and discards snapshots; opening review never submits.
	m, _ = sendKey(m, "enter")
	if m.mode != "upload" || posts.Load() != 0 || m.submit.prepared != nil {
		t.Fatal("default action submitted")
	}
	m, cmd = sendKey(m, "enter")
	m = applySubmissionWork(t, m, cmd)
	m, _ = sendKey(m, "tab")
	if !m.submit.armed {
		t.Fatal("Submit not selected")
	}
	m, cmd = sendKey(m, "enter")
	if m.mode != "sending" || posts.Load() != 0 {
		t.Fatal("unexpected send scheduling")
	}
	m = applySubmissionWork(t, m, cmd)
	if posts.Load() != 1 || m.mode != "results" || !m.submit.watchActive {
		t.Fatalf("upload failed: %d %s %s", posts.Load(), m.mode, m.statusText)
	}
}
func TestUploadPathKeysAndStaleResponses(t *testing.T) {
	s, _ := uploadTestService(t)
	m := uploadTestModel(t, s)
	defer m.stopSubmission()
	m.mode = "upload"
	m.submit.dir = t.TempDir()
	m.submit.selected = map[string]bool{}
	m, _ = sendKey(m, "/")
	m.submit.input.SetValue("")
	for _, key := range []string{"q", "u", "?", "v"} {
		var cmd tea.Cmd
		m, cmd = sendKey(m, key)
		if !m.submit.editing {
			t.Fatal("path input escaped")
		}
		if cmd != nil {
			if _, quit := cmd().(tea.QuitMsg); quit {
				t.Fatal("path input quit")
			}
		}
	}
	if m.submit.input.Value() != "qu?v" || m.showHelp {
		t.Fatal("shortcut consumed text")
	}
	m = resize(m, 30, 10)
	if m.submit.input.Width > 30 {
		t.Fatal("text input ignored resize")
	}
	m, _ = sendKey(m, "esc")
	m.submit.generation = 3
	next, _, _ := m.updateSubmission(uploadOpenedMsg{generation: 2, dir: "wrong"})
	m = next.(Model)
	if m.submit.dir == "wrong" {
		t.Fatal("stale picker replaced current directory")
	}
	m.submit.targetID, m.submit.assignment = "url:lab1", "https://example/course/one"
	m.beginWatch("https://example/submission/one")
	old := m.submit.watchGeneration
	m.submit.targetID, m.submit.assignment = "url:lab2", "https://example/course/two"
	next, _, _ = m.updateSubmission(submissionResultMsg{generation: old, result: submission.Result{SubmissionURL: "https://example/submission/one", Status: "passed", RawStatus: "passed"}})
	m = next.(Model)
	if m.submit.refreshPending != "url:lab1" {
		t.Fatal("background result refreshed a different assignment")
	}
	m.beginWatch("https://example/submission/two")
	next, _, _ = m.updateSubmission(submissionResultMsg{generation: old, result: submission.Result{Title: "stale"}})
	m = next.(Model)
	if m.submit.result.Title == "stale" {
		t.Fatal("stale watch result applied")
	}
}
func TestSubmissionLayoutsAndLocalPreview(t *testing.T) {
	s, _ := uploadTestService(t)
	m := uploadTestModel(t, s)
	defer m.stopSubmission()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.c")
	if e := os.WriteFile(path, []byte("int main(void) { return 0; }"), 0600); e != nil {
		t.Fatal(e)
	}
	content, e := readLocalPreview(context.Background(), state.AssetRef{Name: "main.c", Path: path})
	if e != nil {
		t.Fatal(e)
	}
	doc, e := filepreview.PrepareContext(context.Background(), "main.c", content)
	if e != nil {
		t.Fatal(e)
	}
	if len(doc.Lines) == 0 {
		t.Fatal("local source preview empty")
	}
	p, e := s.Prepare(context.Background(), submission.Selection{AssignmentURL: s.BaseURL + "/course/2025-2026/os", Files: []string{path}})
	if e != nil {
		t.Fatal(e)
	}
	m.submit.prepared = p
	m.submit.form = p.Form
	m.submit.dir = dir
	m.submit.selected = map[string]bool{}
	for i := 0; i < 80; i++ {
		m.submit.entries = append(m.submit.entries, localEntry{path: path, name: fmt.Sprintf("file-%d-%s.c", i, strings.Repeat("long界", 20))})
		m.submit.result.Tests = append(m.submit.result.Tests, submission.TestResult{Name: fmt.Sprint(i), RawStatus: "passed", Status: "passed"})
	}
	m.submit.result.SubmissionURL = s.BaseURL + "/submission/2025-2026/os/@submissions/group/1"
	m.submit.result.Artifacts = []submission.Artifact{{Name: "Compiler log", URL: s.BaseURL + "/file/output/compile.log"}, {Name: "Diff", URL: s.BaseURL + "/file/output/1.diff"}}
	for _, mode := range []string{"upload", "review", "sending", "results"} {
		m.mode = mode
		for _, size := range [][2]int{{1, 1}, {24, 8}, {39, 12}, {71, 18}, {79, 24}, {80, 24}, {120, 42}, {180, 60}} {
			m = resize(m, size[0], size[1])
			assertFits(t, m.View(), size[0], size[1])
			m.preview.focused = true
			assertFits(t, m.View(), size[0], size[1])
			m.preview.focused = false
		}
	}
	m.mode = "results"
	m = resize(m, 100, 20)
	m, _ = sendKey(m, "j")
	if !strings.Contains(m.renderResultList(50, 12), "> Diff") {
		t.Fatal("selected artifact offscreen")
	}
	m, _ = sendKey(m, "k")
	if !strings.Contains(m.renderResultList(50, 12), "> Compiler log") {
		t.Fatal("first artifact offscreen")
	}
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if _, e = readLocalPreview(context.Background(), state.AssetRef{Name: "main.c", Path: path}); e == nil {
		t.Fatal("disappearing file preview succeeded")
	}
}

func TestUnknownReceiptResumesWithoutClaimingOlderSuccess(t *testing.T) {
	s, posts := uploadTestService(t)
	m := uploadTestModel(t, s)
	defer m.stopSubmission()
	assignment := m.selectedNode().CanonicalURL
	if e := s.Store.SaveReceipt(submission.Receipt{AssignmentURL: assignment, StatusURL: strings.Replace(assignment, "/course/", "/stats/", 1), State: "unknown", CreatedAt: time.Now()}); e != nil {
		t.Fatal(e)
	}
	m, cmd := sendKey(m, "v")
	m = applySubmissionWork(t, m, cmd)
	if m.submit.watchActive || m.submit.result.SubmissionURL != "" || m.submit.receipt.State != "unknown" || !strings.Contains(m.statusText, "outcome unknown") || posts.Load() != 0 {
		t.Fatalf("unknown receipt became success: %+v", m.submit)
	}
}
func TestBackgroundWatchAndSingleInflightRequest(t *testing.T) {
	s, _ := uploadTestService(t)
	m := uploadTestModel(t, s)
	defer m.stopSubmission()
	m.mode = "results"
	m.submit.targetID = "url:root"
	m.submit.assignment = m.selectedNode().CanonicalURL
	m.beginWatch(s.BaseURL + "/submission/2025-2026/os/@submissions/group/1")
	first := m.fetchSubmission(false)
	if first == nil || m.fetchSubmission(false) != nil {
		t.Fatal("more than one poll in flight")
	}
	m, _ = sendKey(m, "esc")
	if m.mode != "browse" || !m.submit.watchActive {
		t.Fatal("browsing stopped monitoring")
	}
	m = applySubmissionWork(t, m, first)
	if m.submit.watchActive || m.submit.result.Status != "passed" {
		t.Fatal("background result did not complete")
	}
}

func TestLocalPDFPreview(t *testing.T) {
	if _, e := exec.LookPath("pdftotext"); e != nil {
		t.Skip("requires Poppler")
	}
	path, err := filepath.Abs("../../filepreview/testdata/assignment.pdf")
	if err != nil {
		t.Fatal(err)
	}
	content, err := readLocalPreview(context.Background(), state.AssetRef{Name: "assignment.pdf", Path: path})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := filepreview.PrepareContext(context.Background(), "assignment.pdf", content)
	if err != nil || doc.Pages != 2 || doc.Truncated {
		t.Fatalf("local PDF preview: %+v %v", doc, err)
	}
}
