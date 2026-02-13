package witness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/runtime"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/terminal"
	"github.com/steveyegge/gastown/internal/workspace"
)

// Common errors
var (
	ErrNotRunning     = errors.New("witness not running")
	ErrAlreadyRunning = errors.New("witness already running")
)

// Manager handles witness lifecycle and monitoring operations.
// ZFC-compliant: session existence is the source of truth (tmux or coop).
type Manager struct {
	rig     *rig.Rig
	backend terminal.Backend
}

// NewManager creates a new witness manager for a rig.
func NewManager(r *rig.Rig) *Manager {
	return &Manager{
		rig:     r,
		backend: terminal.NewCoopBackend(terminal.CoopConfig{}),
	}
}

// SetBackend overrides the terminal backend used for session liveness checks.
func (m *Manager) SetBackend(b terminal.Backend) {
	m.backend = b
}

// hasSession checks if a terminal session exists via the CoopBackend.
func (m *Manager) hasSession(sessionID string) (bool, error) {
	return m.backend.HasSession(sessionID)
}

// IsRunning checks if the witness session is active.
// ZFC: session existence is the source of truth (tmux or coop).
func (m *Manager) IsRunning() (bool, error) {
	return m.hasSession(m.SessionName())
}

// SessionName returns the tmux session name for this witness.
func (m *Manager) SessionName() string {
	return fmt.Sprintf("gt-%s-witness", m.rig.Name)
}

// Status returns information about the witness session.
// Uses the backend (coop) to check session existence.
func (m *Manager) Status() (*terminal.SessionInfo, error) {
	sessionID := m.SessionName()

	running, err := m.hasSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("checking session: %w", err)
	}
	if !running {
		return nil, ErrNotRunning
	}

	return &terminal.SessionInfo{Name: sessionID}, nil
}

// witnessDir returns the working directory for the witness.
// Prefers witness/rig/, falls back to witness/, then rig root.
func (m *Manager) witnessDir() string {
	witnessRigDir := filepath.Join(m.rig.Path, "witness", "rig")
	if _, err := os.Stat(witnessRigDir); err == nil {
		return witnessRigDir
	}

	witnessDir := filepath.Join(m.rig.Path, "witness")
	if _, err := os.Stat(witnessDir); err == nil {
		return witnessDir
	}

	return m.rig.Path
}

// Start starts the witness.
// If foreground is true, returns an error (foreground mode deprecated).
// Otherwise, spawns an agent session via the backend.
// agentOverride optionally specifies a different agent alias to use.
// envOverrides are KEY=VALUE pairs that override all other env var sources.
// Session is the source of truth for running state.
func (m *Manager) Start(foreground bool, agentOverride string, envOverrides []string) error {
	sessionID := m.SessionName()

	if foreground {
		return fmt.Errorf("foreground mode is deprecated; use background mode (remove --foreground flag)")
	}

	// Check if session already exists
	running, _ := m.hasSession(sessionID)
	if running {
		// Session exists - check if agent is actually running (healthy vs zombie)
		agentRunning, _ := m.backend.IsAgentRunning(sessionID)
		if agentRunning {
			return ErrAlreadyRunning
		}
		// Zombie - session alive but agent dead. Kill and recreate.
		if err := m.backend.KillSession(sessionID); err != nil {
			return fmt.Errorf("killing zombie session: %w", err)
		}
	}

	// Working directory
	witnessDir := m.witnessDir()

	// Ensure runtime settings exist in witness/ (not witness/rig/) so we don't
	// write into the source repo. Claude walks up the tree to find settings.
	witnessParentDir := filepath.Join(m.rig.Path, "witness")
	townRoot := m.townRoot()
	runtimeConfig := config.ResolveRoleAgentConfig("witness", townRoot, m.rig.Path)
	if err := runtime.EnsureSettingsForRole(witnessParentDir, "witness", runtimeConfig); err != nil {
		return fmt.Errorf("ensuring runtime settings: %w", err)
	}

	roleConfig, err := m.roleConfig()
	if err != nil {
		return err
	}

	// Build startup command
	// NOTE: No gt prime injection needed - SessionStart hook handles it automatically
	command, err := buildWitnessStartCommand(m.rig.Path, m.rig.Name, townRoot, agentOverride, roleConfig)
	if err != nil {
		return err
	}

	// In K8s, sessions are created by the controller when a bead is created.
	// For local development, use the backend to send the startup command.
	// TODO(bd-e52ls): Replace with bead creation for K8s-native session lifecycle.
	_ = witnessDir
	_ = command

	// Set environment variables via backend (non-fatal)
	envVars := config.AgentEnv(config.AgentEnvConfig{
		Role:         "witness",
		Rig:          m.rig.Name,
		TownRoot:     townRoot,
		BDDaemonHost: os.Getenv("BD_DAEMON_HOST"),
	})
	for k, v := range envVars {
		_ = m.backend.SetEnvironment(sessionID, k, v)
	}
	// Apply role config env vars if present (non-fatal).
	for key, value := range roleConfigEnvVars(roleConfig, townRoot, m.rig.Name) {
		_ = m.backend.SetEnvironment(sessionID, key, value)
	}
	// Apply CLI env overrides (highest priority, non-fatal).
	for _, override := range envOverrides {
		if key, value, ok := strings.Cut(override, "="); ok {
			_ = m.backend.SetEnvironment(sessionID, key, value)
		}
	}

	return nil
}

