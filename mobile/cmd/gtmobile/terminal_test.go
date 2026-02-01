package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	gastownv1 "github.com/steveyegge/gastown/mobile/gen/gastown/v1"
	"github.com/steveyegge/gastown/mobile/gen/gastown/v1/gastownv1connect"
)

// setupTerminalTestServer creates a test server for terminal tests.
func setupTerminalTestServer(t *testing.T) (*httptest.Server, gastownv1connect.TerminalServiceClient) {
	t.Helper()

	mux := http.NewServeMux()
	terminalServer := NewTerminalServer()
	mux.Handle(gastownv1connect.NewTerminalServiceHandler(terminalServer))

	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))

	client := gastownv1connect.NewTerminalServiceClient(
		http.DefaultClient,
		server.URL,
	)

	return server, client
}

// isTmuxAvailable checks if tmux is available for testing.
func isTmuxAvailable() bool {
	server := NewTerminalServer()
	_, err := server.tmux.ListSessions()
	return err == nil
}

// TestTerminalServer_ListSessions tests session listing.
func TestTerminalServer_ListSessions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if !isTmuxAvailable() {
		t.Skip("tmux not available")
	}

	server, client := setupTerminalTestServer(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("All", func(t *testing.T) {
		req := connect.NewRequest(&gastownv1.ListSessionsRequest{})
		resp, err := client.ListSessions(ctx, req)
		if err != nil {
			t.Fatalf("ListSessions failed: %v", err)
		}

		// Should return list (may be empty if no tmux sessions)
		t.Logf("Found %d sessions", len(resp.Msg.Sessions))
	})

	t.Run("PrefixFilter", func(t *testing.T) {
		req := connect.NewRequest(&gastownv1.ListSessionsRequest{
			Prefix: "gt-",
		})
		resp, err := client.ListSessions(ctx, req)
		if err != nil {
			t.Fatalf("ListSessions with prefix failed: %v", err)
		}

		// All returned sessions should have the prefix
		for _, sess := range resp.Msg.Sessions {
			if len(sess) < 3 || sess[:3] != "gt-" {
				t.Errorf("Session %q doesn't have 'gt-' prefix", sess)
			}
		}
	})

	t.Run("EmptyPrefix", func(t *testing.T) {
		req := connect.NewRequest(&gastownv1.ListSessionsRequest{
			Prefix: "",
		})
		resp, err := client.ListSessions(ctx, req)
		if err != nil {
			t.Fatalf("ListSessions failed: %v", err)
		}

		// Empty prefix should return all sessions
		t.Logf("Found %d sessions with empty prefix", len(resp.Msg.Sessions))
	})

	t.Run("NonMatchingPrefix", func(t *testing.T) {
		req := connect.NewRequest(&gastownv1.ListSessionsRequest{
			Prefix: "unlikely-prefix-xyz-",
		})
		resp, err := client.ListSessions(ctx, req)
		if err != nil {
			t.Fatalf("ListSessions failed: %v", err)
		}

		// Should return empty list
		if len(resp.Msg.Sessions) != 0 {
			t.Errorf("Expected 0 sessions with non-matching prefix, got %d", len(resp.Msg.Sessions))
		}
	})
}

// TestTerminalServer_HasSession tests session existence checks.
func TestTerminalServer_HasSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if !isTmuxAvailable() {
		t.Skip("tmux not available")
	}

	server, client := setupTerminalTestServer(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("NotExists", func(t *testing.T) {
		req := connect.NewRequest(&gastownv1.HasSessionRequest{
			Session: "nonexistent-session-xyz-123",
		})
		resp, err := client.HasSession(ctx, req)
		if err != nil {
			t.Fatalf("HasSession failed: %v", err)
		}

		if resp.Msg.Exists {
			t.Error("Exists = true for nonexistent session")
		}
	})

	t.Run("MissingSession", func(t *testing.T) {
		req := connect.NewRequest(&gastownv1.HasSessionRequest{
			Session: "",
		})
		_, err := client.HasSession(ctx, req)
		if err == nil {
			t.Error("Expected error for empty session name")
		}
		connectErr, ok := err.(*connect.Error)
		if !ok {
			t.Fatalf("Expected connect.Error, got %T", err)
		}
		if connectErr.Code() != connect.CodeInvalidArgument {
			t.Errorf("Error code = %v, want InvalidArgument", connectErr.Code())
		}
	})

	t.Run("ExistingSession", func(t *testing.T) {
		// First, list sessions to find one that exists
		listReq := connect.NewRequest(&gastownv1.ListSessionsRequest{})
		listResp, err := client.ListSessions(ctx, listReq)
		if err != nil {
			t.Fatalf("ListSessions failed: %v", err)
		}

		if len(listResp.Msg.Sessions) == 0 {
			t.Skip("No sessions available to test HasSession with existing")
		}

		// Check that the first session exists
		req := connect.NewRequest(&gastownv1.HasSessionRequest{
			Session: listResp.Msg.Sessions[0],
		})
		resp, err := client.HasSession(ctx, req)
		if err != nil {
			t.Fatalf("HasSession failed: %v", err)
		}

		if !resp.Msg.Exists {
			t.Errorf("Exists = false for session %q that was in list", listResp.Msg.Sessions[0])
		}
	})
}

