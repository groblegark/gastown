package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMergeMCPConfig_GlobalOnly(t *testing.T) {
	base := make(map[string]interface{})
	layer := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"playwright": map[string]interface{}{
				"command": "npx",
				"args":    []interface{}{"-y", "@playwright/mcp@latest"},
			},
		},
	}

	MergeMCPConfig(base, layer)

	servers, ok := base["mcpServers"].(map[string]interface{})
	if !ok {
		t.Fatal("expected mcpServers map")
	}
	if _, ok := servers["playwright"]; !ok {
		t.Error("expected playwright server")
	}
}

func TestMergeMCPConfig_AddServer(t *testing.T) {
	base := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"playwright": map[string]interface{}{
				"command": "npx",
				"args":    []interface{}{"-y", "@playwright/mcp@latest"},
			},
		},
	}
	layer := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"custom-server": map[string]interface{}{
				"command": "/usr/local/bin/custom",
				"args":    []interface{}{"--flag"},
			},
		},
	}

	MergeMCPConfig(base, layer)

	servers := base["mcpServers"].(map[string]interface{})
	if len(servers) != 2 {
		t.Errorf("expected 2 servers, got %d", len(servers))
	}
	if _, ok := servers["playwright"]; !ok {
		t.Error("expected playwright server to be preserved")
	}
	if _, ok := servers["custom-server"]; !ok {
		t.Error("expected custom-server to be added")
	}
}

func TestMergeMCPConfig_OverrideServer(t *testing.T) {
	base := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"playwright": map[string]interface{}{
				"command": "npx",
				"args":    []interface{}{"-y", "@playwright/mcp@latest"},
			},
		},
	}
	layer := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"playwright": map[string]interface{}{
				"command": "/custom/playwright",
				"args":    []interface{}{"--custom-flag"},
			},
		},
	}

	MergeMCPConfig(base, layer)

	servers := base["mcpServers"].(map[string]interface{})
	pw := servers["playwright"].(map[string]interface{})
	if pw["command"] != "/custom/playwright" {
		t.Errorf("expected overridden command, got %v", pw["command"])
	}
}

func TestMergeMCPConfig_RemoveServer(t *testing.T) {
	base := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"playwright": map[string]interface{}{
				"command": "npx",
			},
			"other": map[string]interface{}{
				"command": "other",
			},
		},
	}
	layer := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"playwright": nil, // Remove playwright
		},
	}

	MergeMCPConfig(base, layer)

	servers := base["mcpServers"].(map[string]interface{})
	if _, ok := servers["playwright"]; ok {
		t.Error("expected playwright to be removed")
	}
	if _, ok := servers["other"]; !ok {
		t.Error("expected other server to be preserved")
	}
}

func TestMergeMCPConfig_RemoveViaJSONNull(t *testing.T) {
	// Simulate what happens when JSON null is unmarshaled
	base := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"playwright": map[string]interface{}{
				"command": "npx",
			},
		},
	}

	// Unmarshal JSON with null value like config beads would produce
	layerJSON := `{"mcpServers":{"playwright":null}}`
	var layer map[string]interface{}
	if err := json.Unmarshal([]byte(layerJSON), &layer); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	MergeMCPConfig(base, layer)

	servers := base["mcpServers"].(map[string]interface{})
	if _, ok := servers["playwright"]; ok {
		t.Error("expected playwright to be removed via JSON null")
	}
}

func TestMergeMCPConfig_NonServerKeys(t *testing.T) {
	base := map[string]interface{}{
		"version": "1.0",
	}
	layer := map[string]interface{}{
		"version": "2.0",
		"newKey":  "value",
	}

	MergeMCPConfig(base, layer)

	if base["version"] != "2.0" {
		t.Errorf("expected version override, got %v", base["version"])
	}
	if base["newKey"] != "value" {
		t.Error("expected newKey to be added")
	}
}

func TestMergeMCPConfig_RemoveNonServerKey(t *testing.T) {
	base := map[string]interface{}{
		"version": "1.0",
		"extra":   "data",
	}
	layer := map[string]interface{}{
		"extra": nil,
	}

	MergeMCPConfig(base, layer)

	if _, ok := base["extra"]; ok {
		t.Error("expected extra key to be removed")
	}
	if base["version"] != "1.0" {
		t.Error("expected version to be preserved")
	}
}

