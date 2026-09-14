package discovery

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"themis-cli/internal/filepreview"
	"themis-cli/internal/state"
)

// PreviewAsset reads at most one bounded prefix with the authenticated client.
// Cancellation and deadlines are owned by the caller; no files are persisted.
func PreviewAsset(ctx context.Context, client *http.Client, asset state.AssetRef) (filepreview.Content, error) {
	var content filepreview.Content
	if state.IsSiteInfoURL(asset.URL) {
		return content, fmt.Errorf("site information has no file preview")
	}
	rawURL := asset.URL
	if looksLikeThemisFile(rawURL) {
		rawURL = ensureRawQuery(rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return content, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return content, fmt.Errorf("load preview: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && resp.Header.Get("Content-Range") == "bytes */0" {
		return content, nil
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return content, fmt.Errorf("preview request returned HTTP %d", resp.StatusCode)
	}
	// Never show a redirected login page as the contents of the selected file.
	if resp.Request != nil && resp.Request.URL != nil {
		original, _ := url.Parse(rawURL)
		final := resp.Request.URL
		if final.Host != original.Host || (final.Path != original.Path && (strings.Contains(strings.ToLower(final.Path), "login") || strings.Contains(strings.ToLower(final.Path), "sso"))) {
			return content, fmt.Errorf("preview requires authentication; sign in again")
		}
	}
	body := bufio.NewReader(resp.Body)
	prefix, _ := body.Peek(512)
	pdfFile := filepreview.IsPDF(asset.Name, prefix) || filepreview.IsPDF(asset.URL, nil) || strings.HasPrefix(resp.Header.Get("Content-Type"), "application/pdf")
	limit := filepreview.MaxBytes
	if pdfFile {
		limit = filepreview.MaxPDFBytes
	}
	data, err := io.ReadAll(io.LimitReader(body, int64(limit+1)))
	if err != nil {
		return content, fmt.Errorf("read preview: %w", err)
	}
	ext := strings.ToLower(path.Ext(asset.Name))
	if isHTMLDocument(data) && ext != ".html" && ext != ".htm" {
		return content, fmt.Errorf("received a web page instead of a file; the session may have expired")
	}
	content.Truncated = len(data) > limit
	if content.Truncated {
		data = data[:limit]
	}
	if resp.StatusCode == http.StatusPartialContent {
		var first, last, total int64
		n, _ := fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-%d/%d", &first, &last, &total)
		if n < 2 || first != 0 || last < first {
			return content, fmt.Errorf("server returned an invalid preview range")
		}
		if n < 3 || last+1 < total {
			content.Truncated = true
		}
	}
	if pdfFile && content.Truncated {
		return content, fmt.Errorf("PDF preview requires the complete file (limit 8 MiB); download to inspect")
	}
	content.Data = data
	return content, nil
}
