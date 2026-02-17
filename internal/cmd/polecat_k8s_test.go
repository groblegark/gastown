package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/rig"
)

// --- extractPolecatNameFromBead tests ---

func TestExtractPolecatNameFromBead_Standard(t *testing.T) {
	issue := &beads.Issue{ID: "gt-gastown-polecat-Toast"}
	got := extractPolecatNameFromBead(issue, "gastown")
	if got != "Toast" {
		t.Errorf("extractPolecatNameFromBead() = %q, want %q", got, "Toast")
	}
}

func TestExtractPolecatNameFromBead_DedupedPrefix(t *testing.T) {
	// When prefix == rig, the rig component is omitted: prefix-polecat-NAME
	issue := &beads.Issue{ID: "gastown-polecat-Biscuit"}
	got := extractPolecatNameFromBead(issue, "gastown")
	if got != "Biscuit" {
		t.Errorf("extractPolecatNameFromBead() = %q, want %q", got, "Biscuit")
	}
}

func TestExtractPolecatNameFromBead_NoMarker(t *testing.T) {
	issue := &beads.Issue{ID: "gt-gastown-witness"}
	got := extractPolecatNameFromBead(issue, "gastown")
	if got != "" {
		t.Errorf("extractPolecatNameFromBead() = %q, want empty string", got)
	}
}

func TestExtractPolecatNameFromBead_EmptyID(t *testing.T) {
	issue := &beads.Issue{ID: ""}
	got := extractPolecatNameFromBead(issue, "gastown")
	if got != "" {
		t.Errorf("extractPolecatNameFromBead() = %q, want empty string", got)
	}
}

func TestExtractPolecatNameFromBead_MultipleDashes(t *testing.T) {
	// Polecat name may contain dashes
	issue := &beads.Issue{ID: "gt-gastown-polecat-Big-Toast"}
	got := extractPolecatNameFromBead(issue, "gastown")
	if got != "Big-Toast" {
		t.Errorf("extractPolecatNameFromBead() = %q, want %q", got, "Big-Toast")
	}
}

// --- discoverRigAgents K8s detection tests ---

func TestDiscoverRigAgents_K8sLabelSetsTarget(t *testing.T) {
	townRoot := t.TempDir()
	writeTestRoutes(t, townRoot, []beads.Route{
		{Prefix: "gt-", Path: "gastown/mayor/rig"},
	})

	r := &rig.Rig{
		Name:     "gastown",
		Path:     filepath.Join(townRoot, "gastown"),
		Polecats: []string{"Toast"},
	}

	allAgentBeads := map[string]*beads.Issue{
		"gt-gastown-polecat-Toast": {
			ID:         "gt-gastown-polecat-Toast",
			AgentState: "working",
			HookBead:   "gt-hook-1",
			Labels:     []string{"execution_target:k8s"},
		},
	}
	allHookBeads := map[string]*beads.Issue{
		"gt-hook-1": {ID: "gt-hook-1", Title: "Fix the bug"},
	}

	agents := discoverRigAgents(map[string]bool{}, r, nil, allAgentBeads, allHookBeads, nil, true)
	if len(agents) != 1 {
		t.Fatalf("discoverRigAgents() returned %d agents, want 1", len(agents))
	}

	if agents[0].Target != "k8s" {
		t.Errorf("agent Target = %q, want %q", agents[0].Target, "k8s")
	}
	if agents[0].State != "working" {
		t.Errorf("agent State = %q, want %q", agents[0].State, "working")
	}
	if !agents[0].HasWork {
		t.Error("agent HasWork = false, want true")
	}
	if agents[0].WorkTitle != "Fix the bug" {
		t.Errorf("agent WorkTitle = %q, want %q", agents[0].WorkTitle, "Fix the bug")
	}
}

func TestDiscoverRigAgents_LocalPolecatHasNoTarget(t *testing.T) {
	townRoot := t.TempDir()
	writeTestRoutes(t, townRoot, []beads.Route{
		{Prefix: "gt-", Path: "gastown/mayor/rig"},
	})

	r := &rig.Rig{
		Name:     "gastown",
		Path:     filepath.Join(townRoot, "gastown"),
		Polecats: []string{"Rascal"},
	}

	allAgentBeads := map[string]*beads.Issue{
		"gt-gastown-polecat-Rascal": {
			ID:         "gt-gastown-polecat-Rascal",
			AgentState: "working",
			// No execution_target:k8s label
		},
	}

	agents := discoverRigAgents(map[string]bool{}, r, nil, allAgentBeads, nil, nil, true)
	if len(agents) != 1 {
		t.Fatalf("discoverRigAgents() returned %d agents, want 1", len(agents))
	}

	if agents[0].Target != "" {
		t.Errorf("agent Target = %q, want empty (local)", agents[0].Target)
	}
}

