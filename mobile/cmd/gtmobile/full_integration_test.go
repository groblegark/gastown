//go:build integration

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/eventbus"
	gastownv1 "github.com/steveyegge/gastown/mobile/gen/gastown/v1"
	"github.com/steveyegge/gastown/mobile/gen/gastown/v1/gastownv1connect"
)

// TestIntegration_FullServerStartup tests complete server startup with all services.
func TestIntegration_FullServerStartup(t *testing.T) {
	townRoot, cleanup := setupFullIntegrationTown(t)
	defer cleanup()

	mux := http.NewServeMux()
	bus := eventbus.New()
	t.Cleanup(func() { bus.Close() })

	// Register all services
	statusServer := NewStatusServer(townRoot)
	mailServer := NewMailServer(townRoot)
	decisionServer := NewDecisionServer(townRoot, bus)
	convoyServer := NewConvoyServer(townRoot)
	activityServer := NewActivityServer(townRoot)
	terminalServer := NewTerminalServer()

	mux.Handle(gastownv1connect.NewStatusServiceHandler(statusServer))
	mux.Handle(gastownv1connect.NewMailServiceHandler(mailServer))
	mux.Handle(gastownv1connect.NewDecisionServiceHandler(decisionServer))
	mux.Handle(gastownv1connect.NewConvoyServiceHandler(convoyServer))
	mux.Handle(gastownv1connect.NewActivityServiceHandler(activityServer))
	mux.Handle(gastownv1connect.NewTerminalServiceHandler(terminalServer))

	// Add health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	// Add SSE endpoint
	mux.HandleFunc("/events/decisions", NewSSEHandler(bus, townRoot))

	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	defer server.Close()

	t.Run("AllServicesRespond", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Test Status Service
		statusClient := gastownv1connect.NewStatusServiceClient(http.DefaultClient, server.URL)
		statusResp, err := statusClient.GetTownStatus(ctx, connect.NewRequest(&gastownv1.GetTownStatusRequest{Fast: true}))
		if err != nil {
			t.Errorf("StatusService failed: %v", err)
		} else if statusResp.Msg.Status == nil {
			t.Error("StatusService returned nil status")
		}

		// Test Decision Service
		decisionClient := gastownv1connect.NewDecisionServiceClient(http.DefaultClient, server.URL)
		listResp, err := decisionClient.ListPending(ctx, connect.NewRequest(&gastownv1.ListPendingRequest{}))
		if err != nil {
			t.Errorf("DecisionService failed: %v", err)
		} else {
			t.Logf("DecisionService returned %d pending decisions", listResp.Msg.Total)
		}

		// Test Convoy Service
		convoyClient := gastownv1connect.NewConvoyServiceClient(http.DefaultClient, server.URL)
		convoyResp, err := convoyClient.ListConvoys(ctx, connect.NewRequest(&gastownv1.ListConvoysRequest{}))
		if err != nil {
			t.Errorf("ConvoyService failed: %v", err)
		} else {
			t.Logf("ConvoyService returned %d convoys", convoyResp.Msg.Total)
		}

		// Test Activity Service
		activityClient := gastownv1connect.NewActivityServiceClient(http.DefaultClient, server.URL)
		activityResp, err := activityClient.ListEvents(ctx, connect.NewRequest(&gastownv1.ListEventsRequest{}))
		if err != nil {
			t.Errorf("ActivityService failed: %v", err)
		} else {
			t.Logf("ActivityService returned %d events", activityResp.Msg.TotalCount)
		}

		// Test Terminal Service
		terminalClient := gastownv1connect.NewTerminalServiceClient(http.DefaultClient, server.URL)
		terminalResp, err := terminalClient.ListSessions(ctx, connect.NewRequest(&gastownv1.ListSessionsRequest{}))
		if err != nil {
			t.Errorf("TerminalService failed: %v", err)
		} else {
			t.Logf("TerminalService returned %d sessions", len(terminalResp.Msg.Sessions))
		}

		// Test Mail Service
		mailClient := gastownv1connect.NewMailServiceClient(http.DefaultClient, server.URL)
		mailResp, err := mailClient.ListInbox(ctx, connect.NewRequest(&gastownv1.ListInboxRequest{}))
		if err != nil {
			t.Errorf("MailService failed: %v", err)
		} else {
			t.Logf("MailService returned %d messages", mailResp.Msg.Total)
		}
	})

	t.Run("HealthEndpoint", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/health")
		if err != nil {
			t.Fatalf("Health check failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Health status = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("SSEEndpoint", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/events/decisions", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("SSE request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("SSE status = %d, want 200", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
			t.Errorf("Content-Type = %q, want text/event-stream", ct)
		}
	})
}

