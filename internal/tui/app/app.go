package app

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"themis-cli/internal/state"
)

type Config struct {
	State               state.State
	DisableAutoRefresh  bool
	ReducedMotion       bool
	RootNodeID          string
	LinkedRootNodeID    string
	SubtreeRefreshDepth int
	RefreshExecutor     RefreshExecutor
	DownloadExecutor    DownloadExecutor
	PreviewExecutor     PreviewExecutor
	DefaultDownloadDir  string
	RecentAssetChoices  map[string][]string
	PersistChoices      PersistChoicesFunc
}

func Run(cfg Config) error {
	model, err := NewModel(cfg)
	if err != nil {
		return err
	}

	program := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("run tui: %w", err)
	}
	return nil
}
