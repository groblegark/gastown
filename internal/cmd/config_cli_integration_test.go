//go:build integration

// Package cmd provides CLI integration tests for the gt config subcommands.
// These tests build the gt binary and exercise the user-facing CLI contract:
// flag parsing, output formatting, error messages, and JSON output.
//
// Run with: go test -tags=integration ./internal/cmd/ -run TestCLIConfig -v
package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- Test helpers ---

// cleanConfigTestEnv returns os.Environ() with GT_* and BD_* variables removed.
// BD_DAEMON_HOST must be cleared so that bd create routes to the local SQLite
// database instead of the remote daemon, which doesn't know about the test town.
func cleanConfigTestEnv() []string {
	var clean []string
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "GT_") && !strings.HasPrefix(env, "BD_") {
			clean = append(clean, env)
		}
	}
	return clean
}

// setupCLIConfigTown creates a minimal Gas Town with beads initialized,
// config beads seeded, and returns (townRoot, gtBinary).
// This is the common setup for CLI config integration tests.
func setupCLIConfigTown(t *testing.T) (string, string) {
	t.Helper()

	gtBinary := buildGT(t)
	tmpDir := t.TempDir()
	hqPath := filepath.Join(tmpDir, "test-hq")

	baseEnv := append(cleanConfigTestEnv(), "HOME="+tmpDir)

	// Install a fresh town with beads initialized
	cmd := exec.Command(gtBinary, "install", hqPath, "--name", "test-town")
	cmd.Env = baseEnv
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gt install failed: %v\nOutput: %s", err, output)
	}

	// Seed config beads individually (full seed fails without accounts.json etc.)
	for _, seedFlag := range []string{"--hooks", "--mcp", "--identity"} {
		cmd = exec.Command(gtBinary, "config", "seed", seedFlag)
		cmd.Dir = hqPath
		cmd.Env = baseEnv
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("gt config seed %s failed: %v\nOutput: %s", seedFlag, err, output)
		}
	}

	return hqPath, gtBinary
}

// runGT runs the gt binary with args in the given town root and returns
// combined stdout+stderr and any error.
func runGT(t *testing.T, gtBinary, townRoot string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(gtBinary, args...)
	cmd.Dir = townRoot
	cmd.Env = append(cleanConfigTestEnv(), "HOME="+filepath.Dir(townRoot))
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// --- gt config resolve tests ---

func TestCLIConfigResolveHooksJSON(t *testing.T) {
	hqPath, gtBinary := setupCLIConfigTown(t)

	output, err := runGT(t, gtBinary, hqPath,
		"config", "resolve", "claude-hooks",
		"--town=test-town", "--role=crew", "--json")
	if err != nil {
		t.Fatalf("gt config resolve failed: %v\nOutput: %s", err, output)
	}

	// Output should be valid JSON
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\nOutput: %s", err, output)
	}

	// Should contain hooks structure
	hooks, ok := result["hooks"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'hooks' key in resolve output, got keys: %v", keysOf(result))
	}
	if len(hooks) == 0 {
		t.Error("expected non-empty hooks map")
	}
}

func TestCLIConfigResolveHooksText(t *testing.T) {
	hqPath, gtBinary := setupCLIConfigTown(t)

	output, err := runGT(t, gtBinary, hqPath,
		"config", "resolve", "claude-hooks",
		"--town=test-town", "--role=crew")
	if err != nil {
		t.Fatalf("gt config resolve failed: %v\nOutput: %s", err, output)
	}

	// Text output should contain key sections
	if !strings.Contains(output, "Resolved Config") {
		t.Error("expected 'Resolved Config' header in text output")
	}
	if !strings.Contains(output, "Effective Config") {
		t.Error("expected 'Effective Config' section in text output")
	}
	if !strings.Contains(output, "Layers:") {
		t.Error("expected 'Layers:' in text output")
	}
}

func TestCLIConfigResolveMCPJSON(t *testing.T) {
	hqPath, gtBinary := setupCLIConfigTown(t)

	output, err := runGT(t, gtBinary, hqPath,
		"config", "resolve", "mcp",
		"--town=test-town", "--json")
	if err != nil {
		t.Fatalf("gt config resolve mcp failed: %v\nOutput: %s", err, output)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\nOutput: %s", err, output)
	}

	// MCP resolve should contain mcpServers
	if _, ok := result["mcpServers"]; !ok {
		t.Errorf("expected 'mcpServers' key in MCP resolve output, got keys: %v", keysOf(result))
	}
}