// TestIntegration_APIKeyAuthentication tests API key authentication.
func TestIntegration_APIKeyAuthentication(t *testing.T) {
	townRoot, cleanup := setupFullIntegrationTown(t)
	defer cleanup()

	apiKey := "test-api-key-12345"

	mux := http.NewServeMux()
	opts := []connect.HandlerOption{
		connect.WithInterceptors(APIKeyInterceptor(apiKey)),
	}

	statusServer := NewStatusServer(townRoot)
	mux.Handle(gastownv1connect.NewStatusServiceHandler(statusServer, opts...))

	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Run("ValidAPIKey", func(t *testing.T) {
		client := gastownv1connect.NewStatusServiceClient(http.DefaultClient, server.URL)
		req := connect.NewRequest(&gastownv1.GetTownStatusRequest{Fast: true})
		req.Header().Set("X-GT-API-Key", apiKey)

		_, err := client.GetTownStatus(ctx, req)
		if err != nil {
			t.Errorf("Valid API key should succeed: %v", err)
		}
	})

	t.Run("InvalidAPIKey", func(t *testing.T) {
		client := gastownv1connect.NewStatusServiceClient(http.DefaultClient, server.URL)
		req := connect.NewRequest(&gastownv1.GetTownStatusRequest{Fast: true})
		req.Header().Set("X-GT-API-Key", "wrong-key")

		_, err := client.GetTownStatus(ctx, req)
		if err == nil {
			t.Error("Invalid API key should fail")
		}

		connectErr, ok := err.(*connect.Error)
		if !ok {
			t.Fatalf("Expected connect.Error, got %T", err)
		}
		if connectErr.Code() != connect.CodeUnauthenticated {
			t.Errorf("Error code = %v, want Unauthenticated", connectErr.Code())
		}
	})

	t.Run("MissingAPIKey", func(t *testing.T) {
		client := gastownv1connect.NewStatusServiceClient(http.DefaultClient, server.URL)
		req := connect.NewRequest(&gastownv1.GetTownStatusRequest{Fast: true})
		// No API key header

		_, err := client.GetTownStatus(ctx, req)
		if err == nil {
			t.Error("Missing API key should fail")
		}
	})
}

