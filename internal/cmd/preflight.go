package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/mail"
	"github.com/steveyegge/gastown/internal/registry"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/util"
	"github.com/steveyegge/gastown/internal/workspace"
)

var (
	preflightRig    string
	preflightDryRun bool
	preflightJSON   bool
)

var preflightCmd = &cobra.Command{
	Use:     "preflight",
	GroupID: GroupWorkspace,
	Short:   "Run pre-batch workspace checks",
	Long: `Run checks before starting batch work to ensure a clean workspace.

Preflight verifies:
  1. Git state is clean (no uncommitted changes in town root)
  2. Town root is on main branch
  3. No stale mail in agent inboxes
  4. No stuck or stopped agents
  5. No orphaned Claude processes
  6. Rig health (polecats, refinery, witness present)

Use --rig to check a specific rig. Without --rig, checks all rigs.
Use --dry-run to show what would be cleaned without taking action.

Examples:
  gt preflight              # Check all rigs
  gt preflight --rig green  # Check specific rig
  gt preflight --dry-run    # Show issues without fixing
  gt preflight --json       # JSON output for scripting`,
	RunE: runPreflight,
}

func init() {
	preflightCmd.Flags().StringVar(&preflightRig, "rig", "", "Check specific rig only")
	preflightCmd.Flags().BoolVar(&preflightDryRun, "dry-run", false, "Show what would be done without taking action")
	preflightCmd.Flags().BoolVar(&preflightJSON, "json", false, "Output as JSON")

	rootCmd.AddCommand(preflightCmd)
}

// PreflightReport contains the results of all preflight checks.
type PreflightReport struct {
	GitClean       bool              `json:"git_clean"`
	OnMainBranch   bool              `json:"on_main_branch"`
	CurrentBranch  string            `json:"current_branch"`
	MailCleaned    int               `json:"mail_cleaned"`
	StaleMailCount int               `json:"stale_mail_count"`
	StuckWorkers   []string          `json:"stuck_workers"`
	StoppedAgents  []string          `json:"stopped_agents"`
	OrphanCount    int               `json:"orphan_count"`
	OrphansCleaned int               `json:"orphans_cleaned"`
	RigHealth      []RigHealthStatus `json:"rig_health"`
	Warnings       []string          `json:"warnings"`
	Errors         []string          `json:"errors"`
	DryRun         bool              `json:"dry_run"`
}

// RigHealthStatus reports health for a single rig.
type RigHealthStatus struct {
	Name       string `json:"name"`
	Healthy    bool   `json:"healthy"`
	HasWitness bool   `json:"has_witness"`
	HasRefinery bool  `json:"has_refinery"`
	Polecats   int    `json:"polecats"`
	Issues     []string `json:"issues,omitempty"`
}

func runPreflight(_ *cobra.Command, _ []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	report := &PreflightReport{DryRun: preflightDryRun}

	if !preflightJSON {
		fmt.Printf("\n%s Running preflight checks...\n\n", style.Bold.Render("✈"))
	}

	// 1. Check git state
	checkGitState(townRoot, report)

	// 2. Check for stale mail
	checkStaleMail(townRoot, report)

	// 3. Check agent health (stuck/stopped workers)
	checkAgentHealth(townRoot, report)

	// 4. Check for orphaned processes
	checkOrphanedProcesses(report, preflightDryRun)

	// 5. Check rig health
	checkRigHealth(townRoot, report)

	// 6. Run bd export to ensure JSONL is current
	if !preflightDryRun {
		syncBeads(townRoot, report)
	}

	// Output
	if preflightJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	printPreflightReport(report)
	return nil
}

