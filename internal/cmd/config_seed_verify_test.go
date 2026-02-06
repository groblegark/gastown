//go:build integration

// Package cmd provides integration tests verifying that gt config seed --all
// populates valid config beads for every category. These tests build the gt
// binary, install a town with all prerequisite config files, run the seed
// command, and cross-check the results via list, resolve, and verify.
//
// Run with: go test -tags=integration ./internal/cmd/ -run TestSeedVerify -v
package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// allConfigCategories lists every category that gt config seed --all should populate.
var allConfigCategories = []string{
	"claude-hooks",
	"mcp",
	"identity",
	"rig-registry",
	"accounts",
	"daemon",
	"role-definition",
	"agent-preset",
	"slack-routing",
	"messaging",
	"escalation",
}

// setupFullSeedTown creates a Gas Town with every config file needed for
// gt config seed --all and returns (townRoot, gtBinary).
func setupFullSeedTown(t *testing.T) (string, string) {
	t.Helper()

	gtBinary := buildGT(t)
	tmpDir := t.TempDir()
	hqPath := filepath.Join(tmpDir, "seed-hq")

	baseEnv := append(cleanConfigTestEnv(), "HOME="+tmpDir)

	// Install a fresh town (creates town.json, rigs.json, daemon.json, escalation.json)
	cmd := exec.Command(gtBinary, "install", hqPath, "--name", "seed-town")
	cmd.Env = baseEnv
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gt install failed: %v\nOutput: %s", err, output)
	}

	// Create mayor/accounts.json (required for --accounts seed)
	accountsJSON := `{
		"version": 1,
		"default": "main",
		"accounts": {
			"main": {
				"config_dir": "/tmp/test-claude",
				"email": "test@example.com",
				"description": "Test account",
				"auth_token": "sk-secret-token-DO-NOT-STORE"
			},
			"backup": {
				"config_dir": "/tmp/test-backup",
				"email": "backup@example.com",
				"base_url": "http://localhost:4000",
				"auth_token": "sk-another-secret"
			}
		}
	}`
	accountsPath := filepath.Join(hqPath, "mayor", "accounts.json")
	if err := os.WriteFile(accountsPath, []byte(accountsJSON), 0644); err != nil {
		t.Fatalf("writing accounts.json: %v", err)
	}

	// Create settings/slack.json (required for --slack seed)
	slackJSON := `{
		"type": "slack",
		"version": 1,
		"enabled": true,
		"default_channel": "C0123456789",
		"channels": {"seed-town/polecats/*": "C0987654321"},
		"channel_names": {"C0987654321": "#polecat-work"},
		"bot_token": "xoxb-secret-bot-token",
		"app_token": "xapp-secret-app-token"
	}`
	settingsDir := filepath.Join(hqPath, "settings")
	os.MkdirAll(settingsDir, 0755)
	if err := os.WriteFile(filepath.Join(settingsDir, "slack.json"), []byte(slackJSON), 0644); err != nil {
		t.Fatalf("writing slack.json: %v", err)
	}

	// Create a rig entry so rig-registry seed has something to process
	rigsJSON := `{
		"version": 1,
		"rigs": {
			"testrig": {
				"git_url": "https://example.com/testrig.git",
				"local_repo": "testrig",
				"added_at": "2026-01-01T00:00:00Z",
				"beads": { "repo": "testrig", "prefix": "tr-" }
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(hqPath, "mayor", "rigs.json"), []byte(rigsJSON), 0644); err != nil {
		t.Fatalf("writing rigs.json: %v", err)
	}

	// Create a per-rig config.json so the rig config bead is also seeded
	rigDir := filepath.Join(hqPath, "testrig")
	os.MkdirAll(rigDir, 0755)
	rigConfigJSON := `{
		"type": "rig",
		"name": "testrig",
		"git_url": "https://example.com/testrig.git",
		"local_repo": "testrig",
		"beads": { "prefix": "tr-" }
	}`
	if err := os.WriteFile(filepath.Join(rigDir, "config.json"), []byte(rigConfigJSON), 0644); err != nil {
		t.Fatalf("writing rig config.json: %v", err)
	}

	return hqPath, gtBinary
}

// runSeedAll executes gt config seed (with optional extra flags) and returns output.
func runSeedAll(t *testing.T, gtBinary, townRoot string, extraFlags ...string) (string, error) {
	t.Helper()
	args := append([]string{"config", "seed"}, extraFlags...)
	return runGT(t, gtBinary, townRoot, args...)
}

// listBeadsByCategory returns ConfigBeadListItems filtered by category.
func listBeadsByCategory(t *testing.T, gtBinary, townRoot, category string) []ConfigBeadListItem {
	t.Helper()
	output, err := runGT(t, gtBinary, townRoot,
		"config", "bead", "list", "--category="+category, "--json")
	if err != nil {
		t.Fatalf("list --category=%s failed: %v\nOutput: %s", category, err, output)
	}

	var items []ConfigBeadListItem
	if err := json.Unmarshal([]byte(output), &items); err != nil {
		t.Fatalf("parsing list output for category %s: %v\nOutput: %s", category, err, output)
	}
	return items
}

// listAllBeads returns all config beads as JSON.
func listAllBeads(t *testing.T, gtBinary, townRoot string) []ConfigBeadListItem {
	t.Helper()
	output, err := runGT(t, gtBinary, townRoot, "config", "bead", "list", "--json")
	if err != nil {
		t.Fatalf("list --json failed: %v\nOutput: %s", err, output)
	}

	var items []ConfigBeadListItem
	if err := json.Unmarshal([]byte(output), &items); err != nil {
		t.Fatalf("parsing list output: %v\nOutput: %s", err, output)
	}
	return items
}

// --- Tests ---

// TestSeedVerifyAllCategories runs gt config seed --all and verifies every
// config category has at least one bead populated.
func TestSeedVerifyAllCategories(t *testing.T) {
	hqPath, gtBinary := setupFullSeedTown(t)

	// Run seed --all (no flags = seeds everything)
	output, err := runSeedAll(t, gtBinary, hqPath)
	if err != nil {
		t.Fatalf("gt config seed failed: %v\nOutput: %s", err, output)
	}

	// Output should indicate successful creation
	if !strings.Contains(output, "Seed complete") {
		t.Errorf("expected 'Seed complete' in output, got:\n%s", output)
	}

	// Verify every category has at least one bead
	for _, category := range allConfigCategories {
		t.Run("category_"+category, func(t *testing.T) {
			items := listBeadsByCategory(t, gtBinary, hqPath, category)
			if len(items) == 0 {
				t.Errorf("category %s has no config beads after seed --all", category)
			}

			// All items should match the expected category
			for _, item := range items {
				if item.Category != category {
					t.Errorf("bead %s has category %q, want %q",
						item.ID, item.Category, category)
				}
			}
		})
	}

	// Total bead count should be > 11 (hooks create multiple, rigs create multiple, etc.)
	allItems := listAllBeads(t, gtBinary, hqPath)
	if len(allItems) < len(allConfigCategories) {
		t.Errorf("expected at least %d beads total, got %d",
			len(allConfigCategories), len(allItems))
	}
	t.Logf("Total config beads created: %d", len(allItems))
}

// TestSeedVerifyIdempotent verifies that running seed --all twice produces
// the same number of beads (second run skips everything).
func TestSeedVerifyIdempotent(t *testing.T) {
	hqPath, gtBinary := setupFullSeedTown(t)

	// First seed
	output1, err := runSeedAll(t, gtBinary, hqPath)
	if err != nil {
		t.Fatalf("first seed failed: %v\nOutput: %s", err, output1)
	}
	count1 := len(listAllBeads(t, gtBinary, hqPath))

	// Second seed (should skip all)
	output2, err := runSeedAll(t, gtBinary, hqPath)
	if err != nil {
		t.Fatalf("second seed failed: %v\nOutput: %s", err, output2)
	}

	count2 := len(listAllBeads(t, gtBinary, hqPath))
	if count1 != count2 {
		t.Errorf("seed is not idempotent: first=%d beads, second=%d beads", count1, count2)
	}

	// Second output should show only skips, no creates
	if strings.Contains(output2, "created") {
		// Parse the summary line for created count
		if !strings.Contains(output2, "created 0") {
			t.Errorf("second seed should create 0 beads, got:\n%s", output2)
		}
	}
}

// TestSeedVerifyDryRun verifies that --dry-run creates nothing.
func TestSeedVerifyDryRun(t *testing.T) {
	hqPath, gtBinary := setupFullSeedTown(t)

	// Dry run
	output, err := runSeedAll(t, gtBinary, hqPath, "--dry-run")
	if err != nil {
		t.Fatalf("dry run failed: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(output, "Dry run complete") {
		t.Errorf("expected 'Dry run complete' in output, got:\n%s", output)
	}

	// Verify no beads were actually created
	items := listAllBeads(t, gtBinary, hqPath)

	// gt install creates a town identity bead, so we may have 1 already
	// but seed --dry-run should not add any new ones
	if len(items) > 1 {
		t.Errorf("dry run should not create beads, but found %d (expected at most 1 from install)",
			len(items))
	}
}

// TestSeedVerifyForce verifies that --force updates existing beads.
func TestSeedVerifyForce(t *testing.T) {
	hqPath, gtBinary := setupFullSeedTown(t)

	// First seed
	_, err := runSeedAll(t, gtBinary, hqPath)
	if err != nil {
		t.Fatalf("first seed failed: %v", err)
	}
	count1 := len(listAllBeads(t, gtBinary, hqPath))

	// Force re-seed
	output, err := runSeedAll(t, gtBinary, hqPath, "--force")
	if err != nil {
		t.Fatalf("force seed failed: %v\nOutput: %s", err, output)
	}

	// Same number of beads (no duplicates)
	count2 := len(listAllBeads(t, gtBinary, hqPath))
	if count1 != count2 {
		t.Errorf("force seed changed bead count: before=%d, after=%d", count1, count2)
	}

	// Output should mention updates
	if !strings.Contains(output, "updated") {
		t.Errorf("force seed should report updates, got:\n%s", output)
	}
}

// TestSeedVerifyMetadataValid checks that all config bead metadata is valid JSON.
func TestSeedVerifyMetadataValid(t *testing.T) {
	hqPath, gtBinary := setupFullSeedTown(t)

	_, err := runSeedAll(t, gtBinary, hqPath)
	if err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	items := listAllBeads(t, gtBinary, hqPath)
	for _, item := range items {
		t.Run("metadata_"+item.Slug, func(t *testing.T) {
			output, err := runGT(t, gtBinary, hqPath,
				"config", "bead", "show", item.Slug, "--json")
			if err != nil {
				t.Fatalf("show %s failed: %v\nOutput: %s", item.Slug, err, output)
			}

			var detail ConfigBeadDetail
			if err := json.Unmarshal([]byte(output), &detail); err != nil {
				t.Fatalf("show --json output for %s is not valid JSON: %v\nOutput: %s",
					item.Slug, err, output)
			}

			// Metadata should be non-nil
			if detail.Metadata == nil {
				t.Errorf("bead %s has nil metadata", item.Slug)
			}

			// Metadata should re-marshal to valid JSON
			metaJSON, err := json.Marshal(detail.Metadata)
			if err != nil {
				t.Errorf("bead %s metadata re-marshal failed: %v", item.Slug, err)
			}
			if !json.Valid(metaJSON) {
				t.Errorf("bead %s metadata is not valid JSON: %s", item.Slug, metaJSON)
			}
		})
	}
}

// TestSeedVerifySecretExclusion ensures that secrets are never stored in beads.
func TestSeedVerifySecretExclusion(t *testing.T) {
	hqPath, gtBinary := setupFullSeedTown(t)

	_, err := runSeedAll(t, gtBinary, hqPath)
	if err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	// Check account beads don't contain auth_token
	accountBeads := listBeadsByCategory(t, gtBinary, hqPath, "accounts")
	for _, item := range accountBeads {
		output, err := runGT(t, gtBinary, hqPath,
			"config", "bead", "show", item.Slug, "--json")
		if err != nil {
			t.Fatalf("show %s failed: %v", item.Slug, err)
		}

		if strings.Contains(output, "auth_token") {
			t.Errorf("account bead %s contains auth_token (secret leak)", item.Slug)
		}
		if strings.Contains(output, "sk-secret") {
			t.Errorf("account bead %s contains secret token value", item.Slug)
		}
	}

	// Check slack bead doesn't contain bot_token or app_token
	slackBeads := listBeadsByCategory(t, gtBinary, hqPath, "slack-routing")
	for _, item := range slackBeads {
		output, err := runGT(t, gtBinary, hqPath,
			"config", "bead", "show", item.Slug, "--json")
		if err != nil {
			t.Fatalf("show %s failed: %v", item.Slug, err)
		}

		if strings.Contains(output, "bot_token") {
			t.Errorf("slack bead %s contains bot_token (secret leak)", item.Slug)
		}
		if strings.Contains(output, "app_token") {
			t.Errorf("slack bead %s contains app_token (secret leak)", item.Slug)
		}
		if strings.Contains(output, "xoxb-") {
			t.Errorf("slack bead %s contains bot token value", item.Slug)
		}
		if strings.Contains(output, "xapp-") {
			t.Errorf("slack bead %s contains app token value", item.Slug)
		}
	}
}

// TestSeedVerifyResolvePerCategory verifies that gt config resolve works
// for each seeded category.
func TestSeedVerifyResolvePerCategory(t *testing.T) {
	hqPath, gtBinary := setupFullSeedTown(t)

	_, err := runSeedAll(t, gtBinary, hqPath)
	if err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	for _, category := range allConfigCategories {
		t.Run("resolve_"+category, func(t *testing.T) {
			args := []string{"config", "resolve", category,
				"--town=seed-town", "--json"}

			// Some categories need additional scope for resolution
			if category == "claude-hooks" || category == "role-definition" {
				args = append(args, "--role=polecat")
			}
			if category == "rig-registry" {
				args = append(args, "--rig=testrig")
			}

			output, err := runGT(t, gtBinary, hqPath, args...)
			if err != nil {
				t.Fatalf("resolve %s failed: %v\nOutput: %s", category, err, output)
			}

			// Output should be valid JSON
			if !json.Valid([]byte(output)) {
				t.Errorf("resolve %s output is not valid JSON:\n%s", category, output)
			}
		})
	}
}

// TestSeedVerifyConfigVerifyPasses runs gt config verify after seed --all
// and checks that all beads pass validation.
func TestSeedVerifyConfigVerifyPasses(t *testing.T) {
	hqPath, gtBinary := setupFullSeedTown(t)

	_, err := runSeedAll(t, gtBinary, hqPath)
	if err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	output, err := runGT(t, gtBinary, hqPath, "config", "verify")
	if err != nil {
		t.Fatalf("verify failed: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(output, "config beads valid") {
		t.Errorf("expected 'config beads valid' after full seed, got:\n%s", output)
	}
}

// TestSeedVerifyExpectedBeadSlugs verifies that specific well-known beads
// are created by seed --all.
func TestSeedVerifyExpectedBeadSlugs(t *testing.T) {
	hqPath, gtBinary := setupFullSeedTown(t)

	_, err := runSeedAll(t, gtBinary, hqPath)
	if err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	allItems := listAllBeads(t, gtBinary, hqPath)
	slugs := make(map[string]bool, len(allItems))
	for _, item := range allItems {
		slugs[item.Slug] = true
	}

	// Expected well-known slugs from each category
	expectedSlugs := []string{
		"hooks-base",                   // claude-hooks: shared base
		"mcp-global",                   // mcp: global MCP config
		"town-seed-town",              // identity: town identity
		"rig-seed-town-testrig",       // rig-registry: rig entry
		"account-main",                // accounts: main account
		"account-backup",              // accounts: backup account
		"accounts-default",            // accounts: default selection
		"daemon-patrol",               // daemon: patrol config
		"slack-routing",               // slack-routing: global slack
		"messaging",                   // messaging: global messaging
		"escalation",                  // escalation: global escalation
		"role-agents-global",          // agent-preset: role-agent mappings
	}

	for _, slug := range expectedSlugs {
		if !slugs[slug] {
			t.Errorf("expected bead with slug %q not found", slug)
		}
	}
}

// TestSeedVerifyRigRegistryHasRigAndRigConfig checks that the rig-registry
// category contains both registry entry and per-rig config beads.
func TestSeedVerifyRigRegistryHasRigAndRigConfig(t *testing.T) {
	hqPath, gtBinary := setupFullSeedTown(t)

	_, err := runSeedAll(t, gtBinary, hqPath)
	if err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	rigBeads := listBeadsByCategory(t, gtBinary, hqPath, "rig-registry")
	if len(rigBeads) < 2 {
		t.Fatalf("expected at least 2 rig-registry beads (registry + rigcfg), got %d", len(rigBeads))
	}

	slugs := make(map[string]bool)
	for _, item := range rigBeads {
		slugs[item.Slug] = true
	}

	// Should have both rig- and rigcfg- entries for testrig
	if !slugs["rig-seed-town-testrig"] {
		t.Error("missing rig-seed-town-testrig bead (rig registry entry)")
	}
	if !slugs["rigcfg-seed-town-testrig"] {
		t.Error("missing rigcfg-seed-town-testrig bead (per-rig config)")
	}
}

// TestSeedVerifyHookBeadsHaveRoleOverrides checks that hooks seed creates
// base + role-specific override beads.
func TestSeedVerifyHookBeadsHaveRoleOverrides(t *testing.T) {
	hqPath, gtBinary := setupFullSeedTown(t)

	_, err := runSeedAll(t, gtBinary, hqPath)
	if err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	hookBeads := listBeadsByCategory(t, gtBinary, hqPath, "claude-hooks")
	if len(hookBeads) < 2 {
		t.Fatalf("expected at least 2 claude-hooks beads (base + role overrides), got %d", len(hookBeads))
	}

	slugs := make(map[string]bool)
	for _, item := range hookBeads {
		slugs[item.Slug] = true
	}

	if !slugs["hooks-base"] {
		t.Error("missing hooks-base bead (shared hooks)")
	}

	// At least one role-specific override should exist
	hasOverride := slugs["hooks-polecat"] || slugs["hooks-crew"]
	if !hasOverride {
		t.Error("expected at least one role-specific hook override (hooks-polecat or hooks-crew)")
	}
}
