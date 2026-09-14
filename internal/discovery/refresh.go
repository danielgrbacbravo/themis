package discovery

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"themis-cli/internal/state"
)

type RefreshResult struct {
	TargetURL    string   `json:"target_url"`
	Depth        int      `json:"depth"`
	FetchedNodes int      `json:"fetched_nodes"`
	UpdatedNodes int      `json:"updated_nodes"`
	RemovedEdges int      `json:"removed_edges"`
	Errors       []string `json:"errors"`
}

const maxRemovedChildTombstones = 100

type pageChild struct {
	Title     string
	URL       string
	NavAPIURL string
}

type pageSnapshot struct {
	Title       string
	Kind        string
	Canonical   string
	NavAPIURL   string
	Children    []pageChild
	Details     map[string]any
	Assets      []state.AssetRef
	ContentHash string
}

func (s *Service) RefreshCatalog(client *http.Client, st *state.State, depth int) (RefreshResult, error) {
	if st == nil {
		return RefreshResult{}, fmt.Errorf("state is nil")
	}
	target := strings.TrimSpace(st.CatalogRootURL)
	if target == "" {
		target = strings.TrimRight(s.BaseURL, "/") + "/course"
	}
	return s.RefreshNode(client, st, target, depth)
}

func (s *Service) RefreshNode(client *http.Client, st *state.State, targetURL string, depth int) (RefreshResult, error) {
	if st == nil {
		return RefreshResult{}, fmt.Errorf("state is nil")
	}
	if client == nil {
		return RefreshResult{}, fmt.Errorf("http client is nil")
	}
	if depth < 0 {
		return RefreshResult{}, fmt.Errorf("depth must be >= 0")
	}

	normalizedTarget, err := s.normalizeURL(targetURL)
	if err != nil {
		return RefreshResult{}, err
	}
	canonicalTarget, err := state.CanonicalizeURL(normalizedTarget)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("canonicalize target URL: %w", err)
	}

	now := time.Now().UTC()
	result := RefreshResult{
		TargetURL: canonicalTarget,
		Depth:     depth,
		Errors:    []string{},
	}
	updatedNodeIDs := map[string]struct{}{}
	visited := map[string]struct{}{}

	var walk func(canonicalURL string, remainingDepth int, parentID string)
	walk = func(canonicalURL string, remainingDepth int, parentID string) {
		if _, ok := visited[canonicalURL]; ok {
			if parentID != "" {
				childID := state.NodeIDFromCanonicalURL(canonicalURL)
				if edgeAdded(st, parentID, childID, now) {
					updatedNodeIDs[parentID] = struct{}{}
					updatedNodeIDs[childID] = struct{}{}
				}
			}
			return
		}
		visited[canonicalURL] = struct{}{}

		snap, fetchErr := s.fetchPageSnapshot(client, canonicalURL)
		if fetchErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", canonicalURL, fetchErr))
			errNodeID := markNodeFetchError(st, canonicalURL, fetchErr.Error(), now)
			updatedNodeIDs[errNodeID] = struct{}{}
			return
		}
		result.FetchedNodes++
		if s.Progress != nil {
			s.Progress(RefreshProgress{
				URL:          snap.Canonical,
				Title:        snap.Title,
				FetchedNodes: result.FetchedNodes,
			})
		}

		nodeID := state.NodeIDFromCanonicalURL(snap.Canonical)
		current, exists := st.Nodes[nodeID]

		if snap.Kind == "assignment" {
			explicitStatusURL := statusPageLinkFromDetails(snap.Details)
			statusURL := explicitStatusURL
			if statusURL == "" {
				statusURL = statusPageLinkFromDetails(current.Details)
			}
			if statusURL == "" {
				statusURL = deriveStatsPageURL(snap.Canonical)
			}
			if statusURL != "" {
				snap.Details = withStatusPageLink(snap.Details, statusURL)
				shouldFetchStats := explicitStatusURL != "" || hasStatsDetails(current.Details) || (parentID == "" && depth == 0)
				if shouldFetchStats {
					stats, err := s.fetchAssignmentStats(client, statusURL)
					if err != nil {
						result.Errors = append(result.Errors, fmt.Sprintf("[stats] %s: %v", statusURL, err))
					} else {
						snap.Details = withStatsDetails(snap.Details, stats)
					}
				}
			}
		}

		patch := state.Node{
			ID:               nodeID,
			Kind:             snap.Kind,
			Title:            snap.Title,
			CanonicalURL:     snap.Canonical,
			NavAPIURL:        snap.NavAPIURL,
			ParentIDs:        append([]string{}, current.ParentIDs...),
			ChildIDs:         append([]string{}, current.ChildIDs...),
			ChildrenHydrated: exists && current.ChildrenHydrated,
			DepthHint:        computeDepthHint(snap.Canonical),
			Status:           current.Status,
			LastFetchedAt:    current.LastFetchedAt,
			LastSuccessAt:    current.LastSuccessAt,
			LastError:        current.LastError,
			ContentHash:      "sha256:" + snap.ContentHash,
			Details:          mergeDetails(current.Details, snap.Details),
			Assets:           snap.Assets,
			CreatedAt:        current.CreatedAt,
			UpdatedAt:        current.UpdatedAt,
		}
		if err := state.ApplyFetchSuccess(&patch, now); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", canonicalURL, err))
			return
		}

		_, changed, upsertErr := state.UpsertNode(st, patch, now)
		if upsertErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", canonicalURL, upsertErr))
			return
		}
		if changed {
			updatedNodeIDs[nodeID] = struct{}{}
		}

		if parentID != "" {
			if edgeAdded(st, parentID, nodeID, now) {
				updatedNodeIDs[parentID] = struct{}{}
				updatedNodeIDs[nodeID] = struct{}{}
			}
		}

		children := make([]string, 0, len(snap.Children))
		for _, child := range snap.Children {
			childCanonical, cErr := state.CanonicalizeURL(child.URL)
			if cErr != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("%s child URL %q: %v", canonicalURL, child.URL, cErr))
				continue
			}
			childID := state.NodeIDFromCanonicalURL(childCanonical)
			children = append(children, childID)

			childNode, ok := st.Nodes[childID]
			if !ok {
				childNode = state.Node{
					ID:           childID,
					CanonicalURL: childCanonical,
					Status:       state.StatusNever,
					Assets:       []state.AssetRef{},
					CreatedAt:    now,
					UpdatedAt:    now,
				}
			}
			if strings.TrimSpace(child.Title) != "" {
				childNode.Title = child.Title
			}
			if strings.TrimSpace(child.NavAPIURL) != "" {
				childNode.NavAPIURL = child.NavAPIURL
			}
			if childNode.Kind == "" {
				childNode.Kind = inferKindFromURL(childCanonical)
			}
			if childNode.Kind == "assignment" {
				if statusURL := deriveStatsPageURL(childCanonical); statusURL != "" {
					childNode.Details = withStatusPageLink(childNode.Details, statusURL)
				}
			}
			if childNode.DepthHint == 0 {
				childNode.DepthHint = computeDepthHint(childCanonical)
			}
			if !containsString(childNode.ParentIDs, nodeID) {
				childNode.ParentIDs = append(childNode.ParentIDs, nodeID)
			}
			_, cChanged, cErr := state.UpsertNode(st, childNode, now)
			if cErr != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("upsert child %s: %v", childCanonical, cErr))
				continue
			}
			if cChanged {
				updatedNodeIDs[childID] = struct{}{}
			}
		}

		diff, setErr := state.SetChildren(st, nodeID, children, now)
		if setErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("set children for %s: %v", canonicalURL, setErr))
			return
		}
		if len(diff.Added) > 0 || len(diff.Removed) > 0 {
			updatedNodeIDs[nodeID] = struct{}{}
			for _, id := range diff.Added {
				updatedNodeIDs[id] = struct{}{}
			}
			for _, id := range diff.Removed {
				updatedNodeIDs[id] = struct{}{}
			}
		}
		if len(diff.Removed) > 0 {
			parent := st.Nodes[nodeID]
			if err := state.ApplyChildRemovalTombstones(&parent, diff.Removed, now, maxRemovedChildTombstones); err == nil {
				st.Nodes[nodeID] = parent
			}
		}
		result.RemovedEdges += len(diff.Removed)

		if remainingDepth == 0 {
			return
		}
		for _, child := range snap.Children {
			childCanonical, cErr := state.CanonicalizeURL(child.URL)
			if cErr != nil {
				continue
			}
			walk(childCanonical, remainingDepth-1, nodeID)
		}
	}

	walk(canonicalTarget, depth, "")
	result.UpdatedNodes = len(updatedNodeIDs)

	if st.BaseURL == "" {
		if base, err := state.CanonicalizeURL(strings.TrimRight(s.BaseURL, "/")); err == nil {
			st.BaseURL = strings.TrimRight(base, "/")
		}
	}
	if st.CatalogRootURL == "" {
		st.CatalogRootURL = strings.TrimRight(s.BaseURL, "/") + "/course"
	}

	return result, nil
}