func checkGitState(townRoot string, report *PreflightReport) {
	// Check current branch
	branchCmd := exec.Command("git", "branch", "--show-current")
	branchCmd.Dir = townRoot
	out, err := branchCmd.Output()
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("git branch check failed: %v", err))
		return
	}

	report.CurrentBranch = strings.TrimSpace(string(out))
	report.OnMainBranch = report.CurrentBranch == "main" || report.CurrentBranch == "master"

	if !report.OnMainBranch {
		report.Warnings = append(report.Warnings, fmt.Sprintf("town root on branch '%s' (expected main)", report.CurrentBranch))
	}

	// Check for uncommitted changes
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = townRoot
	statusOut, err := statusCmd.Output()
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("git status check failed: %v", err))
		return
	}

	report.GitClean = len(strings.TrimSpace(string(statusOut))) == 0
	if !report.GitClean {
		lines := strings.Split(strings.TrimSpace(string(statusOut)), "\n")
		report.Warnings = append(report.Warnings, fmt.Sprintf("git working tree not clean (%d modified files)", len(lines)))
	}
}

func checkStaleMail(townRoot string, report *PreflightReport) {
	mailRouter := mail.NewRouter(townRoot)

	// Check all known agent addresses for unread mail
	addresses := []string{"mayor/", "deacon/", "overseer"}

	// Also check rig-level agents
	rigsConfig, err := loadRigsConfigBeadsFirst(townRoot)
	if err == nil {
		g := git.NewGit(townRoot)
		mgr := rig.NewManager(townRoot, rigsConfig, g)
		rigs, err := mgr.DiscoverRigs()
		if err == nil {
			for _, r := range rigs {
				if preflightRig != "" && r.Name != preflightRig {
					continue
				}
				if r.HasWitness {
					addresses = append(addresses, r.Name+"/witness")
				}
				if r.HasRefinery {
					addresses = append(addresses, r.Name+"/refinery")
				}
				for _, p := range r.Polecats {
					addresses = append(addresses, r.Name+"/"+p)
				}
			}
		}
	}

	var totalStale int
	for _, addr := range addresses {
		mailbox, err := mailRouter.GetMailbox(addr)
		if err != nil {
			continue
		}
		total, unread, err := mailbox.Count()
		if err != nil {
			continue
		}
		_ = total
		if unread > 0 {
			totalStale += unread
		}
	}

	report.StaleMailCount = totalStale
	if totalStale > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("%d unread messages across agent inboxes", totalStale))
	}
}

func checkAgentHealth(townRoot string, report *PreflightReport) {
	// Pre-fetch all agent beads
	allAgentBeads := make(map[string]*beads.Issue)
	var mu sync.Mutex

	// Town-level agents
	townBeadsPath := beads.GetTownBeadsPath(townRoot)
	townBeadsClient := beads.New(townBeadsPath)
	townAgentBeads, _ := townBeadsClient.ListAgentBeads()
	for id, issue := range townAgentBeads {
		allAgentBeads[id] = issue
	}

	// Rig-level agents
	rigsConfig, err := loadRigsConfigBeadsFirst(townRoot)
	if err == nil {
		g := git.NewGit(townRoot)
		mgr := rig.NewManager(townRoot, rigsConfig, g)
		rigs, _ := mgr.DiscoverRigs()
		var wg sync.WaitGroup
		for _, r := range rigs {
			if preflightRig != "" && r.Name != preflightRig {
				continue
			}
			wg.Add(1)
			go func(r *rig.Rig) {
				defer wg.Done()
				rigBeadsPath := filepath.Join(r.Path, "mayor", "rig")
				rigBeads := beads.New(rigBeadsPath)
				rigAgentBeads, _ := rigBeads.ListAgentBeads()
				mu.Lock()
				for id, issue := range rigAgentBeads {
					allAgentBeads[id] = issue
				}
				mu.Unlock()
			}(r)
		}
		wg.Wait()
	}

	// Discover sessions for liveness check
	allSessions := make(map[string]bool)
	lister := &mapAgentLister{agents: allAgentBeads}
	reg := registry.New(lister, nil)
	ctx := context.Background()
	if sessions, err := reg.DiscoverAll(ctx, registry.DiscoverOpts{CheckLiveness: true}); err == nil {
		for _, s := range sessions {
			if s.Alive {
				allSessions[s.TmuxSession] = true
				allSessions[s.ID] = true
			}
		}
	}

	// Check each agent
	for id, issue := range allAgentBeads {
		state := issue.AgentState
		if state == "" {
			fields := beads.ParseAgentFields(issue.Description)
			if fields != nil {
				state = fields.AgentState
			}
		}

		if state == "stuck" {
			report.StuckWorkers = append(report.StuckWorkers, id)
		}

		// Check if expected-running agents are actually stopped
		sessionName := agentBeadIDToSessionName(id)
		if sessionName != "" && !allSessions[sessionName] && !allSessions[id] {
			// Only report non-polecat agents as "stopped" (polecats are expected to come and go)
			if !strings.Contains(id, "-polecat-") && !isPolecatSession(id) {
				report.StoppedAgents = append(report.StoppedAgents, id)
			}
		}
	}

	if len(report.StuckWorkers) > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("%d stuck worker(s): %s", len(report.StuckWorkers), strings.Join(report.StuckWorkers, ", ")))
	}
	if len(report.StoppedAgents) > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("%d expected agent(s) stopped: %s", len(report.StoppedAgents), strings.Join(report.StoppedAgents, ", ")))
	}
}

