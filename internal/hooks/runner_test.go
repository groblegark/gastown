package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunner_Fire_SimpleCommand(t *testing.T) {
	// Create a temporary settings file with a simple hook
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}

	settings := map[string]interface{}{
		"hooks": map[string][]HookEntry{
			"SessionStart": {
				{
					Matcher: "",
					Hooks: []HookAction{
						{Type: "command", Command: "echo hello"},
					},
				},
			},
		},
	}

	data, _ := json.Marshal(settings)
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(settingsPath)
	results, err := runner.Fire(context.Background(), EventSessionStart)
	if err != nil {
		t.Fatalf("Fire failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if !r.Success {
		t.Errorf("expected success, got exit %d stderr=%q error=%q", r.ExitCode, r.Stderr, r.Error)
	}
	if r.Stdout != "hello" {
		t.Errorf("expected stdout 'hello', got %q", r.Stdout)
	}
}

func TestRunner_Fire_FailingCommand(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0755)

	settings := map[string]interface{}{
		"hooks": map[string][]HookEntry{
			"Stop": {
				{
					Matcher: "",
					Hooks: []HookAction{
						{Type: "command", Command: "exit 42"},
					},
				},
			},
		},
	}

	data, _ := json.Marshal(settings)
	settingsPath := filepath.Join(claudeDir, "settings.json")
	os.WriteFile(settingsPath, data, 0644)

	runner := NewRunner(settingsPath)
	results, err := runner.Fire(context.Background(), EventStop)
	if err != nil {
		t.Fatalf("Fire failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.Success {
		t.Error("expected failure")
	}
	if r.ExitCode != 42 {
		t.Errorf("expected exit code 42, got %d", r.ExitCode)
	}
}

func TestRunner_Fire_Timeout(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0755)

	settings := map[string]interface{}{
		"hooks": map[string][]HookEntry{
			"SessionStart": {
				{
					Matcher: "",
					Hooks: []HookAction{
						{Type: "command", Command: "sleep 60"},
					},
				},
			},
		},
	}

	data, _ := json.Marshal(settings)
	settingsPath := filepath.Join(claudeDir, "settings.json")
	os.WriteFile(settingsPath, data, 0644)

	runner := NewRunner(settingsPath, WithTimeout(100*time.Millisecond))
	results, err := runner.Fire(context.Background(), EventSessionStart)
	if err != nil {
		t.Fatalf("Fire failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.Success {
		t.Error("expected failure due to timeout")
	}
}

