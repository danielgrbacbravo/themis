package themis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const userDataRoute = "/user"
const courseRoute = "/course/"
const sessionFileSchemaVersion = 1

var (
	ErrNotAuthenticated   = errors.New("not authenticated")
	ErrSessionExpired     = errors.New("session expired")
	ErrMissingCredentials = errors.New("missing credentials")
	ErrInvalidMFA         = errors.New("invalid mfa")
)

type Session struct {
	BaseURL string
	Client  *http.Client
}

type AuthConfig struct {
	SessionFile string
	Context     context.Context
}

type UserData struct {
	FullName      string
	Email         string
	FirstLoggedIn string
	LastLoggedIn  string
}

func NewSession(baseURL string, cookiePath string) (*Session, error) {
	return NewSessionWithAuthConfig(baseURL, AuthConfig{SessionFile: cookiePath})
}

func NewSessionWithAuthConfig(baseURL string, authConfig AuthConfig) (*Session, error) {
	session, err := LoadSessionWithAuthConfig(baseURL, authConfig)
	if err == nil {
		_, err = session.ValidateAuthentication()
	}
	if err == nil {
		return session, nil
	}
	// Only an authentication failure permits renewal. Network/server errors must
	// remain errors, rather than triggering another credential submission.
	if !errors.Is(err, ErrSessionExpired) && !errors.Is(err, ErrNotAuthenticated) {
		return nil, err
	}
	service, serviceErr := NewAuthService(baseURL, authConfig)
	if serviceErr != nil {
		return nil, serviceErr
	}
	auth, loadErr := LoadAuthSettings(authConfig.SessionFile)
	if loadErr != nil {
		return nil, loadErr
	}
	if auth.TOTPSecret == "" || !auth.SavePassword || !auth.SaveUsername || auth.Password == "" || auth.Username == "" {
		return nil, err
	}
	request, resolveErr := service.ResolveLoginRequest("", "", "")
	if resolveErr != nil {
		return nil, resolveErr
	}
	if _, loginErr := service.Login(request); loginErr != nil {
		return nil, loginErr
	}
	session, err = LoadSessionWithAuthConfig(baseURL, authConfig)
	if err != nil {
		return nil, err
	}
	if _, err := session.ValidateAuthentication(); err != nil {
		return nil, err
	}
	return session, nil
}

// LoadSessionWithAuthConfig restores saved cookies without making network requests.
// Callers must handle authentication failures on their context-bound requests.
func LoadSessionWithAuthConfig(baseURL string, authConfig AuthConfig) (*Session, error) {
	normalizedBaseURL, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return nil, err
	}

	client, err := initializeHTTPClient()
	if err != nil {
		return nil, err
	}

	bindAuthContext(client, authConfig)

	parsedBaseURL, err := url.Parse(normalizedBaseURL)
	if err != nil {
		return nil, err
	}

	sessionFilePath, err := resolveSessionFilePath(authConfig)
	if err != nil {
		return nil, err
	}

	sessionState, err := loadSessionState(sessionFilePath)
	if err != nil {
		return nil, classifyLoadSessionError(err)
	}

	if err := restoreCookieJar(client.Jar, parsedBaseURL, sessionState); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotAuthenticated, err)
	}

	session := &Session{
		BaseURL: normalizedBaseURL,
		Client:  client,
	}
	return session, nil
}

func (s *Session) GetUserData() (UserData, error) {
	doc, statusCode, err := s.getDataFromUserPage()
	if err != nil {
		return UserData{}, err
	}
	if statusCode != http.StatusOK {
		return UserData{}, fmt.Errorf("user endpoint returned status %d", statusCode)
	}

	userData := extractUserDataFields(doc)

	return UserData{
		FullName:      firstNonEmpty(userData["full name"], userData["name"]),
		Email:         userData["email"],
		FirstLoggedIn: trimDate(userData["first login"]),
		LastLoggedIn:  trimDate(userData["last login"]),
	}, nil
}

