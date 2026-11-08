package themis

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const (
	loginStartPath = "/log/in/oidc"
	maxAuthSteps   = 32
	authTimeout    = 45 * time.Second
)

var ErrInvalidCredentials = errors.New("invalid credentials")

var (
	metaRefreshURLPattern  = regexp.MustCompile(`(?i)<meta[^>]+http-equiv=["']refresh["'][^>]+content=["']\d+;\s*url=([^"']+)["']`)
	metaRefreshURLPattern2 = regexp.MustCompile(`(?i)content=["']\d+;\s*url=([^"']+)["'][^>]+http-equiv=["']refresh["']`)
	jsRedirectURLPattern   = regexp.MustCompile(`(?i)window\.location(?:\.href)?\s*=\s*["']([^"']+)["']`)
	samlRedirectURLPattern = regexp.MustCompile(`(?i)href=["'](https?:[^"']*SAMLRequest=[^"']+)["']`)
)

type LoginRequest struct {
	Username     string
	Password     string
	TOTP         string
	TOTPSecret   string
	SaveUsername bool
	SavePassword bool
}

type LoginResult struct {
	User UserData
}

// PerformSSOLogin executes the observed multi-step SSO flow and persists session state.
func PerformSSOLogin(baseURL string, authConfig AuthConfig, req LoginRequest) (LoginResult, error) {
	normalizedBaseURL, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return LoginResult{}, err
	}
	if strings.TrimSpace(req.Username) == "" || strings.TrimSpace(req.Password) == "" {
		return LoginResult{}, fmt.Errorf("%w: username and password are required", ErrMissingCredentials)
	}
	if strings.TrimSpace(req.TOTP) == "" && req.TOTPSecret == "" {
		return LoginResult{}, fmt.Errorf("%w: totp is required", ErrMissingCredentials)
	}

	if req.TOTPSecret != "" {
		req.TOTPSecret, err = NormalizeTOTPSecret(req.TOTPSecret)
		if err != nil {
			return LoginResult{}, err
		}
	}
	client, err := initializeHTTPClient()
	if err != nil {
		return LoginResult{}, err
	}
	bindAuthContext(client, authConfig)
	client.Timeout = authTimeout
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	if err := runSSOSequence(client, normalizedBaseURL, req); err != nil {
		return LoginResult{}, err
	}

	session := &Session{BaseURL: normalizedBaseURL, Client: client}
	userData, err := session.ValidateAuthentication()
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "status 401") || strings.Contains(strings.ToLower(err.Error()), "status 403") {
			return LoginResult{}, fmt.Errorf("%w: %v", ErrSessionExpired, err)
		}
		return LoginResult{}, err
	}

	sessionFilePath, err := resolveSessionFilePath(authConfig)
	if err != nil {
		return LoginResult{}, err
	}
	authSettings := SessionAuthSettings{
		SaveUsername: req.SaveUsername || req.SavePassword,
		SavePassword: req.SavePassword,
	}
	if authSettings.SaveUsername {
		authSettings.Username = strings.TrimSpace(req.Username)
	}
	if authSettings.SavePassword {
		authSettings.Password = req.Password
	}
	if req.TOTPSecret != "" && authSettings.SaveUsername {
		authSettings.TOTPSecret = req.TOTPSecret
		authSettings.TOTPBaseURL = normalizedBaseURL
	}
	user := &SessionUser{
		FullName:      userData.FullName,
		Email:         userData.Email,
		FirstLoggedIn: userData.FirstLoggedIn,
		LastLoggedIn:  userData.LastLoggedIn,
	}
	if err := session.SaveState(sessionFilePath, authSettings, user, time.Now().UTC()); err != nil {
		return LoginResult{}, err
	}

	return LoginResult{User: userData}, nil
}

