package sshpool

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/fs0ciety7000/argos/internal/inventory"
)

// testServer is a minimal SSH server: public-key auth for one key, exec
// requests answered with a canned output.
type testServer struct {
	t       *testing.T
	ln      net.Listener
	cfg     *ssh.ServerConfig
	mu      sync.Mutex
	conns   []net.Conn
	outputs map[string]string
	execs   []string
}

func newHostSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func startServer(t *testing.T, addr string, hostKey ssh.Signer, client ssh.PublicKey) *testServer {
	t.Helper()
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(key.Marshal()) == string(client.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("unauthorized")
		},
	}
	cfg.AddHostKey(hostKey)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	s := &testServer{t: t, ln: ln, cfg: cfg, outputs: map[string]string{}}
	go s.serve()
	t.Cleanup(s.close)
	return s
}

func (s *testServer) addr() string { return s.ln.Addr().String() }

func (s *testServer) close() {
	s.ln.Close()
	s.mu.Lock()
	for _, c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()
}

func (s *testServer) serve() {
	for {
		nc, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns = append(s.conns, nc)
		s.mu.Unlock()
		go s.handle(nc)
	}
}

func (s *testServer) handle(nc net.Conn) {
	_, chans, reqs, err := ssh.NewServerConn(nc, s.cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs) // replies false to keepalives: still a reply
	for nch := range chans {
		ch, creqs, err := nch.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			for req := range creqs {
				if req.Type != "exec" {
					_ = req.Reply(false, nil)
					continue
				}
				n := binary.BigEndian.Uint32(req.Payload[:4])
				cmd := string(req.Payload[4 : 4+n])
				s.mu.Lock()
				s.execs = append(s.execs, cmd)
				out := s.outputs[cmd]
				s.mu.Unlock()
				_ = req.Reply(true, nil)
				if out == "__BLOCK__" { // a never-ending stream
					_, _ = io.WriteString(ch, "first\n")
					_, _ = io.Copy(io.Discard, ch) // until the client closes
					return
				}
				_, _ = io.WriteString(ch, out)
				status := uint32(0)
				if cmd == "refuse" {
					status = 65
				}
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
				return
			}
		}()
	}
}

type fixture struct {
	pool   *Pool
	dir    string
	signer ssh.Signer
	cancel context.CancelFunc
	wg     *sync.WaitGroup
}

func newFixture(t *testing.T, addr string) *fixture {
	t.Helper()
	dir := t.TempDir()
	signer, err := LoadOrCreateKey(filepath.Join(dir, "ssh"))
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{dir: dir, signer: signer}
}

func (f *fixture) start(t *testing.T, addr string) {
	t.Helper()
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	hk, err := NewHostKeys(filepath.Join(f.dir, "ssh", "known_hosts"), nil)
	if err != nil {
		t.Fatal(err)
	}
	hosts := []inventory.Host{{Name: "box", Kind: inventory.KindVPS, Address: host, Port: p, User: "argos"}}
	f.pool = New(hosts, f.signer, hk, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		DialTimeout: time.Second, Keepalive: 50 * time.Millisecond,
		MinBackoff: 20 * time.Millisecond, MaxBackoff: 100 * time.Millisecond,
		CommandTimeout: time.Second, MaxOutput: 64, ReconnectEvery: time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.wg = f.pool.Start(ctx)
	t.Cleanup(func() { cancel(); f.wg.Wait() })
}

func waitState(t *testing.T, p *Pool, want State) Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := p.Statuses()[0]; s.State == want {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("state never became %s; last: %+v", want, p.Statuses()[0])
	return Status{}
}

func TestConnectRunAndTOFU(t *testing.T) {
	hostKey := newHostSigner(t)
	f := newFixture(t, "")
	srv := startServer(t, "127.0.0.1:0", hostKey, f.signer.PublicKey())
	srv.outputs[CmdUname.line] = "Linux 6.1 x86_64\n"
	srv.outputs[CmdMeminfo.line] = strings.Repeat("x", 100)
	f.start(t, srv.addr())

	st := waitState(t, f.pool, StateConnected)
	if st.HostKey != ssh.FingerprintSHA256(hostKey.PublicKey()) {
		t.Fatalf("fingerprint = %s", st.HostKey)
	}
	known, _ := os.ReadFile(filepath.Join(f.dir, "ssh", "known_hosts"))
	if !strings.Contains(string(known), "127.0.0.1") {
		t.Fatalf("host key not recorded: %q", known)
	}

	out, err := f.pool.Run(context.Background(), "box", CmdUname)
	if err != nil || string(out) != "Linux 6.1 x86_64\n" {
		t.Fatalf("run uname: %q %v", out, err)
	}
	if _, err := f.pool.Run(context.Background(), "box", CmdMeminfo); err == nil {
		t.Fatal("oversized output should fail")
	}
	if _, err := f.pool.Run(context.Background(), "box", Command{}); err == nil {
		t.Fatal("zero command must be refused")
	}
	if _, err := f.pool.Run(context.Background(), "nope", CmdUname); !errors.Is(err, ErrUnknownHost) {
		t.Fatalf("unknown host: %v", err)
	}

	time.Sleep(150 * time.Millisecond)
	if s := f.pool.Statuses()[0]; s.LastSeen == nil || s.State != StateConnected {
		t.Fatalf("keepalive did not update status: %+v", s)
	}
}

func TestReconnectAfterServerRestart(t *testing.T) {
	hostKey := newHostSigner(t)
	f := newFixture(t, "")
	srv := startServer(t, "127.0.0.1:0", hostKey, f.signer.PublicKey())
	addr := srv.addr()
	f.start(t, addr)
	waitState(t, f.pool, StateConnected)

	srv.close()
	waitState(t, f.pool, StateError)
	if _, err := f.pool.Run(context.Background(), "box", CmdUname); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("run while down: %v", err)
	}

	startServer(t, addr, hostKey, f.signer.PublicKey())
	waitState(t, f.pool, StateConnected)
}

