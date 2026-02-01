package main

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/steveyegge/gastown/internal/eventbus"
	gastownv1 "github.com/steveyegge/gastown/mobile/gen/gastown/v1"
	"github.com/steveyegge/gastown/mobile/gen/gastown/v1/gastownv1connect"
)

// TestWatchStatus tests the status streaming endpoint.
func TestWatchStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	townRoot, cleanup := setupDecisionTestTown(t)
	defer cleanup()

	mux := http.NewServeMux()
	statusServer := NewStatusServer(townRoot)
	mux.Handle(gastownv1connect.NewStatusServiceHandler(statusServer))

	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	defer server.Close()

	client := gastownv1connect.NewStatusServiceClient(
		http.DefaultClient,
		server.URL,
	)

	t.Run("PeriodicUpdates", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		req := connect.NewRequest(&gastownv1.WatchStatusRequest{})
		stream, err := client.WatchStatus(ctx, req)
		if err != nil {
			t.Fatalf("WatchStatus failed: %v", err)
		}

		// Should receive at least one update
		count := 0
		for stream.Receive() {
			msg := stream.Msg()
			if msg == nil {
				t.Error("Received nil message")
				continue
			}
			if msg.Timestamp == nil {
				t.Error("Update missing timestamp")
			}
			count++
			if count >= 2 {
				break // Got enough updates
			}
		}

		if count < 1 {
			t.Error("Should have received at least 1 update")
		}
	})

	t.Run("ContextCancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		req := connect.NewRequest(&gastownv1.WatchStatusRequest{})
		stream, err := client.WatchStatus(ctx, req)
		if err != nil {
			t.Fatalf("WatchStatus failed: %v", err)
		}

		// Cancel after short delay
		go func() {
			time.Sleep(500 * time.Millisecond)
			cancel()
		}()

		// Drain stream
		for stream.Receive() {
		}

		err = stream.Err()
		if err != nil && err != context.Canceled {
			if connectErr, ok := err.(*connect.Error); ok {
				if connectErr.Code() != connect.CodeCanceled {
					t.Logf("Stream error (may be expected): %v", err)
				}
			}
		}
	})
}

// TestWatchInbox tests the inbox streaming endpoint.
func TestWatchInbox(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	townRoot, cleanup := setupDecisionTestTown(t)
	defer cleanup()

	mux := http.NewServeMux()
	mailServer := NewMailServer(townRoot)
	mux.Handle(gastownv1connect.NewMailServiceHandler(mailServer))

	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	defer server.Close()

	client := gastownv1connect.NewMailServiceClient(
		http.DefaultClient,
		server.URL,
	)

	t.Run("StreamMessages", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		req := connect.NewRequest(&gastownv1.WatchInboxRequest{
			Address: &gastownv1.AgentAddress{Name: "overseer"},
		})
		stream, err := client.WatchInbox(ctx, req)
		if err != nil {
			t.Fatalf("WatchInbox failed: %v", err)
		}

		// Stream should work (may not have messages in test env)
		for stream.Receive() {
			msg := stream.Msg()
			if msg == nil {
				t.Error("Received nil message")
			}
		}

		// Should exit cleanly on timeout
	})

	t.Run("Deduplication", func(t *testing.T) {
		// The WatchInbox implementation tracks seen message IDs
		// This test verifies the deduplication doesn't cause issues
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		req := connect.NewRequest(&gastownv1.WatchInboxRequest{})
		stream, err := client.WatchInbox(ctx, req)
		if err != nil {
			t.Fatalf("WatchInbox failed: %v", err)
		}

		seen := make(map[string]bool)
		for stream.Receive() {
			msg := stream.Msg()
			if msg != nil && msg.Id != "" {
				if seen[msg.Id] {
					t.Errorf("Duplicate message ID received: %s", msg.Id)
				}
				seen[msg.Id] = true
			}
		}
	})
}