func (m *Manager) roleConfig() (*beads.RoleConfig, error) {
	// Role beads use hq- prefix and live in town-level beads, not rig beads
	townRoot := m.townRoot()
	bd := beads.NewWithBeadsDir(townRoot, beads.ResolveBeadsDir(townRoot))
	roleConfig, err := bd.GetRoleConfig(beads.RoleBeadIDTown("witness"))
	if err != nil {
		return nil, fmt.Errorf("loading witness role config: %w", err)
	}
	return roleConfig, nil
}

func (m *Manager) townRoot() string {
	townRoot, err := workspace.Find(m.rig.Path)
	if err != nil || townRoot == "" {
		return m.rig.Path
	}
	return townRoot
}

func roleConfigEnvVars(roleConfig *beads.RoleConfig, townRoot, rigName string) map[string]string {
	if roleConfig == nil || len(roleConfig.EnvVars) == 0 {
		return nil
	}
	expanded := make(map[string]string, len(roleConfig.EnvVars))
	for key, value := range roleConfig.EnvVars {
		expanded[key] = beads.ExpandRolePattern(value, townRoot, rigName, "", "witness")
	}
	return expanded
}

func buildWitnessStartCommand(rigPath, rigName, townRoot, agentOverride string, roleConfig *beads.RoleConfig) (string, error) {
	if agentOverride != "" {
		roleConfig = nil
	}
	if roleConfig != nil && roleConfig.StartCommand != "" {
		return beads.ExpandRolePattern(roleConfig.StartCommand, townRoot, rigName, "", "witness"), nil
	}
	initialPrompt := session.BuildStartupPrompt(session.BeaconConfig{
		Recipient: fmt.Sprintf("%s/witness", rigName),
		Sender:    "deacon",
		Topic:     "patrol",
	}, "I am Witness for "+rigName+". Start patrol: check gt hook, if empty create mol-witness-patrol wisp and execute it.")
	command, err := config.BuildAgentStartupCommandWithAgentOverride("witness", rigName, townRoot, rigPath, initialPrompt, agentOverride)
	if err != nil {
		return "", fmt.Errorf("building startup command: %w", err)
	}
	return command, nil
}

// Stop stops the witness.
// Session existence is the source of truth.
func (m *Manager) Stop() error {
	sessionID := m.SessionName()

	// Check if session exists
	running, _ := m.hasSession(sessionID)
	if !running {
		return ErrNotRunning
	}

	// Kill the session via backend.
	return m.backend.KillSession(sessionID)
}