func TestMergeMCPConfig_EmptyBase(t *testing.T) {
	base := make(map[string]interface{})
	layer := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"playwright": map[string]interface{}{
				"command": "npx",
			},
		},
	}

	MergeMCPConfig(base, layer)

	servers, ok := base["mcpServers"].(map[string]interface{})
	if !ok {
		t.Fatal("expected mcpServers to be created")
	}
	if _, ok := servers["playwright"]; !ok {
		t.Error("expected playwright server")
	}
}

func TestMergeMCPConfig_MultipleLayersMerge(t *testing.T) {
	// Simulate the full merge: global -> rig-specific
	base := make(map[string]interface{})

	// Layer 1: Global config
	global := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"playwright": map[string]interface{}{
				"command": "npx",
				"args":    []interface{}{"-y", "@playwright/mcp@latest"},
			},
		},
	}
	MergeMCPConfig(base, global)

	// Layer 2: Rig-specific adds a server and overrides playwright
	rigLayer := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"playwright": map[string]interface{}{
				"command": "/opt/playwright",
			},
			"custom-tool": map[string]interface{}{
				"command": "/opt/custom",
			},
		},
	}
	MergeMCPConfig(base, rigLayer)

	servers := base["mcpServers"].(map[string]interface{})
	if len(servers) != 2 {
		t.Errorf("expected 2 servers, got %d", len(servers))
	}

	pw := servers["playwright"].(map[string]interface{})
	if pw["command"] != "/opt/playwright" {
		t.Errorf("expected rig-override command, got %v", pw["command"])
	}

	custom := servers["custom-tool"].(map[string]interface{})
	if custom["command"] != "/opt/custom" {
		t.Errorf("expected custom-tool command, got %v", custom["command"])
	}
}

