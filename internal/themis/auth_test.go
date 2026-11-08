package themis

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
)

func TestNextAuthAction_CredentialsForm(t *testing.T) {
	doc := mustDoc(t, `
<html><body>
  <form action="/sso/submit" method="post">
    <input type="hidden" name="state" value="abc" />
    <input type="text" name="Ecom_User_ID" value="" />
    <input type="password" name="Ecom_Password" value="" />
  </form>
</body></html>`)
	pageURL, _ := url.Parse("https://signon.rug.nl/start")

	next, stage, err := nextAuthAction(doc, pageURL, LoginRequest{
		Username: "alice",
		Password: "secret",
		TOTP:     "123456",
	})
	if err != nil {
		t.Fatalf("next action error: %v", err)
	}
	if stage != "credentials" {
		t.Fatalf("unexpected stage: %q", stage)
	}
	if next.method != http.MethodPost {
		t.Fatalf("unexpected method: %q", next.method)
	}
	if next.targetURL != "https://signon.rug.nl/sso/submit" {
		t.Fatalf("unexpected target: %q", next.targetURL)
	}
	if got := next.values.Get("Ecom_User_ID"); got != "alice" {
		t.Fatalf("unexpected username value: %q", got)
	}
	if got := next.values.Get("Ecom_Password"); got != "secret" {
		t.Fatalf("unexpected password value: %q", got)
	}
	if got := next.values.Get("state"); got != "abc" {
		t.Fatalf("unexpected hidden field: %q", got)
	}
}

func TestNextAuthAction_MFAForm(t *testing.T) {
	doc := mustDoc(t, `
<html><body>
  <form action="/mfa" method="post">
    <input type="hidden" name="flow" value="1" />
    <input type="text" name="nffc" value="" />
  </form>
</body></html>`)
	pageURL, _ := url.Parse("https://xfactor.rug.nl/challenge")

	next, stage, err := nextAuthAction(doc, pageURL, LoginRequest{
		Username:   "alice",
		Password:   "secret",
		TOTP:       "654321",
		TOTPSecret: testTOTPSecret,
	})
	if err != nil {
		t.Fatalf("next action error: %v", err)
	}
	if stage != "mfa" {
		t.Fatalf("unexpected stage: %q", stage)
	}
	if got := next.values.Get("nffc"); got != "654321" {
		t.Fatalf("unexpected mfa value: %q", got)
	}
}

func TestBuildFormRequest_RelativeAction(t *testing.T) {
	doc := mustDoc(t, `
<form action="../submit" method="post">
  <input type="hidden" name="foo" value="bar" />
</form>`)
	form := doc.Find("form").First()
	pageURL, _ := url.Parse("https://connect.surfconext.nl/auth/step")

	targetURL, values, err := buildFormRequest(form, pageURL)
	if err != nil {
		t.Fatalf("build form request error: %v", err)
	}
	if targetURL.String() != "https://connect.surfconext.nl/submit" {
		t.Fatalf("unexpected target URL: %q", targetURL.String())
	}
	if values.Get("foo") != "bar" {
		t.Fatalf("unexpected values: %#v", values)
	}
}

func TestDeriveFetchSite(t *testing.T) {
	if got := deriveFetchSite("", "https://idp.example.edu/login"); got != "none" {
		t.Fatalf("expected none, got %q", got)
	}
	if got := deriveFetchSite("https://idp.example.edu/start", "https://idp.example.edu/login"); got != "same-origin" {
		t.Fatalf("expected same-origin, got %q", got)
	}
	if got := deriveFetchSite("https://themis.housing.rug.nl/log/in/oidc", "https://idp.example.edu/login"); got != "cross-site" {
		t.Fatalf("expected cross-site, got %q", got)
	}
}

func TestNewAuthRequest_RespectsClientTimeout(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(120 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer slow.Close()

	client, err := initializeHTTPClient()
	if err != nil {
		t.Fatalf("initialize client: %v", err)
	}
	client.Timeout = 50 * time.Millisecond

	_, err = newAuthRequest(client, http.MethodGet, slow.URL, "", nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(strings.ToLower(err.Error()), "timeout") {
		t.Fatalf("expected timeout-related error, got: %v", err)
	}
}

func TestDetectFallbackRedirect_MetaRefresh(t *testing.T) {
	next, found, err := detectFallbackRedirect(`<meta http-equiv="refresh" content="0; url=/next/step">`, "https://connect.surfconext.nl/start")
	if err != nil {
		t.Fatalf("detect fallback redirect: %v", err)
	}
	if !found {
		t.Fatal("expected fallback redirect to be found")
	}
	if next != "https://connect.surfconext.nl/next/step" {
		t.Fatalf("unexpected fallback url: %q", next)
	}
}

func TestDetectFallbackRedirect_WindowLocation(t *testing.T) {
	next, found, err := detectFallbackRedirect(`<script>window.location="https://signon.rug.nl/nidp/saml2/sso";</script>`, "https://connect.surfconext.nl/start")
	if err != nil {
		t.Fatalf("detect fallback redirect: %v", err)
	}
	if !found {
		t.Fatal("expected fallback redirect to be found")
	}
	if next != "https://signon.rug.nl/nidp/saml2/sso" {
		t.Fatalf("unexpected fallback url: %q", next)
	}
}

func TestResolveRedirectTarget(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusFound,
		Header:     http.Header{"Location": []string{"/callback"}},
	}
	target, err := resolveRedirectTarget(resp, "https://themis.housing.rug.nl/log/in/oidc")
	if err != nil {
		t.Fatalf("resolve redirect target: %v", err)
	}
	if target != "https://themis.housing.rug.nl/callback" {
		t.Fatalf("unexpected redirect target: %q", target)
	}
}

func TestLooksAuthenticated(t *testing.T) {
	if !looksAuthenticated(`<html><body><a href="/user">Alice Example</a></body></html>`) {
		t.Fatal("expected /user anchor to be treated as authenticated")
	}
	if !looksAuthenticated(`<html><body>You are logged in as s1234567</body></html>`) {
		t.Fatal("expected logged-in-as marker to be treated as authenticated")
	}
	if looksAuthenticated(`<html><body>Please log in</body></html>`) {
		t.Fatal("did not expect anonymous page to be treated as authenticated")
	}
}

func TestIsThemisURL(t *testing.T) {
	if !isThemisURL("https://themis.housing.rug.nl/course/", "https://themis.housing.rug.nl") {
		t.Fatal("expected themis url to match base url")
	}
	if isThemisURL("https://signon.rug.nl/nidp/saml2/sso", "https://themis.housing.rug.nl") {
		t.Fatal("did not expect non-themis url to match base url")
	}
}

func TestResolveRelativeURL(t *testing.T) {
	got, err := resolveRelativeURL("https://engine.surfconext.nl/authentication/idp/single-sign-on", "/next")
	if err != nil {
		t.Fatalf("resolveRelativeURL: %v", err)
	}
	if got != "https://engine.surfconext.nl/next" {
		t.Fatalf("unexpected resolved url: %q", got)
	}
}

func mustDoc(t *testing.T, html string) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse doc: %v", err)
	}
	return doc
}
