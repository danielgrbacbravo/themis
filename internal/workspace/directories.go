// Package workspace handles local download folders independently of the TUI.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Service struct{}
type Listing struct {
	Root, Directory string
	Folders         []string
}
type File struct {
	Path, Relative string
	Exists         bool
}

func outside(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel)
}

// Bound rejects traversal and symlink components beneath the chosen root.
// The root itself may be reached through a normal platform symlink.
func (Service) Bound(root, target string) (string, string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", "", err
	}
	physical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || outside(rel) {
		rel, err = filepath.Rel(physical, target)
		if err != nil || outside(rel) {
			return "", "", fmt.Errorf("choose a folder inside the linked project")
		}
	}
	info, err := os.Stat(physical)
	if err != nil {
		return "", "", err
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("project root is not a folder")
	}
	current := physical
	if rel != "." {
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			info, err := os.Lstat(current)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return "", "", err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return "", "", fmt.Errorf("symlink paths cannot be used for downloads")
			}
		}
	}
	return physical, filepath.Join(physical, rel), nil
}
func (s Service) List(root, directory string) (Listing, error) {
	root, directory, err := s.Bound(root, directory)
	if err != nil {
		return Listing{}, err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return Listing{}, err
	}
	result := Listing{Root: root, Directory: directory}
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != ".git" && entry.Name() != ".themis" {
			result.Folders = append(result.Folders, entry.Name())
		}
	}
	return result, nil
}
func (s Service) Create(root, current, name string) (Listing, error) {
	if strings.TrimSpace(name) == "" || filepath.IsAbs(name) {
		return Listing{}, fmt.Errorf("enter a relative folder name or nested path")
	}
	_, current, err := s.Bound(root, current)
	if err != nil {
		return Listing{}, err
	}
	rel := filepath.Clean(name)
	if outside(rel) || rel == "." {
		return Listing{}, fmt.Errorf("enter a folder name inside the current folder")
	}
	// Bound uses the original spelling of root; current may now be canonical.
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return Listing{}, err
	}
	_, target, err := s.Bound(root, filepath.Join(current, rel))
	if err != nil {
		return Listing{}, err
	}
	if err := os.MkdirAll(target, 0755); err != nil {
		return Listing{}, fmt.Errorf("create folder: %w", err)
	}
	return s.List(root, target)
}
func (s Service) Review(root, directory string, relativePaths []string) ([]File, error) {
	listing, err := s.List(root, directory)
	if err != nil {
		return nil, err
	}
	files := make([]File, 0, len(relativePaths))
	for _, rel := range relativePaths {
		if rel == "." || outside(filepath.Clean(rel)) {
			return nil, fmt.Errorf("invalid download filename")
		}
		_, target, err := s.Bound(listing.Root, filepath.Join(listing.Directory, rel))
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(target)
		exists := err == nil
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if exists && !info.Mode().IsRegular() {
			return nil, fmt.Errorf("download target is not a regular file: %s", target)
		}
		files = append(files, File{Path: target, Relative: rel, Exists: exists})
	}
	return files, nil
}
