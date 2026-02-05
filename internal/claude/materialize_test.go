package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMergeHooksConfig_Empty(t *testing.T) {
	result, err := MergeHooksConfig(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected empty map, got %v", result)
	}
}

func TestMergeHooksConfig_SinglePayload(t *testing.T) {
	payloads := []string{
		`{"editorMode":"normal","hooks":{"SessionStart":[{"matcher":"","hooks":[{"type":"command","command":"gt prime"}]}]}}`,
	}

	result, err := MergeHooksConfig(payloads)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result["editorMode"] != "normal" {
		t.Errorf("expected editorMode=normal, got %v", result["editorMode"])
	}

	hooks, ok := result["hooks"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected hooks to be map, got %T", result["hooks"])
	}

	sessionStart, ok := hooks["SessionStart"].([]interface{})
	if !ok {
		t.Fatalf("expected SessionStart to be array, got %T", hooks["SessionStart"])
	}
	if len(sessionStart) != 1 {
		t.Errorf("expected 1 SessionStart hook, got %d", len(sessionStart))
	}
}

func TestMergeHooksConfig_TopLevelOverride(t *testing.T) {
	payloads := []string{
		`{"editorMode":"normal"}`,
		`{"editorMode":"vim"}`,
	}

	result, err := MergeHooksConfig(payloads)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result["editorMode"] != "vim" {
		t.Errorf("expected editorMode=vim (more specific wins), got %v", result["editorMode"])
	}
}

func TestMergeHooksConfig_HookArrayAppend(t *testing.T) {
	payloads := []string{
		`{"hooks":{"PreCompact":[{"matcher":"","hooks":[{"type":"command","command":"A"}]}]}}`,
		`{"hooks":{"PreCompact":[{"matcher":"","hooks":[{"type":"command","command":"B"}]}]}}`,
	}

	result, err := MergeHooksConfig(payloads)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hooks := result["hooks"].(map[string]interface{})
	preCompact := hooks["PreCompact"].([]interface{})
	if len(preCompact) != 2 {
		t.Errorf("expected 2 PreCompact hooks (append), got %d", len(preCompact))
	}
}

func TestMergeHooksConfig_NullSuppressesTopLevel(t *testing.T) {
	payloads := []string{
		`{"editorMode":"normal","enabledPlugins":{"foo":true}}`,
		`{"enabledPlugins":null}`,
	}

	result, err := MergeHooksConfig(payloads)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result["editorMode"] != "normal" {
		t.Errorf("expected editorMode=normal, got %v", result["editorMode"])
	}
	if _, exists := result["enabledPlugins"]; exists {
		t.Error("expected enabledPlugins to be suppressed by null")
	}
}

func TestMergeHooksConfig_NullSuppressesHookType(t *testing.T) {
	payloads := []string{
		`{"hooks":{"PostToolUse":[{"matcher":"","hooks":[{"type":"command","command":"A"}]}],"SessionStart":[{"matcher":"","hooks":[{"type":"command","command":"B"}]}]}}`,
		`{"hooks":{"PostToolUse":null}}`,
	}

	result, err := MergeHooksConfig(payloads)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hooks := result["hooks"].(map[string]interface{})

	if _, exists := hooks["PostToolUse"]; exists {
		t.Error("expected PostToolUse to be suppressed by null")
	}

	// SessionStart should survive
	if _, exists := hooks["SessionStart"]; !exists {
		t.Error("expected SessionStart to survive null suppression of PostToolUse")
	}
}

func TestMergeHooksConfig_MultipleHookTypes(t *testing.T) {
	payloads := []string{
		`{"hooks":{"SessionStart":[{"matcher":"","hooks":[{"type":"command","command":"A"}]}]}}`,
		`{"hooks":{"PreCompact":[{"matcher":"","hooks":[{"type":"command","command":"B"}]}]}}`,
	}

	result, err := MergeHooksConfig(payloads)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hooks := result["hooks"].(map[string]interface{})
	if _, exists := hooks["SessionStart"]; !exists {
		t.Error("expected SessionStart from first payload")
	}
	if _, exists := hooks["PreCompact"]; !exists {
		t.Error("expected PreCompact from second payload")
	}
}

