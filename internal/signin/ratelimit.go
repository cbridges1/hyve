package signin

import (
	"sync"
	"time"
)

// limiter blocks a key (a username or a client address) for Lockout after
// MaxFailures failed sign-ins within Window. A success clears the key.
type limiter struct {
	MaxFailures int
	Window      time.Duration
	Lockout     time.Duration
	Now         func() time.Time

	mu    sync.Mutex
	state map[string]*limitState
}

type limitState struct {
	failures     []time.Time
	blockedUntil time.Time
}

func newLimiter(maxFailures int, window, lockout time.Duration) *limiter {
	return &limiter{MaxFailures: maxFailures, Window: window, Lockout: lockout, state: map[string]*limitState{}}
}

func (l *limiter) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// Blocked reports whether key is locked out right now.
func (l *limiter) Blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.state[key]
	return ok && l.now().Before(st.blockedUntil)
}

// Fail records a failed attempt for key.
func (l *limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	st, ok := l.state[key]
	if !ok {
		st = &limitState{}
		l.state[key] = st
	}
	cutoff := now.Add(-l.Window)
	kept := st.failures[:0]
	for _, t := range st.failures {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	st.failures = append(kept, now)
	if len(st.failures) >= l.MaxFailures {
		st.blockedUntil = now.Add(l.Lockout)
		st.failures = nil
	}
	l.gc(now)
}

// Succeed clears key.
func (l *limiter) Succeed(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.state, key)
}

// gc drops keys with nothing left to remember. Called with mu held.
func (l *limiter) gc(now time.Time) {
	if len(l.state) < 10000 {
		return
	}
	cutoff := now.Add(-l.Window)
	for k, st := range l.state {
		if now.After(st.blockedUntil) && (len(st.failures) == 0 || st.failures[len(st.failures)-1].Before(cutoff)) {
			delete(l.state, k)
		}
	}
}