func TestRunner_Fire_NoHooksForEvent(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0755)

	settings := map[string]interface{}{
		"hooks": map[string][]HookEntry{
			"SessionStart": {
				{
					Matcher: "",
					Hooks: []HookAction{
						{Type: "command", Command: "echo start"},
					},
				},
			},
		},
	}

	data, _ := json.Marshal(settings)
	settingsPath := filepath.Join(claudeDir, "settings.json")
	os.WriteFile(settingsPath, data, 0644)

	runner := NewRunner(settingsPath)
	results, err := runner.Fire(context.Background(), EventStop)
	if err != nil {
		t.Fatalf("Fire failed: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestRunner_Fire_MultipleHooks(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0755)

	settings := map[string]interface{}{
		"hooks": map[string][]HookEntry{
			"PostToolUse": {
				{
					Matcher: "",
					Hooks: []HookAction{
						{Type: "command", Command: "echo first"},
						{Type: "command", Command: "echo second"},
					},
				},
				{
					Matcher: "Bash(*)",
					Hooks: []HookAction{
						{Type: "command", Command: "echo matcher-hook"},
					},
				},
			},
		},
	}

	data, _ := json.Marshal(settings)
	settingsPath := filepath.Join(claudeDir, "settings.json")
	os.WriteFile(settingsPath, data, 0644)

	runner := NewRunner(settingsPath)
	results, err := runner.Fire(context.Background(), EventPostToolUse)
	if err != nil {
		t.Fatalf("Fire failed: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	for i, r := range results {
		if !r.Success {
			t.Errorf("result %d: expected success, got exit %d", i, r.ExitCode)
		}
	}

	if results[2].Matcher != "Bash(*)" {
		t.Errorf("expected matcher 'Bash(*)', got %q", results[2].Matcher)
	}
}

func TestRunner_Fire_WithEnv(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0755)

	settings := map[string]interface{}{
		"hooks": map[string][]HookEntry{
			"SessionStart": {
				{
					Matcher: "",
					Hooks: []HookAction{
						{Type: "command", Command: "echo $TEST_HOOK_VAR"},
					},
				},
			},
		},
	}

	data, _ := json.Marshal(settings)
	settingsPath := filepath.Join(claudeDir, "settings.json")
	os.WriteFile(settingsPath, data, 0644)

	runner := NewRunner(settingsPath, WithEnv([]string{"TEST_HOOK_VAR=injected"}))
	results, err := runner.Fire(context.Background(), EventSessionStart)
	if err != nil {
		t.Fatalf("Fire failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	if results[0].Stdout != "injected" {
		t.Errorf("expected stdout 'injected', got %q", results[0].Stdout)
	}
}

func TestRunner_ListEvents(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0755)

	settings := map[string]interface{}{
		"hooks": map[string][]HookEntry{
			"SessionStart": {{Matcher: "", Hooks: []HookAction{{Type: "command", Command: "echo start"}}}},
			"Stop":         {{Matcher: "", Hooks: []HookAction{{Type: "command", Command: "echo stop"}}}},
		},
	}

	data, _ := json.Marshal(settings)
	settingsPath := filepath.Join(claudeDir, "settings.json")
	os.WriteFile(settingsPath, data, 0644)

	runner := NewRunner(settingsPath)
	events, err := runner.ListEvents()
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}

	if len(events) != 2 {
		t.Errorf("expected 2 events, got %d: %v", len(events), events)
	}
}

func TestRunner_Fire_SkipsNonCommandHooks(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0755)

	settings := map[string]interface{}{
		"hooks": map[string][]HookEntry{
			"SessionStart": {
				{
					Matcher: "",
					Hooks: []HookAction{
						{Type: "builtin", Command: ""},
						{Type: "command", Command: "echo real"},
					},
				},
			},
		},
	}

	data, _ := json.Marshal(settings)
	settingsPath := filepath.Join(claudeDir, "settings.json")
	os.WriteFile(settingsPath, data, 0644)

	runner := NewRunner(settingsPath)
	results, err := runner.Fire(context.Background(), EventSessionStart)
	if err != nil {
		t.Fatalf("Fire failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result (skipping builtin), got %d", len(results))
	}

	if results[0].Stdout != "real" {
		t.Errorf("expected stdout 'real', got %q", results[0].Stdout)
	}
}

func TestEvent_IsValid(t *testing.T) {
	tests := []struct {
		event Event
		valid bool
	}{
		{EventSessionStart, true},
		{EventPreCompact, true},
		{EventUserPromptSubmit, true},
		{EventPreToolUse, true},
		{EventPostToolUse, true},
		{EventStop, true},
		{Event("Invalid"), false},
		{Event(""), false},
	}

	for _, tt := range tests {
		if got := tt.event.IsValid(); got != tt.valid {
			t.Errorf("Event(%q).IsValid() = %v, want %v", tt.event, got, tt.valid)
		}
	}
}

func TestAllEvents(t *testing.T) {
	events := AllEvents()
	if len(events) != 6 {
		t.Errorf("expected 6 events, got %d", len(events))
	}

	// Verify order
	expected := []Event{
		EventSessionStart, EventPreCompact, EventUserPromptSubmit,
		EventPreToolUse, EventPostToolUse, EventStop,
	}
	for i, e := range expected {
		if events[i] != e {
			t.Errorf("events[%d] = %q, want %q", i, events[i], e)
		}
	}
}
