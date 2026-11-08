package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"themis-cli/internal/catalog"
	"themis-cli/internal/projectlink"
	"themis-cli/internal/state"
)

func setupFixture(t *testing.T) (state.State, state.Node, string) {
	t.Helper()
	st := state.NewEmptyState()
	st.BaseURL = "https://example.test"
	st.CatalogRootURL = st.BaseURL + "/course"
	add := func(path, title, kind string) state.Node {
		id, raw, err := state.NodeIDFromURL(st.BaseURL + "/course/" + path)
		if err != nil {
			t.Fatal(err)
		}
		n := state.Node{ID: id, CanonicalURL: raw, Title: title, Kind: kind, ChildrenHydrated: true, Status: state.StatusOK}
		st.Nodes[id] = n
		return n
	}
	course := add("2026-2027/pc", "Parallel Computing", "course")
	lab := add("2026-2027/pc/lab7", "Lab 7", "assignment")
	exercise := add("2026-2027/pc/lab7/e01", "Exercise 1", "assignment")
	pdf := state.AssetRef{Name: "assignment.pdf", URL: st.BaseURL + "/file/2026-2027/pc/lab7/%40files/assignment.pdf"}
	lab.Assets = []state.AssetRef{pdf}
	lab.ChildIDs = []string{exercise.ID}
	lab.ParentIDs = []string{course.ID}
	exercise.ParentIDs = []string{lab.ID}
	exercise.Assets = []state.AssetRef{pdf, {Name: "main.c", URL: st.BaseURL + "/file/2026-2027/pc/lab7/e01/%40files/main.c"}, {Name: "1.in", URL: st.BaseURL + "/file/2026-2027/pc/lab7/e01/%40tests/1.in"}}
	course.ChildIDs = []string{lab.ID}
	st.Nodes[course.ID] = course
	st.Nodes[lab.ID] = lab
	st.Nodes[exercise.ID] = exercise
	root := t.TempDir()
	return st, lab, root
}
func fixtureDownload(_ context.Context, a catalog.Asset, path string) error {
	return os.WriteFile(path, []byte("contents of "+a.URL), 0644)
}
func TestSetupPlanApplyResumeAndPreserveExistingWork(t *testing.T) {
	st, lab, root := setupFixture(t)
	prior := filepath.Join(root, "pc-lab-06")
	if err := os.Mkdir(prior, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prior, "solution.c"), []byte("my work"), 0644); err != nil {
		t.Fatal(err)
	}
	exercise := lab.ChildIDs[0]
	plan, err := Create(st, lab, Options{Mode: "setup", Root: root, Into: "pc-lab-07", Mappings: map[string]string{exercise: "pc-lab-07/pc-lab-07-e01"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 3 {
		t.Fatalf("shared PDF duplicated: %+v", plan.Files)
	}
	if _, err := os.Stat(filepath.Join(root, ".themis")); !os.IsNotExist(err) {
		t.Fatal("planning changed workspace")
	}
	planPath := filepath.Join(t.TempDir(), "plan.json")
	if err := Save(planPath, plan); err != nil {
		t.Fatal(err)
	}
	plan, err = Read(planPath)
	if err != nil {
		t.Fatal(err)
	}
	failed := false
	download := func(ctx context.Context, a catalog.Asset, path string) error {
		if !failed && strings.HasSuffix(a.Name, ".in") {
			failed = true
			return errors.New("temporary network error")
		}
		return fixtureDownload(ctx, a, path)
	}
	first, err := Apply(context.Background(), st, plan, download)
	if err == nil || first.Complete || len(first.Items) != 3 {
		t.Fatalf("partial setup: %+v %v", first, err)
	}
	second, err := Apply(context.Background(), st, plan, download)
	if err != nil || !second.Complete {
		t.Fatalf("resume: %+v %v", second, err)
	}
	third, err := Apply(context.Background(), st, plan, func(context.Context, catalog.Asset, string) error {
		t.Fatal("idempotent setup downloaded again")
		return nil
	})
	if err != nil || !third.Complete {
		t.Fatal(third, err)
	}
	for _, item := range third.Items {
		if item.State != "skipped" {
			t.Fatal("repeat did not skip owned file")
		}
	}
	content, err := os.ReadFile(filepath.Join(prior, "solution.c"))
	if err != nil || string(content) != "my work" {
		t.Fatal("earlier lab changed")
	}
	cfg, err := projectlink.Load(projectlink.ConfigPathFromRepoRoot(plan.Root))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DownloadDirs[exercise] != "pc-lab-07/pc-lab-07-e01" || cfg.LinkedRootURL != strings.TrimSuffix(lab.CanonicalURL, "/lab7") {
		t.Fatalf("wrong mapping %+v", cfg)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); !os.IsNotExist(err) {
		t.Fatal("setup initialized Git")
	}
}
func TestSetupConflictsAndPlanRevalidation(t *testing.T) {
	st, lab, root := setupFixture(t)
	opts := Options{Mode: "setup", Root: root, Into: "lab7"}
	plan, err := Create(st, lab, opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(context.Background(), st, plan, fixtureDownload); err != nil {
		t.Fatal(err)
	}
	starter := filepath.Join(root, "lab7/e01/main.c")
	if err := os.WriteFile(starter, []byte("edited solution"), 0644); err != nil {
		t.Fatal(err)
	}
	conflict, err := Create(st, lab, opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(context.Background(), st, conflict, fixtureDownload); err == nil || !strings.Contains(err.Error(), "DESTINATION_CONFLICT") {
		t.Fatal("overwrote local edits", err)
	}
	opts.Overwrite = true
	replacement, err := Create(st, lab, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(starter, []byte("newer edit"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(context.Background(), st, replacement, fixtureDownload); err == nil {
		t.Fatal("accepted changed review")
	}
	replacement, err = Create(st, lab, opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(context.Background(), st, replacement, fixtureDownload); err != nil {
		t.Fatal("explicit replacement failed", err)
	}
	changed := st.Nodes[lab.ID]
	changed.Title = "Different lab"
	st.Nodes[lab.ID] = changed
	if _, err := Apply(context.Background(), st, replacement, fixtureDownload); err == nil || !strings.Contains(err.Error(), "PLAN_CHANGED") {
		t.Fatal("stale source accepted", err)
	}
}
func TestSetupRequiresCompleteHierarchyAndRejectsEscapes(t *testing.T) {
	st, lab, root := setupFixture(t)
	child := st.Nodes[lab.ChildIDs[0]]
	child.ChildrenHydrated = false
	st.Nodes[child.ID] = child
	if _, err := Create(st, lab, Options{Mode: "setup", Root: root, Into: "lab7"}); err == nil {
		t.Fatal("planned unloaded exercise")
	}
	child.ChildrenHydrated = true
	st.Nodes[child.ID] = child
	if _, err := Create(st, lab, Options{Mode: "setup", Root: root, Into: "../escape"}); err == nil {
		t.Fatal("accepted traversal")
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(st, lab, Options{Mode: "setup", Root: root, Into: "escape/lab7"}); err == nil {
		t.Fatal("accepted symlink")
	}
	plan, err := Create(st, lab, Options{Mode: "setup", Root: root, Into: "lab7"})
	if err != nil {
		t.Fatal(err)
	}
	plan.Files[0].Destination = "../outside"
	plan.ID = plan.Seal()
	if _, err := Apply(context.Background(), st, plan, fixtureDownload); err == nil {
		t.Fatal("trusted an unsafe edited plan")
	}
	child.ChildIDs = []string{lab.ID}
	st.Nodes[child.ID] = child
	if _, err := Create(st, lab, Options{Mode: "setup", Root: root, Into: "lab7"}); err == nil {
		t.Fatal("accepted cyclic hierarchy")
	}
}

func TestSetupPreservesPreferencesChangedDuringDownload(t *testing.T) {
	st, lab, root := setupFixture(t)
	course := strings.TrimSuffix(lab.CanonicalURL, "/lab7")
	path := projectlink.ConfigPathFromRepoRoot(root)
	if err := projectlink.Save(path, projectlink.Config{BaseURL: st.BaseURL, LinkedRootURL: course, Preferences: projectlink.DefaultPreferences()}); err != nil {
		t.Fatal(err)
	}
	plan, err := Create(st, lab, Options{Mode: "setup", Root: root, Into: "lab7"})
	if err != nil {
		t.Fatal(err)
	}
	changed := false
	download := func(ctx context.Context, a catalog.Asset, file string) error {
		if !changed {
			cfg, err := projectlink.Load(path)
			if err != nil {
				return err
			}
			cfg.Preferences.DefaultRefreshDepth = 9
			cfg.RecentAssetChoices = map[string][]string{"earlier-lab": {"keep"}}
			if err := projectlink.Save(path, cfg); err != nil {
				return err
			}
			changed = true
		}
		return fixtureDownload(ctx, a, file)
	}
	if _, err := Apply(context.Background(), st, plan, download); err != nil {
		t.Fatal(err)
	}
	cfg, err := projectlink.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Preferences.DefaultRefreshDepth != 9 || len(cfg.RecentAssetChoices["earlier-lab"]) != 1 {
		t.Fatal("setup overwrote unrelated project preferences")
	}
}
