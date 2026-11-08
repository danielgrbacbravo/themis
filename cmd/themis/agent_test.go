package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"themis-cli/internal/projectlink"
	"themis-cli/internal/setup"
	"themis-cli/internal/state"
	"themis-cli/internal/themis"
)

func agentCall(t *testing.T, command string, args ...string) (int, agentOutput, []byte) {
	t.Helper()
	var buf bytes.Buffer
	code := executeAgentCLI(context.Background(), command, args, &buf)
	decoder := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	var out agentOutput
	if err := decoder.Decode(&out); err != nil {
		t.Fatalf("invalid JSON: %s %v", buf.String(), err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("more than one JSON result: %s", buf.String())
	}
	return code, out, buf.Bytes()
}
func TestAgentLabWorkflowWithLocalPlatform(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("THEMIS_BASE_URL", "")
	pdf, err := os.ReadFile("../../internal/filepreview/testdata/assignment.pdf")
	if err != nil {
		t.Fatal(err)
	}
	var posts, downloads atomic.Int32
	coursePath := "/course/2026-2027/pc"
	labPath := coursePath + "/lab7"
	exercisePath := labPath + "/e01"
	sharedFile := "/file/2026-2027/pc/lab7/@files/lab7.pdf"
	starterFile := "/file/2026-2027/pc/lab7/e01/@files/main.c"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			posts.Add(1)
			http.Error(w, "no writes", 405)
			return
		}
		switch r.URL.Path {
		case "/course/", "/user":
			fmt.Fprint(w, `<a href="/user">Student</a>`)
		case coursePath:
			fmt.Fprintf(w, `<title>Parallel Computing</title><div class="subsec round shade ass-children"><ul class="round"><li><span class="ass-link"><a href="%s">Lab 7</a></span></li></ul></div>`, labPath)
		case labPath:
			fmt.Fprintf(w, `<title>Lab 7</title><p class="ass-description">Parallel reduction assignment</p><div class="subsec round shade ass-children"><ul class="round"><li><span class="ass-link"><a href="%s">Exercise 1</a></span></li></ul></div><a href="%s">lab7.pdf</a>`, exercisePath, sharedFile)
		case exercisePath:
			fmt.Fprintf(w, `<title>Exercise 1</title><div class="subsec round shade ass-children"><ul class="round"></ul></div><a href="%s">lab7.pdf</a><a href="%s">main.c</a><a href="/file/2026-2027/pc/lab7/e01/@tests/1.in">1.in</a>`, sharedFile, starterFile)
		case sharedFile:
			downloads.Add(1)
			w.Header().Set("Content-Type", "application/pdf")
			w.Write(pdf)
		case starterFile:
			downloads.Add(1)
			fmt.Fprint(w, "starter line 1\nstarter line 2\n")
		case "/file/2026-2027/pc/lab7/e01/@tests/1.in":
			downloads.Add(1)
			fmt.Fprint(w, "test input\n")
		default:
			if strings.HasPrefix(r.URL.Path, "/stats/") {
				fmt.Fprint(w, `<html></html>`)
			} else {
				http.NotFound(w, r)
			}
		}
	}))
	defer server.Close()
	repo := t.TempDir()
	cache := filepath.Join(t.TempDir(), "state.json")
	session := filepath.Join(t.TempDir(), "session.json")
	if err := os.Mkdir(filepath.Join(repo, "pc-lab-06"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "pc-lab-06", "solution.c"), []byte("previous work"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := projectlink.Save(projectlink.ConfigPathFromRepoRoot(repo), projectlink.Config{BaseURL: server.URL, LinkedRootURL: server.URL + coursePath, Preferences: projectlink.DefaultPreferences()}); err != nil {
		t.Fatal(err)
	}
	if err := themis.SaveSessionState(session, themis.SessionState{SchemaVersion: 1, BaseURL: server.URL, Auth: themis.SessionAuthSettings{Password: "never-print-this"}, Cookies: []themis.SessionCookieScope{{URL: server.URL, Cookies: []themis.SessionCookie{{Name: "session", Value: "private-cookie", Path: "/"}}}}}); err != nil {
		t.Fatal(err)
	}
	common := []string{"--dir", repo, "--state-file", cache, "--session-file", session, "--json"}
	call := func(command string, args ...string) (agentOutput, []byte) {
		t.Helper()
		code, out, raw := agentCall(t, command, append(args, common...)...)
		if code != 0 {
			t.Fatalf("%s %v failed %d: %s", command, args, code, raw)
		}
		if strings.Contains(string(raw), "private-cookie") || strings.Contains(string(raw), "never-print-this") {
			t.Fatal("credentials leaked")
		}
		return out, raw
	}
	_, raw := call("project", "inspect")
	if !strings.Contains(string(raw), "pc-lab-06") {
		t.Fatal("existing workspace context missing")
	}
	call("auth", "status")
	_, raw = call("search", "lab 7", "--refresh", "--depth", "1")
	if !strings.Contains(string(raw), "Lab 7") || !strings.Contains(string(raw), "coverage") {
		t.Fatal("search lacks identities/coverage")
	}
	call("catalog", "list", "--parent", server.URL+coursePath)
	call("show", "--target", server.URL+labPath, "--refresh", "--depth", "1")
	_, raw = call("assets", "list", "--target", server.URL+exercisePath)

	// Decode the typed asset contract to verify parent document attribution.
	var assetEnvelope struct {
		Data struct {
			Assets []catalogAssetForTest `json:"assets"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &assetEnvelope); err != nil {
		t.Fatal(err)
	}
	pdfID, starterID := "", ""
	for _, a := range assetEnvelope.Data.Assets {
		if a.Name == "lab7.pdf" {
			pdfID = a.ID
			if !a.Inherited || a.SourceURL != server.URL+labPath {
				t.Fatalf("shared PDF owner lost: %+v", a)
			}
		}
		if a.Name == "main.c" {
			starterID = a.ID
		}
	}
	if pdfID == "" || starterID == "" {
		t.Fatal("missing assets")
	}
	_, raw = call("assets", "read", "--asset", starterID, "--lines", "1")
	if !strings.Contains(string(raw), "starter line 1") || !strings.Contains(string(raw), "next_line") {
		t.Fatal("text pagination missing")
	}
	if _, err := exec.LookPath("pdftotext"); err == nil {
		_, raw = call("assets", "read", "--asset", pdfID, "--pages", "1")
		if !strings.Contains(string(raw), "next_page") {
			t.Fatal("PDF pagination missing")
		}
	}
	planFile := filepath.Join(t.TempDir(), "lab7.json")
	call("setup", "plan", "--target", server.URL+labPath, "--into", "pc-lab-07", "--out", planFile)
	if _, err := os.Stat(filepath.Join(repo, "pc-lab-07")); !os.IsNotExist(err) {
		t.Fatal("planning created folders")
	}
	var plan setup.Plan
	plan, err = setup.Read(planFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 3 {
		t.Fatalf("shared PDF duplicated: %+v", plan.Files)
	}
	call("setup", "apply", "--plan", planFile, "--refresh")
	before := downloads.Load()
	call("setup", "apply", "--plan", planFile)
	if downloads.Load() != before {
		t.Fatal("repeated setup re-downloaded files")
	}
	for _, relative := range []string{"pc-lab-07/lab7.pdf", "pc-lab-07/e01/main.c", "pc-lab-07/e01/tests/1.in"} {
		if _, err := os.Stat(filepath.Join(repo, relative)); err != nil {
			t.Fatal(relative, err)
		}
	}
	content, err := os.ReadFile(filepath.Join(repo, "pc-lab-06", "solution.c"))
	if err != nil || string(content) != "previous work" {
		t.Fatal("prior work changed")
	}
	if posts.Load() != 0 {
		t.Fatal("setup submitted code")
	}
}

type catalogAssetForTest struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	SourceURL string `json:"source_url"`
	Inherited bool   `json:"inherited"`
}

func TestAgentCachedErrorsPaginationAndReadOnlyPlanning(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(t.TempDir(), "cache.json")
	st := state.NewEmptyState()
	st.BaseURL = "https://example.test"
	st.CatalogRootURL = st.BaseURL + "/course"
	for _, year := range []string{"2025-2026", "2026-2027"} {
		raw := st.BaseURL + "/course/" + year + "/pc"
		id, _, _ := state.NodeIDFromURL(raw)
		st.Nodes[id] = state.Node{ID: id, CanonicalURL: raw, Title: "Parallel Computing", Kind: "course", ChildrenHydrated: false}
	}
	if err := state.SaveAtomic(cache, st, false); err != nil {
		t.Fatal(err)
	}
	common := []string{"--dir", dir, "--state-file", cache, "--base-url", st.BaseURL, "--json"}
	code, out, raw := agentCall(t, "search", append([]string{"PC", "--limit", "1"}, common...)...)
	if code != 0 || len(out.Warnings) == 0 || !strings.Contains(string(raw), "2026-2027") || !strings.Contains(string(raw), `"next_offset":1`) {
		t.Fatal(string(raw))
	}
	code, out, _ = agentCall(t, "show", append([]string{"--target", "PC"}, common...)...)
	if code != 1 || out.Error == nil || out.Error.Code != "AMBIGUOUS_TARGET" {
		t.Fatalf("fuzzy action selected silently: %+v", out)
	}
	code, out, _ = agentCall(t, "auth", append([]string{"status", "--session-file", filepath.Join(t.TempDir(), "missing")}, common...)...)
	if code != 1 || out.Error == nil || out.Error.Code != "AUTH_REQUIRED" {
		t.Fatal(out)
	}
	code, out, _ = agentCall(t, "search", append([]string{"PC", "--limit", "0"}, common...)...)
	if code != 1 || out.Error.Code != "INVALID_ARGUMENT" {
		t.Fatal(out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".themis")); !os.IsNotExist(err) {
		t.Fatal("read-only commands modified workspace")
	}
	code, out, _ = agentCall(t, "capabilities", "--dir", "/does/not/exist", "--json")
	if code != 0 || out.Version != 1 {
		t.Fatal("capabilities depends on project state")
	}
}

func TestAgentAuthenticationFailureDoesNotInventLogout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "temporarily unavailable", 503) }))
	defer server.Close()
	session := filepath.Join(t.TempDir(), "session.json")
	if err := themis.SaveSessionState(session, themis.SessionState{SchemaVersion: 1, BaseURL: server.URL, Cookies: []themis.SessionCookieScope{{URL: server.URL, Cookies: []themis.SessionCookie{{Name: "session", Value: "fake", Path: "/"}}}}}); err != nil {
		t.Fatal(err)
	}
	code, out, raw := agentCall(t, "auth", "status", "--dir", t.TempDir(), "--base-url", server.URL, "--session-file", session, "--json")
	if code != 1 || out.Error.Code == "AUTH_REQUIRED" || !strings.Contains(string(raw), `"authenticated":null`) {
		t.Fatalf("network failure became a logout: %s", raw)
	}
}
