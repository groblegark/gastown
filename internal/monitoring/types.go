// Package monitoring provides agent status tracking and inference.
//
// Agents can be in one of several states, determined by priority sources:
//  1. Boss override (Witness/Mayor explicitly sets status)
//  2. Self-reported (agent reports its own status)
//  3. Inferred (detected from activity timestamps and pane output)
package monitoring

import "time"

// AgentStatus represents the operational status of an agent.
type AgentStatus string

const (
	StatusAvailable AgentStatus = "available" // Ready for work assignment
	StatusWorking   AgentStatus = "working"   // Actively executing a task
	StatusThinking  AgentStatus = "thinking"  // Processing/reasoning (tool output detected)
	StatusBlocked   AgentStatus = "blocked"   // Waiting on external dependency
	StatusWaiting   AgentStatus = "waiting"   // Waiting for human input or decision
	StatusReviewing AgentStatus = "reviewing" // Reviewing code or output
	StatusIdle      AgentStatus = "idle"      // No activity for extended period
	StatusPaused    AgentStatus = "paused"    // Manually paused by operator
	StatusError     AgentStatus = "error"     // In error state, needs intervention
	StatusOffline   AgentStatus = "offline"   // Session not running
)

// IsHealthy returns true if the status indicates normal operation.
func (s AgentStatus) IsHealthy() bool {
	switch s {
	case StatusAvailable, StatusWorking, StatusThinking, StatusReviewing:
		return true
	default:
		return false
	}
}

// NeedsAttention returns true if the status may require operator intervention.
func (s AgentStatus) NeedsAttention() bool {
	switch s {
	case StatusBlocked, StatusError, StatusIdle:
		return true
	default:
		return false
	}
}

// StatusSource indicates how a status was determined, in priority order.
type StatusSource string

const (
	SourceBossOverride StatusSource = "boss"     // Witness/Mayor explicitly set
	SourceSelfReported StatusSource = "self"     // Agent reported its own status
	SourceInferred     StatusSource = "inferred" // Detected from activity/output
)

// StatusReport captures a point-in-time status for an agent.
type StatusReport struct {
	AgentID      string       `json:"agent_id"`
	Status       AgentStatus  `json:"status"`
	Source       StatusSource `json:"source"`
	Since        time.Time    `json:"since"`         // When this status began
	Message      string       `json:"message,omitempty"` // Optional context
	LastActivity time.Time    `json:"last_activity,omitempty"`
}

// Duration returns how long the agent has been in this status.
func (r StatusReport) Duration() time.Duration {
	if r.Since.IsZero() {
		return 0
	}
	return time.Since(r.Since)
}