func (s *Session) CheckBaseURLAccess() error {
	resp, err := s.Client.Get(s.BaseURL)
	if err != nil {
		return fmt.Errorf("error accessing base URL: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("base URL returned status %d", resp.StatusCode)
	}
	return nil
}

func (s *Session) ValidateAuthentication() (UserData, error) {
	courseIdentity, err := s.getAuthenticatedIdentityFromCoursePage()
	if err != nil {
		return UserData{}, err
	}

	userData, err := s.GetUserData()
	if err == nil {
		if strings.TrimSpace(userData.FullName) == "" {
			userData.FullName = courseIdentity.FullName
		}
		if strings.TrimSpace(userData.Email) == "" {
			userData.Email = courseIdentity.Email
		}
		if strings.TrimSpace(userData.FullName) != "" {
			return userData, nil
		}
	}

	if strings.TrimSpace(courseIdentity.FullName) == "" {
		return UserData{}, fmt.Errorf("%w: authentication check failed: no authenticated user anchor found on %s", ErrSessionExpired, courseRoute)
	}
	return courseIdentity, nil
}

func (s *Session) getDataFromUserPage() (*goquery.Document, int, error) {
	resp, err := s.Client.Get(s.BaseURL + userDataRoute)
	if err != nil {
		return nil, 0, fmt.Errorf("error fetching user data page: %w", err)
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("error parsing user data page: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, resp.StatusCode, fmt.Errorf("%w: user endpoint returned status %d", ErrSessionExpired, resp.StatusCode)
	}
	return doc, resp.StatusCode, nil
}

func (s *Session) getAuthenticatedIdentityFromCoursePage() (UserData, error) {
	resp, err := s.Client.Get(s.BaseURL + courseRoute)
	if err != nil {
		return UserData{}, fmt.Errorf("error fetching course page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return UserData{}, fmt.Errorf("%w: course endpoint returned status %d", ErrSessionExpired, resp.StatusCode)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return UserData{}, fmt.Errorf("course endpoint returned status %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return UserData{}, fmt.Errorf("error parsing course page: %w", err)
	}

	anchorText := findUserAnchorText(doc)
	if strings.TrimSpace(anchorText) == "" {
		return UserData{}, fmt.Errorf("%w: no user anchor found on %s", ErrSessionExpired, courseRoute)
	}

	return UserData{FullName: anchorText}, nil
}

func findUserAnchorText(doc *goquery.Document) string {
	text := ""
	doc.Find("a").EachWithBreak(func(_ int, sel *goquery.Selection) bool {
		href := strings.TrimSpace(sel.AttrOr("href", ""))
		if !isUserHref(href) {
			return true
		}
		value := strings.TrimSpace(sel.Text())
		if value == "" {
			return true
		}
		text = strings.Join(strings.Fields(value), " ")
		return false
	})
	return text
}

func isUserHref(href string) bool {
	if href == "" {
		return false
	}
	u, err := url.Parse(href)
	if err != nil {
		return false
	}
	path := strings.TrimRight(strings.TrimSpace(u.Path), "/")
	return path == "/user"
}

func extractUserDataFields(doc *goquery.Document) map[string]string {
	fields := map[string]string{}
	doc.Find("div.cfg-line").Each(func(_ int, sel *goquery.Selection) {
		key := normalizeProfileKey(sel.Find("span.cfg-key").Text())
		value := strings.TrimSpace(sel.Find("span.cfg-val").Text())
		if key == "" || value == "" {
			return
		}
		fields[key] = value
	})
	return fields
}

func normalizeProfileKey(key string) string {
	key = strings.TrimSpace(strings.TrimSuffix(key, ":"))
	key = strings.ToLower(key)
	return strings.Join(strings.Fields(key), " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func initializeHTTPClient() (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}

	return &http.Client{Jar: jar}, nil
}

func resolveSessionFilePath(authConfig AuthConfig) (string, error) {
	if strings.TrimSpace(authConfig.SessionFile) != "" {
		return strings.TrimSpace(authConfig.SessionFile), nil
	}

	return DefaultSessionFilePath()
}

func DefaultSessionFilePath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}

	return filepath.Join(homeDir, ".config", "themis", "session.json"), nil
}

func loadSessionState(path string) (SessionState, error) {
	rawState, err := os.ReadFile(path)
	if err != nil {
		return SessionState{}, fmt.Errorf("read session file %q: %w", path, err)
	}

	rawTrimmed := strings.TrimSpace(string(rawState))
	if rawTrimmed == "" {
		return SessionState{}, fmt.Errorf("session file is empty: %s", path)
	}

	var sessionState SessionState
	if err := json.Unmarshal([]byte(rawTrimmed), &sessionState); err != nil {
		legacyCookies, parseErr := parseCookieString(rawTrimmed, fmt.Sprintf("legacy session file %q", path))
		if parseErr != nil {
			return SessionState{}, fmt.Errorf("decode session state from %q: %w", path, err)
		}

		sessionState = SessionState{
			SchemaVersion: sessionFileSchemaVersion,
			Cookies: []SessionCookieScope{
				{
					URL:     "",
					Cookies: fromHTTPCookies(legacyCookies),
				},
			},
		}
	}

	if sessionState.SchemaVersion == 0 {
		sessionState.SchemaVersion = sessionFileSchemaVersion
	}
	if sessionState.Cookies == nil {
		sessionState.Cookies = []SessionCookieScope{}
	}
	return sessionState, nil
}

func LoadSessionState(path string) (SessionState, error) {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultSessionFilePath()
		if err != nil {
			return SessionState{}, err
		}
	}
	return loadSessionState(path)
}

func SaveSessionState(path string, state SessionState) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("session file path is required")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create session directory: %w", err)
	}

	if state.SchemaVersion == 0 {
		state.SchemaVersion = sessionFileSchemaVersion
	}
	if state.Cookies == nil {
		state.Cookies = []SessionCookieScope{}
	}

	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session state: %w", err)
	}
	payload = append(payload, '\n')
	// A private temporary file also replaces older, overly permissive files.
	temp, err := os.CreateTemp(dir, ".themis-session-*")
	if err != nil {
		return fmt.Errorf("create private session file: %w", err)
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(payload); err != nil {
		temp.Close()
		return fmt.Errorf("write session file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close session file: %w", err)
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return fmt.Errorf("save session file: %w", err)
	}
	return nil
}