func TestDiscoverRigAgents_MixedLocalAndK8s(t *testing.T) {
	townRoot := t.TempDir()
	writeTestRoutes(t, townRoot, []beads.Route{
		{Prefix: "gt-", Path: "gastown/mayor/rig"},
	})

	r := &rig.Rig{
		Name:     "gastown",
		Path:     filepath.Join(townRoot, "gastown"),
		Polecats: []string{"Local", "Remote"},
	}

	allAgentBeads := map[string]*beads.Issue{
		"gt-gastown-polecat-Local": {
			ID:         "gt-gastown-polecat-Local",
			AgentState: "working",
		},
		"gt-gastown-polecat-Remote": {
			ID:         "gt-gastown-polecat-Remote",
			AgentState: "spawning",
			Labels:     []string{"execution_target:k8s"},
		},
	}

	agents := discoverRigAgents(map[string]bool{}, r, nil, allAgentBeads, nil, nil, true)
	if len(agents) != 2 {
		t.Fatalf("discoverRigAgents() returned %d agents, want 2", len(agents))
	}

	// Find each by name
	agentByName := make(map[string]AgentRuntime)
	for _, a := range agents {
		agentByName[a.Name] = a
	}

	local := agentByName["Local"]
	if local.Target != "" {
		t.Errorf("Local agent Target = %q, want empty", local.Target)
	}

	remote := agentByName["Remote"]
	if remote.Target != "k8s" {
		t.Errorf("Remote agent Target = %q, want %q", remote.Target, "k8s")
	}
	if remote.State != "spawning" {
		t.Errorf("Remote agent State = %q, want %q", remote.State, "spawning")
	}
}

// --- buildStatusIndicator tests ---

func TestBuildStatusIndicator_K8sWorking(t *testing.T) {
	agent := AgentRuntime{Target: "k8s", State: "working"}
	result := buildStatusIndicator(agent)
	if !strings.Contains(result, "☸") {
		t.Errorf("K8s working indicator should contain ☸, got %q", result)
	}
	if !strings.Contains(result, "working") {
		t.Errorf("K8s working indicator should contain 'working', got %q", result)
	}
}

func TestBuildStatusIndicator_K8sSpawning(t *testing.T) {
	agent := AgentRuntime{Target: "k8s", State: "spawning"}
	result := buildStatusIndicator(agent)
	if !strings.Contains(result, "☸") {
		t.Errorf("K8s spawning indicator should contain ☸, got %q", result)
	}
	if !strings.Contains(result, "spawning") {
		t.Errorf("K8s spawning indicator should contain 'spawning', got %q", result)
	}
}

func TestBuildStatusIndicator_K8sDone(t *testing.T) {
	agent := AgentRuntime{Target: "k8s", State: "done"}
	result := buildStatusIndicator(agent)
	if !strings.Contains(result, "☸") {
		t.Errorf("K8s done indicator should contain ☸, got %q", result)
	}
	if !strings.Contains(result, "done") {
		t.Errorf("K8s done indicator should contain 'done', got %q", result)
	}
}

func TestBuildStatusIndicator_K8sStuck(t *testing.T) {
	agent := AgentRuntime{Target: "k8s", State: "stuck"}
	result := buildStatusIndicator(agent)
	if !strings.Contains(result, "☸") {
		t.Errorf("K8s stuck indicator should contain ☸, got %q", result)
	}
	if !strings.Contains(result, "stuck") {
		t.Errorf("K8s stuck indicator should contain 'stuck', got %q", result)
	}
}

func TestBuildStatusIndicator_K8sUnknownState(t *testing.T) {
	agent := AgentRuntime{Target: "k8s", State: ""}
	result := buildStatusIndicator(agent)
	if !strings.Contains(result, "☸") {
		t.Errorf("K8s unknown state indicator should contain ☸, got %q", result)
	}
}

