package mayor

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
	ErrNotRunning     = errors.New("mayor not running")
	ErrAlreadyRunning = errors.New("mayor already running")
)

// Manager handles mayor lifecycle operations.
type Manager struct {
	townRoot string
	backend  terminal.Backend
}

// NewManager creates a new mayor manager for a town.
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

// SessionName returns the tmux session name for the mayor.
// This is a package-level function for convenience.
func SessionName() string {
	return session.MayorSessionName()
}

// SessionName returns the tmux session name for the mayor.
func (m *Manager) SessionName() string {
	return SessionName()
}

// mayorDir returns the working directory for the mayor.
func (m *Manager) mayorDir() string {
	return filepath.Join(m.townRoot, "mayor")
}

// Start starts the mayor session.
// agentOverride optionally specifies a different agent alias to use.
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

	// Ensure mayor directory exists (for Claude settings)
	mayorDir := m.mayorDir()
	if err := os.MkdirAll(mayorDir, 0755); err != nil {
		return fmt.Errorf("creating mayor directory: %w", err)
	}

	// Ensure runtime settings exist
	runtimeConfig := config.ResolveRoleAgentConfig("mayor", m.townRoot, mayorDir)
	if err := runtime.EnsureSettingsForRole(mayorDir, "mayor", runtimeConfig); err != nil {
		return fmt.Errorf("ensuring runtime settings: %w", err)
	}

	// Symlink settings.json from town root to mayor's settings.
	if err := m.ensureTownRootSettingsSymlink(); err != nil {
		return fmt.Errorf("symlinking mayor settings to town root: %w", err)
	}

	// Build startup beacon
	beacon := session.FormatStartupBeacon(session.BeaconConfig{
		Recipient: "mayor",
		Sender:    "human",
		Topic:     "cold-start",
	})

	// Build startup command
	startupCmd, err := config.BuildAgentStartupCommandWithAgentOverride("mayor", "", m.townRoot, "", beacon, agentOverride)
	if err != nil {
		return fmt.Errorf("building startup command: %w", err)
	}

	// In K8s, sessions are created by the controller when a bead is created.
	// For local development, the backend manages session lifecycle.
	// TODO(bd-e52ls): Replace with bead creation for K8s-native session lifecycle.
	_ = mayorDir
	_ = startupCmd
	_ = runtimeConfig

	// Set environment variables via backend (non-fatal)
	envVars := config.AgentEnv(config.AgentEnvConfig{
		Role:         "mayor",
		TownRoot:     m.townRoot,
		BDDaemonHost: os.Getenv("BD_DAEMON_HOST"),
	})
	for k, v := range envVars {
		_ = m.backend.SetEnvironment(sessionID, k, v)
	}

	return nil
}

// ensureTownRootSettingsSymlink creates a symlink from townRoot/.claude/settings.json
// to mayor/.claude/settings.json. The mayor session runs from townRoot (not mayorDir)
// per issue #280, but Claude Code only looks in cwd for .claude/settings.json.
// The town root .claude/ directory contains other content (commands/, skills/,
// settings.local.json), so we symlink just the settings.json file rather than
// the whole directory.
func (m *Manager) ensureTownRootSettingsSymlink() error {
	townClaudeDir := filepath.Join(m.townRoot, ".claude")
	symlinkPath := filepath.Join(townClaudeDir, "settings.json")
	mayorSettings := filepath.Join(m.mayorDir(), ".claude", "settings.json")

	// Ensure town root .claude/ directory exists
	if err := os.MkdirAll(townClaudeDir, 0755); err != nil {
		return fmt.Errorf("creating town root .claude dir: %w", err)
	}

	// Check if something already exists at the symlink path
	if info, err := os.Lstat(symlinkPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			// Already a symlink - check if it points to the right place
			target, err := os.Readlink(symlinkPath)
			if err == nil {
				expectedTarget := filepath.Join("..", "mayor", ".claude", "settings.json")
				if target == expectedTarget {
					return nil // Already correctly set up
				}
			}
			// Wrong target - remove and recreate
			if err := os.Remove(symlinkPath); err != nil {
				return fmt.Errorf("removing stale settings.json symlink: %w", err)
			}
		} else {
			// Regular file - back it up and replace with symlink
			backupPath := symlinkPath + ".bak"
			if err := os.Rename(symlinkPath, backupPath); err != nil {
				return fmt.Errorf("backing up existing settings.json: %w", err)
			}
		}
	}

	// Verify the mayor settings file exists
	if _, err := os.Stat(mayorSettings); os.IsNotExist(err) {
		return fmt.Errorf("mayor settings not found at %s", mayorSettings)
	}

	// Create relative symlink: ../mayor/.claude/settings.json
	relTarget := filepath.Join("..", "mayor", ".claude", "settings.json")
	if err := os.Symlink(relTarget, symlinkPath); err != nil {
		return fmt.Errorf("creating settings.json symlink: %w", err)
	}

	return nil
}

// Stop stops the mayor session.
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

// IsRunning checks if the mayor session is active.
func (m *Manager) IsRunning() (bool, error) {
	return m.hasSession(m.SessionName())
}

// Status returns information about the mayor session.
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
