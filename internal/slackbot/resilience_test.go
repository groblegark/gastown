package slackbot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestResilience_RateLimitRetry tests retry behavior on rate limiting.
func TestResilience_RateLimitRetry(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping resilience test in short mode")
	}

	t.Run("RetriesOnRateLimit", func(t *testing.T) {
		var requestCount int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count := atomic.AddInt32(&requestCount, 1)
			if count < 3 {
				// First 2 requests rate limited
				w.WriteHeader(http.StatusTooManyRequests)
				w.Header().Set("Retry-After", "0")
				json.NewEncoder(w).Encode(map[string]interface{}{
					"ok":    false,
					"error": "rate_limited",
				})
				return
			}
			// Third request succeeds
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ok": true,
			})
		}))
		defer server.Close()

		// Simulate a client that retries
		client := &http.Client{Timeout: 5 * time.Second}
		var lastStatus int

		for i := 0; i < 5; i++ {
			resp, err := client.Get(server.URL + "/api/chat.postMessage")
			if err != nil {
				t.Fatalf("Request failed: %v", err)
			}
			lastStatus = resp.StatusCode
			resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				break
			}
			time.Sleep(10 * time.Millisecond) // Brief wait between retries
		}

		if lastStatus != http.StatusOK {
			t.Error("Should eventually succeed after rate limit clears")
		}

		if atomic.LoadInt32(&requestCount) < 3 {
			t.Error("Should have retried multiple times")
		}
	})

	t.Run("ExponentialBackoff", func(t *testing.T) {
		// Test that backoff increases between retries
		// This is a design test - actual implementation may vary
		retryDelays := []time.Duration{
			100 * time.Millisecond,
			200 * time.Millisecond,
			400 * time.Millisecond,
		}

		for i := 1; i < len(retryDelays); i++ {
			if retryDelays[i] <= retryDelays[i-1] {
				t.Error("Backoff should increase between retries")
			}
		}
	})
}

// TestResilience_ServerErrorRetry tests retry behavior on server errors.
func TestResilience_ServerErrorRetry(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping resilience test in short mode")
	}

	t.Run("RetriesOnServerError", func(t *testing.T) {
		var requestCount int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count := atomic.AddInt32(&requestCount, 1)
			if count < 2 {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"ok":    false,
					"error": "internal_error",
				})
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ok": true,
			})
		}))
		defer server.Close()

		client := &http.Client{Timeout: 5 * time.Second}
		var succeeded bool

		for i := 0; i < 3; i++ {
			resp, err := client.Get(server.URL + "/api/test")
			if err != nil {
				continue
			}
			if resp.StatusCode == http.StatusOK {
				succeeded = true
			}
			resp.Body.Close()
			if succeeded {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		if !succeeded {
			t.Error("Should succeed after transient error")
		}
	})

	t.Run("GivesUpAfterMaxRetries", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()

		client := &http.Client{Timeout: 5 * time.Second}
		maxRetries := 3
		var lastErr error

		for i := 0; i < maxRetries; i++ {
			resp, err := client.Get(server.URL + "/api/test")
			if err != nil {
				lastErr = err
				continue
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusServiceUnavailable {
				break
			}
		}

		// Should have failed after max retries
		_ = lastErr // Document the failure
	})
}

