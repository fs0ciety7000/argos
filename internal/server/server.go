// Package server wires HTTP routes: health check, auth, API and the embedded UI.
package server

import (
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/fs0ciety7000/argos/internal/auth"
	"github.com/fs0ciety7000/argos/web"
)

// contentSecurityPolicy allows only same-origin resources. Inline styles are
// permitted because Svelte sets style attributes for dynamic values.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// Middleware wraps a handler (e.g. the Cloudflare Access check).
type Middleware func(http.Handler) http.Handler

// New returns the root HTTP handler. edge, if non-nil, runs before
// everything else except security headers (Cloudflare Access verification).
func New(logger *slog.Logger, a *auth.Service, edge Middleware, hosts Hosts) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	mux.HandleFunc("POST /api/auth/login", a.Login)
	mux.HandleFunc("POST /api/auth/logout", a.Logout)
	mux.HandleFunc("GET /api/auth/session", a.Session)

	// Every other API route requires a session. Feature handlers register on api.
	// Rules: no GET handler may change state (SameOrigin only checks unsafe
	// methods), and a WebSocket upgrade must check auth.ValidOrigin itself.
	api := http.NewServeMux()
	registerHosts(api, hosts)
	mux.Handle("/api/", a.Require(api))

	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		logger.Error("embedded UI unavailable", "err", err)
	} else {
		mux.Handle("/", readOnly(http.FileServerFS(dist)))
	}

	var h http.Handler = auth.SameOrigin(mux)
	if edge != nil {
		h = edge(h)
	}
	return securityHeaders(h)
}

// readOnly allows only GET and HEAD. "/" cannot carry a method in its
// pattern because it would conflict with the method-less "/api/" subtree.
func readOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}
