// Package sshpool keeps one persistent SSH connection per host, with
// keepalives, reconnection backoff, trust-on-first-use host keys and a
// closed set of named commands (no arbitrary shell).
package sshpool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/fs0ciety7000/argos/internal/inventory"
)

// State is a host's connection state.
type State string

const (
	StateConnecting     State = "connecting"
	StateConnected      State = "connected"
	StateError          State = "error"
	StateHostKeyChanged State = "hostkey_changed"
)

// Status is a snapshot of one host's connection, safe to expose to the UI.
type Status struct {
	Host           inventory.Host `json:"host"`
	State          State          `json:"state"`
	Error          string         `json:"error,omitempty"`
	LatencyMS      float64        `json:"latencyMs,omitempty"`
	LastSeen       *time.Time     `json:"lastSeen,omitempty"`
	ConnectedSince *time.Time     `json:"connectedSince,omitempty"`
	HostKey        string         `json:"hostKey,omitempty"` // SHA256 fingerprint
}

// Command is a fixed, named remote command. Its fields are unexported, so
// only the commands declared in this package can ever be run.
type Command struct {
	name string
	line string
}

// Name identifies the command in logs.
func (c Command) Name() string { return c.name }

// Read-only commands available on every host.
var (
	CmdUname   = Command{"uname", "uname -srm"}
	CmdUptime  = Command{"uptime", "cat /proc/uptime /proc/loadavg"}
	CmdMeminfo = Command{"meminfo", "cat /proc/meminfo"}
)

// Options tune timings; zero values get production defaults.
type Options struct {
	DialTimeout    time.Duration
	Keepalive      time.Duration
	MinBackoff     time.Duration
	MaxBackoff     time.Duration
	CommandTimeout time.Duration
	MaxOutput      int
}

func (o *Options) defaults() {
	if o.DialTimeout == 0 {
		o.DialTimeout = 10 * time.Second
	}
	if o.Keepalive == 0 {
		o.Keepalive = 15 * time.Second
	}
	if o.MinBackoff == 0 {
		o.MinBackoff = 2 * time.Second
	}
	if o.MaxBackoff == 0 {
		o.MaxBackoff = time.Minute
	}
	if o.CommandTimeout == 0 {
		o.CommandTimeout = 15 * time.Second
	}
	if o.MaxOutput == 0 {
		o.MaxOutput = 1 << 20
	}
}

// ErrNotConnected is returned by Run when the host has no live connection.
var ErrNotConnected = errors.New("host not connected")

// ErrUnknownHost is returned for a name not in the inventory.
var ErrUnknownHost = errors.New("unknown host")

// Pool manages the connections.
type Pool struct {
	signer   ssh.Signer
	hostKeys *HostKeys
	logger   *slog.Logger
	opts     Options
	order    []string
	conns    map[string]*conn
}

type conn struct {
	host inventory.Host
	kick chan struct{}

	mu     sync.Mutex
	client *ssh.Client
	status Status
}

// New creates a pool; call Start to connect.
func New(hosts []inventory.Host, signer ssh.Signer, hostKeys *HostKeys, logger *slog.Logger, opts Options) *Pool {
	opts.defaults()
	p := &Pool{signer: signer, hostKeys: hostKeys, logger: logger, opts: opts, conns: make(map[string]*conn, len(hosts))}
	for _, h := range hosts {
		p.order = append(p.order, h.Name)
		p.conns[h.Name] = &conn{host: h, kick: make(chan struct{}, 1), status: Status{Host: h, State: StateConnecting}}
	}
	return p
}

// Start launches one connection loop per host. They stop when ctx ends.
func (p *Pool) Start(ctx context.Context) *sync.WaitGroup {
	var wg sync.WaitGroup
	for _, name := range p.order {
		c := p.conns[name]
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.loop(ctx, c)
		}()
	}
	return &wg
}

// Statuses returns a snapshot of every host, in inventory order.
func (p *Pool) Statuses() []Status {
	out := make([]Status, 0, len(p.order))
	for _, name := range p.order {
		c := p.conns[name]
		c.mu.Lock()
		out = append(out, c.status)
		c.mu.Unlock()
	}
	return out
}

// Reconnect asks the host's loop to drop its connection and redial now.
func (p *Pool) Reconnect(name string) error {
	c, ok := p.conns[name]
	if !ok {
		return ErrUnknownHost
	}
	select {
	case c.kick <- struct{}{}:
	default: // a reconnect is already pending
	}
	return nil
}

// Run executes a named command on a host and returns its stdout, capped at
// Options.MaxOutput bytes.
func (p *Pool) Run(ctx context.Context, name string, cmd Command) ([]byte, error) {
	c, ok := p.conns[name]
	if !ok {
		return nil, ErrUnknownHost
	}
	if cmd.line == "" {
		return nil, errors.New("invalid command")
	}
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return nil, ErrNotConnected
	}

	sess, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("%s: new session: %w", cmd.name, err)
	}
	defer sess.Close()
	out := &cappedBuffer{max: p.opts.MaxOutput}
	sess.Stdout = out

	ctx, cancel := context.WithTimeout(ctx, p.opts.CommandTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd.line) }()
	select {
	case err := <-done:
		if err != nil {
			return nil, fmt.Errorf("%s: %w", cmd.name, err)
		}
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		_ = sess.Close()
		return nil, fmt.Errorf("%s: %w", cmd.name, ctx.Err())
	}
	if out.truncated {
		return nil, fmt.Errorf("%s: output larger than %d bytes", cmd.name, p.opts.MaxOutput)
	}
	return out.Bytes(), nil
}