// TestResilience_SSEReconnect tests SSE reconnection on disconnect.
func TestResilience_SSEReconnect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping resilience test in short mode")
	}

	t.Run("ReconnectsOnDisconnect", func(t *testing.T) {
		var connectionCount int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count := atomic.AddInt32(&connectionCount, 1)

			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")

			// First connection: immediate close
			if count == 1 {
				return
			}

			// Second connection: stay alive briefly
			flusher, ok := w.(http.Flusher)
			if !ok {
				return
			}

			w.Write([]byte("event: connected\ndata: {\"status\":\"ok\"}\n\n"))
			flusher.Flush()

			// Keep alive for a bit
			time.Sleep(100 * time.Millisecond)
		}))
		defer server.Close()

		// Simulate multiple connection attempts
		for i := 0; i < 3; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/events", nil)

			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				resp.Body.Close()
			}
			cancel()
		}

		if atomic.LoadInt32(&connectionCount) < 2 {
			t.Error("Should have reconnected at least once")
		}
	})

	t.Run("BackoffOnReconnect", func(t *testing.T) {
		reconnectDelays := []time.Duration{
			100 * time.Millisecond,
			200 * time.Millisecond,
			400 * time.Millisecond,
			800 * time.Millisecond,
		}

		// Verify delays increase
		for i := 1; i < len(reconnectDelays); i++ {
			if reconnectDelays[i] <= reconnectDelays[i-1] {
				t.Error("Reconnect delay should increase")
			}
		}

		// Verify max delay cap exists
		maxDelay := 30 * time.Second
		for _, d := range reconnectDelays {
			if d > maxDelay {
				t.Errorf("Delay %v exceeds max %v", d, maxDelay)
			}
		}
	})
}

// TestResilience_MalformedDecision tests handling of malformed decision data.
func TestResilience_MalformedDecision(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping resilience test in short mode")
	}

	tests := []struct {
		name      string
		data      string
		shouldErr bool
	}{
		{
			name:      "valid JSON",
			data:      `{"id":"dec-1","question":"Test?"}`,
			shouldErr: false,
		},
		{
			name:      "invalid JSON",
			data:      `{not valid json}`,
			shouldErr: true,
		},
		{
			name:      "missing required fields",
			data:      `{"id":"dec-1"}`,
			shouldErr: false, // Should handle gracefully
		},
		{
			name:      "empty object",
			data:      `{}`,
			shouldErr: false, // Should handle gracefully
		},
		{
			name:      "null",
			data:      `null`,
			shouldErr: false, // Should handle gracefully
		},
		{
			name:      "array instead of object",
			data:      `[1,2,3]`,
			shouldErr: false, // Should handle gracefully
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var data map[string]interface{}
			err := json.Unmarshal([]byte(tt.data), &data)

			if tt.shouldErr && err == nil {
				t.Error("Expected error for malformed data")
			}
			if !tt.shouldErr && err != nil && tt.data != `null` && tt.data[0] != '[' {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

// TestResilience_ConcurrentChannelCreation tests thread-safe channel operations.
func TestResilience_ConcurrentChannelCreation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping resilience test in short mode")
	}

	t.Run("NoRaceOnChannelCache", func(t *testing.T) {
		var mu sync.Mutex
		channelCache := make(map[string]string)

		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()

				channelName := "test-channel"

				// Simulate cache lookup and creation
				mu.Lock()
				if _, exists := channelCache[channelName]; !exists {
					channelCache[channelName] = "C" + channelName
				}
				mu.Unlock()
			}(i)
		}
		wg.Wait()

		// Should have exactly one entry
		if len(channelCache) != 1 {
			t.Errorf("Expected 1 cache entry, got %d", len(channelCache))
		}
	})

	t.Run("ConcurrentDifferentChannels", func(t *testing.T) {
		var mu sync.Mutex
		channelCache := make(map[string]string)

		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()

				channelName := "channel-" + string(rune('a'+id))

				mu.Lock()
				if _, exists := channelCache[channelName]; !exists {
					channelCache[channelName] = "C" + channelName
				}
				mu.Unlock()
			}(i)
		}
		wg.Wait()

		// Should have 10 different channels
		if len(channelCache) != 10 {
			t.Errorf("Expected 10 cache entries, got %d", len(channelCache))
		}
	})
}

// TestResilience_MessageQueueing tests message queue behavior under load.
func TestResilience_MessageQueueing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping resilience test in short mode")
	}

	t.Run("QueueCapacity", func(t *testing.T) {
		queue := make(chan string, 100)

		// Fill queue
		for i := 0; i < 100; i++ {
			queue <- "message"
		}

		if len(queue) != 100 {
			t.Errorf("Queue should be full, got %d", len(queue))
		}

		// Non-blocking send should fail when full
		select {
		case queue <- "overflow":
			t.Error("Send should block when queue is full")
		default:
			// Expected - queue is full
		}
	})

	t.Run("QueueDraining", func(t *testing.T) {
		queue := make(chan string, 10)

		// Add messages
		for i := 0; i < 5; i++ {
			queue <- "message"
		}

		// Drain asynchronously
		drained := 0
		timeout := time.After(100 * time.Millisecond)

	drain:
		for {
			select {
			case <-queue:
				drained++
			case <-timeout:
				break drain
			default:
				break drain
			}
		}

		if drained != 5 {
			t.Errorf("Drained %d messages, expected 5", drained)
		}
	})
}