func TestHostKeyChangeIsRefused(t *testing.T) {
	f := newFixture(t, "")
	srv := startServer(t, "127.0.0.1:0", newHostSigner(t), f.signer.PublicKey())
	addr := srv.addr()
	f.start(t, addr)
	waitState(t, f.pool, StateConnected)

	srv.close()
	startServer(t, addr, newHostSigner(t), f.signer.PublicKey()) // impostor
	st := waitState(t, f.pool, StateHostKeyChanged)
	if !strings.Contains(st.Error, "SHA256:") {
		t.Fatalf("error should name fingerprints: %q", st.Error)
	}
	// It must stay refused, even after an explicit reconnect.
	_ = f.pool.Reconnect("box")
	time.Sleep(200 * time.Millisecond)
	if s := f.pool.Statuses()[0]; s.State == StateConnected {
		t.Fatal("connected to a host with a changed key")
	}
}

func TestUnauthorizedClient(t *testing.T) {
	f := newFixture(t, "")
	other := newHostSigner(t) // server only trusts some other key
	srv := startServer(t, "127.0.0.1:0", newHostSigner(t), other.PublicKey())
	f.start(t, srv.addr())
	st := waitState(t, f.pool, StateError)
	if !strings.Contains(st.Error, "unable to authenticate") {
		t.Fatalf("unexpected error: %q", st.Error)
	}
}

func TestLoadOrCreateKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ssh")
	a, err := LoadOrCreateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(a.PublicKey().Marshal()) != string(b.PublicKey().Marshal()) {
		t.Fatal("key not persisted")
	}
	info, _ := os.Stat(filepath.Join(dir, keyFile))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %o", info.Mode().Perm())
	}
	if !strings.HasPrefix(AuthorizedKey(a.PublicKey()), "restrict ssh-ed25519 ") {
		t.Fatalf("authorized key = %q", AuthorizedKey(a.PublicKey()))
	}
	_ = os.Chmod(filepath.Join(dir, keyFile), 0o644)
	if _, err := LoadOrCreateKey(dir); err == nil {
		t.Fatal("world-readable key must be refused")
	}
}

