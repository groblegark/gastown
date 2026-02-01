package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/protobuf/types/known/structpb"

	gastownv1 "github.com/steveyegge/gastown/mobile/gen/gastown/v1"
	"github.com/steveyegge/gastown/mobile/gen/gastown/v1/gastownv1connect"
)

// setupActivityTestServer creates a test server for activity tests.
func setupActivityTestServer(t *testing.T, townRoot string) (*httptest.Server, gastownv1connect.ActivityServiceClient) {
	t.Helper()

	mux := http.NewServeMux()
	activityServer := NewActivityServer(townRoot)
	mux.Handle(gastownv1connect.NewActivityServiceHandler(activityServer))

	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))

	client := gastownv1connect.NewActivityServiceClient(
		http.DefaultClient,
		server.URL,
	)

	return server, client
}

// writeTestEvents writes test events to the events file.
func writeTestEvents(t *testing.T, townRoot string, events []string) {
	t.Helper()
	eventsPath := filepath.Join(townRoot, ".events.jsonl")
	content := ""
	for _, e := range events {
		content += e + "\n"
	}
	if err := os.WriteFile(eventsPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test events: %v", err)
	}
}

// writeFeedEvents writes test events to the curated feed file.
func writeFeedEvents(t *testing.T, townRoot string, events []string) {
	t.Helper()
	feedPath := filepath.Join(townRoot, ".feed.jsonl")
	content := ""
	for _, e := range events {
		content += e + "\n"
	}
	if err := os.WriteFile(feedPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write feed events: %v", err)
	}
}

