package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/fs0ciety7000/argos/internal/docker"
)

// ContainerManager is the part of docker.Manager the HTTP layer needs.
type ContainerManager interface {
	Snapshot() []docker.HostContainers
	RestartAsync(host, name string) error
}

// ActionLog reads the action journal.
type ActionLog interface {
	RecentActions(ctx context.Context, limit int) ([]docker.Action, error)
}

// Containers groups the Docker API.
type Containers struct {
	Manager ContainerManager
	Actions ActionLog
}

func registerContainers(api *http.ServeMux, c *Containers) {
	if c == nil {
		return
	}
	api.HandleFunc("GET /api/containers", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, c.Manager.Snapshot())
	})
	// Named action, validated server-side; runs asynchronously (202).
	api.HandleFunc("POST /api/hosts/{host}/containers/{name}/restart", func(w http.ResponseWriter, r *http.Request) {
		err := c.Manager.RestartAsync(r.PathValue("host"), r.PathValue("name"))
		switch {
		case err == nil:
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "restarting"})
		case errors.Is(err, docker.ErrNotDockerHost), errors.Is(err, docker.ErrUnknown):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown container"})
		case errors.Is(err, docker.ErrProtected):
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "container is protected"})
		case errors.Is(err, docker.ErrBusy):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "restart already in progress"})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		}
	})
	api.HandleFunc("GET /api/actions", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
		defer cancel()
		acts, err := c.Actions.RecentActions(ctx, 50)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, acts)
	})
}
