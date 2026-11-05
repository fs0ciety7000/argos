// Package logstream streams one container's logs to a browser over a
// dedicated WebSocket: opened on demand, closed when the viewer leaves.
package logstream

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/coder/websocket"

	"github.com/fs0ciety7000/argos/internal/sshpool"
)

const (
	maxStreams   = 4
	lineBuffer   = 2000
	flushEvery   = 200 * time.Millisecond
	maxBatch     = 500 // lines per message: caps the rate at 2500 lines/s
	writeTimeout = 10 * time.Second
	recheckEvery = 15 * time.Second
)

// Streamer follows a command's output (sshpool.Pool).
type Streamer interface {
	Stream(ctx context.Context, host string, cmd sshpool.Command, onLine func([]byte, bool)) error
}

// Line is one log line as sent to the browser.
type Line struct {
	TS        string `json:"ts,omitempty"` // RFC 3339 from docker --timestamps
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
}

type message struct {
	Type    string `json:"type"` // "lines" | "end"
	Lines   []Line `json:"lines,omitempty"`
	Dropped int64  `json:"dropped,omitempty"`
	Reason  string `json:"reason,omitempty"` // for "end"
}

// Handler serves GET /api/logs/ws?host=…&container=…
type Handler struct {
	Streamer Streamer
	// Known reports whether container currently exists on docker host.
	Known      func(host, container string) bool
	OriginOK   func(*http.Request) bool
	StillValid func(*http.Request) bool
	Logger     *slog.Logger

	active atomic.Int32
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, name := r.URL.Query().Get("host"), r.URL.Query().Get("container")
	if !h.OriginOK(r) {
		http.Error(w, "cross-origin websocket refused", http.StatusForbidden)
		return
	}
	cmd, err := sshpool.DockerLogs(name)
	if err != nil || !h.Known(host, name) {
		http.Error(w, "unknown container", http.StatusNotFound)
		return
	}
	if h.active.Add(1) > maxStreams {
		h.active.Add(-1)
		http.Error(w, "too many log streams", http.StatusServiceUnavailable)
		return
	}
	defer h.active.Add(-1)

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1024)
	ctx, cancel := context.WithCancel(conn.CloseRead(r.Context()))
	defer cancel()

	lines := make(chan Line, lineBuffer)
	var dropped atomic.Int64
	ended := make(chan error, 1)
	go func() {
		ended <- h.Streamer.Stream(ctx, host, cmd, func(b []byte, truncated bool) {
			select {
			case lines <- parseLine(b, truncated):
			default:
				dropped.Add(1) // the browser cannot keep up: drop, never block SSH
			}
		})
	}()

	flush := time.NewTicker(flushEvery)
	defer flush.Stop()
	recheck := time.NewTicker(recheckEvery)
	defer recheck.Stop()
	send := func(all bool) error {
		for {
			batch := drain(lines, maxBatch)
			d := dropped.Swap(0)
			if len(batch) == 0 && d == 0 {
				return nil
			}
			if err := write(ctx, conn, message{Type: "lines", Lines: batch, Dropped: d}); err != nil {
				return err
			}
			if !all || len(batch) < maxBatch {
				return nil
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case err := <-ended:
			if send(true) != nil {
				return
			}
			_ = write(ctx, conn, message{Type: "end", Reason: reason(err)})
			conn.Close(websocket.StatusNormalClosure, "stream ended")
			return
		case <-flush.C:
			if send(false) != nil {
				return
			}
		case <-recheck.C:
			if !h.StillValid(r) {
				conn.Close(websocket.StatusPolicyViolation, "session expired")
				return
			}
		}
	}
}

func drain(ch <-chan Line, max int) []Line {
	var out []Line
	for len(out) < max {
		select {
		case l := <-ch:
			out = append(out, l)
		default:
			return out
		}
	}
	return out
}

func reason(err error) string {
	switch {
	case err == nil:
		return "ended" // container stopped or logs closed
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout" // one-hour cap
	case errors.Is(err, sshpool.ErrNotConnected):
		return "not-connected"
	default:
		return "error"
	}
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)

// parseLine splits docker's timestamp prefix and cleans the text: ANSI
// escapes and control characters removed, invalid UTF-8 replaced.
func parseLine(b []byte, truncated bool) Line {
	s := string(b)
	l := Line{Truncated: truncated}
	if ts, rest, ok := strings.Cut(s, " "); ok && len(ts) >= 20 && ts[4] == '-' && ts[10] == 'T' {
		if _, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			l.TS, s = ts, rest
		}
	}
	s = ansi.ReplaceAllString(s, "")
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	l.Text = strings.Map(func(r rune) rune {
		if r == '\t' {
			return r
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, strings.TrimRight(s, "\r"))
	return l
}

func write(ctx context.Context, conn *websocket.Conn, m message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, b)
}
