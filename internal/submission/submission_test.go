package submission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, e := os.ReadFile("testdata/" + name + ".html")
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func document(t *testing.T, body string) *goquery.Document {
	t.Helper()
	d, e := goquery.NewDocumentFromReader(strings.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func localFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	return p
}
func serviceAt(server *httptest.Server) *Service {
	return &Service{BaseURL: server.URL, Client: func() (*http.Client, error) { return server.Client(), nil }}
}
func resultHTML(t *testing.T, status, one, two string) string {
	return strings.NewReplacer("STATUS", status, "TEST1", one, "TEST2", two).Replace(fixture(t, "result"))
}

const resultPath = "/submission/course/ex/@submissions/group/42"

func TestParseForms(t *testing.T) {
	f, err := ParseForm(document(t, fixture(t, "open")), "https://themis.example/course/course/ex")
	if err != nil {
		t.Fatal(err)
	}
	if f.MaxFiles != 50 || f.MaxBytes != 500000 || f.Account != "Student Example" || f.Group != "Example Group" || !f.FoldersAllowed || f.Fields.Get("judgenow") != "" || !strings.Contains(f.Action, "_csrf=TOKEN&sudo=student") {
		t.Fatalf("incorrect form: %+v", f)
	}
	_, err = ParseForm(document(t, fixture(t, "closed")), f.AssignmentURL)
	if err == nil || !strings.Contains(err.Error(), "has ended") {
		t.Fatalf("closed reason missing: %v", err)
	}
	for _, body := range []string{strings.Replace(fixture(t, "open"), `data-files="50"`, `data-files="broken"`, 1), strings.Replace(fixture(t, "open"), `<button type="submit">`, `<input name="answer-1"><button type="submit">`, 1), strings.Replace(fixture(t, "open"), `<button type="submit">`, `<button type="submit" disabled>`, 1)} {
		if _, err := ParseForm(document(t, body), f.AssignmentURL); err == nil {
			t.Fatal("unsupported form accepted")
		}
	}
	f2, err := ParseForm(document(t, fixture(t, "languages")), f.AssignmentURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(f2.Languages) != 2 || f2.Fields.Get("nonce") != "keep-me" {
		t.Fatalf("language fields: %+v", f2)
	}
	if lang, e := Validate(f2, []File{{Name: "main.py", Size: 12}}, ""); e != nil || lang != "python" {
		t.Fatalf("language inference: %s %v", lang, e)
	}
	if _, e := Validate(f2, []File{{Name: "main.py", Size: 12}, {Name: "main.c", Size: 12}}, ""); !errors.Is(e, ErrLanguage) {
		t.Fatalf("conflicting languages: %v", e)
	}
	for _, files := range [][]File{{{Name: "bad.txt", Size: 1}}, {{Name: "a.c", Size: 60}, {Name: "b.c", Size: 60}}, {{Name: "a.c"}, {Name: "b.c"}, {Name: "c.c"}}, {{Name: "folder/a.c", Field: "folders[]"}}} {
		if _, e := Validate(f2, files, "c"); e == nil {
			t.Fatal("invalid manifest accepted")
		}
	}
	changed := f
	changed.Action = strings.Replace(f.Action, "TOKEN", "fresh", 1)
	changed.Fields = map[string][]string{"_csrf": {"fresh"}, "judgeLanguage": {"c"}}
	if !sameRequirements(f, changed) {
		t.Fatal("CSRF rotation requires unnecessary review")
	}
	changed.Action += "&different=1"
	if sameRequirements(f, changed) {
		t.Fatal("non-CSRF action change missed")
	}
}

func TestExpandAndSnapshots(t *testing.T) {
	dir := t.TempDir()
	single := localFile(t, dir, "main.c", "old bytes")
	folder := filepath.Join(dir, "src")
	localFile(t, folder, "test/1.in", "input")
	localFile(t, folder, ".git/secret", "exclude")
	localFile(t, folder, ".themis/cache", "exclude")
	if e := os.Symlink(single, filepath.Join(folder, "linked.c")); e != nil {
		t.Fatal(e)
	}
	files, e := Expand(context.Background(), Selection{Files: []string{single}, Folders: []string{folder}})
	if e != nil {
		t.Fatal(e)
	}
	if len(files) != 2 || files[1].Name != "src/test/1.in" || files[1].Field != "folders[]" {
		t.Fatalf("manifest: %+v", files)
	}
	other := localFile(t, t.TempDir(), "main.c", "duplicate")
	for _, selection := range []Selection{{Files: []string{single, other}}, {Folders: []string{t.TempDir()}}, {Files: []string{filepath.Join(folder, "linked.c")}}, {Files: []string{single + "missing"}}} {
		if _, e := Expand(context.Background(), selection); e == nil {
			t.Fatal("bad selection accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := Expand(ctx, Selection{Files: []string{single}}); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

func TestMultipartFreshCSRFAndExactSnapshot(t *testing.T) {
	open := fixture(t, "open")
	result := resultHTML(t, "queued", "none", "none")
	var gets, posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/course/"):
			token := fmt.Sprintf("token%d", gets.Add(1))
			fmt.Fprint(w, strings.ReplaceAll(open, "TOKEN", token))
		case r.Method == "POST":
			posts.Add(1)
			if r.URL.Query().Get("_csrf") != "token2" || r.URL.Query().Get("sudo") != "student" {
				t.Errorf("stale query: %s", r.URL.RawQuery)
			}
			mr, e := r.MultipartReader()
			if e != nil {
				t.Error(e)
				return
			}
			fields := map[string]string{}
			uploaded := map[string]string{}
			for {
				part, e := mr.NextPart()
				if e == io.EOF {
					break
				}
				if e != nil {
					t.Error(e)
					return
				}
				b, e := io.ReadAll(part)
				if e != nil {
					t.Error(e)
					return
				}
				_, params, e := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
				if e != nil {
					t.Error(e)
				}
				if name, ok := params["filename"]; ok {
					uploaded[params["name"]+":"+name] = string(b)
				} else {
					fields[params["name"]] = string(b)
				}
			}
			if uploaded["files[]:main.c"] != "reviewed" || uploaded["folders[]:tests/sub/1.in"] != "test input" || len(uploaded) != 2 {
				t.Errorf("wrong upload: %#v", uploaded)
			}
			if fields["_csrf"] != "token2" || fields["judgeLanguage"] != "c" || fields["judgenow"] != "" {
				t.Errorf("wrong fields: %#v", fields)
			}
			http.Redirect(w, r, resultPath, http.StatusSeeOther)
		case r.URL.Path == resultPath:
			fmt.Fprint(w, result)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	s := serviceAt(server)
	s.Store = &Store{Path: filepath.Join(t.TempDir(), "submissions.json")}
	dir := t.TempDir()
	file := localFile(t, dir, "main.c", "reviewed")
	folder := filepath.Join(dir, "tests")
	localFile(t, folder, "sub/1.in", "test input")
	p, e := s.Prepare(context.Background(), Selection{AssignmentURL: server.URL + "/course/course/ex", Files: []string{file}, Folders: []string{folder}})
	if e != nil {
		t.Fatal(e)
	}
	defer p.Cleanup()
	snapshot := p.Files[0].Snapshot
	if info, e := os.Stat(snapshot); e != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("snapshot permissions: %v %v", info, e)
	}
	localFile(t, dir, "main.c", "edited since review")
	var sent int64
	receipt, e := s.Submit(context.Background(), p, func(n, total int64) { sent = n })
	if e != nil {
		t.Fatal(e)
	}
	if receipt.State != "accepted" || receipt.SubmissionURL != server.URL+resultPath || sent != p.TotalBytes {
		t.Fatalf("receipt %+v bytes %d", receipt, sent)
	}
	if _, e = s.Submit(context.Background(), p, nil); e == nil || posts.Load() != 1 {
		t.Fatalf("duplicate attempt: %d %v", posts.Load(), e)
	}
	record, ok, e := (&Store{Path: s.Store.Path}).Latest(p.Form.AssignmentURL)
	if e != nil || !ok || record.Receipt.SubmissionURL != receipt.SubmissionURL {
		t.Fatalf("resume: %+v %v", record, e)
	}
	b, _ := os.ReadFile(s.Store.Path)
	var j journal
	if e = json.Unmarshal(b, &j); e != nil {
		t.Fatal(e)
	}
	if len(j.Records) != 1 {
		t.Fatalf("intent not replaced: %d", len(j.Records))
	}
	for _, secret := range []string{"token2", "reviewed", "_csrf", "snapshot"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("history leaked %s", secret)
		}
	}
	if info, _ := os.Stat(s.Store.Path); info.Mode().Perm() != 0600 {
		t.Fatal("history not private")
	}
	if e = p.Cleanup(); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(snapshot); !os.IsNotExist(e) {
		t.Fatal("snapshot survived cleanup")
	}
}

func TestChangedRequirementsNeverPost(t *testing.T) {
	open := fixture(t, "open")
	var gets, posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			return
		}
		body := open
		if gets.Add(1) > 1 {
			body = strings.Replace(body, `data-files="50"`, `data-files="10"`, 1)
		}
		fmt.Fprint(w, body)
	}))
	defer server.Close()
	s := serviceAt(server)
	p, e := s.Prepare(context.Background(), Selection{AssignmentURL: server.URL + "/course/course/ex", Files: []string{localFile(t, t.TempDir(), "a.c", "a")}})
	if e != nil {
		t.Fatal(e)
	}
	defer p.Cleanup()
	_, e = s.Submit(context.Background(), p, nil)
	var changed *ReviewChanged
	if !errors.As(e, &changed) || changed.Form.MaxFiles != 10 || posts.Load() != 0 {
		t.Fatalf("missing review: %v posts=%d", e, posts.Load())
	}
	if e = p.ApplyReview(changed.Form, "c"); e != nil {
		t.Fatal(e)
	}
}