func (s *Session) SaveState(path string, auth SessionAuthSettings, user *SessionUser, lastAuthenticatedAt time.Time) error {
	baseURL, err := url.Parse(s.BaseURL)
	if err != nil {
		return fmt.Errorf("parse base URL %q: %w", s.BaseURL, err)
	}

	state := SessionState{
		SchemaVersion:       sessionFileSchemaVersion,
		BaseURL:             s.BaseURL,
		LastAuthenticatedAt: &lastAuthenticatedAt,
		User:                user,
		Auth:                auth,
		Cookies: []SessionCookieScope{
			{
				URL:     s.BaseURL,
				Cookies: fromHTTPCookies(s.Client.Jar.Cookies(baseURL)),
			},
		},
	}

	return SaveSessionState(path, state)
}

func restoreCookieJar(jar http.CookieJar, baseURL *url.URL, state SessionState) error {
	if len(state.Cookies) == 0 {
		return fmt.Errorf("session file has no cookies")
	}

	restored := 0
	for _, scopedCookies := range state.Cookies {
		targetURL := baseURL
		if strings.TrimSpace(scopedCookies.URL) != "" {
			parsed, err := url.Parse(strings.TrimSpace(scopedCookies.URL))
			if err != nil {
				return fmt.Errorf("invalid cookie scope URL %q: %w", scopedCookies.URL, err)
			}
			if parsed.Scheme == "" || parsed.Host == "" {
				return fmt.Errorf("invalid cookie scope URL %q", scopedCookies.URL)
			}
			targetURL = parsed
		}

		cookies := toHTTPCookies(scopedCookies.Cookies)
		if len(cookies) == 0 {
			continue
		}
		jar.SetCookies(targetURL, cookies)
		restored += len(cookies)
	}

	if restored == 0 {
		return fmt.Errorf("session file has no valid cookies")
	}
	return nil
}

