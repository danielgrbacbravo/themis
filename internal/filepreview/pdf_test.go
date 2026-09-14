package filepreview

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func requirePDFTool(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("PDF extraction integration test requires Poppler")
	}
}
func TestPDFExtractionIncludesPagesAndEncodedText(t *testing.T) {
	requirePDFTool(t)
	data, err := os.ReadFile("testdata/assignment.pdf")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Prepare("assignment.pdf", Content{Data: data})
	if err != nil {
		t.Fatal(err)
	}
	text := plain(doc)
	for _, want := range []string{"Page 1", "Page 2", "master-worker", "checklist", "café"} {
		if !strings.Contains(text, want) {
			t.Fatalf("PDF missing %q: %s", want, text)
		}
	}
	if doc.Pages != 2 || doc.Language != "PDF text" || doc.Bytes != len(data) || doc.Truncated {
		t.Fatalf("unexpected PDF metadata: %+v", doc)
	}
	// Content sniffing also supports links without a filename extension.
	if _, err := Prepare("Assignment", Content{Data: data}); err != nil {
		t.Fatal(err)
	}
}
func TestPDFLimitsAndNoText(t *testing.T) {
	requirePDFTool(t)
	data, _ := os.ReadFile("testdata/many-pages.pdf")
	doc, err := Prepare("many.pdf", Content{Data: data})
	if err != nil || doc.Pages != maxPDFPages || !doc.Truncated {
		t.Fatalf("page bound: %d %v %v", doc.Pages, doc.Truncated, err)
	}
	data, _ = os.ReadFile("testdata/empty.pdf")
	if _, err := Prepare("empty.pdf", Content{Data: data}); err == nil || !strings.Contains(err.Error(), "OCR") {
		t.Fatalf("empty PDF: %v", err)
	}
	if _, err := Prepare("large.pdf", Content{Data: make([]byte, MaxPDFBytes+1)}); err == nil {
		t.Fatal("oversized PDF accepted")
	}
	if _, err := Prepare("partial.pdf", Content{Data: []byte("%PDF-1.4"), Truncated: true}); err == nil {
		t.Fatal("partial PDF accepted")
	}
}
func TestPDFMissingParserAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PrepareContext(ctx, "x.pdf", Content{Data: []byte("%PDF-1.4")}); err == nil {
		t.Fatal("canceled PDF preview succeeded")
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := Prepare("x.pdf", Content{Data: []byte("%PDF-1.4")}); err == nil || !strings.Contains(err.Error(), "Poppler") {
		t.Fatalf("missing parser: %v", err)
	}
}
func TestPDFOutputCaptureRemainsBoundedWithIOCopy(t *testing.T) {
	out := &limitedCapture{limit: 10}
	if _, err := io.Copy(out, strings.NewReader(strings.Repeat("x", 100))); err != nil {
		t.Fatal(err)
	}
	if out.buf.Len() != 10 || !out.truncated {
		t.Fatal("PDF process output exceeded bound")
	}
}

func TestPDFProcessIsCanceled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pdftotext"), []byte("#!/bin/sh\nexec /bin/sleep 10\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := PrepareContext(ctx, "file.pdf", Content{Data: []byte("%PDF-1.4")})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("parser did not stop: %v", err)
	}
}
