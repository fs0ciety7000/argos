package docker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/fs0ciety7000/argos/internal/inventory"
	"github.com/fs0ciety7000/argos/internal/sshpool"
)

// Runner runs a named command on a host (sshpool.Pool).
type Runner interface {
	Run(ctx context.Context, host string, cmd sshpool.Command) ([]byte, error)
}

// Action is one journal entry.
type Action struct {
	TS        time.Time `json:"ts"`
	Host      string    `json:"host"`
	Container string    `json:"container"`
	Action    string    `json:"action"`  // "restart", "auto-restart-abandoned"
	Trigger   string    `json:"trigger"` // "manual" or "auto"
	OK        bool      `json:"ok"`
	Message   string    `json:"message,omitempty"`
}

// Journal records actions (store.Store).
type Journal interface {
	LogAction(ctx context.Context, a Action) error
}

// HostContainers is the latest view of one Docker host.
type HostContainers struct {
	Host       string      `json:"host"`
	Containers []Container `json:"containers"`
	UpdatedAt  *time.Time  `json:"updatedAt,omitempty"`
	Error      string      `json:"error,omitempty"`
}

// Errors returned by Restart.
var (
	ErrNotDockerHost = errors.New("not a docker host")
	ErrUnknown       = errors.New("unknown container")
	ErrProtected     = errors.New("container is protected")
	ErrBusy          = errors.New("a restart is already in progress")
)

// Containers Argos must never restart: its own, and Coolify's core (which
// would cut access to Argos itself).
var coreProtected = map[string]bool{
	"coolify": true, "coolify-proxy": true, "coolify-db": true, "coolify-redis": true,
	"coolify-realtime": true, "coolify-sentinel": true,
}

// Auto-restart policy.
const (
	autoMaxPerHour   = 3
	autoFirstBackoff = time.Minute
	autoConfirmPolls = 2 // a failure must be seen on 2 consecutive polls
)

// Options sets polling intervals; zero values get defaults.
type Options struct {
	ListEvery  time.Duration // default 10 s
	StatsEvery time.Duration // default 30 s
	// SelfID is the short ID of Argos' own container (its hostname).
	SelfID string
	// Protected adds container names that must never be restarted.
	Protected []string
}

type autoState struct {
	seen         int         // consecutive polls seen failing
	attempts     []time.Time // within the last hour
	next         time.Time   // earliest next attempt
	gaveUp       bool        // budget exhausted: stays off until healthy for an hour or a manual restart
	healthySince time.Time
}

// autoResetAfter is how long a container must stay healthy before a
// previous give-up is forgotten.
const autoResetAfter = time.Hour

// maxConcurrentRestarts per host: bounds SSH sessions and load.
const maxConcurrentRestarts = 2

type hostState struct {
	view  HostContainers
	stats map[string]Stat
	busy  map[string]bool
	auto  map[string]*autoState
	slots chan struct{}
}

// Manager polls Docker hosts and performs restarts.
type Manager struct {
	runner    Runner
	journal   Journal
	publish   func(HostContainers)
	logger    *slog.Logger
	opts      Options
	hosts     []inventory.Host
	protected map[string]bool
	prefixes  []string // protected name prefixes ("name*" entries)
	refresh   map[string]chan struct{}

	mu    sync.Mutex
	state map[string]*hostState
}

// New builds a manager for the hosts with docker enabled.
func New(all []inventory.Host, runner Runner, journal Journal, publish func(HostContainers), logger *slog.Logger, opts Options) *Manager {
	if opts.ListEvery == 0 {
		opts.ListEvery = 10 * time.Second
	}
	if opts.StatsEvery == 0 {
		opts.StatsEvery = 30 * time.Second
	}
	if publish == nil {
		publish = func(HostContainers) {}
	}
	m := &Manager{runner: runner, journal: journal, publish: publish, logger: logger, opts: opts,
		protected: map[string]bool{}, refresh: map[string]chan struct{}{}, state: map[string]*hostState{}}
	for name := range coreProtected {
		m.protected[name] = true
	}
	for _, name := range opts.Protected {
		name = strings.TrimSpace(name)
		switch {
		case strings.HasSuffix(name, "*") && len(name) > 1:
			// Coolify renames app containers on every deploy: protect by prefix.
			m.prefixes = append(m.prefixes, strings.TrimSuffix(name, "*"))
		case name != "":
			m.protected[name] = true
		}
	}
	for _, h := range all {
		if h.Kind != inventory.KindVPS || !h.Docker {
			continue
		}
		m.hosts = append(m.hosts, h)
		m.refresh[h.Name] = make(chan struct{}, 1)
		m.state[h.Name] = &hostState{view: HostContainers{Host: h.Name, Containers: []Container{}},
			busy: map[string]bool{}, auto: map[string]*autoState{}, slots: make(chan struct{}, maxConcurrentRestarts)}
	}
	return m
}

