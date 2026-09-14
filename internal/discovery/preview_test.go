package discovery

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"themis-cli/internal/filepreview"
	"themis-cli/internal/state"
	"time"
)

func TestPreviewAssetUsesAuthenticatedRawBoundedRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("raw") != "true" || r.URL.Query().Get("id") != "1" {
			t.Error("raw request lost query or range")
		}
		if c, err := r.Cookie("session"); err != nil || c.Value != "valid" {
			t.Error("missing authenticated cookie")
		}
		fmt.Fprint(w, strings.Repeat("x", filepreview.MaxBytes+1000))
	}))
	defer server.Close()
	client := server.Client()
	client.Jar, _ = cookiejar.New(nil)
	base, _ := url.Parse(server.URL)
	client.Jar.SetCookies(base, []*http.Cookie{{Name: "session", Value: "valid"}})
	out, err := PreviewAsset(context.Background(), client, state.AssetRef{Name: "main.c", URL: server.URL + "/file/main.c?id=1"})
	if err != nil || !out.Truncated || len(out.Data) != filepreview.MaxBytes {
		t.Fatalf("unexpected preview: %d %v %v", len(out.Data), out.Truncated, err)
	}
}

func TestPreviewAssetRejectsErrorsAndCancels(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/missing":
			http.Error(w, "not found", 404)
		case "/html":
			fmt.Fprint(w, "<!doctype html><html>Sign in</html>")
		case "/redirect":
			http.Redirect(w, r, "/login", 302)
		case "/login":
			fmt.Fprint(w, "sign in")
		case "/slow":
			close(started)
			<-r.Context().Done()
			close(canceled)
		}
	}))
	defer server.Close()
	for _, p := range []string{"/missing", "/html", "/redirect"} {
		if _, err := PreviewAsset(context.Background(), server.Client(), state.AssetRef{Name: "main.c", URL: server.URL + p}); err == nil {
			t.Fatalf("%s should fail", p)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := PreviewAsset(ctx, server.Client(), state.AssetRef{URL: server.URL + "/slow"})
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled fetch succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("request did not cancel")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("server did not observe cancellation")
	}
}

func TestPreviewAssetHandlesPartialAndEmptyRanges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/empty":
			w.Header().Set("Content-Range", "bytes */0")
			w.WriteHeader(416)
		case "/partial":
			w.Header().Set("Content-Range", "bytes 0-2/100")
			w.WriteHeader(206)
			fmt.Fprint(w, "abc")
		case "/wrong":
			w.Header().Set("Content-Range", "bytes 3-5/100")
			w.WriteHeader(206)
			fmt.Fprint(w, "abc")
		}
	}))
	defer server.Close()
	out, err := PreviewAsset(context.Background(), server.Client(), state.AssetRef{URL: server.URL + "/empty"})
	if err != nil || len(out.Data) != 0 || out.Truncated {
		t.Fatalf("empty range: %+v %v", out, err)
	}
	out, err = PreviewAsset(context.Background(), server.Client(), state.AssetRef{URL: server.URL + "/partial"})
	if err != nil || !out.Truncated || string(out.Data) != "abc" {
		t.Fatalf("partial range: %+v %v", out, err)
	}
	if _, err = PreviewAsset(context.Background(), server.Client(), state.AssetRef{URL: server.URL + "/wrong"}); err == nil {
		t.Fatal("non-prefix range accepted")
	}
}

func TestPreviewAssetReadsCompletePDFBeyondTextLimit(t *testing.T) {
	// A PDF must include its trailer; the 64 KiB text-prefix cap cannot apply.
	data := append([]byte("%PDF-1.4\n"), []byte(strings.Repeat("x", filepreview.MaxBytes+1000))...)
	data = append(data, []byte("\n%%EOF")...)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			t.Error("PDF was requested as a partial file")
		}
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(data)
	}))
	defer server.Close()
	out, err := PreviewAsset(context.Background(), server.Client(), state.AssetRef{Name: "Assignment", URL: server.URL + "/file/document"})
	if err != nil || out.Truncated || len(out.Data) != len(data) || !strings.HasSuffix(string(out.Data), "%%EOF") {
		t.Fatalf("PDF read incomplete: %d %v", len(out.Data), err)
	}
}
