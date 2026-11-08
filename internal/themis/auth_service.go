package themis

import (
	"fmt"
	"strings"
)

// AuthService centralizes auth/session operations for both interactive and non-interactive callers.
type AuthService struct {
	BaseURL string
	Config  AuthConfig
}

func NewAuthService(baseURL string, config AuthConfig) (*AuthService, error) {
	normalizedBaseURL, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	return &AuthService{
		BaseURL: normalizedBaseURL,
		Config:  config,
	}, nil
}

func (s *AuthService) LoadSessionState() (SessionState, error) {
	return LoadSessionState(s.Config.SessionFile)
}

func (s *AuthService) SaveSessionState(state SessionState) error {
	path, err := resolveSessionFilePath(s.Config)
	if err != nil {
		return err
	}
	return SaveSessionState(path, state)
}

func (s *AuthService) VerifySession() (*Session, UserData, error) {
	session, err := NewSessionWithAuthConfig(s.BaseURL, s.Config)
	if err != nil {
		return nil, UserData{}, err
	}
	user, err := session.ValidateAuthentication()
	if err != nil {
		return nil, UserData{}, err
	}
	return session, user, nil
}

func (s *AuthService) Login(req LoginRequest) (LoginResult, error) {
	auth, err := LoadAuthSettings(s.Config.SessionFile)
	if err != nil {
		return LoginResult{}, err
	}
	// Preserve a configured key across manual logins for the same account only.
	if req.TOTPSecret == "" {
		req.TOTPSecret = savedTOTPFor(auth, req.Username, s.BaseURL)
	}
	result, err := PerformSSOLogin(s.BaseURL, s.Config, req)
	if err != nil {
		return LoginResult{}, err
	}
	return result, nil
}

func (s *AuthService) ResolveLoginRequest(username string, password string, totp string) (LoginRequest, error) {
	mergedUsername := strings.TrimSpace(username)
	mergedPassword := password
	mergedTOTP := strings.TrimSpace(totp)

	auth, err := LoadAuthSettings(s.Config.SessionFile)
	if err != nil {
		return LoginRequest{}, err
	}

	if (username == "" || strings.TrimSpace(username) == auth.Username) && auth.TOTPSecret != "" && auth.TOTPBaseURL != s.BaseURL {
		return LoginRequest{}, fmt.Errorf("saved authenticator key belongs to another Themis instance")
	}
	if mergedUsername == "" && auth.SaveUsername {
		mergedUsername = strings.TrimSpace(auth.Username)
	}
	if mergedPassword == "" && auth.SavePassword && mergedUsername == auth.Username {
		mergedPassword = auth.Password
	}

	if mergedUsername == "" {
		return LoginRequest{}, fmt.Errorf("%w: username is required for non-interactive login", ErrMissingCredentials)
	}
	if mergedPassword == "" {
		return LoginRequest{}, fmt.Errorf("%w: password is required for non-interactive login", ErrMissingCredentials)
	}
	if mergedTOTP == "" && (auth.TOTPSecret == "" || mergedUsername != auth.Username) {
		return LoginRequest{}, fmt.Errorf("%w: --totp is required for non-interactive login", ErrMissingCredentials)
	}

	return LoginRequest{
		Username:     mergedUsername,
		Password:     mergedPassword,
		TOTP:         mergedTOTP,
		TOTPSecret:   savedTOTPFor(auth, mergedUsername, s.BaseURL),
		SaveUsername: auth.SaveUsername,
		SavePassword: auth.SavePassword,
	}, nil
}

func savedTOTPFor(auth SessionAuthSettings, username, baseURL string) string {
	if auth.Username == strings.TrimSpace(username) && auth.TOTPBaseURL == baseURL {
		return auth.TOTPSecret
	}
	return ""
}
