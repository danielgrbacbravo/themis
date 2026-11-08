package themis

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// LoadAuthSettings loads persisted global auth preferences/credentials.
// Missing session files are treated as empty settings.
func LoadAuthSettings(sessionFile string) (SessionAuthSettings, error) {
	path, err := resolveSessionFilePath(AuthConfig{SessionFile: sessionFile})
	if err != nil {
		return SessionAuthSettings{}, err
	}

	state, err := loadSessionState(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SessionAuthSettings{}, nil
		}
		return SessionAuthSettings{}, err
	}

	return normalizeAuthSettings(state.Auth), nil
}

// SaveAuthSettings persists global auth preferences/credentials while preserving
// all non-auth session fields (including cookies and user metadata).
func SaveAuthSettings(sessionFile string, auth SessionAuthSettings) error {
	path, err := resolveSessionFilePath(AuthConfig{SessionFile: sessionFile})
	if err != nil {
		return err
	}

	state, err := loadSessionState(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		state = SessionState{
			SchemaVersion: sessionFileSchemaVersion,
			Cookies:       []SessionCookieScope{},
		}
	}

	if state.SchemaVersion == 0 {
		state.SchemaVersion = sessionFileSchemaVersion
	}
	if state.Cookies == nil {
		state.Cookies = []SessionCookieScope{}
	}

	if state.Auth.Username != auth.Username && state.Auth.TOTPSecret == auth.TOTPSecret {
		auth.TOTPSecret, auth.TOTPBaseURL = "", ""
	}
	if auth.TOTPSecret != "" {
		if !auth.SaveUsername || strings.TrimSpace(auth.Username) == "" {
			return fmt.Errorf("save a username before storing a TOTP key")
		}
		auth.TOTPSecret, err = NormalizeTOTPSecret(auth.TOTPSecret)
		if err != nil {
			return err
		}
		if auth.TOTPBaseURL == "" {
			return fmt.Errorf("TOTP key must be bound to a Themis instance")
		}
		auth.TOTPBaseURL, err = NormalizeBaseURL(auth.TOTPBaseURL)
		if err != nil {
			return fmt.Errorf("invalid TOTP instance URL")
		}
	}
	state.Auth = normalizeAuthSettings(auth)
	return SaveSessionState(path, state)
}

func normalizeAuthSettings(auth SessionAuthSettings) SessionAuthSettings {
	auth.Username = strings.TrimSpace(auth.Username)
	if auth.SavePassword {
		auth.SaveUsername = true
	}
	if !auth.SaveUsername {
		auth.Username = ""
		auth.SavePassword = false
		auth.Password = ""
	}
	if !auth.SavePassword {
		auth.Password = ""
	}
	if !auth.SaveUsername || auth.Username == "" {
		auth.TOTPSecret = ""
	}
	if auth.TOTPSecret == "" {
		auth.TOTPBaseURL = ""
	}
	return auth
}