func TestCLIConfigResolveNoBeads(t *testing.T) {
	// Create a town WITHOUT seeding config beads
	gtBinary := buildGT(t)
	tmpDir := t.TempDir()
	hqPath := filepath.Join(tmpDir, "empty-hq")

	cmd := exec.Command(gtBinary, "install", hqPath, "--name", "empty-town")
	cmd.Env = append(cleanConfigTestEnv(), "HOME="+tmpDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gt install failed: %v\nOutput: %s", err, output)
	}

	// Resolve should not error but report no beads found
	output, err := runGT(t, gtBinary, hqPath,
		"config", "resolve", "claude-hooks",
		"--town=empty-town", "--role=crew")
	if err != nil {
		t.Fatalf("gt config resolve should not fail with no beads: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(output, "No config beads found") {
		t.Errorf("expected 'No config beads found' message, got: %s", output)
	}
}

func TestCLIConfigResolveInvalidCategory(t *testing.T) {
	hqPath, gtBinary := setupCLIConfigTown(t)

	output, err := runGT(t, gtBinary, hqPath,
		"config", "resolve", "nonexistent-category")
	if err == nil {
		t.Fatal("expected error for invalid category")
	}

	if !strings.Contains(output, "invalid category") {
		t.Errorf("expected 'invalid category' in error output, got: %s", output)
	}
}

// --- gt config verify tests ---

func TestCLIConfigVerifyAllValid(t *testing.T) {
	hqPath, gtBinary := setupCLIConfigTown(t)

	output, err := runGT(t, gtBinary, hqPath, "config", "verify")
	if err != nil {
		t.Fatalf("gt config verify failed: %v\nOutput: %s", err, output)
	}

	// After seed, all beads should be valid
	if !strings.Contains(output, "config beads valid") {
		t.Errorf("expected 'config beads valid' in output, got: %s", output)
	}
}

func TestCLIConfigVerifyMissingBeads(t *testing.T) {
	// Create a town WITHOUT seeding config beads
	gtBinary := buildGT(t)
	tmpDir := t.TempDir()
	hqPath := filepath.Join(tmpDir, "unseeded-hq")

	cmd := exec.Command(gtBinary, "install", hqPath, "--name", "unseeded-town")
	cmd.Env = append(cleanConfigTestEnv(), "HOME="+tmpDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gt install failed: %v\nOutput: %s", err, output)
	}

	output, err := runGT(t, gtBinary, hqPath, "config", "verify")
	// verify may or may not return non-zero on problems, check output
	_ = err

	// Should report missing beads for expected categories
	if !strings.Contains(output, "no beads found") {
		t.Errorf("expected 'no beads found' warning for unseeded town, got: %s", output)
	}
}

// --- gt config materialize tests ---

func TestCLIConfigMaterializeHooks(t *testing.T) {
	hqPath, gtBinary := setupCLIConfigTown(t)

	// Materialize hooks into a subdirectory
	workDir := filepath.Join(hqPath, "mat-test-hooks")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(gtBinary, "config", "materialize",
		"--hooks", "--scope=test-town///crew")
	cmd.Dir = workDir
	cmd.Env = append(cleanConfigTestEnv(), "HOME="+filepath.Dir(hqPath))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gt config materialize --hooks failed: %v\nOutput: %s", err, output)
	}

	// Verify settings.json was created
	settingsPath := filepath.Join(workDir, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings.json not created: %v", err)
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v", err)
	}

	if _, ok := settings["hooks"]; !ok {
		t.Error("materialized settings.json missing 'hooks' key")
	}
}

func TestCLIConfigMaterializeMCP(t *testing.T) {
	hqPath, gtBinary := setupCLIConfigTown(t)

	workDir := filepath.Join(hqPath, "mat-test-mcp")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(gtBinary, "config", "materialize",
		"--mcp", "--scope=test-town")
	cmd.Dir = workDir
	cmd.Env = append(cleanConfigTestEnv(), "HOME="+filepath.Dir(hqPath))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gt config materialize --mcp failed: %v\nOutput: %s", err, output)
	}

	// Verify .mcp.json was created
	mcpPath := filepath.Join(workDir, ".mcp.json")
	data, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf(".mcp.json not created: %v", err)
	}

	var mcpConfig map[string]interface{}
	if err := json.Unmarshal(data, &mcpConfig); err != nil {
		t.Fatalf(".mcp.json is not valid JSON: %v", err)
	}

	if _, ok := mcpConfig["mcpServers"]; !ok {
		t.Error("materialized .mcp.json missing 'mcpServers' key")
	}
}

