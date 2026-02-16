// Package dog provides dog session management for Deacon's helper workers.
package dog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/claude"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/terminal"
	"github.com/steveyegge/gastown/internal/workspace"
)

// Session errors
var (
	ErrSessionRunning  = errors.New("session already running")
	ErrSessionNotFound = errors.New("session not found")
)

// SessionManager handles dog session lifecycle.
type SessionManager struct {
	backend  terminal.Backend
	townRoot string
	townName string
}

// NewSessionManager creates a new dog session manager.
func NewSessionManager(backend terminal.Backend, townRoot string) *SessionManager {
	if backend == nil {
		backend = terminal.NewCoopBackend(terminal.CoopConfig{})
	}
	townName, _ := workspace.GetTownName(townRoot)
	return &SessionManager{
		backend:  backend,
		townRoot: townRoot,
		townName: townName,
	}
}

// SetBackend overrides the terminal backend used for session liveness checks.
func (m *SessionManager) SetBackend(b terminal.Backend) {
	m.backend = b
}

// hasSession checks if a terminal session exists via the CoopBackend.
func (m *SessionManager) hasSession(sessionID string) (bool, error) {
	return m.backend.HasSession(sessionID)
}

// SessionStartOptions configures dog session startup.
type SessionStartOptions struct {
	// WorkDesc is the work description (formula or bead ID) for the startup prompt.
	WorkDesc string

	// AgentOverride specifies an alternate agent (e.g., "gemini", "claude-haiku").
	AgentOverride string
}

// SessionInfo contains information about a running dog session.
type SessionInfo struct {
	// DogName is the dog name.
	DogName string `json:"dog_name"`

	// SessionID is the session identifier.
	SessionID string `json:"session_id"`

	// Running indicates if the session is currently active.
	Running bool `json:"running"`

	// Attached indicates if someone is attached to the session.
	Attached bool `json:"attached,omitempty"`

	// Created is when the session was created.
	Created time.Time `json:"created,omitempty"`
}

// SessionName generates the session name for a dog.
// Pattern: gt-{town}-deacon-{name}
func (m *SessionManager) SessionName(dogName string) string {
	return fmt.Sprintf("gt-%s-deacon-%s", m.townName, dogName)
}

// kennelPath returns the path to the dog's kennel directory.
func (m *SessionManager) kennelPath(dogName string) string {
	return filepath.Join(m.townRoot, "deacon", "dogs", dogName)
}

// Start creates and starts a new session for a dog.
// Dogs run Claude sessions that check mail for work and execute formulas.
// When running in K8s (detected via KUBERNETES_SERVICE_HOST), creates an
// agent bead with K8s labels instead of a session.
func (m *SessionManager) Start(dogName string, opts SessionStartOptions) error {
	kennelDir := m.kennelPath(dogName)
	if _, err := os.Stat(kennelDir); os.IsNotExist(err) {
		return fmt.Errorf("%w: %s", ErrDogNotFound, dogName)
	}

	// Check execution target — dogs are town-level (no rig path)
	execTarget := config.ResolveExecutionTarget("", "")
	if execTarget == config.ExecutionTargetK8s {
		return m.startK8s(dogName, opts)
	}

	sessionID := m.SessionName(dogName)

	// Check if session already exists
	running, err := m.hasSession(sessionID)
	if err != nil {
		return fmt.Errorf("checking session: %w", err)
	}
	if running {
		// Session exists - check if Claude is actually running
		agentRunning, runErr := m.backend.IsAgentRunning(sessionID)
		if runErr == nil && agentRunning {
			return fmt.Errorf("%w: %s", ErrSessionRunning, sessionID)
		}
		// Zombie session - kill and recreate
		if err := m.backend.KillSession(sessionID); err != nil {
			return fmt.Errorf("killing zombie session: %w", err)
		}
	}

	// Ensure Claude settings exist for dogs
	if err := claude.EnsureSettingsForRole(kennelDir, "dog"); err != nil {
		return fmt.Errorf("ensuring Claude settings: %w", err)
	}

	// In K8s mode, the session is created by the controller via bead creation.
	// For local mode, set up the environment and verify the session comes up.

	// Set environment variables
	envVars := config.AgentEnv(config.AgentEnvConfig{
		Role:         "dog",
		TownRoot:     m.townRoot,
		BDDaemonHost: os.Getenv("BD_DAEMON_HOST"),
	})
	for k, v := range envVars {
		_ = m.backend.SetEnvironment(sessionID, k, v)
	}

	time.Sleep(constants.ShutdownNotifyDelay)

	// Verify session survived startup
	running, err = m.hasSession(sessionID)
	if err != nil {
		return fmt.Errorf("verifying session: %w", err)
	}
	if !running {
		return fmt.Errorf("session %s died during startup", sessionID)
	}

	return nil
}

