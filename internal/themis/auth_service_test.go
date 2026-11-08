package themis

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestAuthServiceResolveLoginRequest_UsesSavedCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	if err := SaveAuthSettings(path, SessionAuthSettings{
		Username:     "saved-user",
		Password:     "saved-pass",
		SaveUsername: true,
		SavePassword: true,
	}); err != nil {
		t.Fatalf("save auth settings: %v", err)
	}

	service, err := NewAuthService("https://themis.housing.rug.nl", AuthConfig{SessionFile: path})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}

	req, err := service.ResolveLoginRequest("", "", "123456")
	if err != nil {
		t.Fatalf("resolve login request: %v", err)
	}
	if req.Username != "saved-user" || req.Password != "saved-pass" || req.TOTP != "123456" {
		t.Fatalf("unexpected resolved request: %#v", req)
	}
	if !req.SaveUsername || !req.SavePassword {
		t.Fatalf("expected save preferences preserved, got: %#v", req)
	}
}

func TestAuthServiceResolveLoginRequest_MissingCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	service, err := NewAuthService("https://themis.housing.rug.nl", AuthConfig{SessionFile: path})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}

	_, err = service.ResolveLoginRequest("", "", "123456")
	if !errors.Is(err, ErrMissingCredentials) {
		t.Fatalf("expected ErrMissingCredentials, got: %v", err)
	}
}

func TestAuthServiceVerifySession_NotAuthenticated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-session.json")
	service, err := NewAuthService("https://themis.housing.rug.nl", AuthConfig{SessionFile: path})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}

	_, _, err = service.VerifySession()
	if !errors.Is(err, ErrNotAuthenticated) {
		t.Fatalf("expected ErrNotAuthenticated, got: %v", err)
	}
}

func TestAuthServiceVerifySession_Expired(t *testing.T) {
	server := newAuthServiceTestServer()
	defer server.Close()

	path := filepath.Join(t.TempDir(), "session.json")
	if err := SaveSessionState(path, SessionState{
		SchemaVersion: 1,
		BaseURL:       server.URL,
		Cookies: []SessionCookieScope{
			{
				URL: server.URL,
				Cookies: []SessionCookie{
					{Name: "session", Value: "expired"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("save seed session: %v", err)
	}

	service, err := NewAuthService(server.URL, AuthConfig{SessionFile: path})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}

	_, _, err = service.VerifySession()
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expected ErrSessionExpired, got: %v", err)
	}
}

func newAuthServiceTestServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "ok")
			return
		case "/user", "/course/":
			cookie, err := r.Cookie("session")
			if err != nil || cookie.Value != "ok" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, "unauthorized")
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `
<section class="border accent">
  <div class="cfg-container">
    <div class="cfg-line"><span class="cfg-key">Full name:</span><span class="cfg-val">Alice Example</span></div>
    <div class="cfg-line"><span class="cfg-key">Email:</span><span class="cfg-val">alice@example.com</span></div>
  </div>
</section>`)
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "not found")
		}
	}))
}