func (s *Service) fetchPageSnapshot(client *http.Client, pageURL string) (pageSnapshot, error) {
	resp, err := client.Get(pageURL)
	if err != nil {
		return pageSnapshot{}, fmt.Errorf("fetch page: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return pageSnapshot{}, fmt.Errorf("fetch page status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return pageSnapshot{}, fmt.Errorf("read page: %w", err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return pageSnapshot{}, fmt.Errorf("parse page: %w", err)
	}

	canonical, err := state.CanonicalizeURL(pageURL)
	if err != nil {
		return pageSnapshot{}, err
	}
	children, err := s.extractChildren(doc, canonical)
	if err != nil {
		return pageSnapshot{}, err
	}

	h := sha256.Sum256(body)

	return pageSnapshot{
		Title:       extractTitle(doc),
		Kind:        inferKindFromURL(canonical),
		Canonical:   canonical,
		NavAPIURL:   s.extractCurrentNavAPIURL(doc, canonical),
		Children:    children,
		Details:     extractDetails(doc, canonical),
		Assets:      extractAssets(doc, canonical),
		ContentHash: hex.EncodeToString(h[:]),
	}, nil
}

func (s *Service) fetchAssignmentStats(client *http.Client, statsURL string) (map[string]any, error) {
	resp, err := client.Get(statsURL)
	if err != nil {
		return nil, fmt.Errorf("fetch stats page: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("fetch stats page status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read stats page: %w", err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("parse stats page: %w", err)
	}

	canonicalStatsURL, err := state.CanonicalizeURL(statsURL)
	if err != nil {
		return nil, fmt.Errorf("canonicalize stats URL: %w", err)
	}

	section := doc.Find("section.status").First()
	if section.Length() == 0 {
		return nil, fmt.Errorf("status section not found")
	}

	summary := map[string]any{}
	if title := strings.TrimSpace(section.Find(".sec-heading .sec-title a").First().Text()); title != "" {
		summary["title"] = title
	}

	counts := map[string]any{}
	submissionRefs := map[string]any{}
	currentGroup := ""

	section.Find(".cfg-group-title, .cfg-line").Each(func(_ int, sel *goquery.Selection) {
		if sel.HasClass("cfg-group-title") {
			currentGroup = normalizeConfigKey(sel.Text())
			return
		}
		if !sel.HasClass("cfg-line") {
			return
		}

		key := normalizeConfigKey(sel.Find(".cfg-key").First().Text())
		keySel := sel.Find(".cfg-key").First().Clone()
		keySel.Find(".tip-text").Remove()
		key = normalizeConfigKey(keySel.Text())
		if key == "" {
			return
		}
		valSel := sel.Find(".cfg-val").First()
		valText := strings.TrimSpace(valSel.Text())

		link := valSel.Find("a").First()
		linkURL := ""
		linkTitle := ""
		if link.Length() > 0 {
			linkTitle = strings.TrimSpace(link.Text())
			if href, ok := link.Attr("href"); ok {
				if abs, rErr := resolveLinkFromCanonical(canonicalStatsURL, href); rErr == nil {
					linkURL = abs
				}
			}
		}

		switch currentGroup {
		case "counts":
			if v, ok := parseIntValue(valText); ok {
				counts[key] = v
			}
		case "submissions":
			ref := map[string]any{}
			if linkURL != "" {
				ref["url"] = linkURL
			}
			if linkTitle != "" {
				ref["title"] = linkTitle
			}
			statusClass := statusClassFromSelection(valSel)
			if statusClass != "" {
				ref["status"] = statusClass
			}
			if len(ref) > 0 {
				submissionRefs[key] = ref
			}
		default:
			switch key {
			case "assignment":
				if linkURL != "" {
					summary["assignment_url"] = linkURL
				}
				if linkTitle != "" {
					summary["assignment_title"] = linkTitle
				} else if valText != "" {
					summary["assignment_title"] = valText
				}
			case "group", "grade", "language", "visible":
				if valText != "" {
					summary[key] = valText
				}
			case "status":
				if valText != "" {
					summary["status_text"] = valText
				}
				if statusClass := statusClassFromSelection(valSel); statusClass != "" {
					summary["status"] = statusClass
				}
			}
		}
	})

	if downloadHref, ok := section.Find("a.button.iconize.download").First().Attr("href"); ok {
		if abs, err := resolveLinkFromCanonical(canonicalStatsURL, downloadHref); err == nil {
			summary["download_url"] = abs
		}
	}

	stats := map[string]any{
		"status_page": canonicalStatsURL,
		"fetched_at":  time.Now().UTC().Format(time.RFC3339),
	}
	if len(summary) > 0 {
		stats["summary"] = summary
	}
	if len(counts) > 0 {
		stats["counts"] = counts
	}
	if len(submissionRefs) > 0 {
		stats["submission_refs"] = submissionRefs
	}

	return stats, nil
}

func parseIntValue(raw string) (int, bool) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, ",", ""))
	if raw == "" {
		return 0, false
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return v, true
}

func statusClassFromSelection(sel *goquery.Selection) string {
	if sel == nil {
		return ""
	}
	icon := sel.Find("i.status-icon").First()
	if icon.Length() == 0 {
		icon = sel.Find("i.icon").First()
	}
	if icon.Length() == 0 {
		return ""
	}
	classAttr, ok := icon.Attr("class")
	if !ok {
		return ""
	}
	for _, part := range strings.Fields(classAttr) {
		if part == "icon" || part == "status-icon" {
			continue
		}
		return strings.TrimSpace(part)
	}
	return ""
}

func mergeDetails(existing map[string]any, fresh map[string]any) map[string]any {
	if len(existing) == 0 && len(fresh) == 0 {
		return nil
	}
	merged := map[string]any{}
	for k, v := range existing {
		merged[k] = v
	}
	for k, v := range fresh {
		merged[k] = v
	}
	return merged
}

func statusPageLinkFromDetails(details map[string]any) string {
	if details == nil {
		return ""
	}
	raw, ok := details["links"]
	if !ok {
		return ""
	}
	switch links := raw.(type) {
	case map[string]string:
		return strings.TrimSpace(links["status_page"])
	case map[string]any:
		if v, ok := links["status_page"]; ok {
			if s, ok := v.(string); ok {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func withStatusPageLink(details map[string]any, statusPageURL string) map[string]any {
	statusPageURL = strings.TrimSpace(statusPageURL)
	if statusPageURL == "" {
		return details
	}
	if details == nil {
		details = map[string]any{}
	}

	raw, ok := details["links"]
	if !ok {
		details["links"] = map[string]string{"status_page": statusPageURL}
		return details
	}

	switch links := raw.(type) {
	case map[string]string:
		clone := map[string]string{}
		for k, v := range links {
			clone[k] = v
		}
		clone["status_page"] = statusPageURL
		details["links"] = clone
	case map[string]any:
		clone := map[string]any{}
		for k, v := range links {
			clone[k] = v
		}
		clone["status_page"] = statusPageURL
		details["links"] = clone
	default:
		details["links"] = map[string]string{"status_page": statusPageURL}
	}

	return details
}

func withStatsDetails(details map[string]any, stats map[string]any) map[string]any {
	if len(stats) == 0 {
		return details
	}
	if details == nil {
		details = map[string]any{}
	}
	details["stats"] = stats
	return details
}

func hasStatsDetails(details map[string]any) bool {
	if details == nil {
		return false
	}
	_, ok := details["stats"]
	return ok
}

func deriveStatsPageURL(canonicalURL string) string {
	parsed, err := url.Parse(canonicalURL)
	if err != nil {
		return ""
	}
	if !strings.HasPrefix(parsed.Path, "/course/") {
		return ""
	}
	suffix := strings.TrimPrefix(parsed.Path, "/course")
	if suffix == "" || suffix == "/" {
		return ""
	}
	statsURL := *parsed
	statsURL.Path = "/stats" + suffix
	statsURL.RawQuery = ""
	statsURL.Fragment = ""
	return statsURL.String()
}

func (s *Service) extractChildren(doc *goquery.Document, currentURL string) ([]pageChild, error) {
	children := make([]pageChild, 0)
	doc.Find("div.subsec.round.shade.ass-children ul.round li").Each(func(_ int, sel *goquery.Selection) {
		anchor := sel.Find("span.ass-link a")
		title := strings.TrimSpace(anchor.Text())
		href, ok := anchor.Attr("href")
		if !ok {
			return
		}
		absolute, err := s.resolveURL(currentURL, href)
		if err != nil {
			return
		}
		navAPI := ""
		if navPath, ok := anchor.Attr("data-navhref"); ok {
			navAPI, _ = s.resolveURL(currentURL, navPath)
		}
		children = append(children, pageChild{Title: title, URL: absolute, NavAPIURL: navAPI})
	})
	return children, nil
}

func (s *Service) extractCurrentNavAPIURL(doc *goquery.Document, canonicalURL string) string {
	parsed, err := url.Parse(canonicalURL)
	if err != nil {
		return ""
	}
	rel := parsed.Path
	if rel == "" {
		rel = "/"
	}
	selector := fmt.Sprintf("#nav a[href='%s']", rel)
	if anchor := doc.Find(selector).First(); anchor.Length() > 0 {
		if nav, ok := anchor.Attr("data-navhref"); ok {
			resolved, err := s.resolveURL(canonicalURL, nav)
			if err == nil {
				return resolved
			}
		}
	}

	if strings.HasPrefix(rel, "/course") {
		suffix := strings.TrimPrefix(rel, "/course")
		if suffix == "" || suffix == "/" {
			return strings.TrimRight(s.BaseURL, "/") + "/api/navigation/"
		}
		return strings.TrimRight(s.BaseURL, "/") + "/api/navigation" + suffix
	}
	return ""
}

func extractTitle(doc *goquery.Document) string {
	if title := strings.TrimSpace(doc.Find(".page-body section.assignment .sec-heading .sec-title a").Last().Text()); title != "" {
		return title
	}
	if titleTag := strings.TrimSpace(doc.Find("head title").First().Text()); titleTag != "" {
		const prefix = "Assignment:"
		const suffix = "- Themis"
		clean := strings.TrimSpace(strings.TrimPrefix(titleTag, prefix))
		clean = strings.TrimSpace(strings.TrimSuffix(clean, suffix))
		if clean != "" {
			return clean
		}
	}
	return "Untitled"
}

func extractDetails(doc *goquery.Document, canonicalURL string) map[string]any {
	out := map[string]any{}

	breadcrumb := make([]string, 0)
	doc.Find(".page-body section.assignment .sec-heading .sec-title a").Each(func(_ int, sel *goquery.Selection) {
		text := strings.TrimSpace(sel.Text())
		if text != "" {
			breadcrumb = append(breadcrumb, text)
		}
	})
	if len(breadcrumb) > 0 {
		out["breadcrumb"] = breadcrumb
	}

	config := map[string]any{}
	doc.Find(".ass-config .cfg-line").Each(func(_ int, sel *goquery.Selection) {
		keySel := sel.Find(".cfg-key").First().Clone()
		keySel.Find(".tip-text, [role=tooltip]").Remove()
		key := normalizeConfigKey(keySel.Text())
		if key == "" {
			return
		}
		valSel := sel.Find(".cfg-val").First()
		visibleVal := valSel.Clone()
		visibleVal.Find(".tip-text, [role=tooltip]").Remove()
		val := strings.TrimSpace(visibleVal.Text())
		if key == "start" || key == "deadline" || key == "end" {
			iso := strings.TrimSpace(valSel.Find(".tip-text").First().Text())
			if iso == "" {
				iso, _ = valSel.Find("[data-title]").First().Attr("data-title")
			}
			if iso == "" {
				iso, _ = valSel.Find("time[datetime]").First().Attr("datetime")
			}
			if iso != "" {
				config[key+"_iso"] = strings.TrimSpace(iso)
			}
			if val != "" {
				config[key+"_display"] = val
			}
			return
		}
		if val != "" {
			config[key] = val
		}
	})
	if len(config) > 0 {
		out["config"] = config
	}

	links := map[string]string{}
	if statusHref, ok := doc.Find("a.iconize.status").First().Attr("href"); ok {
		if abs, err := resolveLinkFromCanonical(canonicalURL, statusHref); err == nil {
			links["status_page"] = abs
		}
	}
	if len(links) > 0 {
		out["links"] = links
	}

	if desc := strings.TrimSpace(doc.Find("p.ass-description").First().Text()); desc != "" {
		out["description"] = desc
	}

	return out
}

func extractAssets(doc *goquery.Document, canonicalURL string) []state.AssetRef {
	assets := make([]state.AssetRef, 0)
	seen := map[string]bool{}

	doc.Find("a[href]").Each(func(_ int, sel *goquery.Selection) {
		href, ok := sel.Attr("href")
		if !ok {
			return
		}
		className, _ := sel.Attr("class")
		if strings.Contains(className, "status") {
			return
		}

		abs, err := resolveLinkFromCanonical(canonicalURL, href)
		if err != nil {
			return
		}
		if shouldIgnoreAssetURL(abs) {
			return
		}
		if seen[abs] {
			return
		}
		seen[abs] = true

		name := strings.TrimSpace(sel.Text())
		if name == "" {
			name = path.Base(strings.TrimSpace(href))
		}
		assets = append(assets, state.AssetRef{
			Kind: classifyAssetKind(abs),
			Name: name,
			URL:  abs,
		})
	})

	return assets
}

func resolveLinkFromCanonical(canonicalBase string, href string) (string, error) {
	base, err := url.Parse(canonicalBase)
	if err != nil {
		return "", err
	}
	rel, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return "", err
	}
	return base.ResolveReference(rel).String(), nil
}

func normalizeConfigKey(raw string) string {
	raw = strings.TrimSpace(strings.TrimSuffix(raw, ":"))
	raw = strings.ToLower(raw)
	if raw == "" {
		return ""
	}
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return ""
	}
	out := strings.Join(parts, "_")
	out = strings.Trim(out, "_:")
	return out
}

func shouldIgnoreAssetURL(raw string) bool {
	if state.IsSiteInfoURL(raw) {
		return true
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return true
	}
	cleanPath := strings.TrimSpace(parsed.Path)
	if strings.HasPrefix(cleanPath, "/course") {
		return true
	}
	if strings.HasPrefix(cleanPath, "/stats") {
		return true
	}
	if strings.HasPrefix(cleanPath, "/help") || strings.HasPrefix(cleanPath, "/user") || strings.HasPrefix(cleanPath, "/log") {
		return true
	}
	return false
}

func classifyAssetKind(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "asset"
	}
	cleanPath := strings.ToLower(parsed.Path)
	switch {
	case strings.Contains(cleanPath, "/file/"):
		return "file"
	case strings.HasSuffix(cleanPath, ".in") || strings.HasSuffix(cleanPath, ".out"):
		return "test"
	case strings.HasSuffix(cleanPath, ".zip") || strings.HasSuffix(cleanPath, ".tar") || strings.HasSuffix(cleanPath, ".tgz") || strings.HasSuffix(cleanPath, ".gz") || strings.HasSuffix(cleanPath, ".rar"):
		return "archive"
	case strings.HasSuffix(cleanPath, ".pdf") || strings.HasSuffix(cleanPath, ".md") || strings.HasSuffix(cleanPath, ".txt"):
		return "document"
	default:
		return "asset"
	}
}

func inferKindFromURL(canonicalURL string) string {
	parsed, err := url.Parse(canonicalURL)
	if err != nil {
		return "assignment"
	}
	p := strings.Trim(path.Clean(parsed.Path), "/")
	if p == "" || p == "course" {
		return "catalog"
	}
	if !strings.HasPrefix(p, "course/") {
		return "assignment"
	}
	tail := strings.TrimPrefix(p, "course/")
	parts := strings.Split(tail, "/")
	switch len(parts) {
	case 1:
		return "year"
	case 2:
		return "course"
	default:
		return "assignment"
	}
}

func computeDepthHint(canonicalURL string) int {
	parsed, err := url.Parse(canonicalURL)
	if err != nil {
		return 0
	}
	p := strings.Trim(path.Clean(parsed.Path), "/")
	if p == "" || p == "course" {
		return 0
	}
	if strings.HasPrefix(p, "course/") {
		tail := strings.TrimPrefix(p, "course/")
		if tail == "" {
			return 0
		}
		return len(strings.Split(tail, "/"))
	}
	return len(strings.Split(p, "/"))
}

func markNodeFetchError(st *state.State, canonicalURL string, msg string, now time.Time) string {
	nodeID := state.NodeIDFromCanonicalURL(canonicalURL)
	node, ok := st.Nodes[nodeID]
	if !ok {
		node = state.Node{
			ID:           nodeID,
			CanonicalURL: canonicalURL,
			Title:        canonicalURL,
			Kind:         inferKindFromURL(canonicalURL),
			Assets:       []state.AssetRef{},
			CreatedAt:    now,
		}
	}
	_ = state.ApplyFetchFailure(&node, now, msg)
	if node.ParentIDs == nil {
		node.ParentIDs = []string{}
	}
	if node.ChildIDs == nil {
		node.ChildIDs = []string{}
	}
	_, _, _ = state.UpsertNode(st, node, now)
	return nodeID
}

func edgeAdded(st *state.State, parentID string, childID string, now time.Time) bool {
	parent, ok := st.Nodes[parentID]
	if !ok {
		return false
	}
	if containsString(parent.ChildIDs, childID) {
		return false
	}
	diff, err := state.SetChildren(st, parentID, append(parent.ChildIDs, childID), now)
	if err != nil {
		return false
	}
	return len(diff.Added) > 0
}

func containsString(list []string, target string) bool {
	for _, v := range list {
		if v == target {
			return true
		}
	}
	return false
}
