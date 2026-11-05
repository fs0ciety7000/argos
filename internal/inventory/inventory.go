// Package inventory parses and validates the list of monitored hosts
// (ARGOS_HOSTS, a JSON array).
package inventory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
)

// Kind is the type of a host. It decides which actions are allowed.
type Kind string

const (
	// KindVPS is a machine we administer: Docker actions may be enabled.
	KindVPS Kind = "vps"
	// KindSeedbox is a shared box without root: read-only, always.
	KindSeedbox Kind = "seedbox"
)

const maxHosts = 32

var (
	nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	userRe = regexp.MustCompile(`^[a-z_][a-z0-9_.-]{0,31}$`)
	// RFC 1123 hostname labels; IPs are checked separately.
	hostRe = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)
)

// Host is one monitored machine.
type Host struct {
	Name    string `json:"name"`
	Kind    Kind   `json:"kind"`
	Address string `json:"address"`
	Port    int    `json:"port,omitempty"`
	User    string `json:"user"`
	// Docker enables container actions. Only valid on a VPS.
	Docker bool `json:"docker,omitempty"`
}

// Addr returns host:port for dialing.
func (h Host) Addr() string {
	return net.JoinHostPort(h.Address, strconv.Itoa(h.Port))
}

// Parse decodes and validates ARGOS_HOSTS. An empty string means no hosts.
func Parse(raw string) ([]Host, error) {
	if len(bytes.TrimSpace([]byte(raw))) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	var hosts []Host
	if err := dec.Decode(&hosts); err != nil {
		return nil, fmt.Errorf("ARGOS_HOSTS is not a valid JSON array of hosts: %w", err)
	}
	if dec.More() {
		return nil, errors.New("ARGOS_HOSTS: trailing data after the JSON array")
	}
	if len(hosts) > maxHosts {
		return nil, fmt.Errorf("ARGOS_HOSTS: %d hosts, at most %d", len(hosts), maxHosts)
	}
	seen := make(map[string]bool, len(hosts))
	for i := range hosts {
		h := &hosts[i]
		if h.Port == 0 {
			h.Port = 22
		}
		if err := h.validate(); err != nil {
			return nil, fmt.Errorf("ARGOS_HOSTS[%d] (%q): %w", i, h.Name, err)
		}
		if seen[h.Name] {
			return nil, fmt.Errorf("ARGOS_HOSTS: duplicate host name %q", h.Name)
		}
		seen[h.Name] = true
	}
	return hosts, nil
}

func (h Host) validate() error {
	if !nameRe.MatchString(h.Name) {
		return errors.New("name must be 1-32 chars of a-z, 0-9 and '-'")
	}
	switch h.Kind {
	case KindVPS:
	case KindSeedbox:
		if h.Docker {
			return errors.New("docker actions are not allowed on a seedbox")
		}
	default:
		return fmt.Errorf("kind must be %q or %q", KindVPS, KindSeedbox)
	}
	if net.ParseIP(h.Address) == nil && (len(h.Address) > 253 || !hostRe.MatchString(h.Address)) {
		return errors.New("address must be a hostname or an IP address")
	}
	if h.Port < 1 || h.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if !userRe.MatchString(h.User) {
		return errors.New("user must be a valid Unix user name")
	}
	return nil
}