func TestPOSTOutcomesAndNoRetries(t *testing.T) {
	for _, kind := range []string{"disconnect", "server-error", "generic-redirect", "wrong-assignment", "multiple-links", "single-history-link", "notice", "auth", "concrete-unavailable"} {
		t.Run(kind, func(t *testing.T) {
			open := fixture(t, "open")
			notice := fixture(t, "notice")
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					if strings.HasPrefix(r.URL.Path, "/course/") {
						fmt.Fprint(w, open)
					} else {
						http.NotFound(w, r)
					}
					return
				}
				posts.Add(1)
				io.Copy(io.Discard, r.Body)
				switch kind {
				case "disconnect":
					conn, _, e := w.(http.Hijacker).Hijack()
					if e == nil {
						conn.Close()
					}
				case "server-error":
					w.WriteHeader(500)
				case "generic-redirect":
					http.Redirect(w, r, "/stats/course/ex", 303)
				case "wrong-assignment":
					http.Redirect(w, r, "/submission/course/other/@submissions/group/42", 303)
				case "multiple-links":
					fmt.Fprintf(w, `<a href="%s">old</a><a href="%s">other</a>`, resultPath, strings.Replace(resultPath, "42", "43", 1))
				case "single-history-link":
					fmt.Fprintf(w, `<section class="status"><a href="%s">Latest submission</a></section>`, resultPath)
				case "notice":
					fmt.Fprint(w, notice)
				case "auth":
					w.WriteHeader(403)
				case "concrete-unavailable":
					http.Redirect(w, r, resultPath, 303)
				}
			}))
			defer server.Close()
			s := serviceAt(server)
			s.Store = &Store{Path: filepath.Join(t.TempDir(), "submissions.json")}
			p, e := s.Prepare(context.Background(), Selection{AssignmentURL: server.URL + "/course/course/ex", Files: []string{localFile(t, t.TempDir(), "a.c", "a")}})
			if e != nil {
				t.Fatal(e)
			}
			defer p.Cleanup()
			r, e := s.Submit(context.Background(), p, nil)
			switch kind {
			case "concrete-unavailable":
				if e != nil || r.State != "accepted" {
					t.Fatalf("concrete receipt lost: %+v %v", r, e)
				}
			case "notice":
				if e == nil || !strings.Contains(e.Error(), "not permitted") {
					t.Fatal(e)
				}
			case "auth":
				if !errors.Is(e, ErrAuth) {
					t.Fatal(e)
				}
			default:
				if !errors.Is(e, ErrUnknown) || r.SubmissionURL != "" {
					t.Fatalf("ambiguous accepted: %+v %v", r, e)
				}
			}
			_, _ = s.Submit(context.Background(), p, nil)
			if posts.Load() != 1 {
				t.Fatalf("POST replayed %d times", posts.Load())
			}
		})
	}
}

