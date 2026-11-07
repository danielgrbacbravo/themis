package browserprefs

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRecentLimitCanonicalDedupAndPersistence(t *testing.T) {
	var p Preferences
	for i := 0; i < 25; i++ {
		p = p.Open(Target{URL: fmt.Sprintf("https://example.test/course/2026/course%d", i), Title: "Course"})
	}
	p = p.Open(Target{URL: "https://example.test/course/2026/course20/?x=y", Title: "Reopened"})
	if len(p.Recent) != 20 || p.Recent[0].Title != "Reopened" {
		t.Fatalf("unexpected recents: %+v", p)
	}
	count := 0
	for _, item := range p.Recent {
		if item.URL == p.Recent[0].URL {
			count++
		}
	}
	if count != 1 {
		t.Fatal("duplicate recent")
	}
	path := filepath.Join(t.TempDir(), "browser.json")
	if err := Save(path, p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || len(got.Recent) != 20 || got.Recent[0] != p.Recent[0] {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	if err := os.WriteFile(path, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("malformed preferences silently accepted")
	}
	if err := Save(filepath.Join(path, "impossible"), p); err == nil {
		t.Fatal("expected persistence failure")
	}
}
