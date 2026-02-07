package slackbot

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/steveyegge/gastown/internal/rpcclient"
)

// startTestNATS starts an embedded NATS server with JetStream for testing.
func startTestNATS(t *testing.T) (*natsserver.Server, string) {
	t.Helper()
	opts := &natsserver.Options{
		Host:      "127.0.0.1",
		Port:      -1, // Auto-assign port
		NoLog:     true,
		NoSigs:    true,
		JetStream: true,
		StoreDir:  t.TempDir(),
	}

	s, err := natsserver.NewServer(opts)
	if err != nil {
		t.Fatalf("failed to create NATS server: %v", err)
	}
	s.Start()
	if !s.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS server not ready")
	}

	url := s.ClientURL()
	t.Logf("Test NATS server at %s", url)
	return s, url
}

// ensureTestStream creates the HOOK_EVENTS JetStream stream for testing.
func ensureTestStream(t *testing.T, nc *nats.Conn) nats.JetStreamContext {
	t.Helper()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("failed to get JetStream: %v", err)
	}

	_, err = js.AddStream(&nats.StreamConfig{
		Name:     "HOOK_EVENTS",
		Subjects: []string{"hooks.>"},
		Storage:  nats.MemoryStorage,
	})
	if err != nil {
		t.Fatalf("failed to create stream: %v", err)
	}
	return js
}

func TestNewBusListener_DefaultConfig(t *testing.T) {
	cfg := BusListenerConfig{
		NatsURL: "nats://localhost:4222",
	}
	listener := NewBusListener(cfg, nil, nil)

	if listener.cfg.ConsumerName != "slack-bot" {
		t.Errorf("expected consumer name 'slack-bot', got %q", listener.cfg.ConsumerName)
	}
	if listener.cfg.StreamName != "HOOK_EVENTS" {
		t.Errorf("expected stream name 'HOOK_EVENTS', got %q", listener.cfg.StreamName)
	}
	if len(listener.cfg.Subjects) != 6 {
		t.Errorf("expected 6 default subjects, got %d", len(listener.cfg.Subjects))
	}
}

func TestNewBusListener_CustomSubjects(t *testing.T) {
	cfg := BusListenerConfig{
		NatsURL:  "nats://localhost:4222",
		Subjects: []string{"decisions.>"},
	}
	listener := NewBusListener(cfg, nil, nil)

	if len(listener.cfg.Subjects) != 1 {
		t.Errorf("expected 1 subject, got %d", len(listener.cfg.Subjects))
	}
	if listener.cfg.Subjects[0] != "decisions.>" {
		t.Errorf("expected 'decisions.>', got %q", listener.cfg.Subjects[0])
	}
}

func TestBusListener_HandleMessage_DecisionCreated(t *testing.T) {
	cfg := Config{
		BotToken:    "xoxb-test",
		AppToken:    "xapp-test",
		RPCEndpoint: "http://localhost:8443",
		ChannelID:   "C12345",
	}
	bot, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create bot: %v", err)
	}

	rpcClient := rpcclient.NewClient("http://localhost:8443")
	listener := NewBusListener(BusListenerConfig{
		NatsURL: "nats://localhost:4222",
	}, bot, rpcClient)

	payload := DecisionEventPayload{
		DecisionID:  "test-decision-1",
		Question:    "Which approach?",
		Urgency:     "high",
		RequestedBy: "gastown/polecats/alpha",
		OptionCount: 3,
	}
	data, _ := json.Marshal(payload)

	// handleMessage should not panic with a valid payload
	listener.handleMessage("hooks.DecisionCreated", data)

	// Verify de-duplication: second call should be a no-op
	listener.seenMu.Lock()
	seen := listener.seen["test-decision-1"]
	listener.seenMu.Unlock()
	if !seen {
		t.Error("expected decision to be marked as seen")
	}
}

func TestBusListener_HandleMessage_DecisionResponded(t *testing.T) {
	cfg := Config{
		BotToken:    "xoxb-test",
		AppToken:    "xapp-test",
		RPCEndpoint: "http://localhost:8443",
		ChannelID:   "C12345",
	}
	bot, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create bot: %v", err)
	}

	rpcClient := rpcclient.NewClient("http://localhost:8443")
	listener := NewBusListener(BusListenerConfig{
		NatsURL: "nats://localhost:4222",
	}, bot, rpcClient)

	payload := DecisionEventPayload{
		DecisionID:  "test-decision-2",
		ChosenIndex: 1,
		ChosenLabel: "Option B",
		ResolvedBy:  "slack:U1234",
	}
	data, _ := json.Marshal(payload)

	listener.handleMessage("hooks.DecisionResponded", data)

	// Verify de-duplication with resolved: prefix
	listener.seenMu.Lock()
	seen := listener.seen["resolved:test-decision-2"]
	listener.seenMu.Unlock()
	if !seen {
		t.Error("expected resolution to be marked as seen")
	}
}