func TestResultsLatestJudgementAndHidden(t *testing.T) {
	s := &Service{BaseURL: "https://themis.example"}
	url := s.BaseURL + resultPath
	for _, tc := range []struct {
		raw, one, two, normalized string
		completed                 int
		terminal                  bool
	}{{"passed", "passed", "passed", "passed", 2, true}, {"compilerr", "none", "none", "failed", 0, true}, {"running", "passed", "none", "running", 1, false}, {"queued", "none", "none", "queued", 0, false}, {"none", "none", "none", "unjudged", 0, true}, {"unrecognized", "none", "none", "unknown", 0, false}} {
		r, e := s.ParseResult(document(t, resultHTML(t, tc.raw, tc.one, tc.two)), url)
		if e != nil {
			t.Fatal(e)
		}
		if r.Status != tc.normalized || r.Completed != tc.completed || r.KnownTotal != 2 || r.Terminal() != tc.terminal || r.Score != "75%" || r.Tests[0].Name != "Test 1" || r.Tests[0].Hint != "Hint one" {
			t.Fatalf("wrong result %+v", r)
		}
		if len(r.Artifacts) != 4 || len(r.Tests[0].Artifacts) != 2 {
			t.Fatalf("wrong artifacts: %+v", r.Artifacts)
		}
	}
	for _, body := range []string{fixture(t, "hidden"), strings.ReplaceAll(resultHTML(t, "passed", "passed", "passed"), "/group/42", "/group/43")} {
		if _, e := s.ParseResult(document(t, body), url); e == nil {
			t.Fatal("missing/mismatched result accepted")
		}
	}
	d := document(t, `<section class="status"><div class="cfg-line"><span class="cfg-key">Latest:</span><span class="cfg-val"><a href="`+resultPath+`">42</a></span></div></section>`)
	if LatestLink(d) != resultPath {
		t.Fatal("latest link not parsed")
	}
}

