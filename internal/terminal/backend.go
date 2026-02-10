// Package terminal provides a backend abstraction for terminal I/O operations.
//
// This enables the same peek/nudge commands to work with both local tmux
// sessions and remote K8s pods via SSH+tmux.
package terminal

// Backend provides terminal capture and input for agent sessions.
// Implementations include local tmux (TmuxBackend) and remote SSH+tmux
// for K8s-hosted polecats (SSHBackend).
type Backend interface {
	// HasSession checks if a terminal session exists and is running.
	HasSession(session string) (bool, error)

	// CapturePane captures the last N lines of terminal output from a session.
	CapturePane(session string, lines int) (string, error)

	// CapturePaneAll captures the full scrollback history from a session.
	CapturePaneAll(session string) (string, error)

	// CapturePaneLines captures the last N lines as a string slice.
	CapturePaneLines(session string, lines int) ([]string, error)

	// NudgeSession sends a message to a terminal session with proper
	// serialization and Enter key handling.
	NudgeSession(session string, message string) error

	// SendKeys sends raw keystrokes to a terminal session.
	SendKeys(session string, keys string) error

	// IsPaneDead checks if the session's pane process has exited.
	// For tmux: checks pane_dead flag. For coop: checks if agent state is "exited" or "crashed".
	IsPaneDead(session string) (bool, error)

	// SetPaneDiedHook sets up a callback/hook for when an agent's pane dies.
	// For tmux: sets a tmux pane-died hook. For coop: this is a no-op (coop manages its own lifecycle).
	SetPaneDiedHook(session, agentID string) error
}
