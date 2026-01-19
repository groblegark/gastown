package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var blockPRWorkflowCmd = &cobra.Command{
	Use:    "block-pr-workflow",
	Hidden: true, // Internal command for Claude Code hooks
	Short:  "Block PR workflow operations (hook helper)",
	Long: `Block PR workflow operations in Gas Town.

This command is called by Claude Code PreToolUse hooks to enforce the
"no PRs" policy. Gas Town workers push directly to main - PRs add friction
that breaks the autonomous execution model.

Exit codes:
  0 - Operation allowed (not in a restricted context)
  2 - Operation BLOCKED (hook will prevent tool execution)

The hook configuration in .claude/settings.json:
  {
    "PreToolUse": [{
      "matcher": "Bash(gh pr create*)",
      "hooks": [{"type": "command", "command": "gt block-pr-workflow --reason pr-create"}]
    }]
  }`,
	RunE: runBlockPRWorkflow,
}

var blockPRReason string

func init() {
	blockPRWorkflowCmd.Flags().StringVar(&blockPRReason, "reason", "", "Reason for the block check (pr-create, feature-branch)")
	rootCmd.AddCommand(blockPRWorkflowCmd)
}

func runBlockPRWorkflow(cmd *cobra.Command, args []string) error {
	// Check if we're in a Gas Town agent context
	// These env vars indicate we're running as a managed agent
	isPolecat := os.Getenv("GT_POLECAT") != ""
	isCrew := os.Getenv("GT_CREW") != ""
	isWitness := os.Getenv("GT_WITNESS") != ""
	isRefinery := os.Getenv("GT_REFINERY") != ""
	isMayor := os.Getenv("GT_MAYOR") != ""
	isDeacon := os.Getenv("GT_DEACON") != ""

	// Also check if we're in a crew worktree by path
	cwd, _ := os.Getwd()
	inCrewWorktree := strings.Contains(cwd, "/crew/")
	inPolecatWorktree := strings.Contains(cwd, "/polecats/")

	isGasTownAgent := isPolecat || isCrew || isWitness || isRefinery || isMayor || isDeacon || inCrewWorktree || inPolecatWorktree

	if !isGasTownAgent {
		// Not in a Gas Town managed context - allow the operation
		// This lets humans use PRs if they want
		return nil
	}

	// We're in a Gas Town context - block PR operations
	switch blockPRReason {
	case "pr-create":
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "╔══════════════════════════════════════════════════════════════════╗")
		fmt.Fprintln(os.Stderr, "║  ❌ PR CREATION BLOCKED                                          ║")
		fmt.Fprintln(os.Stderr, "╠══════════════════════════════════════════════════════════════════╣")
		fmt.Fprintln(os.Stderr, "║  Gas Town workers push directly to main. PRs are forbidden.     ║")
		fmt.Fprintln(os.Stderr, "║                                                                  ║")
		fmt.Fprintln(os.Stderr, "║  Instead of:  gh pr create ...                                   ║")
		fmt.Fprintln(os.Stderr, "║  Do this:     git push origin main                               ║")
		fmt.Fprintln(os.Stderr, "║                                                                  ║")
		fmt.Fprintln(os.Stderr, "║  Why? PRs add friction that breaks autonomous execution.        ║")
		fmt.Fprintln(os.Stderr, "║  See: ~/gt/docs/PRIMING.md (GUPP principle)                     ║")
		fmt.Fprintln(os.Stderr, "╚══════════════════════════════════════════════════════════════════╝")
		fmt.Fprintln(os.Stderr, "")
		os.Exit(2) // Exit 2 = BLOCK in Claude Code hooks

	case "feature-branch":
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "╔══════════════════════════════════════════════════════════════════╗")
		fmt.Fprintln(os.Stderr, "║  ⚠️  FEATURE BRANCH BLOCKED                                      ║")
		fmt.Fprintln(os.Stderr, "╠══════════════════════════════════════════════════════════════════╣")
		fmt.Fprintln(os.Stderr, "║  Gas Town workers commit directly to main. No feature branches. ║")
		fmt.Fprintln(os.Stderr, "║                                                                  ║")
		fmt.Fprintln(os.Stderr, "║  Instead of:  git checkout -b feature/...                        ║")
		fmt.Fprintln(os.Stderr, "║  Do this:     git add . && git commit && git push origin main   ║")
		fmt.Fprintln(os.Stderr, "║                                                                  ║")
		fmt.Fprintln(os.Stderr, "║  Why? Feature branches lead to PRs. We push directly to main.  ║")
		fmt.Fprintln(os.Stderr, "╚══════════════════════════════════════════════════════════════════╝")
		fmt.Fprintln(os.Stderr, "")
		os.Exit(2)

	default:
		// Unknown reason but we're in Gas Town context - block conservatively
		fmt.Fprintf(os.Stderr, "❌ Operation blocked by Gas Town policy (reason: %s)\n", blockPRReason)
		fmt.Fprintln(os.Stderr, "Gas Town workers push directly to main. See ~/gt/docs/PRIMING.md")
		os.Exit(2)
	}

	return nil
}
