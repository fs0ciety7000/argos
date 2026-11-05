package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 default; what authenticator apps implement.
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	totpPeriod = 30 * time.Second
	totpDigits = 6
	// Accept one step of clock drift on either side.
	totpSkew = 1
	// 160-bit secrets, as recommended by RFC 4226 §4.
	totpSecretLen = 20
	minSecretLen  = 16
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// hotp computes an RFC 4226 HOTP value truncated to the given digits.
func hotp(key []byte, counter uint64, digits int) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	mod := uint32(1)
	for range digits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, code%mod)
}

// TOTP verifies RFC 6238 codes and refuses to accept the same time step twice.
type TOTP struct {
	key []byte
	now func() time.Time

	mu   sync.Mutex
	last uint64 // last accepted counter; codes at or before it are replays
}

// NewTOTP parses a base32 secret (spaces, case and padding are ignored).
func NewTOTP(secret string) (*TOTP, error) {
	clean := strings.ToUpper(strings.NewReplacer(" ", "", "=", "").Replace(strings.TrimSpace(secret)))
	key, err := b32.DecodeString(clean)
	if err != nil {
		return nil, errors.New("TOTP secret is not valid base32")
	}
	if len(key) < minSecretLen {
		return nil, fmt.Errorf("TOTP secret too short: %d bytes, want at least %d", len(key), minSecretLen)
	}
	return &TOTP{key: key, now: time.Now}, nil
}

// Verify checks a 6-digit code against the current window and marks its
// time step as used.
func (t *TOTP) Verify(code string) bool {
	if len(code) != totpDigits {
		return false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return false
		}
	}
	current := uint64(t.now().Unix()) / uint64(totpPeriod/time.Second)

	t.mu.Lock()
	defer t.mu.Unlock()
	for d := -totpSkew; d <= totpSkew; d++ {
		counter := current + uint64(d)
		if counter <= t.last {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(hotp(t.key, counter, totpDigits)), []byte(code)) == 1 {
			t.last = counter
			return true
		}
	}
	return false
}

// GenerateTOTPSecret returns a new random base32 secret.
func GenerateTOTPSecret() (string, error) {
	key := make([]byte, totpSecretLen)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("read secret: %w", err)
	}
	return b32.EncodeToString(key), nil
}

// TOTPURI builds the otpauth:// URI that authenticator apps import.
func TOTPURI(secret, account string) string {
	v := url.Values{}
	v.Set("secret", secret)
	v.Set("issuer", "Argos")
	v.Set("algorithm", "SHA1")
	v.Set("digits", fmt.Sprint(totpDigits))
	v.Set("period", fmt.Sprint(int(totpPeriod/time.Second)))
	return "otpauth://totp/" + url.PathEscape("Argos:"+account) + "?" + v.Encode()
}
