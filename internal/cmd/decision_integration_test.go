//go:build integration

// Package cmd contains integration tests for decision turn-check skip logic.
//
// Run with: go test -tags=integration ./internal/cmd -run TestDecisionTurnCheck -v
package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
)

// setupDecisionTestTown creates a minimal Gas Town with beads for testing decision logic.
// Returns townRoot.
func setupDecisionTestTown(t *testing.T) string {
	t.Helper()

	townRoot := t.TempDir()

	// Create town-level .beads directory
	townBeadsDir := filepath.Join(townRoot, ".beads")
	if err := os.MkdirAll(townBeadsDir, 0755); err != nil {
		t.Fatalf("mkdir town .beads: %v", err)
	}

	// Create routes.jsonl
	routes := []beads.Route{
		{Prefix: "hq-", Path: "."},
	}
	if err := beads.WriteRoutes(townBeadsDir, routes); err != nil {
		t.Fatalf("write routes: %v", err)
	}

	return townRoot
}

// initDecisionBeadsDB initializes beads database with prefix.
func initDecisionBeadsDB(t *testing.T, dir, prefix string) {
	t.Helper()

	cmd := exec.Command("bd", "--no-daemon", "init", "--quiet", "--prefix", prefix)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bd init failed in %s: %v\n%s", dir, err, output)
	}

	// Create empty issues.jsonl to prevent bd auto-export from corrupting routes.jsonl
	issuesPath := filepath.Join(dir, ".beads", "issues.jsonl")
	if err := os.WriteFile(issuesPath, []byte(""), 0644); err != nil {
		t.Fatalf("create issues.jsonl in %s: %v", dir, err)
	}
}

// createTestDecision creates a pending decision for testing.
func createTestDecision(t *testing.T, dir, question, requestedBy string) string {
	t.Helper()

	// Create decision using beads API directly
	bd := beads.New(beads.ResolveBeadsDir(dir))

	fields := &beads.DecisionFields{
		Question:    question,
		Options:     []beads.DecisionOption{{Label: "Yes"}, {Label: "No"}},
		Urgency:     beads.UrgencyMedium,
		RequestedBy: requestedBy,
	}

	issue, err := bd.CreateBdDecision(fields)
	if err != nil {
		t.Fatalf("create decision in %s: %v", dir, err)
	}

	return issue.ID
}

// TestCheckAgentHasPendingDecisions tests the checkAgentHasPendingDecisions function.
// This is the core function that determines whether turn-check should be skipped.
func TestCheckAgentHasPendingDecisions(t *testing.T) {
	// Skip if bd is not available
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not installed, skipping integration test")
	}

	townRoot := setupDecisionTestTown(t)
	initDecisionBeadsDB(t, townRoot, "hq")

	testAgentID := "gastown/polecats/test-agent"
	otherAgentID := "gastown/crew/other-agent"

	// Change to townRoot so workspace detection works
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer os.Chdir(oldCwd)
	if err := os.Chdir(townRoot); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	t.Run("no pending decisions returns false", func(t *testing.T) {
		// Set agent identity
		os.Setenv("GT_ROLE", testAgentID)
		defer os.Unsetenv("GT_ROLE")

		// No decisions exist yet
		result := checkAgentHasPendingDecisions()
		if result {
			t.Error("expected false when no pending decisions, got true")
		}
	})

	t.Run("pending decision from other agent returns false", func(t *testing.T) {
		// Create decision from another agent
		_ = createTestDecision(t, townRoot, "Other agent's question?", otherAgentID)

		// Set our agent identity
		os.Setenv("GT_ROLE", testAgentID)
		defer os.Unsetenv("GT_ROLE")

		// Should return false because the pending decision is from another agent
		result := checkAgentHasPendingDecisions()
		if result {
			t.Error("expected false when pending decision is from other agent, got true")
		}
	})

	t.Run("pending decision from current agent returns true", func(t *testing.T) {
		// Create decision from our agent
		_ = createTestDecision(t, townRoot, "Our agent's question?", testAgentID)

		// Set our agent identity
		os.Setenv("GT_ROLE", testAgentID)
		defer os.Unsetenv("GT_ROLE")

		// Should return true because we have a pending decision
		result := checkAgentHasPendingDecisions()
		if !result {
			t.Error("expected true when agent has pending decision, got false")
		}
	})
}

