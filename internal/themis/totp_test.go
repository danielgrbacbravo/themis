package themis

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testTOTPSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestTOTPCodeRFC6238(t *testing.T) {
	// RFC 6238 SHA1 vectors, reduced to the six digits accepted by this IdP.
	for _, tc := range []struct {
		unix int64
		want string
	}{
		{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"},
		{1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"},
	} {
		got, err := TOTPCode(testTOTPSecret, time.Unix(tc.unix, 0))
		if err != nil || got != tc.want {
			t.Fatalf("at %d: got %s, %v; want %s", tc.unix, got, err, tc.want)
		}
	}
}

func TestNormalizeTOTPSecret(t *testing.T) {
	for _, input := range []string{strings.ToLower(testTOTPSecret), "  " + testTOTPSecret[:16] + " " + testTOTPSecret[16:] + "\n", "otpauth://totp/University:alice?secret=" + testTOTPSecret + "&issuer=University&digits=6&period=30&algorithm=SHA1"} {
		got, err := NormalizeTOTPSecret(input)
		if err != nil || got != testTOTPSecret {
			t.Fatalf("normalization failed: %v", err)
		}
	}
	for _, input := range []string{"", "BAD", "private!secret", "otpauth://hotp/alice?secret=" + testTOTPSecret, "otpauth://totp/alice?secret=" + testTOTPSecret + "&digits=8", "otpauth://totp/alice?secret=" + testTOTPSecret + "&period=60", "otpauth://totp/alice?secret=" + testTOTPSecret + "&algorithm=SHA256", "otpauth://totp/alice?secret=" + testTOTPSecret + "&secret=BAD"} {
		_, err := NormalizeTOTPSecret(input)
		if err == nil {
			t.Fatal("invalid key accepted")
		}
		if strings.Contains(err.Error(), testTOTPSecret) || strings.Contains(err.Error(), "private!secret") {
			t.Fatal("error exposed key")
		}
	}
}

func savedTestAuth(base string) SessionAuthSettings {
	return SessionAuthSettings{Username: "alice", Password: " password with spaces ", SaveUsername: true, SavePassword: true, TOTPSecret: testTOTPSecret, TOTPBaseURL: base}
}

func TestSavedTOTPResolutionAndAccountBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	base := "https://themis.example.edu"
	if err := SaveAuthSettings(path, savedTestAuth(base)); err != nil {
		t.Fatal(err)
	}
	service, _ := NewAuthService(base, AuthConfig{SessionFile: path})
	request, err := service.ResolveLoginRequest("", "", "")
	if err != nil || request.TOTPSecret != testTOTPSecret || request.Password != " password with spaces " {
		t.Fatalf("saved resolution failed: %v", err)
	}
	request, err = service.ResolveLoginRequest("", "", "123456")
	if err != nil || request.TOTP != "123456" {
		t.Fatal("manual OTP did not override saved code")
	}
	_, err = service.ResolveLoginRequest("bob", "other", "")
	if !errors.Is(err, ErrMissingCredentials) {
		t.Fatalf("key reused for different account: %v", err)
	}
	other, _ := NewAuthService("https://another.example.edu", AuthConfig{SessionFile: path})
	if _, err := other.ResolveLoginRequest("", "", ""); err == nil {
		t.Fatal("key reused on different instance")
	}
	auth, _ := LoadAuthSettings(path)
	auth.Username = "bob"
	if err := SaveAuthSettings(path, auth); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadAuthSettings(path)
	if got.TOTPSecret != "" {
		t.Fatal("account change retained old key")
	}
}

func TestTOTPSettingsPersistenceAndClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	base := "https://themis.example.edu"
	seed := SessionState{BaseURL: base, User: &SessionUser{FullName: "Alice"}, Cookies: []SessionCookieScope{{URL: base, Cookies: []SessionCookie{{Name: "session", Value: "ok"}}}}}
	if err := SaveSessionState(path, seed); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := SaveAuthSettings(path, savedTestAuth(base)); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("credentials permissions: %v", info.Mode())
	}
	state, err := LoadSessionState(path)
	if err != nil || state.User.FullName != "Alice" || len(state.Cookies) != 1 {
		t.Fatal("lost session metadata")
	}
	auth := state.Auth
	auth.TOTPSecret = ""
	if err := SaveAuthSettings(path, auth); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), testTOTPSecret) {
		t.Fatal("key persisted after removal")
	}
	got, _ := LoadAuthSettings(path)
	if got.Password != auth.Password || got.TOTPBaseURL != "" {
		t.Fatal("clear affected password or retained binding")
	}
}