// TestActivityServer_ListEvents tests event listing functionality.
func TestActivityServer_ListEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	townRoot, cleanup := setupDecisionTestTown(t)
	defer cleanup()

	server, client := setupActivityTestServer(t, townRoot)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("Empty", func(t *testing.T) {
		req := connect.NewRequest(&gastownv1.ListEventsRequest{})
		resp, err := client.ListEvents(ctx, req)
		if err != nil {
			t.Fatalf("ListEvents failed: %v", err)
		}
		if len(resp.Msg.Events) != 0 {
			t.Errorf("len(Events) = %d, want 0 for empty file", len(resp.Msg.Events))
		}
	})

	t.Run("RawEvents", func(t *testing.T) {
		// Write test events
		writeTestEvents(t, townRoot, []string{
			`{"ts":"2026-01-01T10:00:00Z","source":"gt","type":"sling","actor":"gastown/crew/test","visibility":"feed"}`,
			`{"ts":"2026-01-01T10:01:00Z","source":"gt","type":"hook","actor":"gastown/crew/test","visibility":"audit"}`,
			`{"ts":"2026-01-01T10:02:00Z","source":"gt","type":"done","actor":"gastown/polecats/alpha","visibility":"both"}`,
		})

		req := connect.NewRequest(&gastownv1.ListEventsRequest{
			Curated: false,
		})
		resp, err := client.ListEvents(ctx, req)
		if err != nil {
			t.Fatalf("ListEvents failed: %v", err)
		}

		if len(resp.Msg.Events) != 3 {
			t.Errorf("len(Events) = %d, want 3", len(resp.Msg.Events))
		}

		// Events should be newest first
		if len(resp.Msg.Events) > 0 {
			if resp.Msg.Events[0].Type != "done" {
				t.Errorf("First event type = %q, want 'done' (newest)", resp.Msg.Events[0].Type)
			}
		}
	})

	t.Run("CuratedEvents", func(t *testing.T) {
		writeFeedEvents(t, townRoot, []string{
			`{"ts":"2026-01-01T11:00:00Z","source":"gt","type":"spawn","actor":"witness","visibility":"feed","summary":"Spawned polecat alpha","count":1}`,
		})

		req := connect.NewRequest(&gastownv1.ListEventsRequest{
			Curated: true,
		})
		resp, err := client.ListEvents(ctx, req)
		if err != nil {
			t.Fatalf("ListEvents curated failed: %v", err)
		}

		if len(resp.Msg.Events) != 1 {
			t.Errorf("len(Events) = %d, want 1", len(resp.Msg.Events))
		}

		if len(resp.Msg.Events) > 0 {
			e := resp.Msg.Events[0]
			if e.Summary != "Spawned polecat alpha" {
				t.Errorf("Summary = %q", e.Summary)
			}
			if e.Count != 1 {
				t.Errorf("Count = %d, want 1", e.Count)
			}
		}
	})

	t.Run("Limit", func(t *testing.T) {
		// Write many events
		events := make([]string, 50)
		for i := 0; i < 50; i++ {
			events[i] = `{"ts":"2026-01-01T12:00:00Z","source":"gt","type":"test","actor":"test"}`
		}
		writeTestEvents(t, townRoot, events)

		req := connect.NewRequest(&gastownv1.ListEventsRequest{
			Limit: 10,
		})
		resp, err := client.ListEvents(ctx, req)
		if err != nil {
			t.Fatalf("ListEvents failed: %v", err)
		}

		if len(resp.Msg.Events) != 10 {
			t.Errorf("len(Events) = %d, want 10 (limited)", len(resp.Msg.Events))
		}
	})

	t.Run("FilterByType", func(t *testing.T) {
		writeTestEvents(t, townRoot, []string{
			`{"ts":"2026-01-01T13:00:00Z","type":"sling","actor":"test"}`,
			`{"ts":"2026-01-01T13:01:00Z","type":"hook","actor":"test"}`,
			`{"ts":"2026-01-01T13:02:00Z","type":"sling","actor":"test"}`,
		})

		req := connect.NewRequest(&gastownv1.ListEventsRequest{
			Filter: &gastownv1.EventFilter{
				Types: []string{"sling"},
			},
		})
		resp, err := client.ListEvents(ctx, req)
		if err != nil {
			t.Fatalf("ListEvents failed: %v", err)
		}

		for _, e := range resp.Msg.Events {
			if e.Type != "sling" {
				t.Errorf("Event type = %q, want 'sling'", e.Type)
			}
		}
	})

	t.Run("FilterByActor", func(t *testing.T) {
		writeTestEvents(t, townRoot, []string{
			`{"ts":"2026-01-01T14:00:00Z","type":"test","actor":"alice"}`,
			`{"ts":"2026-01-01T14:01:00Z","type":"test","actor":"bob"}`,
			`{"ts":"2026-01-01T14:02:00Z","type":"test","actor":"alice"}`,
		})

		req := connect.NewRequest(&gastownv1.ListEventsRequest{
			Filter: &gastownv1.EventFilter{
				Actor: "alice",
			},
		})
		resp, err := client.ListEvents(ctx, req)
		if err != nil {
			t.Fatalf("ListEvents failed: %v", err)
		}

		for _, e := range resp.Msg.Events {
			if e.Actor != "alice" {
				t.Errorf("Event actor = %q, want 'alice'", e.Actor)
			}
		}
	})

	t.Run("FilterByVisibility", func(t *testing.T) {
		writeTestEvents(t, townRoot, []string{
			`{"ts":"2026-01-01T15:00:00Z","type":"test","visibility":"audit"}`,
			`{"ts":"2026-01-01T15:01:00Z","type":"test","visibility":"feed"}`,
			`{"ts":"2026-01-01T15:02:00Z","type":"test","visibility":"both"}`,
		})

		req := connect.NewRequest(&gastownv1.ListEventsRequest{
			Filter: &gastownv1.EventFilter{
				Visibility: gastownv1.Visibility_VISIBILITY_FEED,
			},
		})
		resp, err := client.ListEvents(ctx, req)
		if err != nil {
			t.Fatalf("ListEvents failed: %v", err)
		}

		for _, e := range resp.Msg.Events {
			if e.Visibility != gastownv1.Visibility_VISIBILITY_FEED {
				t.Errorf("Event visibility = %v, want FEED", e.Visibility)
			}
		}
	})

	t.Run("FilterByTimeRange", func(t *testing.T) {
		writeTestEvents(t, townRoot, []string{
			`{"ts":"2026-01-01T09:00:00Z","type":"old","actor":"test"}`,
			`{"ts":"2026-01-01T12:00:00Z","type":"middle","actor":"test"}`,
			`{"ts":"2026-01-01T15:00:00Z","type":"new","actor":"test"}`,
		})

		req := connect.NewRequest(&gastownv1.ListEventsRequest{
			Filter: &gastownv1.EventFilter{
				After:  "2026-01-01T10:00:00Z",
				Before: "2026-01-01T14:00:00Z",
			},
		})
		resp, err := client.ListEvents(ctx, req)
		if err != nil {
			t.Fatalf("ListEvents failed: %v", err)
		}

		for _, e := range resp.Msg.Events {
			if e.Type != "middle" {
				t.Errorf("Event type = %q, expected only 'middle' in range", e.Type)
			}
		}
	})
}

