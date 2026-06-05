package runtime

import (
	"sync"
	"time"
)

// RateLimiter is a per-API-key fixed-window limiter (requests per minute). It is in-memory
// and per-process: correct for a single instance, which is the current deployment shape. For
// a multi-replica deployment, back it with a shared store (e.g. Redis) so the budget is
// enforced across instances — the call sites here would not change.
//
// A non-positive rpm means "unlimited" (the limiter is a no-op for that key), so keys created
// without an explicit budget, and local/demo keys, are never throttled.
type RateLimiter struct {
	mu      sync.Mutex
	windows map[int64]*rlWindow
}

type rlWindow struct {
	count   int
	resetAt time.Time
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{windows: map[int64]*rlWindow{}}
}

// Allow reports whether a request for the given API key id is within its per-minute budget.
// When the budget is exhausted it returns false plus the duration until the window resets
// (suitable for a Retry-After header). A nil receiver or rpm <= 0 always allows.
func (r *RateLimiter) Allow(keyID int64, rpm int) (bool, time.Duration) {
	if r == nil || rpm <= 0 {
		return true, 0
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.windows[keyID]
	if !ok || now.After(w.resetAt) {
		r.windows[keyID] = &rlWindow{count: 1, resetAt: now.Add(time.Minute)}
		return true, 0
	}
	if w.count >= rpm {
		return false, time.Until(w.resetAt)
	}
	w.count++
	return true, 0
}
