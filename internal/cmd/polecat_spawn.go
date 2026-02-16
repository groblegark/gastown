// Package cmd provides polecat spawning utilities for gt sling.
package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/events"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/workspace"
)

// SpawnedPolecatInfo contains info about a spawned polecat session.
type SpawnedPolecatInfo struct {
	RigName     string // Rig name (e.g., "gastown")
	PolecatName string // Polecat name (e.g., "Toast")
	ClonePath   string // Path to polecat's git worktree
	K8sSpawn    bool   // True when dispatched to K8s (no local worktree/session)
}

// AgentID returns the agent identifier (e.g., "gastown/polecats/Toast")
func (s *SpawnedPolecatInfo) AgentID() string {
	return fmt.Sprintf("%s/polecats/%s", s.RigName, s.PolecatName)
}

// SlingSpawnOptions contains options for spawning a polecat via sling.
type SlingSpawnOptions struct {
	Force           bool   // Force spawn even if polecat has uncommitted work
	Account         string // Claude Code account handle to use
	Create          bool   // Create polecat if it doesn't exist (currently always true for sling)
	HookBead        string // Bead ID to set as hook_bead at spawn time (atomic assignment)
	Agent           string // Agent override for this spawn (e.g., "gemini", "codex", "claude-haiku")
	ExecutionTarget string // "local" (default) or "k8s" — overrides rig config
}

// SpawnPolecatForSling creates a fresh polecat and optionally starts its session.
// This is used by gt sling when the target is a rig name.
// The caller (sling) handles hook attachment and nudging.
func SpawnPolecatForSling(rigName string, opts SlingSpawnOptions) (*SpawnedPolecatInfo, error) {
	// Find workspace
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return nil, fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	// Load rig config (beads-first, filesystem fallback).
	rigsConfig, err := loadRigsConfigBeadsFirst(townRoot)
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
	execTarget := resolveExecutionTarget(r.Path, opts.ExecutionTarget)
	if execTarget != config.ExecutionTargetK8s {
		return nil, fmt.Errorf("local execution is no longer supported (codebase is K8s-only since v0.7.0)")
	}
	return spawnPolecatForK8sCMD(townRoot, rigName, r, opts)
}

// IsRigName checks if a target string is a rig name (not a role or path).
// Returns the rig name and true if it's a valid rig.
func IsRigName(target string) (string, bool) {
	// If it contains a slash, it's a path format (rig/role or rig/crew/name)
	if strings.Contains(target, "/") {
		return "", false
	}

	// Check known non-rig role names
	switch strings.ToLower(target) {
	case "mayor", "may", "deacon", "dea", "crew", "witness", "wit", "refinery", "ref":
		return "", false
	}

	// Try to load as a rig
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return "", false
	}

	rigsConfig, err := loadRigsConfigBeadsFirst(townRoot)
	if err != nil {
		return "", false
	}

	g := git.NewGit(townRoot)
	rigMgr := rig.NewManager(townRoot, rigsConfig, g)
	_, err = rigMgr.GetRig(target)
	if err != nil {
		return "", false
	}

	return target, true
}

// verifyAndSetHookBead verifies the agent bead has hook_bead set, and retries if not.
// This fixes bd-3q6.8-1 where the slot set in CreateOrReopenAgentBead may fail silently.
func verifyAndSetHookBead(townRoot, rigName, polecatName, hookBead string) error {
	// Agent bead uses rig prefix and is stored in rig beads
	prefix := beads.GetPrefixForRig(townRoot, rigName)
	agentBeadID := beads.PolecatBeadIDWithPrefix(prefix, rigName, polecatName)

	// Read the agent bead
	beadsClient := beads.New(townRoot)
	agentIssue, err := beadsClient.Show(agentBeadID)
	if err != nil {
		return fmt.Errorf("reading agent bead %s: %w", agentBeadID, err)
	}

	// Check if hook_bead is already set correctly
	if agentIssue.HookBead == hookBead {
		return nil // Already set correctly
	}

	// Hook not set or set to wrong value - retry setting it
	fmt.Printf("  Retrying hook_bead set for %s...\n", agentBeadID)
	if err := beadsClient.SetHookBead(agentBeadID, hookBead); err != nil {
		return fmt.Errorf("retrying hook_bead set: %w", err)
	}

	return nil
}