// TestTerminalServer_PeekSession tests session output capture.
func TestTerminalServer_PeekSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if !isTmuxAvailable() {
		t.Skip("tmux not available")
	}

	server, client := setupTerminalTestServer(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("NotExists", func(t *testing.T) {
		req := connect.NewRequest(&gastownv1.PeekSessionRequest{
			Session: "nonexistent-session-xyz-123",
		})
		resp, err := client.PeekSession(ctx, req)
		if err != nil {
			t.Fatalf("PeekSession failed: %v", err)
		}

		if resp.Msg.Exists {
			t.Error("Exists = true for nonexistent session")
		}
		if resp.Msg.Output != "" {
			t.Error("Output should be empty for nonexistent session")
		}
	})

	t.Run("MissingSession", func(t *testing.T) {
		req := connect.NewRequest(&gastownv1.PeekSessionRequest{
			Session: "",
		})
		_, err := client.PeekSession(ctx, req)
		if err == nil {
			t.Error("Expected error for empty session name")
		}
	})

	t.Run("LinesParam", func(t *testing.T) {
		// Find an existing session
		listResp, err := client.ListSessions(ctx, connect.NewRequest(&gastownv1.ListSessionsRequest{}))
		if err != nil {
			t.Fatalf("ListSessions failed: %v", err)
		}

		if len(listResp.Msg.Sessions) == 0 {
			t.Skip("No sessions available to test PeekSession")
		}

		session := listResp.Msg.Sessions[0]

		// Test with different line counts
		cases := []struct {
			name  string
			lines int32
		}{
			{"default", 0},
			{"small", 10},
			{"medium", 50},
			{"large", 200},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				req := connect.NewRequest(&gastownv1.PeekSessionRequest{
					Session: session,
					Lines:   tc.lines,
				})
				resp, err := client.PeekSession(ctx, req)
				if err != nil {
					t.Fatalf("PeekSession failed: %v", err)
				}

				if !resp.Msg.Exists {
					t.Error("Session should exist")
				}
			})
		}
	})

	t.Run("AllFlag", func(t *testing.T) {
		// Find an existing session
		listResp, err := client.ListSessions(ctx, connect.NewRequest(&gastownv1.ListSessionsRequest{}))
		if err != nil {
			t.Fatalf("ListSessions failed: %v", err)
		}

		if len(listResp.Msg.Sessions) == 0 {
			t.Skip("No sessions available to test PeekSession with All flag")
		}

		session := listResp.Msg.Sessions[0]

		req := connect.NewRequest(&gastownv1.PeekSessionRequest{
			Session: session,
			All:     true,
		})
		resp, err := client.PeekSession(ctx, req)
		if err != nil {
			t.Fatalf("PeekSession failed: %v", err)
		}

		if !resp.Msg.Exists {
			t.Error("Session should exist")
		}

		// All flag should capture all scrollback
		t.Logf("Captured %d lines with All flag", len(resp.Msg.Lines))
	})

	t.Run("LinesSlice", func(t *testing.T) {
		// Find an existing session
		listResp, err := client.ListSessions(ctx, connect.NewRequest(&gastownv1.ListSessionsRequest{}))
		if err != nil {
			t.Fatalf("ListSessions failed: %v", err)
		}

		if len(listResp.Msg.Sessions) == 0 {
			t.Skip("No sessions available to test PeekSession")
		}

		session := listResp.Msg.Sessions[0]

		req := connect.NewRequest(&gastownv1.PeekSessionRequest{
			Session: session,
			Lines:   10,
		})
		resp, err := client.PeekSession(ctx, req)
		if err != nil {
			t.Fatalf("PeekSession failed: %v", err)
		}

		// Lines should be populated (may be empty if no output)
		if resp.Msg.Output != "" && len(resp.Msg.Lines) == 0 {
			t.Error("Output is non-empty but Lines slice is empty")
		}
	})
}

