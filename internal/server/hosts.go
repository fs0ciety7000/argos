package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/fs0ciety7000/argos/internal/sshpool"
)

// HostPool is the part of sshpool.Pool the HTTP layer needs.
type HostPool interface {
	Statuses() []sshpool.Status
	Reconnect(name string) error
}

// Hosts groups what the hosts API exposes.
type Hosts struct {
	Pool        HostPool
	PublicKey   string // authorized_keys line for Argos' own key
	Fingerprint string
}

func registerHosts(api *http.ServeMux, h Hosts) {
	api.HandleFunc("GET /api/hosts", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, h.Pool.Statuses())
	})
	// A named, idempotent action: drop and redial one connection.
	api.HandleFunc("POST /api/hosts/{name}/reconnect", func(w http.ResponseWriter, r *http.Request) {
		if err := h.Pool.Reconnect(r.PathValue("name")); err != nil {
			if errors.Is(err, sshpool.ErrUnknownHost) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown host"})
				return
			}
			if errors.Is(err, sshpool.ErrReconnectThrottled) {
				w.Header().Set("Retry-After", "10")
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "reconnect requested too recently"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
	api.HandleFunc("GET /api/ssh/public-key", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"publicKey": h.PublicKey, "fingerprint": h.Fingerprint})
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