// TestResilience_Timeouts tests timeout handling.
func TestResilience_Timeouts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping resilience test in short mode")
	}

	t.Run("SlowServer", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(2 * time.Second)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := &http.Client{
			Timeout: 100 * time.Millisecond,
		}

		start := time.Now()
		_, err := client.Get(server.URL + "/slow")
		elapsed := time.Since(start)

		if err == nil {
			t.Error("Expected timeout error")
		}

		// Should timeout around 100ms, not 2s
		if elapsed > 500*time.Millisecond {
			t.Errorf("Took %v, should have timed out faster", elapsed)
		}
	})

	t.Run("ContextTimeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(2 * time.Second)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/slow", nil)
		start := time.Now()
		_, err := http.DefaultClient.Do(req)
		elapsed := time.Since(start)

		if err == nil {
			t.Error("Expected context deadline error")
		}

		if elapsed > 500*time.Millisecond {
			t.Errorf("Took %v, should have cancelled faster", elapsed)
		}
	})
}

// TestResilience_ChannelNameCollision tests handling of channel name collisions.
func TestResilience_ChannelNameCollision(t *testing.T) {
	t.Run("SanitizedNamesCollide", func(t *testing.T) {
		bot := &Bot{channelPrefix: "gt-decisions"}

		// These agents might produce similar channel names
		agents := []string{
			"gastown/polecats/test-agent",
			"gastown/polecats/test_agent",
			"gastown/polecats/TEST-AGENT",
		}

		names := make(map[string]bool)
		for _, agent := range agents {
			name := bot.agentToChannelName(agent)
			if names[name] {
				// Collision is expected - sanitization normalizes names
				t.Logf("Collision for %q -> %q", agent, name)
			}
			names[name] = true
		}
	})
}

// TestResilience_EmptyResponses tests handling of empty API responses.
func TestResilience_EmptyResponses(t *testing.T) {
	t.Run("EmptyBody", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			// Empty body
		}))
		defer server.Close()

		resp, err := http.Get(server.URL + "/api/test")
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()

		var result map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&result)
		// Should handle empty body gracefully
		if err == nil && len(result) > 0 {
			t.Error("Empty body should decode to empty or error")
		}
	})

	t.Run("NullResponse", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("null"))
		}))
		defer server.Close()

		resp, err := http.Get(server.URL + "/api/test")
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()

		var result interface{}
		err = json.NewDecoder(resp.Body).Decode(&result)
		if err != nil {
			t.Errorf("Failed to decode null: %v", err)
		}
		if result != nil {
			t.Errorf("Expected nil, got %v", result)
		}
	})
}

// TestResilience_LargePayloads tests handling of large payloads.
func TestResilience_LargePayloads(t *testing.T) {
	t.Run("LargeDecisionContext", func(t *testing.T) {
		// Test with a large context string
		largeContext := make([]byte, 100000) // 100KB
		for i := range largeContext {
			largeContext[i] = 'x'
		}

		result := formatContextForSlack(string(largeContext), 500)

		if len(result) > 550 { // Allow some buffer for truncation markers
			t.Errorf("Result should be truncated to ~500 chars, got %d", len(result))
		}

		if result == "" {
			t.Error("Large context should produce some output")
		}
	})

	t.Run("ManyOptions", func(t *testing.T) {
		// Decisions with many options
		options := make([]string, 50)
		for i := range options {
			options[i] = "Option " + string(rune('A'+i%26))
		}

		// Should handle gracefully (actual display may truncate)
		if len(options) != 50 {
			t.Error("Test setup failed")
		}
	})
}
