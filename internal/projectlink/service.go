package projectlink

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"themis-cli/internal/state"
	"themis-cli/internal/themis"
)

// Context describes the effective association inherited by the launch directory.
type Context struct {
	WorkingDir string
	ConfigPath string
	LinkDir    string
	Config     Config
}
type Change struct {
	Directory string
	TargetURL string
	Remove    bool
}
type Service struct {
	WorkingDir string
	BaseURL    string
}

func (s Service) Resolve() (Context, error) {
	cwd, err := filepath.Abs(s.WorkingDir)
	if err != nil {
		return Context{}, err
	}
	ctx := Context{WorkingDir: cwd}
	cfg, path, err := ResolveByCWD(cwd)
	if errors.Is(err, ErrNotLinked) {
		return ctx, nil
	}
	if err != nil {
		return ctx, err
	}
	if err := ValidateTarget(s.BaseURL, cfg.LinkedRootURL); err != nil {
		return ctx, err
	}
	canonical, _ := state.CanonicalizeURL(cfg.LinkedRootURL)
	cfg.LinkedRootURL = canonical
	cfg.LinkedRootNodeID = state.NodeIDFromCanonicalURL(canonical)
	ctx.Config, ctx.ConfigPath, ctx.LinkDir = cfg, path, filepath.Dir(filepath.Dir(path))
	return ctx, nil
}
func ValidateTarget(base, target string) error {
	normalized, err := themis.NormalizeBaseURL(base)
	if err != nil {
		return err
	}
	canonical, err := state.CanonicalizeURL(target)
	if err != nil {
		return err
	}
	b, _ := url.Parse(normalized)
	t, _ := url.Parse(canonical)
	if !strings.EqualFold(b.Host, t.Host) || b.Scheme != t.Scheme {
		return fmt.Errorf("target belongs to a different Themis instance")
	}
	if t.Path != "/course" && !strings.HasPrefix(t.Path, "/course/") {
		return fmt.Errorf("target must be a Themis course or assignment")
	}
	return nil
}
func (s Service) Apply(change Change) (Context, error) {
	directory := change.Directory
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(s.WorkingDir, directory)
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return Context{}, err
	}
	info, err := os.Stat(directory)
	if err != nil {
		return Context{}, err
	}
	if !info.IsDir() {
		return Context{}, fmt.Errorf("link directory is not a directory")
	}
	path := ConfigPathFromRepoRoot(directory)
	if change.Remove {
		if err := os.Remove(path); err != nil {
			return Context{}, fmt.Errorf("remove project link: %w", err)
		}
	} else {
		if err := ValidateTarget(s.BaseURL, change.TargetURL); err != nil {
			return Context{}, err
		}
		cfg, err := Load(path)
		if err != nil && !os.IsNotExist(err) {
			return Context{}, err
		}
		if os.IsNotExist(err) {
			cfg = Config{Preferences: DefaultPreferences()}
		}
		canonical, _ := state.CanonicalizeURL(change.TargetURL)
		if cfg.LinkedRootURL != canonical {
			cfg.LastOpenNodeID = ""
		}
		cfg.BaseURL, cfg.LinkedRootURL = s.BaseURL, canonical
		cfg.LinkedRootNodeID = state.NodeIDFromCanonicalURL(canonical)
		if err := Save(path, cfg); err != nil {
			return Context{}, err
		}
	}
	return s.Resolve()
}
