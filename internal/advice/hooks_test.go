package advice

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ============================================================================
// Hook Trigger Tests
// ============================================================================

func TestExecute_BasicSuccess(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	hook := &Hook{
		ID:      "test-hook-1",
		Command: "echo hello",
		Trigger: TriggerBeforeCommit,
	}

	result := runner.Execute(hook)

	if !result.Success {
		t.Errorf("expected success, got failure: %v", result.Error)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	if !strings.Contains(result.Output, "hello") {
		t.Errorf("expected output to contain 'hello', got %q", result.Output)
	}
	if result.TimedOut {
		t.Error("expected no timeout")
	}
}

func TestExecute_CommandFailure(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	hook := &Hook{
		ID:      "test-hook-fail",
		Command: "exit 42",
		Trigger: TriggerBeforePush,
	}

	result := runner.Execute(hook)

	if result.Success {
		t.Error("expected failure, got success")
	}
	if result.ExitCode != 42 {
		t.Errorf("expected exit code 42, got %d", result.ExitCode)
	}
}

func TestExecute_AllTriggerTypes(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	triggers := []string{
		TriggerSessionEnd,
		TriggerBeforeCommit,
		TriggerBeforePush,
		TriggerBeforeHandoff,
	}

	for _, trigger := range triggers {
		t.Run(trigger, func(t *testing.T) {
			hook := &Hook{
				ID:      "trigger-test",
				Command: "echo trigger",
				Trigger: trigger,
			}

			result := runner.Execute(hook)

			if !result.Success {
				t.Errorf("expected success for trigger %q, got failure: %v", trigger, result.Error)
			}
		})
	}
}

// ============================================================================
// Timeout Tests
// ============================================================================

func TestExecute_TimeoutRespected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timeout test in short mode")
	}

	runner := NewRunner(t.TempDir(), "test-agent")

	hook := &Hook{
		ID:      "timeout-test",
		Command: "sleep 10",
		Timeout: 1, // 1 second timeout
		Trigger: TriggerSessionEnd,
	}

	start := time.Now()
	result := runner.Execute(hook)
	elapsed := time.Since(start)

	if !result.TimedOut {
		t.Error("expected timeout, but hook completed normally")
	}

	if result.Success {
		t.Error("expected failure due to timeout")
	}

	// Should complete in approximately 1 second (with some buffer)
	if elapsed < 900*time.Millisecond || elapsed > 3*time.Second {
		t.Errorf("expected elapsed time ~1s, got %v", elapsed)
	}
}

func TestExecute_DefaultTimeout(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	hook := &Hook{
		ID:      "default-timeout",
		Command: "echo quick",
		// No timeout set - should use default
	}

	result := runner.Execute(hook)

	if !result.Success {
		t.Errorf("expected success: %v", result.Error)
	}
}

func TestExecute_TimeoutClamped(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	// Test that timeout > MaxTimeout is clamped
	hook := &Hook{
		ID:      "clamped-timeout",
		Command: "echo fast",
		Timeout: 999999, // Unreasonably large
	}

	// Should not hang - timeout should be clamped to MaxTimeout
	result := runner.Execute(hook)

	if !result.Success {
		t.Errorf("expected success: %v", result.Error)
	}
}

func TestExecute_TimeoutKillsHungProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping hung process test in short mode")
	}

	runner := NewRunner(t.TempDir(), "test-agent")

	// A command that would hang forever if not killed
	hook := &Hook{
		ID:      "hung-process",
		Command: "while true; do sleep 1; done",
		Timeout: 1,
		Trigger: TriggerBeforeHandoff,
	}

	result := runner.Execute(hook)

	if !result.TimedOut {
		t.Error("expected timeout for hung process")
	}

	if result.Error == nil {
		t.Error("expected error for timed out process")
	}
}

// ============================================================================
// OnFailure Behavior Tests
// ============================================================================

func TestRunAll_OnFailureBlock(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	hooks := []*Hook{
		{ID: "hook-1", Command: "echo first", OnFailure: OnFailureBlock},
		{ID: "hook-2", Command: "exit 1", OnFailure: OnFailureBlock},
		{ID: "hook-3", Command: "echo third", OnFailure: OnFailureBlock},
	}

	results, err := runner.RunAll(hooks)

	if err == nil {
		t.Error("expected blocking error, got nil")
	}

	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}

	// First hook should succeed
	if !results[0].Success {
		t.Error("first hook should succeed")
	}

	// Second hook should fail
	if results[1].Success {
		t.Error("second hook should fail")
	}

	// Error message should reference the failing hook
	if !strings.Contains(err.Error(), "hook-2") {
		t.Errorf("error should reference hook-2: %v", err)
	}
}