func TestBusListener_DuplicateFiltering(t *testing.T) {
	listener := NewBusListener(BusListenerConfig{
		NatsURL: "nats://localhost:4222",
	}, nil, nil)

	// Pre-mark as seen
	listener.seenMu.Lock()
	listener.seen["existing-decision"] = true
	listener.seenMu.Unlock()

	// handleDecisionCreated should skip this one (nil bot won't be called)
	listener.handleDecisionCreated(DecisionEventPayload{
		DecisionID: "existing-decision",
		Question:   "Should not process this",
	})

	// If we got here without a nil pointer panic, de-duplication worked
}

func TestBusListener_HandleMessage_InvalidJSON(t *testing.T) {
	listener := NewBusListener(BusListenerConfig{
		NatsURL: "nats://localhost:4222",
	}, nil, nil)

	// Should not panic on invalid JSON
	listener.handleMessage("hooks.DecisionCreated", []byte("not json"))
}

func TestBusListener_HandleMessage_EmptyDecisionID(t *testing.T) {
	listener := NewBusListener(BusListenerConfig{
		NatsURL: "nats://localhost:4222",
	}, nil, nil)

	payload := DecisionEventPayload{
		Question: "No ID here",
	}
	data, _ := json.Marshal(payload)

	// Should log and skip, not process
	listener.handleMessage("hooks.DecisionCreated", data)
}

func TestBusListener_HandleMessage_SubjectRouting(t *testing.T) {
	tests := []struct {
		subject     string
		wantCreated bool
		wantResolved bool
	}{
		{"hooks.DecisionCreated", true, false},
		{"hooks.DecisionResponded", false, true},
		{"decisions.created", true, false},
		{"decisions.responded", false, true},
		{"hooks.decision.created", true, false},
		{"hooks.decision.responded", false, true},
		{"hooks.Unknown", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.subject, func(t *testing.T) {
			listener := NewBusListener(BusListenerConfig{
				NatsURL: "nats://localhost:4222",
			}, nil, nil)

			payload := DecisionEventPayload{
				DecisionID: "route-test-" + tt.subject,
				Question:   "Test routing",
			}
			data, _ := json.Marshal(payload)

			// handleMessage routes based on subject
			listener.handleMessage(tt.subject, data)

			listener.seenMu.Lock()
			createdSeen := listener.seen["route-test-"+tt.subject]
			resolvedSeen := listener.seen["resolved:route-test-"+tt.subject]
			listener.seenMu.Unlock()

			if createdSeen != tt.wantCreated {
				t.Errorf("created seen=%v, want %v", createdSeen, tt.wantCreated)
			}
			if resolvedSeen != tt.wantResolved {
				t.Errorf("resolved seen=%v, want %v", resolvedSeen, tt.wantResolved)
			}
		})
	}
}

func TestBusListener_PlainNATS_ReceivesEvents(t *testing.T) {
	ns, url := startTestNATS(t)
	defer ns.Shutdown()

	cfg := Config{
		BotToken:    "xoxb-test",
		AppToken:    "xapp-test",
		RPCEndpoint: "http://localhost:8443",
		ChannelID:   "C12345",
	}
	bot, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create bot: %v", err)
	}

	rpcClient := rpcclient.NewClient("http://localhost:8443")
	listener := NewBusListener(BusListenerConfig{
		NatsURL:  url,
		Subjects: []string{"hooks.DecisionCreated"},
	}, bot, rpcClient)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start listener in background (will use plain NATS since no JetStream stream)
	go func() {
		_ = listener.Run(ctx)
	}()

	// Give listener time to subscribe
	time.Sleep(500 * time.Millisecond)

	// Publish a test event
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer nc.Close()

	payload := DecisionEventPayload{
		DecisionID: "nats-test-1",
		Question:   "Test from NATS",
		Urgency:    "high",
	}
	data, _ := json.Marshal(payload)
	if err := nc.Publish("hooks.DecisionCreated", data); err != nil {
		t.Fatalf("failed to publish: %v", err)
	}
	nc.Flush()

	// Wait for processing
	time.Sleep(500 * time.Millisecond)

	// Verify the event was received and processed
	listener.seenMu.Lock()
	seen := listener.seen["nats-test-1"]
	listener.seenMu.Unlock()

	if !seen {
		t.Error("expected decision to be seen after NATS publish")
	}
}