func TestCLIConfigMaterializeNoFlags(t *testing.T) {
	hqPath, gtBinary := setupCLIConfigTown(t)

	output, err := runGT(t, gtBinary, hqPath, "config", "materialize")
	if err == nil {
		t.Fatal("expected error when no --hooks or --mcp flag specified")
	}

	if !strings.Contains(output, "--hooks") || !strings.Contains(output, "--mcp") {
		t.Errorf("expected error mentioning --hooks and --mcp flags, got: %s", output)
	}
}

// --- gt config bead CRUD lifecycle ---

func TestCLIConfigBeadCRUDLifecycle(t *testing.T) {
	hqPath, gtBinary := setupCLIConfigTown(t)

	slug := "test-lifecycle-bead"
	category := "claude-hooks"
	metadata := `{"hooks":{"TestHook":[{"command":"echo test"}]}}`

	// Step 1: Create
	output, err := runGT(t, gtBinary, hqPath,
		"config", "bead", "create", slug,
		"--category="+category,
		"--metadata="+metadata)
	if err != nil {
		t.Fatalf("create failed: %v\nOutput: %s", err, output)
	}
	if !strings.Contains(output, "Created config bead") {
		t.Errorf("expected 'Created config bead' message, got: %s", output)
	}
	if !strings.Contains(output, slug) {
		t.Errorf("expected slug %q in output, got: %s", slug, output)
	}

	// Step 2: Show (text)
	output, err = runGT(t, gtBinary, hqPath,
		"config", "bead", "show", slug)
	if err != nil {
		t.Fatalf("show failed: %v\nOutput: %s", err, output)
	}
	if !strings.Contains(output, slug) {
		t.Errorf("expected slug in show output, got: %s", output)
	}
	if !strings.Contains(output, category) {
		t.Errorf("expected category in show output, got: %s", output)
	}

	// Step 3: Show (JSON)
	output, err = runGT(t, gtBinary, hqPath,
		"config", "bead", "show", slug, "--json")
	if err != nil {
		t.Fatalf("show --json failed: %v\nOutput: %s", err, output)
	}

	var detail ConfigBeadDetail
	if err := json.Unmarshal([]byte(output), &detail); err != nil {
		t.Fatalf("show --json output is not valid JSON: %v\nOutput: %s", err, output)
	}
	if detail.Slug != slug {
		t.Errorf("detail.Slug = %q, want %q", detail.Slug, slug)
	}
	if detail.Category != category {
		t.Errorf("detail.Category = %q, want %q", detail.Category, category)
	}

	// Step 4: List
	output, err = runGT(t, gtBinary, hqPath,
		"config", "bead", "list", "--json")
	if err != nil {
		t.Fatalf("list --json failed: %v\nOutput: %s", err, output)
	}

	var items []ConfigBeadListItem
	if err := json.Unmarshal([]byte(output), &items); err != nil {
		t.Fatalf("list --json output is not valid JSON: %v\nOutput: %s", err, output)
	}

	found := false
	for _, item := range items {
		if item.Slug == slug {
			found = true
			if item.Category != category {
				t.Errorf("listed bead category = %q, want %q", item.Category, category)
			}
			break
		}
	}
	if !found {
		t.Errorf("created bead %q not found in list output", slug)
	}

	// Step 5: Update (full replace)
	newMetadata := `{"hooks":{"UpdatedHook":[{"command":"echo updated"}]}}`
	output, err = runGT(t, gtBinary, hqPath,
		"config", "bead", "update", slug,
		"--metadata="+newMetadata)
	if err != nil {
		t.Fatalf("update failed: %v\nOutput: %s", err, output)
	}
	if !strings.Contains(output, "Updated config bead") {
		t.Errorf("expected 'Updated config bead' message, got: %s", output)
	}

	// Verify updated metadata
	output, err = runGT(t, gtBinary, hqPath,
		"config", "bead", "show", slug, "--json")
	if err != nil {
		t.Fatalf("show after update failed: %v\nOutput: %s", err, output)
	}
	if err := json.Unmarshal([]byte(output), &detail); err != nil {
		t.Fatalf("show --json after update is not valid JSON: %v", err)
	}
	metaJSON, _ := json.Marshal(detail.Metadata)
	if !strings.Contains(string(metaJSON), "UpdatedHook") {
		t.Errorf("metadata should contain UpdatedHook after update, got: %s", metaJSON)
	}

	// Step 6: Delete
	output, err = runGT(t, gtBinary, hqPath,
		"config", "bead", "delete", slug)
	if err != nil {
		t.Fatalf("delete failed: %v\nOutput: %s", err, output)
	}
	if !strings.Contains(output, "Deleted config bead") {
		t.Errorf("expected 'Deleted config bead' message, got: %s", output)
	}

	// Verify it's gone
	output, err = runGT(t, gtBinary, hqPath,
		"config", "bead", "show", slug)
	if err == nil {
		t.Error("expected error showing deleted bead, but got nil")
	}
	if !strings.Contains(output, "not found") {
		t.Errorf("expected 'not found' for deleted bead, got: %s", output)
	}
}

