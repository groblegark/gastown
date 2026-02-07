package sling

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/claude"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/events"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/ratelimit"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/tmux"
	"github.com/steveyegge/gastown/internal/workspace"
)

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

	// Check for rate limit backoff before spawning
	tracker := ratelimit.NewTracker(r.Path)
	if loadErr := tracker.Load(); loadErr == nil {
		if tracker.ShouldDefer() {
			waitTime := tracker.TimeUntilReady()
			return nil, fmt.Errorf("%w: rate limit backoff active for %s, retry in %v",
				polecat.ErrRateLimited, rigName, waitTime.Round(1e9))
		}
	}

	polecatGit := git.NewGit(r.Path)
	t := tmux.NewTmux()
	polecatMgr := polecat.NewManager(r, polecatGit, t)

	polecatName, err := polecatMgr.AllocateName()
	if err != nil {
		return nil, fmt.Errorf("allocating polecat name: %w", err)
	}
	fmt.Printf("Allocated polecat: %s\n", polecatName)

	cleanupOrphanPolecatState(rigName, polecatName, r.Path, t)

	existingPolecat, err := polecatMgr.Get(polecatName)

	addOpts := polecat.AddOptions{
		HookBead: opts.HookBead,
	}

	if err == nil {
		if !opts.Force {
			pGit := git.NewGit(existingPolecat.ClonePath)
			workStatus, checkErr := pGit.CheckUncommittedWork()
			if checkErr == nil && !workStatus.Clean() {
				return nil, fmt.Errorf("polecat '%s' has uncommitted work: %s\nUse --force to proceed anyway",
					polecatName, workStatus.String())
			}
		}

		if existingPolecat.Branch != "" {
			bd := beads.New(r.Path)
			mr, mrErr := bd.FindMRForBranch(existingPolecat.Branch)
			if mrErr == nil && mr != nil {
				return nil, fmt.Errorf("polecat '%s' has unmerged MR: %s\n"+
					"Wait for MR to merge before respawning, or use:\n"+
					"  gt polecat nuke --force %s/%s  # to abandon the MR",
					polecatName, mr.ID, rigName, polecatName)
			}
		}

		fmt.Printf("Repairing stale polecat %s with fresh worktree...\n", polecatName)
		if _, err = polecatMgr.RepairWorktreeWithOptions(polecatName, opts.Force, addOpts); err != nil {
			return nil, fmt.Errorf("repairing stale polecat: %w", err)
		}
	} else if err == polecat.ErrPolecatNotFound {
		fmt.Printf("Creating polecat %s...\n", polecatName)
		if _, err = polecatMgr.AddWithOptions(polecatName, addOpts); err != nil {
			return nil, fmt.Errorf("creating polecat: %w", err)
		}
	} else {
		return nil, fmt.Errorf("getting polecat: %w", err)
	}

	polecatObj, err := polecatMgr.Get(polecatName)
	if err != nil {
		return nil, fmt.Errorf("getting polecat after creation: %w", err)
	}

	if err := verifyWorktreeExists(polecatObj.ClonePath); err != nil {
		_ = polecatMgr.Remove(polecatName, true)
		return nil, fmt.Errorf("worktree verification failed for %s: %w\nHint: try 'gt polecat nuke %s/%s --force' to clean up",
			polecatName, err, rigName, polecatName)
	}

	if opts.HookBead != "" {
		if err := verifyAndSetHookBead(townRoot, rigName, polecatName, opts.HookBead); err != nil {
			fmt.Printf("Warning: could not verify hook_bead: %v\n", err)
		}
	}

	accountsPath := constants.MayorAccountsPath(townRoot)
	resolvedAccount, err := config.ResolveAccount(accountsPath, opts.Account)
	if err != nil {
		return nil, fmt.Errorf("resolving account: %w", err)
	}

	if err := config.ValidateAccountAuth(resolvedAccount); err != nil {
		return nil, err
	}

	var claudeConfigDir, accountHandle, authToken, baseURL string
	if resolvedAccount != nil {
		claudeConfigDir = resolvedAccount.ConfigDir
		accountHandle = resolvedAccount.Handle
		authToken = resolvedAccount.AuthToken
		baseURL = resolvedAccount.BaseURL
	}

	if accountHandle != "" {
		fmt.Printf("Using account: %s\n", accountHandle)
	}

	// Materialize MCP config from config beads before session start
	polecatHomeDir := filepath.Dir(polecatObj.ClonePath)
	townBeadsDir := beads.ResolveBeadsDir(townRoot)
	beadsForMCP := beads.NewWithBeadsDir(polecatHomeDir, townBeadsDir)
	townName := filepath.Base(townRoot)
	mcpLayers, _ := beadsForMCP.ResolveConfigMetadata(beads.ConfigCategoryMCP, townName, rigName, "polecat", polecatName)
	if len(mcpLayers) > 0 {
		if err := claude.MaterializeMCPConfig(polecatHomeDir, mcpLayers); err != nil {
			fmt.Printf("Warning: could not materialize MCP config from beads: %v\n", err)
		}
	}

	polecatSessMgr := polecat.NewSessionManager(t, r)
	running, _ := polecatSessMgr.IsRunning(polecatName)

	if running {
		prefix := beads.GetPrefixForRig(townRoot, rigName)
		agentBeadID := beads.PolecatBeadIDWithPrefix(prefix, rigName, polecatName)
		beadsClient := beads.New(townRoot)
		if agentBead, showErr := beadsClient.Show(agentBeadID); showErr == nil {
			if agentBead.HookBead != "" && agentBead.HookBead != opts.HookBead {
				fmt.Printf("  Polecat %s has hooked work (%s), killing stale session...\n",
					polecatName, agentBead.HookBead)
				sessionName := polecatSessMgr.SessionName(polecatName)
				_ = t.KillSessionWithProcesses(sessionName)
				running = false
			}
		}
	}

	if !running {
		fmt.Printf("Starting session for %s/%s...\n", rigName, polecatName)
		startOpts := polecat.SessionStartOptions{
			RuntimeConfigDir: claudeConfigDir,
			AuthToken:        authToken,
			BaseURL:          baseURL,
		}
		if opts.Agent != "" {
			cmd, err := config.BuildPolecatStartupCommandWithAgentOverride(rigName, polecatName, r.Path, "", opts.Agent)
			if err != nil {
				return nil, err
			}
			startOpts.Command = cmd
		}
		if err := polecatSessMgr.Start(polecatName, startOpts); err != nil {
			if errors.Is(err, polecat.ErrRateLimited) {
				fmt.Printf("⚠️  Rate limit detected during spawn\n")
				notifyWitnessRateLimit(rigName, polecatName, accountHandle)
			}
			return nil, fmt.Errorf("starting session: %w", err)
		}
	}

	sessionName := polecatSessMgr.SessionName(polecatName)

	if err := verifySpawnedPolecat(polecatObj.ClonePath, sessionName, t); err != nil {
		return nil, fmt.Errorf("spawn verification failed for %s: %w", polecatName, err)
	}

	fmt.Printf("✓ Polecat %s spawned\n", polecatName)

	_ = events.LogFeed(events.TypeSpawn, "gt", events.SpawnPayload(rigName, polecatName))

	return &SpawnResult{
		RigName:     rigName,
		PolecatName: polecatName,
		ClonePath:   polecatObj.ClonePath,
		SessionName: sessionName,
		Pane:        "",
		Account:     opts.Account,
		Agent:       opts.Agent,
	}, nil
}

