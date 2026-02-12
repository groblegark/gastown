package podmanager

import (
	"testing"
	"time"
)

func TestSidecarRateLimiter_AllowWithinLimit(t *testing.T) {
	rl := NewSidecarRateLimiter(3)

	if !rl.Allow("agent-1") {
		t.Error("first change should be allowed")
	}
	if !rl.Allow("agent-1") {
		t.Error("second change should be allowed")
	}
	if !rl.Allow("agent-1") {
		t.Error("third change should be allowed")
	}
}

func TestSidecarRateLimiter_BlockExceedingLimit(t *testing.T) {
	rl := NewSidecarRateLimiter(3)

	for i := 0; i < 3; i++ {
		rl.Allow("agent-1")
	}

	if rl.Allow("agent-1") {
		t.Error("fourth change should be blocked")
	}
	if rl.Allow("agent-1") {
		t.Error("fifth change should also be blocked")
	}
}

func TestSidecarRateLimiter_IndependentAgents(t *testing.T) {
	rl := NewSidecarRateLimiter(2)

	rl.Allow("agent-1")
	rl.Allow("agent-1")

	// agent-2 should have its own counter
	if !rl.Allow("agent-2") {
		t.Error("agent-2 should be allowed (independent from agent-1)")
	}

	// agent-1 should still be blocked
	if rl.Allow("agent-1") {
		t.Error("agent-1 should still be blocked")
	}
}

func TestSidecarRateLimiter_DisabledWithZero(t *testing.T) {
	rl := NewSidecarRateLimiter(0)

	for i := 0; i < 100; i++ {
		if !rl.Allow("agent-1") {
			t.Fatalf("change %d should be allowed when rate limiting is disabled", i+1)
		}
	}
}

func TestSidecarRateLimiter_WindowExpiry(t *testing.T) {
	rl := NewSidecarRateLimiter(2)
	rl.window = 100 * time.Millisecond // short window for testing

	rl.Allow("agent-1")
	rl.Allow("agent-1")

	if rl.Allow("agent-1") {
		t.Error("should be blocked at limit")
	}

	// Wait for window to expire
	time.Sleep(150 * time.Millisecond)

	if !rl.Allow("agent-1") {
		t.Error("should be allowed after window expires")
	}
}

func TestSidecarRateLimiter_Count(t *testing.T) {
	rl := NewSidecarRateLimiter(5)

	if rl.Count("agent-1") != 0 {
		t.Error("count should be 0 for unknown agent")
	}

	rl.Allow("agent-1")
	rl.Allow("agent-1")
	rl.Allow("agent-1")

	if c := rl.Count("agent-1"); c != 3 {
		t.Errorf("count = %d, want 3", c)
	}

	if c := rl.Count("agent-2"); c != 0 {
		t.Errorf("count for agent-2 = %d, want 0", c)
	}
}

func TestSidecarRateLimiter_CountAfterExpiry(t *testing.T) {
	rl := NewSidecarRateLimiter(5)
	rl.window = 100 * time.Millisecond

	rl.Allow("agent-1")
	rl.Allow("agent-1")

	time.Sleep(150 * time.Millisecond)

	if c := rl.Count("agent-1"); c != 0 {
		t.Errorf("count should be 0 after window expiry, got %d", c)
	}
}
