package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNestedFoldersAndReviewedPaths(t *testing.T) {
	root := t.TempDir()
	service := Service{}
	listing, err := service.Create(root, root, "pc-lab-07/pc-lab-07-e01")
	if err != nil {
		t.Fatal(err)
	}
	target := listing.Directory
	if filepath.Base(target) != "pc-lab-07-e01" {
		t.Fatal(target)
	}
	if err := os.WriteFile(filepath.Join(target, "main.c"), []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}
	files, err := service.Review(root, target, []string{"main.c", "tests/1.in", "imgs/1.img"})
	if err != nil {
		t.Fatal(err)
	}
	if !files[0].Exists || files[1].Exists || files[1].Path != filepath.Join(target, "tests/1.in") {
		t.Fatalf("bad plan %+v", files)
	}
	if _, err := service.List(root, listing.Directory); err != nil {
		t.Fatal("canonical destination could not be reopened", err)
	}
	for _, bad := range []string{"../outside", filepath.Join(t.TempDir(), "absolute"), "."} {
		if _, err := service.Create(root, root, bad); err == nil {
			t.Fatalf("accepted invalid folder %s", bad)
		}
	}
	if _, err := service.Review(root, target, []string{"../outside"}); err == nil {
		t.Fatal("accepted traversal")
	}
	if _, err := service.Review(root, target, []string{"."}); err == nil {
		t.Fatal("accepted folder as output file")
	}
}
func TestSymlinkPathsAndExistingFoldersAreRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	service := Service{}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.List(root, filepath.Join(root, "escape")); err == nil {
		t.Fatal("followed linked folder")
	}
	if _, err := service.Create(root, root, "escape/child"); err == nil {
		t.Fatal("created outside root through link")
	}
	if _, err := service.Review(root, root, []string{"escape/file"}); err == nil {
		t.Fatal("review followed linked path")
	}
	if err := os.Mkdir(filepath.Join(root, "tests"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Review(root, root, []string{"tests"}); err == nil {
		t.Fatal("accepted a directory as a file")
	}
	listing, err := service.List(root, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Folders) != 1 || listing.Folders[0] != "tests" {
		t.Fatalf("directory picker includes symlinks: %+v", listing)
	}
}
