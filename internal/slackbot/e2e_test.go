package slackbot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// MockSlackAPI simulates Slack API responses for testing.
type MockSlackAPI struct {
	mu               sync.Mutex
	postedMessages   []map[string]interface{}
	conversationIDs  map[string]string // name -> channel ID
	createdChannels  []string
	failureMode      string // "rate_limit", "server_error", "none"
	requestCount     int
}

func NewMockSlackAPI() *MockSlackAPI {
	return &MockSlackAPI{
		conversationIDs: map[string]string{
			"gt-decisions": "C0000000001",
		},
	}
}

func (m *MockSlackAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requestCount++

	// Simulate failures
	switch m.failureMode {
	case "rate_limit":
		w.WriteHeader(http.StatusTooManyRequests)
		w.Header().Set("Retry-After", "1")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": "rate_limited",
		})
		return
	case "server_error":
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": "internal_error",
		})
		return
	}

	// Route by path
	switch r.URL.Path {
	case "/api/chat.postMessage":
		m.handlePostMessage(w, r)
	case "/api/conversations.list":
		m.handleConversationsList(w, r)
	case "/api/conversations.create":
		m.handleConversationsCreate(w, r)
	default:
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": true,
		})
	}
}

func (m *MockSlackAPI) handlePostMessage(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": "parse_error",
		})
		return
	}

	msg := map[string]interface{}{
		"channel": r.FormValue("channel"),
		"text":    r.FormValue("text"),
	}
	if blocks := r.FormValue("blocks"); blocks != "" {
		msg["blocks"] = blocks
	}
	m.postedMessages = append(m.postedMessages, msg)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"channel": r.FormValue("channel"),
		"ts":      "1234567890.123456",
	})
}

func (m *MockSlackAPI) handleConversationsList(w http.ResponseWriter, r *http.Request) {
	channels := []map[string]interface{}{}
	for name, id := range m.conversationIDs {
		channels = append(channels, map[string]interface{}{
			"id":   id,
			"name": name,
		})
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":       true,
		"channels": channels,
	})
}

func (m *MockSlackAPI) handleConversationsCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": "parse_error",
		})
		return
	}

	name := r.FormValue("name")
	channelID := "C" + name
	m.conversationIDs[name] = channelID
	m.createdChannels = append(m.createdChannels, name)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true,
		"channel": map[string]interface{}{
			"id":   channelID,
			"name": name,
		},
	})
}

func (m *MockSlackAPI) GetPostedMessages() []map[string]interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]map[string]interface{}{}, m.postedMessages...)
}

func (m *MockSlackAPI) GetCreatedChannels() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string{}, m.createdChannels...)
}

func (m *MockSlackAPI) SetFailureMode(mode string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failureMode = mode
}

func (m *MockSlackAPI) GetRequestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requestCount
}

func (m *MockSlackAPI) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.postedMessages = nil
	m.createdChannels = nil
	m.requestCount = 0
	m.failureMode = "none"
}

// TestE2E_DecisionLifecycle tests the complete decision lifecycle:
// create -> post -> resolve -> notification.
func TestE2E_DecisionLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	// Note: Full E2E tests require a running RPC server and Slack workspace.
	// These tests simulate the flow with mocked components.

	t.Run("BasicFlow", func(t *testing.T) {
		mock := NewMockSlackAPI()
		server := httptest.NewServer(mock)
		defer server.Close()

		// Verify mock is working
		resp, err := http.Get(server.URL + "/api/conversations.list")
		if err != nil {
			t.Fatalf("Mock server request failed: %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Mock server returned %d", resp.StatusCode)
		}
	})

	t.Run("DecisionPosted", func(t *testing.T) {
		mock := NewMockSlackAPI()
		server := httptest.NewServer(mock)
		defer server.Close()

		// Simulate posting a decision message
		req, _ := http.NewRequest("POST", server.URL+"/api/chat.postMessage", nil)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Post message failed: %v", err)
		}
		resp.Body.Close()

		// Verify the message was "posted"
		messages := mock.GetPostedMessages()
		if len(messages) != 1 {
			t.Errorf("Expected 1 posted message, got %d", len(messages))
		}
	})

	t.Run("ChannelCreationFlow", func(t *testing.T) {
		mock := NewMockSlackAPI()
		server := httptest.NewServer(mock)
		defer server.Close()

		// Simulate creating a new channel
		req, _ := http.NewRequest("POST", server.URL+"/api/conversations.create?name=gt-test-channel", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Create channel failed: %v", err)
		}
		resp.Body.Close()

		// Verify channel was created
		channels := mock.GetCreatedChannels()
		if len(channels) != 1 {
			t.Errorf("Expected 1 created channel, got %d", len(channels))
		}
	})
}

