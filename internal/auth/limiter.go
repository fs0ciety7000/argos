package auth

import (
	"sync"
	"time"
)

// FailureLimiter blocks logins after too many failures within a sliding
// window. It is global on purpose: behind proxies the client IP is easy to
// spoof, and there is only one account to protect.
//
// Each attempt reserves a slot up front (counted as a failure) and a
// success clears all slots. Reserving before the expensive password check
// means concurrent requests cannot slip past the limit.
type FailureLimiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu       sync.Mutex
	failures []time.Time
}

// NewFailureLimiter allows at most max failed attempts per window.
func NewFailureLimiter(max int, window time.Duration) *FailureLimiter {
	return &FailureLimiter{max: max, window: window, now: time.Now}
}

// Reserve records an attempt if the budget allows it. When it does not,
// it returns false and how long until the oldest attempt leaves the window.
func (l *FailureLimiter) Reserve() (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
	if len(l.failures) >= l.max {
		return false, l.failures[0].Add(l.window).Sub(now)
	}
	l.failures = append(l.failures, now)
	return true, 0
}

// Reset clears recorded attempts after a successful login.
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