// --- gt config bead list tests ---

func TestCLIConfigBeadListByCategory(t *testing.T) {
	hqPath, gtBinary := setupCLIConfigTown(t)

	// List only claude-hooks beads
	output, err := runGT(t, gtBinary, hqPath,
		"config", "bead", "list", "--category=claude-hooks", "--json")
	if err != nil {
		t.Fatalf("list --category failed: %v\nOutput: %s", err, output)
	}

	var items []ConfigBeadListItem
	if err := json.Unmarshal([]byte(output), &items); err != nil {
		t.Fatalf("list --json output is not valid JSON: %v\nOutput: %s", err, output)
	}

	// All items should be claude-hooks category
	for _, item := range items {
		if item.Category != "claude-hooks" {
			t.Errorf("filtered list should only contain claude-hooks, got category=%q for %s",
				item.Category, item.Slug)
		}
	}

	if len(items) == 0 {
		t.Error("expected at least one claude-hooks config bead after seed")
	}
}

func TestCLIConfigBeadListText(t *testing.T) {
	hqPath, gtBinary := setupCLIConfigTown(t)

	output, err := runGT(t, gtBinary, hqPath, "config", "bead", "list")
	if err != nil {
		t.Fatalf("list failed: %v\nOutput: %s", err, output)
	}

	// Text output should show bead count
	if !strings.Contains(output, "config beads total") {
		t.Errorf("expected 'config beads total' in text output, got: %s", output)
	}
}

// --- gt config bead create validation ---

func TestCLIConfigBeadCreateMissingCategory(t *testing.T) {
	hqPath, gtBinary := setupCLIConfigTown(t)

	output, err := runGT(t, gtBinary, hqPath,
		"config", "bead", "create", "test-slug",
		"--metadata="+`{"key":"value"}`)
	if err == nil {
		t.Fatal("expected error for missing --category flag")
	}
	if !strings.Contains(output, "category") {
		t.Errorf("expected 'category' in error message, got: %s", output)
	}
}

func TestCLIConfigBeadCreateInvalidJSON(t *testing.T) {
	hqPath, gtBinary := setupCLIConfigTown(t)

	output, err := runGT(t, gtBinary, hqPath,
		"config", "bead", "create", "test-slug",
		"--category=claude-hooks",
		"--metadata=not-valid-json")
	if err == nil {
		t.Fatal("expected error for invalid JSON metadata")
	}
	if !strings.Contains(output, "JSON") {
		t.Errorf("expected 'JSON' in error message, got: %s", output)
	}
}

func TestCLIConfigBeadCreateInvalidCategory(t *testing.T) {
	hqPath, gtBinary := setupCLIConfigTown(t)

	output, err := runGT(t, gtBinary, hqPath,
		"config", "bead", "create", "test-slug",
		"--category=bogus-category",
		"--metadata="+`{"key":"value"}`)
	if err == nil {
		t.Fatal("expected error for invalid category")
	}
	if !strings.Contains(output, "invalid category") {
		t.Errorf("expected 'invalid category' in error message, got: %s", output)
	}
}

// --- gt config bead update with merge ---