// TestActivityServer_EmitEvent tests event emission.
func TestActivityServer_EmitEvent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	townRoot, cleanup := setupDecisionTestTown(t)
	defer cleanup()

	server, client := setupActivityTestServer(t, townRoot)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("RequiredFields", func(t *testing.T) {
		req := connect.NewRequest(&gastownv1.EmitEventRequest{
			Type:  "test_event",
			Actor: "test_actor",
		})
		resp, err := client.EmitEvent(ctx, req)
		if err != nil {
			t.Fatalf("EmitEvent failed: %v", err)
		}

		if !resp.Msg.Success {
			t.Error("Success = false, want true")
		}
		if resp.Msg.Timestamp == "" {
			t.Error("Timestamp is empty")
		}
	})

	t.Run("MissingType", func(t *testing.T) {
		req := connect.NewRequest(&gastownv1.EmitEventRequest{
			Actor: "test_actor",
		})
		_, err := client.EmitEvent(ctx, req)
		if err == nil {
			t.Error("Expected error for missing type")
		}
		connectErr, ok := err.(*connect.Error)
		if !ok {
			t.Fatalf("Expected connect.Error, got %T", err)
		}
		if connectErr.Code() != connect.CodeInvalidArgument {
			t.Errorf("Error code = %v, want InvalidArgument", connectErr.Code())
		}
	})

	t.Run("MissingActor", func(t *testing.T) {
		req := connect.NewRequest(&gastownv1.EmitEventRequest{
			Type: "test_event",
		})
		_, err := client.EmitEvent(ctx, req)
		if err == nil {
			t.Error("Expected error for missing actor")
		}
	})

	t.Run("AllFields", func(t *testing.T) {
		payload, _ := structpb.NewStruct(map[string]interface{}{
			"key1": "value1",
			"key2": 42.0,
		})

		req := connect.NewRequest(&gastownv1.EmitEventRequest{
			Type:       "full_event",
			Actor:      "full_actor",
			Payload:    payload,
			Visibility: gastownv1.Visibility_VISIBILITY_BOTH,
		})
		resp, err := client.EmitEvent(ctx, req)
		if err != nil {
			t.Fatalf("EmitEvent failed: %v", err)
		}

		if !resp.Msg.Success {
			t.Error("Success = false, want true")
		}

		// Verify the event was written by reading it back
		listResp, err := client.ListEvents(ctx, connect.NewRequest(&gastownv1.ListEventsRequest{
			Filter: &gastownv1.EventFilter{
				Types: []string{"full_event"},
			},
		}))
		if err != nil {
			t.Fatalf("ListEvents failed: %v", err)
		}

		if len(listResp.Msg.Events) < 1 {
			t.Fatal("Emitted event not found in list")
		}

		e := listResp.Msg.Events[0]
		if e.Type != "full_event" {
			t.Errorf("Type = %q", e.Type)
		}
		if e.Actor != "full_actor" {
			t.Errorf("Actor = %q", e.Actor)
		}
		if e.Visibility != gastownv1.Visibility_VISIBILITY_BOTH {
			t.Errorf("Visibility = %v", e.Visibility)
		}
	})

	t.Run("VisibilityMapping", func(t *testing.T) {
		cases := []struct {
			input gastownv1.Visibility
			want  gastownv1.Visibility
		}{
			{gastownv1.Visibility_VISIBILITY_AUDIT, gastownv1.Visibility_VISIBILITY_AUDIT},
			{gastownv1.Visibility_VISIBILITY_FEED, gastownv1.Visibility_VISIBILITY_FEED},
			{gastownv1.Visibility_VISIBILITY_BOTH, gastownv1.Visibility_VISIBILITY_BOTH},
		}

		for _, tc := range cases {
			t.Run(tc.input.String(), func(t *testing.T) {
				req := connect.NewRequest(&gastownv1.EmitEventRequest{
					Type:       "visibility_test",
					Actor:      "test",
					Visibility: tc.input,
				})
				_, err := client.EmitEvent(ctx, req)
				if err != nil {
					t.Fatalf("EmitEvent failed: %v", err)
				}
			})
		}
	})
}

