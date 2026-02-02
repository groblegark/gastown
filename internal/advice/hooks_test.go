package advice

import (
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
