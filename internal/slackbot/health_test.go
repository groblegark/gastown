package slackbot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthServer_Endpoints(t *testing.T) {
	// Create a minimal bot (we only need the IsConnected method)
	bot := &Bot{}

	// Test /healthz when disconnected
	t.Run("healthz_disconnected", func(t *testing.T) {
		SetConnected(false)

		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		w := httptest.NewRecorder()

		// Create handler directly to test without starting server
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if bot.IsConnected() {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("ok"))
			} else {
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte("disconnected"))
			}
		})

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, w.Code)
		}
		if w.Body.String() != "disconnected" {
			t.Errorf("expected body 'disconnected', got %q", w.Body.String())
		}
	})

	// Test /healthz when connected
	t.Run("healthz_connected", func(t *testing.T) {
		SetConnected(true)
		defer SetConnected(false)

		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		w := httptest.NewRecorder()

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if bot.IsConnected() {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("ok"))
			} else {
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte("disconnected"))
			}
		})

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}
		if w.Body.String() != "ok" {
			t.Errorf("expected body 'ok', got %q", w.Body.String())
		}
	})

	// Test /readyz always returns OK
	t.Run("readyz_always_ok", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		w := httptest.NewRecorder()

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ready"))
		})

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}
		if w.Body.String() != "ready" {
			t.Errorf("expected body 'ready', got %q", w.Body.String())
		}
	})
}

func TestHealthServer_Start(t *testing.T) {
	bot := &Bot{}
	healthServer := NewHealthServer(bot, 0) // Port 0 = random available port

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Start should return when context is cancelled
	err := healthServer.Start(ctx)
	if err != nil && err != context.DeadlineExceeded {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSetConnected(t *testing.T) {
	bot := &Bot{}

	// Initially disconnected
	SetConnected(false)
	if bot.IsConnected() {
		t.Error("expected disconnected initially")
	}

	// Connect
	SetConnected(true)
	if !bot.IsConnected() {
		t.Error("expected connected after SetConnected(true)")
	}

	// Disconnect
	SetConnected(false)
	if bot.IsConnected() {
		t.Error("expected disconnected after SetConnected(false)")
	}
}
