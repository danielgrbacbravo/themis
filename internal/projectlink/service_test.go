package projectlink

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryInheritanceAndChanges(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	base := "https://example.test"
	parentURL := base + "/course/2026/os"
	childURL := parentURL + "/lab1"
	parent := Config{DownloadDirs: map[string]string{"exercise": "pc-lab-07/e01"}, BaseURL: base, LinkedRootURL: parentURL, LastOpenNodeID: "old", LastDownloadDir: "downloads", RecentAssetChoices: map[string][]string{"exercise": {"asset"}}, Preferences: Preferences{DefaultRefreshDepth: 4}}
	if err := Save(ConfigPathFromRepoRoot(root), parent); err != nil {
		t.Fatal(err)
	}
	svc := Service{WorkingDir: child, BaseURL: base}
	ctx, err := svc.Resolve()
	if err != nil || ctx.LinkDir != root {
		t.Fatalf("inherited context: %+v, %v", ctx, err)
	}
	ctx, err = svc.Apply(Change{Directory: child, TargetURL: childURL})
	if err != nil || ctx.LinkDir != child {
		t.Fatalf("override: %+v, %v", ctx, err)
	}
	ctx, err = svc.Apply(Change{Directory: child, Remove: true})
	if err != nil || ctx.LinkDir != root {
		t.Fatalf("reveal parent: %+v, %v", ctx, err)
	}
	ctx, err = svc.Apply(Change{Directory: root, TargetURL: childURL})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Config.LastOpenNodeID != "" || ctx.Config.LastDownloadDir != "downloads" || ctx.Config.Preferences.DefaultRefreshDepth != 4 || len(ctx.Config.RecentAssetChoices) != 1 || ctx.Config.DownloadDirs["exercise"] != "pc-lab-07/e01" {
		t.Fatalf("lost settings: %+v", ctx.Config)
	}
	if _, err := svc.Apply(Change{Directory: root, TargetURL: "https://other.test/course/x"}); err == nil {
		t.Fatal("accepted other instance")
	}
	cfg, err := Load(ConfigPathFromRepoRoot(root))
	if err != nil || cfg.LinkedRootURL != childURL {
		t.Fatal("failed write changed effective config")
	}
	if _, err := svc.Apply(Change{Directory: filepath.Join(root, "missing"), TargetURL: childURL}); err == nil {
		t.Fatal("accepted missing directory")
	}
}
func TestMalformedLinkCannotBeOverwritten(t *testing.T) {
	dir := t.TempDir()
	path := ConfigPathFromRepoRoot(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	svc := Service{WorkingDir: dir, BaseURL: "https://example.test"}
	if _, err := svc.Resolve(); err == nil {
		t.Fatal("expected malformed config error")
	}
	if _, err := svc.Apply(Change{Directory: dir, TargetURL: "https://example.test/course/2026/os"}); err == nil {
		t.Fatal("overwrote malformed config")
	}
}