// TestIntegration_TLSConfiguration tests TLS setup.
func TestIntegration_TLSConfiguration(t *testing.T) {
	t.Run("LoadValidCerts", func(t *testing.T) {
		// Create temp directory for test certs
		tmpDir := t.TempDir()

		// Generate self-signed certificate for testing
		certPEM := []byte(`-----BEGIN CERTIFICATE-----
MIIBkTCB+wIJAKHBfpRgDyBhMA0GCSqGSIb3DQEBCwUAMBExDzANBgNVBAMMBnRl
c3RjYTAeFw0yNjAxMjkxMjAwMDBaFw0yNzAxMjkxMjAwMDBaMBExDzANBgNVBAMM
BnRlc3RjYTBcMA0GCSqGSIb3DQEBAQUAA0sAMEgCQQC7o96HeFU6NTDB0f5P/GNN
j0k4rPEjKQXKdFftHXnvYKmEBQqDnR5rKLKefYVW9nqJh5yPsUKjEVAGZjLmAgMB
AAGjUzBRMB0GA1UdDgQWBBSfU5yP0dLiV0v0KSdTc0R0FZPDHjAfBgNVHSMEGDAW
gBSfU5yP0dLiV0v0KSdTc0R0FZPDHjAPBgNVHRMBAf8EBTADAQH/MA0GCSqGSIb3
DQEBCwUAA0EAOaWg8lLCVXOE3XkGqnujXuY12KhQ8RrHFJMNXNZ9SnFFfVJr0tSP
qH8V7qDVVD6b7bDxH4f3f3M7jGP9KXKHug==
-----END CERTIFICATE-----`)

		keyPEM := []byte(`-----BEGIN RSA PRIVATE KEY-----
MIIBOgIBAAJBALuj3od4VTo1MMHR/k/8Y02PSTis8SMpBcp0V+0dee9gqYQFCoOd
HmsosJ59hVb2eomHnI+xQqMRUAZmMuYCAwEAAQJAZFnN0TnfnGn3y7FpLbxwGQhG
qIVlBmtQUoSxLJN0KbwLqhDVqULbRUJ8VRf0Mc6xNf0BjUhNL4MqHH8YxAJhAQIh
AOKd5W7pZXMnLdHSJmZt2nD2M8g5N0yBcKvMJKMJQM0BAiEA0gHzVrL8fNK9u7D7
J2X8LGm4OoJQsmVLbB8fMFsZbwECIQCYE1Kz3h8PlfONg3Ihu5d3pJGZaLMPmRK3
7I8nH1MHAQIQL0j8S3DOMqJpS9aNHJLV4aM3hplLgMB4B2k/wNPFAQIhALn99qeL
xBKVuOH3mP3r7fqjxL4JOxF5r5wKdJNqIGMB
-----END RSA PRIVATE KEY-----`)

		certFile := filepath.Join(tmpDir, "cert.pem")
		keyFile := filepath.Join(tmpDir, "key.pem")

		if err := os.WriteFile(certFile, certPEM, 0644); err != nil {
			t.Fatalf("Failed to write cert: %v", err)
		}
		if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
			t.Fatalf("Failed to write key: %v", err)
		}

		// Test loading
		tlsConfig, err := LoadTLSConfig(certFile, keyFile)
		if err != nil {
			t.Fatalf("LoadTLSConfig failed: %v", err)
		}

		if tlsConfig == nil {
			t.Fatal("TLS config is nil")
		}
		if len(tlsConfig.Certificates) != 1 {
			t.Errorf("Expected 1 certificate, got %d", len(tlsConfig.Certificates))
		}
		if tlsConfig.MinVersion != tls.VersionTLS12 {
			t.Errorf("MinVersion = %d, want TLS 1.2", tlsConfig.MinVersion)
		}
	})

	t.Run("InvalidCertPath", func(t *testing.T) {
		_, err := LoadTLSConfig("/nonexistent/cert.pem", "/nonexistent/key.pem")
		if err == nil {
			t.Error("Expected error for invalid paths")
		}
	})
}

// TestIntegration_SlackBotRPCConnection tests Slack bot to RPC server connection.
func TestIntegration_SlackBotRPCConnection(t *testing.T) {
	townRoot, cleanup := setupFullIntegrationTown(t)
	defer cleanup()

	// Start RPC server
	mux := http.NewServeMux()
	bus := eventbus.New()
	t.Cleanup(func() { bus.Close() })

	decisionServer := NewDecisionServer(townRoot, bus)
	mux.Handle(gastownv1connect.NewDecisionServiceHandler(decisionServer))
	mux.HandleFunc("/events/decisions", NewSSEHandler(bus, townRoot))

	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	defer server.Close()

	t.Run("RPCClientConnection", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		client := gastownv1connect.NewDecisionServiceClient(http.DefaultClient, server.URL)
		_, err := client.ListPending(ctx, connect.NewRequest(&gastownv1.ListPendingRequest{}))
		if err != nil {
			t.Errorf("RPC client connection failed: %v", err)
		}
	})

	t.Run("SSEConnection", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/events/decisions", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("SSE connection failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("SSE status = %d, want 200", resp.StatusCode)
		}
	})
}