// WakeRigAgents wakes the witness for a rig after polecat dispatch.
func WakeRigAgents(rigName string) {
	bootCmd := exec.Command("gt", "rig", "boot", rigName)
	_ = bootCmd.Run()

	t := tmux.NewTmux()
	witnessSession := fmt.Sprintf("gt-%s-witness", rigName)
	_ = t.NudgeSession(witnessSession, "Polecat dispatched - check for work")
}

// OjSlingEnabled returns true when OJ dispatch is active.
func OjSlingEnabled() bool {
	return os.Getenv("GT_SLING_OJ") == "1"
}

func cleanupOrphanPolecatState(rigName, polecatName, rigPath string, tm *tmux.Tmux) {
	polecatDir := filepath.Join(rigPath, "polecats", polecatName)
	sessionName := fmt.Sprintf("gt-%s-%s", rigName, polecatName)

	if err := tm.KillSession(sessionName); err == nil {
		fmt.Printf("  Cleaned up orphan tmux session: %s\n", sessionName)
	}

	if entries, err := filepath.Glob(polecatDir + "/*"); err == nil && len(entries) == 0 {
		if rmErr := os.RemoveAll(polecatDir); rmErr == nil {
			fmt.Printf("  Cleaned up empty polecat directory: %s\n", polecatDir)
		}
	}

	repoGit := git.NewGit(rigPath)
	_ = repoGit.WorktreePrune()
}

func verifyWorktreeExists(clonePath string) error {
	info, err := os.Stat(clonePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("worktree directory does not exist: %s", clonePath)
		}
		return fmt.Errorf("checking worktree directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("worktree path is not a directory: %s", clonePath)
	}

	gitPath := filepath.Join(clonePath, ".git")
	_, err = os.Stat(gitPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("worktree missing .git file (not a valid git worktree): %s", clonePath)
		}
		return fmt.Errorf("checking .git: %w", err)
	}
	return nil
}

func verifySpawnedPolecat(clonePath, sessionName string, t *tmux.Tmux) error {
	gitPath := filepath.Join(clonePath, ".git")
	if _, err := os.Stat(gitPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("worktree disappeared: %s (missing .git)", clonePath)
		}
		return fmt.Errorf("checking worktree: %w", err)
	}

	hasSession, err := t.HasSession(sessionName)
	if err != nil {
		return fmt.Errorf("checking session: %w", err)
	}
	if !hasSession {
		return fmt.Errorf("session disappeared: %s", sessionName)
	}
	return nil
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

// GetSessionPane returns the pane identifier for a session's main pane.
func GetSessionPane(sessionName string) (string, error) {
	const maxRetries = 30
	const retryDelay = 100 * time.Millisecond

	var lastErr error
	for i := 0; i < maxRetries; i++ {
		cmd := exec.Command("tmux", "list-panes", "-t", sessionName, "-F", "#{pane_id}")
		out, err := cmd.Output()
		if err != nil {
			lastErr = err
			time.Sleep(retryDelay)
			continue
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) == 0 || lines[0] == "" {
			lastErr = fmt.Errorf("no panes found in session")
			time.Sleep(retryDelay)
			continue
		}
		return lines[0], nil
	}
	return "", fmt.Errorf("pane lookup failed after %d retries: %w", maxRetries, lastErr)
}