// Snapshot returns the latest view of every Docker host.
func (m *Manager) Snapshot() []HostContainers {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]HostContainers, 0, len(m.hosts))
	for _, h := range m.hosts {
		out = append(out, m.copyView(h.Name))
	}
	return out
}

func (m *Manager) copyView(host string) HostContainers {
	v := m.state[host].view
	// make+copy, not append(nil, ...): an empty list must encode as [] not null.
	cs := make([]Container, len(v.Containers))
	copy(cs, v.Containers)
	v.Containers = cs
	return v
}

// HasContainer reports whether name is currently listed on a docker host.
func (m *Manager) HasContainer(host, name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.state[host]
	if !ok {
		return false
	}
	for _, c := range st.view.Containers {
		if c.Name == name {
			return true
		}
	}
	return false
}

// Start launches one poller per Docker host.
func (m *Manager) Start(ctx context.Context) *sync.WaitGroup {
	var wg sync.WaitGroup
	for _, h := range m.hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.poll(ctx, h.Name)
		}()
	}
	return &wg
}

func (m *Manager) poll(ctx context.Context, host string) {
	list := time.NewTicker(m.opts.ListEvery)
	defer list.Stop()
	stats := time.NewTicker(m.opts.StatsEvery)
	defer stats.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-list.C:
			m.refreshList(ctx, host)
		case <-m.refresh[host]:
			m.refreshList(ctx, host)
		case <-stats.C:
			m.refreshStats(ctx, host)
		}
	}
}

func (m *Manager) refreshList(ctx context.Context, host string) {
	out, err := m.runner.Run(ctx, host, sshpool.CmdDockerList)
	var list []Container
	if err == nil {
		list, err = ParseList(out)
	}
	if err != nil && list == nil {
		if ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		st := m.state[host]
		changed := st.view.Error != errorText(err)
		st.view.Error = errorText(err)
		v := m.copyView(host)
		m.mu.Unlock()
		if changed {
			m.logger.Warn("docker list failed", "host", host, "err", err)
			m.publish(v)
		}
		return
	}

	now := time.Now()
	m.mu.Lock()
	st := m.state[host]
	if st.view.UpdatedAt == nil || st.view.Error != "" {
		m.logger.Info("docker containers listed", "host", host, "containers", len(list))
	}
	for i := range list {
		c := &list[i]
		c.Protected = m.isProtected(c)
		if s, ok := st.stats[c.Name]; ok && c.State == "running" {
			cpu, mem, lim := s.CPU, s.Mem, s.MemLimit
			c.CPU, c.Mem, c.MemLimit = &cpu, &mem, &lim
		}
	}
	st.view.Containers, st.view.UpdatedAt, st.view.Error = list, &now, ""
	if err != nil {
		st.view.Error = errorText(err)
	}
	due := m.autoCandidatesLocked(st, now)
	v := m.copyView(host)
	m.mu.Unlock()
	m.publish(v)

	for _, name := range due {
		go func() {
			if err := m.restart(ctx, host, name, "auto"); err != nil && !errors.Is(err, ErrBusy) {
				m.logger.Warn("auto-restart failed", "host", host, "container", name, "err", err)
			}
		}()
	}
}

func (m *Manager) refreshStats(ctx context.Context, host string) {
	out, err := m.runner.Run(ctx, host, sshpool.CmdDockerStats)
	if err != nil {
		return // the list poll reports connectivity problems
	}
	stats := ParseStats(out)
	m.mu.Lock()
	st := m.state[host]
	st.stats = stats
	for i := range st.view.Containers {
		c := &st.view.Containers[i]
		if s, ok := stats[c.Name]; ok && c.State == "running" {
			cpu, mem, lim := s.CPU, s.Mem, s.MemLimit
			c.CPU, c.Mem, c.MemLimit = &cpu, &mem, &lim
		} else {
			c.CPU, c.Mem, c.MemLimit = nil, nil, nil
		}
	}
	v := m.copyView(host)
	m.mu.Unlock()
	m.publish(v)
}

func (m *Manager) isProtected(c *Container) bool {
	if m.protected[c.Name] || (m.opts.SelfID != "" && c.ID == m.opts.SelfID) {
		return true
	}
	for _, p := range m.prefixes {
		if strings.HasPrefix(c.Name, p) {
			return true
		}
	}
	return false
}

