package discovery

import (
	"bytes"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"themis-cli/internal/state"
)

func TestDownloadAssetRefs(t *testing.T) {
	assets := []state.AssetRef{
		{Name: "1.in", URL: "https://themis.housing.rug.nl/file/course/%40tests/1.in"},
		{Name: "1.out", URL: "https://themis.housing.rug.nl/file/course/%40tests/1.out"},
		{Name: "archive.zip", URL: "https://themis.housing.rug.nl/download/archive.zip"},
	}

	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case "https://themis.housing.rug.nl/file/course/%40tests/1.in?raw=true":
			return okResponse(req, "in-data"), nil
		case "https://themis.housing.rug.nl/file/course/%40tests/1.out?raw=true":
			return okResponse(req, "out-data"), nil
		case "https://themis.housing.rug.nl/download/archive.zip":
			return okResponse(req, "zip-data"), nil
		default:
			return notFoundResponse(req), nil
		}
	})}

	dir := t.TempDir()
	downloaded, err := DownloadAssetRefs(client, assets, dir)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if len(downloaded) != 3 {
		t.Fatalf("expected 3 downloaded assets, got %d", len(downloaded))
	}

	expected := map[string]bool{
		filepath.Join(dir, "tests/1.in"):  false,
		filepath.Join(dir, "tests/1.out"): false,
		filepath.Join(dir, "archive.zip"): false,
	}
	for _, item := range downloaded {
		if _, ok := expected[item.Path]; ok {
			expected[item.Path] = true
		}
	}
	for p, seen := range expected {
		if !seen {
			t.Fatalf("expected downloaded path missing: %s", p)
		}
	}
}

func TestDownloadAssetRefs_DedupesNames(t *testing.T) {
	assets := []state.AssetRef{
		{Name: "same.txt", URL: "https://themis.housing.rug.nl/download/a.txt"},
		{Name: "same.txt", URL: "https://themis.housing.rug.nl/download/b.txt"},
	}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() == "https://themis.housing.rug.nl/download/a.txt" || req.URL.String() == "https://themis.housing.rug.nl/download/b.txt" {
			return okResponse(req, "data"), nil
		}
		return notFoundResponse(req), nil
	})}

	dir := t.TempDir()
	downloaded, err := DownloadAssetRefs(client, assets, dir)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if downloaded[0].Name == downloaded[1].Name {
		t.Fatalf("expected deduped names, got %s and %s", downloaded[0].Name, downloaded[1].Name)
	}
	if filepath.Base(downloaded[1].Path) == filepath.Base(downloaded[0].Path) {
		t.Fatalf("expected deduped file paths")
	}
}

func TestDownloadAssetRefs_PreservesTestsRelativePaths(t *testing.T) {
	assets := []state.AssetRef{
		{Name: "1.in", URL: "https://themis.housing.rug.nl/file/course/%40tests/1.in"},
		{Name: "1.out", URL: "https://themis.housing.rug.nl/file/course/%40tests/1.out"},
		{Name: "imgs/1.img", URL: "https://themis.housing.rug.nl/file/course/%40tests/imgs/1.img"},
	}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case "https://themis.housing.rug.nl/file/course/%40tests/1.in?raw=true":
			return okResponse(req, "in-data"), nil
		case "https://themis.housing.rug.nl/file/course/%40tests/1.out?raw=true":
			return okResponse(req, "out-data"), nil
		case "https://themis.housing.rug.nl/file/course/%40tests/imgs/1.img?raw=true":
			return okResponse(req, "img-data"), nil
		default:
			return notFoundResponse(req), nil
		}
	})}

	dir := t.TempDir()
	downloaded, err := DownloadAssetRefs(client, assets, dir)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if len(downloaded) != 3 {
		t.Fatalf("expected 3 downloaded assets, got %d", len(downloaded))
	}

	expected := map[string]bool{
		filepath.Join(dir, "tests/1.in"):       false,
		filepath.Join(dir, "tests/1.out"):      false,
		filepath.Join(dir, "tests/imgs/1.img"): false,
	}
	for _, item := range downloaded {
		if _, ok := expected[item.Path]; ok {
			expected[item.Path] = true
		}
	}
	for p, seen := range expected {
		if !seen {
			t.Fatalf("expected downloaded path missing: %s", p)
		}
	}
}

func TestDownloadAssetRefs_PreservesRootImgsPath(t *testing.T) {
	assets := []state.AssetRef{
		{Name: "imgs/1.img", URL: "https://themis.housing.rug.nl/imgs/1.img"},
	}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() == "https://themis.housing.rug.nl/imgs/1.img" {
			return okResponse(req, "img-data"), nil
		}
		return notFoundResponse(req), nil
	})}

	dir := t.TempDir()
	downloaded, err := DownloadAssetRefs(client, assets, dir)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if len(downloaded) != 1 {
		t.Fatalf("expected 1 downloaded asset, got %d", len(downloaded))
	}
	if downloaded[0].Path != filepath.Join(dir, "imgs/1.img") {
		t.Fatalf("unexpected path: %s", downloaded[0].Path)
	}
}

func okResponse(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
		Request:    req,
	}
}

func notFoundResponse(req *http.Request) *http.Response {
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Body:       io.NopCloser(strings.NewReader("not found")),
		Header:     make(http.Header),
		Request:    req,
	}
}

func TestDownloadAssetRefsSkipsSiteInfoFromOldCaches(t *testing.T) {
	assets := []state.AssetRef{
		{Name: "About Themis", URL: "https://themis.housing.rug.nl/about", Kind: "asset"},
		{Name: "Privacy Policy", URL: "https://www.rug.nl/info/privacy", Kind: "asset"},
		{Name: "Privacy Policy", URL: "https://themis.housing.rug.nl/file/course/privacy.pdf", Kind: "file"},
	}
	hits := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		hits++
		if req.URL.Path != "/file/course/privacy.pdf" {
			t.Fatalf("requested site info: %s", req.URL)
		}
		return okResponse(req, "assignment document"), nil
	})}
	downloaded, err := DownloadAssetRefs(client, assets, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if hits != 1 || len(downloaded) != 1 {
		t.Fatalf("expected only assignment file, got %d requests and %d files", hits, len(downloaded))
	}
}
