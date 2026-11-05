// Package live fans out real-time messages to WebSocket clients.
package live

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	maxClients   = 8
	clientBuffer = 32
	writeTimeout = 10 * time.Second
	pingEvery    = 30 * time.Second
	recheckEvery = time.Minute
)

// ErrTooManyClients is returned when the hub is full.
var ErrTooManyClients = errors.New("too many live clients")

// Hub broadcasts JSON messages to every subscriber.
type Hub struct {
	logger *slog.Logger

	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

// NewHub returns an empty hub.
func NewHub(logger *slog.Logger) *Hub {
	return &Hub{logger: logger, subs: map[chan []byte]struct{}{}}
}

// Publish sends v (JSON-encoded once) to every client. A client too slow
// to keep up is dropped rather than slowing everyone down.
func (h *Hub) Publish(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		h.logger.Error("encode live message", "err", err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- b:
		default:
			delete(h.subs, ch)
			close(ch)
		}
	}
}

func (h *Hub) subscribe() (chan []byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) >= maxClients {
		return nil, ErrTooManyClients
	}
	ch := make(chan []byte, clientBuffer)
	h.subs[ch] = struct{}{}
	return ch, nil
}

func (h *Hub) unsubscribe(ch chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
}

// Clients returns the number of connected clients.
func (h *Hub) Clients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// Handler upgrades to a WebSocket and streams hub messages. The caller
// must already have authenticated the request. originOK enforces the
// same-origin rule (GET is exempt from the CSRF middleware); stillValid is
// polled so a connection ends when its session does; snapshot is the
// first message sent.
func (h *Hub) Handler(originOK func(*http.Request) bool, stillValid func(*http.Request) bool, snapshot func() any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !originOK(r) {
			http.Error(w, "cross-origin websocket refused", http.StatusForbidden)
			return
		}
		ch, err := h.subscribe()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer h.unsubscribe(ch)

		// coder/websocket also rejects a cross-origin handshake by default.
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		conn.SetReadLimit(1024) // clients send nothing meaningful
		ctx := conn.CloseRead(r.Context())

		first, err := json.Marshal(snapshot())
		if err != nil || write(ctx, conn, first) != nil {
			return
		}
		ping := time.NewTicker(pingEvery)
		defer ping.Stop()
		recheck := time.NewTicker(recheckEvery)
		defer recheck.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					conn.Close(websocket.StatusTryAgainLater, "too slow")
					return
				}
				if write(ctx, conn, msg) != nil {
					return
				}
			case <-ping.C:
				pctx, cancel := context.WithTimeout(ctx, writeTimeout)
				err := conn.Ping(pctx)
				cancel()
				if err != nil {
					return
				}
			case <-recheck.C:
				if !stillValid(r) {
					conn.Close(websocket.StatusPolicyViolation, "session expired")
					return
				}
			}
		}
	}
}

func write(ctx context.Context, conn *websocket.Conn, msg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, msg)
}
