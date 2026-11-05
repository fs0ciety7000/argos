package server

import (
	"context"
	"net/http"
	"time"

	"github.com/fs0ciety7000/argos/internal/store"
)

// MetricsStore is the read side of store.Store.
type MetricsStore interface {
	History(ctx context.Context, host string, r store.Range, now time.Time) (store.Series, error)
	AggregateThroughput(ctx context.Context, r store.Range, now time.Time) (store.Throughput, error)
	DiskTrends(ctx context.Context, now time.Time) ([]store.DiskTrend, error)
}

// Metrics groups the history API and the live stream.
type Metrics struct {
	Store MetricsStore
	Known func(host string) bool // host is in the inventory
	Live  http.Handler           // WebSocket endpoint
}

const queryTimeout = 5 * time.Second

func registerMetrics(api *http.ServeMux, m *Metrics) {
	if m == nil {
		return
	}
	api.HandleFunc("GET /api/metrics", func(w http.ResponseWriter, r *http.Request) {
		host := r.URL.Query().Get("host")
		rng, err := store.ParseRange(r.URL.Query().Get("range"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if !m.Known(host) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown host"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
		defer cancel()
		series, err := m.Store.History(ctx, host, rng, time.Now())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, series)
	})
	api.HandleFunc("GET /api/throughput", func(w http.ResponseWriter, r *http.Request) {
		rng, err := store.ParseRange(r.URL.Query().Get("range"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
		defer cancel()
		tp, err := m.Store.AggregateThroughput(ctx, rng, time.Now())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, tp)
	})
	api.HandleFunc("GET /api/storage", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
		defer cancel()
		trends, err := m.Store.DiskTrends(ctx, time.Now())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, trends)
	})
	if m.Live != nil {
		api.Handle("GET /api/ws", m.Live)
	}
}