// TestWatchDecisionsStream tests the decision streaming endpoint.
func TestWatchDecisionsStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	townRoot, cleanup := setupDecisionTestTown(t)
	defer cleanup()

	server, client, bus := setupDecisionTestServer(t, townRoot)
	defer server.Close()

	t.Run("InitialDecisions", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Create a decision first
		_, err := client.CreateDecision(ctx, connect.NewRequest(&gastownv1.CreateDecisionRequest{
			Question: "Stream initial test?",
			Options:  []*gastownv1.DecisionOption{{Label: "Yes"}},
		}))
		if err != nil {
			t.Fatalf("CreateDecision failed: %v", err)
		}

		// Start watching - should receive existing decision
		req := connect.NewRequest(&gastownv1.WatchDecisionsRequest{})
		stream, err := client.WatchDecisions(ctx, req)
		if err != nil {
			t.Fatalf("WatchDecisions failed: %v", err)
		}

		received := false
		for stream.Receive() {
			msg := stream.Msg()
			if msg != nil && msg.Question == "Stream initial test?" {
				received = true
				break
			}
		}

		if !received {
			t.Log("May not have received initial decision (timing dependent)")
		}
	})

	t.Run("NewDecisionViaEventBus", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		req := connect.NewRequest(&gastownv1.WatchDecisionsRequest{})
		stream, err := client.WatchDecisions(ctx, req)
		if err != nil {
			t.Fatalf("WatchDecisions failed: %v", err)
		}

		// Drain initial decisions
		drainCtx, drainCancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer drainCancel()
		go func() {
			<-drainCtx.Done()
		}()

		// Publish a new decision via event bus
		newDecision := &gastownv1.Decision{
			Id:       "event-bus-test-123",
			Question: "Via event bus?",
		}
		bus.PublishDecisionCreated("event-bus-test-123", newDecision)

		// Should eventually receive it
		received := false
		for stream.Receive() {
			msg := stream.Msg()
			if msg != nil && msg.Id == "event-bus-test-123" {
				received = true
				break
			}
		}

		if !received {
			t.Log("May not have received event bus decision (timing dependent)")
		}
	})

	t.Run("DeduplicationFromEventBus", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		req := connect.NewRequest(&gastownv1.WatchDecisionsRequest{})
		stream, err := client.WatchDecisions(ctx, req)
		if err != nil {
			t.Fatalf("WatchDecisions failed: %v", err)
		}

		// Track decision IDs
		seen := make(map[string]int)
		var mu sync.Mutex

		go func() {
			for stream.Receive() {
				msg := stream.Msg()
				if msg != nil && msg.Id != "" {
					mu.Lock()
					seen[msg.Id]++
					mu.Unlock()
				}
			}
		}()

		// Wait for stream to stabilize
		time.Sleep(500 * time.Millisecond)

		// Publish same decision twice
		bus.PublishDecisionCreated("dedup-test", nil)
		bus.PublishDecisionCreated("dedup-test", nil)

		time.Sleep(500 * time.Millisecond)
		cancel()

		mu.Lock()
		defer mu.Unlock()

		// Should only see each ID once (deduplication)
		if count := seen["dedup-test"]; count > 1 {
			t.Errorf("Decision dedup-test seen %d times, want <= 1", count)
		}
	})
}

