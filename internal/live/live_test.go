package live

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func newServer(t *testing.T, h *Hub, originOK bool, valid *atomic.Bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h.Handler(
		func(*http.Request) bool { return originOK },
		func(*http.Request) bool { return valid.Load() },
		func() any { return map[string]string{"type": "snapshot"} },
	))
	t.Cleanup(srv.Close)
	return srv
}

func dial(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c
}

func read(t *testing.T, c *websocket.Conn) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func logger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestSnapshotThenBroadcast(t *testing.T) {
	h := NewHub(logger())
	var valid atomic.Bool
	valid.Store(true)
	srv := newServer(t, h, true, &valid)
	a, b := dial(t, srv), dial(t, srv)
	if got := read(t, a); got != `{"type":"snapshot"}` {
		t.Fatalf("first message = %s", got)
	}
	read(t, b)
	h.Publish(map[string]int{"n": 1})
	if got := read(t, a); got != `{"n":1}` {
		t.Fatalf("a got %s", got)
	}
	if got := read(t, b); got != `{"n":1}` {
		t.Fatalf("b got %s", got)
	}
}

func TestOriginRefused(t *testing.T) {
	h := NewHub(logger())
	var valid atomic.Bool
	srv := newServer(t, h, false, &valid)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, res, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err == nil || res == nil || res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin dial should get 403, got err=%v res=%v", err, res)
	}
	if h.Clients() != 0 {
		t.Fatal("refused client was registered")
	}
}

func TestMaxClients(t *testing.T) {
	h := NewHub(logger())
	for range maxClients {
		if _, err := h.subscribe(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.subscribe(); err != ErrTooManyClients {
		t.Fatalf("over the limit: %v", err)
	}
}

func TestSlowClientDropped(t *testing.T) {
	h := NewHub(logger())
	ch, _ := h.subscribe()
	for range clientBuffer + 1 {
		h.Publish(1)
	}
	if h.Clients() != 0 {
		t.Fatal("slow client should be dropped")
	}
	n := 0
	for range ch {
		n++
	}
	if n != clientBuffer {
		t.Fatalf("drained %d buffered messages", n)
	}
}