// autoCandidatesLocked updates per-container failure tracking and returns
// the containers due for an automatic restart.
func (m *Manager) autoCandidatesLocked(st *hostState, now time.Time) []string {
	var due []string
	present := map[string]bool{}
	for _, c := range st.view.Containers {
		present[c.Name] = true
		a := st.auto[c.Name]
		// Docker's own restart loop is not ours to fight.
		eligible := c.AutoRestart && !c.Protected && c.Failing && c.State != "restarting"
		if !eligible {
			if a != nil && !c.Failing {
				a.seen = 0
				// A crash loop passes through "running": only a full hour of
				// health forgets a give-up.
				if a.healthySince.IsZero() {
					a.healthySince = now
				} else if a.gaveUp && now.Sub(a.healthySince) >= autoResetAfter {
					a.gaveUp = false
				}
			}
			continue
		}
		if a == nil {
			a = &autoState{}
			st.auto[c.Name] = a
		}
		a.seen++
		a.healthySince = time.Time{}
		if a.gaveUp {
			continue
		}
		kept := a.attempts[:0]
		for _, t := range a.attempts {
			if now.Sub(t) < time.Hour {
				kept = append(kept, t)
			}
		}
		a.attempts = kept
		if a.seen < autoConfirmPolls || now.Before(a.next) || st.busy[c.Name] {
			continue
		}
		if len(a.attempts) >= autoMaxPerHour {
			if !a.gaveUp {
				a.gaveUp = true
				go m.log(Action{TS: now, Container: c.Name, Action: "auto-restart-abandoned", Trigger: "auto",
					Message: fmt.Sprintf("%d tentatives en 1 h : intervention manuelle requise", autoMaxPerHour)}, hostOf(st))
			}
			continue
		}
		a.attempts = append(a.attempts, now)
		a.next = now.Add(autoFirstBackoff << (len(a.attempts) - 1)) // 1, 2, 4 min
		due = append(due, c.Name)
	}
	for name := range st.auto { // forget removed containers
		if !present[name] {
			delete(st.auto, name)
		}
	}
	return due
}

func hostOf(st *hostState) string { return st.view.Host }

// Restart restarts one container on owner request and waits for the result.
func (m *Manager) Restart(ctx context.Context, host, name string) error {
	return m.restart(ctx, host, name, "manual")
}

// RestartAsync validates the request synchronously (unknown, protected,
// already restarting) and performs the restart in the background: a
// restart can outlast an HTTP request behind Cloudflare. The outcome goes
// to the journal and the live stream.
func (m *Manager) RestartAsync(host, name string) error {
	cmd, st, err := m.begin(host, name)
	if err != nil {
		return err
	}
	go func() {
		if err := m.finish(context.Background(), host, name, "manual", cmd, st); err != nil {
			m.logger.Warn("manual restart failed", "host", host, "container", name, "err", err)
		}
	}()
	return nil
}

func (m *Manager) restart(ctx context.Context, host, name, trigger string) error {
	cmd, st, err := m.begin(host, name)
	if err != nil {
		return err
	}
	return m.finish(ctx, host, name, trigger, cmd, st)
}

// begin validates a restart and marks the container busy.
func (m *Manager) begin(host, name string) (sshpool.Command, *hostState, error) {
	cmd, err := sshpool.DockerRestart(name)
	if err != nil {
		return sshpool.Command{}, nil, ErrUnknown
	}
	m.mu.Lock()
	st, ok := m.state[host]
	if !ok {
		m.mu.Unlock()
		return sshpool.Command{}, nil, ErrNotDockerHost
	}
	var found *Container
	for i := range st.view.Containers {
		if st.view.Containers[i].Name == name {
			found = &st.view.Containers[i]
			break
		}
	}
	switch {
	case found == nil:
		m.mu.Unlock()
		return sshpool.Command{}, nil, ErrUnknown
	case found.Protected:
		m.mu.Unlock()
		return sshpool.Command{}, nil, ErrProtected
	case st.busy[name]:
		m.mu.Unlock()
		return sshpool.Command{}, nil, ErrBusy
	}
	st.busy[name] = true
	found.State = "restarting"
	v := m.copyView(host)
	m.mu.Unlock()
	m.publish(v)
	return cmd, st, nil
}

// finish runs the restart, clears the busy flag and journals the outcome.
func (m *Manager) finish(ctx context.Context, host, name, trigger string, cmd sshpool.Command, st *hostState) error {
	st.slots <- struct{}{} // at most maxConcurrentRestarts per host
	m.logger.Info("restarting container", "host", host, "container", name, "trigger", trigger)
	_, runErr := m.runner.Run(context.WithoutCancel(ctx), host, cmd)
	<-st.slots

	m.mu.Lock()
	delete(st.busy, name)
	if a := st.auto[name]; a != nil && trigger == "manual" {
		*a = autoState{} // the owner took over: fresh auto-restart budget
	}
	m.mu.Unlock()

	a := Action{TS: time.Now(), Container: name, Action: "restart", Trigger: trigger, OK: runErr == nil}
	if runErr != nil {
		a.Message = errorText(runErr)
	}
	m.log(a, host)
	select { // refresh the list now rather than in up to 10 s
	case m.refresh[host] <- struct{}{}:
	default:
	}
	if runErr != nil {
		return fmt.Errorf("restart %s: %w", name, runErr)
	}
	return nil
}

func (m *Manager) log(a Action, host string) {
	a.Host = host
	if m.journal == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.journal.LogAction(ctx, a); err != nil {
		m.logger.Error("journal action", "err", err)
	}
}

func errorText(err error) string {
	s := err.Error()
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