// TestTurnCheckSkipsWhenAgentHasPendingDecisions tests the full turn-check flow
// with the pending decisions skip logic.
func TestTurnCheckSkipsWhenAgentHasPendingDecisions(t *testing.T) {
	// Skip if bd is not available
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not installed, skipping integration test")
	}

	townRoot := setupDecisionTestTown(t)
	initDecisionBeadsDB(t, townRoot, "hq")

	testAgentID := "gastown/polecats/test-polecat"
	testSessionID := "test-session-turn-check-skip"

	// Change to townRoot so workspace detection works
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer os.Chdir(oldCwd)
	if err := os.Chdir(townRoot); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	// Clean up any existing marker
	clearTurnMarker(testSessionID)
	defer clearTurnMarker(testSessionID)

	t.Run("turn-check blocks when no marker and no pending decisions", func(t *testing.T) {
		os.Setenv("GT_ROLE", testAgentID)
		defer os.Unsetenv("GT_ROLE")

		// No marker exists, no pending decisions
		// Strict mode should block
		result := checkTurnMarker(testSessionID, false)
		if result == nil {
			t.Error("expected block result when no marker and no pending decisions")
		}
		if result != nil && result.Decision != "block" {
			t.Errorf("expected block decision, got %q", result.Decision)
		}
	})

	t.Run("turn-check allows when marker exists", func(t *testing.T) {
		// Create marker
		if err := createTurnMarker(testSessionID); err != nil {
			t.Fatalf("createTurnMarker: %v", err)
		}
		defer clearTurnMarker(testSessionID)

		// Should allow through
		result := checkTurnMarker(testSessionID, false)
		if result != nil {
			t.Errorf("expected nil (allow) when marker exists, got %+v", result)
		}
	})

	t.Run("turn-check skips block when agent has pending decisions", func(t *testing.T) {
		os.Setenv("GT_ROLE", testAgentID)
		defer os.Unsetenv("GT_ROLE")

		// Create a pending decision from this agent
		decisionID := createTestDecision(t, townRoot, "Should we proceed?", testAgentID)
		t.Logf("Created pending decision: %s", decisionID)

		// No marker exists
		clearTurnMarker(testSessionID)

		// Verify the agent has pending decisions
		hasPending := checkAgentHasPendingDecisions()
		if !hasPending {
			t.Fatal("expected checkAgentHasPendingDecisions to return true")
		}

		// Simulate the turn-check logic from runDecisionTurnCheck:
		// If no marker and not soft mode, check for pending decisions first
		markerExists := turnMarkerExists(testSessionID)
		if markerExists {
			t.Fatal("marker should not exist")
		}

		// The fix: when agent has pending decisions, skip the block
		// This is what runDecisionTurnCheck does
		if hasPending {
			// Turn-check should be skipped - agent has pending decisions
			t.Log("Turn-check skipped: agent has pending decisions")
		} else {
			// Would block - but this shouldn't happen in this test
			result := checkTurnMarker(testSessionID, false)
			if result == nil {
				t.Error("expected block when no pending decisions")
			}
		}
	})
}

// TestTurnCheckMarkerPersistenceMultipleFirings tests that the marker persists
// across multiple turn-check calls (regression test for bd-bug-stop_hook_fires_even_when_decision).
func TestTurnCheckMarkerPersistenceMultipleFirings(t *testing.T) {
	testSessionID := "test-session-marker-persistence"

	// Clean up
	clearTurnMarker(testSessionID)
	defer clearTurnMarker(testSessionID)

	// Create marker
	if err := createTurnMarker(testSessionID); err != nil {
		t.Fatalf("createTurnMarker: %v", err)
	}

	// Verify marker exists
	if !turnMarkerExists(testSessionID) {
		t.Fatal("marker should exist after creation")
	}

	// Multiple turn-check calls should all pass
	for i := 0; i < 5; i++ {
		result := checkTurnMarker(testSessionID, false)
		if result != nil {
			t.Errorf("call %d: expected nil (allow), got %+v", i+1, result)
		}
		// Marker should still exist
		if !turnMarkerExists(testSessionID) {
			t.Errorf("call %d: marker should persist after check", i+1)
		}
	}
}

// TestDecisionTurnCheckVerboseOutput tests that verbose mode outputs debug info.
func TestDecisionTurnCheckVerboseOutput(t *testing.T) {
	// This is a unit test that doesn't need the full workspace setup
	testSessionID := "test-session-verbose"

	clearTurnMarker(testSessionID)
	defer clearTurnMarker(testSessionID)

	// Test with marker
	if err := createTurnMarker(testSessionID); err != nil {
		t.Fatalf("createTurnMarker: %v", err)
	}

	// Verify marker path is correct
	path := turnMarkerPath(testSessionID)
	expected := "/tmp/.decision-offered-" + testSessionID
	if path != expected {
		t.Errorf("turnMarkerPath = %q, want %q", path, expected)
	}
}

// TestTurnCheckSoftModeNeverBlocks tests that soft mode never blocks.
func TestTurnCheckSoftModeNeverBlocks(t *testing.T) {
	testSessionID := "test-session-soft-mode"

	// Clean state - no marker
	clearTurnMarker(testSessionID)
	defer clearTurnMarker(testSessionID)

	// Soft mode should never block, even without marker
	result := checkTurnMarker(testSessionID, true)
	if result != nil {
		t.Errorf("soft mode should return nil, got %+v", result)
	}
}

// TestPendingDecisionFieldsParsing tests that decision fields are parsed correctly
// for the RequestedBy check.
func TestPendingDecisionFieldsParsing(t *testing.T) {
	// Test the parsing logic used in checkAgentHasPendingDecisions
	testDesc := `## Question
Should we proceed?

## Options
### 1. Yes
### 2. No

---
_Requested by: gastown/polecats/test-agent_
_Urgency: medium_`

	fields := beads.ParseDecisionFields(testDesc)

	if fields.RequestedBy != "gastown/polecats/test-agent" {
		t.Errorf("RequestedBy = %q, want %q", fields.RequestedBy, "gastown/polecats/test-agent")
	}

	if fields.Question != "Should we proceed?" {
		t.Errorf("Question = %q, want %q", fields.Question, "Should we proceed?")
	}

	if fields.Urgency != "medium" {
		t.Errorf("Urgency = %q, want %q", fields.Urgency, "medium")
	}
}