func TestCLIConfigBeadUpdateMerge(t *testing.T) {
	hqPath, gtBinary := setupCLIConfigTown(t)

	slug := "test-merge-bead"
	initialMeta := `{"key1":"val1","key2":"val2"}`

	// Create
	output, err := runGT(t, gtBinary, hqPath,
		"config", "bead", "create", slug,
		"--category=claude-hooks",
		"--metadata="+initialMeta)
	if err != nil {
		t.Fatalf("create failed: %v\nOutput: %s", err, output)
	}

	// Merge update - add key3, override key1
	mergeMeta := `{"key1":"updated","key3":"new"}`
	output, err = runGT(t, gtBinary, hqPath,
		"config", "bead", "update", slug,
		"--merge-metadata="+mergeMeta)
	if err != nil {
		t.Fatalf("merge update failed: %v\nOutput: %s", err, output)
	}
	if !strings.Contains(output, "Merged metadata") {
		t.Errorf("expected 'Merged metadata' message, got: %s", output)
	}

	// Verify merged result
	output, err = runGT(t, gtBinary, hqPath,
		"config", "bead", "show", slug, "--json")
	if err != nil {
		t.Fatalf("show after merge failed: %v\nOutput: %s", err, output)
	}

	var detail ConfigBeadDetail
	if err := json.Unmarshal([]byte(output), &detail); err != nil {
		t.Fatalf("show --json is not valid JSON: %v", err)
	}

	metaMap, ok := detail.Metadata.(map[string]interface{})
	if !ok {
		t.Fatalf("metadata is not a map: %T", detail.Metadata)
	}

	// key1 should be updated
	if metaMap["key1"] != "updated" {
		t.Errorf("key1 = %v, want 'updated'", metaMap["key1"])
	}
	// key2 should be preserved
	if metaMap["key2"] != "val2" {
		t.Errorf("key2 = %v, want 'val2'", metaMap["key2"])
	}
	// key3 should be added
	if metaMap["key3"] != "new" {
		t.Errorf("key3 = %v, want 'new'", metaMap["key3"])
	}
}

// --- gt config bead delete nonexistent ---

func TestCLIConfigBeadDeleteNotFound(t *testing.T) {
	hqPath, gtBinary := setupCLIConfigTown(t)

	output, err := runGT(t, gtBinary, hqPath,
		"config", "bead", "delete", "nonexistent-slug")
	if err == nil {
		t.Fatal("expected error deleting nonexistent bead")
	}
	if !strings.Contains(output, "not found") {
		t.Errorf("expected 'not found' in error message, got: %s", output)
	}
}

// --- gt config seed idempotency via CLI ---

func TestCLIConfigSeedIdempotent(t *testing.T) {
	hqPath, gtBinary := setupCLIConfigTown(t)

	// First seed was done in setup. Count beads.
	output1, err := runGT(t, gtBinary, hqPath,
		"config", "bead", "list", "--json")
	if err != nil {
		t.Fatalf("first list failed: %v\nOutput: %s", err, output1)
	}

	var items1 []ConfigBeadListItem
	if err := json.Unmarshal([]byte(output1), &items1); err != nil {
		t.Fatalf("first list JSON parse failed: %v", err)
	}

	// Run seed again (use specific flags - full seed fails without accounts.json etc.)
	for _, seedFlag := range []string{"--hooks", "--mcp", "--identity"} {
		output, err := runGT(t, gtBinary, hqPath, "config", "seed", seedFlag)
		if err != nil {
			t.Fatalf("second seed %s failed: %v\nOutput: %s", seedFlag, err, output)
		}
		_ = output
	}

	// Count beads after second seed - should be the same
	output2, err := runGT(t, gtBinary, hqPath,
		"config", "bead", "list", "--json")
	if err != nil {
		t.Fatalf("second list failed: %v\nOutput: %s", err, output2)
	}

	var items2 []ConfigBeadListItem
	if err := json.Unmarshal([]byte(output2), &items2); err != nil {
		t.Fatalf("second list JSON parse failed: %v", err)
	}

	if len(items1) != len(items2) {
		t.Errorf("seed should be idempotent: first=%d beads, second=%d beads",
			len(items1), len(items2))
	}
}

// --- helpers ---

// keysOf returns the keys of a string-keyed map for diagnostic messages.
func keysOf(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

