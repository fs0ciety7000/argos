package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fs0ciety7000/argos/internal/inventory"
	"github.com/fs0ciety7000/argos/internal/sshpool"
)

func line(name, state, health string, exit int, oom bool, auto string) string {
	return fmt.Sprintf(`{"id":"0123456789abcdef0123","name":"/%s","image":"img:1","state":%q,"health":%q,"exitCode":%d,"oomKilled":%t,`+
		`"startedAt":"2026-11-05T10:00:00.123456789Z","finishedAt":"0001-01-01T00:00:00Z","restartCount":2,"autorestart":%q,"coolifyName":"app"}`,
		name, state, health, exit, oom, auto)
}

func TestParseList(t *testing.T) {
	out := strings.Join([]string{
		line("web", "running", "healthy", 0, false, "true"),
		line("crashed", "exited", "", 1, false, ""),
		line("stopped", "exited", "", 143, false, ""),
		line("oom", "exited", "", 137, true, ""),
		line("sick", "running", "unhealthy", 0, false, ""),
		`not json`,
		strings.Replace(line("evil", "running", "", 0, false, ""), `"/evil"`, `"/bad\u0007name"`, 1),
		strings.Replace(line("short", "running", "", 0, false, ""), "0123456789abcdef0123", "xyz", 1),
	}, "\n")
	list, err := ParseList([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 5 {
		t.Fatalf("got %d containers, want 5 (hostile lines skipped)", len(list))
	}
	byName := map[string]Container{}
	for _, c := range list {
		byName[c.Name] = c
	}
	web := byName["web"]
	if web.ID != "0123456789ab" || !web.AutoRestart || web.App != "app" || web.StartedAt == nil || web.FinishedAt != nil || web.Failing {
		t.Fatalf("web = %+v", web)
	}
	for name, want := range map[string]bool{"crashed": true, "stopped": false, "oom": true, "sick": true} {
		if byName[name].Failing != want {
			t.Errorf("%s failing = %v, want %v", name, byName[name].Failing, want)
		}
	}
}

func TestParseStats(t *testing.T) {
	out := `{"Name":"web","CPUPerc":"12.50%","MemUsage":"24.5MiB / 1.94GiB"}
{"Name":"db","CPUPerc":"NaN%","MemUsage":"1MiB / 2GiB"}
{"Name":"x","CPUPerc":"1%","MemUsage":"garbage"}
{"Name":"y","CPUPerc":"1%","MemUsage":"512B / 1kB"}`
	st := ParseStats([]byte(out))
	if len(st) != 2 {
		t.Fatalf("stats = %+v", st)
	}
	mib, gib := 24.5, 1.94 // variables: float→uint64 conversion of constants does not truncate
	if w := st["web"]; w.CPU != 12.5 || w.Mem != uint64(mib*(1<<20)) || w.MemLimit != uint64(gib*(1<<30)) {
		t.Fatalf("web = %+v", w)
	}
	if y := st["y"]; y.Mem != 512 || y.MemLimit != 1000 {
		t.Fatalf("y = %+v", y)
	}
}

// --- manager ---

type fakeRunner struct {
	mu       sync.Mutex
	list     string
	restarts []string
	fail     error
}

func (f *fakeRunner) Run(_ context.Context, host string, cmd sshpool.Command) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch cmd.Name() {
	case "docker-list":
		return []byte(f.list), nil
	case "docker-stats":
		return []byte(`{"Name":"web","CPUPerc":"3%","MemUsage":"10MiB / 1GiB"}`), nil
	case "docker-restart":
		f.restarts = append(f.restarts, host)
		return nil, f.fail
	}
	return nil, errors.New("unexpected command " + cmd.Name())
}

func (f *fakeRunner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.restarts)
}

type fakeJournal struct {
	mu      sync.Mutex
	actions []Action
}

func (j *fakeJournal) LogAction(_ context.Context, a Action) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.actions = append(j.actions, a)
	return nil
}

func (j *fakeJournal) all() []Action {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]Action(nil), j.actions...)
}

var hosts = []inventory.Host{
	{Name: "vps", Kind: inventory.KindVPS, Docker: true},
	{Name: "nodocker", Kind: inventory.KindVPS},
	{Name: "box", Kind: inventory.KindSeedbox},
}

func newManager(r *fakeRunner, j *fakeJournal) *Manager {
	return New(hosts, r, j, nil, slog.New(slog.NewTextHandler(io.Discard, nil)),
		Options{ListEvery: time.Hour, StatsEvery: time.Hour, SelfID: "fedcba987654", Protected: []string{"postgres"}})
}

