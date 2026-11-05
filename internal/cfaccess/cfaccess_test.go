package cfaccess

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	team = "argos-test.cloudflareaccess.com"
	aud  = "3d7449fa6fc0f44f815c147c0f01d32cb50f76b6d1ce2095867dd72d714965bb"
)

type fixture struct {
	v       *Verifier
	keys    map[string]*rsa.PrivateKey
	fetches atomic.Int32
	now     time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{keys: map[string]*rsa.PrivateKey{}, now: time.Unix(1_800_000_000, 0)}
	f.addKey(t, "k1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.fetches.Add(1)
		var set jwks
		for kid, k := range f.keys {
			set.Keys = append(set.Keys, struct {
				Kid string `json:"kid"`
				Kty string `json:"kty"`
				Alg string `json:"alg"`
				N   string `json:"n"`
				E   string `json:"e"`
			}{kid, "RSA", "RS256",
				base64.RawURLEncoding.EncodeToString(k.N.Bytes()),
				base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes())})
		}
		_ = json.NewEncoder(w).Encode(set)
	}))
	t.Cleanup(srv.Close)

	v, err := New(team, aud)
	if err != nil {
		t.Fatal(err)
	}
	v.certsURL = srv.URL
	v.now = func() time.Time { return f.now }
	f.v = v
	return f
}

func (f *fixture) addKey(t *testing.T, kid string) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f.keys[kid] = k
}

func (f *fixture) token(t *testing.T, kid, alg string, c map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	signing := enc(map[string]string{"alg": alg, "kid": kid, "typ": "JWT"}) + "." + enc(c)
	digest := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.keys[kid], crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (f *fixture) claims() map[string]any {
	return map[string]any{
		"iss":   "https://" + team,
		"aud":   []string{aud},
		"exp":   f.now.Add(time.Hour).Unix(),
		"nbf":   f.now.Add(-time.Minute).Unix(),
		"email": "owner@example.com",
	}
}

func TestVerifyValid(t *testing.T) {
	f := newFixture(t)
	id, err := f.v.Verify(context.Background(), f.token(t, "k1", "RS256", f.claims()))
	if err != nil {
		t.Fatal(err)
	}
	if id.Email != "owner@example.com" {
		t.Fatalf("email = %q", id.Email)
	}
	c := f.claims()
	c["aud"] = aud // string form
	if _, err := f.v.Verify(context.Background(), f.token(t, "k1", "RS256", c)); err != nil {
		t.Fatalf("string aud: %v", err)
	}
}

func TestVerifyRejects(t *testing.T) {
	f := newFixture(t)
	mod := func(k string, v any) map[string]any { c := f.claims(); c[k] = v; return c }
	cases := map[string]string{
		"wrong aud":     f.token(t, "k1", "RS256", mod("aud", []string{"other-app-000000000000000000000000000000"})),
		"wrong issuer":  f.token(t, "k1", "RS256", mod("iss", "https://evil.cloudflareaccess.com")),
		"expired":       f.token(t, "k1", "RS256", mod("exp", f.now.Add(-2*time.Minute).Unix())),
		"no exp":        f.token(t, "k1", "RS256", mod("exp", 0)),
		"not yet valid": f.token(t, "k1", "RS256", mod("nbf", f.now.Add(2*time.Minute).Unix())),
		"alg HS256":     f.token(t, "k1", "HS256", f.claims()),
		"empty":         "",
		"garbage":       "a.b.c",
	}
	good := f.token(t, "k1", "RS256", f.claims())
	p := strings.Split(good, ".")
	tampered := f.claims()
	tampered["email"] = "attacker@example.com"
	b, _ := json.Marshal(tampered)
	cases["tampered claims"] = p[0] + "." + base64.RawURLEncoding.EncodeToString(b) + "." + p[2]
	cases["alg none"] = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","kid":"k1"}`)) + "." + p[1] + "."

	for name, tok := range cases {
		if _, err := f.v.Verify(context.Background(), tok); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestKeyRotationAndCaching(t *testing.T) {
	f := newFixture(t)
	if _, err := f.v.Verify(context.Background(), f.token(t, "k1", "RS256", f.claims())); err != nil {
		t.Fatal(err)
	}
	if _, err := f.v.Verify(context.Background(), f.token(t, "k1", "RS256", f.claims())); err != nil {
		t.Fatal(err)
	}
	if n := f.fetches.Load(); n != 1 {
		t.Fatalf("certs fetched %d times, want 1 (cached)", n)
	}

	f.addKey(t, "k2")
	// Unknown kid right after a fetch: no refetch storm.
	if _, err := f.v.Verify(context.Background(), f.token(t, "k2", "RS256", f.claims())); err == nil {
		t.Fatal("unknown kid accepted before refresh delay")
	}
	f.now = f.now.Add(minRefreshDelay)
	if _, err := f.v.Verify(context.Background(), f.token(t, "k2", "RS256", f.claims())); err != nil {
		t.Fatalf("rotated key after refresh delay: %v", err)
	}
}

func TestMiddleware(t *testing.T) {
	f := newFixture(t)
	h := f.v.Middleware(slog.New(slog.NewTextHandler(io.Discard, nil)), "/health")(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))

	do := func(path, tok string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if tok != "" {
			req.Header.Set(Header, tok)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := do("/health", ""); c != http.StatusTeapot {
		t.Fatalf("/health should skip the check, got %d", c)
	}
	if c := do("/", ""); c != http.StatusForbidden {
		t.Fatalf("no token: %d", c)
	}
	if c := do("/api/auth/session", "x.y.z"); c != http.StatusForbidden {
		t.Fatalf("bad token: %d", c)
	}
	if c := do("/", f.token(t, "k1", "RS256", f.claims())); c != http.StatusTeapot {
		t.Fatalf("valid token: %d", c)
	}
}

func TestNewValidation(t *testing.T) {
	for _, d := range []string{"", "evil.com", "team.cloudflareaccess.com/x", "user@team.cloudflareaccess.com"} {
		if _, err := New(d, aud); err == nil {
			t.Errorf("team domain %q accepted", d)
		}
	}
	if _, err := New("https://"+team+"/", aud); err != nil {
		t.Errorf("scheme and slash should be tolerated: %v", err)
	}
	if _, err := New(team, "short"); err == nil {
		t.Error("short aud accepted")
	}
}
