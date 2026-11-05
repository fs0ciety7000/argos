package logstream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/fs0ciety7000/argos/internal/sshpool"
)

type fakeStreamer struct {
	lines []string
	block bool // keep the stream open until ctx ends
}

func (f *fakeStreamer) Stream(ctx context.Context, _ string, _ sshpool.Command, onLine func([]byte, bool)) error {
	for _, l := range f.lines {
		onLine([]byte(l), false)
	}
	if f.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func newServer(t *testing.T, s Streamer, origin bool) (*httptest.Server, *Handler) {
	t.Helper()
	h := &Handler{
		Streamer:   s,
		Known:      func(host, c string) bool { return host == "vps" && c == "web" },
		OriginOK:   func(*http.Request) bool { return origin },
		StillValid: func(*http.Request) bool { return true },
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, h
}

func dial(t *testing.T, srv *httptest.Server, q string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/?"+q, nil)
}

func readAll(t *testing.T, c *websocket.Conn) []message {
	t.Helper()
	var out []message
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, b, err := c.Read(ctx)
		cancel()
		if err != nil {
			return out
		}
		var m message
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
		if m.Type == "end" {
			return out
		}
	}
}

func TestStreamsLinesThenEnd(t *testing.T) {
	var in []string
	for i := range 1200 {
		in = append(in, fmt.Sprintf("2026-11-05T10:00:00.%09dZ line %d", i, i))
	}
	srv, _ := newServer(t, &fakeStreamer{lines: in}, true)
	c, _, err := dial(t, srv, "host=vps&container=web")
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	msgs := readAll(t, c)
	var got []Line
	for _, m := range msgs {
		if len(m.Lines) > maxBatch {
			t.Fatalf("batch of %d lines, max %d", len(m.Lines), maxBatch)
		}
		got = append(got, m.Lines...)
	}
	if last := msgs[len(msgs)-1]; last.Type != "end" || last.Reason != "ended" {
		t.Fatalf("last message = %+v", last)
	}
	if len(got) != 1200 || got[0].Text != "line 0" || got[0].TS == "" || got[1199].Text != "line 1199" {
		t.Fatalf("got %d lines, first %+v", len(got), got[0])
	}
}

func TestRefusals(t *testing.T) {
	srv, _ := newServer(t, &fakeStreamer{}, false)
	if _, res, err := dial(t, srv, "host=vps&container=web"); err == nil || res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin: %v %v", err, res)
	}
	srv2, h := newServer(t, &fakeStreamer{block: true}, true)
	for _, q := range []string{"host=vps&container=ghost", "host=box&container=web", "host=vps&container=a;id"} {
		if _, res, err := dial(t, srv2, q); err == nil || res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %v %v", q, err, res)
		}
	}
	var conns []*websocket.Conn
	for range maxStreams {
		c, _, err := dial(t, srv2, "host=vps&container=web")
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	if _, res, err := dial(t, srv2, "host=vps&container=web"); err == nil || res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("over the stream limit: %v %v", err, res)
	}
	for _, c := range conns {
		c.Close(websocket.StatusNormalClosure, "")
	}
	deadline := time.Now().Add(3 * time.Second)
	for h.active.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if h.active.Load() != 0 {
		t.Fatalf("%d streams still active after clients left", h.active.Load())
	}
}

func TestParseLine(t *testing.T) {
	l := parseLine([]byte("2026-11-05T10:00:00.123456789Z \x1b[31mERROR\x1b[0m boom\x07‮\r"), false)
	if l.TS != "2026-11-05T10:00:00.123456789Z" || l.Text != "ERROR boom" {
		t.Fatalf("parsed %+v", l)
	}
	if l := parseLine([]byte("no timestamp\there"), true); l.TS != "" || l.Text != "no timestamp\there" || !l.Truncated {
		t.Fatalf("parsed %+v", l)
	}
	if l := parseLine([]byte{0xff, 'a'}, false); l.Text != "�a" {
		t.Fatalf("invalid utf-8: %q", l.Text)
	}
}
