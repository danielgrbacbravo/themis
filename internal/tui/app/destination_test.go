package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"themis-cli/internal/state"
	"themis-cli/internal/workspace"
)

func destinationFixture(t *testing.T) Model {
	t.Helper()
	m := browserFixture(t)
	root := t.TempDir()
	m.browser.project.LinkDir = root
	m.browser.project.WorkingDir = root
	m.defaultDownloadDir = root
	m.destination.service = &workspace.Service{}
	m.reducedMotion = true
	return m
}
func destinationFinish(t *testing.T, m Model, cmd tea.Cmd) (Model, tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("missing folder operation")
	}
	next, nextCmd := m.Update(workMessage(cmd))
	return next.(Model), nextCmd
}
func TestDownloadDestinationCreateReviewAndRemember(t *testing.T) {
	m := destinationFixture(t)
	var requests []DownloadRequest
	m.downloadExecutor = func(_ state.State, req DownloadRequest) DownloadOutcome {
		requests = append(requests, req)
		return DownloadOutcome{NodeID: req.NodeID, TargetDir: req.TargetDir, Downloaded: 1}
	}
	m, _ = sendKey(m, "d")
	m, cmd := sendKey(m, "enter")
	if len(requests) != 0 || m.browser.overlay != "destination" {
		t.Fatal("download began before destination review")
	}
	m, _ = destinationFinish(t, m, cmd)
	m, _ = sendKey(m, "n")
	m, _ = sendKey(m, "pc-lab-07/pc-lab-07-e01")
	m, cmd = sendKey(m, "enter")
	m, _ = destinationFinish(t, m, cmd)
	folder := m.destination.dir
	if _, err := os.Stat(folder); err != nil {
		t.Fatal(err)
	}
	if filepath.Base(folder) != "pc-lab-07-e01" {
		t.Fatal("nested folder not selected")
	}
	for _, size := range [][2]int{{100, 30}, {50, 16}, {24, 8}} {
		m = resize(m, size[0], size[1])
		assertFits(t, m.View(), size[0], size[1])
		if !strings.Contains(m.View(), "Esc") {
			t.Fatal("destination picker lost its cancel hint")
		}
	}
	m = resize(m, 100, 30)
	m, cmd = sendKey(m, "enter")
	m, _ = destinationFinish(t, m, cmd)
	for _, size := range [][2]int{{100, 30}, {50, 16}, {24, 8}} {
		m = resize(m, size[0], size[1])
		assertFits(t, m.View(), size[0], size[1])
	}
	m = resize(m, 100, 30)
	if m.destination.phase != "review" || len(requests) != 0 {
		t.Fatal("review must not start a download")
	}
	if !strings.Contains(m.renderDestination(90, 24), "tests/1.in") {
		t.Fatal("review omits actual test destination")
	}
	t.Logf("Destination review:\n%s", m.View())
	m, cmd = sendKey(m, "enter")
	if !m.downloadInFlight || m.downloadTargetDir != folder {
		t.Fatal("reviewed folder not used")
	}
	for m.downloadInFlight {
		m, cmd = destinationFinish(t, m, cmd)
	}
	if len(requests) != 2 || requests[0].RelativePath != filepath.Join("tests", "1.in") || requests[1].RelativePath != filepath.Join("tests", "1.out") {
		t.Fatalf("wrong per-file plan %+v", requests)
	}
	if m.downloadDirs[m.selectedNodeID] != folder {
		t.Fatal("assignment folder not remembered")
	}
	m, _ = sendKey(m, "esc")
	m, _ = sendKey(m, "d")
	m, cmd = sendKey(m, "enter")
	m, _ = destinationFinish(t, m, cmd)
	if m.destination.dir != folder {
		t.Fatal("assignment did not reopen remembered folder")
	}
}
func TestDestinationCancelAndLateReply(t *testing.T) {
	m := destinationFixture(t)
	m, _ = sendKey(m, "d")
	original := m.defaultDownloadDir
	m, cmd := sendKey(m, "enter")
	generation := m.destination.generation
	m, _ = sendKey(m, "esc")
	next, _ := m.Update(workMessage(cmd))
	m = next.(Model)
	if m.browser.overlay != "" || m.destination.generation == generation || m.defaultDownloadDir != original || m.downloadInFlight {
		t.Fatal("late reply reopened canceled destination")
	}
	m, cmd = sendKey(m, "enter")
	m, _ = destinationFinish(t, m, cmd)
	m, _ = sendKey(m, "n")
	m, _ = sendKey(m, "../escape")
	m, cmd = sendKey(m, "enter")
	m, _ = destinationFinish(t, m, cmd)
	if m.destination.message == "" || m.destination.phase != "create" {
		t.Fatal("folder error was hidden")
	}
	m, _ = sendKey(m, "esc")
	m, cmd = sendKey(m, "enter")
	m, _ = destinationFinish(t, m, cmd)
	m, _ = sendKey(m, "esc")
	if m.destination.phase != "select" || m.downloadInFlight {
		t.Fatal("review escape did not return to folders")
	}
}
