// Package browserprefs stores browsing history independently of directory links.
package browserprefs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"themis-cli/internal/state"
)

type Target struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}
type Preferences struct {
	Recent []Target `json:"recent"`
}

func (p Preferences) Open(target Target) Preferences {
	url, err := state.CanonicalizeURL(target.URL)
	if err != nil {
		return p
	}
	target.URL = url
	next := Preferences{Recent: []Target{target}}
	for _, item := range p.Recent {
		canonical, err := state.CanonicalizeURL(item.URL)
		if err == nil && canonical != url && len(next.Recent) < 20 {
			item.URL = canonical
			next.Recent = append(next.Recent, item)
		}
	}
	return next
}
func Load(path string) (Preferences, error) {
	var p Preferences
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return Preferences{}, fmt.Errorf("read browser preferences: %w", err)
	}
	return p, nil
}
func Save(path string, p Preferences) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".browser-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := json.NewEncoder(f).Encode(p); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