func TestManagerOnlyDockerHosts(t *testing.T) {
	m := newManager(&fakeRunner{}, &fakeJournal{})
	snap := m.Snapshot()
	if len(snap) != 1 || snap[0].Host != "vps" {
		t.Fatalf("snapshot = %+v", snap)
	}
	if err := m.Restart(context.Background(), "box", "web"); !errors.Is(err, ErrNotDockerHost) {
		t.Fatalf("seedbox restart: %v", err)
	}
	if err := m.Restart(context.Background(), "nodocker", "web"); !errors.Is(err, ErrNotDockerHost) {
		t.Fatalf("non-docker vps restart: %v", err)
	}
}

func TestManualRestart(t *testing.T) {
	r := &fakeRunner{list: strings.Join([]string{
		line("web", "running", "", 0, false, ""),
		line("coolify-proxy", "running", "", 0, false, ""),
		line("postgres", "running", "", 0, false, ""),
	}, "\n")}
	j := &fakeJournal{}
	m := newManager(r, j)
	ctx := context.Background()
	m.refreshList(ctx, "vps")
	m.refreshStats(ctx, "vps")

	snap := m.Snapshot()[0]
	if c := snap.Containers[0]; c.CPU == nil || *c.CPU != 3 {
		t.Fatalf("stats not merged: %+v", c)
	}
	for _, name := range []string{"coolify-proxy", "postgres"} {
		if err := m.Restart(ctx, "vps", name); !errors.Is(err, ErrProtected) {
			t.Errorf("%s: %v, want protected", name, err)
		}
	}
	if err := m.Restart(ctx, "vps", "ghost"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unknown: %v", err)
	}
	if err := m.Restart(ctx, "vps", "a;id"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("injection: %v", err)
	}
	if err := m.Restart(ctx, "vps", "web"); err != nil {
		t.Fatal(err)
	}
	if r.count() != 1 {
		t.Fatalf("restarts = %d", r.count())
	}
	acts := j.all()
	if len(acts) != 1 || acts[0].Container != "web" || acts[0].Trigger != "manual" || !acts[0].OK || acts[0].Host != "vps" {
		t.Fatalf("journal = %+v", acts)
	}

	r.fail = errors.New("boom")
	if err := m.Restart(ctx, "vps", "web"); err == nil {
		t.Fatal("failed restart reported as success")
	}
	if a := j.all()[1]; a.OK || a.Message == "" {
		t.Fatalf("failure not journaled: %+v", a)
	}
}

func TestSelfProtected(t *testing.T) {
	r := &fakeRunner{list: strings.Replace(line("argos-app", "running", "", 0, false, ""), "0123456789abcdef0123", "fedcba9876543210ffff", 1)}
	m := newManager(r, &fakeJournal{})
	m.refreshList(context.Background(), "vps")
	if c := m.Snapshot()[0].Containers[0]; !c.Protected {
		t.Fatal("Argos' own container must be protected")
	}
}

func TestAutoRestartPolicy(t *testing.T) {
	r := &fakeRunner{list: strings.Join([]string{
		line("web", "exited", "", 1, false, "true"),       // eligible
		line("nolabel", "exited", "", 1, false, ""),       // not opted in
		line("stopped", "exited", "", 143, false, "true"), // docker stop: not a failure
		line("coolify", "exited", "", 1, false, "true"),   // protected
	}, "\n")}
	j := &fakeJournal{}
	m := newManager(r, j)
	ctx := context.Background()
	st := m.state["vps"]

	m.refreshList(ctx, "vps") // first sighting: not yet confirmed
	time.Sleep(20 * time.Millisecond)
	if r.count() != 0 {
		t.Fatal("restarted before confirmation")
	}

	var backoffs []time.Duration
	for i := range autoMaxPerHour + 2 {
		m.mu.Lock()
		st.auto["web"].next = time.Time{} // skip the backoff for the test
		m.mu.Unlock()
		before := time.Now()
		m.refreshList(ctx, "vps")
		waitFor(t, func() bool { return r.count() >= min(i+1, autoMaxPerHour) })
		if i < autoMaxPerHour {
			m.mu.Lock()
			backoffs = append(backoffs, st.auto["web"].next.Sub(before).Round(time.Minute))
			m.mu.Unlock()
		}
	}
	time.Sleep(50 * time.Millisecond)
	if r.count() != autoMaxPerHour {
		t.Fatalf("auto restarts = %d, want budget %d", r.count(), autoMaxPerHour)
	}
	var abandoned int
	for _, a := range j.all() {
		if a.Container != "web" {
			t.Fatalf("auto-restart touched %s", a.Container)
		}
		if a.Action == "auto-restart-abandoned" {
			abandoned++
		}
	}
	if abandoned != 1 {
		t.Fatalf("abandon logged %d times, want 1", abandoned)
	}

	// Backoff: attempts spaced 1, 2, 4 minutes.
	if fmt.Sprint(backoffs) != "[1m0s 2m0s 4m0s]" {
		t.Fatalf("backoffs = %v", backoffs)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met")
}
