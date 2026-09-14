package state

import "testing"

func TestIsSiteInfoURL(t *testing.T) {
	for _, tc := range []struct {
		url     string
		ignored bool
	}{
		{"https://themis.housing.rug.nl/about", true},
		{"https://themis.housing.rug.nl/about/?lang=en#top", true},
		{"/about", true},
		{"https://www.rug.nl/info/privacy", true},
		{"https://rug.nl/info/privacy/?lang=en", true},
		{"https://themis.housing.rug.nl/file/course/privacy-policy.pdf", false},
		{"https://themis.housing.rug.nl/file/course/about", false},
		{"https://themis.housing.rug.nl/download/about.pdf", false},
		{"https://example.org/info/privacy", false},
		{"://bad URL", false},
	} {
		if got := IsSiteInfoURL(tc.url); got != tc.ignored {
			t.Errorf("IsSiteInfoURL(%q) = %t, want %t", tc.url, got, tc.ignored)
		}
	}
}
