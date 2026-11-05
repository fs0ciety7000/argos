package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

const (
	sessionTokenLen = 32
	// One user: a handful of devices at most. Oldest sessions are evicted.
	maxSessions = 16
)

type session struct {
	created  time.Time
	lastSeen time.Time
}

// Sessions is an in-memory session store. Tokens are kept only as SHA-256
// digests, so a memory dump does not yield usable cookies. A restart logs
// everyone out, which is acceptable for a single-user tool.
type Sessions struct {
	ttl  time.Duration // absolute lifetime
	idle time.Duration // inactivity timeout
	now  func() time.Time

	mu sync.Mutex
	m  map[[sha256.Size]byte]*session
}

// NewSessions returns a store with the given absolute and idle timeouts.
func NewSessions(ttl, idle time.Duration) *Sessions {
	return &Sessions{ttl: ttl, idle: idle, now: time.Now, m: make(map[[sha256.Size]byte]*session)}
}

// Create starts a session and returns its token and absolute expiry.
func (s *Sessions) Create() (string, time.Time, error) {
	raw := make([]byte, sessionTokenLen)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("read token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	if len(s.m) >= maxSessions {
		s.evictOldestLocked()
	}
	s.m[sha256.Sum256([]byte(token))] = &session{created: now, lastSeen: now}
	return token, now.Add(s.ttl), nil
}

// Validate reports whether token is a live session and refreshes its idle timer.
func (s *Sessions) Validate(token string) (expires time.Time, ok bool) {
	return s.check(token, true)
}

// Peek is Validate without refreshing the idle timer: a background
// connection (WebSocket) must not keep an abandoned session alive.
func (s *Sessions) Peek(token string) bool {
	_, ok := s.check(token, false)
	return ok
}

func (s *Sessions) check(token string, touch bool) (expires time.Time, ok bool) {
	if token == "" {
		return time.Time{}, false
	}
	key := sha256.Sum256([]byte(token))
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()
	sess, found := s.m[key]
	if !found {
		return time.Time{}, false
	}
	if s.expiredLocked(sess, now) {
		delete(s.m, key)
		return time.Time{}, false
	}
	if touch {
		sess.lastSeen = now
	}
	return sess.created.Add(s.ttl), true
}

// Delete ends the session for token, if any.
func (s *Sessions) Delete(token string) {
	key := sha256.Sum256([]byte(token))
	s.mu.Lock()
	delete(s.m, key)
	s.mu.Unlock()
}

func (s *Sessions) expiredLocked(sess *session, now time.Time) bool {
	return now.Sub(sess.created) >= s.ttl || now.Sub(sess.lastSeen) >= s.idle
}

func (s *Sessions) sweepLocked(now time.Time) {
	for k, sess := range s.m {
		if s.expiredLocked(sess, now) {
			delete(s.m, k)
		}
	}
}

func (s *Sessions) evictOldestLocked() {
	var oldestKey [sha256.Size]byte
	var oldest time.Time
	first := true
	for k, sess := range s.m {
		if first || sess.created.Before(oldest) {
			oldestKey, oldest, first = k, sess.created, false
		}
	}
	delete(s.m, oldestKey)
}
