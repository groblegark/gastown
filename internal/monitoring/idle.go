package monitoring

import "time"

// Default idle detection thresholds.
const (
	DefaultIdleTimeout   = 2 * time.Minute  // No activity → idle
	DefaultStaleTimeout  = 5 * time.Minute  // Extended idle → stale
	DefaultStuckTimeout  = 15 * time.Minute // Very long idle → stuck
)

// IdleDetector tracks activity timestamps and detects idle agents.
type IdleDetector struct {
	idleTimeout  time.Duration
	staleTimeout time.Duration
	stuckTimeout time.Duration
}

// IdleDetectorOption configures an IdleDetector.
type IdleDetectorOption func(*IdleDetector)

// WithIdleTimeout sets the idle detection threshold.
func WithIdleTimeout(d time.Duration) IdleDetectorOption {
	return func(id *IdleDetector) { id.idleTimeout = d }
}

// WithStaleTimeout sets the stale detection threshold.
func WithStaleTimeout(d time.Duration) IdleDetectorOption {
	return func(id *IdleDetector) { id.staleTimeout = d }
}

// WithStuckTimeout sets the stuck detection threshold.
func WithStuckTimeout(d time.Duration) IdleDetectorOption {
	return func(id *IdleDetector) { id.stuckTimeout = d }
}

// NewIdleDetector creates an IdleDetector with the given options.
func NewIdleDetector(opts ...IdleDetectorOption) *IdleDetector {
	d := &IdleDetector{
		idleTimeout:  DefaultIdleTimeout,
		staleTimeout: DefaultStaleTimeout,
		stuckTimeout: DefaultStuckTimeout,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// IdleLevel represents the severity of idleness.
type IdleLevel int

const (
	IdleLevelActive IdleLevel = iota // Within idle timeout
	IdleLevelIdle                     // Past idle timeout
	IdleLevelStale                    // Past stale timeout
	IdleLevelStuck                    // Past stuck timeout
)

// String returns the human-readable idle level.
func (l IdleLevel) String() string {
	switch l {
	case IdleLevelActive:
		return "active"
	case IdleLevelIdle:
		return "idle"
	case IdleLevelStale:
		return "stale"
	case IdleLevelStuck:
		return "stuck"
	default:
		return "unknown"
	}
}

// Classify determines the idle level based on time since last activity.
func (d *IdleDetector) Classify(lastActivity time.Time) IdleLevel {
	if lastActivity.IsZero() {
		return IdleLevelStuck // No activity data at all
	}

	elapsed := time.Since(lastActivity)
	if elapsed < 0 {
		return IdleLevelActive // Future timestamp (clock skew)
	}

	switch {
	case elapsed >= d.stuckTimeout:
		return IdleLevelStuck
	case elapsed >= d.staleTimeout:
		return IdleLevelStale
	case elapsed >= d.idleTimeout:
		return IdleLevelIdle
	default:
		return IdleLevelActive
	}
}

// InferStatus returns the appropriate AgentStatus based on idle level.
func (d *IdleDetector) InferStatus(lastActivity time.Time) AgentStatus {
	switch d.Classify(lastActivity) {
	case IdleLevelActive:
		return StatusWorking
	case IdleLevelIdle:
		return StatusIdle
	case IdleLevelStale:
		return StatusIdle
	case IdleLevelStuck:
		return StatusError
	default:
		return StatusOffline
	}
}