func TestMergeHooksConfig_InvalidJSON(t *testing.T) {
	payloads := []string{`not valid json`}

	_, err := MergeHooksConfig(payloads)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestMergeHooksConfig_EmptyPayloadsSkipped(t *testing.T) {
	payloads := []string{
		"",
		`{"editorMode":"normal"}`,
		"",
	}

	result, err := MergeHooksConfig(payloads)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result["editorMode"] != "normal" {
		t.Errorf("expected editorMode=normal, got %v", result["editorMode"])
	}
}

func TestMergeHooksConfig_FullScenario(t *testing.T) {
	// Simulates: global base → rig override → role append
	payloads := []string{
		// Global base
		`{"editorMode":"normal","hooks":{"SessionStart":[{"matcher":"","hooks":[{"type":"command","command":"gt prime"}]}],"PostToolUse":[{"matcher":"","hooks":[{"type":"command","command":"gt drain"}]}]}}`,
		// Rig override: change editorMode, add PreCompact
		`{"editorMode":"vim","hooks":{"PreCompact":[{"matcher":"","hooks":[{"type":"command","command":"gt prime --hook"}]}]}}`,
		// Role: suppress PostToolUse, append to SessionStart
		`{"hooks":{"PostToolUse":null,"SessionStart":[{"matcher":"","hooks":[{"type":"command","command":"gt mail check"}]}]}}`,
	}

	result, err := MergeHooksConfig(payloads)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// editorMode should be "vim" (rig override)
	if result["editorMode"] != "vim" {
		t.Errorf("expected editorMode=vim, got %v", result["editorMode"])
	}

	hooks := result["hooks"].(map[string]interface{})

	// SessionStart should have 2 entries (global + role append)
	sessionStart := hooks["SessionStart"].([]interface{})
	if len(sessionStart) != 2 {
		t.Errorf("expected 2 SessionStart hooks, got %d", len(sessionStart))
	}

	// PostToolUse should be suppressed (role null)
	if _, exists := hooks["PostToolUse"]; exists {
		t.Error("expected PostToolUse to be suppressed")
	}

	// PreCompact should exist (rig addition)
	if _, exists := hooks["PreCompact"]; !exists {
		t.Error("expected PreCompact from rig layer")
	}
}

func TestMaterializeSettings_FallbackWhenEmpty(t *testing.T) {
	dir := t.TempDir()

	err := MaterializeSettings(dir, "polecat", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have created settings from embedded template
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		t.Fatal("expected settings.json to be created via fallback")
	}
}

func TestMaterializeSettings_WritesFromPayloads(t *testing.T) {
	dir := t.TempDir()

	payloads := []string{
		`{"editorMode":"normal","hooks":{"SessionStart":[{"matcher":"","hooks":[{"type":"command","command":"test"}]}]}}`,
	}

	err := MaterializeSettings(dir, "polecat", payloads)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("reading settings.json: %v", err)
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parsing settings.json: %v", err)
	}

	if settings["editorMode"] != "normal" {
		t.Errorf("expected editorMode=normal, got %v", settings["editorMode"])
	}
}

func TestMaterializeSettings_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()

	// Write initial settings
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0755)
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(`{"old":"value"}`), 0600)

	// Materialize should overwrite when payloads provided
	payloads := []string{`{"editorMode":"new"}`}
	err := MaterializeSettings(dir, "polecat", payloads)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if err != nil {
		t.Fatalf("reading settings.json: %v", err)
	}

	var settings map[string]interface{}
	json.Unmarshal(data, &settings)

	if settings["editorMode"] != "new" {
		t.Errorf("expected editorMode=new, got %v", settings["editorMode"])
	}
	if _, exists := settings["old"]; exists {
		t.Error("expected old settings to be replaced")
	}
}

func TestWriteSettings(t *testing.T) {
	dir := t.TempDir()

	settings := map[string]interface{}{
		"editorMode": "normal",
		"hooks": map[string]interface{}{
			"SessionStart": []interface{}{
				map[string]interface{}{
					"matcher": "",
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": "gt prime",
						},
					},
				},
			},
		},
	}

	err := WriteSettings(dir, settings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("reading settings.json: %v", err)
	}

	var written map[string]interface{}
	if err := json.Unmarshal(data, &written); err != nil {
		t.Fatalf("parsing written settings: %v", err)
	}

	if written["editorMode"] != "normal" {
		t.Errorf("expected editorMode=normal, got %v", written["editorMode"])
	}
}