// agentBeadIDToSessionName converts an agent bead ID to the expected tmux session name.
func agentBeadIDToSessionName(beadID string) string {
	// Town-level agents: hq-mayor → gt-mayor, hq-deacon → gt-deacon
	if strings.HasPrefix(beadID, "hq-") {
		rest := strings.TrimPrefix(beadID, "hq-")
		return "gt-" + rest
	}
	// Rig-level agents: prefix-rig-role-name → gt-rig-name
	return ""
}

// isPolecatSession checks if a bead ID represents a polecat agent.
func isPolecatSession(beadID string) bool {
	return strings.Contains(beadID, "polecat")
}

func checkOrphanedProcesses(report *PreflightReport, dryRun bool) {
	zombies, err := util.FindZombieClaudeProcesses()
	if err != nil {
		report.Warnings = append(report.Warnings, fmt.Sprintf("orphan check failed: %v", err))
		return
	}

	report.OrphanCount = len(zombies)
	if len(zombies) == 0 {
		return
	}

	report.Warnings = append(report.Warnings, fmt.Sprintf("%d orphaned Claude process(es) found", len(zombies)))

	if !dryRun {
		results, err := util.CleanupZombieClaudeProcesses()
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("orphan cleanup failed: %v", err))
			return
		}
		for _, r := range results {
			if r.Signal == "SIGTERM" || r.Signal == "SIGKILL" {
				report.OrphansCleaned++
			}
		}
	}
}

func checkRigHealth(townRoot string, report *PreflightReport) {
	rigsConfig, err := loadRigsConfigBeadsFirst(townRoot)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("loading rigs config: %v", err))
		return
	}

	g := git.NewGit(townRoot)
	mgr := rig.NewManager(townRoot, rigsConfig, g)
	rigs, err := mgr.DiscoverRigs()
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("discovering rigs: %v", err))
		return
	}

	for _, r := range rigs {
		if preflightRig != "" && r.Name != preflightRig {
			continue
		}

		health := RigHealthStatus{
			Name:        r.Name,
			HasWitness:  r.HasWitness,
			HasRefinery: r.HasRefinery,
			Polecats:    len(r.Polecats),
			Healthy:     true,
		}

		if !r.HasWitness {
			health.Issues = append(health.Issues, "no witness configured")
			health.Healthy = false
		}
		if !r.HasRefinery {
			health.Issues = append(health.Issues, "no refinery configured")
			health.Healthy = false
		}
		if len(r.Polecats) == 0 {
			health.Issues = append(health.Issues, "no polecats configured")
			health.Healthy = false
		}

		// Check rig git state
		rigGitCmd := exec.Command("git", "status", "--porcelain")
		rigGitCmd.Dir = r.Path
		rigOut, err := rigGitCmd.Output()
		if err == nil && len(strings.TrimSpace(string(rigOut))) > 0 {
			health.Issues = append(health.Issues, "rig has uncommitted changes")
		}

		report.RigHealth = append(report.RigHealth, health)

		if !health.Healthy {
			for _, issue := range health.Issues {
				report.Warnings = append(report.Warnings, fmt.Sprintf("rig %s: %s", r.Name, issue))
			}
		}
	}
}