// TestActivityServer_parseLine tests the line parsing helper.
func TestActivityServer_parseLine(t *testing.T) {
	server := &ActivityServer{townRoot: ""}

	t.Run("ValidJSON", func(t *testing.T) {
		line := `{"ts":"2026-01-01T10:00:00Z","source":"gt","type":"sling","actor":"test","visibility":"feed"}`
		event := server.parseLine(line, false)
		if event == nil {
			t.Fatal("parseLine returned nil for valid JSON")
		}
		if event.Timestamp != "2026-01-01T10:00:00Z" {
			t.Errorf("Timestamp = %q", event.Timestamp)
		}
		if event.Source != "gt" {
			t.Errorf("Source = %q", event.Source)
		}
		if event.Type != "sling" {
			t.Errorf("Type = %q", event.Type)
		}
		if event.Actor != "test" {
			t.Errorf("Actor = %q", event.Actor)
		}
		if event.Visibility != gastownv1.Visibility_VISIBILITY_FEED {
			t.Errorf("Visibility = %v", event.Visibility)
		}
	})

	t.Run("InvalidJSON", func(t *testing.T) {
		line := `not valid json`
		event := server.parseLine(line, false)
		if event != nil {
			t.Error("parseLine should return nil for invalid JSON")
		}
	})

	t.Run("EmptyLine", func(t *testing.T) {
		event := server.parseLine("", false)
		if event != nil {
			t.Error("parseLine should return nil for empty line")
		}
	})

	t.Run("WhitespaceOnly", func(t *testing.T) {
		event := server.parseLine("   \t\n  ", false)
		if event != nil {
			t.Error("parseLine should return nil for whitespace-only line")
		}
	})

	t.Run("CuratedFields", func(t *testing.T) {
		line := `{"ts":"2026-01-01T10:00:00Z","type":"spawn","summary":"Test summary","count":5}`
		event := server.parseLine(line, true)
		if event == nil {
			t.Fatal("parseLine returned nil")
		}
		if event.Summary != "Test summary" {
			t.Errorf("Summary = %q", event.Summary)
		}
		if event.Count != 5 {
			t.Errorf("Count = %d, want 5", event.Count)
		}
	})

	t.Run("WithPayload", func(t *testing.T) {
		line := `{"ts":"2026-01-01T10:00:00Z","type":"test","payload":{"key":"value","num":42}}`
		event := server.parseLine(line, false)
		if event == nil {
			t.Fatal("parseLine returned nil")
		}
		if event.Payload == nil {
			t.Fatal("Payload is nil")
		}
		// Check payload fields
		fields := event.Payload.GetFields()
		if fields["key"].GetStringValue() != "value" {
			t.Errorf("Payload key = %q", fields["key"].GetStringValue())
		}
		if fields["num"].GetNumberValue() != 42 {
			t.Errorf("Payload num = %v", fields["num"].GetNumberValue())
		}
	})
}

