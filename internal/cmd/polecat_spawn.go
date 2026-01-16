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
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/events"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/tmux"
	"github.com/steveyegge/gastown/internal/workspace"
)

// SpawnedPolecatInfo contains info about a spawned polecat session.
type SpawnedPolecatInfo struct {
	RigName     string // Rig name (e.g., "gastown")
	PolecatName string // Polecat name (e.g., "Toast")
	ClonePath   string // Path to polecat's git worktree
	SessionName string // Tmux session name (e.g., "gt-gastown-p-Toast")
	Pane        string // Tmux pane ID
}

// AgentID returns the agent identifier (e.g., "gastown/polecats/Toast")
func (s *SpawnedPolecatInfo) AgentID() string {
	return fmt.Sprintf("%s/polecats/%s", s.RigName, s.PolecatName)
}

// SlingSpawnOptions contains options for spawning a polecat via sling.
type SlingSpawnOptions struct {
	Force    bool   // Force spawn even if polecat has uncommitted work
	Account  string // Claude Code account handle to use
	Create   bool   // Create polecat if it doesn't exist (currently always true for sling)
	HookBead string // Bead ID to set as hook_bead at spawn time (atomic assignment)
	Agent    string // Agent override for this spawn (e.g., "gemini", "codex", "claude-haiku")
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

	// Load rig config
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

	// Get polecat manager (with tmux for session-aware allocation)
	polecatGit := git.NewGit(r.Path)
	t := tmux.NewTmux()
	polecatMgr := polecat.NewManager(r, polecatGit, t)

	// Allocate a new polecat name
	polecatName, err := polecatMgr.AllocateName()
	if err != nil {
		return nil, fmt.Errorf("allocating polecat name: %w", err)
	}
	fmt.Printf("Allocated polecat: %s\n", polecatName)

	// Check if polecat already exists (shouldn't happen - indicates stale state needing repair)
	existingPolecat, err := polecatMgr.Get(polecatName)

	// Build add options with hook_bead set atomically at spawn time
	addOpts := polecat.AddOptions{
		HookBead: opts.HookBead,
	}

	if err == nil {
		// Stale state: polecat exists despite fresh name allocation - repair it
		// Check for uncommitted work first
		if !opts.Force {
			pGit := git.NewGit(existingPolecat.ClonePath)
			workStatus, checkErr := pGit.CheckUncommittedWork()
			if checkErr == nil && !workStatus.Clean() {
				return nil, fmt.Errorf("polecat '%s' has uncommitted work: %s\nUse --force to proceed anyway",
					polecatName, workStatus.String())
			}
		}
		fmt.Printf("Repairing stale polecat %s with fresh worktree...\n", polecatName)
		if _, err = polecatMgr.RepairWorktreeWithOptions(polecatName, opts.Force, addOpts); err != nil {
			return nil, fmt.Errorf("repairing stale polecat: %w", err)
		}
	} else if err == polecat.ErrPolecatNotFound {
		// Create new polecat
		fmt.Printf("Creating polecat %s...\n", polecatName)
		if _, err = polecatMgr.AddWithOptions(polecatName, addOpts); err != nil {
			return nil, fmt.Errorf("creating polecat: %w", err)
		}
	} else {
		return nil, fmt.Errorf("getting polecat: %w", err)
	}

	// Get polecat object for path info
	polecatObj, err := polecatMgr.Get(polecatName)
	if err != nil {
		return nil, fmt.Errorf("getting polecat after creation: %w", err)
	}

	// Create agent bead for the polecat (ZFC: track agent lifecycle)
	// This ensures gt hook discovery works and hook_bead slot can be set
	if err := createPolecatAgentBead(townRoot, rigName, polecatName, opts.HookBead); err != nil {
		// Non-fatal: log warning but continue - polecat will still work via nudge
		fmt.Printf("%s Could not create agent bead for polecat: %v\n", style.Dim.Render("Warning:"), err)
	}

	// Resolve account for runtime config
	accountsPath := constants.MayorAccountsPath(townRoot)
	claudeConfigDir, accountHandle, err := config.ResolveAccountConfigDir(accountsPath, opts.Account)
	if err != nil {
		return nil, fmt.Errorf("resolving account: %w", err)
	}

	// Validate that the account has credentials before spawning
	// This prevents OAuth prompts from appearing in polecat sessions
	if err := config.ValidateAccountCredentials(claudeConfigDir, accountHandle); err != nil {
		return nil, err
	}

	if accountHandle != "" {
		fmt.Printf("Using account: %s\n", accountHandle)
	}

	// Start session (reuse tmux from manager)
	polecatSessMgr := polecat.NewSessionManager(t, r)

	// Check if already running
	running, _ := polecatSessMgr.IsRunning(polecatName)
	if !running {
		fmt.Printf("Starting session for %s/%s...\n", rigName, polecatName)
		startOpts := polecat.SessionStartOptions{
			RuntimeConfigDir: claudeConfigDir,
		}
		if opts.Agent != "" {
			cmd, err := config.BuildPolecatStartupCommandWithAgentOverride(rigName, polecatName, r.Path, "", opts.Agent)
			if err != nil {
				return nil, err
			}
			startOpts.Command = cmd
		}
		if err := polecatSessMgr.Start(polecatName, startOpts); err != nil {
			return nil, fmt.Errorf("starting session: %w", err)
		}
	}

	// Get session name and pane
	sessionName := polecatSessMgr.SessionName(polecatName)

	// Debug: verify session exists before pane lookup
	debug := os.Getenv("GT_DEBUG_SLING") != ""
	if debug {
		fmt.Fprintf(os.Stderr, "[sling-debug] SpawnPolecatForSling: session started, verifying session %q exists...\n", sessionName)
		hasCmd := exec.Command("tmux", "has-session", "-t", "="+sessionName)
		if hasErr := hasCmd.Run(); hasErr != nil {
			fmt.Fprintf(os.Stderr, "[sling-debug] WARNING: session %q does NOT exist after Start() returned!\n", sessionName)
			// Try to get more info about what happened
			listCmd := exec.Command("tmux", "list-sessions", "-F", "#{session_name}")
			if listOut, listErr := listCmd.Output(); listErr == nil {
				fmt.Fprintf(os.Stderr, "[sling-debug] Current sessions: %s\n", strings.TrimSpace(string(listOut)))
			}
		} else {
			fmt.Fprintf(os.Stderr, "[sling-debug] session %q exists, checking if pane is alive...\n", sessionName)
			// Check if Claude is running
			paneCmd := exec.Command("tmux", "list-panes", "-t", sessionName, "-F", "#{pane_current_command}")
			if paneOut, paneErr := paneCmd.Output(); paneErr == nil {
				fmt.Fprintf(os.Stderr, "[sling-debug] pane command: %s\n", strings.TrimSpace(string(paneOut)))
			} else {
				fmt.Fprintf(os.Stderr, "[sling-debug] list-panes failed: %v\n", paneErr)
			}
		}
	}

	pane, err := getSessionPane(sessionName)
	if err != nil {
		return nil, fmt.Errorf("getting pane for %s: %w", sessionName, err)
	}

	fmt.Printf("%s Polecat %s spawned\n", style.Bold.Render("✓"), polecatName)

	// Log spawn event to activity feed
	_ = events.LogFeed(events.TypeSpawn, "gt", events.SpawnPayload(rigName, polecatName))

	return &SpawnedPolecatInfo{
		RigName:     rigName,
		PolecatName: polecatName,
		ClonePath:   polecatObj.ClonePath,
		SessionName: sessionName,
		Pane:        pane,
	}, nil
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

	rigsConfigPath := filepath.Join(townRoot, "mayor", "rigs.json")
	rigsConfig, err := config.LoadRigsConfig(rigsConfigPath)
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

// createPolecatAgentBead creates an agent bead for a spawned polecat.
// This enables gt hook discovery and allows hook_bead slot to be set.
// Format: <prefix>-<rig>-polecat-<name> (e.g., gt-gastown-polecat-Toast)
// Agent beads are stored at rig-level per architecture.md.
func createPolecatAgentBead(townRoot, rigName, polecatName, hookBead string) error {
	// Load rig config to get the beads prefix
	rigPath := filepath.Join(townRoot, rigName)
	rigCfg, err := rig.LoadRigConfig(rigPath)
	prefix := "gt" // fallback
	if err == nil && rigCfg.Beads != nil && rigCfg.Beads.Prefix != "" {
		prefix = rigCfg.Beads.Prefix
	}

	// Build the agent bead ID using rig's prefix (architecture.md)
	agentBeadID := beads.PolecatBeadIDWithPrefix(prefix, rigName, polecatName)

	// Open rig-level beads database (redirect system handles routing)
	rigBeadsDir := beads.ResolveBeadsDir(rigPath)
	bd := beads.NewWithBeadsDir(rigPath, rigBeadsDir)

	// Define agent fields
	fields := &beads.AgentFields{
		RoleType:   "polecat",
		Rig:        rigName,
		AgentState: "idle",
		HookBead:   hookBead,
		RoleBead:   beads.RoleBeadIDTown("polecat"),
	}

	// Description for the agent bead
	desc := fmt.Sprintf("Agent bead for %s/%s polecat", rigName, polecatName)

	// Create or reopen the agent bead (handles tombstones from previous spawns)
	_, err = bd.CreateOrReopenAgentBead(agentBeadID, desc, fields)
	if err != nil {
		return fmt.Errorf("creating agent bead %s: %w", agentBeadID, err)
	}

	fmt.Printf("   ✓ Created agent bead: %s\n", agentBeadID)
	return nil
}
