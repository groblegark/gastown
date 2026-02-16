package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestE2E_ValidSettings(t *testing.T) {
	dir := t.TempDir()

	// Build a well-formed settings.json with all 6 event types using safe commands.
	commands := map[Event]string{
		EventSessionStart:     "echo session-start",
		EventPreCompact:       "echo pre-compact",
		EventUserPromptSubmit: "date +%s",
		EventPreToolUse:       "true",
		EventPostToolUse:      "echo post-tool-use",
		EventStop:             "echo stop",
	}

	allHooks := make(map[string][]HookEntry)
	for event, cmd := range commands {
		allHooks[string(event)] = []HookEntry{
			{
				Matcher: "",
				Hooks: []HookAction{
					{Type: "command", Command: cmd},
				},
			},
		}
	}

	settingsPath := writeSettings(t, dir, allHooks)
	runner := NewRunner(settingsPath)

	// Verify the runner can list all configured events.
	events, err := runner.ListEvents()
	if err != nil {
		t.Fatalf("ListEvents error: %v", err)
	}
	if len(events) != 6 {
		t.Errorf("expected 6 configured events, got %d: %v", len(events), events)
	}

	// Fire each event and verify it succeeds.
	for event, cmd := range commands {
		t.Run(string(event), func(t *testing.T) {
			results, err := runner.Fire(context.Background(), event)
			if err != nil {
				t.Fatalf("Fire(%s) error: %v", event, err)
			}
			if len(results) != 1 {
				t.Fatalf("expected 1 result for %s, got %d", event, len(results))
			}
			if !results[0].Success {
				t.Errorf("command %q failed: exit %d stderr=%q error=%q",
					cmd, results[0].ExitCode, results[0].Stderr, results[0].Error)
			}
		})
	}
}