func runSSOSequence(client *http.Client, baseURL string, req LoginRequest) error {
	startURL := strings.TrimRight(baseURL, "/") + loginStartPath
	resp, err := newAuthRequest(client, http.MethodGet, startURL, baseURL, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	lastStage := ""
	currentURL := responseURL(resp, startURL)
	lastNoProgressURL := ""
	for i := 0; i < maxAuthSteps; i++ {
		authDebugf("step=%d url=%s status=%d stage=%s", i, currentURL, resp.StatusCode, lastStage)
		if isRedirectStatus(resp.StatusCode) {
			nextURL, err := resolveRedirectTarget(resp, currentURL)
			if err != nil {
				return err
			}
			authDebugf("redirect -> %s", nextURL)
			resp.Body.Close()
			resp, err = newAuthRequest(client, http.MethodGet, nextURL, currentURL, nil)
			if err != nil {
				return err
			}
			currentURL = responseURL(resp, nextURL)
			lastStage = "redirect"
			continue
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("read auth page: %w", err)
		}
		bodyStr := string(body)
		if isThemisURL(currentURL, baseURL) && looksAuthenticated(bodyStr) {
			authDebugf("detected authenticated themis page during auth sequence")
			return nil
		}

		fallbackURL, fallbackFound, err := detectFallbackRedirect(bodyStr, currentURL)
		if err != nil {
			return err
		}
		if fallbackFound {
			authDebugf("fallback redirect -> %s", fallbackURL)
			resp.Body.Close()
			resp, err = newAuthRequest(client, http.MethodGet, fallbackURL, currentURL, nil)
			if err != nil {
				return err
			}
			currentURL = responseURL(resp, fallbackURL)
			lastStage = "fallback"
			continue
		}

		doc, err := goquery.NewDocumentFromReader(strings.NewReader(bodyStr))
		if err != nil {
			return fmt.Errorf("parse auth page: %w", err)
		}
		pageURL, err := url.Parse(currentURL)
		if err != nil {
			return fmt.Errorf("parse auth page url %q: %w", currentURL, err)
		}
		next, stage, stageErr := nextAuthAction(doc, pageURL, req)
		if stageErr == nil && next == nil {
			if isThemisURL(currentURL, baseURL) {
				authDebugf("stopping at themis url without further auth actions: %s", currentURL)
				return nil
			}
			if currentURL != lastNoProgressURL {
				retryResp, retryErr := newAuthRequest(client, http.MethodGet, currentURL, currentURL, nil)
				if retryErr == nil {
					resp.Body.Close()
					resp = retryResp
					currentURL = responseURL(resp, currentURL)
					lastNoProgressURL = currentURL
					lastStage = "rotation-retry"
					continue
				}
			}
			authDebugf(
				"stall markers: has_form=%t has_meta_refresh=%t has_window_location=%t has_saml_redirect=%t has_credentials=%t has_mfa=%t has_no_session=%t",
				strings.Contains(strings.ToLower(bodyStr), "<form"),
				metaRefreshURLPattern.FindStringSubmatch(bodyStr) != nil || metaRefreshURLPattern2.FindStringSubmatch(bodyStr) != nil,
				jsRedirectURLPattern.FindStringSubmatch(bodyStr) != nil,
				samlRedirectURLPattern.FindStringSubmatch(bodyStr) != nil,
				strings.Contains(bodyStr, "Ecom_User_ID") || strings.Contains(bodyStr, "Ecom_Password"),
				strings.Contains(bodyStr, `name="nffc"`) || strings.Contains(bodyStr, "name='nffc'"),
				strings.Contains(strings.ToLower(bodyStr), "no session"),
			)
			return fmt.Errorf("%w: login sequence stalled at %s (stage=%s)", ErrNotAuthenticated, currentURL, lastStage)
		}
		if stageErr != nil {
			return stageErr
		}

		lastNoProgressURL = ""
		lastStage = stage
		authDebugf("form stage=%s -> %s", stage, next.targetURL)
		resp, err = newAuthRequest(client, next.method, next.targetURL, next.referer, next.values)
		if err != nil {
			return err
		}
		currentURL = responseURL(resp, next.targetURL)
	}

	if lastStage == "mfa" {
		return fmt.Errorf("%w: mfa challenge did not complete", ErrInvalidMFA)
	}
	if lastStage == "credentials" {
		return fmt.Errorf("%w: credential challenge did not complete", ErrInvalidCredentials)
	}
	return fmt.Errorf("%w: login sequence did not complete (last_url=%s stage=%s)", ErrNotAuthenticated, currentURL, lastStage)
}

type authAction struct {
	method    string
	targetURL string
	referer   string
	values    url.Values
}

func nextAuthAction(doc *goquery.Document, pageURL *url.URL, req LoginRequest) (*authAction, string, error) {
	form := doc.Find("form").First()
	if form.Length() == 0 {
		return nil, "", nil
	}

	actionURL, values, err := buildFormRequest(form, pageURL)
	if err != nil {
		return nil, "", err
	}

	if hasInput(form, "Ecom_User_ID") && hasInput(form, "Ecom_Password") {
		values.Set("Ecom_User_ID", strings.TrimSpace(req.Username))
		values.Set("Ecom_Password", req.Password)
		values.Set("option", "credential")
		return &authAction{
			method:    http.MethodPost,
			targetURL: actionURL.String(),
			referer:   pageURL.String(),
			values:    values,
		}, "credentials", nil
	}

	if hasInput(form, "nffc") {
		code := strings.TrimSpace(req.TOTP)
		if code == "" {
			code, err = TOTPCode(req.TOTPSecret, time.Now())
			if err != nil {
				return nil, "mfa", err
			}
		}
		values.Set("nffc", code)
		values.Set("option", "credential")
		return &authAction{
			method:    http.MethodPost,
			targetURL: actionURL.String(),
			referer:   pageURL.String(),
			values:    values,
		}, "mfa", nil
	}

	method := strings.ToUpper(strings.TrimSpace(form.AttrOr("method", "GET")))
	if method != http.MethodGet {
		method = http.MethodPost
	}
	return &authAction{
		method:    method,
		targetURL: actionURL.String(),
		referer:   pageURL.String(),
		values:    values,
	}, "relay", nil
}

func buildFormRequest(form *goquery.Selection, pageURL *url.URL) (*url.URL, url.Values, error) {
	action := strings.TrimSpace(form.AttrOr("action", ""))
	targetURL := pageURL
	if action != "" {
		parsedAction, err := pageURL.Parse(action)
		if err != nil {
			return nil, nil, fmt.Errorf("parse form action %q: %w", action, err)
		}
		targetURL = parsedAction
	}
	values := url.Values{}
	form.Find("input").Each(func(_ int, input *goquery.Selection) {
		name := strings.TrimSpace(input.AttrOr("name", ""))
		if name == "" {
			return
		}
		values.Set(name, input.AttrOr("value", ""))
	})
	return targetURL, values, nil
}

func hasInput(form *goquery.Selection, name string) bool {
	return form.Find(fmt.Sprintf(`input[name="%s"]`, name)).Length() > 0
}

func newAuthRequest(client *http.Client, method string, rawURL string, referer string, values url.Values) (*http.Response, error) {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		method = http.MethodGet
	}

	var req *http.Request
	var err error
	switch method {
	case http.MethodPost:
		req, err = http.NewRequest(http.MethodPost, rawURL, strings.NewReader(values.Encode()))
		if err != nil {
			return nil, fmt.Errorf("build auth post request: %w", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if strings.TrimSpace(referer) != "" {
			if parsedReferer, parseErr := url.Parse(referer); parseErr == nil && parsedReferer.Scheme != "" && parsedReferer.Host != "" {
				req.Header.Set("Origin", parsedReferer.Scheme+"://"+parsedReferer.Host)
			}
		} else if parsedTarget, parseErr := url.Parse(rawURL); parseErr == nil && parsedTarget.Scheme != "" && parsedTarget.Host != "" {
			req.Header.Set("Origin", parsedTarget.Scheme+"://"+parsedTarget.Host)
		}
	default:
		targetURL, parseErr := url.Parse(rawURL)
		if parseErr != nil {
			return nil, fmt.Errorf("parse auth get url %q: %w", rawURL, parseErr)
		}
		query := targetURL.Query()
		for key, list := range values {
			query.Del(key)
			for _, value := range list {
				query.Add(key, value)
			}
		}
		targetURL.RawQuery = query.Encode()
		req, err = http.NewRequest(http.MethodGet, targetURL.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("build auth get request: %w", err)
		}
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:141.0) Gecko/20100101 Firefox/141.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Sec-Fetch-Site", deriveFetchSite(referer, rawURL))
	if strings.TrimSpace(referer) != "" {
		req.Header.Set("Referer", referer)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute auth request: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("%w: auth endpoint returned status %d", ErrNotAuthenticated, resp.StatusCode)
		}
		return nil, fmt.Errorf("auth endpoint returned status %d", resp.StatusCode)
	}
	return resp, nil
}

func responseURL(resp *http.Response, fallback string) string {
	if resp != nil && resp.Request != nil && resp.Request.URL != nil {
		return resp.Request.URL.String()
	}
	return fallback
}

func isRedirectStatus(status int) bool {
	return status >= 300 && status < 400
}

func resolveRedirectTarget(resp *http.Response, currentURL string) (string, error) {
	location := strings.TrimSpace(resp.Header.Get("Location"))
	if location == "" {
		return "", fmt.Errorf("redirect response missing location header")
	}
	base, err := url.Parse(currentURL)
	if err != nil {
		return "", fmt.Errorf("parse current url %q: %w", currentURL, err)
	}
	target, err := base.Parse(location)
	if err != nil {
		return "", fmt.Errorf("parse redirect location %q: %w", location, err)
	}
	return target.String(), nil
}

func detectFallbackRedirect(body string, currentURL string) (string, bool, error) {
	for _, pattern := range []*regexp.Regexp{metaRefreshURLPattern, metaRefreshURLPattern2, jsRedirectURLPattern, samlRedirectURLPattern} {
		match := pattern.FindStringSubmatch(body)
		if len(match) < 2 || strings.TrimSpace(match[1]) == "" {
			continue
		}
		resolved, err := resolveRelativeURL(currentURL, match[1])
		if err != nil {
			return "", false, err
		}
		return resolved, true, nil
	}
	return "", false, nil
}

func looksAuthenticated(body string) bool {
	body = strings.TrimSpace(body)
	if body == "" {
		return false
	}
	if strings.Contains(strings.ToLower(body), `href="/user"`) || strings.Contains(strings.ToLower(body), "href='/user'") {
		return true
	}
	return regexp.MustCompile(`(?i)logged\s+in\s+as\s+[sp][0-9]{7}`).FindStringIndex(body) != nil
}

func isThemisURL(rawURL string, baseURL string) bool {
	return strings.HasPrefix(strings.TrimSpace(rawURL), strings.TrimRight(baseURL, "/"))
}

func authDebugf(format string, args ...any) {
	if os.Getenv("THEMIS_AUTH_DEBUG") == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "[themis auth] "+format+"\n", args...)
}

func bodyPrefix(body string, limit int) string {
	body = strings.Join(strings.Fields(body), " ")
	if len(body) <= limit {
		return body
	}
	return body[:limit]
}

func resolveRelativeURL(baseURL string, target string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base url %q: %w", baseURL, err)
	}
	resolved, err := base.Parse(strings.TrimSpace(target))
	if err != nil {
		return "", fmt.Errorf("parse target url %q: %w", target, err)
	}
	return resolved.String(), nil
}

func deriveFetchSite(referer string, rawTargetURL string) string {
	referer = strings.TrimSpace(referer)
	if referer == "" {
		return "none"
	}
	refURL, err := url.Parse(referer)
	if err != nil || refURL.Host == "" {
		return "none"
	}
	targetURL, err := url.Parse(strings.TrimSpace(rawTargetURL))
	if err != nil || targetURL.Host == "" {
		return "none"
	}
	if strings.EqualFold(refURL.Host, targetURL.Host) {
		return "same-origin"
	}
	return "cross-site"
}
