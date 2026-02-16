package monitoring

import (
	"sync"
	"time"
)

// agentState holds the internal tracking state for a single agent.
type agentState struct {
	bossOverride  *StatusReport // nil if no override
	selfReported  *StatusReport // nil if not self-reported
	lastActivity  time.Time
	lastOutput    string      // most recent output for pattern detection
	patternStatus AgentStatus // last detected pattern status
}

// Tracker manages per-agent status tracking with thread-safe access.
// Status is resolved by priority: boss override > self-reported > inferred.
type Tracker struct {
	mu       sync.RWMutex
	agents   map[string]*agentState
	patterns *PatternRegistry
	idle     *IdleDetector
}

// TrackerOption configures a Tracker.
type TrackerOption func(*Tracker)

// WithPatternRegistry sets a custom PatternRegistry for the Tracker.
func WithPatternRegistry(r *PatternRegistry) TrackerOption {
	return func(t *Tracker) { t.patterns = r }
}

// WithIdleDetector sets a custom IdleDetector for the Tracker.
func WithIdleDetector(d *IdleDetector) TrackerOption {
	return func(t *Tracker) { t.idle = d }
}

// NewTracker creates a Tracker with the given options.
// Defaults to NewPatternRegistry() and NewIdleDetector() if not overridden.
func NewTracker(opts ...TrackerOption) *Tracker {
	t := &Tracker{
		agents: make(map[string]*agentState),
	}
	for _, opt := range opts {
		opt(t)
	}
	if t.patterns == nil {
		t.patterns = NewPatternRegistry()
	}
	if t.idle == nil {
		t.idle = NewIdleDetector()
	}
	return t
}

// getOrCreate returns the agent state, creating it if absent.
// Caller must hold t.mu for writing.
func (t *Tracker) getOrCreate(agentID string) *agentState {
	s, ok := t.agents[agentID]
	if !ok {
		s = &agentState{}
		t.agents[agentID] = s
	}
	return s
}

// UpdateActivity records agent output, runs pattern detection, and updates
// the last activity timestamp.
func (t *Tracker) UpdateActivity(agentID string, output string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	s := t.getOrCreate(agentID)
	s.lastActivity = time.Now()
	s.lastOutput = output

	if detected := t.patterns.Detect(output); detected != "" {
		s.patternStatus = detected
	}
}

// SetStatus sets an agent's status from the given source.
func (t *Tracker) SetStatus(agentID string, status AgentStatus, source StatusSource, message string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	s := t.getOrCreate(agentID)
	now := time.Now()

	report := &StatusReport{
		AgentID:      agentID,
		Status:       status,
		Source:       source,
		Since:        now,
		Message:      message,
		LastActivity: s.lastActivity,
	}

	switch source {
	case SourceBossOverride:
		s.bossOverride = report
	case SourceSelfReported:
		s.selfReported = report
	case SourceInferred:
		// Inferred status is computed on read, not stored via SetStatus.
		// Allow callers to set it explicitly if they want to force a value.
		s.patternStatus = status
	}
}

// GetStatus returns the current effective status for an agent, resolved by
// priority: boss override > self-reported > inferred.
// Returns a zero-value StatusReport with StatusOffline if the agent is unknown.
func (t *Tracker) GetStatus(agentID string) StatusReport {
	t.mu.RLock()
	defer t.mu.RUnlock()

	s, ok := t.agents[agentID]
	if !ok {
		return StatusReport{
			AgentID: agentID,
			Status:  StatusOffline,
			Source:  SourceInferred,
		}
	}

	// Priority 1: boss override
	if s.bossOverride != nil {
		r := *s.bossOverride
		r.LastActivity = s.lastActivity
		return r
	}

	// Priority 2: self-reported
	if s.selfReported != nil {
		r := *s.selfReported
		r.LastActivity = s.lastActivity
		return r
	}

	// Priority 3: inferred from idle detection + pattern detection
	return t.inferStatus(agentID, s)
}

// inferStatus builds an inferred StatusReport from idle level and pattern
// detection. Caller must hold t.mu for reading.
func (t *Tracker) inferStatus(agentID string, s *agentState) StatusReport {
	idleLevel := t.idle.Classify(s.lastActivity)

	var status AgentStatus
	var message string

	switch idleLevel {
	case IdleLevelActive:
		// Active — use pattern detection result if available.
		if s.patternStatus != "" {
			status = s.patternStatus
		} else if !s.lastActivity.IsZero() {
			status = StatusWorking
		} else {
			status = StatusOffline
		}
	default:
		// Idle, stale, or stuck — use the idle detector's inference.
		status = t.idle.InferStatus(s.lastActivity)
		message = "idle level: " + idleLevel.String()
	}

	return StatusReport{
		AgentID:      agentID,
		Status:       status,
		Source:       SourceInferred,
		Since:        s.lastActivity,
		Message:      message,
		LastActivity: s.lastActivity,
	}
}

// ClearOverride removes the boss override for an agent, falling back to
// self-reported or inferred status.
func (t *Tracker) ClearOverride(agentID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if s, ok := t.agents[agentID]; ok {
		s.bossOverride = nil
	}
}

// AllStatuses returns the current effective status for every tracked agent.
func (t *Tracker) AllStatuses() []StatusReport {
	t.mu.RLock()
	defer t.mu.RUnlock()

	reports := make([]StatusReport, 0, len(t.agents))
	for agentID, s := range t.agents {
		var r StatusReport

		if s.bossOverride != nil {
			r = *s.bossOverride
			r.LastActivity = s.lastActivity
		} else if s.selfReported != nil {
			r = *s.selfReported
			r.LastActivity = s.lastActivity
		} else {
			r = t.inferStatus(agentID, s)
		}

		reports = append(reports, r)
	}
	return reports
}

// RemoveAgent stops tracking the given agent entirely.
func (t *Tracker) RemoveAgent(agentID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.agents, agentID)
}
