package discovery

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"themis-cli/internal/state"
)

type DownloadedAsset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Path string `json:"path"`
}

func DownloadAssetRefs(client *http.Client, assets []state.AssetRef, targetDir string) ([]DownloadedAsset, error) {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, fmt.Errorf("create target dir: %w", err)
	}

	downloaded := make([]DownloadedAsset, 0, len(assets))
	usedPaths := map[string]int{}

	for i, asset := range assets {
		assetURL := strings.TrimSpace(asset.URL)
		if assetURL == "" || state.IsSiteInfoURL(assetURL) {
			continue
		}

		rawURL := assetURL
		if looksLikeThemisFile(assetURL) {
			rawURL = ensureRawQuery(assetURL)
		}

		body, err := downloadRawFile(client, rawURL)
		if err != nil {
			return nil, fmt.Errorf("download asset %s: %w", assetURL, err)
		}

		relPath := resolveAssetRelativePath(asset, i+1)
		relPath = dedupeRelativePath(relPath, usedPaths)
		outPath := filepath.Join(targetDir, relPath)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return nil, fmt.Errorf("create asset parent dir: %w", err)
		}
		if err := os.WriteFile(outPath, body, 0o644); err != nil {
			return nil, fmt.Errorf("write asset %s: %w", outPath, err)
		}

		downloaded = append(downloaded, DownloadedAsset{
			Name: filepath.Base(relPath),
			URL:  assetURL,
			Path: outPath,
		})
	}

	return downloaded, nil
}

func looksLikeThemisFile(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return strings.Contains(parsed.Path, "/file/")
}

func ensureRawQuery(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := parsed.Query()
	if q.Get("raw") == "true" {
		return parsed.String()
	}
	q.Set("raw", "true")
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

func resolveAssetRelativePath(asset state.AssetRef, index int) string {
	if rel, ok := relativePathFromThemisTestsURL(asset.URL); ok {
		return filepath.Join("tests", sanitizeRelativePath(rel, index))
	}
	if strings.TrimSpace(asset.Path) != "" {
		return sanitizeRelativePath(asset.Path, index)
	}
	if strings.TrimSpace(asset.Name) != "" {
		return sanitizeRelativePath(asset.Name, index)
	}
	if parsed, err := url.Parse(asset.URL); err == nil {
		if rel, ok := relativePathFromAssetURL(parsed); ok {
			return sanitizeRelativePath(rel, index)
		}
	}
	return sanitizeRelativePath("asset-"+strconv.Itoa(index), index)
}

func sanitizeFilename(name string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "\x00", "")
	clean := strings.TrimSpace(replacer.Replace(name))
	if clean == "" {
		return "asset"
	}
	return clean
}

func dedupeRelativePath(relPath string, used map[string]int) string {
	if _, ok := used[relPath]; !ok {
		used[relPath] = 1
		return relPath
	}
	count := used[relPath]
	used[relPath] = count + 1
	dir := filepath.Dir(relPath)
	name := filepath.Base(relPath)
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	next := fmt.Sprintf("%s-%d%s", base, count+1, ext)
	if dir == "." {
		return next
	}
	return filepath.Join(dir, next)
}

func relativePathFromThemisTestsURL(rawURL string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", false
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	for i, seg := range segments {
		if seg == "%40tests" || seg == "@tests" {
			if i+1 >= len(segments) {
				return "", false
			}
			parts := make([]string, 0, len(segments)-(i+1))
			for _, s := range segments[i+1:] {
				unescaped, err := url.PathUnescape(s)
				if err != nil {
					parts = append(parts, s)
					continue
				}
				parts = append(parts, unescaped)
			}
			return strings.Join(parts, "/"), true
		}
	}
	return "", false
}

func relativePathFromAssetURL(parsed *url.URL) (string, bool) {
	cleanPath := strings.TrimSpace(parsed.EscapedPath())
	if cleanPath == "" || cleanPath == "/" {
		return "", false
	}
	if strings.HasPrefix(cleanPath, "/imgs/") {
		unescaped, err := url.PathUnescape(strings.TrimPrefix(cleanPath, "/"))
		if err != nil {
			return strings.TrimPrefix(cleanPath, "/"), true
		}
		return unescaped, true
	}
	base := path.Base(cleanPath)
	if base == "" || base == "." || base == "/" {
		return "", false
	}
	unescaped, err := url.PathUnescape(base)
	if err != nil {
		return base, true
	}
	return unescaped, true
}

func sanitizeRelativePath(raw string, index int) string {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	raw = strings.TrimPrefix(raw, "/")
	if raw == "" {
		return "asset-" + strconv.Itoa(index)
	}
	clean := path.Clean(raw)
	if clean == "." || clean == "" || clean == "/" || clean == ".." || strings.HasPrefix(clean, "../") {
		return "asset-" + strconv.Itoa(index)
	}
	segments := strings.Split(clean, "/")
	out := make([]string, 0, len(segments))
	for _, seg := range segments {
		seg = sanitizeFilename(seg)
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		out = append(out, seg)
	}
	if len(out) == 0 {
		return "asset-" + strconv.Itoa(index)
	}
	return filepath.Join(out...)
}
