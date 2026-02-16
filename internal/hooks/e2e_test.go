package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeSettings creates a .claude/settings.json in the given dir and returns
// the full path to the settings file. The caller provides the hooks map.
func writeSettings(t *testing.T, dir string, hooks map[string][]HookEntry) string {
	t.Helper()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}

	settings := map[string]interface{}{
		"hooks": hooks,
	}
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}

	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, data, 0644); err != nil {
		t.Fatal(err)
	}
	return settingsPath
}

func TestE2E_FireAllEventTypes(t *testing.T) {
	dir := t.TempDir()

	// Build hooks map with every event type echoing its own name.
	allHooks := make(map[string][]HookEntry)
	for _, event := range AllEvents() {
		allHooks[string(event)] = []HookEntry{
			{
				Matcher: "",
				Hooks: []HookAction{
					{Type: "command", Command: fmt.Sprintf("echo %s", string(event))},
				},
			},
		}
	}

	settingsPath := writeSettings(t, dir, allHooks)
	runner := NewRunner(settingsPath)

	for _, event := range AllEvents() {
		t.Run(string(event), func(t *testing.T) {
			results, err := runner.Fire(context.Background(), event)
			if err != nil {
				t.Fatalf("Fire(%s) error: %v", event, err)
			}
			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d", len(results))
			}
			r := results[0]
			if !r.Success {
				t.Errorf("expected success, got exit %d stderr=%q error=%q", r.ExitCode, r.Stderr, r.Error)
			}
			if r.Stdout != string(event) {
				t.Errorf("expected stdout %q, got %q", string(event), r.Stdout)
			}
		})
	}
}

