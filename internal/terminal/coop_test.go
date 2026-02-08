package terminal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCoopBackendImplementsInterface verifies CoopBackend satisfies Backend.
func TestCoopBackendImplementsInterface(t *testing.T) {
	var _ Backend = (*CoopBackend)(nil)
}

func newTestCoop(t *testing.T, handler http.Handler) (*CoopBackend, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	b := NewCoopBackend(CoopConfig{})
	b.AddSession("test", srv.URL)
	return b, srv
}

func TestCoopBackend_HasSession_Running(t *testing.T) {
	b, srv := newTestCoop(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		pid := int32(1234)
		json.NewEncoder(w).Encode(coopHealthResponse{
			Status: "running",
			PID:    &pid,
			Ready:  true,
		})
	}))
	defer srv.Close()

	ok, err := b.HasSession("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected session to be running")
	}
}

func TestCoopBackend_HasSession_NotRegistered(t *testing.T) {
	b := NewCoopBackend(CoopConfig{})

	ok, err := b.HasSession("missing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected false for unregistered session")
	}
}

func TestCoopBackend_HasSession_Unreachable(t *testing.T) {
	b := NewCoopBackend(CoopConfig{})
	b.AddSession("dead", "http://127.0.0.1:1") // port 1 — won't connect

	ok, err := b.HasSession("dead")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected false for unreachable host")
	}
}

func TestCoopBackend_HasSession_NoPID(t *testing.T) {
	b, srv := newTestCoop(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(coopHealthResponse{
			Status: "running",
			PID:    nil,
			Ready:  false,
		})
	}))
	defer srv.Close()

	ok, err := b.HasSession("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected false when PID is nil")
	}
}

func TestCoopBackend_CapturePane(t *testing.T) {
	b, srv := newTestCoop(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/screen/text" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("line1\nline2\nline3\nline4\nline5"))
	}))
	defer srv.Close()

	text, err := b.CapturePane("test", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "line3\nline4\nline5" {
		t.Errorf("got %q, want last 3 lines", text)
	}
}

func TestCoopBackend_CapturePane_AllLines(t *testing.T) {
	b, srv := newTestCoop(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("a\nb\nc"))
	}))
	defer srv.Close()

	text, err := b.CapturePane("test", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "a\nb\nc" {
		t.Errorf("got %q, want full text", text)
	}
}

func TestCoopBackend_CapturePane_NotRegistered(t *testing.T) {
	b := NewCoopBackend(CoopConfig{})

	_, err := b.CapturePane("missing", 10)
	if err == nil {
		t.Fatal("expected error for unregistered session")
	}
}

func TestCoopBackend_NudgeSession(t *testing.T) {
	var gotMessage string
	b, srv := newTestCoop(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agent/nudge" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		var req coopNudgeRequest
		json.NewDecoder(r.Body).Decode(&req)
		gotMessage = req.Message
		json.NewEncoder(w).Encode(coopNudgeResponse{Delivered: true})
	}))
	defer srv.Close()

	err := b.NudgeSession("test", "hello agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMessage != "hello agent" {
		t.Errorf("got message %q, want %q", gotMessage, "hello agent")
	}
}

func TestCoopBackend_NudgeSession_NotDelivered(t *testing.T) {
	reason := "agent_busy"
	b, srv := newTestCoop(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(coopNudgeResponse{
			Delivered: false,
			Reason:    &reason,
		})
	}))
	defer srv.Close()

	err := b.NudgeSession("test", "hello")
	if err == nil {
		t.Fatal("expected error when nudge not delivered")
	}
	if !strings.Contains(err.Error(), "agent_busy") {
		t.Errorf("error should contain reason, got: %v", err)
	}
}

func TestCoopBackend_SendKeys(t *testing.T) {
	var gotKeys []string
	b, srv := newTestCoop(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/input/keys" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var req coopKeysRequest
		json.NewDecoder(r.Body).Decode(&req)
		gotKeys = req.Keys
		json.NewEncoder(w).Encode(map[string]int{"bytes_written": 2})
	}))
	defer srv.Close()

	err := b.SendKeys("test", "Enter Escape")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gotKeys) != 2 || gotKeys[0] != "Enter" || gotKeys[1] != "Escape" {
		t.Errorf("got keys %v, want [Enter Escape]", gotKeys)
	}
}

func TestCoopBackend_SendKeys_Empty(t *testing.T) {
	b := NewCoopBackend(CoopConfig{})
	b.AddSession("test", "http://unused")

	err := b.SendKeys("test", "")
	if err != nil {
		t.Fatalf("unexpected error for empty keys: %v", err)
	}
}

func TestCoopBackend_AgentState(t *testing.T) {
	b, srv := newTestCoop(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agent/state" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(CoopAgentState{
			Agent:         "claude-code",
			State:         "working",
			DetectionTier: "tier2_nats",
		})
	}))
	defer srv.Close()

	state, err := b.AgentState("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.State != "working" {
		t.Errorf("got state %q, want %q", state.State, "working")
	}
	if state.Agent != "claude-code" {
		t.Errorf("got agent %q, want %q", state.Agent, "claude-code")
	}
}

func TestCoopBackend_RespondToPrompt(t *testing.T) {
	b, srv := newTestCoop(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agent/respond" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var req CoopRespondRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Accept == nil || !*req.Accept {
			t.Error("expected accept=true")
		}
		json.NewEncoder(w).Encode(CoopRespondResponse{Delivered: true})
	}))
	defer srv.Close()

	accept := true
	err := b.RespondToPrompt("test", CoopRespondRequest{Accept: &accept})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCoopBackend_AuthToken(t *testing.T) {
	var gotAuth string
	b, srv := newTestCoop(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(coopHealthResponse{Status: "running"})
	}))
	b.token = "secret-token"
	defer srv.Close()

	b.HasSession("test")
	if gotAuth != "Bearer secret-token" {
		t.Errorf("got auth %q, want %q", gotAuth, "Bearer secret-token")
	}
}

func TestCoopBackend_SessionManagement(t *testing.T) {
	b := NewCoopBackend(CoopConfig{})

	b.AddSession("a", "http://host-a:8080")
	b.AddSession("b", "http://host-b:8080")

	url, err := b.baseURL("a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "http://host-a:8080" {
		t.Errorf("got %q, want %q", url, "http://host-a:8080")
	}

	b.RemoveSession("a")
	_, err = b.baseURL("a")
	if err == nil {
		t.Error("expected error after removing session")
	}

	url, err = b.baseURL("b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "http://host-b:8080" {
		t.Errorf("got %q, want %q", url, "http://host-b:8080")
	}
}

func TestCoopBackend_TrailingSlash(t *testing.T) {
	b := NewCoopBackend(CoopConfig{})
	b.AddSession("s", "http://host:8080/")

	url, err := b.baseURL("s")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "http://host:8080" {
		t.Errorf("got %q, want %q (trailing slash should be stripped)", url, "http://host:8080")
	}
}