func TestBusListener_JetStream_ReceivesEvents(t *testing.T) {
	ns, url := startTestNATS(t)
	defer ns.Shutdown()

	// Create JetStream stream first
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	js := ensureTestStream(t, nc)

	// Publish event BEFORE listener starts (tests JetStream replay)
	payload := DecisionEventPayload{
		DecisionID: "js-replay-1",
		Question:   "Replay test",
	}
	data, _ := json.Marshal(payload)
	if _, err := js.Publish("hooks.DecisionCreated", data); err != nil {
		t.Fatalf("failed to publish to JetStream: %v", err)
	}
	nc.Close()

	// Create bot and listener
	cfg := Config{
		BotToken:    "xoxb-test",
		AppToken:    "xapp-test",
		RPCEndpoint: "http://localhost:8443",
		ChannelID:   "C12345",
	}
	bot, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create bot: %v", err)
	}

	rpcClient := rpcclient.NewClient("http://localhost:8443")
	listener := NewBusListener(BusListenerConfig{
		NatsURL:      url,
		Subjects:     []string{"hooks.DecisionCreated"},
		ConsumerName: "test-consumer",
	}, bot, rpcClient)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		_ = listener.Run(ctx)
	}()

	// Wait for replay processing
	time.Sleep(2 * time.Second)

	listener.seenMu.Lock()
	seen := listener.seen["js-replay-1"]
	listener.seenMu.Unlock()

	if !seen {
		t.Error("expected JetStream replay to deliver pre-published event")
	}
}

