// Package docker tracks containers on Docker hosts through the argos-docker
// guard script: listing, stats, manual and automatic restarts.
package docker

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Limits on host-supplied data (a compromised host controls this output).
const (
	maxContainers = 500
	maxNameLen    = 128
	maxImageLen   = 256
	maxShortLen   = 32
)

// Container is one container as shown in the UI.
type Container struct {
	ID           string     `json:"id"` // short (12 chars)
	Name         string     `json:"name"`
	Image        string     `json:"image"`
	State        string     `json:"state"`            // running, exited, restarting, paused, created, dead
	Health       string     `json:"health,omitempty"` // healthy, unhealthy, starting
	ExitCode     int        `json:"exitCode"`
	OOMKilled    bool       `json:"oomKilled"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
	RestartCount int        `json:"restartCount"`
	AutoRestart  bool       `json:"autoRestart"`   // label argos.autorestart=true
	App          string     `json:"app,omitempty"` // label coolify.name
	Protected    bool       `json:"protected"`
	Failing      bool       `json:"failing"`
	CPU          *float64   `json:"cpu,omitempty"` // percent (may exceed 100 on multi-core)
	Mem          *uint64    `json:"mem,omitempty"` // bytes
	MemLimit     *uint64    `json:"memLimit,omitempty"`
}

type inspectLine struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Image        string `json:"image"`
	State        string `json:"state"`
	Health       string `json:"health"`
	ExitCode     int    `json:"exitCode"`
	OOMKilled    bool   `json:"oomKilled"`
	StartedAt    string `json:"startedAt"`
	FinishedAt   string `json:"finishedAt"`
	RestartCount int    `json:"restartCount"`
	AutoRestart  string `json:"autorestart"`
	CoolifyName  string `json:"coolifyName"`
}

// ParseList reads `argos-docker list` (one JSON object per line).
// Malformed or out-of-bounds lines are skipped, not fatal.
func ParseList(out []byte) ([]Container, error) {
	var list []Container
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), 256<<10)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var l inspectLine
		if err := json.Unmarshal(line, &l); err != nil {
			continue
		}
		c, ok := l.container()
		if !ok {
			continue
		}
		if len(list) >= maxContainers {
			return list, errors.New("too many containers, list truncated")
		}
		list = append(list, c)
	}
	return list, sc.Err()
}

func (l inspectLine) container() (Container, bool) {
	name := strings.TrimPrefix(l.Name, "/")
	if !clean(name, maxNameLen) || !clean(l.Image, maxImageLen) || !clean(l.State, maxShortLen) ||
		(l.Health != "" && !clean(l.Health, maxShortLen)) || !isHex(l.ID) || len(l.ID) < 12 {
		return Container{}, false
	}
	c := Container{
		ID: l.ID[:12], Name: name, Image: l.Image, State: l.State, Health: l.Health,
		ExitCode: l.ExitCode, OOMKilled: l.OOMKilled, RestartCount: max(l.RestartCount, 0),
		AutoRestart: l.AutoRestart == "true",
	}
	if clean(l.CoolifyName, maxNameLen) {
		c.App = l.CoolifyName
	}
	c.StartedAt = dockerTime(l.StartedAt)
	c.FinishedAt = dockerTime(l.FinishedAt)
	c.Failing = failing(c)
	return c, true
}

// failing: crashed (non-zero exit that is not a plain `docker stop`),
// unhealthy, or stuck in Docker's own restart loop.
func failing(c Container) bool {
	switch {
	case c.Health == "unhealthy", c.State == "restarting":
		return true
	case c.State == "exited" || c.State == "dead":
		if c.ExitCode == 0 {
			return false
		}
		// 137 (SIGKILL) and 143 (SIGTERM) are what `docker stop` leaves,
		// unless the kernel OOM-killed the process.
		if (c.ExitCode == 137 || c.ExitCode == 143) && !c.OOMKilled {
			return false
		}
		return true
	}
	return false
}

// Stat is one line of `docker stats`.
type Stat struct {
	CPU      float64
	Mem      uint64
	MemLimit uint64
}

type statsLine struct {
	Name     string `json:"Name"`
	CPUPerc  string `json:"CPUPerc"`
	MemUsage string `json:"MemUsage"`
}

// ParseStats reads `argos-docker stats`, keyed by container name.
func ParseStats(out []byte) map[string]Stat {
	stats := map[string]Stat{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), 256<<10)
	for sc.Scan() && len(stats) < maxContainers {
		var l statsLine
		if json.Unmarshal(bytes.TrimSpace(sc.Bytes()), &l) != nil || !clean(l.Name, maxNameLen) {
			continue
		}
		cpu, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(l.CPUPerc), "%"), 64)
		if err != nil || math.IsNaN(cpu) || math.IsInf(cpu, 0) || cpu < 0 || cpu > 100_000 {
			continue
		}
		used, limit, ok := strings.Cut(l.MemUsage, "/")
		if !ok {
			continue
		}
		mem, ok1 := parseSize(used)
		lim, ok2 := parseSize(limit)
		if !ok1 || !ok2 {
			continue
		}
		stats[l.Name] = Stat{CPU: cpu, Mem: mem, MemLimit: lim}
	}
	return stats
}

var sizeUnits = map[string]float64{
	"B": 1, "kB": 1e3, "KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12,
	"KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40,
}

// parseSize reads Docker's human sizes ("24.5MiB", "1.94GiB", "512B").
func parseSize(s string) (uint64, bool) {
	s = strings.TrimSpace(s)
	i := strings.IndexFunc(s, func(r rune) bool { return unicode.IsLetter(r) })
	if i <= 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(s[:i], 64)
	mult, ok := sizeUnits[s[i:]]
	if err != nil || !ok || math.IsNaN(v) || v < 0 {
		return 0, false
	}
	b := v * mult
	if b > float64(math.MaxInt64) {
		return 0, false
	}
	return uint64(b), true
}

func dockerTime(s string) *time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.Year() < 2000 { // Docker uses 0001-01-01 for "never"
		return nil
	}
	return &t
}

func clean(s string, maxLen int) bool {
	if s == "" || len(s) > maxLen || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func isHex(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return s != ""
}
