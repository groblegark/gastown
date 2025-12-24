// Package witness provides the polecat monitoring agent.
package witness

import (
	"time"
)

// State represents the witness's running state.
type State string

const (
	// StateStopped means the witness is not running.
	StateStopped State = "stopped"

	// StateRunning means the witness is actively monitoring.
	StateRunning State = "running"

	// StatePaused means the witness is paused (not monitoring).
	StatePaused State = "paused"
)

// Witness represents a rig's polecat monitoring agent.
type Witness struct {
	// RigName is the rig this witness monitors.
	RigName string `json:"rig_name"`

	// State is the current running state.
	State State `json:"state"`

	// PID is the process ID if running in background.
	PID int `json:"pid,omitempty"`

	// StartedAt is when the witness was started.
	StartedAt *time.Time `json:"started_at,omitempty"`

	// MonitoredPolecats tracks polecats being monitored.
	MonitoredPolecats []string `json:"monitored_polecats,omitempty"`

	// LastCheckAt is when the last health check was performed.
	LastCheckAt *time.Time `json:"last_check_at,omitempty"`

	// Stats contains cumulative statistics.
	Stats WitnessStats `json:"stats"`

	// Config contains auto-spawn configuration.
	Config WitnessConfig `json:"config"`

	// SpawnedIssues tracks which issues have been spawned (to avoid duplicates).
	SpawnedIssues []string `json:"spawned_issues,omitempty"`
}

// WitnessConfig contains configuration for the witness.
type WitnessConfig struct {
	// MaxWorkers is the maximum number of concurrent polecats (default: 4).
	MaxWorkers int `json:"max_workers"`

	// SpawnDelayMs is the delay between spawns in milliseconds (default: 5000).
	SpawnDelayMs int `json:"spawn_delay_ms"`

	// AutoSpawn enables automatic spawning for ready issues (default: true).
	AutoSpawn bool `json:"auto_spawn"`

	// EpicID limits spawning to children of this epic (optional).
	EpicID string `json:"epic_id,omitempty"`

	// IssuePrefix limits spawning to issues with this prefix (optional).
	IssuePrefix string `json:"issue_prefix,omitempty"`
}

// WitnessStats contains cumulative witness statistics.
type WitnessStats struct {
	// TotalChecks is the total number of health checks performed.
	TotalChecks int `json:"total_checks"`

	// TotalNudges is the total number of nudges sent to polecats.
	TotalNudges int `json:"total_nudges"`

	// TotalEscalations is the total number of escalations to mayor.
	TotalEscalations int `json:"total_escalations"`

	// TodayChecks is the number of checks today.
	TodayChecks int `json:"today_checks"`

	// TodayNudges is the number of nudges today.
	TodayNudges int `json:"today_nudges"`
}

// WorkerState tracks the state of a single worker (polecat) across wisp burns.
type WorkerState struct {
	// Issue is the current issue the worker is assigned to.
	Issue string `json:"issue,omitempty"`

	// ArmID is the mol-polecat-arm instance tracking this worker.
	ArmID string `json:"arm_id,omitempty"`

	// NudgeCount is how many times this worker has been nudged.
	NudgeCount int `json:"nudge_count"`

	// LastNudge is when the worker was last nudged.
	LastNudge *time.Time `json:"last_nudge,omitempty"`

	// LastActive is when the worker was last seen active.
	LastActive *time.Time `json:"last_active,omitempty"`
}

// WitnessHandoffState tracks all worker states across wisp burns.
// This is persisted in a pinned handoff bead that survives wisp burns.
type WitnessHandoffState struct {
	// WorkerStates maps polecat names to their state.
	WorkerStates map[string]WorkerState `json:"worker_states"`

	// PatrolInstanceID is the mol-witness-patrol instance tracking this patrol.
	PatrolInstanceID string `json:"patrol_instance_id,omitempty"`

	// LastPatrol is when the last patrol cycle completed.
	LastPatrol *time.Time `json:"last_patrol,omitempty"`
}

// HandoffBeadID is the well-known ID suffix for the witness handoff bead.
// The full ID is constructed as "<rig>-witness-state" (e.g., "gastown-witness-state").
const HandoffBeadID = "witness-state"