func TestHostKeyRelearnedAfterOwnerRemovesLine(t *testing.T) {
	f := newFixture(t, "")
	srv := startServer(t, "127.0.0.1:0", newHostSigner(t), f.signer.PublicKey())
	addr := srv.addr()
	f.start(t, addr)
	waitState(t, f.pool, StateConnected)

	srv.close()
	newKey := newHostSigner(t)
	startServer(t, addr, newKey, f.signer.PublicKey())
	waitState(t, f.pool, StateHostKeyChanged)

	// Owner removes the stale line (legitimate reinstall), then reconnects.
	if err := os.WriteFile(filepath.Join(f.dir, "ssh", "known_hosts"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.Reconnect("box"); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, f.pool, StateConnected)
	if st.HostKey != ssh.FingerprintSHA256(newKey.PublicKey()) {
		t.Fatalf("new key not pinned: %s", st.HostKey)
	}
}

func TestDockerCommandRefusedWithoutDocker(t *testing.T) {
	f := newFixture(t, "")
	srv := startServer(t, "127.0.0.1:0", newHostSigner(t), f.signer.PublicKey())
	f.start(t, srv.addr()) // host "box" is a VPS without docker
	waitState(t, f.pool, StateConnected)
	cmd := Command{name: "docker-ps", line: "docker ps", needsDocker: true}
	if _, err := f.pool.Run(context.Background(), "box", cmd); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("docker command on non-docker host: %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.execs) != 0 {
		t.Fatalf("command reached the server: %v", srv.execs)
	}
}

func TestReconnectThrottled(t *testing.T) {
	hosts := []inventory.Host{{Name: "box", Kind: inventory.KindVPS, Address: "127.0.0.1", Port: 1, User: "u"}}
	p := New(hosts, newHostSigner(t), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{ReconnectEvery: time.Hour})
	if err := p.Reconnect("box"); err != nil {
		t.Fatal(err)
	}
	if err := p.Reconnect("box"); !errors.Is(err, ErrReconnectThrottled) {
		t.Fatalf("second reconnect: %v", err)
	}
	if err := p.Reconnect("nope"); !errors.Is(err, ErrUnknownHost) {
		t.Fatalf("unknown host: %v", err)
	}
}

func TestKnownHostsWithoutTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	other := newHostSigner(t)
	line := "other.example " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(other.PublicKey())))
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil { // no final newline
		t.Fatal(err)
	}
	hk, err := NewHostKeys(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	key := newHostSigner(t).PublicKey()
	remote := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 22}
	if err := hk.Check("box.example:22", remote, key); err != nil {
		t.Fatalf("first contact: %v", err)
	}
	if err := hk.Check("box.example:22", remote, key); err != nil {
		t.Fatalf("known host after append: %v", err)
	}
	if err := hk.Check("other.example:22", remote, other.PublicKey()); err != nil {
		t.Fatalf("pre-existing line corrupted: %v", err)
	}
	_ = os.Chmod(path, 0o666)
	if _, err := NewHostKeys(path, nil); err == nil {
		t.Fatal("world-writable known_hosts must be refused")
	}
}

func TestDockerRestartValidation(t *testing.T) {
	for _, bad := range []string{"", "-f", "a'b", "a;id", "a b", "$(id)", ".x", strings.Repeat("a", 129)} {
		if _, err := DockerRestart(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	cmd, err := DockerRestart("web-1")
	if err != nil || cmd.line != "sudo -n /usr/local/sbin/argos-docker restart 'web-1'" || !cmd.needsDocker {
		t.Fatalf("restart command = %+v, %v", cmd, err)
	}
	if !CmdDockerList.needsDocker || !CmdDockerStats.needsDocker {
		t.Fatal("docker commands must require docker")
	}
}

func TestStream(t *testing.T) {
	f := newFixture(t, "")
	srv := startServer(t, "127.0.0.1:0", newHostSigner(t), f.signer.PublicKey())
	long := strings.Repeat("x", MaxLineLen+500)
	srv.outputs["lines"] = "one\ntwo\n" + long + "\nthree"
	srv.outputs["block"] = "__BLOCK__"
	f.start(t, srv.addr())
	waitState(t, f.pool, StateConnected)

	var got []string
	var truncated []bool
	err := f.pool.Stream(context.Background(), "box", Command{name: "t", line: "lines", stream: true}, func(l []byte, tr bool) {
		got = append(got, string(l))
		truncated = append(truncated, tr)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0] != "one" || got[3] != "three" || len(got[2]) != MaxLineLen || !truncated[2] || truncated[0] {
		t.Fatalf("lines = %d %q…, truncated = %v", len(got), got[:2], truncated)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	first := make(chan struct{}, 1)
	go func() {
		done <- f.pool.Stream(ctx, "box", Command{name: "t", line: "block", stream: true}, func([]byte, bool) {
			select {
			case first <- struct{}{}:
			default:
			}
		})
	}()
	<-first
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled stream returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not stop on cancel")
	}

	var ee *ssh.ExitError
	if err := f.pool.Stream(context.Background(), "box", Command{name: "t", line: "refuse", stream: true}, func([]byte, bool) {}); !errors.As(err, &ee) || ee.ExitStatus() != 65 {
		t.Fatalf("refused stream should surface exit status 65, got %v", err)
	}
	if _, err := f.pool.Run(context.Background(), "box", Command{name: "t", line: "lines", stream: true}); err == nil {
		t.Fatal("Run must refuse stream commands")
	}
	if err := f.pool.Stream(context.Background(), "box", CmdUname, func([]byte, bool) {}); err == nil {
		t.Fatal("Stream must refuse non-stream commands")
	}
	logs, _ := DockerLogs("web")
	if err := f.pool.Stream(context.Background(), "box", logs, func([]byte, bool) {}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("docker logs on a non-docker host: %v", err)
	}
}

func TestDockerLogsValidation(t *testing.T) {
	if _, err := DockerLogs("a;id"); err == nil {
		t.Fatal("invalid name accepted")
	}
	c, err := DockerLogs("web")
	if err != nil || c.line != "sudo -n /usr/local/sbin/argos-docker logs 'web' 200" || !c.stream || !c.needsDocker {
		t.Fatalf("logs command = %+v %v", c, err)
	}
}
