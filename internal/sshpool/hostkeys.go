package sshpool

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// HostKeyChangedError means a host presented a key different from the one
// recorded on first contact: possible man-in-the-middle, or a reinstalled
// server. Argos refuses to connect until the owner removes the old line.
type HostKeyChangedError struct {
	Host string
	Got  string // SHA256 fingerprint presented now
	Want []string
}

func (e *HostKeyChangedError) Error() string {
	return fmt.Sprintf("host key changed for %s: got %s, expected %v", e.Host, e.Got, e.Want)
}

// HostKeys implements trust-on-first-use over an OpenSSH known_hosts file.
type HostKeys struct {
	path string

	mu sync.Mutex
	cb ssh.HostKeyCallback
	// onLearn is called after a new host key is recorded.
	onLearn func(host, fingerprint string)
}

// NewHostKeys opens (creating if needed) the known_hosts file at path.
func NewHostKeys(path string, onLearn func(host, fingerprint string)) (*HostKeys, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	f.Close()
	k := &HostKeys{path: path, onLearn: onLearn}
	if err := k.reload(); err != nil {
		return nil, err
	}
	return k, nil
}

func (k *HostKeys) reload() error {
	cb, err := knownhosts.New(k.path)
	if err != nil {
		return fmt.Errorf("parse %s: %w", k.path, err)
	}
	k.cb = cb
	return nil
}

// Check verifies key for hostname ("host:port"). Unknown hosts are
// recorded; a mismatch returns *HostKeyChangedError.
func (k *HostKeys) Check(hostname string, remote net.Addr, key ssh.PublicKey) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	err := k.cb(hostname, remote, key)
	if err == nil {
		return nil
	}
	var ke *knownhosts.KeyError
	if !errors.As(err, &ke) {
		return err // e.g. revoked key
	}
	if len(ke.Want) > 0 {
		want := make([]string, 0, len(ke.Want))
		for _, w := range ke.Want {
			want = append(want, ssh.FingerprintSHA256(w.Key))
		}
		return &HostKeyChangedError{Host: hostname, Got: ssh.FingerprintSHA256(key), Want: want}
	}

	// First contact: trust and record.
	f, err := os.OpenFile(k.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("record host key: %w", err)
	}
	_, werr := fmt.Fprintln(f, knownhosts.Line([]string{hostname}, key))
	cerr := f.Close()
	if werr != nil || cerr != nil {
		return fmt.Errorf("record host key: %w", errors.Join(werr, cerr))
	}
	if err := k.reload(); err != nil {
		return err
	}
	if k.onLearn != nil {
		k.onLearn(hostname, ssh.FingerprintSHA256(key))
	}
	return nil
}
