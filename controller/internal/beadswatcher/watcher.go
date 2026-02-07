// Package beadswatcher watches BD Daemon for agent lifecycle events and
// emits them on a channel. The controller's main loop reads these events
// and translates them to K8s pod operations.
package beadswatcher

import (
	"context"
	"fmt"
	"log/slog"
)

// EventType identifies the kind of beads lifecycle event.
type EventType string

const (
	// AgentSpawn means a new agent needs a pod (crew created, polecat hooked).
	AgentSpawn EventType = "agent_spawn"

	// AgentDone means an agent completed its work (gt done, bead closed).
	AgentDone EventType = "agent_done"

	// AgentStuck means an agent is unresponsive (witness escalation).
	AgentStuck EventType = "agent_stuck"

	// AgentKill means an agent should be terminated (lifecycle shutdown).
	AgentKill EventType = "agent_kill"
)

// Event represents a beads lifecycle event that requires a pod operation.
type Event struct {
	Type      EventType
	Rig       string
	Role      string // polecat, crew, witness, refinery, mayor, deacon
	AgentName string
	BeadID    string            // The bead that triggered this event
	Metadata  map[string]string // Additional context from beads
}

// Watcher subscribes to BD Daemon lifecycle events and emits them on a channel.
type Watcher interface {
	// Start begins watching for beads events. Blocks until ctx is canceled.
	Start(ctx context.Context) error

	// Events returns a read-only channel of lifecycle events.
	Events() <-chan Event
}

// StubWatcher is a no-op implementation for scaffolding. It will be replaced
// by a real connect-rpc implementation in gt-naa65p.4.
type StubWatcher struct {
	events chan Event
	logger *slog.Logger
}

// NewStubWatcher creates a watcher that emits no events.
// TODO(gt-naa65p.4): Replace with connect-rpc BD Daemon subscriber.
func NewStubWatcher(logger *slog.Logger) *StubWatcher {
	return &StubWatcher{
		events: make(chan Event),
		logger: logger,
	}
}

// Start blocks until context is canceled. The stub emits no events.
func (w *StubWatcher) Start(ctx context.Context) error {
	w.logger.Info("beads watcher started (stub — no events will be emitted)")
	<-ctx.Done()
	close(w.events)
	return fmt.Errorf("watcher stopped: %w", ctx.Err())
}

// Events returns the event channel.
func (w *StubWatcher) Events() <-chan Event {
	return w.events
}