// TestIntegration_CrossServiceInteraction tests interactions between services.
func TestIntegration_CrossServiceInteraction(t *testing.T) {
	townRoot, cleanup := setupFullIntegrationTown(t)
	defer cleanup()

	mux := http.NewServeMux()
	bus := eventbus.New()
	t.Cleanup(func() { bus.Close() })

	decisionServer := NewDecisionServer(townRoot, bus)
	convoyServer := NewConvoyServer(townRoot)
	activityServer := NewActivityServer(townRoot)

	mux.Handle(gastownv1connect.NewDecisionServiceHandler(decisionServer))
	mux.Handle(gastownv1connect.NewConvoyServiceHandler(convoyServer))
	mux.Handle(gastownv1connect.NewActivityServiceHandler(activityServer))

	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("DecisionToEventBus", func(t *testing.T) {
		// Subscribe to event bus
		events, unsub := bus.Subscribe()
		defer unsub()

		done := make(chan bool)
		go func() {
			for range events {
				done <- true
				return
			}
		}()

		// Create a decision
		client := gastownv1connect.NewDecisionServiceClient(http.DefaultClient, server.URL)
		_, err := client.CreateDecision(ctx, connect.NewRequest(&gastownv1.CreateDecisionRequest{
			Question: "Cross-service test?",
			Options:  []*gastownv1.DecisionOption{{Label: "OK"}},
		}))
		if err != nil {
			t.Fatalf("CreateDecision failed: %v", err)
		}

		// Event should be published
		select {
		case <-done:
			// Good
		case <-time.After(2 * time.Second):
			t.Error("Event not published to bus")
		}
	})

	t.Run("ActivityEmitAndList", func(t *testing.T) {
		activityClient := gastownv1connect.NewActivityServiceClient(http.DefaultClient, server.URL)

		// Emit an event
		_, err := activityClient.EmitEvent(ctx, connect.NewRequest(&gastownv1.EmitEventRequest{
			Type:  "integration_test",
			Actor: "test_runner",
		}))
		if err != nil {
			t.Fatalf("EmitEvent failed: %v", err)
		}

		// List should include it
		listResp, err := activityClient.ListEvents(ctx, connect.NewRequest(&gastownv1.ListEventsRequest{
			Filter: &gastownv1.EventFilter{
				Types: []string{"integration_test"},
			},
		}))
		if err != nil {
			t.Fatalf("ListEvents failed: %v", err)
		}

		found := false
		for _, e := range listResp.Msg.Events {
			if e.Type == "integration_test" {
				found = true
				break
			}
		}
		if !found {
			t.Error("Emitted event not found in list")
		}
	})
}

// setupFullIntegrationTown creates a complete town structure for integration tests.
func setupFullIntegrationTown(t *testing.T) (string, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "integration-test-*")
	if err != nil {
		t.Fatal(err)
	}

	// Create town structure
	dirs := []string{
		"mayor",
		"gastown",
		"gastown/polecats",
		"gastown/crew",
		".mail",
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(tmpDir, dir), 0755); err != nil {
			os.RemoveAll(tmpDir)
			t.Fatal(err)
		}
	}

	// Create config files
	townConfig := `{"name": "integration-test-town"}`
	if err := os.WriteFile(filepath.Join(tmpDir, "mayor", "town.json"), []byte(townConfig), 0644); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatal(err)
	}

	rigsConfig := `{"rigs": {"gastown": {"path": "gastown"}}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "mayor", "rigs.json"), []byte(rigsConfig), 0644); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatal(err)
	}

	// Initialize beads
	b := beads.NewIsolated(tmpDir)
	if err := b.Init("hq-"); err != nil {
		os.RemoveAll(tmpDir)
		t.Skipf("cannot initialize beads: %v", err)
	}

	// Create empty events files
	os.WriteFile(filepath.Join(tmpDir, ".events.jsonl"), []byte(""), 0644)
	os.WriteFile(filepath.Join(tmpDir, ".feed.jsonl"), []byte(""), 0644)

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	return tmpDir, cleanup
}

// TestIntegration_CertificatePool tests certificate pool creation.
func TestIntegration_CertificatePool(t *testing.T) {
	t.Run("EmptyPool", func(t *testing.T) {
		pool := x509.NewCertPool()
		if pool == nil {
			t.Error("Failed to create certificate pool")
		}
	})

	t.Run("SystemPool", func(t *testing.T) {
		pool, err := x509.SystemCertPool()
		if err != nil {
			// Some systems may not have a system pool
			t.Logf("No system cert pool: %v", err)
			return
		}
		if pool == nil {
			t.Error("System pool is nil")
		}
	})
}
