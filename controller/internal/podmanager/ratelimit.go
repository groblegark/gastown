package podmanager

import (
	"sync"
	"time"
)

// SidecarRateLimiter tracks sidecar change frequency per agent bead.
// It enforces a maximum number of changes per rolling hour window.
type SidecarRateLimiter struct {
	mu         sync.Mutex
	maxPerHour int
	window     time.Duration
	changes    map[string][]time.Time
}

// NewSidecarRateLimiter creates a rate limiter with the given max changes per hour.
// A maxPerHour of 0 disables rate limiting.
func NewSidecarRateLimiter(maxPerHour int) *SidecarRateLimiter {
	return &SidecarRateLimiter{
		maxPerHour: maxPerHour,
		window:     time.Hour,
		changes:    make(map[string][]time.Time),
	}
}

// Allow checks whether a sidecar change for the given bead ID is within the rate limit.
// Returns true if the change is allowed. If allowed, records the change timestamp.
func (r *SidecarRateLimiter) Allow(beadID string) bool {
	if r.maxPerHour <= 0 {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	r.pruneExpired(beadID, now)

	if len(r.changes[beadID]) >= r.maxPerHour {
		return false
	}

	r.changes[beadID] = append(r.changes[beadID], now)
	return true
}

// Count returns the number of recent changes for the given bead ID within the window.
func (r *SidecarRateLimiter) Count(beadID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneExpired(beadID, time.Now())
	return len(r.changes[beadID])
}

// pruneExpired removes timestamps older than the window for a given bead ID.
// Must be called with r.mu held.
func (r *SidecarRateLimiter) pruneExpired(beadID string, now time.Time) {
	cutoff := now.Add(-r.window)
	timestamps := r.changes[beadID]
	valid := timestamps[:0]
	for _, ts := range timestamps {
		if ts.After(cutoff) {
			valid = append(valid, ts)
		}
	}
	if len(valid) == 0 {
		delete(r.changes, beadID)
	} else {
		r.changes[beadID] = valid
	}
}