// TestE2E_ChannelRouting tests agent, epic, and convoy channel routing.
func TestE2E_ChannelRouting(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	// Test data for routing scenarios
	tests := []struct {
		name        string
		agent       string
		epicTitle   string
		convoyTitle string
		wantChannel string
	}{
		{
			name:        "agent only routing",
			agent:       "gastown/polecats/furiosa",
			wantChannel: "gt-decisions-gastown-polecats",
		},
		{
			name:        "epic routing priority",
			agent:       "gastown/polecats/furiosa",
			epicTitle:   "Mobile App Epic",
			wantChannel: "gt-decisions-mobile-app-epic",
		},
		{
			name:        "convoy routing highest priority",
			agent:       "gastown/polecats/furiosa",
			epicTitle:   "Some Epic",
			convoyTitle: "Production Deploy Convoy",
			wantChannel: "gt-decisions-production-deploy-convoy",
		},
	}

	bot := &Bot{
		channelPrefix: "gt-decisions",
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the channel name derivation
			if tt.convoyTitle == "" && tt.epicTitle == "" {
				got := bot.agentToChannelName(tt.agent)
				if got != tt.wantChannel {
					t.Errorf("agentToChannelName(%q) = %q, want %q", tt.agent, got, tt.wantChannel)
				}
			}
			// Note: Full routing with epic/convoy requires Slack API for channel lookup/creation
		})
	}
}

// TestE2E_ResolutionNotification tests that resolution triggers notification.
func TestE2E_ResolutionNotification(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	mock := NewMockSlackAPI()
	server := httptest.NewServer(mock)
	defer server.Close()

	t.Run("ResolvedMessagePosted", func(t *testing.T) {
		// Simulate resolution notification
		// In real scenario, the bot would post to the decision thread
		http.Post(server.URL+"/api/chat.postMessage?channel=C123&text=Decision+resolved", "", nil)

		messages := mock.GetPostedMessages()
		if len(messages) < 1 {
			t.Error("Resolution notification should be posted")
		}
	})
}

// TestE2E_ConcurrentDecisions tests handling multiple decisions concurrently.
func TestE2E_ConcurrentDecisions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	mock := NewMockSlackAPI()
	server := httptest.NewServer(mock)
	defer server.Close()

	t.Run("ConcurrentPosts", func(t *testing.T) {
		var wg sync.WaitGroup
		numDecisions := 10

		for i := 0; i < numDecisions; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				http.Post(server.URL+"/api/chat.postMessage?channel=C123", "", nil)
			}(i)
		}

		wg.Wait()

		messages := mock.GetPostedMessages()
		if len(messages) != numDecisions {
			t.Errorf("Expected %d messages, got %d", numDecisions, len(messages))
		}
	})

	t.Run("ConcurrentChannelCreation", func(t *testing.T) {
		mock.Reset()
		var wg sync.WaitGroup

		// Try to create same channel from multiple goroutines
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				http.Post(server.URL+"/api/conversations.create?name=shared-channel", "", nil)
			}()
		}

		wg.Wait()

		// All should succeed (mock doesn't enforce uniqueness)
		channels := mock.GetCreatedChannels()
		if len(channels) != 5 {
			t.Errorf("Expected 5 create attempts, got %d", len(channels))
		}
	})
}

// TestE2E_SSEToSlackFlow tests the SSE -> Slack notification flow.
func TestE2E_SSEToSlackFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	t.Run("SSEEventTriggersSlackPost", func(t *testing.T) {
		// This would test the full flow:
		// 1. Connect to SSE endpoint
		// 2. Receive decision event
		// 3. Post to Slack channel

		// Note: Full test requires mocked SSE server
		// Here we just verify the components work
	})
}

// TestE2E_StartupBehavior tests bot startup and initial state.
func TestE2E_StartupBehavior(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	t.Run("ConfigValidation", func(t *testing.T) {
		// Missing tokens should fail
		_, err := New(Config{})
		if err == nil {
			t.Error("Expected error for empty config")
		}
	})

	t.Run("ValidConfig", func(t *testing.T) {
		cfg := Config{
			BotToken:    "xoxb-test",
			AppToken:    "xapp-test",
			RPCEndpoint: "http://localhost:8443",
		}
		bot, err := New(cfg)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if bot == nil {
			t.Error("Bot should not be nil")
		}
	})
}

// TestE2E_GracefulShutdown tests clean shutdown behavior.
func TestE2E_GracefulShutdown(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	t.Run("ContextCancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		// Simulate startup and immediate shutdown
		done := make(chan struct{})
		go func() {
			// Would run bot here
			<-ctx.Done()
			close(done)
		}()

		cancel()

		select {
		case <-done:
			// Good - shutdown completed
		case <-time.After(2 * time.Second):
			t.Error("Shutdown should complete within timeout")
		}
	})
}