func TestBuildStatusIndicator_LocalRunning(t *testing.T) {
	agent := AgentRuntime{Running: true}
	result := buildStatusIndicator(agent)
	// Local running uses ● (filled circle), not ☸
	if strings.Contains(result, "☸") {
		t.Errorf("Local running indicator should not contain ☸, got %q", result)
	}
	if !strings.Contains(result, "●") {
		t.Errorf("Local running indicator should contain ●, got %q", result)
	}
}

func TestBuildStatusIndicator_LocalStopped(t *testing.T) {
	agent := AgentRuntime{Running: false}
	result := buildStatusIndicator(agent)
	if strings.Contains(result, "☸") {
		t.Errorf("Local stopped indicator should not contain ☸, got %q", result)
	}
	if !strings.Contains(result, "○") {
		t.Errorf("Local stopped indicator should contain ○, got %q", result)
	}
}

// --- PolecatListItem Target field tests ---

func TestPolecatListItem_LocalTarget(t *testing.T) {
	item := PolecatListItem{
		Rig:    "gastown",
		Name:   "Toast",
		Target: "local",
	}
	if item.Target != "local" {
		t.Errorf("Target = %q, want %q", item.Target, "local")
	}
}

func TestPolecatListItem_K8sTarget(t *testing.T) {
	item := PolecatListItem{
		Rig:    "gastown",
		Name:   "Remote",
		Target: "k8s",
	}
	if item.Target != "k8s" {
		t.Errorf("Target = %q, want %q", item.Target, "k8s")
	}
}

// --- PolecatStatus Target field tests ---

func TestPolecatStatus_K8sTarget(t *testing.T) {
	status := PolecatStatus{
		Rig:    "gastown",
		Name:   "K8sAgent",
		Target: "k8s",
	}
	if status.Target != "k8s" {
		t.Errorf("Target = %q, want %q", status.Target, "k8s")
	}
}

func TestPolecatStatus_LocalTarget(t *testing.T) {
	status := PolecatStatus{
		Rig:    "gastown",
		Name:   "LocalAgent",
		Target: "local",
	}
	if status.Target != "local" {
		t.Errorf("Target = %q, want %q", status.Target, "local")
	}
}

// --- polecatTarget isK8s field tests ---

func TestPolecatTarget_IsK8sFlag(t *testing.T) {
	target := polecatTarget{
		rigName:     "gastown",
		polecatName: "Remote",
		isK8s:       true,
	}
	if !target.isK8s {
		t.Error("polecatTarget.isK8s = false, want true")
	}
}

// --- resolvePolecatTargets format validation tests ---

func TestResolvePolecatTargets_RejectsBareName(t *testing.T) {
	// A bare name without "/" should be rejected (not using --all)
	_, err := resolvePolecatTargets([]string{"Toast"}, false)
	if err == nil {
		t.Fatal("expected error for bare name without rig/polecat format")
	}
	if !strings.Contains(err.Error(), "rig/polecat") {
		t.Errorf("error %q should mention 'rig/polecat' format", err.Error())
	}
}

// --- SafetyCheckResult struct tests ---

func TestSafetyCheckResult_BlockedWhenReasons(t *testing.T) {
	result := &SafetyCheckResult{
		Polecat: "gastown/Toast",
		Blocked: true,
		Reasons: []string{"has unpushed commits"},
	}
	if !result.Blocked {
		t.Error("SafetyCheckResult.Blocked = false, want true when Reasons exist")
	}
}

func TestSafetyCheckResult_NotBlockedWhenNoReasons(t *testing.T) {
	result := &SafetyCheckResult{
		Polecat: "gastown/Toast",
	}
	if result.Blocked {
		t.Error("SafetyCheckResult.Blocked = true, want false when no Reasons")
	}
}

// --- displaySafetyCheckBlocked output tests ---