// startK8s creates an agent bead for a K8s dog without creating a session.
// The K8s controller watches for agent beads with gt:agent + execution_target:k8s
// labels, then creates pods.
func (m *SessionManager) startK8s(dogName string, opts SessionStartOptions) error {
	agentBeadID := beads.DogBeadIDTown(dogName)
	beadsClient := beads.New(m.townRoot)

	_, err := beadsClient.CreateOrReopenAgentBead(agentBeadID, agentBeadID, &beads.AgentFields{
		RoleType:        "dog",
		AgentState:      "spawning",
		ExecutionTarget: "k8s",
	})
	if err != nil {
		return fmt.Errorf("creating agent bead for K8s dog: %w", err)
	}

	fmt.Printf("Dog %s dispatched to K8s (agent_state=spawning)\n", dogName)
	return nil
}

// Stop terminates a dog session.
func (m *SessionManager) Stop(dogName string, force bool) error {
	sessionID := m.SessionName(dogName)

	running, err := m.hasSession(sessionID)
	if err != nil {
		return fmt.Errorf("checking session: %w", err)
	}
	if !running {
		return ErrSessionNotFound
	}

	// Try graceful shutdown first
	if !force {
		_ = m.backend.SendKeys(sessionID, "C-c")
		time.Sleep(100 * time.Millisecond)
	}

	if err := m.backend.KillSession(sessionID); err != nil {
		return fmt.Errorf("killing session: %w", err)
	}

	return nil
}

// IsRunning checks if a dog session is active.
func (m *SessionManager) IsRunning(dogName string) (bool, error) {
	sessionID := m.SessionName(dogName)
	return m.hasSession(sessionID)
}

// Status returns detailed status for a dog session.
func (m *SessionManager) Status(dogName string) (*SessionInfo, error) {
	sessionID := m.SessionName(dogName)

	running, err := m.hasSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("checking session: %w", err)
	}

	info := &SessionInfo{
		DogName:   dogName,
		SessionID: sessionID,
		Running:   running,
	}

	if !running {
		return info, nil
	}

	// Coop doesn't provide detailed session info; attached status is not available.
	return info, nil
}

// GetPane returns the pane ID for a dog session.
func (m *SessionManager) GetPane(dogName string) (string, error) {
	sessionID := m.SessionName(dogName)

	running, err := m.hasSession(sessionID)
	if err != nil {
		return "", fmt.Errorf("checking session: %w", err)
	}
	if !running {
		return "", ErrSessionNotFound
	}

	// In K8s/coop mode, return the session ID directly.
	return sessionID, nil
}

// EnsureRunning ensures a dog session is running, starting it if needed.
// Returns the pane ID.
func (m *SessionManager) EnsureRunning(dogName string, opts SessionStartOptions) (string, error) {
	running, err := m.IsRunning(dogName)
	if err != nil {
		return "", err
	}

	if !running {
		if err := m.Start(dogName, opts); err != nil {
			return "", err
		}
	}

	return m.GetPane(dogName)
}