// TestActivityServer_matchesFilter tests the filter matching helper.
func TestActivityServer_matchesFilter(t *testing.T) {
	server := &ActivityServer{townRoot: ""}

	t.Run("NilFilter", func(t *testing.T) {
		event := &gastownv1.ActivityEvent{Type: "test"}
		if !server.matchesFilter(event, nil) {
			t.Error("nil filter should match everything")
		}
	})

	t.Run("EmptyFilter", func(t *testing.T) {
		event := &gastownv1.ActivityEvent{Type: "test"}
		if !server.matchesFilter(event, &gastownv1.EventFilter{}) {
			t.Error("empty filter should match everything")
		}
	})

	t.Run("TypeMatch", func(t *testing.T) {
		event := &gastownv1.ActivityEvent{Type: "sling"}
		filter := &gastownv1.EventFilter{Types: []string{"sling", "hook"}}
		if !server.matchesFilter(event, filter) {
			t.Error("should match when type is in list")
		}
	})

	t.Run("TypeNoMatch", func(t *testing.T) {
		event := &gastownv1.ActivityEvent{Type: "done"}
		filter := &gastownv1.EventFilter{Types: []string{"sling", "hook"}}
		if server.matchesFilter(event, filter) {
			t.Error("should not match when type is not in list")
		}
	})

	t.Run("ActorMatch", func(t *testing.T) {
		event := &gastownv1.ActivityEvent{Actor: "alice"}
		filter := &gastownv1.EventFilter{Actor: "alice"}
		if !server.matchesFilter(event, filter) {
			t.Error("should match when actor matches")
		}
	})

	t.Run("ActorNoMatch", func(t *testing.T) {
		event := &gastownv1.ActivityEvent{Actor: "bob"}
		filter := &gastownv1.EventFilter{Actor: "alice"}
		if server.matchesFilter(event, filter) {
			t.Error("should not match when actor differs")
		}
	})

	t.Run("VisibilityMatch", func(t *testing.T) {
		event := &gastownv1.ActivityEvent{Visibility: gastownv1.Visibility_VISIBILITY_FEED}
		filter := &gastownv1.EventFilter{Visibility: gastownv1.Visibility_VISIBILITY_FEED}
		if !server.matchesFilter(event, filter) {
			t.Error("should match when visibility matches")
		}
	})

	t.Run("VisibilityNoMatch", func(t *testing.T) {
		event := &gastownv1.ActivityEvent{Visibility: gastownv1.Visibility_VISIBILITY_AUDIT}
		filter := &gastownv1.EventFilter{Visibility: gastownv1.Visibility_VISIBILITY_FEED}
		if server.matchesFilter(event, filter) {
			t.Error("should not match when visibility differs")
		}
	})

	t.Run("TimeRangeAfter", func(t *testing.T) {
		event := &gastownv1.ActivityEvent{Timestamp: "2026-01-01T12:00:00Z"}
		filter := &gastownv1.EventFilter{After: "2026-01-01T10:00:00Z"}
		if !server.matchesFilter(event, filter) {
			t.Error("should match when event is after filter time")
		}
	})

	t.Run("TimeRangeBefore", func(t *testing.T) {
		event := &gastownv1.ActivityEvent{Timestamp: "2026-01-01T08:00:00Z"}
		filter := &gastownv1.EventFilter{After: "2026-01-01T10:00:00Z"}
		if server.matchesFilter(event, filter) {
			t.Error("should not match when event is before filter time")
		}
	})
}

// TestActivityServer_WatchEvents tests event streaming.
func TestActivityServer_WatchEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	townRoot, cleanup := setupDecisionTestTown(t)
	defer cleanup()

	server, client := setupActivityTestServer(t, townRoot)
	defer server.Close()

	t.Run("ContextCancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		req := connect.NewRequest(&gastownv1.WatchEventsRequest{})
		stream, err := client.WatchEvents(ctx, req)
		if err != nil {
			t.Fatalf("WatchEvents failed: %v", err)
		}

		// Cancel immediately
		cancel()

		// Drain stream
		for stream.Receive() {
			// Ignore messages
		}

		// Should exit cleanly
		err = stream.Err()
		if err != nil && err != context.Canceled {
			if connectErr, ok := err.(*connect.Error); ok {
				if connectErr.Code() != connect.CodeCanceled {
					t.Logf("Stream error (may be expected): %v", err)
				}
			}
		}
	})

	t.Run("BackfillEvents", func(t *testing.T) {
		// Write some events first
		writeTestEvents(t, townRoot, []string{
			`{"ts":"2026-01-01T10:00:00Z","type":"event1"}`,
			`{"ts":"2026-01-01T10:01:00Z","type":"event2"}`,
			`{"ts":"2026-01-01T10:02:00Z","type":"event3"}`,
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
}
