package auth

import (
	"sync"
	"time"
)

// FailureLimiter blocks logins after too many failures within a sliding
// window. It is global on purpose: behind proxies the client IP is easy to
// spoof, and there is only one account to protect.
type FailureLimiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu       sync.Mutex
	failures []time.Time
}

// NewFailureLimiter allows at most max failures per window.
func NewFailureLimiter(max int, window time.Duration) *FailureLimiter {
	return &FailureLimiter{max: max, window: window, now: time.Now}
}

// Allow reports whether a login attempt may proceed and, if not, how long
// until the oldest failure leaves the window.
func (l *FailureLimiter) Allow() (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
	if len(l.failures) < l.max {
		return true, 0
	}
	return false, l.failures[0].Add(l.window).Sub(now)
}

// Fail records a failed attempt.
func (l *FailureLimiter) Fail() {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
	l.failures = append(l.failures, now)
}

// Reset clears recorded failures after a successful login.
func (l *FailureLimiter) Reset() {
	l.mu.Lock()
	l.failures = l.failures[:0]
	l.mu.Unlock()
}

func (l *FailureLimiter) pruneLocked(now time.Time) {
	cut := 0
	for cut < len(l.failures) && now.Sub(l.failures[cut]) >= l.window {
		cut++
	}
	l.failures = append(l.failures[:0], l.failures[cut:]...)
}