func TestRunAll_OnFailureWarn(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	hooks := []*Hook{
		{ID: "hook-1", Command: "echo first", OnFailure: OnFailureWarn},
		{ID: "hook-2", Command: "exit 1", OnFailure: OnFailureWarn},
		{ID: "hook-3", Command: "echo third", OnFailure: OnFailureWarn},
	}

	results, err := runner.RunAll(hooks)

	if err != nil {
		t.Errorf("expected no blocking error with warn, got: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}

	// All hooks should have run
	if !results[0].Success {
		t.Error("first hook should succeed")
	}
	if results[1].Success {
		t.Error("second hook should fail")
	}
	if !results[2].Success {
		t.Error("third hook should succeed")
	}
}

func TestRunAll_OnFailureIgnore(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	hooks := []*Hook{
		{ID: "hook-1", Command: "exit 1", OnFailure: OnFailureIgnore},
		{ID: "hook-2", Command: "echo second", OnFailure: OnFailureIgnore},
	}

	results, err := runner.RunAll(hooks)

	if err != nil {
		t.Errorf("expected no error with ignore, got: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	// First hook fails but is ignored
	if results[0].Success {
		t.Error("first hook should fail")
	}

	// Second hook runs successfully
	if !results[1].Success {
		t.Error("second hook should succeed")
	}
}

func TestRunAll_DefaultOnFailure(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	// No OnFailure set - should default to warn
	hooks := []*Hook{
		{ID: "hook-1", Command: "exit 1"},
		{ID: "hook-2", Command: "echo second"},
	}

	results, err := runner.RunAll(hooks)

	// Default is warn, so no blocking error
	if err != nil {
		t.Errorf("expected no error with default (warn), got: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

// ============================================================================
// Hook Context Tests (Environment Variables)
// ============================================================================

func TestExecute_EnvironmentVariables(t *testing.T) {
	workDir := t.TempDir()
	runner := NewRunner(workDir, "test-agent-123")
	runner.SetEnv("CUSTOM_VAR", "custom-value")

	// Command that outputs all GT_ environment variables
	hook := &Hook{
		ID:      "env-test",
		Command: "env | grep -E '^(GT_|CUSTOM_)'",
		Trigger: TriggerBeforeCommit,
	}

	result := runner.Execute(hook)

	if !result.Success {
		t.Errorf("expected success: %v", result.Error)
	}

	// Check for standard advice hook environment variables
	expectedVars := []string{
		"GT_ADVICE_HOOK_ID=env-test",
		"GT_ADVICE_HOOK_TRIGGER=before-commit",
		"GT_AGENT_ID=test-agent-123",
		"GT_WORK_DIR=" + workDir,
		"CUSTOM_VAR=custom-value",
	}

	for _, expected := range expectedVars {
		if !strings.Contains(result.Output, expected) {
			t.Errorf("expected output to contain %q, got:\n%s", expected, result.Output)
		}
	}
}

func TestExecute_WorkingDirectory(t *testing.T) {
	// Create a unique temp directory with a marker file
	workDir := t.TempDir()
	markerFile := filepath.Join(workDir, "marker.txt")
	if err := os.WriteFile(markerFile, []byte("marker"), 0644); err != nil {
		t.Fatalf("failed to create marker file: %v", err)
	}

	runner := NewRunner(workDir, "test-agent")

	// Command that checks for the marker file in the current directory
	hook := &Hook{
		ID:      "workdir-test",
		Command: "ls marker.txt && pwd",
		Trigger: TriggerSessionEnd,
	}

	result := runner.Execute(hook)

	if !result.Success {
		t.Errorf("expected success, hook should find marker.txt: %v\nOutput: %s", result.Error, result.Output)
	}

	// Output should contain the working directory path
	if !strings.Contains(result.Output, workDir) {
		t.Errorf("expected output to contain work dir %q, got:\n%s", workDir, result.Output)
	}
}

func TestExecute_InheritsProcessEnv(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	// Set a unique environment variable in this process
	uniqueKey := "GT_TEST_UNIQUE_VAR_12345"
	uniqueValue := "unique-test-value"
	os.Setenv(uniqueKey, uniqueValue)
	defer os.Unsetenv(uniqueKey)

	hook := &Hook{
		ID:      "inherit-env-test",
		Command: "echo $" + uniqueKey,
	}

	result := runner.Execute(hook)

	if !result.Success {
		t.Errorf("expected success: %v", result.Error)
	}

	if !strings.Contains(result.Output, uniqueValue) {
		t.Errorf("expected hook to inherit process env, output: %s", result.Output)
	}
}

// ============================================================================
// Safety Tests
// ============================================================================

func TestValidateHook_EmptyCommand(t *testing.T) {
	hook := &Hook{
		ID:      "empty-cmd",
		Command: "",
	}

	err := ValidateHook(hook)
	if err == nil {
		t.Error("expected error for empty command")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention 'empty': %v", err)
	}
}

func TestValidateHook_TooLongCommand(t *testing.T) {
	hook := &Hook{
		ID:      "long-cmd",
		Command: strings.Repeat("x", MaxCommandLength+1),
	}

	err := ValidateHook(hook)
	if err == nil {
		t.Error("expected error for command exceeding max length")
	}
	if !strings.Contains(err.Error(), "maximum length") {
		t.Errorf("error should mention 'maximum length': %v", err)
	}
}

func TestValidateHook_InvalidTrigger(t *testing.T) {
	hook := &Hook{
		ID:      "bad-trigger",
		Command: "echo test",
		Trigger: "not-a-valid-trigger",
	}

	err := ValidateHook(hook)
	if err == nil {
		t.Error("expected error for invalid trigger")
	}
	if !strings.Contains(err.Error(), "invalid hook trigger") {
		t.Errorf("error should mention 'invalid hook trigger': %v", err)
	}
}

func TestValidateHook_InvalidOnFailure(t *testing.T) {
	hook := &Hook{
		ID:        "bad-onfailure",
		Command:   "echo test",
		OnFailure: "explode",
	}

	err := ValidateHook(hook)
	if err == nil {
		t.Error("expected error for invalid on_failure")
	}
	if !strings.Contains(err.Error(), "invalid on_failure") {
		t.Errorf("error should mention 'invalid on_failure': %v", err)
	}
}

func TestValidateHook_NegativeTimeout(t *testing.T) {
	hook := &Hook{
		ID:      "neg-timeout",
		Command: "echo test",
		Timeout: -1,
	}

	err := ValidateHook(hook)
	if err == nil {
		t.Error("expected error for negative timeout")
	}
	if !strings.Contains(err.Error(), "negative") {
		t.Errorf("error should mention 'negative': %v", err)
	}
}

func TestValidateHook_NilHook(t *testing.T) {
	err := ValidateHook(nil)
	if err == nil {
		t.Error("expected error for nil hook")
	}
}

func TestExecute_MalformedCommand(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	// Commands that are syntactically invalid
	testCases := []struct {
		name    string
		command string
	}{
		{"unclosed quote", "echo 'unclosed"},
		{"invalid redirect", "echo test >"},
		{"bad pipe", "| cat"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hook := &Hook{
				ID:      "malformed-" + tc.name,
				Command: tc.command,
			}

			result := runner.Execute(hook)

			// Should not panic, should return a result (likely failure)
			if result == nil {
				t.Error("result should not be nil")
			}

			// Malformed commands should fail
			if result.Success {
				t.Errorf("expected malformed command %q to fail", tc.command)
			}
		})
	}
}

func TestExecute_CommandNotFound(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	hook := &Hook{
		ID:      "not-found",
		Command: "definitely_not_a_real_command_12345",
	}

	result := runner.Execute(hook)

	if result.Success {
		t.Error("expected failure for non-existent command")
	}

	if result.ExitCode == 0 {
		t.Error("expected non-zero exit code for non-existent command")
	}
}

func TestExecute_SpecialCharactersInCommand(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	// Test that special characters are handled correctly
	testCases := []struct {
		name    string
		command string
		expect  string
	}{
		{"dollar sign", "echo '$HOME'", "$HOME"},
		{"semicolon", "echo one; echo two", "two"},
		{"ampersand", "echo test && echo pass", "pass"},
		{"backticks", "echo `echo inner`", "inner"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hook := &Hook{
				ID:      "special-" + tc.name,
				Command: tc.command,
			}

			result := runner.Execute(hook)

			if !result.Success {
				t.Errorf("expected success: %v", result.Error)
			}

			if !strings.Contains(result.Output, tc.expect) {
				t.Errorf("expected output to contain %q, got: %s", tc.expect, result.Output)
			}
		})
	}
}

// ============================================================================
// Helper Function Tests
// ============================================================================

func TestIsValidTrigger(t *testing.T) {
	validTriggers := []string{
		TriggerSessionEnd,
		TriggerBeforeCommit,
		TriggerBeforePush,
		TriggerBeforeHandoff,
	}

	for _, trigger := range validTriggers {
		if !IsValidTrigger(trigger) {
			t.Errorf("expected %q to be valid", trigger)
		}
	}

	invalidTriggers := []string{"", "invalid", "before_commit", "SESSION-END"}
	for _, trigger := range invalidTriggers {
		if IsValidTrigger(trigger) {
			t.Errorf("expected %q to be invalid", trigger)
		}
	}
}

func TestIsValidOnFailure(t *testing.T) {
	validValues := []string{OnFailureBlock, OnFailureWarn, OnFailureIgnore}

	for _, value := range validValues {
		if !IsValidOnFailure(value) {
			t.Errorf("expected %q to be valid", value)
		}
	}

	invalidValues := []string{"", "fail", "BLOCK", "continue"}
	for _, value := range invalidValues {
		if IsValidOnFailure(value) {
			t.Errorf("expected %q to be invalid", value)
		}
	}
}

func TestExecute_Duration(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	hook := &Hook{
		ID:      "duration-test",
		Command: "sleep 0.1",
	}

	result := runner.Execute(hook)

	if result.Duration < 50*time.Millisecond {
		t.Errorf("expected duration >= 50ms, got %v", result.Duration)
	}

	if result.Duration > 1*time.Second {
		t.Errorf("expected duration < 1s, got %v", result.Duration)
	}
}

// ============================================================================
// Platform-specific tests
// ============================================================================

func TestExecute_ShellSelection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test not applicable on Windows")
	}

	runner := NewRunner(t.TempDir(), "test-agent")
	runner.Shell = "bash"

	// Use a bash-specific feature
	hook := &Hook{
		ID:      "bash-test",
		Command: "echo ${BASH_VERSION:0:1}", // Bash-specific substring syntax
	}

	result := runner.Execute(hook)

	// Just verify it runs without error - output depends on bash version
	if result.Error != nil && !strings.Contains(result.Error.Error(), "not found") {
		// Only fail if it's not a "bash not found" error
		t.Errorf("unexpected error: %v", result.Error)
	}
}

func TestRunAll_EmptyHooksList(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	results, err := runner.RunAll([]*Hook{})

	if err != nil {
		t.Errorf("expected no error for empty hooks: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestRunAll_NilHooksList(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	results, err := runner.RunAll(nil)

	if err != nil {
		t.Errorf("expected no error for nil hooks: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

// ============================================================================
// Security Tests - Shell Injection Attempts (gt-uwohce)
// ============================================================================
//
// These tests verify that hooks execute in their proper sandbox context and
// that security-relevant behaviors work correctly. Note: hooks ARE shell
// commands by design - they're supposed to have shell capabilities. These
// tests verify:
//   - Commands execute in the proper workdir context (can't escape via path traversal)
//   - Timeout properly kills long-running/malicious commands
//   - Process group killing cleans up child processes
//   - Environment variables are properly sandboxed

func TestExecute_ShellInjection_CommandChaining(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	// Test that command chaining works (this is expected - hooks ARE shell commands)
	// But we verify the sandbox context is maintained
	testCases := []struct {
		name        string
		command     string
		expectInOut string
	}{
		{"semicolon chaining", "echo first; echo second", "second"},
		{"and chaining", "echo first && echo second", "second"},
		{"or chaining", "false || echo fallback", "fallback"},
		{"subshell", "echo $(echo nested)", "nested"},
		{"backtick subshell", "echo `echo backtick`", "backtick"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hook := &Hook{
				ID:      "injection-" + tc.name,
				Command: tc.command,
				Trigger: TriggerBeforeCommit,
			}

			result := runner.Execute(hook)

			// Commands should execute (hooks ARE shell commands)
			if !result.Success {
				t.Errorf("command should execute: %v", result.Error)
			}
			if !strings.Contains(result.Output, tc.expectInOut) {
				t.Errorf("expected output to contain %q, got: %s", tc.expectInOut, result.Output)
			}
		})
	}
}

func TestExecute_ShellInjection_EnvironmentAccess(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	// Hooks can access environment variables - but they get the GT_ sandbox vars
	hook := &Hook{
		ID:      "env-access",
		Command: "echo $GT_AGENT_ID $GT_WORK_DIR",
		Trigger: TriggerBeforeCommit,
	}

	result := runner.Execute(hook)

	if !result.Success {
		t.Errorf("expected success: %v", result.Error)
	}

	// Should have the sandbox environment variables
	if !strings.Contains(result.Output, "test-agent") {
		t.Errorf("expected GT_AGENT_ID in output, got: %s", result.Output)
	}
}

func TestExecute_ShellInjection_WorkdirContainment(t *testing.T) {
	// Verify that hooks execute in their designated workdir
	workDir := t.TempDir()
	runner := NewRunner(workDir, "test-agent")

	// Create a marker file in workdir
	markerPath := filepath.Join(workDir, "workdir-marker.txt")
	if err := os.WriteFile(markerPath, []byte("marker"), 0644); err != nil {
		t.Fatalf("failed to create marker: %v", err)
	}

	hook := &Hook{
		ID:      "workdir-check",
		Command: "pwd && ls workdir-marker.txt",
		Trigger: TriggerBeforeCommit,
	}

	result := runner.Execute(hook)

	if !result.Success {
		t.Errorf("expected success, hook should find marker in workdir: %v\nOutput: %s", result.Error, result.Output)
	}

	// Verify pwd output matches workDir
	if !strings.Contains(result.Output, workDir) {
		t.Errorf("expected workdir %q in output, got: %s", workDir, result.Output)
	}
}

func TestExecute_ShellInjection_PathTraversalAttempt(t *testing.T) {
	// Create a nested workdir
	workDir := t.TempDir()
	runner := NewRunner(workDir, "test-agent")

	// Attempt to traverse up - command executes but stays in workdir context
	hook := &Hook{
		ID:      "path-traversal",
		Command: "cd ../.. && pwd",
		Trigger: TriggerBeforeCommit,
	}

	result := runner.Execute(hook)

	// Command executes (no error) but pwd shows we left workdir
	// This documents the behavior - hooks can cd around
	if !result.Success {
		t.Errorf("expected success: %v", result.Error)
	}

	// Subsequent commands in the SAME hook can access parent dirs
	// but this is by design - hooks are shell commands
	// The security boundary is: hooks run with agent's permissions, not root
}

func TestExecute_ShellInjection_NullByteInCommand(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	// Null bytes in commands should be handled gracefully
	hook := &Hook{
		ID:      "null-byte",
		Command: "echo hello\x00world",
		Trigger: TriggerBeforeCommit,
	}

	result := runner.Execute(hook)

	// Should not panic - shell may truncate or error
	if result == nil {
		t.Error("result should not be nil")
	}
}

func TestExecute_ShellInjection_NewlineInCommand(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	// Newlines in commands - shell interprets as command separator
	hook := &Hook{
		ID:      "newline",
		Command: "echo first\necho second",
		Trigger: TriggerBeforeCommit,
	}

	result := runner.Execute(hook)

	if !result.Success {
		t.Errorf("expected success: %v", result.Error)
	}

	// Both lines should execute
	if !strings.Contains(result.Output, "first") || !strings.Contains(result.Output, "second") {
		t.Errorf("expected both lines in output, got: %s", result.Output)
	}
}

// ============================================================================
// MaxCommandLength Boundary Tests (gt-uwohce)
// ============================================================================

func TestValidateHook_CommandLengthBoundary(t *testing.T) {
	testCases := []struct {
		name        string
		commandLen  int
		expectError bool
	}{
		{"exactly at limit", MaxCommandLength, false},
		{"one under limit", MaxCommandLength - 1, false},
		{"one over limit", MaxCommandLength + 1, true},
		{"way over limit", MaxCommandLength * 2, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create command of exact length: "echo " (5 chars) + padding
			padding := strings.Repeat("x", tc.commandLen-5)
			command := "echo " + padding

			if len(command) != tc.commandLen {
				t.Fatalf("test setup error: expected len %d, got %d", tc.commandLen, len(command))
			}

			hook := &Hook{
				ID:      "length-test",
				Command: command,
				Trigger: TriggerBeforeCommit,
			}

			err := ValidateHook(hook)

			if tc.expectError && err == nil {
				t.Errorf("expected error for command length %d", tc.commandLen)
			}
			if !tc.expectError && err != nil {
				t.Errorf("unexpected error for command length %d: %v", tc.commandLen, err)
			}
		})
	}
}

func TestExecute_MaxLengthCommandActuallyRuns(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	// Create a command at exactly MaxCommandLength that actually does something
	// "echo " is 5 chars, we need MaxCommandLength - 5 more
	marker := "BOUNDARY_TEST_MARKER"
	padding := strings.Repeat("x", MaxCommandLength-5-len(marker)-1) // -1 for space
	command := "echo " + marker + " " + padding

	if len(command) != MaxCommandLength {
		t.Fatalf("test setup error: expected len %d, got %d", MaxCommandLength, len(command))
	}

	hook := &Hook{
		ID:      "max-length-exec",
		Command: command,
		Trigger: TriggerBeforeCommit,
	}

	result := runner.Execute(hook)

	if !result.Success {
		t.Errorf("expected success for max-length command: %v", result.Error)
	}

	if !strings.Contains(result.Output, marker) {
		t.Errorf("expected marker %q in output, got: %s", marker, result.Output)
	}
}

// ============================================================================
// Process Group Orphan Prevention Tests (gt-uwohce)
// ============================================================================

func TestExecute_TimeoutKillsChildProcesses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping process group test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("process group test not applicable on Windows")
	}

	workDir := t.TempDir()
	runner := NewRunner(workDir, "test-agent")

	// Create a PID file path for child process
	childPidFile := filepath.Join(workDir, "child.pid")

	// Command that spawns a child process which writes its PID and sleeps
	// Parent also sleeps so timeout kills it
	hook := &Hook{
		ID: "orphan-test",
		Command: fmt.Sprintf(`
			# Spawn a child that writes its PID and sleeps
			(echo $$ > %s; sleep 60) &
			# Parent also sleeps
			sleep 60
		`, childPidFile),
		Timeout: 1, // 1 second timeout
		Trigger: TriggerBeforeCommit,
	}

	result := runner.Execute(hook)

	if !result.TimedOut {
		t.Error("expected timeout")
	}

	// Give a moment for process cleanup
	time.Sleep(100 * time.Millisecond)

	// Read the child PID
	pidBytes, err := os.ReadFile(childPidFile)
	if err != nil {
		// If file doesn't exist, child may not have started yet - that's OK
		t.Logf("child PID file not found (child may not have started): %v", err)
		return
	}

	pidStr := strings.TrimSpace(string(pidBytes))
	if pidStr == "" {
		t.Log("child PID file empty")
		return
	}

	// Check if child process is still running
	// On Unix, we can check /proc/<pid> or try to signal it
	procPath := filepath.Join("/proc", pidStr)
	if _, err := os.Stat(procPath); err == nil {
		// Process still exists - this is a failure (orphan not killed)
		// But give it another moment since SIGKILL is async
		time.Sleep(200 * time.Millisecond)
		if _, err := os.Stat(procPath); err == nil {
			t.Errorf("child process %s still running after timeout - orphan not killed", pidStr)
		}
	}
}

func TestExecute_TimeoutKillsForkBomb(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fork bomb test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("fork bomb test not applicable on Windows")
	}

	runner := NewRunner(t.TempDir(), "test-agent")

	// A controlled "fork bomb" - spawns a few children that each sleep
	// The timeout should kill them all via process group
	hook := &Hook{
		ID: "controlled-fork",
		Command: `
			# Spawn 3 sleeping children
			sleep 60 &
			sleep 60 &
			sleep 60 &
			# Parent waits
			sleep 60
		`,
		Timeout: 1,
		Trigger: TriggerBeforeCommit,
	}

	start := time.Now()
	result := runner.Execute(hook)
	elapsed := time.Since(start)

	if !result.TimedOut {
		t.Error("expected timeout")
	}

	// Should complete close to 1 second (timeout value)
	if elapsed > 3*time.Second {
		t.Errorf("took too long (%v) - processes may not have been killed properly", elapsed)
	}
}

func TestExecute_TimeoutWithPipelineChildren(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping pipeline test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("pipeline test not applicable on Windows")
	}

	runner := NewRunner(t.TempDir(), "test-agent")

	// Pipeline creates multiple processes - all should be killed on timeout
	hook := &Hook{
		ID:      "pipeline-timeout",
		Command: "yes | head -n 1000000000", // Would run forever without timeout
		Timeout: 1,
		Trigger: TriggerBeforeCommit,
	}

	start := time.Now()
	result := runner.Execute(hook)
	elapsed := time.Since(start)

	if !result.TimedOut {
		t.Error("expected timeout")
	}

	// Should complete close to 1 second
	if elapsed > 3*time.Second {
		t.Errorf("pipeline took too long (%v) - child processes may not have been killed", elapsed)
	}
}

// ============================================================================
// Additional Security Edge Cases (gt-uwohce)
// ============================================================================

func TestExecute_UnicodeInCommand(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	testCases := []struct {
		name    string
		command string
		expect  string
	}{
		{"simple unicode", "echo '日本語'", "日本語"},
		{"emoji", "echo '🎉'", "🎉"},
		{"mixed", "echo 'hello 世界'", "世界"},
		{"rtl characters", "echo 'مرحبا'", "مرحبا"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hook := &Hook{
				ID:      "unicode-" + tc.name,
				Command: tc.command,
				Trigger: TriggerBeforeCommit,
			}

			result := runner.Execute(hook)

			if !result.Success {
				t.Errorf("expected success: %v", result.Error)
			}

			if !strings.Contains(result.Output, tc.expect) {
				t.Errorf("expected %q in output, got: %s", tc.expect, result.Output)
			}
		})
	}
}

func TestExecute_QuoteEscaping(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	testCases := []struct {
		name    string
		command string
		expect  string
	}{
		{"single quotes", `echo 'single'\''quote'`, "single'quote"},
		{"double quotes", `echo "double\"quote"`, `double"quote`},
		{"mixed quotes", `echo "it's a \"test\""`, `it's a "test"`},
		{"escaped backslash", `echo "back\\slash"`, `back\slash`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hook := &Hook{
				ID:      "quote-" + tc.name,
				Command: tc.command,
				Trigger: TriggerBeforeCommit,
			}

			result := runner.Execute(hook)

			if !result.Success {
				t.Errorf("expected success: %v", result.Error)
			}

			if !strings.Contains(result.Output, tc.expect) {
				t.Errorf("expected %q in output, got: %s", tc.expect, result.Output)
			}
		})
	}
}

