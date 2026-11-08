package themis

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// NormalizeTOTPSecret accepts a base32 key or a standard SHA1/6-digit/30s
// provisioning URI. Errors deliberately never include secret input.
func NormalizeTOTPSecret(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(raw), "otpauth:") {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "otpauth" || u.Host != "totp" {
			return "", fmt.Errorf("expected a TOTP provisioning URI")
		}
		q, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return "", fmt.Errorf("invalid TOTP provisioning parameters")
		}
		for _, values := range q {
			if len(values) != 1 {
				return "", fmt.Errorf("duplicate TOTP provisioning parameter")
			}
		}
		if a := q.Get("algorithm"); a != "" && !strings.EqualFold(a, "SHA1") {
			return "", fmt.Errorf("only SHA1 TOTP keys are supported")
		}
		if d := q.Get("digits"); d != "" && d != "6" {
			return "", fmt.Errorf("only 6-digit TOTP keys are supported")
		}
		if p := q.Get("period"); p != "" && p != "30" {
			return "", fmt.Errorf("only 30-second TOTP keys are supported")
		}
		raw = q.Get("secret")
	}
	raw = strings.ToUpper(strings.Join(strings.Fields(raw), ""))
	raw = strings.TrimRight(raw, "=")
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(raw)
	if err != nil || len(key) < 10 {
		return "", fmt.Errorf("invalid TOTP key: expected at least 16 base32 characters")
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(key), nil
}

// TOTPCode implements RFC 6238 with the parameters used by the university IdP.
func TOTPCode(secret string, at time.Time) (string, error) {
	normalized, err := NormalizeTOTPSecret(secret)
	if err != nil {
		return "", err
	}
	if at.Unix() < 0 {
		return "", fmt.Errorf("TOTP requires a time after the Unix epoch")
	}
	key, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(normalized)
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(at.Unix()/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 15
	code := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", code%1000000), nil
}
