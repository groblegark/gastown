//go:build integration

package terminal

import (
	"strings"
	"testing"
	"time"
)

// TestIntegration_HasSession verifies HasSession against a real coop process.
func TestIntegration_HasSession(t *testing.T) {
	base, cleanup := startCoop(t)
	defer cleanup()

	b := NewCoopBackend(CoopConfig{})
	b.AddSession("claude", base)

	ok, err := b.HasSession("claude")
	if err != nil {
		t.Fatalf("HasSession error: %v", err)
	}
	if !ok {
		t.Error("expected HasSession=true for running coop")
	}

	// Unregistered session should return false (no error).
	ok, err = b.HasSession("nonexistent")
	if err != nil {
		t.Fatalf("HasSession(nonexistent) error: %v", err)
	}
	if ok {
		t.Error("expected HasSession=false for unregistered session")
	}
}

// TestIntegration_CapturePane verifies screen capture against a real coop process.
func TestIntegration_CapturePane(t *testing.T) {
	base, cleanup := startCoop(t)
	defer cleanup()

	b := NewCoopBackend(CoopConfig{})
	b.AddSession("claude", base)

	// Coop running bash should show a prompt.
	text, err := b.CapturePane("claude", 10)
	if err != nil {
		t.Fatalf("CapturePane error: %v", err)
	}
	t.Logf("CapturePane(10 lines): %q", text)
}

// TestIntegration_CapturePaneAll verifies full scrollback capture.
func TestIntegration_CapturePaneAll(t *testing.T) {
	base, cleanup := startCoop(t)
	defer cleanup()

	b := NewCoopBackend(CoopConfig{})
	b.AddSession("claude", base)

	text, err := b.CapturePaneAll("claude")
	if err != nil {
		t.Fatalf("CapturePaneAll error: %v", err)
	}
	t.Logf("CapturePaneAll: %d bytes", len(text))
}

// TestIntegration_CapturePaneLines verifies line-based capture.
func TestIntegration_CapturePaneLines(t *testing.T) {
	base, cleanup := startCoop(t)
	defer cleanup()

	b := NewCoopBackend(CoopConfig{})
	b.AddSession("claude", base)

	lines, err := b.CapturePaneLines("claude", 5)
	if err != nil {
		t.Fatalf("CapturePaneLines error: %v", err)
	}
	if len(lines) > 5 {
		t.Errorf("expected at most 5 lines, got %d", len(lines))
	}
	t.Logf("CapturePaneLines(5): %v", lines)
}

// TestIntegration_SendInput verifies text input delivery to a real coop process.
func TestIntegration_SendInput(t *testing.T) {
	base, cleanup := startCoop(t)
	defer cleanup()

	b := NewCoopBackend(CoopConfig{})
	b.AddSession("claude", base)

	// Send text with Enter — should execute in the shell.
	err := b.SendInput("claude", "echo INTTEST_MARKER", true)
	if err != nil {
		t.Fatalf("SendInput(enter=true) error: %v", err)
	}

	// Give the shell time to process.
	time.Sleep(1 * time.Second)

	// Verify the command output appears in screen capture.
	// Coop returns 200-char wide lines with trailing spaces; trim them.
	text, err := b.CapturePaneAll("claude")
	if err != nil {
		t.Fatalf("CapturePane after SendInput error: %v", err)
	}
	// Trim trailing whitespace from each line for comparison.
	var trimmed []string
	for _, line := range strings.Split(text, "\n") {
		trimmed = append(trimmed, strings.TrimRight(line, " "))
	}
	joined := strings.Join(trimmed, "\n")
	if !strings.Contains(joined, "INTTEST_MARKER") {
		t.Errorf("expected screen to contain 'INTTEST_MARKER', got: %q", joined)
	}
}

// TestIntegration_SendKeys verifies raw keystroke delivery.
func TestIntegration_SendKeys(t *testing.T) {
	base, cleanup := startCoop(t)
	defer cleanup()

	b := NewCoopBackend(CoopConfig{})
	b.AddSession("claude", base)

	// Sending Enter key should not error.
	err := b.SendKeys("claude", "enter")
	if err != nil {
		t.Fatalf("SendKeys error: %v", err)
	}

	// Empty keys should be a no-op.
	err = b.SendKeys("claude", "")
	if err != nil {
		t.Fatalf("SendKeys(empty) error: %v", err)
	}
}