func TestAutomaticSessionRenewal(t *testing.T) {
	for _, mode := range []string{"expired", "no-cookies", "server-error", "no-key", "wrong-instance", "invalid-otp"} {
		t.Run(mode, func(t *testing.T) {
			logins, credentials, challenges := 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/course/", "/user":
					if mode == "server-error" {
						w.WriteHeader(503)
						return
					}
					cookie, err := r.Cookie("session")
					if err != nil || cookie.Value != "ok" {
						w.WriteHeader(401)
						return
					}
					fmt.Fprint(w, `<a href="/user">Alice</a><section class="border accent"><div class="cfg-container"><div class="cfg-line"><span class="cfg-key">Full name:</span><span class="cfg-val">Alice</span></div></div></section>`)
				case "/log/in/oidc":
					logins++
					fmt.Fprint(w, `<form action="/credentials" method="post"><input name="Ecom_User_ID"><input name="Ecom_Password"></form>`)
				case "/credentials":
					credentials++
					if r.FormValue("Ecom_User_ID") != "alice" || r.FormValue("Ecom_Password") != " password with spaces " {
						t.Error("wrong saved credentials")
					}
					fmt.Fprint(w, `<form action="/mfa" method="post"><input name="nffc"></form>`)
				case "/mfa":
					challenges++
					code := r.FormValue("nffc")
					valid := false
					for _, delta := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
						want, _ := TOTPCode(testTOTPSecret, time.Now().Add(delta))
						valid = valid || code == want
					}
					if !valid {
						t.Error("invalid generated OTP")
					}
					if mode == "invalid-otp" {
						w.WriteHeader(401)
						return
					}
					http.SetCookie(w, &http.Cookie{Name: "session", Value: "ok", Path: "/"})
					fmt.Fprint(w, `<a href="/user">Alice</a>`)
				default:
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "session.json")
			auth := savedTestAuth(server.URL)
			if mode == "no-key" {
				auth.TOTPSecret, auth.TOTPBaseURL = "", ""
			}
			if mode == "wrong-instance" {
				auth.TOTPBaseURL = "https://elsewhere.example.edu"
			}
			if err := SaveAuthSettings(path, auth); err != nil {
				t.Fatal(err)
			}
			if mode != "no-cookies" {
				st, _ := LoadSessionState(path)
				st.BaseURL = server.URL
				st.Cookies = []SessionCookieScope{{URL: server.URL, Cookies: []SessionCookie{{Name: "session", Value: "expired", Path: "/"}}}}
				if err := SaveSessionState(path, st); err != nil {
					t.Fatal(err)
				}
			}
			cfg := AuthConfig{SessionFile: path}
			session, err := NewSessionWithAuthConfig(server.URL, cfg)
			if mode == "expired" || mode == "no-cookies" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := session.ValidateAuthentication(); err != nil {
					t.Fatal(err)
				}
				authAfter, _ := LoadAuthSettings(path)
				if authAfter.TOTPSecret != testTOTPSecret {
					t.Fatal("renewal lost saved key")
				}
				if _, err := NewSessionWithAuthConfig(server.URL, cfg); err != nil {
					t.Fatal(err)
				}
				if logins != 1 || credentials != 1 || challenges != 1 {
					t.Fatal("renewal repeated or skipped SSO stages")
				}
			} else {
				if err == nil {
					t.Fatal("expected error")
				}
				if mode == "invalid-otp" {
					if logins != 1 || challenges != 1 {
						t.Fatal("invalid OTP retried")
					}
				} else if logins != 0 {
					t.Fatal("unexpected automatic login")
				}
				if mode == "server-error" && errors.Is(err, ErrSessionExpired) {
					t.Fatal("server failure misclassified as expired session")
				}
			}
		})
	}
}

func TestRenewalHonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	// No cookies causes renewal; a cancelled request must never reach the server.
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(500) }))
	defer server.Close()
	if err := SaveAuthSettings(path, savedTestAuth(server.URL)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewSessionWithAuthConfig(server.URL, AuthConfig{SessionFile: path, Context: ctx})
	if !errors.Is(err, context.Canceled) || requests != 0 {
		t.Fatalf("cancellation lost: requests=%d err=%v", requests, err)
	}
}
