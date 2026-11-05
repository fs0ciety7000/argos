package sshpool

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

const keyFile = "id_ed25519"

// LoadOrCreateKey returns Argos' SSH identity from dir, generating an
// ed25519 key on first start. The private key never leaves the server:
// only the public half is shown to the owner.
func LoadOrCreateKey(dir string) (ssh.Signer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("chmod %s: %w", dir, err)
	}
	path := filepath.Join(dir, keyFile)

	info, err := os.Lstat(path)
	switch {
	case err == nil:
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s is not a regular file", path)
		}
		if info.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("%s is readable by group/others (mode %o); chmod 600 it", path, info.Mode().Perm())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		signer, err := ssh.ParsePrivateKey(data)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		return signer, nil
	case !errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "argos")
	if err != nil {
		return nil, fmt.Errorf("marshal key: %w", err)
	}
	// Write to a temp file, fsync, then rename: a crash never leaves an
	// empty key that would block every later start.
	tmp, err := os.CreateTemp(dir, ".id_ed25519-*")
	if err != nil {
		return nil, fmt.Errorf("create key: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := pem.Encode(tmp, block); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("write key: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return nil, fmt.Errorf("install key: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, err
	}
	// Informational copy of the public key; never follow a planted symlink.
	pub := path + ".pub"
	_ = os.Remove(pub)
	if f, err := os.OpenFile(pub, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644); err == nil {
		_, _ = f.WriteString(AuthorizedKey(signer.PublicKey()) + "\n")
		_ = f.Close()
	}
	return signer, nil
}

// AuthorizedKey formats a public key as an authorized_keys line. The
// "restrict" option disables port/agent/X11 forwarding and PTYs: plain exec
// (all Argos needs) keeps working, and a leaked key cannot open tunnels.
func AuthorizedKey(pub ssh.PublicKey) string {
	return "restrict " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))) + " argos"
}