// TestTerminalServer_WatchSession tests session streaming.
func TestTerminalServer_WatchSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if !isTmuxAvailable() {
		t.Skip("tmux not available")
	}

	server, client := setupTerminalTestServer(t)
	defer server.Close()

	t.Run("MissingSession", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		req := connect.NewRequest(&gastownv1.WatchSessionRequest{
			Session: "",
		})
		_, err := client.WatchSession(ctx, req)
		if err == nil {
			t.Error("Expected error for empty session name")
		}
	})

	t.Run("ContextCancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		// Find an existing session
		listResp, err := client.ListSessions(ctx, connect.NewRequest(&gastownv1.ListSessionsRequest{}))
		if err != nil {
			t.Fatalf("ListSessions failed: %v", err)
		}

		var session string
		if len(listResp.Msg.Sessions) > 0 {
			session = listResp.Msg.Sessions[0]
		} else {
			session = "nonexistent-session" // Will report exists=false
		}

		req := connect.NewRequest(&gastownv1.WatchSessionRequest{
			Session:    session,
			IntervalMs: 100,
		})
		stream, err := client.WatchSession(ctx, req)
		if err != nil {
			t.Fatalf("WatchSession failed: %v", err)
		}

		// Cancel after a short time
		go func() {
			time.Sleep(200 * time.Millisecond)
			cancel()
		}()

		// Drain stream
		for stream.Receive() {
			// Ignore updates
		}

		// Should exit cleanly on cancellation
		err = stream.Err()
		if err != nil && err != context.Canceled {
			if connectErr, ok := err.(*connect.Error); ok {
				if connectErr.Code() != connect.CodeCanceled {
					t.Logf("Stream error (may be expected): %v", err)
				}
			}
		}
	})

	t.Run("NonexistentSession", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		req := connect.NewRequest(&gastownv1.WatchSessionRequest{
			Session:    "nonexistent-session-xyz-123",
			IntervalMs: 100,
		})
		stream, err := client.WatchSession(ctx, req)
		if err != nil {
			t.Fatalf("WatchSession failed: %v", err)
		}

		// Should receive an update with exists=false
		if stream.Receive() {
			msg := stream.Msg()
			if msg.Exists {
				t.Error("Exists = true for nonexistent session")
			}
		}
	})

	t.Run("IntervalUpdates", func(t *testing.T) {
		// Find an existing session
		ctx := context.Background()
		listResp, err := client.ListSessions(ctx, connect.NewRequest(&gastownv1.ListSessionsRequest{}))
		if err != nil {
			t.Fatalf("ListSessions failed: %v", err)
		}

		if len(listResp.Msg.Sessions) == 0 {
			t.Skip("No sessions available to test WatchSession")
		}

		session := listResp.Msg.Sessions[0]

		// Create a short-lived context
		streamCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
		defer cancel()

		req := connect.NewRequest(&gastownv1.WatchSessionRequest{
			Session:    session,
			Lines:      20,
			IntervalMs: 200,
		})
		stream, err := client.WatchSession(streamCtx, req)
		if err != nil {
			t.Fatalf("WatchSession failed: %v", err)
		}

		// Count received updates
		count := 0
		for stream.Receive() {
			msg := stream.Msg()
			if !msg.Exists {
				t.Error("Session should exist")
			}
			if msg.Timestamp == "" {
				t.Error("Timestamp should be set")
			}
			count++
		}

		// Should have received multiple updates
		t.Logf("Received %d updates", count)
		if count < 1 {
			t.Error("Should have received at least 1 update")
		}
	})
}

// TestTerminalServer_EdgeCases tests edge cases.
func TestTerminalServer_EdgeCases(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if !isTmuxAvailable() {
		t.Skip("tmux not available")
	}

	server, client := setupTerminalTestServer(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("MaxLinesClamp", func(t *testing.T) {
		// Find an existing session
		listResp, err := client.ListSessions(ctx, connect.NewRequest(&gastownv1.ListSessionsRequest{}))
		if err != nil {
			t.Fatalf("ListSessions failed: %v", err)
		}

		if len(listResp.Msg.Sessions) == 0 {
			t.Skip("No sessions available")
		}

		// Request more than max lines (1000)
		req := connect.NewRequest(&gastownv1.PeekSessionRequest{
			Session: listResp.Msg.Sessions[0],
			Lines:   5000, // Should be clamped to 1000
		})
		resp, err := client.PeekSession(ctx, req)
		if err != nil {
			t.Fatalf("PeekSession failed: %v", err)
		}

		// Should succeed (lines clamped internally)
		if !resp.Msg.Exists {
			t.Error("Session should exist")
		}
	})

	t.Run("MinIntervalClamp", func(t *testing.T) {
		// Find an existing session
		listResp, err := client.ListSessions(ctx, connect.NewRequest(&gastownv1.ListSessionsRequest{}))
		if err != nil {
			t.Fatalf("ListSessions failed: %v", err)
		}

		if len(listResp.Msg.Sessions) == 0 {
			t.Skip("No sessions available")
		}

		// Create a short-lived context
		streamCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()

		// Request interval less than min (100ms)
		req := connect.NewRequest(&gastownv1.WatchSessionRequest{
			Session:    listResp.Msg.Sessions[0],
			IntervalMs: 10, // Should be clamped to 1000
		})
		stream, err := client.WatchSession(streamCtx, req)
		if err != nil {
			t.Fatalf("WatchSession failed: %v", err)
		}

		// Should succeed (interval clamped internally)
		if stream.Receive() {
			msg := stream.Msg()
			if !msg.Exists {
				t.Error("Session should exist")
			}
		}
	})
}
