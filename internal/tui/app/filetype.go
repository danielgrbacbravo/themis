package app

import (
	"net/url"
	"path"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"themis-cli/internal/state"
)

type fileType struct {
	icon, label string
}

// Use ordinary glyphs and short language marks rather than private-use font
// icons. The icon column has a fixed cell width across terminal fonts/themes.
func assetFileType(asset state.AssetRef) fileType {
	names := []string{asset.Name, asset.Path}
	if u, err := url.Parse(asset.URL); err == nil {
		names = append(names, u.Path)
	}
	for _, name := range names {
		base := strings.ToLower(path.Base(strings.ReplaceAll(name, "\\", "/")))
		switch base {
		case "makefile", "gnumakefile", "cmakelists.txt":
			return fileType{"#", "Build file"}
		case "dockerfile":
			return fileType{"#", "Container file"}
		}
		switch strings.ToLower(path.Ext(base)) {
		case ".in":
			return fileType{"⇥", "Test input"}
		case ".out", ".ans", ".expected":
			return fileType{"⇤", "Expected output"}
		case ".c":
			return fileType{"C", "C source"}
		case ".h":
			return fileType{"H", "C/C++ header"}
		case ".cc", ".cpp", ".cxx", ".c++":
			return fileType{"C+", "C++ source"}
		case ".hpp", ".hh", ".hxx":
			return fileType{"H+", "C++ header"}
		case ".py", ".pyi":
			return fileType{"Py", "Python source"}
		case ".rs":
			return fileType{"Rs", "Rust source"}
		case ".go":
			return fileType{"Go", "Go source"}
		case ".java":
			return fileType{"Jv", "Java source"}
		case ".js", ".jsx", ".mjs", ".cjs":
			return fileType{"JS", "JavaScript source"}
		case ".ts", ".tsx":
			return fileType{"TS", "TypeScript source"}
		case ".sh", ".bash", ".zsh", ".fish":
			return fileType{"#", "Shell script"}
		case ".json", ".yaml", ".yml", ".toml", ".xml", ".csv":
			return fileType{"{}", "Data/config"}
		case ".html", ".htm", ".css", ".scss":
			return fileType{"<>", "Web source"}
		case ".pdf":
			return fileType{"▤", "PDF document"}
		case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".bmp", ".ico":
			return fileType{"▧", "Image"}
		case ".zip", ".gz", ".bz2", ".xz", ".tar", ".7z":
			return fileType{"▦", "Archive"}
		}
	}
	for _, name := range names {
		for _, component := range strings.Split(strings.ToLower(strings.ReplaceAll(name, "\\", "/")), "/") {
			if component == "tests" || component == "@tests" {
				return fileType{"⇄", "Test case"}
			}
		}
	}
	for _, name := range names {
		switch strings.ToLower(path.Ext(name)) {
		case ".txt", ".md", ".rst", ".log":
			return fileType{"≡", "Text document"}
		}
	}
	return fileType{"◇", "File"}
}

func renderFileIcon(asset state.AssetRef) string {
	kind := assetFileType(asset)
	style := titleStyle
	if kind.label == "Test input" || kind.label == "Expected output" || kind.label == "Test case" {
		style = infoStyle
	}
	return style.Render(kind.icon) + strings.Repeat(" ", maxInt(0, 2-lipgloss.Width(kind.icon)))
}