func TestBusListener_EmitDecisionResponse(t *testing.T) {
	ns, url := startTestNATS(t)
	defer ns.Shutdown()

	// Create stream and subscriber to verify emission
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer nc.Close()

	js := ensureTestStream(t, nc)

	// Subscribe to responses
	received := make(chan *nats.Msg, 1)
	sub, err := nc.Subscribe("hooks.DecisionResponded", func(msg *nats.Msg) {
		received <- msg
	})
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	// Create listener with JetStream connection
	listener := &BusListener{
		cfg:  BusListenerConfig{NatsURL: url},
		seen: make(map[string]bool),
		nc:   nc,
		js:   js,
	}

	// Emit a response
	err = listener.EmitDecisionResponse("test-decision", 1, "Option B", "U1234")
	if err != nil {
		t.Fatalf("EmitDecisionResponse failed: %v", err)
	}

	// Verify reception
	select {
	case msg := <-received:
		var payload DecisionEventPayload
		if err := json.Unmarshal(msg.Data, &payload); err != nil {
			t.Fatalf("failed to parse emitted payload: %v", err)
		}
		if payload.DecisionID != "test-decision" {
			t.Errorf("expected decision_id 'test-decision', got %q", payload.DecisionID)
		}
		if payload.ChosenIndex != 1 {
			t.Errorf("expected chosen_index 1, got %d", payload.ChosenIndex)
		}
		if payload.ChosenLabel != "Option B" {
			t.Errorf("expected chosen_label 'Option B', got %q", payload.ChosenLabel)
		}
		if payload.ResolvedBy != "slack:U1234" {
			t.Errorf("expected resolved_by 'slack:U1234', got %q", payload.ResolvedBy)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for emitted event")
	}
}

func TestDecisionEventPayload_JSON(t *testing.T) {
	payload := DecisionEventPayload{
		DecisionID:  "test-123",
		Question:    "Which approach?",
		Urgency:     "high",
		RequestedBy: "gastown/polecats/alpha",
		OptionCount: 3,
		ChosenIndex: 1,
		ChosenLabel: "Option B",
		ResolvedBy:  "slack:U1234",
		Rationale:   "Best tradeoff",
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded DecisionEventPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.DecisionID != payload.DecisionID {
		t.Errorf("DecisionID mismatch: got %q, want %q", decoded.DecisionID, payload.DecisionID)
	}
	if decoded.ChosenIndex != payload.ChosenIndex {
		t.Errorf("ChosenIndex mismatch: got %d, want %d", decoded.ChosenIndex, payload.ChosenIndex)
	}
	if decoded.ResolvedBy != payload.ResolvedBy {
		t.Errorf("ResolvedBy mismatch: got %q, want %q", decoded.ResolvedBy, payload.ResolvedBy)
	}
}

func TestBusListener_HandleMessage_BeadStatusChanged(t *testing.T) {
	listener := NewBusListener(BusListenerConfig{
		NatsURL: "nats://localhost:4222",
	}, nil, nil)

	payload := BeadStatusChangedPayload{
		BeadID:    "gt-abc123",
		Title:     "Fix authentication bug",
		OldStatus: "open",
		NewStatus: "in_progress",
		Assignee:  "gastown/polecats/alpha",
		ChangedBy: "mayor",
	}
	data, _ := json.Marshal(payload)

	// Should not panic with nil bot (skips posting)
	listener.handleMessage("hooks.BeadStatusChanged", data)

	// Verify de-duplication key was set
	listener.seenMu.Lock()
	dedupeKey := "status:gt-abc123:open→in_progress"
	seen := listener.seen[dedupeKey]
	listener.seenMu.Unlock()
	if !seen {
		t.Error("expected bead status change to be marked as seen")
	}
}

func TestBusListener_HandleMessage_BeadStatusChanged_UninterestingTransition(t *testing.T) {
	listener := NewBusListener(BusListenerConfig{
		NatsURL: "nats://localhost:4222",
	}, nil, nil)

	// open → deferred is not in the interesting transitions map
	payload := BeadStatusChangedPayload{
		BeadID:    "gt-boring",
		Title:     "Boring transition",
		OldStatus: "open",
		NewStatus: "deferred",
	}
	data, _ := json.Marshal(payload)

	listener.handleMessage("hooks.BeadStatusChanged", data)

	// Should NOT be in seen map since it was filtered out
	listener.seenMu.Lock()
	dedupeKey := "status:gt-boring:open→deferred"
	seen := listener.seen[dedupeKey]
	listener.seenMu.Unlock()
	if seen {
		t.Error("uninteresting transition should not be marked as seen")
	}
}

func TestBusListener_HandleMessage_BeadStatusChanged_EmptyBeadID(t *testing.T) {
	listener := NewBusListener(BusListenerConfig{
		NatsURL: "nats://localhost:4222",
	}, nil, nil)

	payload := BeadStatusChangedPayload{
		Title:     "No bead ID",
		OldStatus: "open",
		NewStatus: "closed",
	}
	data, _ := json.Marshal(payload)

	// Should log and skip, not panic
	listener.handleMessage("hooks.BeadStatusChanged", data)
}

func TestBusListener_HandleMessage_BeadStatusChanged_InvalidJSON(t *testing.T) {
	listener := NewBusListener(BusListenerConfig{
		NatsURL: "nats://localhost:4222",
	}, nil, nil)

	// Should not panic on invalid JSON for bead events
	listener.handleMessage("hooks.BeadStatusChanged", []byte("not json"))
}

func TestBusListener_HandleMessage_WorkCompleted(t *testing.T) {
	listener := NewBusListener(BusListenerConfig{
		NatsURL: "nats://localhost:4222",
	}, nil, nil)

	payload := WorkCompletedPayload{
		BeadID:   "gt-xyz789",
		Title:    "Implement feature X",
		Assignee: "gastown/polecats/beta",
		Summary:  "Added new feature with tests",
		Branch:   "feat/feature-x",
		Commit:   "abc1234567890",
	}
	data, _ := json.Marshal(payload)

	// Should not panic with nil bot (skips posting)
	listener.handleMessage("hooks.WorkCompleted", data)

	// Verify de-duplication key was set
	listener.seenMu.Lock()
	seen := listener.seen["completed:gt-xyz789"]
	listener.seenMu.Unlock()
	if !seen {
		t.Error("expected work completed to be marked as seen")
	}
}

func TestBusListener_HandleMessage_WorkCompleted_EmptyBeadID(t *testing.T) {
	listener := NewBusListener(BusListenerConfig{
		NatsURL: "nats://localhost:4222",
	}, nil, nil)

	payload := WorkCompletedPayload{
		Title: "No bead ID",
	}
	data, _ := json.Marshal(payload)

	// Should log and skip, not panic
	listener.handleMessage("hooks.WorkCompleted", data)
}

func TestBusListener_HandleMessage_WorkCompleted_InvalidJSON(t *testing.T) {
	listener := NewBusListener(BusListenerConfig{
		NatsURL: "nats://localhost:4222",
	}, nil, nil)

	// Should not panic on invalid JSON for work completed events
	listener.handleMessage("hooks.WorkCompleted", []byte("bad json"))
}

func TestBusListener_HandleMessage_WorkCompleted_DuplicateFiltering(t *testing.T) {
	listener := NewBusListener(BusListenerConfig{
		NatsURL: "nats://localhost:4222",
	}, nil, nil)

	// Pre-mark as seen
	listener.seenMu.Lock()
	listener.seen["completed:gt-dupe"] = true
	listener.seenMu.Unlock()

	// handleWorkCompleted should skip (nil bot won't be called)
	listener.handleWorkCompleted(WorkCompletedPayload{
		BeadID: "gt-dupe",
		Title:  "Already completed",
	})

	// If we got here without a nil pointer panic, de-duplication worked
}

func TestBusListener_HandleMessage_BeadStatusChanged_SubjectRouting(t *testing.T) {
	tests := []struct {
		subject  string
		wantSeen bool
	}{
		{"hooks.BeadStatusChanged", true},
		{"bead.status_changed", true},
		{"beads.status_changed", true},
		{"hooks.Unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.subject, func(t *testing.T) {
			listener := NewBusListener(BusListenerConfig{
				NatsURL: "nats://localhost:4222",
			}, nil, nil)

			payload := BeadStatusChangedPayload{
				BeadID:    "route-test-bead",
				Title:     "Test routing",
				OldStatus: "open",
				NewStatus: "in_progress",
			}
			data, _ := json.Marshal(payload)

			listener.handleMessage(tt.subject, data)

			listener.seenMu.Lock()
			dedupeKey := "status:route-test-bead:open→in_progress"
			seen := listener.seen[dedupeKey]
			listener.seenMu.Unlock()

			if seen != tt.wantSeen {
				t.Errorf("seen=%v, want %v", seen, tt.wantSeen)
			}
		})
	}
}

func TestBusListener_HandleMessage_WorkCompleted_SubjectRouting(t *testing.T) {
	tests := []struct {
		subject  string
		wantSeen bool
	}{
		{"hooks.WorkCompleted", true},
		{"work.completed", true},
		{"work_completed", true},
		{"hooks.Unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.subject, func(t *testing.T) {
			listener := NewBusListener(BusListenerConfig{
				NatsURL: "nats://localhost:4222",
			}, nil, nil)

			payload := WorkCompletedPayload{
				BeadID: "route-test-work",
				Title:  "Test routing",
			}
			data, _ := json.Marshal(payload)

			listener.handleMessage(tt.subject, data)

			listener.seenMu.Lock()
			seen := listener.seen["completed:route-test-work"]
			listener.seenMu.Unlock()

			if seen != tt.wantSeen {
				t.Errorf("seen=%v, want %v", seen, tt.wantSeen)
			}
		})
	}
}