func TestDisplaySafetyCheckBlocked_ShowsPolecatNames(t *testing.T) {
	blocked := []*SafetyCheckResult{
		{
			Polecat: "gastown/Toast",
			Blocked: true,
			Reasons: []string{"has unpushed commits", "has open MR (gt-123)"},
		},
		{
			Polecat: "gastown/Remote",
			Blocked: true,
			Reasons: []string{"has work on hook (gt-456)"},
		},
	}

	output := captureStdout(t, func() {
		displaySafetyCheckBlocked(blocked)
	})

	if !strings.Contains(output, "gastown/Toast") {
		t.Errorf("output should contain 'gastown/Toast', got:\n%s", output)
	}
	if !strings.Contains(output, "gastown/Remote") {
		t.Errorf("output should contain 'gastown/Remote', got:\n%s", output)
	}
	if !strings.Contains(output, "unpushed") {
		t.Errorf("output should contain reason 'unpushed', got:\n%s", output)
	}
	if !strings.Contains(output, "--force") {
		t.Errorf("output should mention --force option, got:\n%s", output)
	}
}

// --- K8s status rendering output tests ---

func TestBuildStatusIndicator_K8sAllStates(t *testing.T) {
	tests := []struct {
		name     string
		state    string
		wantIcon string
		wantText string
	}{
		{"spawning", "spawning", "☸", "spawning"},
		{"working", "working", "☸", "working"},
		{"done", "done", "☸", "done"},
		{"stuck", "stuck", "☸", "stuck"},
		{"empty", "", "☸", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := AgentRuntime{Target: "k8s", State: tt.state}
			result := buildStatusIndicator(agent)
			if !strings.Contains(result, tt.wantIcon) {
				t.Errorf("indicator should contain %q, got %q", tt.wantIcon, result)
			}
			if tt.wantText != "" && !strings.Contains(result, tt.wantText) {
				t.Errorf("indicator should contain %q, got %q", tt.wantText, result)
			}
		})
	}
}

// --- AgentRuntime Target field integration test ---

func TestDiscoverRigAgents_K8sPolecatNotRunning(t *testing.T) {
	// K8s polecats should not show as "running" based on sessions
	townRoot := t.TempDir()
	writeTestRoutes(t, townRoot, []beads.Route{
		{Prefix: "gt-", Path: "gastown/mayor/rig"},
	})

	r := &rig.Rig{
		Name:     "gastown",
		Path:     filepath.Join(townRoot, "gastown"),
		Polecats: []string{"K8sAgent"},
	}

	// The session does not exist for this agent
	allSessions := map[string]bool{
		// no "gt-gastown-K8sAgent" entry
	}

	allAgentBeads := map[string]*beads.Issue{
		"gt-gastown-polecat-K8sAgent": {
			ID:         "gt-gastown-polecat-K8sAgent",
			AgentState: "working",
			Labels:     []string{"execution_target:k8s"},
		},
	}

	agents := discoverRigAgents(allSessions, r, nil, allAgentBeads, nil, nil, true)
	if len(agents) != 1 {
		t.Fatalf("discoverRigAgents() returned %d agents, want 1", len(agents))
	}

	agent := agents[0]
	if agent.Target != "k8s" {
		t.Errorf("agent Target = %q, want %q", agent.Target, "k8s")
	}
	if agent.Running {
		t.Error("K8s agent should not be Running (no session)")
	}
	if agent.State != "working" {
		t.Errorf("agent State = %q, want %q", agent.State, "working")
	}
}

// --- K8s polecats with sessions map doesn't affect K8s detection ---

func TestDiscoverRigAgents_K8sTargetNotAffectedByTmux(t *testing.T) {
	townRoot := t.TempDir()
	writeTestRoutes(t, townRoot, []beads.Route{
		{Prefix: "gt-", Path: "gastown/mayor/rig"},
	})

	r := &rig.Rig{
		Name:     "gastown",
		Path:     filepath.Join(townRoot, "gastown"),
		Polecats: []string{"Agent"},
	}

	// Even if a session with matching name exists, K8s target should still be "k8s"
	allSessions := map[string]bool{
		"gt-gastown-Agent": true,
	}

	allAgentBeads := map[string]*beads.Issue{
		"gt-gastown-polecat-Agent": {
			ID:         "gt-gastown-polecat-Agent",
			AgentState: "working",
			Labels:     []string{"execution_target:k8s"},
		},
	}

	agents := discoverRigAgents(allSessions, r, nil, allAgentBeads, nil, nil, true)
	if len(agents) != 1 {
		t.Fatalf("discoverRigAgents() returned %d agents, want 1", len(agents))
	}

	if agents[0].Target != "k8s" {
		t.Errorf("agent Target = %q, want %q (K8s label overrides session)", agents[0].Target, "k8s")
	}
}
