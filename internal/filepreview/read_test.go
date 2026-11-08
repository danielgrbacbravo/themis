package filepreview

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestReadTextRangesAndUnknownBytes(t *testing.T) {
	content := Content{Data: []byte("one\ntwo\nthree\nfour\n")}
	opts := ReadOptions{StartPage: 1, Pages: 2, StartLine: 1, Lines: 2}
	r, err := ReadContext(context.Background(), "notes.txt", content, opts)
	if err != nil || r.Text != "one\ntwo" || r.NextLine == nil || *r.NextLine != 3 || r.Complete {
		t.Fatalf("first range %+v %v", r, err)
	}
	opts.StartLine = 3
	r, err = ReadContext(context.Background(), "notes.txt", content, opts)
	if err != nil || r.Text != "three\nfour" || r.NextLine != nil {
		t.Fatal(r, err)
	}
	opts.StartLine = 1
	opts.Lines = 20
	r, err = ReadContext(context.Background(), "notes.txt", content, opts)
	if err != nil || !r.Complete {
		t.Fatal(r, err)
	}
	content.Truncated = true
	r, err = ReadContext(context.Background(), "notes.txt", content, opts)
	if err != nil || r.Complete || !r.SourceTruncated {
		t.Fatal("truncated source presented as complete")
	}
	if _, err := ReadContext(context.Background(), "blob", Content{Data: []byte{0, 1, 2}}, opts); err == nil {
		t.Fatal("binary presented as text")
	}
}
func TestReadPDFRangesBeyondPreview(t *testing.T) {
	requirePDFTool(t)
	data, err := os.ReadFile("testdata/many-pages.pdf")
	if err != nil {
		t.Fatal(err)
	}
	opts := ReadOptions{StartPage: 21, Pages: 1, StartLine: 1, Lines: 20}
	r, err := ReadContext(context.Background(), "statement.pdf", Content{Data: data}, opts)
	if err != nil || len(r.Pages) != 1 || r.Pages[0].Number != 21 || r.NextPage == nil || *r.NextPage != 22 {
		t.Fatalf("PDF beyond TUI limit: %+v %v", r, err)
	}
	opts.StartPage = 1
	opts.Pages = 1
	data, err = os.ReadFile("testdata/assignment.pdf")
	if err != nil {
		t.Fatal(err)
	}
	r, err = ReadContext(context.Background(), "statement.pdf", Content{Data: data}, opts)
	if err != nil || r.NextPage == nil || *r.NextPage != 2 {
		t.Fatal(r, err)
	}
	opts.StartPage = 2
	r, err = ReadContext(context.Background(), "statement.pdf", Content{Data: data}, opts)
	if err != nil || r.NextPage != nil {
		t.Fatal(r, err)
	}
	data, err = os.ReadFile("testdata/empty.pdf")
	if err != nil {
		t.Fatal(err)
	}
	opts.StartPage = 1
	if _, err := ReadContext(context.Background(), "empty.pdf", Content{Data: data}, opts); err == nil || !strings.Contains(err.Error(), "OCR") {
		t.Fatal("image-only PDF was treated as empty instructions", err)
	}
}