func (p *Pool) loop(ctx context.Context, c *conn) {
	backoff := p.opts.MinBackoff
	for {
		c.setState(StateConnecting, "")
		client, fingerprint, err := p.dial(ctx, c.host)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			var changed *HostKeyChangedError
			if errors.As(err, &changed) {
				p.logger.Error("ssh host key changed: refusing to connect", "host", c.host.Name, "got", changed.Got, "want", changed.Want)
				c.setState(StateHostKeyChanged, fmt.Sprintf("clé d'hôte modifiée : reçue %s, attendue %v", changed.Got, changed.Want))
				// Never retry on our own: wait for an explicit reconnect.
				if !p.wait(ctx, c, 0) {
					return
				}
				continue
			}
			p.logger.Warn("ssh connect failed", "host", c.host.Name, "err", err, "retry_in", backoff)
			c.setState(StateError, truncate(err.Error(), 300))
			if !p.wait(ctx, c, backoff) {
				return
			}
			backoff = min(backoff*2, p.opts.MaxBackoff)
			continue
		}

		backoff = p.opts.MinBackoff
		now := time.Now()
		c.mu.Lock()
		c.client = client
		c.status.State, c.status.Error, c.status.HostKey = StateConnected, "", fingerprint
		c.status.ConnectedSince, c.status.LastSeen = &now, &now
		c.mu.Unlock()
		p.logger.Info("ssh connected", "host", c.host.Name, "hostkey", fingerprint)

		reason := p.keepalive(ctx, c, client)
		c.mu.Lock()
		c.client = nil
		c.status.ConnectedSince = nil
		c.mu.Unlock()
		_ = client.Close()
		if ctx.Err() != nil {
			return
		}
		if reason != nil {
			p.logger.Warn("ssh connection lost", "host", c.host.Name, "err", reason)
			c.setState(StateError, truncate("connexion perdue : "+reason.Error(), 300))
			if !p.wait(ctx, c, jitter(p.opts.MinBackoff)) {
				return
			}
		}
	}
}

// keepalive pings until the connection dies (returns the cause), the owner
// asks for a reconnect (returns nil) or ctx ends.
func (p *Pool) keepalive(ctx context.Context, c *conn, client *ssh.Client) error {
	t := time.NewTicker(p.opts.Keepalive)
	defer t.Stop()
	dead := make(chan error, 1)
	go func() { dead <- client.Wait() }()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-c.kick:
			return nil
		case err := <-dead:
			if err == nil {
				err = errors.New("closed by remote")
			}
			return err
		case <-t.C:
			start := time.Now()
			res := make(chan error, 1)
			go func() {
				_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
				res <- err
			}()
			select {
			case err := <-res:
				if err != nil {
					return fmt.Errorf("keepalive: %w", err)
				}
			case <-time.After(p.opts.DialTimeout):
				return errors.New("keepalive timeout")
			case <-ctx.Done():
				return nil
			}
			now := time.Now()
			c.mu.Lock()
			c.status.LastSeen = &now
			c.status.LatencyMS = float64(now.Sub(start).Microseconds()) / 1000
			c.mu.Unlock()
		}
	}
}

// wait sleeps for d (forever when d == 0) or until a reconnect is
// requested. It returns false when ctx ends.
func (p *Pool) wait(ctx context.Context, c *conn, d time.Duration) bool {
	var timer <-chan time.Time
	if d > 0 {
		t := time.NewTimer(jitter(d))
		defer t.Stop()
		timer = t.C
	}
	select {
	case <-ctx.Done():
		return false
	case <-c.kick:
		return true
	case <-timer:
		return true
	}
}

func (p *Pool) dial(ctx context.Context, h inventory.Host) (*ssh.Client, string, error) {
	var fingerprint string
	cfg := &ssh.ClientConfig{
		User: h.User,
		Auth: []ssh.AuthMethod{ssh.PublicKeys(p.signer)},
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			fingerprint = ssh.FingerprintSHA256(key)
			return p.hostKeys.Check(hostname, remote, key)
		},
		ClientVersion: "SSH-2.0-Argos",
		Timeout:       p.opts.DialTimeout,
	}
	dctx, cancel := context.WithTimeout(ctx, p.opts.DialTimeout)
	defer cancel()
	var d net.Dialer
	raw, err := d.DialContext(dctx, "tcp", h.Addr())
	if err != nil {
		return nil, "", err
	}
	_ = raw.SetDeadline(time.Now().Add(p.opts.DialTimeout))
	sc, chans, reqs, err := ssh.NewClientConn(raw, h.Addr(), cfg)
	if err != nil {
		raw.Close()
		return nil, "", err
	}
	_ = raw.SetDeadline(time.Time{})
	return ssh.NewClient(sc, chans, reqs), fingerprint, nil
}

func (c *conn) setState(s State, msg string) {
	c.mu.Lock()
	c.status.State, c.status.Error = s, msg
	c.mu.Unlock()
}

// cappedBuffer keeps at most max bytes and records truncation. It must not
// embed bytes.Buffer: io.Copy would use the promoted ReadFrom and bypass
// the cap entirely.
type cappedBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.buf.Len(); len(p) > room {
		b.truncated = true
		if room > 0 {
			b.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *cappedBuffer) Bytes() []byte { return b.buf.Bytes() }

func jitter(d time.Duration) time.Duration {
	return d/2 + rand.N(d/2+1)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
