package app

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"themis-cli/internal/browserprefs"
	"themis-cli/internal/projectlink"
	"themis-cli/internal/state"
	"themis-cli/internal/submission"
	"themis-cli/internal/workspace"
)

type Config struct {
	DestinationService      *workspace.Service
	DownloadDirs            map[string]string
	ProjectContext          projectlink.Context
	ManageLink              func(projectlink.Change) (projectlink.Context, error)
	BrowserPreferences      browserprefs.Preferences
	SaveBrowserPreferences  func(browserprefs.Preferences) error
	BrowserPreferencesError error
	State                   state.State
	DisableAutoRefresh      bool
	ReducedMotion           bool
	RootNodeID              string
	LinkedRootNodeID        string
	SubtreeRefreshDepth     int
	RefreshExecutor         RefreshExecutor
	DownloadExecutor        DownloadExecutor
	PreviewExecutor         PreviewExecutor
	SubmissionService       *submission.Service
	DefaultDownloadDir      string
	RecentAssetChoices      map[string][]string
	PersistChoices          PersistChoicesFunc
}

func Run(cfg Config) error {
	model, err := NewModel(cfg)
	if err != nil {
		return err
	}

	program := tea.NewProgram(model, tea.WithAltScreen())
	final, err := program.Run()
	if m, ok := final.(Model); ok {
		m.stopSubmission()
		m.cancelPreview()
	}
	if err != nil {
		return fmt.Errorf("run tui: %w", err)
	}
	return nil
}
