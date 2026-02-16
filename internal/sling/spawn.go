package sling

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/events"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/terminal"
	"github.com/steveyegge/gastown/internal/workspace"
)

// ResolveExecutionTarget determines the execution target for a rig.
// Priority: explicit override > rig settings > K8s auto-detect > "local".
// When running inside a K8s pod (KUBERNETES_SERVICE_HOST is set), defaults
// to "k8s" instead of "local".
func ResolveExecutionTarget(rigPath, override string) config.ExecutionTarget {
	if override != "" {
		return config.ExecutionTarget(override)
	}

	settingsPath := filepath.Join(rigPath, "settings", "config.json")
	settings, err := config.LoadRigSettings(settingsPath)
	if err == nil && settings.Execution != nil && settings.Execution.Target != "" {
		return settings.Execution.Target
	}

	// Auto-detect K8s: every pod gets KUBERNETES_SERVICE_HOST injected by kubelet.
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return config.ExecutionTargetK8s
	}

	return config.ExecutionTargetLocal
}

// SpawnPolecatForSling creates a fresh polecat and starts its session.
// This is used by gt sling when the target is a rig name.
// The caller handles hook attachment and nudging.
func SpawnPolecatForSling(rigName string, opts SpawnOptions) (*SpawnResult, error) {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return nil, fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	rigsConfigPath := filepath.Join(townRoot, "mayor", "rigs.json")
	rigsConfig, err := config.LoadRigsConfig(rigsConfigPath)
	if err != nil {
		rigsConfig = &config.RigsConfig{Rigs: make(map[string]config.RigEntry)}
	}

	g := git.NewGit(townRoot)
	rigMgr := rig.NewManager(townRoot, rigsConfig, g)
	r, err := rigMgr.GetRig(rigName)
	if err != nil {
		return nil, fmt.Errorf("rig '%s' not found", rigName)
	}

	// Resolve execution target: explicit override > rig settings > "local"
	execTarget := ResolveExecutionTarget(r.Path, opts.ExecutionTarget)
	if execTarget != config.ExecutionTargetK8s {
		return nil, fmt.Errorf("local execution is no longer supported (codebase is K8s-only since v0.7.0)")
	}
	return spawnPolecatForK8s(townRoot, rigName, r, opts)
}

// WakeRigAgents wakes the witness for a rig after polecat dispatch.
func WakeRigAgents(rigName string) {
	bootCmd := exec.Command("gt", "rig", "boot", rigName)
	_ = bootCmd.Run()

	// Nudge witness via backend (coop in K8s mode).
	backend := terminal.NewCoopBackend(terminal.CoopConfig{})
	witnessSession := fmt.Sprintf("gt-%s-witness", rigName)
	_ = backend.NudgeSession(witnessSession, "Polecat dispatched - check for work")
}

// OjSlingEnabled returns true when OJ dispatch is active.
func OjSlingEnabled() bool {
	return os.Getenv("GT_SLING_OJ") == "1"
}

func verifyAndSetHookBead(townRoot, rigName, polecatName, hookBead string) error {
	prefix := beads.GetPrefixForRig(townRoot, rigName)
	agentBeadID := beads.PolecatBeadIDWithPrefix(prefix, rigName, polecatName)

	beadsClient := beads.New(townRoot)
	agentIssue, err := beadsClient.Show(agentBeadID)
	if err != nil {
		return fmt.Errorf("reading agent bead %s: %w", agentBeadID, err)
	}

	if agentIssue.HookBead == hookBead {
		return nil
	}

	fmt.Printf("  Retrying hook_bead set for %s...\n", agentBeadID)
	if err := beadsClient.SetHookBead(agentBeadID, hookBead); err != nil {
		return fmt.Errorf("retrying hook_bead set: %w", err)
	}
	return nil
}

func notifyWitnessRateLimit(rigName, polecatName, account string) {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return
	}

	subject := fmt.Sprintf("RATE_LIMITED polecat:%s", polecatName)
	body := fmt.Sprintf("Account: %s\nSource: spawn\nPolecat: %s\nRig: %s\n",
		account, polecatName, rigName)

	witnessAddr := fmt.Sprintf("%s/witness", rigName)
	cmd := exec.Command("gt", "mail", "send", witnessAddr, "-s", subject, "-m", body)
	cmd.Dir = townRoot
	_ = cmd.Run()
}

// spawnPolecatForK8s creates an agent bead for a K8s polecat without creating
// a local worktree or session. The K8s controller watches for agent beads
// with agent_state=spawning and execution_target:k8s label, then creates pods.
func spawnPolecatForK8s(townRoot, rigName string, r *rig.Rig, opts SpawnOptions) (*SpawnResult, error) {
	g := git.NewGit(r.Path)
	polecatMgr := polecat.NewManager(r, g)

	polecatName, err := polecatMgr.AllocateName()
	if err != nil {
		return nil, fmt.Errorf("allocating polecat name: %w", err)
	}
	fmt.Printf("Allocated polecat: %s (K8s)\n", polecatName)

	// Create or reopen agent bead with spawning state and hook_bead set atomically.
	// Use rig beads (mayor/rig) to match where local polecats store their agent beads.
	// The K8s controller detects agent_state=spawning + execution_target:k8s label
	// and creates a pod for this polecat.
	prefix := beads.GetPrefixForRig(townRoot, rigName)
	agentBeadID := beads.PolecatBeadIDWithPrefix(prefix, rigName, polecatName)
	rigBeadsPath := filepath.Join(r.Path, "mayor", "rig")
	beadsClient := beads.New(rigBeadsPath)
	_, err = beadsClient.CreateOrReopenAgentBead(agentBeadID, agentBeadID, &beads.AgentFields{
		RoleType:        "polecat",
		Rig:             rigName,
		AgentState:      "spawning",
		HookBead:        opts.HookBead,
		ExecutionTarget: "k8s",
	})
	if err != nil {
		return nil, fmt.Errorf("creating agent bead for K8s polecat: %w", err)
	}

	fmt.Printf("✓ Polecat %s dispatched to K8s (agent_state=spawning)\n", polecatName)

	_ = events.LogFeed(events.TypeSpawn, "gt", events.SpawnPayload(rigName, polecatName))

	return &SpawnResult{
		RigName:     rigName,
		PolecatName: polecatName,
		ClonePath:   "", // No local worktree for K8s polecats
		Account:     opts.Account,
		Agent:       opts.Agent,
		K8sSpawn:    true,
	}, nil
}

