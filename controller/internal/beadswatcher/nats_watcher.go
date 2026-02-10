package beadswatcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
)

// NATSConfig holds configuration for the NATSWatcher.
type NATSConfig struct {
	// NatsURL is the NATS server URL (e.g., "nats://host:4222").
	NatsURL string

	// NatsToken is the auth token for NATS (optional).
	NatsToken string

	// ConsumerName is the durable consumer name for JetStream.
	// Allows crash recovery and fan-out across replicas.
	ConsumerName string

	// Config embeds the common watcher config for building lifecycle events.
	Config
}

// NATSWatcher subscribes to the MUTATION_EVENTS JetStream stream and translates
// mutation events on agent beads into lifecycle Events. Uses a durable consumer
// for crash recovery and replay.
type NATSWatcher struct {
	cfg    NATSConfig
	events chan Event
	logger *slog.Logger
}

// NewNATSWatcher creates a watcher backed by JetStream mutation events.
func NewNATSWatcher(cfg NATSConfig, logger *slog.Logger) *NATSWatcher {
	return &NATSWatcher{
		cfg:    cfg,
		events: make(chan Event, 64),
		logger: logger,
	}
}

// Start begins watching the JetStream stream. Blocks until ctx is canceled.
// Reconnects with exponential backoff on errors.
func (w *NATSWatcher) Start(ctx context.Context) error {
	backoff := time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			close(w.events)
			return fmt.Errorf("watcher stopped: %w", ctx.Err())
		default:
		}

		err := w.subscribe(ctx)
		if err != nil {
			if ctx.Err() != nil {
				close(w.events)
				return fmt.Errorf("watcher stopped: %w", ctx.Err())
			}
			w.logger.Warn("JetStream subscription error, reconnecting",
				"error", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				close(w.events)
				return fmt.Errorf("watcher stopped: %w", ctx.Err())
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		} else {
			backoff = time.Second
		}
	}
}

// Events returns a read-only channel of lifecycle events.
func (w *NATSWatcher) Events() <-chan Event {
	return w.events
}

// mutationPayload mirrors the eventbus.MutationEventPayload from the beads daemon.
type mutationPayload struct {
	Type      string   `json:"type"`
	IssueID   string   `json:"issue_id"`
	Title     string   `json:"title,omitempty"`
	Assignee  string   `json:"assignee,omitempty"`
	Actor     string   `json:"actor,omitempty"`
	Timestamp string   `json:"timestamp"`
	OldStatus string   `json:"old_status,omitempty"`
	NewStatus string   `json:"new_status,omitempty"`
	ParentID  string   `json:"parent_id,omitempty"`
	IssueType string   `json:"issue_type,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	AwaitType string   `json:"await_type,omitempty"`
}

// subscribe connects to NATS and subscribes to MUTATION_EVENTS via JetStream.
func (w *NATSWatcher) subscribe(ctx context.Context) error {
	opts := []nats.Option{
		nats.Name("gastown-controller"),
	}
	if w.cfg.NatsToken != "" {
		opts = append(opts, nats.Token(w.cfg.NatsToken))
	}

	nc, err := nats.Connect(w.cfg.NatsURL, opts...)
	if err != nil {
		return fmt.Errorf("NATS connect: %w", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		return fmt.Errorf("JetStream context: %w", err)
	}

	consumerName := w.cfg.ConsumerName
	if consumerName == "" {
		consumerName = "controller"
	}

	w.logger.Info("subscribing to MUTATION_EVENTS stream",
		"consumer", consumerName, "url", w.cfg.NatsURL)

	// Subscribe with a durable pull consumer for reliable delivery.
	sub, err := js.PullSubscribe(
		"mutations.>",
		consumerName,
		nats.AckExplicit(),
		nats.DeliverAll(),
	)
	if err != nil {
		return fmt.Errorf("JetStream subscribe: %w", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	w.logger.Info("JetStream subscription active")

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// Fetch messages in batches with a timeout.
		msgs, err := sub.Fetch(10, nats.MaxWait(2*time.Second))
		if err != nil {
			if err == nats.ErrTimeout {
				continue // No messages available, loop back
			}
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("JetStream fetch: %w", err)
		}

		for _, msg := range msgs {
			w.processMessage(msg)
			if err := msg.Ack(); err != nil {
				w.logger.Warn("failed to ack message", "error", err)
			}
		}
	}
}

// processMessage parses a JetStream message and emits a lifecycle Event if relevant.
func (w *NATSWatcher) processMessage(msg *nats.Msg) {
	var payload mutationPayload
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		w.logger.Debug("skipping malformed JetStream message", "error", err)
		return
	}

	// Convert to the mutationEvent format used by the shared mapping logic.
	ts, _ := time.Parse(time.RFC3339Nano, payload.Timestamp)
	raw := mutationEvent{
		Type:      payload.Type,
		IssueID:   payload.IssueID,
		Title:     payload.Title,
		Assignee:  payload.Assignee,
		Actor:     payload.Actor,
		Timestamp: ts,
		OldStatus: payload.OldStatus,
		NewStatus: payload.NewStatus,
		IssueType: payload.IssueType,
		Labels:    payload.Labels,
	}

	if !isAgentBead(raw) {
		return
	}

	// Reuse the SSEWatcher's mapping logic via a temporary SSEWatcher.
	// The mapping logic only depends on cfg, not on SSE connection state.
	mapper := &SSEWatcher{cfg: w.cfg.Config, events: w.events, logger: w.logger}
	event, ok := mapper.mapMutation(raw)
	if !ok {
		return
	}

	w.logger.Info("emitting lifecycle event (JetStream)",
		"type", event.Type, "rig", event.Rig,
		"role", event.Role, "agent", event.AgentName,
		"bead", event.BeadID)

	select {
	case w.events <- event:
	default:
		w.logger.Warn("event channel full, dropping event",
			"type", event.Type, "bead", event.BeadID)
	}
}
