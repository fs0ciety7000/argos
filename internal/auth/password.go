package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2Params are the argon2id cost parameters encoded in a PHC string.
type Argon2Params struct {
	Memory  uint32 // KiB
	Time    uint32
	Threads uint8
	KeyLen  uint32
}

// DefaultArgon2Params follows the RFC 9106 "second recommended option"
// (64 MiB, 3 passes), sized for a single-user login.
var DefaultArgon2Params = Argon2Params{Memory: 64 * 1024, Time: 3, Threads: 2, KeyLen: 32}

// Upper bounds on parsed parameters, so a malformed hash in the environment
// cannot make every login allocate gigabytes or spin for minutes.
const (
	maxArgonMemory  = 1024 * 1024 // 1 GiB
	maxArgonTime    = 16
	maxArgonThreads = 16
	minSaltLen      = 16
	minKeyLen       = 16
	maxKeyLen       = 64
)

var b64 = base64.RawStdEncoding

// PasswordHash is a parsed argon2id PHC string.
type PasswordHash struct {
	params Argon2Params
	salt   []byte
	key    []byte
}

// HashPassword derives an argon2id PHC string:
// $argon2id$v=19$m=65536,t=3,p=2$<salt>$<key>
func HashPassword(password string, p Argon2Params) (string, error) {
	if password == "" {
		return "", errors.New("empty password")
	}
	salt := make([]byte, minSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// ParsePasswordHash validates and parses an argon2id PHC string.
func ParsePasswordHash(s string) (*PasswordHash, error) {
	parts := strings.Split(strings.TrimSpace(s), "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return nil, errors.New("not an argon2id PHC string")
	}
	if parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return nil, fmt.Errorf("unsupported argon2 version %q", parts[2])
	}
	var p Argon2Params
	// Sscanf ignores trailing input; re-render and compare to reject junk.
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil ||
		parts[3] != fmt.Sprintf("m=%d,t=%d,p=%d", p.Memory, p.Time, p.Threads) {
		return nil, fmt.Errorf("invalid argon2 parameters %q", parts[3])
	}
	if p.Memory < 8*uint32(p.Threads) || p.Memory > maxArgonMemory || p.Time < 1 || p.Time > maxArgonTime ||
		p.Threads < 1 || p.Threads > maxArgonThreads {
		return nil, errors.New("argon2 parameters out of range")
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) < minSaltLen {
		return nil, errors.New("invalid salt")
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil || len(key) < minKeyLen || len(key) > maxKeyLen {
		return nil, errors.New("invalid key")
	}
	p.KeyLen = uint32(len(key))
	return &PasswordHash{params: p, salt: salt, key: key}, nil
}

// Verify reports whether password matches, in constant time with respect
// to the derived key.
func (h *PasswordHash) Verify(password string) bool {
	got := argon2.IDKey([]byte(password), h.salt, h.params.Time, h.params.Memory, h.params.Threads, h.params.KeyLen)
	return subtle.ConstantTimeCompare(got, h.key) == 1
}
