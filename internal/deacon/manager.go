package deacon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/runtime"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/terminal"
	"github.com/steveyegge/gastown/internal/tmux"
)

// Common errors
var (
	ErrNotRunning     = errors.New("deacon not running")
	ErrAlreadyRunning = errors.New("deacon already running")
)

// Manager handles deacon lifecycle operations.
type Manager struct {
	townRoot string
	backend  terminal.Backend
}

// NewManager creates a new deacon manager for a town.
func NewManager(townRoot string) *Manager {
	return &Manager{
		townRoot: townRoot,
		backend:  terminal.NewCoopBackend(terminal.CoopConfig{}),
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

// SessionName returns the tmux session name for the deacon.
// This is a package-level function for convenience.
func SessionName() string {
	return session.DeaconSessionName()
}

// SessionName returns the tmux session name for the deacon.
func (m *Manager) SessionName() string {
	return SessionName()
}

// deaconDir returns the working directory for the deacon.
func (m *Manager) deaconDir() string {
	return filepath.Join(m.townRoot, "deacon")
}

// Start starts the deacon session.
// agentOverride allows specifying an alternate agent alias (e.g., for testing).
// Restarts are handled by daemon via ensureDeaconRunning on each heartbeat.
func (m *Manager) Start(agentOverride string) error {
	sessionID := m.SessionName()

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

	// Ensure deacon directory exists
	deaconDir := m.deaconDir()
	if err := os.MkdirAll(deaconDir, 0755); err != nil {
		return fmt.Errorf("creating deacon directory: %w", err)
	}

	// Ensure runtime settings exist in deaconDir where session runs.
	runtimeConfig := config.ResolveRoleAgentConfig("deacon", m.townRoot, deaconDir)
	if err := runtime.EnsureSettingsForRole(deaconDir, "deacon", runtimeConfig); err != nil {
		return fmt.Errorf("ensuring runtime settings: %w", err)
	}

	initialPrompt := session.BuildStartupPrompt(session.BeaconConfig{
		Recipient: "deacon",
		Sender:    "daemon",
		Topic:     "patrol",
	}, "I am Deacon. Start patrol: check gt hook, if empty create mol-deacon-patrol wisp and execute it.")
	startupCmd, err := config.BuildAgentStartupCommandWithAgentOverride("deacon", "", m.townRoot, "", initialPrompt, agentOverride)
	if err != nil {
		return fmt.Errorf("building startup command: %w", err)
	}

	// In K8s, sessions are created by the controller when a bead is created.
	// For local development, the backend manages session lifecycle.
	// TODO(bd-e52ls): Replace with bead creation for K8s-native session lifecycle.
	_ = deaconDir
	_ = startupCmd
	_ = runtimeConfig

	// Set environment variables via backend (non-fatal)
	envVars := config.AgentEnv(config.AgentEnvConfig{
		Role:         "deacon",
		TownRoot:     m.townRoot,
		BDDaemonHost: os.Getenv("BD_DAEMON_HOST"),
	})
	for k, v := range envVars {
		_ = m.backend.SetEnvironment(sessionID, k, v)
	}

	return nil
}

// Stop stops the deacon session.
func (m *Manager) Stop() error {
	sessionID := m.SessionName()

	// Check if session exists
	running, err := m.hasSession(sessionID)
	if err != nil {
		return fmt.Errorf("checking session: %w", err)
	}
	if !running {
		return ErrNotRunning
	}

	// Kill the session via backend.
	if err := m.backend.KillSession(sessionID); err != nil {
		return fmt.Errorf("killing session: %w", err)
	}

	return nil
}

// IsRunning checks if the deacon session is active.
func (m *Manager) IsRunning() (bool, error) {
	return m.hasSession(m.SessionName())
}

// Status returns information about the deacon session.
func (m *Manager) Status() (*tmux.SessionInfo, error) {
	sessionID := m.SessionName()

	running, err := m.hasSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("checking session: %w", err)
	}
	if !running {
		return nil, ErrNotRunning
	}

	return &tmux.SessionInfo{Name: sessionID}, nil
}
