package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/mail"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/util"
	"github.com/steveyegge/gastown/internal/workspace"
)

var (
	postflightRig         string
	postflightArchiveMail bool
	postflightDryRun      bool
	postflightJSON        bool
)

var postflightCmd = &cobra.Command{
	Use:     "postflight",
	GroupID: GroupWorkspace,
	Short:   "Run post-batch workspace cleanup",
	Long: `Run cleanup after batch work completes.

Postflight performs:
  1. Archive old mail (with --archive-mail)
  2. Clean up stale integration branches
  3. Clean up orphaned processes
  4. Sync beads (bd export)
  5. Report on workspace state

Use --rig to scope to a specific rig.
Use --archive-mail to archive read messages from all inboxes.
Use --dry-run to show what would be done without taking action.

Examples:
  gt postflight                     # Basic cleanup
  gt postflight --archive-mail      # Also archive old mail
  gt postflight --rig green         # Scope to specific rig
  gt postflight --dry-run           # Show what would happen
  gt postflight --json              # JSON output for scripting`,
	RunE: runPostflight,
}

func init() {
	postflightCmd.Flags().StringVar(&postflightRig, "rig", "", "Scope to specific rig only")
	postflightCmd.Flags().BoolVar(&postflightArchiveMail, "archive-mail", false, "Archive read messages from inboxes")
	postflightCmd.Flags().BoolVar(&postflightDryRun, "dry-run", false, "Show what would be done without taking action")
	postflightCmd.Flags().BoolVar(&postflightJSON, "json", false, "Output as JSON")

	rootCmd.AddCommand(postflightCmd)
}

// PostflightReport contains the results of all postflight actions.
type PostflightReport struct {
	MailArchived    int      `json:"mail_archived"`
	BranchesCleaned int     `json:"branches_cleaned"`
	OrphansCleaned  int     `json:"orphans_cleaned"`
	BeadsSynced     bool    `json:"beads_synced"`
	Warnings        []string `json:"warnings"`
	Errors          []string `json:"errors"`
	DryRun          bool    `json:"dry_run"`
}

func runPostflight(_ *cobra.Command, _ []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	report := &PostflightReport{DryRun: postflightDryRun}

	if !postflightJSON {
		fmt.Printf("\n%s Running postflight cleanup...\n\n", style.Bold.Render("🛬"))
	}

	// 1. Archive mail if requested
	if postflightArchiveMail {
		archiveMail(townRoot, report, postflightDryRun)
	}

	// 2. Clean stale integration branches
	cleanStaleBranches(townRoot, report, postflightDryRun)

	// 3. Clean orphaned processes
	cleanOrphans(report, postflightDryRun)

	// 4. Sync beads
	if !postflightDryRun {
		postflightSyncBeads(townRoot, report)
	}

	// Output
	if postflightJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	printPostflightReport(report)
	return nil
}