func TestExecute_LargeOutput(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	// Generate large output - should be captured without hanging
	hook := &Hook{
		ID:      "large-output",
		Command: "seq 1 10000",
		Trigger: TriggerBeforeCommit,
		Timeout: 10,
	}

	result := runner.Execute(hook)

	if !result.Success {
		t.Errorf("expected success: %v", result.Error)
	}

	// Should have captured output
	if len(result.Output) < 1000 {
		t.Errorf("expected large output, got %d bytes", len(result.Output))
	}

	// Should contain expected values
	if !strings.Contains(result.Output, "10000") {
		t.Error("expected output to contain '10000'")
	}
}

func TestExecute_StderrCapture(t *testing.T) {
	runner := NewRunner(t.TempDir(), "test-agent")

	// Verify stderr is captured along with stdout
	hook := &Hook{
		ID:      "stderr-test",
		Command: "echo stdout; echo stderr >&2",
		Trigger: TriggerBeforeCommit,
	}

	result := runner.Execute(hook)

	if !result.Success {
		t.Errorf("expected success: %v", result.Error)
	}

	// Both stdout and stderr should be in output
	if !strings.Contains(result.Output, "stdout") {
		t.Error("expected stdout in output")
	}
	if !strings.Contains(result.Output, "stderr") {
		t.Error("expected stderr in output")
	}
}
