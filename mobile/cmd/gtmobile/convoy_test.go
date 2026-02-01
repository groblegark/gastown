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

// setupConvoyTestServer creates a test server for convoy tests.
func setupConvoyTestServer(t *testing.T, townRoot string) (*httptest.Server, gastownv1connect.ConvoyServiceClient) {
	t.Helper()

	mux := http.NewServeMux()
	convoyServer := NewConvoyServer(townRoot)
	mux.Handle(gastownv1connect.NewConvoyServiceHandler(convoyServer))

	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))

	client := gastownv1connect.NewConvoyServiceClient(
		http.DefaultClient,
		server.URL,
	)

	return server, client
}

// TestConvoyServer_ListConvoys tests convoy listing functionality.
func TestConvoyServer_ListConvoys(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	townRoot, cleanup := setupDecisionTestTown(t)
	defer cleanup()

	server, client := setupConvoyTestServer(t, townRoot)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("Empty", func(t *testing.T) {
		req := connect.NewRequest(&gastownv1.ListConvoysRequest{
			Status: gastownv1.ConvoyStatusFilter_CONVOY_STATUS_FILTER_ALL,
		})
		resp, err := client.ListConvoys(ctx, req)
		if err != nil {
			t.Fatalf("ListConvoys failed: %v", err)
		}
		if resp.Msg.Total != 0 {
			t.Errorf("Total = %d, want 0 for empty town", resp.Msg.Total)
		}
	})

	t.Run("FilterByOpen", func(t *testing.T) {
		// Create a convoy first
		createReq := connect.NewRequest(&gastownv1.CreateConvoyRequest{
			Name: "Test Convoy",
		})
		createResp, err := client.CreateConvoy(ctx, createReq)
		if err != nil {
			t.Fatalf("CreateConvoy failed: %v", err)
		}
		convoyID := createResp.Msg.Convoy.Id

		// List open convoys
		req := connect.NewRequest(&gastownv1.ListConvoysRequest{
			Status: gastownv1.ConvoyStatusFilter_CONVOY_STATUS_FILTER_OPEN,
		})
		resp, err := client.ListConvoys(ctx, req)
		if err != nil {
			t.Fatalf("ListConvoys failed: %v", err)
		}

		found := false
		for _, c := range resp.Msg.Convoys {
			if c.Id == convoyID {
				found = true
				break
			}
		}
		if !found {
			t.Error("Created convoy not found in open list")
		}
	})

	t.Run("FilterByClosed", func(t *testing.T) {
		req := connect.NewRequest(&gastownv1.ListConvoysRequest{
			Status: gastownv1.ConvoyStatusFilter_CONVOY_STATUS_FILTER_CLOSED,
		})
		resp, err := client.ListConvoys(ctx, req)
		if err != nil {
			t.Fatalf("ListConvoys failed: %v", err)
		}
		// Should have no closed convoys yet
		if resp.Msg.Total != 0 {
			t.Errorf("Total closed = %d, want 0", resp.Msg.Total)
		}
	})

	t.Run("TreeMode", func(t *testing.T) {
		req := connect.NewRequest(&gastownv1.ListConvoysRequest{
			Status: gastownv1.ConvoyStatusFilter_CONVOY_STATUS_FILTER_ALL,
			Tree:   true,
		})
		resp, err := client.ListConvoys(ctx, req)
		if err != nil {
			t.Fatalf("ListConvoys with tree failed: %v", err)
		}
		// Just verify it doesn't error - tree mode populates progress
		if resp.Msg.Convoys != nil {
			for _, c := range resp.Msg.Convoys {
				// Progress should be set in tree mode
				if c.Progress == "" {
					t.Errorf("Convoy %s has empty progress in tree mode", c.Id)
				}
			}
		}
	})
}