func TestE2E_EmptyHooksSection(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}

	// settings.json with "hooks": {}
	settings := map[string]interface{}{
		"hooks": map[string]interface{}{},
	}
	data, _ := json.Marshal(settings)
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(settingsPath)

	// Fire on every event type; all should return empty results, no error.
	for _, event := range AllEvents() {
		results, err := runner.Fire(context.Background(), event)
		if err != nil {
			t.Errorf("Fire(%s) returned error: %v", event, err)
		}
		if len(results) != 0 {
			t.Errorf("Fire(%s) expected 0 results, got %d", event, len(results))
		}
	}

	// ListEvents should also be empty.
	events, err := runner.ListEvents()
	if err != nil {
		t.Fatalf("ListEvents error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

func TestE2E_NoHooksKey(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}

	// settings.json without a "hooks" key at all.
	settings := map[string]interface{}{
		"someOtherSetting": true,
	}
	data, _ := json.Marshal(settings)
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(settingsPath)

	// Fire should return empty results, no error.
	results, err := runner.Fire(context.Background(), EventSessionStart)
	if err != nil {
		t.Fatalf("Fire returned error for missing hooks key: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}

	// ListEvents should also return empty.
	events, err := runner.ListEvents()
	if err != nil {
		t.Fatalf("ListEvents error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

func TestE2E_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}

	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{not valid json!!!`), 0644); err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(settingsPath)

	// Fire should return an error for malformed JSON.
	_, err := runner.Fire(context.Background(), EventSessionStart)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), "parsing settings") {
		t.Errorf("expected parsing error, got: %v", err)
	}

	// ListEvents should also return an error.
	_, err = runner.ListEvents()
	if err == nil {
		t.Fatal("expected error from ListEvents for malformed JSON, got nil")
	}
}

func TestE2E_NonCommandType(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Hook with type "notification" should be skipped by the runner.
	settings := map[string]interface{}{
		"hooks": map[string][]HookEntry{
			"SessionStart": {
				{
					Matcher: "",
					Hooks: []HookAction{
						{Type: "notification", Command: "echo should-be-skipped"},
						{Type: "command", Command: "echo kept"},
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
		t.Fatalf("Fire error: %v", err)
	}

	// Only the "command" type hook should produce a result.
	if len(results) != 1 {
		t.Fatalf("expected 1 result (notification skipped), got %d", len(results))
	}
	if results[0].Stdout != "kept" {
		t.Errorf("expected stdout %q, got %q", "kept", results[0].Stdout)
	}
}

func TestE2E_EmptyCommand(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Hook with type "command" but an empty command string should be skipped.
	settings := map[string]interface{}{
		"hooks": map[string][]HookEntry{
			"PreCompact": {
				{
					Matcher: "",
					Hooks: []HookAction{
						{Type: "command", Command: ""},
						{Type: "command", Command: "echo present"},
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
	results, err := runner.Fire(context.Background(), EventPreCompact)
	if err != nil {
		t.Fatalf("Fire error: %v", err)
	}

	// Only the non-empty command should produce a result.
	if len(results) != 1 {
		t.Fatalf("expected 1 result (empty command skipped), got %d", len(results))
	}
	if results[0].Stdout != "present" {
		t.Errorf("expected stdout %q, got %q", "present", results[0].Stdout)
	}
}

func TestE2E_AllEventsRoundTrip(t *testing.T) {
	dir := t.TempDir()

	// Create settings with a hook for every event returned by AllEvents().
	allHooks := make(map[string][]HookEntry)
	for _, event := range AllEvents() {
		allHooks[string(event)] = []HookEntry{
			{
				Matcher: "",
				Hooks: []HookAction{
					{Type: "command", Command: fmt.Sprintf("echo roundtrip-%s", string(event))},
				},
			},
		}
	}

	settingsPath := writeSettings(t, dir, allHooks)
	runner := NewRunner(settingsPath)

	// Verify ListEvents returns exactly the events we configured.
	listedEvents, err := runner.ListEvents()
	if err != nil {
		t.Fatalf("ListEvents error: %v", err)
	}
	if len(listedEvents) != len(AllEvents()) {
		t.Fatalf("expected %d events from ListEvents, got %d", len(AllEvents()), len(listedEvents))
	}

	// Fire each event and verify the full round-trip.
	for _, event := range AllEvents() {
		t.Run(string(event), func(t *testing.T) {
			results, err := runner.Fire(context.Background(), event)
			if err != nil {
				t.Fatalf("Fire(%s) error: %v", event, err)
			}
			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d", len(results))
			}

			expected := fmt.Sprintf("roundtrip-%s", string(event))
			if results[0].Stdout != expected {
				t.Errorf("expected stdout %q, got %q", expected, results[0].Stdout)
			}
			if !results[0].Success {
				t.Errorf("expected success for %s", event)
			}
			if results[0].ExitCode != 0 {
				t.Errorf("expected exit code 0, got %d", results[0].ExitCode)
			}
		})
	}
}

func TestE2E_LargeOutput(t *testing.T) {
	dir := t.TempDir()

	// Generate > 2000 chars of output.  "printf" repeating 'A' 3000 times.
	hooks := map[string][]HookEntry{
		"SessionStart": {
			{
				Matcher: "",
				Hooks: []HookAction{
					{Type: "command", Command: "printf 'A%.0s' $(seq 1 3000)"},
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

	// truncateOutput caps at 2000 chars (with trailing "...").
	if len(r.Stdout) > 2000 {
		t.Errorf("expected stdout to be truncated to at most 2000 chars, got %d", len(r.Stdout))
	}

	// Should end with "..." because the raw output was 3000 chars.
	if !strings.HasSuffix(r.Stdout, "...") {
		t.Errorf("expected truncated output to end with %q, got suffix %q",
			"...", r.Stdout[len(r.Stdout)-10:])
	}

	// Verify the truncated content is the expected character.
	trimmed := strings.TrimSuffix(r.Stdout, "...")
	for _, ch := range trimmed {
		if ch != 'A' {
			t.Errorf("expected all 'A' characters before truncation marker, found %q", string(ch))
			break
		}
	}
}
