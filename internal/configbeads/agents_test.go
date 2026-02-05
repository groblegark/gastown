package configbeads

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
)

func TestLoadAgentPresetsFromBeadsScope_NotFound(t *testing.T) {
	dir := setupTestTown(t)
	bd := setupTestBeads(t, dir)

	presets, roleAgents, err := LoadAgentPresetsFromBeadsScope(bd, "testtown", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if presets != nil || roleAgents != nil {
		t.Error("expected nil when no agent preset beads exist")
	}
}

func TestLoadAgentPresetsFromBeadsScope_Found(t *testing.T) {
	dir := setupTestTown(t)
	bd := setupTestBeads(t, dir)

	// Create an agent preset config bead
	metadata := map[string]interface{}{
		"name":    "claude",
		"command": "claude",
		"args":    []string{"--dangerously-skip-permissions"},
	}
	metaJSON, _ := json.Marshal(metadata)

	fields := &beads.ConfigFields{
		Rig:      "testtown",
		Category: beads.ConfigCategoryAgentPreset,
		Metadata: string(metaJSON),
	}
	_, err := bd.CreateConfigBead("agent-claude", fields, "", "")
	if err != nil {
		t.Skipf("bd create failed (known bd CLI issue): %v", err)
		return
	}

	presets, _, err := LoadAgentPresetsFromBeadsScope(bd, "testtown", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if presets == nil {
		t.Fatal("expected non-nil presets")
	}
	claude := presets["claude"]
	if claude == nil {
		t.Fatal("expected claude preset")
	}
	if claude.Command != "claude" {
		t.Errorf("command = %q, want claude", claude.Command)
	}
}

func TestLoadAgentRegistry_FallbackToFilesystem(t *testing.T) {
	dir := setupTestTown(t)
	_ = setupTestBeads(t, dir)

	// Reset registry for test isolation
	config.ResetRegistryForTesting()
	defer config.ResetRegistryForTesting()

	// Create a filesystem agents.json
	settingsDir := filepath.Join(dir, "settings")
	if err := os.MkdirAll(settingsDir, 0755); err != nil {
		t.Fatal(err)
	}
	registry := &config.AgentRegistry{
		Version: 1,
		Agents: map[string]*config.AgentPresetInfo{
			"fs-agent": {
				Name:    "fs-agent",
				Command: "fs-cli",
				Args:    []string{"--flag"},
			},
		},
	}
	if err := config.SaveAgentRegistry(filepath.Join(settingsDir, "agents.json"), registry); err != nil {
		t.Fatal(err)
	}

	// No beads exist, should fallback to filesystem
	err := LoadAgentRegistry(dir, "testtown", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the filesystem agent was loaded
	info := config.GetAgentPresetByName("fs-agent")
	if info == nil {
		t.Fatal("expected fs-agent from filesystem fallback")
	}
	if info.Command != "fs-cli" {
		t.Errorf("command = %q, want fs-cli", info.Command)
	}
}