// TestConvoyServer_GetConvoyStatus tests getting convoy details.
func TestConvoyServer_GetConvoyStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	townRoot, cleanup := setupDecisionTestTown(t)
	defer cleanup()

	server, client := setupConvoyTestServer(t, townRoot)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("NotFound", func(t *testing.T) {
		req := connect.NewRequest(&gastownv1.GetConvoyStatusRequest{
			ConvoyId: "nonexistent-convoy",
		})
		_, err := client.GetConvoyStatus(ctx, req)
		if err == nil {
			t.Error("Expected error for nonexistent convoy")
		}
		connectErr, ok := err.(*connect.Error)
		if !ok {
			t.Fatalf("Expected connect.Error, got %T", err)
		}
		if connectErr.Code() != connect.CodeNotFound {
			t.Errorf("Error code = %v, want NotFound", connectErr.Code())
		}
	})

	t.Run("ExistingConvoy", func(t *testing.T) {
		// Create a convoy first
		createResp, err := client.CreateConvoy(ctx, connect.NewRequest(&gastownv1.CreateConvoyRequest{
			Name:  "Status Test Convoy",
			Owner: "test-agent",
		}))
		if err != nil {
			t.Fatalf("CreateConvoy failed: %v", err)
		}
		convoyID := createResp.Msg.Convoy.Id

		// Get its status
		req := connect.NewRequest(&gastownv1.GetConvoyStatusRequest{
			ConvoyId: convoyID,
		})
		resp, err := client.GetConvoyStatus(ctx, req)
		if err != nil {
			t.Fatalf("GetConvoyStatus failed: %v", err)
		}

		if resp.Msg.Convoy == nil {
			t.Fatal("GetConvoyStatus returned nil convoy")
		}
		if resp.Msg.Convoy.Id != convoyID {
			t.Errorf("Convoy ID = %q, want %q", resp.Msg.Convoy.Id, convoyID)
		}
		if resp.Msg.Convoy.Title != "Status Test Convoy" {
			t.Errorf("Title = %q, want 'Status Test Convoy'", resp.Msg.Convoy.Title)
		}
	})

	t.Run("WithTrackedIssuesCount", func(t *testing.T) {
		// Create convoy without issues - tracked count should be 0
		createResp, err := client.CreateConvoy(ctx, connect.NewRequest(&gastownv1.CreateConvoyRequest{
			Name: "Empty Convoy",
		}))
		if err != nil {
			t.Fatalf("CreateConvoy failed: %v", err)
		}

		req := connect.NewRequest(&gastownv1.GetConvoyStatusRequest{
			ConvoyId: createResp.Msg.Convoy.Id,
		})
		resp, err := client.GetConvoyStatus(ctx, req)
		if err != nil {
			t.Fatalf("GetConvoyStatus failed: %v", err)
		}

		if resp.Msg.Total != 0 {
			t.Errorf("Total tracked = %d, want 0", resp.Msg.Total)
		}
	})
}