func TestWatchTransitionsCancellationAndTimeout(t *testing.T) {
	queued := resultHTML(t, "queued", "none", "none")
	running := resultHTML(t, "running", "passed", "none")
	passed := resultHTML(t, "passed", "passed", "passed")
	var calls, inflight, maxFlight atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inflight.Add(1)
		defer inflight.Add(-1)
		if n > maxFlight.Load() {
			maxFlight.Store(n)
		}
		b := queued
		switch calls.Add(1) {
		case 2:
			b = running
		case 3:
			b = passed
		}
		fmt.Fprint(w, b)
	}))
	defer server.Close()
	s := serviceAt(server)
	var states []string
	r, e := s.Watch(context.Background(), server.URL+resultPath, WatchOptions{Interval: time.Millisecond, Timeout: time.Second}, func(e WatchEvent) {
		if e.Result != nil {
			states = append(states, e.Result.Status)
		}
	})
	if e != nil || r.Status != "passed" || strings.Join(states, ",") != "queued,running,passed" || maxFlight.Load() != 1 {
		t.Fatalf("watch: %+v %v %v", r, states, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = s.Watch(ctx, server.URL+resultPath, WatchOptions{}, nil); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e = s.Watch(context.Background(), server.URL+resultPath, WatchOptions{Timeout: 10 * time.Millisecond, Interval: time.Second}, nil); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	for i, want := range []time.Duration{2, 4, 8, 16, 30, 30} {
		if Backoff(i) != want*time.Second {
			t.Fatal("bad backoff", i)
		}
	}
}
func TestWatchAuthAndTransientCancellation(t *testing.T) {
	for _, status := range []int{403, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(status) }))
			defer server.Close()
			s := serviceAt(server)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, e := s.Watch(ctx, server.URL+resultPath, WatchOptions{}, func(e WatchEvent) {
				if e.Err != nil {
					if e.Next != 4*time.Second {
						t.Errorf("wrong first retry %s", e.Next)
					}
					cancel()
				}
			})
			if status == 403 && !errors.Is(e, ErrAuth) {
				t.Fatal(e)
			}
			if status == 503 && !errors.Is(e, context.Canceled) {
				t.Fatal(e)
			}
			if calls.Load() != 1 {
				t.Fatal("unexpected retry")
			}
		})
	}
}

func TestWatchPausesAfterFiveFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("exercises real polling backoff timers")
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(503) }))
	defer server.Close()
	s := serviceAt(server)
	var delays []time.Duration
	_, err := s.Watch(context.Background(), server.URL+resultPath, WatchOptions{Timeout: 80 * time.Second}, func(e WatchEvent) { delays = append(delays, e.Next) })
	if !errors.Is(err, ErrPaused) || calls.Load() != 5 || fmt.Sprint(delays) != "[4s 8s 16s 30s 0s]" {
		t.Fatalf("watch did not pause: %d %v %v", calls.Load(), delays, err)
	}
}

func TestFreshFormAndPOSTShareCookieJar(t *testing.T) {
	open := fixture(t, "open")
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			c, e := r.Cookie("fresh-session")
			if e != nil || c.Value != "from-form" {
				t.Error("POST lost cookies set by fresh form")
			}
			io.Copy(io.Discard, r.Body)
			http.Redirect(w, r, resultPath, 303)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/course/") {
			http.SetCookie(w, &http.Cookie{Name: "fresh-session", Value: "from-form", Path: "/"})
			fmt.Fprint(w, open)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	s := &Service{BaseURL: server.URL, Client: func() (*http.Client, error) { jar, _ := cookiejar.New(nil); return &http.Client{Jar: jar}, nil }}
	p, e := s.Prepare(context.Background(), Selection{AssignmentURL: server.URL + "/course/course/ex", Files: []string{localFile(t, t.TempDir(), "a.c", "a")}})
	if e != nil {
		t.Fatal(e)
	}
	defer p.Cleanup()
	if _, e = s.Submit(context.Background(), p, nil); e != nil || posts.Load() != 1 {
		t.Fatal(e)
	}
}
func TestCancellationPreservesUncertainIntent(t *testing.T) {
	open := fixture(t, "open")
	started := make(chan struct{})
	release := make(chan struct{})
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			fmt.Fprint(w, open)
			return
		}
		posts.Add(1)
		io.Copy(io.Discard, r.Body)
		close(started)
		<-release
	}))
	defer server.Close()
	defer close(release)
	s := serviceAt(server)
	s.Store = &Store{Path: filepath.Join(t.TempDir(), "submissions.json")}
	p, e := s.Prepare(context.Background(), Selection{AssignmentURL: server.URL + "/course/course/ex", Files: []string{localFile(t, t.TempDir(), "a.c", "a")}})
	if e != nil {
		t.Fatal(e)
	}
	defer p.Cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := s.Submit(ctx, p, nil); done <- e }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("POST did not start")
	}
	record, ok, e := (&Store{Path: s.Store.Path}).Latest(p.Form.AssignmentURL)
	if e != nil || !ok || record.Receipt.State != "unknown" {
		t.Fatalf("in-flight intent missing: %+v %v", record, e)
	}
	cancel()
	select {
	case e = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("POST did not cancel")
	}
	if !errors.Is(e, ErrUnknown) {
		t.Fatal(e)
	}
	record, ok, e = (&Store{Path: s.Store.Path}).Latest(p.Form.AssignmentURL)
	if e != nil || !ok || record.Receipt.State != "unknown" || posts.Load() != 1 {
		t.Fatalf("uncertain intent lost: %+v %v", record, e)
	}
}

func TestPreparedSnapshotRemovedOnCancellation(t *testing.T) {
	open := fixture(t, "open")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, open) }))
	defer server.Close()
	s := serviceAt(server)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, e := s.Prepare(ctx, Selection{AssignmentURL: server.URL + "/course/course/ex", Files: []string{localFile(t, t.TempDir(), "a.c", "a")}})
	if e != nil {
		t.Fatal(e)
	}
	defer p.Cleanup()
	cancel()
	deadline := time.Now().Add(time.Second)
	for {
		_, e = os.Stat(p.Files[0].Snapshot)
		if os.IsNotExist(e) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancelled snapshot still exists")
		}
		time.Sleep(time.Millisecond)
	}
}
