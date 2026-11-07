package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"themis-cli/internal/submission"
	"themis-cli/internal/themis"
)

func captureSubmissionCLI(t *testing.T, run func() int) (int, submissionOutput, string) {
	t.Helper()
	dir := t.TempDir()
	out, e := os.Create(filepath.Join(dir, "stdout"))
	if e != nil {
		t.Fatal(e)
	}
	defer out.Close()
	errout, e := os.Create(filepath.Join(dir, "stderr"))
	if e != nil {
		t.Fatal(e)
	}
	defer errout.Close()
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = out, errout
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
	code := run()
	out.Seek(0, 0)
	data, e := io.ReadAll(out)
	if e != nil {
		t.Fatal(e)
	}
	var result submissionOutput
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if e = decoder.Decode(&result); e != nil {
		t.Fatalf("not one JSON result: %q %v", data, e)
	}
	var extra any
	if e = decoder.Decode(&extra); e != io.EOF {
		t.Fatalf("extra stdout: %q", data)
	}
	errout.Seek(0, 0)
	progress, _ := io.ReadAll(errout)
	return code, result, string(progress)
}
func TestSubmissionCLIFlowAndJSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			io.Copy(io.Discard, r.Body)
			http.Redirect(w, r, "/submission/test/ex/@submissions/group/1", 303)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/submission/") {
			fmt.Fprint(w, `<section class="submission passed" id="/test/ex/@submissions/group/1"><div class="sec-title"><a>Exercise</a></div></section>`)
			return
		}
		fmt.Fprint(w, `<h1>Exercise</h1><a href="/user">Student</a><div class="cfg-line"><span class="cfg-key">Group</span><span class="cfg-val">Group</span></div><form method="post" data-submitform action="/submit/test/ex?_csrf=secret" data-files="50" data-filesize="500000" data-languages="c"><input type="hidden" name="judgeLanguage" value="c"><input type="file" name="files[]"></form>`)
	}))
	defer server.Close()
	dir := t.TempDir()
	session := filepath.Join(dir, "session.json")
	e := themis.SaveSessionState(session, themis.SessionState{SchemaVersion: 1, BaseURL: server.URL, Cookies: []themis.SessionCookieScope{{URL: server.URL, Cookies: []themis.SessionCookie{{Name: "session", Value: "fake", Path: "/"}}}}})
	if e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(dir, "main.c")
	if e = os.WriteFile(file, []byte("int main(void) { return 0; }"), 0600); e != nil {
		t.Fatal(e)
	}
	args := []string{"--base-url", server.URL, "--session-file", session, "--assignment-url", server.URL + "/course/test/ex", "--file", file, "--json"}
	code, result, _ := captureSubmissionCLI(t, func() int { return runSubmit(append(append([]string{}, args...), "--dry-run")) })
	if code != 0 || result.Status != "prepared" || result.Prepared == nil || posts.Load() != 0 {
		t.Fatalf("dry run: %d %+v", code, result)
	}
	b, _ := json.Marshal(result)
	if strings.Contains(string(b), "_csrf") || strings.Contains(string(b), "secret") {
		t.Fatal("review JSON leaked form credentials")
	}
	code, result, _ = captureSubmissionCLI(t, func() int { return runSubmit(args) })
	if code != 1 || !strings.Contains(result.Error, "--yes") || posts.Load() != 0 {
		t.Fatalf("unconfirmed noninteractive post: %d %+v", code, result)
	}
	code, result, progress := captureSubmissionCLI(t, func() int { return runSubmit(append(append([]string{}, args...), "--yes", "--watch=false")) })
	if code != 0 || result.Status != "accepted" || result.Receipt == nil || posts.Load() != 1 || !strings.Contains(progress, "main.c") {
		t.Fatalf("submit: %d %+v %s", code, result, progress)
	}
	code, result, _ = captureSubmissionCLI(t, func() int {
		return runSubmissionStatus([]string{"--base-url", server.URL, "--session-file", session, "--submission-url", result.Receipt.SubmissionURL, "--watch", "--json"})
	})
	if code != 0 || result.Result == nil || result.Result.Status != "passed" || posts.Load() != 1 {
		t.Fatalf("status: %d %+v", code, result)
	}
	code, result, _ = captureSubmissionCLI(t, func() int { return runSubmit([]string{"--unknown-flag", "--json"}) })
	if code != 1 || result.Error == "" {
		t.Fatal("flag errors lost")
	}
}
func TestSubmissionExitCodesAndInputCancellation(t *testing.T) {
	for _, tc := range []struct {
		state string
		err   error
		want  int
	}{{"passed", nil, 0}, {"failed", nil, 2}, {"unjudged", nil, 2}, {"", errors.New("failure"), 1}, {"", submission.ErrUnknown, 3}, {"", submission.ErrPaused, 3}, {"", context.DeadlineExceeded, 3}, {"", context.Canceled, 130}} {
		if got := submissionExit(&submission.Result{Status: tc.state}, tc.err); got != tc.want {
			t.Errorf("%s %v: %d", tc.state, tc.err, got)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	cancel()
	start := time.Now()
	if _, e := readSubmissionInput(ctx, bufio.NewReader(reader)); !errors.Is(e, context.Canceled) || time.Since(start) > time.Second {
		t.Fatal("input failed to cancel", e)
	}
}
