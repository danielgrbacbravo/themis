package projectlink

import "time"

const CurrentSchemaVersion = 1

type Preferences struct {
	DefaultRefreshDepth          int  `json:"default_refresh_depth"`
	AutoRefreshOnOpen            bool `json:"auto_refresh_on_open"`
	ShowStaleWarningAfterMinutes int  `json:"show_stale_warning_after_minutes"`
}

type Config struct {
	SchemaVersion      int                 `json:"schema_version"`
	BaseURL            string              `json:"base_url"`
	LinkedRootURL      string              `json:"linked_root_url"`
	LinkedRootNodeID   string              `json:"linked_root_node_id,omitempty"`
	LastOpenNodeID     string              `json:"last_open_node_id,omitempty"`
	DownloadDirs       map[string]string   `json:"download_dirs,omitempty"`
	LastDownloadDir    string              `json:"last_download_dir,omitempty"`
	RecentAssetChoices map[string][]string `json:"recent_asset_choices,omitempty"`
	Preferences        Preferences         `json:"preferences"`
	UpdatedAt          time.Time           `json:"updated_at"`
}

func DefaultPreferences() Preferences {
	return Preferences{
		DefaultRefreshDepth:          1,
		AutoRefreshOnOpen:            false,
		ShowStaleWarningAfterMinutes: 120,
	}
}

func applyDefaults(cfg *Config) {
	if cfg.SchemaVersion == 0 {
		cfg.SchemaVersion = CurrentSchemaVersion
	}
	defaults := DefaultPreferences()
	if cfg.Preferences.DefaultRefreshDepth <= 0 {
		cfg.Preferences.DefaultRefreshDepth = defaults.DefaultRefreshDepth
	}
	if cfg.Preferences.ShowStaleWarningAfterMinutes <= 0 {
		cfg.Preferences.ShowStaleWarningAfterMinutes = defaults.ShowStaleWarningAfterMinutes
	}
}
