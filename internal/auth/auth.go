// Package auth implements Argos' single-account authentication: argon2id
// password + TOTP, in-memory sessions in a __Host- cookie, a global
// failure limiter and same-origin checks for state-changing requests.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// CookieName uses the __Host- prefix: Secure, Path=/, no Domain, so the
// cookie is bound to this exact origin.
const CookieName = "__Host-argos_session"

const (
	sessionTTL    = 12 * time.Hour
	sessionIdle   = 2 * time.Hour
	maxFailures   = 10
	failureWindow = 15 * time.Minute
	maxLoginBody  = 4 << 10
	// Concurrent argon2id verifications (~64 MiB each).
	maxConcurrentHashes = 2
)

// Config holds the secrets read from the environment.
type Config struct {
	PasswordHash string // ARGOS_ADMIN_PASSWORD_HASH (argon2id PHC string)
	TOTPSecret   string // ARGOS_TOTP_SECRET (base32)
}

// Service owns credentials, sessions and the login limiter.
type Service struct {
	logger   *slog.Logger
	password *PasswordHash
	totp     *TOTP
	sessions *Sessions
	limiter  *FailureLimiter
	// hashSlots is a semaphore bounding concurrent password hashes.
	hashSlots chan struct{}
}

// New builds the service. With no credentials at all, login is disabled
// (fail closed) but the server can still start and answer /health.
// A partial or malformed configuration is an error.
func New(cfg Config, logger *slog.Logger) (*Service, error) {
	s := &Service{
		logger:    logger,
		sessions:  NewSessions(sessionTTL, sessionIdle),
		limiter:   NewFailureLimiter(maxFailures, failureWindow),
		hashSlots: make(chan struct{}, maxConcurrentHashes),
	}
	switch {
	case cfg.PasswordHash == "" && cfg.TOTPSecret == "":
		logger.Warn("authentication not configured: login disabled",
			"missing", []string{"ARGOS_ADMIN_PASSWORD_HASH", "ARGOS_TOTP_SECRET"})
		return s, nil
	case cfg.PasswordHash == "":
		return nil, errors.New("ARGOS_ADMIN_PASSWORD_HASH is required when ARGOS_TOTP_SECRET is set")
	case cfg.TOTPSecret == "":
		return nil, errors.New("ARGOS_TOTP_SECRET is required when ARGOS_ADMIN_PASSWORD_HASH is set")
	}
	pw, err := ParsePasswordHash(cfg.PasswordHash)
	if err != nil {
		return nil, fmt.Errorf("ARGOS_ADMIN_PASSWORD_HASH: %w", err)
	}
	totp, err := NewTOTP(cfg.TOTPSecret)
	if err != nil {
		return nil, fmt.Errorf("ARGOS_TOTP_SECRET: %w", err)
	}
	s.password, s.totp = pw, totp
	return s, nil
}

// Enabled reports whether credentials are configured.
func (s *Service) Enabled() bool { return s.password != nil && s.totp != nil }

type loginRequest struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

type sessionResponse struct {
	Authenticated bool      `json:"authenticated"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

// Login handles POST /api/auth/login.
func (s *Service) Login(w http.ResponseWriter, r *http.Request) {
	if !s.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "auth not configured")
		return
	}
	if !isJSON(r) {
		writeError(w, http.StatusUnsupportedMediaType, "expected application/json")
		return
	}
	var req loginRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Reserve a failure slot BEFORE hashing, so concurrent requests cannot
	// all pass the check while the first hash is still running. A success
	// clears the slots.
	if ok, retry := s.limiter.Reserve(); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retry.Seconds()))))
		writeError(w, http.StatusTooManyRequests, "too many failed attempts")
		return
	}
	// Each argon2id hash allocates ~64 MiB: cap concurrent hashes so a burst
	// of logins cannot exhaust memory.
	select {
	case s.hashSlots <- struct{}{}:
		defer func() { <-s.hashSlots }()
	default:
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, "login busy")
		return
	}

	// Always run the expensive password check; only consume a TOTP step
	// when the password is right, so a wrong guess cannot burn the code.
	if !s.password.Verify(req.Password) || !s.totp.Verify(req.Code) {
		s.logger.Warn("login failed", clientAttrs(r)...)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	token, expires, err := s.sessions.Create()
	if err != nil {
		s.logger.Error("create session", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.limiter.Reset()
	s.logger.Info("login succeeded", clientAttrs(r)...)
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(sessionTTL / time.Second),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, sessionResponse{Authenticated: true, ExpiresAt: expires})
}

// Logout handles POST /api/auth/logout. It is idempotent.
func (s *Service) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		s.sessions.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// Session handles GET /api/auth/session.
func (s *Service) Session(w http.ResponseWriter, r *http.Request) {
	expires, ok := s.validate(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse{Authenticated: true, ExpiresAt: expires})
}

// Require rejects requests without a live session.
func (s *Service) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.validate(r); !ok {
			writeError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Service) validate(r *http.Request) (time.Time, bool) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return time.Time{}, false
	}
	return s.sessions.Validate(c.Value)
}

// SameOrigin rejects state-changing requests whose Origin header is missing
// or is not https://<request host> (CSRF defence in depth on top of
// SameSite=Strict, which still lets sibling *.fs0ciety.org sites send the
// cookie). Plain http is only accepted for loopback hosts (local dev).
// Safe methods pass through: no GET handler may change state, and the
// WebSocket handler must call ValidOrigin itself.
func SameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if !ValidOrigin(r) {
			writeError(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ValidOrigin reports whether the request's Origin header names this exact
// host over https (or http on loopback).
func ValidOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	u, err := url.Parse(origin)
	if origin == "" || err != nil || u.Host == "" || u.Host != r.Host || u.Path != "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		h := u.Hostname()
		return h == "localhost" || h == "127.0.0.1" || h == "::1"
	default:
		return false
	}
}

// clientAttrs describes the caller for audit logs. Cf-Connecting-IP is
// set by Cloudflare but can be forged on direct origin access, so it is
// logged as a hint only and never used for decisions.
func clientAttrs(r *http.Request) []any {
	attrs := []any{"remote", r.RemoteAddr}
	if ip := r.Header.Get("Cf-Connecting-IP"); ip != "" {
		attrs = append(attrs, "cf_ip_unverified", ip)
	}
	return attrs
}

func isJSON(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mt == "application/json"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
