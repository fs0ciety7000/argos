package auth

import (
	"encoding/base32"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var rfcKey = []byte("12345678901234567890")

func TestHOTPRFC4226Vectors(t *testing.T) {
	want := []string{"755224", "287082", "359152", "969429", "338314", "254676", "287922", "162583", "399871", "520489"}
	for i, w := range want {
		if got := hotp(rfcKey, uint64(i), 6); got != w {
			t.Errorf("counter %d: got %s, want %s", i, got, w)
		}
	}
}

func TestTOTPRFC6238Vectors(t *testing.T) {
	cases := []struct {
		unix int64
		want string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	}
	for _, c := range cases {
		if got := hotp(rfcKey, uint64(c.unix)/30, 8); got != c.want {
			t.Errorf("t=%d: got %s, want %s", c.unix, got, c.want)
		}
	}
}

func newTestTOTP(t *testing.T, now *time.Time) *TOTP {
	t.Helper()
	totp, err := NewTOTP(base32.StdEncoding.EncodeToString(rfcKey))
	if err != nil {
		t.Fatal(err)
	}
	totp.now = func() time.Time { return *now }
	return totp
}

func TestTOTPVerify(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	totp := newTestTOTP(t, &now)
	step := uint64(now.Unix() / 30)

	if totp.Verify("12345") || totp.Verify("abcdef") {
		t.Fatal("malformed codes must be rejected")
	}
	if !totp.Verify(hotp(rfcKey, step-1, 6)) {
		t.Fatal("previous step should be accepted (clock skew)")
	}
	current := hotp(rfcKey, step, 6)
	if !totp.Verify(current) {
		t.Fatal("current code should be accepted")
	}
	if totp.Verify(current) {
		t.Fatal("replayed code must be rejected")
	}
	if totp.Verify(hotp(rfcKey, step-1, 6)) {
		t.Fatal("older step must be rejected after a newer one was used")
	}
	if totp.Verify(hotp(rfcKey, step+2, 6)) {
		t.Fatal("code two steps ahead must be rejected")
	}
}

func TestNewTOTPRejectsBadSecrets(t *testing.T) {
	for _, s := range []string{"", "not base32!", "MFRGG"} {
		if _, err := NewTOTP(s); err == nil {
			t.Errorf("NewTOTP(%q) should fail", s)
		}
	}
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewTOTP(strings.ToLower(secret)); err != nil {
		t.Fatalf("generated secret should parse (case-insensitive): %v", err)
	}
	if uri := TOTPURI(secret, "admin"); !strings.HasPrefix(uri, "otpauth://totp/Argos:admin?") || !strings.Contains(uri, "secret="+secret) {
		t.Fatalf("unexpected URI %q", uri)
	}
}

var fastArgon = Argon2Params{Memory: 1024, Time: 1, Threads: 1, KeyLen: 32}

func TestPasswordHash(t *testing.T) {
	phc, err := HashPassword("correct horse battery", fastArgon)
	if err != nil {
		t.Fatal(err)
	}
	h, err := ParsePasswordHash(phc)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Verify("correct horse battery") {
		t.Fatal("right password rejected")
	}
	if h.Verify("correct horse batterY") || h.Verify("") {
		t.Fatal("wrong password accepted")
	}
	bad := []string{
		"",
		"$2a$10$bcrypt",
		"$argon2i$v=19$m=1024,t=1,p=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5",
		strings.Replace(phc, "m=1024", "m=99999999", 1),
		strings.Replace(phc, "t=1", "t=0", 1),
		strings.Replace(phc, "p=1", "p=1junk", 1),
		strings.Replace(phc, "p=1", "p=64", 1),
		strings.Replace(phc, "v=19", "v=19x", 1),
	}
	for _, b := range bad {
		if _, err := ParsePasswordHash(b); err == nil {
			t.Errorf("ParsePasswordHash(%q) should fail", b)
		}
	}
}

func TestSessions(t *testing.T) {
	now := time.Unix(0, 0)
	s := NewSessions(time.Hour, 10*time.Minute)
	s.now = func() time.Time { return now }

	tok, _, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Validate(tok); !ok {
		t.Fatal("fresh session invalid")
	}
	if _, ok := s.Validate(tok + "x"); ok {
		t.Fatal("unknown token accepted")
	}
	now = now.Add(9 * time.Minute)
	if _, ok := s.Validate(tok); !ok {
		t.Fatal("active session expired early")
	}
	now = now.Add(11 * time.Minute)
	if _, ok := s.Validate(tok); ok {
		t.Fatal("idle session should expire")
	}

	tok2, _, _ := s.Create()
	for range 7 {
		now = now.Add(9 * time.Minute)
		s.Validate(tok2)
	}
	if _, ok := s.Validate(tok2); ok {
		t.Fatal("session past absolute TTL should expire")
	}

	tok3, _, _ := s.Create()
	s.Delete(tok3)
	if _, ok := s.Validate(tok3); ok {
		t.Fatal("deleted session still valid")
	}
}

func TestSessionsEvictOldest(t *testing.T) {
	now := time.Unix(0, 0)
	s := NewSessions(time.Hour, time.Hour)
	s.now = func() time.Time { return now }
	first, _, _ := s.Create()
	for range maxSessions {
		now = now.Add(time.Second)
		if _, _, err := s.Create(); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := s.Validate(first); ok {
		t.Fatal("oldest session should have been evicted")
	}
}

func TestFailureLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewFailureLimiter(3, time.Minute)
	l.now = func() time.Time { return now }
	for range 3 {
		if ok, _ := l.Reserve(); !ok {
			t.Fatal("blocked too early")
		}
		now = now.Add(time.Second)
	}
	ok, retry := l.Reserve()
	if ok || retry <= 0 || retry > time.Minute {
		t.Fatalf("expected block with retry in (0,1m], got ok=%v retry=%v", ok, retry)
	}
	now = now.Add(time.Minute)
	if ok, _ := l.Reserve(); !ok {
		t.Fatal("window should have slid")
	}
	l.Reset()
	for range 3 {
		if ok, _ := l.Reserve(); !ok {
			t.Fatal("reset should clear attempts")
		}
	}
}

// --- HTTP flow ---

type harness struct {
	svc  *Service
	h    http.Handler
	now  time.Time
	step func() string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	phc, err := HashPassword("s3cret-passphrase", fastArgon)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(Config{PasswordHash: phc, TOTPSecret: base32.StdEncoding.EncodeToString(rfcKey)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	hs := &harness{svc: svc, now: time.Unix(1_700_000_000, 0)}
	svc.totp.now = func() time.Time { return hs.now }
	hs.step = func() string { return hotp(rfcKey, uint64(hs.now.Unix()/30), 6) }

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/login", svc.Login)
	mux.HandleFunc("POST /api/auth/logout", svc.Logout)
	mux.HandleFunc("GET /api/auth/session", svc.Session)
	mux.Handle("/api/", svc.Require(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})))
	hs.h = SameOrigin(mux)
	return hs
}

func (hs *harness) do(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "https://argos.test"+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", "https://argos.test")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	hs.h.ServeHTTP(rec, req)
	return rec
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			return c
		}
	}
	t.Fatal("no session cookie set")
	return nil
}

