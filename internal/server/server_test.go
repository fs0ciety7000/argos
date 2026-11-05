package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fs0ciety7000/argos/internal/auth"
	"github.com/fs0ciety7000/argos/internal/sshpool"
)

func newHandler(t *testing.T) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := auth.New(auth.Config{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	return New(logger, a, nil, Hosts{Pool: fakePool{}}, &Metrics{Store: nil, Known: func(string) bool { return true }})
}

func TestHealth(t *testing.T) {
	rec := httptest.NewRecorder()
	newHandler(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got, want := rec.Body.String(), `{"status":"ok"}`; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy", "X-Frame-Options", "Strict-Transport-Security"} {
		if rec.Header().Get(h) == "" {
			t.Errorf("missing security header %s", h)
		}
	}
}

func TestAPIRequiresSession(t *testing.T) {
	rec := httptest.NewRecorder()
	newHandler(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hosts", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestLoginDisabledWithoutConfig(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://argos.test/api/auth/login", strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://argos.test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	newHandler(t).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

type fakePool struct{}

func (fakePool) Statuses() []sshpool.Status { return nil }
func (fakePool) Reconnect(string) error     { return nil }

func TestHostsAPIRequiresSession(t *testing.T) {
	for _, path := range []string{"/api/hosts", "/api/ssh/public-key", "/api/metrics?host=a&range=1h", "/api/throughput?range=1h", "/api/storage"} {
		rec := httptest.NewRecorder()
		newHandler(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without session: %d, want 401", path, rec.Code)
		}
	}
}