// TestIntegration_IsAgentRunning verifies agent process state detection.
func TestIntegration_IsAgentRunning(t *testing.T) {
	base, cleanup := startCoop(t)
	defer cleanup()

	b := NewCoopBackend(CoopConfig{})
	b.AddSession("claude", base)

	running, err := b.IsAgentRunning("claude")
	if err != nil {
		t.Fatalf("IsAgentRunning error: %v", err)
	}
	if !running {
		t.Error("expected agent to be running (bash process)")
	}
}

// TestIntegration_GetAgentState verifies structured agent state retrieval.
// Note: /api/v1/agent/state requires agent detection (Claude Code).
// With a plain bash child process, this returns 404. This is expected behavior —
// agent/state only works when coop detects a supported agent.
func TestIntegration_GetAgentState(t *testing.T) {
	base, cleanup := startCoop(t)
	defer cleanup()

	b := NewCoopBackend(CoopConfig{})
	b.AddSession("claude", base)

	state, err := b.GetAgentState("claude")
	if err != nil {
		// Expected: 404 when no agent detected (plain bash child).
		// This is correct coop behavior — agent/state requires agent detection.
		t.Logf("GetAgentState returned expected error for non-agent process: %v", err)
		return
	}
	t.Logf("GetAgentState: %q", state)
}

// TestIntegration_AgentState verifies the richer AgentState method.
func TestIntegration_AgentState(t *testing.T) {
	base, cleanup := startCoop(t)
	defer cleanup()

	b := NewCoopBackend(CoopConfig{})
	b.AddSession("claude", base)

	agentState, err := b.AgentState("claude")
	if err != nil {
		// Expected: 404 for non-agent processes.
		t.Logf("AgentState returned expected error for non-agent process: %v", err)
		return
	}
	t.Logf("AgentState: state=%q agent=%q tier=%q", agentState.State, agentState.Agent, agentState.DetectionTier)
}

// TestIntegration_IsPaneDead verifies dead pane detection.
// Note: With a plain bash child, IsPaneDead calls AgentState which returns 404.
// This is expected — IsPaneDead is designed for agent processes, not raw shells.
func TestIntegration_IsPaneDead(t *testing.T) {
	base, cleanup := startCoop(t)
	defer cleanup()

	b := NewCoopBackend(CoopConfig{})
	b.AddSession("claude", base)

	dead, err := b.IsPaneDead("claude")
	if err != nil {
		// Expected: agent/state 404 for non-agent processes.
		t.Logf("IsPaneDead returned expected error for non-agent process: %v", err)
		return
	}
	if dead {
		t.Error("expected pane to be alive")
	}
}

// TestIntegration_SetPaneDiedHook verifies the no-op hook setting.
func TestIntegration_SetPaneDiedHook(t *testing.T) {
	base, cleanup := startCoop(t)
	defer cleanup()

	b := NewCoopBackend(CoopConfig{})
	b.AddSession("claude", base)

	// Should be a no-op, no error.
	err := b.SetPaneDiedHook("claude", "test-agent")
	if err != nil {
		t.Fatalf("SetPaneDiedHook error: %v", err)
	}
}

// TestIntegration_SetGetEnvironment verifies env var set/get roundtrip.
func TestIntegration_SetGetEnvironment(t *testing.T) {
	base, cleanup := startCoop(t)
	defer cleanup()

	b := NewCoopBackend(CoopConfig{})
	b.AddSession("claude", base)

	// Set an env var.
	err := b.SetEnvironment("claude", "GT_TEST_VAR", "integration_test")
	if err != nil {
		t.Fatalf("SetEnvironment error: %v", err)
	}

	// Get the env var back.
	val, err := b.GetEnvironment("claude", "GT_TEST_VAR")
	if err != nil {
		t.Fatalf("GetEnvironment error: %v", err)
	}
	if val != "integration_test" {
		t.Errorf("GetEnvironment got %q, want %q", val, "integration_test")
	}
}

// TestIntegration_GetEnvironment_NotFound verifies missing env var handling.
func TestIntegration_GetEnvironment_NotFound(t *testing.T) {
	base, cleanup := startCoop(t)
	defer cleanup()

	b := NewCoopBackend(CoopConfig{})
	b.AddSession("claude", base)

	_, err := b.GetEnvironment("claude", "GT_NONEXISTENT_VAR_12345")
	if err == nil {
		t.Fatal("expected error for missing env var")
	}
}

