package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"themis-cli/internal/themis"
)

func TestReadAuthenticatorKey(t *testing.T) {
	for _, tc := range []struct {
		input string
		valid bool
	}{
		{"gezdgnbvgy3tqojqgezdgnbvgy3tqojq\n", true},
		{"not-a-secret", false},
		{strings.Repeat("A", 8193), false},
	} {
		path := filepath.Join(t.TempDir(), "input")
		if err := os.WriteFile(path, []byte(tc.input), 0600); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		secret, err := readAuthenticatorKey(file, &out)
		file.Close()
		if (err == nil) != tc.valid {
			t.Fatalf("unexpected input result: %v", err)
		}
		if tc.valid && secret != "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" {
			t.Fatal("key not normalized")
		}
		if out.Len() != 0 {
			t.Fatal("piped secret produced terminal output")
		}
		if err != nil && strings.Contains(err.Error(), tc.input) {
			t.Fatal("key leaked in error")
		}
	}
}

func TestAuthSettingsCLIHelper(t *testing.T) {
	if os.Getenv("THEMIS_AUTH_TEST_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			runAuth(os.Args[i+1:])
			return
		}
	}
	t.Fatal("missing test arguments")
}

func TestAuthSettingsCLIStoresAndRedactsKey(t *testing.T) {
	session := filepath.Join(t.TempDir(), "session.json")
	key := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	call := func(input string, args ...string) ([]byte, error) {
		cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestAuthSettingsCLIHelper$", "--", "--session-file", session, "--json"}, args...)...)
		cmd.Env = append(os.Environ(), "THEMIS_AUTH_TEST_HELPER=1")
		cmd.Stdin = strings.NewReader(input)
		return cmd.CombinedOutput()
	}
	output, err := call(key, "--username", "alice", "--password", "fake-password", "--save-username", "--save-password", "--totp-secret-stdin")
	if err != nil {
		t.Fatalf("configuration failed: %v %s", err, output)
	}
	if strings.Contains(string(output), key) || strings.Contains(string(output), "fake-password") {
		t.Fatal("credentials leaked in auth output")
	}
	// The Go test helper adds a PASS line after returning; inspect only JSON.
	var result struct {
		Auth map[string]any `json:"auth_settings"`
	}
	if err := json.NewDecoder(bytes.NewReader(output)).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Auth["totp_secret_stored"] != true {
		t.Fatal("key not reported stored")
	}
	auth, err := themis.LoadAuthSettings(session)
	if err != nil || auth.TOTPSecret != key {
		t.Fatalf("key not saved: %v", err)
	}
	output, err = call("", "--clear-totp-secret")
	if err != nil {
		t.Fatalf("clear failed: %v %s", err, output)
	}
	auth, err = themis.LoadAuthSettings(session)
	if err != nil || auth.TOTPSecret != "" || auth.Password != "fake-password" {
		t.Fatal("clear did not preserve password")
	}
	// Validation must fail without changing any previously saved association.
	before, _ := os.ReadFile(session)
	output, err = call("private-invalid-key", "--totp-secret-stdin")
	if err == nil || strings.Contains(string(output), "private-invalid-key") {
		t.Fatal("invalid input accepted or leaked")
	}
	after, _ := os.ReadFile(session)
	if !bytes.Equal(before, after) {
		t.Fatal("invalid input changed saved auth settings")
	}
}