type SessionState struct {
	SchemaVersion       int                  `json:"schema_version"`
	BaseURL             string               `json:"base_url,omitempty"`
	LastAuthenticatedAt *time.Time           `json:"last_authenticated_at,omitempty"`
	User                *SessionUser         `json:"user,omitempty"`
	Auth                SessionAuthSettings  `json:"auth"`
	Cookies             []SessionCookieScope `json:"cookies"`
}

type SessionUser struct {
	FullName      string `json:"full_name,omitempty"`
	Email         string `json:"email,omitempty"`
	FirstLoggedIn string `json:"first_logged_in,omitempty"`
	LastLoggedIn  string `json:"last_logged_in,omitempty"`
}

type SessionAuthSettings struct {
	TOTPSecret   string `json:"totp_secret,omitempty"`
	TOTPBaseURL  string `json:"totp_base_url,omitempty"`
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
	SaveUsername bool   `json:"save_username"`
	SavePassword bool   `json:"save_password"`
}

type SessionCookieScope struct {
	URL     string          `json:"url,omitempty"`
	Cookies []SessionCookie `json:"cookies"`
}

type SessionCookie struct {
	Name     string    `json:"name"`
	Value    string    `json:"value"`
	Path     string    `json:"path,omitempty"`
	Domain   string    `json:"domain,omitempty"`
	Expires  time.Time `json:"expires,omitempty"`
	MaxAge   int       `json:"max_age,omitempty"`
	Secure   bool      `json:"secure,omitempty"`
	HTTPOnly bool      `json:"http_only,omitempty"`
	SameSite int       `json:"same_site,omitempty"`
}

func parseCookieString(cookieString string, source string) ([]*http.Cookie, error) {
	cookiePairs := strings.Split(cookieString, ";")
	cookies := make([]*http.Cookie, 0, len(cookiePairs))

	for _, pair := range cookiePairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}

		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			return nil, fmt.Errorf("invalid cookie pair in %s: %q", source, pair)
		}

		cookies = append(cookies, &http.Cookie{
			Name:  strings.TrimSpace(parts[0]),
			Value: strings.TrimSpace(parts[1]),
		})
	}

	if len(cookies) == 0 {
		return nil, fmt.Errorf("no valid cookies found in %s", source)
	}

	return cookies, nil
}

func toHTTPCookies(cookies []SessionCookie) []*http.Cookie {
	result := make([]*http.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		name := strings.TrimSpace(cookie.Name)
		if name == "" {
			continue
		}
		result = append(result, &http.Cookie{
			Name:     name,
			Value:    cookie.Value,
			Path:     cookie.Path,
			Domain:   cookie.Domain,
			Expires:  cookie.Expires,
			MaxAge:   cookie.MaxAge,
			Secure:   cookie.Secure,
			HttpOnly: cookie.HTTPOnly,
			SameSite: http.SameSite(cookie.SameSite),
		})
	}
	return result
}

func fromHTTPCookies(cookies []*http.Cookie) []SessionCookie {
	result := make([]SessionCookie, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil || strings.TrimSpace(cookie.Name) == "" {
			continue
		}
		result = append(result, SessionCookie{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Path:     cookie.Path,
			Domain:   cookie.Domain,
			Expires:  cookie.Expires,
			MaxAge:   cookie.MaxAge,
			Secure:   cookie.Secure,
			HTTPOnly: cookie.HttpOnly,
			SameSite: int(cookie.SameSite),
		})
	}
	return result
}

func trimDate(value string) string {
	if len(value) < 15 {
		return value
	}
	return value[:15]
}

func classifyLoadSessionError(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %v", ErrNotAuthenticated, err)
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "session file is empty") || strings.Contains(msg, "session file has no cookies") {
		return fmt.Errorf("%w: %v", ErrNotAuthenticated, err)
	}
	return err
}

// All requests, including SSO redirects and renewal, share the caller's deadline.
type authContextTransport struct {
	ctx       context.Context
	transport http.RoundTripper
}

func (t authContextTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return t.transport.RoundTrip(r.Clone(t.ctx))
}
func bindAuthContext(client *http.Client, config AuthConfig) {
	if config.Context == nil {
		return
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = authContextTransport{config.Context, transport}
}