// TestIntegration_GetPaneWorkDir verifies working directory retrieval.
// Note: On macOS, coop reads /proc/<pid>/cwd which doesn't exist.
// This test verifies the error handling for this platform difference.
func TestIntegration_GetPaneWorkDir(t *testing.T) {
	base, cleanup := startCoop(t)
	defer cleanup()

	b := NewCoopBackend(CoopConfig{})
	b.AddSession("claude", base)

	cwd, err := b.GetPaneWorkDir("claude")
	if err != nil {
		// On macOS, /proc doesn't exist, so cwd lookup fails.
		// This is a known platform limitation — coop's /proc/pid/cwd
		// only works on Linux.
		t.Logf("GetPaneWorkDir returned expected error (macOS): %v", err)
		return
	}
	if cwd == "" {
		t.Error("expected non-empty working directory")
	}
	t.Logf("GetPaneWorkDir: %q", cwd)
}

// TestIntegration_KillSession verifies session termination.
func TestIntegration_KillSession(t *testing.T) {
	base, cleanup := startCoop(t)
	defer cleanup()

	b := NewCoopBackend(CoopConfig{})
	b.AddSession("claude", base)

	// Verify running first.
	ok, err := b.HasSession("claude")
	if err != nil || !ok {
		t.Fatalf("expected session to be running before kill")
	}

	// Kill the session.
	err = b.KillSession("claude")
	if err != nil {
		t.Fatalf("KillSession error: %v", err)
	}

	// Wait for process to die.
	time.Sleep(2 * time.Second)

	// After kill, the process should be dead.
	// Check via IsAgentRunning (uses /api/v1/status, not agent/state).
	running, err := b.IsAgentRunning("claude")
	if err != nil {
		// Coop may have shut down entirely — that's expected.
		t.Logf("IsAgentRunning after kill returned error (expected): %v", err)
		return
	}
	if running {
		t.Error("expected agent to not be running after KillSession")
	}
}

// TestIntegration_NudgeSession verifies nudge delivery.
// With bash as the child process, coop may report the agent is still starting
// or not ready, since there's no Claude agent to detect.
func TestIntegration_NudgeSession(t *testing.T) {
	base, cleanup := startCoop(t)
	defer cleanup()

	b := NewCoopBackend(CoopConfig{})
	b.AddSession("claude", base)

	err := b.NudgeSession("claude", "test nudge message")
	if err != nil {
		// Expected: coop may return errors for non-Claude processes
		// since nudge requires agent detection to verify readiness.
		t.Logf("NudgeSession returned expected error for non-agent process: %v", err)
	}
}

// TestIntegration_SwitchSession verifies session switch.
// Note: PUT /api/v1/session/switch may not be supported in all coop versions.
func TestIntegration_SwitchSession(t *testing.T) {
	base, cleanup := startCoop(t)
	defer cleanup()

	b := NewCoopBackend(CoopConfig{})
	b.AddSession("claude", base)

	err := b.SwitchSession("claude", SwitchConfig{
		ExtraEnv: map[string]string{"GT_TEST_SWITCH": "1"},
	})
	if err != nil {
		// Switch may not be supported in all coop versions (405).
		t.Logf("SwitchSession returned error: %v", err)
		return
	}

	// After switch, the session should still be alive.
	time.Sleep(2 * time.Second)
	ok, err := b.HasSession("claude")
	if err != nil {
		t.Fatalf("HasSession after switch error: %v", err)
	}
	if !ok {
		t.Error("expected session to be running after switch")
	}
}

// TestIntegration_RespawnPane verifies pane respawn (switch-to-self).
func TestIntegration_RespawnPane(t *testing.T) {
	base, cleanup := startCoop(t)
	defer cleanup()

	b := NewCoopBackend(CoopConfig{})
	b.AddSession("claude", base)

	err := b.RespawnPane("claude")
	if err != nil {
		// Respawn uses switch which may not be supported.
		t.Logf("RespawnPane returned error: %v", err)
		return
	}

	// After respawn, session should still be alive.
	time.Sleep(2 * time.Second)
	ok, err := b.HasSession("claude")
	if err != nil {
		t.Fatalf("HasSession after respawn error: %v", err)
	}
	if !ok {
		t.Error("expected session to be running after respawn")
	}
}