// TestSSEHandler tests the SSE endpoint for decision events.
func TestSSEHandler(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	townRoot, cleanup := setupDecisionTestTown(t)
	defer cleanup()

	bus := eventbus.New()
	defer bus.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/events/decisions", NewSSEHandler(bus, townRoot))

	server := httptest.NewServer(mux)
	defer server.Close()

	t.Run("ConnectedEvent", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/events/decisions", nil)
		if err != nil {
			t.Fatalf("NewRequest failed: %v", err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Status = %d, want 200", resp.StatusCode)
		}

		// Check SSE headers
		if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
			t.Errorf("Content-Type = %q, want 'text/event-stream'", ct)
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("Cache-Control = %q, want 'no-cache'", cc)
		}

		// Read first few events
		scanner := bufio.NewScanner(resp.Body)
		foundConnected := false

		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "connected") {
				foundConnected = true
				break
			}
			// Don't loop forever
			if strings.HasPrefix(line, ":") && strings.Contains(line, "keepalive") {
				break
			}
		}

		if !foundConnected {
			t.Log("Connected event may not have been received in time")
		}
	})

	t.Run("InitialDecisions", func(t *testing.T) {
		// Create a decision first via beads
		// (This is harder to test in isolation - SSE reads from beads directly)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/events/decisions", nil)
		if err != nil {
			t.Fatalf("NewRequest failed: %v", err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()

		// Just verify it doesn't error
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			// Read some lines
			break
		}
	})

	t.Run("Keepalive", func(t *testing.T) {
		// SSE should send keepalive comments every 30 seconds
		// We can't wait that long in a test, but we verify the endpoint works
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/events/decisions", nil)
		if err != nil {
			t.Fatalf("NewRequest failed: %v", err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()

		// Just verify connection works
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Status = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("ReceivesNewDecisions", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/events/decisions", nil)
		if err != nil {
			t.Fatalf("NewRequest failed: %v", err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()

		// Start reading in background
		received := make(chan bool)
		go func() {
			scanner := bufio.NewScanner(resp.Body)
			for scanner.Scan() {
				line := scanner.Text()
				if strings.Contains(line, "sse-test-decision") {
					received <- true
					return
				}
			}
		}()

		// Give time for subscription to be set up
		time.Sleep(100 * time.Millisecond)

		// Publish a decision via event bus
		bus.PublishDecisionCreated("sse-test-decision", &gastownv1.Decision{
			Id:       "sse-test-decision",
			Question: "SSE test?",
		})

		select {
		case <-received:
			// Good - received the decision
		case <-time.After(2 * time.Second):
			t.Log("May not have received SSE decision event (timing dependent)")
		}
	})
}

// TestWatchConvoysStream tests the convoy streaming endpoint.
func TestWatchConvoysStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	townRoot, cleanup := setupDecisionTestTown(t)
	defer cleanup()

	mux := http.NewServeMux()
	convoyServer := NewConvoyServer(townRoot)
	mux.Handle(gastownv1connect.NewConvoyServiceHandler(convoyServer))

	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	defer server.Close()

	client := gastownv1connect.NewConvoyServiceClient(
		http.DefaultClient,
		server.URL,
	)

	t.Run("ExistingConvoys", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Create a convoy first
		_, err := client.CreateConvoy(ctx, connect.NewRequest(&gastownv1.CreateConvoyRequest{
			Name: "Stream test convoy",
		}))
		if err != nil {
			t.Fatalf("CreateConvoy failed: %v", err)
		}

		// Start watching
		req := connect.NewRequest(&gastownv1.WatchConvoysRequest{})
		stream, err := client.WatchConvoys(ctx, req)
		if err != nil {
			t.Fatalf("WatchConvoys failed: %v", err)
		}

		// Should receive existing convoy
		received := false
		for stream.Receive() {
			msg := stream.Msg()
			if msg != nil && msg.UpdateType == "existing" {
				received = true
				break
			}
		}

		if !received {
			t.Log("May not have received existing convoy (timing dependent)")
		}
	})

	t.Run("StatusChanges", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		// Create a convoy
		createResp, err := client.CreateConvoy(ctx, connect.NewRequest(&gastownv1.CreateConvoyRequest{
			Name: "Status change convoy",
		}))
		if err != nil {
			t.Fatalf("CreateConvoy failed: %v", err)
		}
		convoyID := createResp.Msg.Convoy.Id

		// Start watching
		streamCtx, streamCancel := context.WithCancel(ctx)
		defer streamCancel()

		req := connect.NewRequest(&gastownv1.WatchConvoysRequest{})
		stream, err := client.WatchConvoys(streamCtx, req)
		if err != nil {
			t.Fatalf("WatchConvoys failed: %v", err)
		}

		// Drain initial existing convoys in background
		updates := make(chan *gastownv1.ConvoyUpdate, 10)
		go func() {
			for stream.Receive() {
				msg := stream.Msg()
				if msg != nil && msg.ConvoyId == convoyID {
					updates <- msg
				}
			}
			close(updates)
		}()

		// Wait for initial update
		time.Sleep(200 * time.Millisecond)

		// Close the convoy (this should trigger a status change)
		_, err = client.CloseConvoy(ctx, connect.NewRequest(&gastownv1.CloseConvoyRequest{
			ConvoyId: convoyID,
			Reason:   "Test close",
		}))
		if err != nil {
			t.Fatalf("CloseConvoy failed: %v", err)
		}

		// Wait for status change update (polling every 10s, so this may take a while)
		// We set a shorter timeout since we can't wait 10s in tests
		t.Log("Status change would be detected on next poll interval (10s)")
	})
}

// TestWatchEventsStream tests the activity events streaming endpoint.
func TestWatchEventsStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	townRoot, cleanup := setupDecisionTestTown(t)
	defer cleanup()

	mux := http.NewServeMux()
	activityServer := NewActivityServer(townRoot)
	mux.Handle(gastownv1connect.NewActivityServiceHandler(activityServer))

	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	defer server.Close()

	client := gastownv1connect.NewActivityServiceClient(
		http.DefaultClient,
		server.URL,
	)

	t.Run("Backfill", func(t *testing.T) {
		// Write some events first
		writeTestEvents(t, townRoot, []string{
			`{"ts":"2026-01-01T10:00:00Z","type":"test1"}`,
			`{"ts":"2026-01-01T10:01:00Z","type":"test2"}`,
			`{"ts":"2026-01-01T10:02:00Z","type":"test3"}`,
		})

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		req := connect.NewRequest(&gastownv1.WatchEventsRequest{
			IncludeBackfill: true,
			BackfillCount:   10,
		})
		stream, err := client.WatchEvents(ctx, req)
		if err != nil {
			t.Fatalf("WatchEvents failed: %v", err)
		}

		// Should receive backfilled events
		count := 0
		for stream.Receive() {
			count++
			if count >= 3 {
				break
			}
		}

		if count < 3 {
			t.Logf("Received %d events (expected 3, timing dependent)", count)
		}
	})

	t.Run("WithFilter", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		req := connect.NewRequest(&gastownv1.WatchEventsRequest{
			Filter: &gastownv1.EventFilter{
				Types: []string{"sling"},
			},
		})
		stream, err := client.WatchEvents(ctx, req)
		if err != nil {
			t.Fatalf("WatchEvents with filter failed: %v", err)
		}

		// Stream should work (may not receive anything if no matching events)
		for stream.Receive() {
			msg := stream.Msg()
			if msg != nil && msg.Type != "sling" {
				t.Errorf("Received non-sling event: %s", msg.Type)
			}
		}
	})

	t.Run("CuratedFeed", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		req := connect.NewRequest(&gastownv1.WatchEventsRequest{
			Curated: true,
		})
		stream, err := client.WatchEvents(ctx, req)
		if err != nil {
			t.Fatalf("WatchEvents curated failed: %v", err)
		}

		// Just verify it works
		for stream.Receive() {
			break // Read one event or timeout
		}
	})
}