func TestE2E_FireWithMatcher(t *testing.T) {
	dir := t.TempDir()

	hooks := map[string][]HookEntry{
		"PreToolUse": {
			{
				Matcher: "",
				Hooks: []HookAction{
					{Type: "command", Command: "echo universal"},
				},
			},
			{
				Matcher: "Bash(*)",
				Hooks: []HookAction{
					{Type: "command", Command: "echo matched-bash"},
				},
			},
		},
	}

	settingsPath := writeSettings(t, dir, hooks)
	runner := NewRunner(settingsPath)

	results, err := runner.Fire(context.Background(), EventPreToolUse)
	if err != nil {
		t.Fatalf("Fire error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Universal hook (empty matcher) fires first.
	if results[0].Stdout != "universal" {
		t.Errorf("expected first result stdout %q, got %q", "universal", results[0].Stdout)
	}
	if results[0].Matcher != "" {
		t.Errorf("expected empty matcher for universal hook, got %q", results[0].Matcher)
	}

	// Matcher-specific hook fires second.
	if results[1].Stdout != "matched-bash" {
		t.Errorf("expected second result stdout %q, got %q", "matched-bash", results[1].Stdout)
	}
	if results[1].Matcher != "Bash(*)" {
		t.Errorf("expected matcher %q, got %q", "Bash(*)", results[1].Matcher)
	}
}

func TestE2E_FireMultiStepCommand(t *testing.T) {
	dir := t.TempDir()

	hooks := map[string][]HookEntry{
		"SessionStart": {
			{
				Matcher: "",
				Hooks: []HookAction{
					{Type: "command", Command: "echo foo | tr a-z A-Z && echo done"},
				},
			},
		},
	}

	settingsPath := writeSettings(t, dir, hooks)
	runner := NewRunner(settingsPath)

	results, err := runner.Fire(context.Background(), EventSessionStart)
	if err != nil {
		t.Fatalf("Fire error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if !r.Success {
		t.Errorf("expected success, got exit %d stderr=%q error=%q", r.ExitCode, r.Stderr, r.Error)
	}
	if !strings.Contains(r.Stdout, "FOO") {
		t.Errorf("expected stdout to contain %q, got %q", "FOO", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "done") {
		t.Errorf("expected stdout to contain %q, got %q", "done", r.Stdout)
	}
}

func TestE2E_FireWithWorkingDirectory(t *testing.T) {
	dir := t.TempDir()

	hooks := map[string][]HookEntry{
		"SessionStart": {
			{
				Matcher: "",
				Hooks: []HookAction{
					{Type: "command", Command: "pwd"},
				},
			},
		},
	}

	settingsPath := writeSettings(t, dir, hooks)
	runner := NewRunner(settingsPath)

	results, err := runner.Fire(context.Background(), EventSessionStart)
	if err != nil {
		t.Fatalf("Fire error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if !r.Success {
		t.Errorf("expected success, got exit %d stderr=%q error=%q", r.ExitCode, r.Stderr, r.Error)
	}

	// The working directory should be the project root (parent of .claude/).
	// Resolve symlinks for macOS /tmp -> /private/tmp.
	expectedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	actualDir, err := filepath.EvalSymlinks(r.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	if actualDir != expectedDir {
		t.Errorf("expected working dir %q, got %q", expectedDir, actualDir)
	}
}

func TestE2E_FireWithEnvironmentInheritance(t *testing.T) {
	dir := t.TempDir()

	hooks := map[string][]HookEntry{
		"SessionStart": {
			{
				Matcher: "",
				Hooks: []HookAction{
					{Type: "command", Command: "echo $E2E_TEST_INHERIT_VAR"},
				},
			},
		},
	}

	settingsPath := writeSettings(t, dir, hooks)

	// Set an env var that the hook should be able to see via os.Environ() inheritance.
	t.Setenv("E2E_TEST_INHERIT_VAR", "inherited-value")

	runner := NewRunner(settingsPath)

	results, err := runner.Fire(context.Background(), EventSessionStart)
	if err != nil {
		t.Fatalf("Fire error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	if results[0].Stdout != "inherited-value" {
		t.Errorf("expected stdout %q, got %q", "inherited-value", results[0].Stdout)
	}
}

func TestE2E_FireSequentialExecution(t *testing.T) {
	dir := t.TempDir()
	sharedFile := filepath.Join(t.TempDir(), "sequential.txt")

	hooks := map[string][]HookEntry{
		"SessionStart": {
			{
				Matcher: "",
				Hooks: []HookAction{
					// First hook writes a value.
					{Type: "command", Command: fmt.Sprintf("echo sequential-data > %s", sharedFile)},
					// Second hook reads the value written by the first.
					{Type: "command", Command: fmt.Sprintf("cat %s", sharedFile)},
				},
			},
		},
	}

	settingsPath := writeSettings(t, dir, hooks)
	runner := NewRunner(settingsPath)

	results, err := runner.Fire(context.Background(), EventSessionStart)
	if err != nil {
		t.Fatalf("Fire error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// First hook should succeed (write).
	if !results[0].Success {
		t.Errorf("first hook failed: exit %d stderr=%q error=%q", results[0].ExitCode, results[0].Stderr, results[0].Error)
	}

	// Second hook should read what the first wrote.
	if !results[1].Success {
		t.Errorf("second hook failed: exit %d stderr=%q error=%q", results[1].ExitCode, results[1].Stderr, results[1].Error)
	}
	if results[1].Stdout != "sequential-data" {
		t.Errorf("expected second hook stdout %q, got %q", "sequential-data", results[1].Stdout)
	}
}

func TestE2E_FireGracefulFailure(t *testing.T) {
	dir := t.TempDir()

	hooks := map[string][]HookEntry{
		"Stop": {
			{
				Matcher: "",
				Hooks: []HookAction{
					{Type: "command", Command: "echo first-passes"},
					{Type: "command", Command: "exit 1"},
					{Type: "command", Command: "echo third-also-passes"},
				},
			},
		},
	}

	settingsPath := writeSettings(t, dir, hooks)
	runner := NewRunner(settingsPath, WithTimeout(5*time.Second))

	results, err := runner.Fire(context.Background(), EventStop)
	if err != nil {
		t.Fatalf("Fire should not return error even when hooks fail: %v", err)
	}

	// All three hooks should have executed.
	if len(results) != 3 {
		t.Fatalf("expected 3 results (all hooks execute despite failures), got %d", len(results))
	}

	// First: success
	if !results[0].Success {
		t.Errorf("first hook should succeed, got exit %d", results[0].ExitCode)
	}
	if results[0].Stdout != "first-passes" {
		t.Errorf("expected first stdout %q, got %q", "first-passes", results[0].Stdout)
	}

	// Second: failure
	if results[1].Success {
		t.Error("second hook should fail")
	}
	if results[1].ExitCode != 1 {
		t.Errorf("expected exit code 1, got %d", results[1].ExitCode)
	}

	// Third: success (verifies execution continued after second hook's failure)
	if !results[2].Success {
		t.Errorf("third hook should succeed despite earlier failure, got exit %d", results[2].ExitCode)
	}
	if results[2].Stdout != "third-also-passes" {
		t.Errorf("expected third stdout %q, got %q", "third-also-passes", results[2].Stdout)
	}
}
