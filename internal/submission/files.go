package submission

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dlclark/regexp2"
)

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}

func Expand(ctx context.Context, selection Selection) ([]File, error) {
	var files []File
	seen := map[string]bool{}
	add := func(source, name, field string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Lstat(source)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", source, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("not a regular file (symlinks are not followed): %s", source)
		}
		name = filepath.ToSlash(name)
		if strings.ContainsAny(name, "\r\n\x00") || strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("invalid destination filename: %q", name)
		}
		if seen[name] {
			return fmt.Errorf("duplicate destination filename: %s", name)
		}
		seen[name] = true
		files = append(files, File{Source: source, Name: name, Field: field, Size: info.Size()})
		return nil
	}
	for _, file := range selection.Files {
		abs, err := filepath.Abs(file)
		if err != nil {
			return nil, err
		}
		if err = add(abs, filepath.Base(abs), "files[]"); err != nil {
			return nil, err
		}
	}
	for _, folder := range selection.Folders {
		abs, err := filepath.Abs(folder)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(abs)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("not a directory: %s", folder)
		}
		before := len(files)
		err = filepath.WalkDir(abs, func(p string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == ".git" || d.Name() == ".themis" {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			rel, err := filepath.Rel(filepath.Dir(abs), p)
			if err != nil {
				return err
			}
			return add(p, rel, "folders[]")
		})
		if err != nil {
			return nil, err
		}
		if len(files) == before {
			return nil, fmt.Errorf("folder has no regular files: %s", folder)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("select at least one file or folder")
	}
	return files, nil
}
func Validate(form Form, files []File, language string) (string, error) {
	if len(files) == 0 {
		return "", fmt.Errorf("select at least one file")
	}
	if form.MaxFiles > 0 && len(files) > form.MaxFiles {
		return "", fmt.Errorf("%d files selected; maximum is %d", len(files), form.MaxFiles)
	}
	var total int64
	var rule *regexp2.Regexp
	var err error
	if form.FilenameRule != "" {
		rule, err = regexp2.Compile(form.FilenameRule, regexp2.ECMAScript)
		if err != nil {
			return "", fmt.Errorf("unsupported filename rule: %w", err)
		}
		rule.MatchTimeout = 100 * time.Millisecond
	}
	matches := map[string]bool{}
	for _, f := range files {
		if f.Field == "folders[]" && !form.FoldersAllowed {
			return "", fmt.Errorf("this form does not accept folder uploads")
		}
		total += f.Size
		if total < 0 {
			return "", fmt.Errorf("upload size overflow")
		}
		if rule != nil {
			ok, e := rule.MatchString(filepath.Base(f.Name))
			if e != nil {
				return "", fmt.Errorf("filename validation timed out")
			}
			if !ok {
				return "", fmt.Errorf("filename not permitted: %s", f.Name)
			}
		}
		if l := form.Suffixes[strings.TrimPrefix(filepath.Ext(f.Name), ".")]; l != "" {
			matches[l] = true
		}
	}
	if form.MaxBytes > 0 && total > form.MaxBytes {
		return "", fmt.Errorf("combined size %d bytes exceeds maximum %d", total, form.MaxBytes)
	}
	if language != "" {
		if !contains(form.Languages, language) {
			return "", ErrLanguage
		}
		return language, nil
	}
	if len(form.Languages) == 1 {
		return form.Languages[0], nil
	}
	if len(matches) == 1 {
		for l := range matches {
			if contains(form.Languages, l) {
				return l, nil
			}
		}
	}
	if len(form.Languages) == 0 {
		return "", nil
	}
	return "", ErrLanguage
}
func (s *Service) Prepare(ctx context.Context, selection Selection) (*Prepared, error) {
	form, err := s.ReadForm(ctx, selection.AssignmentURL)
	if err != nil {
		return nil, err
	}
	files, err := Expand(ctx, selection)
	if err != nil {
		return nil, err
	}
	language, err := Validate(form, files, selection.Language)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "themis-submit-")
	if err != nil {
		return nil, err
	}
	p := &Prepared{Form: form, Files: files, Language: language, dir: dir}
	// Cancellation also owns the finished snapshot until Cleanup transfers it
	// out of scope. This covers an asynchronous UI result discarded on quit.
	p.mu.Lock()
	p.stopCleanup = context.AfterFunc(ctx, func() { _ = p.Cleanup() })
	p.mu.Unlock()
	success := false
	defer func() {
		if !success {
			_ = p.Cleanup()
		}
	}()
	for i := range p.Files {
		f := &p.Files[i]
		before, err := os.Lstat(f.Source)
		if err != nil {
			return nil, err
		}
		if !before.Mode().IsRegular() {
			return nil, fmt.Errorf("source is no longer a regular file: %s", f.Source)
		}
		in, err := os.Open(f.Source)
		if err != nil {
			return nil, err
		}
		st, e := in.Stat()
		if e != nil || !st.Mode().IsRegular() || !os.SameFile(before, st) {
			in.Close()
			return nil, fmt.Errorf("source file is no longer readable: %s", f.Source)
		}
		out, err := os.CreateTemp(dir, "file-")
		if err != nil {
			in.Close()
			return nil, err
		}
		f.Snapshot = out.Name()
		hash := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(out, hash), io.LimitReader(contextReader{ctx, in}, f.Size+1))
		closeErr := out.Close()
		in.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if n != f.Size {
			return nil, fmt.Errorf("file changed while preparing: %s; select it again", f.Source)
		}
		f.SHA256 = fmt.Sprintf("%x", hash.Sum(nil))
		p.TotalBytes += n
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	success = true
	return p, nil
}
func (p *Prepared) Cleanup() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopCleanup != nil {
		p.stopCleanup()
		p.stopCleanup = nil
	}
	if p.dir == "" {
		return nil
	}
	err := os.RemoveAll(p.dir)
	if err == nil {
		p.dir = ""
	}
	return err
}
func (p *Prepared) ApplyReview(form Form, language string) error {
	l, err := Validate(form, p.Files, language)
	if err != nil {
		return err
	}
	p.Form = form
	p.Language = l
	return nil
}