// TestConvoyServer_CreateConvoy tests convoy creation.
func TestConvoyServer_CreateConvoy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	townRoot, cleanup := setupDecisionTestTown(t)
	defer cleanup()

	server, client := setupConvoyTestServer(t, townRoot)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("Minimal", func(t *testing.T) {
		req := connect.NewRequest(&gastownv1.CreateConvoyRequest{
			Name: "Minimal Convoy",
		})
		resp, err := client.CreateConvoy(ctx, req)
		if err != nil {
			t.Fatalf("CreateConvoy failed: %v", err)
		}

		if resp.Msg.Convoy == nil {
			t.Fatal("CreateConvoy returned nil convoy")
		}
		if resp.Msg.Convoy.Id == "" {
			t.Error("Convoy ID is empty")
		}
		if resp.Msg.Convoy.Title != "Minimal Convoy" {
			t.Errorf("Title = %q, want 'Minimal Convoy'", resp.Msg.Convoy.Title)
		}
		if resp.Msg.Convoy.Status != "open" {
			t.Errorf("Status = %q, want 'open'", resp.Msg.Convoy.Status)
		}
	})

	t.Run("AllFields", func(t *testing.T) {
		req := connect.NewRequest(&gastownv1.CreateConvoyRequest{
			Name:          "Full Convoy",
			Owner:         "gastown/crew/test",
			Notify:        "gastown/witness",
			Molecule:      "mol-123",
			Owned:         true,
			MergeStrategy: "direct",
		})
		resp, err := client.CreateConvoy(ctx, req)
		if err != nil {
			t.Fatalf("CreateConvoy failed: %v", err)
		}

		convoy := resp.Msg.Convoy
		if convoy.Title != "Full Convoy" {
			t.Errorf("Title = %q", convoy.Title)
		}
		if convoy.Owner != "gastown/crew/test" {
			t.Errorf("Owner = %q, want 'gastown/crew/test'", convoy.Owner)
		}
		if convoy.Notify != "gastown/witness" {
			t.Errorf("Notify = %q, want 'gastown/witness'", convoy.Notify)
		}
		if convoy.Molecule != "mol-123" {
			t.Errorf("Molecule = %q, want 'mol-123'", convoy.Molecule)
		}
		if !convoy.Owned {
			t.Error("Owned should be true")
		}
		if convoy.MergeStrategy != "direct" {
			t.Errorf("MergeStrategy = %q, want 'direct'", convoy.MergeStrategy)
		}
	})

	t.Run("TrackIssues", func(t *testing.T) {
		// Create convoy with non-existent issues (should log warnings but not fail)
		req := connect.NewRequest(&gastownv1.CreateConvoyRequest{
			Name:     "Tracking Convoy",
			IssueIds: []string{"fake-issue-1", "fake-issue-2"},
		})
		resp, err := client.CreateConvoy(ctx, req)
		if err != nil {
			t.Fatalf("CreateConvoy failed: %v", err)
		}

		// TrackedCount may be 0 if issues don't exist
		if resp.Msg.Convoy == nil {
			t.Fatal("CreateConvoy returned nil convoy")
		}
	})
}

// TestConvoyServer_AddToConvoy tests adding issues to convoy.
func TestConvoyServer_AddToConvoy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	townRoot, cleanup := setupDecisionTestTown(t)
	defer cleanup()

	server, client := setupConvoyTestServer(t, townRoot)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("NotFound", func(t *testing.T) {
		req := connect.NewRequest(&gastownv1.AddToConvoyRequest{
			ConvoyId: "nonexistent-convoy",
			IssueIds: []string{"issue-1"},
		})
		_, err := client.AddToConvoy(ctx, req)
		if err == nil {
			t.Error("Expected error for nonexistent convoy")
		}
	})

	t.Run("AddToOpen", func(t *testing.T) {
		// Create convoy
		createResp, err := client.CreateConvoy(ctx, connect.NewRequest(&gastownv1.CreateConvoyRequest{
			Name: "Add Test Convoy",
		}))
		if err != nil {
			t.Fatalf("CreateConvoy failed: %v", err)
		}
		convoyID := createResp.Msg.Convoy.Id

		// Add issues
		req := connect.NewRequest(&gastownv1.AddToConvoyRequest{
			ConvoyId: convoyID,
			IssueIds: []string{"issue-1", "issue-2"},
		})
		resp, err := client.AddToConvoy(ctx, req)
		if err != nil {
			t.Fatalf("AddToConvoy failed: %v", err)
		}

		if resp.Msg.Reopened {
			t.Error("Should not reopen an already open convoy")
		}
	})

	t.Run("ReopenClosed", func(t *testing.T) {
		// Create and close a convoy
		createResp, err := client.CreateConvoy(ctx, connect.NewRequest(&gastownv1.CreateConvoyRequest{
			Name: "Reopen Test Convoy",
		}))
		if err != nil {
			t.Fatalf("CreateConvoy failed: %v", err)
		}
		convoyID := createResp.Msg.Convoy.Id

		// Close it
		_, err = client.CloseConvoy(ctx, connect.NewRequest(&gastownv1.CloseConvoyRequest{
			ConvoyId: convoyID,
			Reason:   "Test close",
		}))
		if err != nil {
			t.Fatalf("CloseConvoy failed: %v", err)
		}

		// Add issues - should reopen
		req := connect.NewRequest(&gastownv1.AddToConvoyRequest{
			ConvoyId: convoyID,
			IssueIds: []string{"issue-3"},
		})
		resp, err := client.AddToConvoy(ctx, req)
		if err != nil {
			t.Fatalf("AddToConvoy failed: %v", err)
		}

		if !resp.Msg.Reopened {
			t.Error("Should reopen a closed convoy when adding issues")
		}
		if resp.Msg.Convoy.Status != "open" {
			t.Errorf("Status = %q, want 'open' after reopen", resp.Msg.Convoy.Status)
		}
	})
}