func TestBeadStatusChangedPayload_JSON(t *testing.T) {
	payload := BeadStatusChangedPayload{
		BeadID:    "gt-abc123",
		Title:     "Fix the bug",
		OldStatus: "open",
		NewStatus: "in_progress",
		Assignee:  "gastown/polecats/alpha",
		ChangedBy: "mayor",
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded BeadStatusChangedPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.BeadID != payload.BeadID {
		t.Errorf("BeadID mismatch: got %q, want %q", decoded.BeadID, payload.BeadID)
	}
	if decoded.OldStatus != payload.OldStatus {
		t.Errorf("OldStatus mismatch: got %q, want %q", decoded.OldStatus, payload.OldStatus)
	}
	if decoded.NewStatus != payload.NewStatus {
		t.Errorf("NewStatus mismatch: got %q, want %q", decoded.NewStatus, payload.NewStatus)
	}
	if decoded.Assignee != payload.Assignee {
		t.Errorf("Assignee mismatch: got %q, want %q", decoded.Assignee, payload.Assignee)
	}
}

func TestWorkCompletedPayload_JSON(t *testing.T) {
	payload := WorkCompletedPayload{
		BeadID:   "gt-xyz789",
		Title:    "Implement feature",
		Assignee: "gastown/polecats/beta",
		Summary:  "Done with tests",
		Branch:   "feat/feature-x",
		Commit:   "abc1234567890",
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded WorkCompletedPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.BeadID != payload.BeadID {
		t.Errorf("BeadID mismatch: got %q, want %q", decoded.BeadID, payload.BeadID)
	}
	if decoded.Summary != payload.Summary {
		t.Errorf("Summary mismatch: got %q, want %q", decoded.Summary, payload.Summary)
	}
	if decoded.Branch != payload.Branch {
		t.Errorf("Branch mismatch: got %q, want %q", decoded.Branch, payload.Branch)
	}
	if decoded.Commit != payload.Commit {
		t.Errorf("Commit mismatch: got %q, want %q", decoded.Commit, payload.Commit)
	}
}

func TestInterestingTransitions(t *testing.T) {
	tests := []struct {
		old, new    string
		interesting bool
	}{
		{"open", "in_progress", true},
		{"open", "closed", true},
		{"open", "deferred", false},
		{"in_progress", "closed", true},
		{"in_progress", "blocked", true},
		{"in_progress", "open", false},
		{"blocked", "in_progress", true},
		{"blocked", "closed", true},
		{"deferred", "in_progress", true},
		{"deferred", "open", true},
		{"deferred", "closed", false},
		{"unknown", "closed", false},
	}

	for _, tt := range tests {
		t.Run(tt.old+"→"+tt.new, func(t *testing.T) {
			targets, ok := interestingTransitions[tt.old]
			got := ok && targets[tt.new]
			if got != tt.interesting {
				t.Errorf("transition %s→%s: got interesting=%v, want %v", tt.old, tt.new, got, tt.interesting)
			}
		})
	}
}

func TestBusListener_PlainNATS_ReceivesBeadStatusChanged(t *testing.T) {
	ns, url := startTestNATS(t)
	defer ns.Shutdown()

	listener := NewBusListener(BusListenerConfig{
		NatsURL:  url,
		Subjects: []string{"hooks.BeadStatusChanged"},
	}, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		_ = listener.Run(ctx)
	}()

	// Give listener time to subscribe
	time.Sleep(500 * time.Millisecond)

	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer nc.Close()

	payload := BeadStatusChangedPayload{
		BeadID:    "nats-bead-1",
		Title:     "Test bead status via NATS",
		OldStatus: "open",
		NewStatus: "in_progress",
	}
	data, _ := json.Marshal(payload)
	if err := nc.Publish("hooks.BeadStatusChanged", data); err != nil {
		t.Fatalf("failed to publish: %v", err)
	}
	nc.Flush()

	time.Sleep(500 * time.Millisecond)

	listener.seenMu.Lock()
	seen := listener.seen["status:nats-bead-1:open→in_progress"]
	listener.seenMu.Unlock()

	if !seen {
		t.Error("expected bead status change to be seen after NATS publish")
	}
}

func TestBusListener_PlainNATS_ReceivesWorkCompleted(t *testing.T) {
	ns, url := startTestNATS(t)
	defer ns.Shutdown()

	listener := NewBusListener(BusListenerConfig{
		NatsURL:  url,
		Subjects: []string{"hooks.WorkCompleted"},
	}, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		_ = listener.Run(ctx)
	}()

	time.Sleep(500 * time.Millisecond)

	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer nc.Close()

	payload := WorkCompletedPayload{
		BeadID:   "nats-work-1",
		Title:    "Test work completed via NATS",
		Assignee: "gastown/polecats/test",
		Summary:  "All done",
	}
	data, _ := json.Marshal(payload)
	if err := nc.Publish("hooks.WorkCompleted", data); err != nil {
		t.Fatalf("failed to publish: %v", err)
	}
	nc.Flush()

	time.Sleep(500 * time.Millisecond)

	listener.seenMu.Lock()
	seen := listener.seen["completed:nats-work-1"]
	listener.seenMu.Unlock()

	if !seen {
		t.Error("expected work completion to be seen after NATS publish")
	}
}

func TestContainsHelper(t *testing.T) {
	tests := []struct {
		s, substr string
		want      bool
	}{
		{"hooks.DecisionCreated", "DecisionCreated", true},
		{"hooks.DecisionResponded", "DecisionResponded", true},
		{"decisions.created", "decision.created", false},
		{"decisions.created", "decisions.created", true},
		{"", "test", false},
		{"test", "", true},
	}

	for _, tt := range tests {
		got := contains(tt.s, tt.substr)
		if got != tt.want {
			t.Errorf("contains(%q, %q) = %v, want %v", tt.s, tt.substr, got, tt.want)
		}
	}
}