func archiveMail(townRoot string, report *PostflightReport, dryRun bool) {
	mailRouter := mail.NewRouter(townRoot)

	// Collect all agent addresses
	addresses := []string{"mayor/", "deacon/", "overseer"}

	rigsConfig, err := loadRigsConfigBeadsFirst(townRoot)
	if err == nil {
		g := git.NewGit(townRoot)
		mgr := rig.NewManager(townRoot, rigsConfig, g)
		rigs, err := mgr.DiscoverRigs()
		if err == nil {
			for _, r := range rigs {
				if postflightRig != "" && r.Name != postflightRig {
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

	var totalArchived int
	for _, addr := range addresses {
		mailbox, err := mailRouter.GetMailbox(addr)
		if err != nil {
			continue
		}
		messages, err := mailbox.List()
		if err != nil {
			continue
		}

		for _, msg := range messages {
			if msg.Read {
				if dryRun {
					totalArchived++
					continue
				}
				if err := mailbox.Archive(msg.ID); err == nil {
					totalArchived++
				}
			}
		}
	}

	report.MailArchived = totalArchived
}

func cleanStaleBranches(townRoot string, report *PostflightReport, dryRun bool) {
	rigsConfig, err := loadRigsConfigBeadsFirst(townRoot)
	if err != nil {
		return
	}

	g := git.NewGit(townRoot)
	mgr := rig.NewManager(townRoot, rigsConfig, g)
	rigs, err := mgr.DiscoverRigs()
	if err != nil {
		report.Warnings = append(report.Warnings, fmt.Sprintf("discovering rigs: %v", err))
		return
	}

	var totalCleaned int
	for _, r := range rigs {
		if postflightRig != "" && r.Name != postflightRig {
			continue
		}

		// Find merged branches in the rig
		branchCmd := exec.Command("git", "branch", "--merged", "main")
		branchCmd.Dir = r.Path
		out, err := branchCmd.Output()
		if err != nil {
			continue
		}

		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			branch := strings.TrimSpace(line)
			// Skip empty, current branch marker, and protected branches
			if branch == "" || strings.HasPrefix(branch, "*") {
				continue
			}
			if branch == "main" || branch == "master" {
				continue
			}
			// Only clean integration branches (beads-sync, polecat work branches)
			if !strings.HasPrefix(branch, "beads-sync") &&
				!strings.HasPrefix(branch, "gt-") &&
				!strings.HasPrefix(branch, "polecat-") {
				continue
			}

			if dryRun {
				totalCleaned++
				report.Warnings = append(report.Warnings, fmt.Sprintf("would delete merged branch: %s/%s", r.Name, branch))
				continue
			}

			deleteCmd := exec.Command("git", "branch", "-d", branch)
			deleteCmd.Dir = r.Path
			if err := deleteCmd.Run(); err == nil {
				totalCleaned++
			}
		}
	}

	report.BranchesCleaned = totalCleaned
}

func cleanOrphans(report *PostflightReport, dryRun bool) {
	zombies, err := util.FindZombieClaudeProcesses()
	if err != nil {
		report.Warnings = append(report.Warnings, fmt.Sprintf("orphan detection failed: %v", err))
		return
	}

	if len(zombies) == 0 {
		return
	}

	if dryRun {
		report.Warnings = append(report.Warnings, fmt.Sprintf("%d orphaned process(es) found (dry-run)", len(zombies)))
		return
	}

	results, err := util.CleanupZombieClaudeProcesses()
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("orphan cleanup: %v", err))
		return
	}

	for _, r := range results {
		if r.Signal == "SIGTERM" || r.Signal == "SIGKILL" {
			report.OrphansCleaned++
		}
	}
}

func postflightSyncBeads(townRoot string, report *PostflightReport) {
	bdPath, err := exec.LookPath("bd")
	if err != nil {
		report.Warnings = append(report.Warnings, "bd not found in PATH, skipping beads sync")
		return
	}

	cmd := exec.Command(bdPath, "export")
	cmd.Dir = townRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		report.Warnings = append(report.Warnings, fmt.Sprintf("bd export: %s", strings.TrimSpace(string(out))))
		return
	}

	report.BeadsSynced = true
}

func printPostflightReport(report *PostflightReport) {
	// Mail archival
	if postflightArchiveMail {
		if report.MailArchived == 0 {
			fmt.Printf("  %s No read messages to archive\n", style.Success.Render("✓"))
		} else if report.DryRun {
			fmt.Printf("  %s Would archive %d read message(s)\n", style.Dim.Render("ℹ"), report.MailArchived)
		} else {
			fmt.Printf("  %s Archived %d read message(s)\n", style.Success.Render("✓"), report.MailArchived)
		}
	}

	// Branch cleanup
	if report.BranchesCleaned == 0 {
		fmt.Printf("  %s No stale integration branches\n", style.Success.Render("✓"))
	} else if report.DryRun {
		fmt.Printf("  %s Would delete %d merged branch(es)\n", style.Dim.Render("ℹ"), report.BranchesCleaned)
	} else {
		fmt.Printf("  %s Cleaned %d merged branch(es)\n", style.Success.Render("✓"), report.BranchesCleaned)
	}

	// Orphan cleanup
	if report.OrphansCleaned > 0 {
		fmt.Printf("  %s Cleaned %d orphaned process(es)\n", style.Success.Render("✓"), report.OrphansCleaned)
	} else {
		fmt.Printf("  %s No orphaned processes\n", style.Success.Render("✓"))
	}

	// Beads sync
	if report.BeadsSynced {
		fmt.Printf("  %s Beads exported to JSONL\n", style.Success.Render("✓"))
	} else if !report.DryRun {
		fmt.Printf("  %s Beads export skipped\n", style.Dim.Render("ℹ"))
	}

	// Errors
	for _, e := range report.Errors {
		fmt.Printf("  %s %s\n", style.Error.Render("✗"), e)
	}

	// Summary
	fmt.Println()
	errorCount := len(report.Errors)
	if errorCount > 0 {
		fmt.Printf("%s Postflight complete: %d error(s)\n", style.Error.Render("✗"), errorCount)
	} else if report.DryRun {
		fmt.Printf("%s Postflight dry run complete\n", style.Dim.Render("ℹ"))
	} else {
		fmt.Printf("%s Postflight complete: workspace clean\n", style.Success.Render("✓"))
	}
}