func TestScopeFromEnv(t *testing.T) {
	// Save and restore environment
	envVars := []string{"GT_TOWN_ROOT", "GT_RIG", "GT_ROLE", "GT_POLECAT", "GT_CREW"}
	saved := make(map[string]string)
	for _, v := range envVars {
		saved[v] = os.Getenv(v)
	}
	t.Cleanup(func() {
		for k, v := range saved {
			if v == "" {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, v)
			}
		}
	})

	tests := []struct {
		name   string
		env    map[string]string
		want   ConfigScope
	}{
		{
			name: "polecat scope",
			env: map[string]string{
				"GT_TOWN_ROOT": "/home/user/gt11",
				"GT_RIG":       "gastown",
				"GT_ROLE":      "polecat",
				"GT_POLECAT":   "slit",
			},
			want: ConfigScope{
				Town:  "gt11",
				Rig:   "gastown",
				Role:  "polecat",
				Agent: "slit",
			},
		},
		{
			name: "crew scope",
			env: map[string]string{
				"GT_TOWN_ROOT": "/home/user/gt11",
				"GT_RIG":       "gastown",
				"GT_ROLE":      "crew",
				"GT_CREW":      "slack",
			},
			want: ConfigScope{
				Town:  "gt11",
				Rig:   "gastown",
				Role:  "crew",
				Agent: "slack",
			},
		},
		{
			name: "empty environment",
			env:  map[string]string{},
			want: ConfigScope{},
		},
		{
			name: "role without agent",
			env: map[string]string{
				"GT_TOWN_ROOT": "/home/user/mytown",
				"GT_RIG":       "beads",
				"GT_ROLE":      "witness",
			},
			want: ConfigScope{
				Town: "mytown",
				Rig:  "beads",
				Role: "witness",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all env vars
			for _, v := range envVars {
				os.Unsetenv(v)
			}
			// Set test env vars
			for k, v := range tt.env {
				os.Setenv(k, v)
			}

			got := ScopeFromEnv()
			if got != tt.want {
				t.Errorf("ScopeFromEnv() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestMaterializeMCPConfig_FallbackToTemplate(t *testing.T) {
	// When no metadata layers are provided, should fall back to embedded template
	workDir := t.TempDir()

	err := MaterializeMCPConfig(workDir, nil)
	if err != nil {
		t.Fatalf("MaterializeMCPConfig() error: %v", err)
	}

	mcpPath := filepath.Join(workDir, ".mcp.json")
	data, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("reading .mcp.json: %v", err)
	}

	// Should contain the embedded template content
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parsing .mcp.json: %v", err)
	}

	servers, ok := config["mcpServers"].(map[string]interface{})
	if !ok {
		t.Fatal("expected mcpServers in fallback config")
	}
	if _, ok := servers["playwright"]; !ok {
		t.Error("expected playwright server in fallback config")
	}
}

func TestMaterializeMCPConfig_WithLayers(t *testing.T) {
	workDir := t.TempDir()

	layers := []string{
		// Global layer
		`{"mcpServers":{"playwright":{"command":"npx","args":["-y","@playwright/mcp@latest"]}}}`,
		// Rig-specific layer: adds custom server, overrides playwright
		`{"mcpServers":{"playwright":{"command":"/opt/pw"},"custom":{"command":"custom-cmd"}}}`,
	}

	err := MaterializeMCPConfig(workDir, layers)
	if err != nil {
		t.Fatalf("MaterializeMCPConfig() error: %v", err)
	}

	mcpPath := filepath.Join(workDir, ".mcp.json")
	data, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("reading .mcp.json: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parsing .mcp.json: %v", err)
	}

	servers, ok := config["mcpServers"].(map[string]interface{})
	if !ok {
		t.Fatal("expected mcpServers")
	}
	if len(servers) != 2 {
		t.Errorf("expected 2 servers, got %d", len(servers))
	}
	pw := servers["playwright"].(map[string]interface{})
	if pw["command"] != "/opt/pw" {
		t.Errorf("expected overridden playwright command, got %v", pw["command"])
	}
	if _, ok := servers["custom"]; !ok {
		t.Error("expected custom server to be added")
	}
}

func TestMaterializeMCPConfig_WithRemoval(t *testing.T) {
	workDir := t.TempDir()

	layers := []string{
		// Global layer with two servers
		`{"mcpServers":{"playwright":{"command":"npx"},"other":{"command":"other"}}}`,
		// Rig layer removes playwright
		`{"mcpServers":{"playwright":null}}`,
	}

	err := MaterializeMCPConfig(workDir, layers)
	if err != nil {
		t.Fatalf("MaterializeMCPConfig() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(workDir, ".mcp.json"))
	if err != nil {
		t.Fatalf("reading .mcp.json: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parsing .mcp.json: %v", err)
	}

	servers := config["mcpServers"].(map[string]interface{})
	if _, ok := servers["playwright"]; ok {
		t.Error("expected playwright to be removed")
	}
	if _, ok := servers["other"]; !ok {
		t.Error("expected other server to be preserved")
	}
}

func TestMaterializeMCPConfig_EmptyMetadata(t *testing.T) {
	workDir := t.TempDir()

	// Empty strings should be skipped, resulting in fallback
	layers := []string{"", ""}

	err := MaterializeMCPConfig(workDir, layers)
	if err != nil {
		t.Fatalf("MaterializeMCPConfig() error: %v", err)
	}

	// Should have written the embedded template as fallback
	data, err := os.ReadFile(filepath.Join(workDir, ".mcp.json"))
	if err != nil {
		t.Fatalf("reading .mcp.json: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parsing .mcp.json: %v", err)
	}

	if _, ok := config["mcpServers"]; !ok {
		t.Error("expected mcpServers from embedded template")
	}
}

func TestMaterializeMCPConfig_InvalidJSON(t *testing.T) {
	workDir := t.TempDir()

	// Invalid JSON should be skipped
	layers := []string{
		"not json at all",
		`{"mcpServers":{"valid":{"command":"test"}}}`,
	}

	err := MaterializeMCPConfig(workDir, layers)
	if err != nil {
		t.Fatalf("MaterializeMCPConfig() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(workDir, ".mcp.json"))
	if err != nil {
		t.Fatalf("reading .mcp.json: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parsing .mcp.json: %v", err)
	}

	servers := config["mcpServers"].(map[string]interface{})
	if _, ok := servers["valid"]; !ok {
		t.Error("expected valid server from second layer")
	}
}

func TestMergeServers_NonMapLayer(t *testing.T) {
	// mergeServers should handle non-map layerServers gracefully
	base := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"existing": map[string]interface{}{"command": "test"},
		},
	}

	// Pass a non-map value - should be a no-op
	mergeServers(base, "not a map")

	servers := base["mcpServers"].(map[string]interface{})
	if _, ok := servers["existing"]; !ok {
		t.Error("existing server should be preserved when layer is not a map")
	}
}
