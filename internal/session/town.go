// Package session provides polecat session lifecycle management.
package session

import (
	"fmt"
	"time"

	"github.com/steveyegge/gastown/internal/events"
	"github.com/steveyegge/gastown/internal/terminal"
)

// TownSession represents a town-level session.
type TownSession struct {
	Name      string // Display name (e.g., "Mayor")
	SessionID string // Session ID (e.g., "hq-mayor")
}

// TownSessions returns the list of town-level sessions in shutdown order.
// Order matters: Boot (Deacon's watchdog) must be stopped before Deacon,
// otherwise Boot will try to restart Deacon.
func TownSessions() []TownSession {
	return []TownSession{
		{"Mayor", MayorSessionName()},
		{"Boot", BootSessionName()},
		{"Deacon", DeaconSessionName()},
	}
}

// StopTownSession stops a single town-level session.
// If force is true, skips graceful shutdown (Ctrl-C) and kills immediately.
// Returns true if the session was running and stopped, false if not running.
func StopTownSession(backend terminal.Backend, ts TownSession, force bool) (bool, error) {
	running, err := backend.HasSession(ts.SessionID)
	if err != nil {
		return false, err
	}
	if !running {
		return false, nil
	}

	return stopTownSessionInternal(backend, ts, force)
}

// stopTownSessionInternal performs the actual session stop.
func stopTownSessionInternal(backend terminal.Backend, ts TownSession, force bool) (bool, error) {
	// Try graceful shutdown first (unless forced)
	if !force {
		_ = backend.SendKeys(ts.SessionID, "C-c")
		time.Sleep(100 * time.Millisecond)
	}

	// Log pre-death event for crash investigation (before killing)
	reason := "user shutdown"
	if force {
		reason = "forced shutdown"
	}
	_ = events.LogFeed(events.TypeSessionDeath, ts.SessionID,
		events.SessionDeathPayload(ts.SessionID, ts.Name, reason, "gt down"))

	// Kill the session.
	if err := backend.KillSession(ts.SessionID); err != nil {
		return false, fmt.Errorf("killing %s session: %w", ts.Name, err)
	}

	return true, nil
}