func syncBeads(townRoot string, report *PreflightReport) {
	// Run bd export in town root to sync JSONL
	bdPath, err := exec.LookPath("bd")
	if err != nil {
		report.Warnings = append(report.Warnings, "bd not found in PATH, skipping beads sync")
		return
	}

	cmd := exec.Command(bdPath, "export")
	cmd.Dir = townRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		// Don't fail on export errors - it's a best-effort sync
		report.Warnings = append(report.Warnings, fmt.Sprintf("bd export: %s", strings.TrimSpace(string(out))))
	}
}

func printPreflightReport(report *PreflightReport) {
	// Git state
	if report.OnMainBranch && report.GitClean {
		fmt.Printf("  %s Git state clean (on %s)\n", style.Success.Render("✓"), report.CurrentBranch)
	} else {
		if !report.OnMainBranch {
			fmt.Printf("  %s On branch '%s' (expected main)\n", style.Warning.Render("⚠"), report.CurrentBranch)
		}
		if !report.GitClean {
			fmt.Printf("  %s Working tree has uncommitted changes\n", style.Warning.Render("⚠"))
		}
	}

	// Mail
	if report.StaleMailCount == 0 {
		fmt.Printf("  %s No stale mail in inboxes\n", style.Success.Render("✓"))
	} else {
		fmt.Printf("  %s %d unread messages across inboxes\n", style.Warning.Render("⚠"), report.StaleMailCount)
	}

	// Agent health
	if len(report.StuckWorkers) == 0 && len(report.StoppedAgents) == 0 {
		fmt.Printf("  %s All agents healthy\n", style.Success.Render("✓"))
	} else {
		if len(report.StuckWorkers) > 0 {
			fmt.Printf("  %s %d stuck worker(s)\n", style.Warning.Render("⚠"), len(report.StuckWorkers))
			for _, w := range report.StuckWorkers {
				fmt.Printf("      %s\n", w)
			}
		}
		if len(report.StoppedAgents) > 0 {
			fmt.Printf("  %s %d expected agent(s) stopped\n", style.Warning.Render("⚠"), len(report.StoppedAgents))
			for _, a := range report.StoppedAgents {
				fmt.Printf("      %s\n", a)
			}
		}
	}

	// Orphans
	if report.OrphanCount == 0 {
		fmt.Printf("  %s No orphaned processes\n", style.Success.Render("✓"))
	} else if report.DryRun {
		fmt.Printf("  %s %d orphaned process(es) (dry-run, not cleaned)\n", style.Warning.Render("⚠"), report.OrphanCount)
	} else {
		fmt.Printf("  %s Cleaned %d/%d orphaned process(es)\n", style.Success.Render("✓"), report.OrphansCleaned, report.OrphanCount)
	}

	// Rig health
	for _, rh := range report.RigHealth {
		if rh.Healthy {
			fmt.Printf("  %s Rig %s healthy (%d polecats, witness %s, refinery %s)\n",
				style.Success.Render("✓"), rh.Name,
				rh.Polecats,
				boolIcon(rh.HasWitness), boolIcon(rh.HasRefinery))
		} else {
			fmt.Printf("  %s Rig %s has issues:\n", style.Warning.Render("⚠"), rh.Name)
			for _, issue := range rh.Issues {
				fmt.Printf("      %s\n", issue)
			}
		}
	}

	// Errors
	for _, e := range report.Errors {
		fmt.Printf("  %s %s\n", style.Error.Render("✗"), e)
	}

	// Summary
	fmt.Println()
	warningCount := len(report.Warnings)
	errorCount := len(report.Errors)
	if errorCount > 0 {
		fmt.Printf("%s Preflight complete: %d error(s), %d warning(s)\n",
			style.Error.Render("✗"), errorCount, warningCount)
	} else if warningCount > 0 {
		fmt.Printf("%s Preflight complete: %d warning(s)\n",
			style.Warning.Render("⚠"), warningCount)
	} else {
		fmt.Printf("%s Preflight complete: workspace ready\n", style.Success.Render("✓"))
	}
}

func boolIcon(b bool) string {
	if b {
		return style.Success.Render("✓")
	}
	return style.Error.Render("✗")
}
