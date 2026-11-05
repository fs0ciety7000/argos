// Package cfaccess verifies the Cloudflare Access JWT that Cloudflare adds
// to every request it proxies (Cf-Access-Jwt-Assertion). With it, a request
// that reaches the origin directly — bypassing Cloudflare and its Access
// policy — is refused, even if the VPS IP is exposed.
package cfaccess

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// Header is the header Cloudflare sets on proxied requests.
const Header = "Cf-Access-Jwt-Assertion"

const (
	leeway          = time.Minute
	certsTTL        = time.Hour
	minRefreshDelay = 30 * time.Second // on an unknown kid, refetch at most this often
	maxCertsBody    = 64 << 10
	maxTokenLen     = 8 << 10
)

// Verifier checks Access JWTs for one application (audience tag).
type Verifier struct {
	issuer   string // https://<team>.cloudflareaccess.com
	aud      string
	certsURL string
	client   *http.Client
	now      func() time.Time

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

// New returns a verifier for the given team domain (e.g.
// "myteam.cloudflareaccess.com") and application audience tag.
func New(teamDomain, aud string) (*Verifier, error) {
	teamDomain = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(teamDomain), "https://"), "/")
	if teamDomain == "" || strings.ContainsAny(teamDomain, "/?#@") || !strings.HasSuffix(teamDomain, ".cloudflareaccess.com") {
		return nil, fmt.Errorf("invalid team domain %q (want <team>.cloudflareaccess.com)", teamDomain)
	}
	if len(aud) < 32 {
		return nil, errors.New("invalid audience tag")
	}
	issuer := "https://" + teamDomain
	return &Verifier{
		issuer:   issuer,
		aud:      aud,
		certsURL: issuer + "/cdn-cgi/access/certs",
		client:   &http.Client{Timeout: 10 * time.Second},
		now:      time.Now,
	}, nil
}

type header struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

type claims struct {
	Iss   string          `json:"iss"`
	Aud   json.RawMessage `json:"aud"` // string or []string
	Exp   int64           `json:"exp"`
	Nbf   int64           `json:"nbf"`
	Email string          `json:"email"`
}

// Identity is what a valid token says about the caller.
type Identity struct {
	Email string
}

// Verify validates signature (RS256), issuer, audience and validity window.
func (v *Verifier) Verify(ctx context.Context, token string) (Identity, error) {
	if token == "" {
		return Identity{}, errors.New("missing token")
	}
	if len(token) > maxTokenLen {
		return Identity{}, errors.New("token too large")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Identity{}, errors.New("malformed token")
	}
	var h header
	if err := decodeSegment(parts[0], &h); err != nil {
		return Identity{}, fmt.Errorf("header: %w", err)
	}
	if h.Alg != "RS256" {
		return Identity{}, fmt.Errorf("unexpected alg %q", h.Alg)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Identity{}, errors.New("malformed signature")
	}
	key, err := v.key(ctx, h.Kid)
	if err != nil {
		return Identity{}, err
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); err != nil {
		return Identity{}, errors.New("bad signature")
	}

	var c claims
	if err := decodeSegment(parts[1], &c); err != nil {
		return Identity{}, fmt.Errorf("claims: %w", err)
	}
	if c.Iss != v.issuer {
		return Identity{}, fmt.Errorf("unexpected issuer %q", c.Iss)
	}
	if !audContains(c.Aud, v.aud) {
		return Identity{}, errors.New("audience mismatch")
	}
	now := v.now()
	if c.Exp == 0 || now.After(time.Unix(c.Exp, 0).Add(leeway)) {
		return Identity{}, errors.New("token expired")
	}
	if c.Nbf != 0 && now.Before(time.Unix(c.Nbf, 0).Add(-leeway)) {
		return Identity{}, errors.New("token not yet valid")
	}
	return Identity{Email: c.Email}, nil
}

// Middleware refuses every request without a valid Access token, except
// the paths listed in skip (exact match), e.g. the container health check.
func (v *Verifier) Middleware(logger *slog.Logger, skip ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if slices.Contains(skip, r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			if _, err := v.Verify(r.Context(), r.Header.Get(Header)); err != nil {
				logger.Warn("cloudflare access check failed", "err", err, "path", r.URL.Path, "remote", r.RemoteAddr)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"cloudflare access required"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (v *Verifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	stale := now.Sub(v.fetchedAt) >= certsTTL
	k, ok := v.keys[kid]
	// Refetch when the cache is stale, or the kid is unknown (key rotation)
	// and we have not refetched very recently.
	if stale || (!ok && now.Sub(v.fetchedAt) >= minRefreshDelay) {
		keys, err := v.fetch(ctx)
		if err != nil {
			if ok {
				return k, nil // keep serving with the cached key
			}
			return nil, fmt.Errorf("fetch certs: %w", err)
		}
		v.keys, v.fetchedAt = keys, now
		k, ok = keys[kid]
	}
	if !ok {
		return nil, fmt.Errorf("unknown key id %q", kid)
	}
	return k, nil
}

type jwks struct {
	Keys []struct {
		Kid string `json:"kid"`
		Kty string `json:"kty"`
		Alg string `json:"alg"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

func (v *Verifier) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.certsURL, nil)
	if err != nil {
		return nil, err
	}
	res, err := v.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", res.StatusCode)
	}
	var set jwks
	if err := json.NewDecoder(io.LimitReader(res.Body, maxCertsBody)).Decode(&set); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(e) > 4 {
			continue
		}
		exp := int(new(big.Int).SetBytes(e).Int64())
		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exp}
		if pub.N.BitLen() < 2048 || exp < 3 {
			continue
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return nil, errors.New("no usable RSA keys")
	}
	return keys, nil
}

func decodeSegment(seg string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return errors.New("bad base64")
	}
	return json.Unmarshal(b, v)
}

func audContains(raw json.RawMessage, want string) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == want
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return slices.Contains(many, want)
	}
	return false
}
