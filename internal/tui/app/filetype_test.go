package app

import (
	"github.com/charmbracelet/lipgloss"
	"testing"
	"themis-cli/internal/state"
)

func TestFileTypeIconsRecognizeSourcesTestsAndURLFallback(t *testing.T) {
	for _, tt := range []struct {
		asset       state.AssetRef
		icon, label string
	}{
		{state.AssetRef{Name: "solver.C"}, "C", "C source"},
		{state.AssetRef{Name: "skeleton.cpp"}, "C+", "C++ source"},
		{state.AssetRef{Name: "run.py"}, "Py", "Python source"},
		{state.AssetRef{Name: "1.in"}, "⇥", "Test input"},
		{state.AssetRef{Name: "1.out"}, "⇤", "Expected output"},
		{state.AssetRef{Name: "answer.expected"}, "⇤", "Expected output"},
		{state.AssetRef{Name: "Assignment", URL: "https://example.org/file/assignment.PDF?raw=true"}, "▤", "PDF document"},
		{state.AssetRef{URL: "https://example.org/file/%40tests/data.txt"}, "⇄", "Test case"},
		{state.AssetRef{Path: "tests/data.txt"}, "⇄", "Test case"},
		{state.AssetRef{Name: "unknown"}, "◇", "File"},
	} {
		kind := assetFileType(tt.asset)
		if kind.icon != tt.icon || kind.label != tt.label {
			t.Fatalf("%+v: %+v", tt.asset, kind)
		}
		if lipgloss.Width(renderFileIcon(tt.asset)) != 2 {
			t.Fatal("icon column changed width")
		}
	}
}