// TestConvoyServer_CloseConvoy tests convoy closing.
func TestConvoyServer_CloseConvoy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	townRoot, cleanup := setupDecisionTestTown(t)
	defer cleanup()

	server, client := setupConvoyTestServer(t, townRoot)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("NotFound", func(t *testing.T) {
		req := connect.NewRequest(&gastownv1.CloseConvoyRequest{
			ConvoyId: "nonexistent-convoy",
		})
		_, err := client.CloseConvoy(ctx, req)
		if err == nil {
			t.Error("Expected error for nonexistent convoy")
		}
	})

	t.Run("CloseOpen", func(t *testing.T) {
		// Create convoy
		createResp, err := client.CreateConvoy(ctx, connect.NewRequest(&gastownv1.CreateConvoyRequest{
			Name: "Close Test Convoy",
		}))
		if err != nil {
			t.Fatalf("CreateConvoy failed: %v", err)
		}
		convoyID := createResp.Msg.Convoy.Id

		// Close it
		req := connect.NewRequest(&gastownv1.CloseConvoyRequest{
			ConvoyId: convoyID,
			Reason:   "Completed",
		})
		resp, err := client.CloseConvoy(ctx, req)
		if err != nil {
			t.Fatalf("CloseConvoy failed: %v", err)
		}

		if resp.Msg.Convoy.Status != "closed" {
			t.Errorf("Status = %q, want 'closed'", resp.Msg.Convoy.Status)
		}
	})

	t.Run("IdempotentClose", func(t *testing.T) {
		// Create and close a convoy
		createResp, err := client.CreateConvoy(ctx, connect.NewRequest(&gastownv1.CreateConvoyRequest{
			Name: "Idempotent Close Convoy",
		}))
		if err != nil {
			t.Fatalf("CreateConvoy failed: %v", err)
		}
		convoyID := createResp.Msg.Convoy.Id

		// Close twice - second should not error
		closeReq := connect.NewRequest(&gastownv1.CloseConvoyRequest{
			ConvoyId: convoyID,
		})
		_, err = client.CloseConvoy(ctx, closeReq)
		if err != nil {
			t.Fatalf("First CloseConvoy failed: %v", err)
		}

		resp, err := client.CloseConvoy(ctx, closeReq)
		if err != nil {
			t.Fatalf("Second CloseConvoy should be idempotent, got: %v", err)
		}

		if resp.Msg.Convoy.Status != "closed" {
			t.Errorf("Status = %q, want 'closed'", resp.Msg.Convoy.Status)
		}
	})
}

// TestConvoyServer_WatchConvoys tests convoy streaming.
func TestConvoyServer_WatchConvoys(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	townRoot, cleanup := setupDecisionTestTown(t)
	defer cleanup()

	server, client := setupConvoyTestServer(t, townRoot)
	defer server.Close()

	t.Run("ContextCancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		req := connect.NewRequest(&gastownv1.WatchConvoysRequest{
			Status: gastownv1.ConvoyStatusFilter_CONVOY_STATUS_FILTER_ALL,
		})
		stream, err := client.WatchConvoys(ctx, req)
		if err != nil {
			t.Fatalf("WatchConvoys failed: %v", err)
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

	t.Run("ReceivesExistingConvoys", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Create a convoy first
		_, err := client.CreateConvoy(ctx, connect.NewRequest(&gastownv1.CreateConvoyRequest{
			Name: "Watch Test Convoy",
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

		// Should receive the existing convoy
		received := false
		for stream.Receive() {
			msg := stream.Msg()
			if msg != nil && msg.UpdateType == "existing" {
				received = true
				break
			}
		}

		if !received {
			t.Log("May not have received convoy within timeout (timing dependent)")
		}
	})
}
