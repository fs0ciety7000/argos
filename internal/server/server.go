// Package server wires HTTP routes: health check, API and the embedded UI.
package server

import (
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/fs0ciety7000/argos/web"
)

// New returns the root HTTP handler.
func New(logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		logger.Error("embedded UI unavailable", "err", err)
	} else {
		mux.Handle("GET /", http.FileServerFS(dist))
	}

	return mux
}
