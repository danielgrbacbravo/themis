package state

import (
	"net/url"
	"strings"
)

// IsSiteInfoURL identifies site-wide informational pages, not assignment files.
// Apply it to cached assets as well as newly discovered links so old caches do
// not put these pages back into download selections.
func IsSiteInfoURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	page := strings.TrimRight(strings.ToLower(parsed.Path), "/")
	if page == "/about" {
		return true
	}
	host := strings.ToLower(parsed.Hostname())
	return (host == "www.rug.nl" || host == "rug.nl") && page == "/info/privacy"
}