func TestLoginFlow(t *testing.T) {
	hs := newHarness(t)

	if rec := hs.do(http.MethodGet, "/api/hosts", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("protected route without session: %d", rec.Code)
	}
	if rec := hs.do(http.MethodPost, "/api/auth/login", `{"password":"nope","code":"`+hs.step()+`"}`, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", rec.Code)
	}
	if rec := hs.do(http.MethodPost, "/api/auth/login", `{"password":"s3cret-passphrase","code":"000000"}`, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong code: %d", rec.Code)
	}

	rec := hs.do(http.MethodPost, "/api/auth/login", `{"password":"s3cret-passphrase","code":"`+hs.step()+`"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid login: %d %s", rec.Code, rec.Body)
	}
	c := sessionCookie(t, rec)
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" {
		t.Fatalf("cookie attributes too weak: %+v", c)
	}

	if rec := hs.do(http.MethodGet, "/api/auth/session", "", c); rec.Code != http.StatusOK {
		t.Fatalf("session check: %d", rec.Code)
	}
	if rec := hs.do(http.MethodGet, "/api/hosts", "", c); rec.Code != http.StatusTeapot {
		t.Fatalf("protected route with session: %d", rec.Code)
	}

	if rec := hs.do(http.MethodPost, "/api/auth/logout", "", c); rec.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", rec.Code)
	}
	if rec := hs.do(http.MethodGet, "/api/hosts", "", c); rec.Code != http.StatusUnauthorized {
		t.Fatalf("route after logout: %d", rec.Code)
	}
}

func TestLoginRejectsReplayedCode(t *testing.T) {
	hs := newHarness(t)
	body := `{"password":"s3cret-passphrase","code":"` + hs.step() + `"}`
	if rec := hs.do(http.MethodPost, "/api/auth/login", body, nil); rec.Code != http.StatusOK {
		t.Fatalf("first login: %d", rec.Code)
	}
	if rec := hs.do(http.MethodPost, "/api/auth/login", body, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("replayed code: %d", rec.Code)
	}
}

func TestLoginRateLimited(t *testing.T) {
	hs := newHarness(t)
	for range maxFailures {
		hs.do(http.MethodPost, "/api/auth/login", `{"password":"x","code":"000000"}`, nil)
	}
	rec := hs.do(http.MethodPost, "/api/auth/login", `{"password":"s3cret-passphrase","code":"`+hs.step()+`"}`, nil)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("expected 429 with Retry-After, got %d", rec.Code)
	}
}

func TestLoginInputValidation(t *testing.T) {
	hs := newHarness(t)
	req := httptest.NewRequest(http.MethodPost, "https://argos.test/api/auth/login", strings.NewReader(`password=x`))
	req.Header.Set("Origin", "https://argos.test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	hs.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("form body: %d", rec.Code)
	}
	if rec := hs.do(http.MethodPost, "/api/auth/login", `{"password":"x","code":"1","admin":true}`, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", rec.Code)
	}
	big := `{"password":"` + strings.Repeat("a", maxLoginBody) + `","code":"123456"}`
	if rec := hs.do(http.MethodPost, "/api/auth/login", big, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized body: %d", rec.Code)
	}
}

func TestSameOrigin(t *testing.T) {
	hs := newHarness(t)
	for _, origin := range []string{"", "https://evil.test", "null", "http://argos.test", "https://argos.test.evil.test", "https://argos.test/x"} {
		req := httptest.NewRequest(http.MethodPost, "https://argos.test/api/auth/logout", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		hs.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("origin %q: got %d, want 403", origin, rec.Code)
		}
	}
}

func TestNewConfig(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc, err := New(Config{}, logger)
	if err != nil || svc.Enabled() {
		t.Fatalf("empty config should give a disabled service, got err=%v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	svc.Login(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled login: %d", rec.Code)
	}
	for _, cfg := range []Config{
		{PasswordHash: "x"},
		{TOTPSecret: "x"},
		{PasswordHash: "bad", TOTPSecret: base32.StdEncoding.EncodeToString(rfcKey)},
	} {
		if _, err := New(cfg, logger); err == nil {
			t.Errorf("config %+v should be rejected", cfg)
		}
	}
}

func TestLoginRateLimitHoldsUnderConcurrency(t *testing.T) {
	hs := newHarness(t)
	const n = 40
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = hs.do(http.MethodPost, "/api/auth/login", `{"password":"x","code":"000000"}`, nil).Code
		}()
	}
	wg.Wait()
	evaluated := 0
	for _, c := range codes {
		switch c {
		case http.StatusUnauthorized:
			evaluated++
		case http.StatusTooManyRequests:
		default:
			t.Fatalf("unexpected status %d", c)
		}
	}
	if evaluated > maxFailures {
		t.Fatalf("%d passwords evaluated concurrently, limit is %d", evaluated, maxFailures)
	}
}

func TestValidOriginLoopbackHTTP(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://localhost:5173/api/auth/logout", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	if !ValidOrigin(req) {
		t.Fatal("http on localhost should be allowed for dev")
	}
}

func TestCurrentCode(t *testing.T) {
	now := time.Unix(1_700_000_005, 0)
	code, left, err := CurrentCode(base32.StdEncoding.EncodeToString(rfcKey), now)
	if err != nil {
		t.Fatal(err)
	}
	if want := hotp(rfcKey, uint64(now.Unix()/30), 6); code != want {
		t.Fatalf("code = %s, want %s", code, want)
	}
	if left != 5*time.Second {
		t.Fatalf("left = %v, want 5s", left)
	}
}