// resolveExecutionTarget determines the execution target for a rig.
// Priority: explicit override > rig settings > K8s auto-detect > "local".
// When running inside a K8s pod (KUBERNETES_SERVICE_HOST is set), defaults
// to "k8s" instead of "local" so agents spawn as pods, not sessions.
func resolveExecutionTarget(rigPath, override string) config.ExecutionTarget {
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

// spawnPolecatForK8sCMD creates an agent bead for a K8s polecat without creating
// a local worktree or session. The K8s controller watches for agent beads
// with agent_state=spawning and execution_target:k8s label, then creates pods.
func spawnPolecatForK8sCMD(townRoot, rigName string, r *rig.Rig, opts SlingSpawnOptions) (*SpawnedPolecatInfo, error) {
	// Allocate polecat name. Try the full polecat Manager first (works when
	// the rig has a full local clone with .beads/). Fall back to a simple
	// name pool when the rig was created via gt rig register (K8s-only, no clone).
	var polecatName string
	rigBeadsDir := beads.ResolveBeadsDir(r.Path)
	if _, err := os.Stat(rigBeadsDir); err == nil {
		// Full rig: use polecat Manager for proper pool + reconciliation.
		polecatGit := git.NewGit(r.Path)
		polecatMgr := polecat.NewManager(r, polecatGit)
		name, err := polecatMgr.AllocateName()
		if err != nil {
			return nil, fmt.Errorf("allocating polecat name: %w", err)
		}
		polecatName = name
	} else {
		// K8s-only rig (gt rig register): use lightweight name pool.
		pool := polecat.NewNamePool(r.Path, r.Name)
		_ = pool.Load()
		name, err := pool.Allocate()
		if err != nil {
			return nil, fmt.Errorf("allocating polecat name: %w", err)
		}
		_ = pool.Save()
		polecatName = name
	}
	fmt.Printf("Allocated polecat: %s (K8s)\n", polecatName)

	// Create or reopen agent bead with spawning state and hook_bead set atomically.
	// Always use townRoot for the beads client — it has daemon connection via
	// .beads/config.yaml. Using the rig's local .beads/ would bypass the daemon
	// and hit a stale local SQLite (missing columns like auto_close).
	// The daemon handles prefix-based routing to the correct database.
	prefix := beads.GetPrefixForRig(townRoot, rigName)
	agentBeadID := beads.PolecatBeadIDWithPrefix(prefix, rigName, polecatName)
	beadsClient := beads.New(townRoot)
	_, err := beadsClient.CreateOrReopenAgentBead(agentBeadID, agentBeadID, &beads.AgentFields{
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

	return &SpawnedPolecatInfo{
		RigName:     rigName,
		PolecatName: polecatName,
		ClonePath:   "", // No local worktree for K8s polecats
		K8sSpawn:    true,
	}, nil
}

// notifyWitnessRateLimit sends a rate limit notification to the rig's witness.
// This is best-effort and failures are silently ignored.
func notifyWitnessRateLimit(rigName, polecatName, account string) {
	// Find workspace
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return
	}

	// Send mail to witness using gt mail send
	subject := fmt.Sprintf("RATE_LIMITED polecat:%s", polecatName)
	body := fmt.Sprintf("Account: %s\nSource: spawn\nPolecat: %s\nRig: %s\n",
		account, polecatName, rigName)

	witnessAddr := fmt.Sprintf("%s/witness", rigName)
	cmd := exec.Command("gt", "mail", "send", witnessAddr, "-s", subject, "-m", body)
	cmd.Dir = townRoot
	_ = cmd.Run() // Best effort - ignore errors
}