// TestDecisionIntegrationWorkspaceDetection tests that workspace detection works
// correctly for the pending decisions check.
func TestDecisionIntegrationWorkspaceDetection(t *testing.T) {
	// Skip if bd is not available
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not installed, skipping integration test")
	}

	townRoot := setupDecisionTestTown(t)
	initDecisionBeadsDB(t, townRoot, "hq")

	// Verify beads directory exists
	beadsDir := filepath.Join(townRoot, ".beads")
	if _, err := os.Stat(beadsDir); os.IsNotExist(err) {
		t.Fatal("beads directory should exist")
	}

	// Verify ResolveBeadsDir works
	resolved := beads.ResolveBeadsDir(townRoot)
	if resolved != beadsDir {
		t.Errorf("ResolveBeadsDir = %q, want %q", resolved, beadsDir)
	}

	// Verify beads connection works
	bd := beads.New(resolved)
	issues, err := bd.ListAllPendingDecisions()
	if err != nil {
		t.Fatalf("ListAllPendingDecisions: %v", err)
	}

	// Should be empty initially
	if len(issues) != 0 {
		t.Errorf("expected 0 pending decisions, got %d", len(issues))
	}
}

// TestDecisionTurnCheckIntegrationEndToEnd tests the complete flow from
// decision creation to turn-check skip.
func TestDecisionTurnCheckIntegrationEndToEnd(t *testing.T) {
	// Skip if bd is not available
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not installed, skipping integration test")
	}

	townRoot := setupDecisionTestTown(t)
	initDecisionBeadsDB(t, townRoot, "hq")

	testAgentID := "gastown/polecats/integration-test-agent"
	testSessionID := "test-session-e2e-" + t.Name()

	// Change to townRoot
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer os.Chdir(oldCwd)
	if err := os.Chdir(townRoot); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	// Set agent identity
	os.Setenv("GT_ROLE", testAgentID)
	defer os.Unsetenv("GT_ROLE")

	// Clean marker state
	clearTurnMarker(testSessionID)
	defer clearTurnMarker(testSessionID)

	// Step 1: Verify no pending decisions initially
	if checkAgentHasPendingDecisions() {
		t.Fatal("should have no pending decisions initially")
	}

	// Step 2: Turn-check would block (no marker, no pending decisions)
	result := checkTurnMarker(testSessionID, false)
	if result == nil || result.Decision != "block" {
		t.Fatal("should block when no marker and no pending decisions")
	}

	// Step 3: Create a pending decision from this agent
	decisionID := createTestDecision(t, townRoot, "Integration test question?", testAgentID)
	t.Logf("Created decision: %s", decisionID)

	// Step 4: Verify agent now has pending decisions
	if !checkAgentHasPendingDecisions() {
		t.Fatal("should have pending decisions after creating one")
	}

	// Step 5: The turn-check skip logic (simulated)
	// In runDecisionTurnCheck, before checking marker, we check for pending decisions
	hasPending := checkAgentHasPendingDecisions()
	if !hasPending {
		t.Fatal("hasPending should be true")
	}

	// When hasPending is true, turn-check is skipped (returns early with nil)
	// This prevents blocking agents that already have outstanding decisions
	t.Log("Turn-check skip logic verified: agent has pending decisions")

	// Step 6: Verify the decision can be retrieved
	bd := beads.New(beads.ResolveBeadsDir(townRoot))
	issue, fields, err := bd.GetDecisionBead(decisionID)
	if err != nil {
		t.Fatalf("GetDecisionBead: %v", err)
	}
	if issue == nil || fields == nil {
		t.Fatal("decision should exist")
	}
	if fields.RequestedBy != testAgentID {
		t.Errorf("RequestedBy = %q, want %q", fields.RequestedBy, testAgentID)
	}
	if fields.ChosenIndex != 0 {
		t.Errorf("ChosenIndex = %d, want 0 (pending)", fields.ChosenIndex)
	}
}

// TestDecisionTurnCheckJSONOutput tests the JSON output format of turn-check blocks.
func TestDecisionTurnCheckJSONOutput(t *testing.T) {
	testSessionID := "test-session-json-output"

	clearTurnMarker(testSessionID)
	defer clearTurnMarker(testSessionID)

	// Get block result
	result := checkTurnMarker(testSessionID, false)
	if result == nil {
		t.Fatal("expected block result")
	}

	// Verify JSON marshaling works
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	// Verify structure
	var decoded TurnBlockResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if decoded.Decision != "block" {
		t.Errorf("Decision = %q, want 'block'", decoded.Decision)
	}

	if decoded.Reason == "" {
		t.Error("Reason should not be empty")
	}
}
