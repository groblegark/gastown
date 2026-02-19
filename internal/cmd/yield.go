package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/bdcmd"
)

var yieldCmd = &cobra.Command{
	Use:     "yield",
	GroupID: GroupWork,
	Short:   "Yield and wait for events (checkpoint)",
	Long: `Block the calling process until a meaningful event arrives for this agent.

This is the checkpoint/yield command: agents call "gt yield" when they have
no immediate work, and the process blocks until something happens — an inbox
message, a decision response, or a timeout.

When the event arrives, the payload is printed to stdout and the command exits 0.
On timeout, exits 1.

This wraps "bd done" (the beads yield command). In the future, "bd done" will be
renamed to "bd yield" and "bd done" will handle session teardown. Use "gt yield"
now for the checkpoint/wait pattern and "gt done" for session teardown.

Examples:
  gt yield                             # Wait for any event (30m timeout)
  gt yield --timeout=600               # Wait 10 minutes
  gt yield --on=inbox                  # Only wait for inbox messages
  gt yield --on=decision               # Only wait for decision responses`,
	DisableFlagParsing: true, // Pass all flags through to bd done
	RunE:               runYield,
}

func init() {
	rootCmd.AddCommand(yieldCmd)
}

func runYield(cmd *cobra.Command, args []string) error {
	// Handle --help since DisableFlagParsing bypasses Cobra's help handling
	if helped, err := checkHelpFlag(cmd, args); helped || err != nil {
		return err
	}

	// Build bd done command with all args passed through.
	// gt yield wraps bd done (the event-waiting command).
	bdArgs := append([]string{"done"}, args...)

	// If --agent is not provided and BD_ACTOR is set, bd done uses BD_ACTOR
	// automatically. No need to inject it here.

	// If --timeout is not provided, check GT_YIELD_TIMEOUT env var
	hasTimeout := false
	for _, arg := range args {
		if arg == "--timeout" || len(arg) > 10 && arg[:10] == "--timeout=" {
			hasTimeout = true
			break
		}
	}
	if !hasTimeout {
		if envTimeout := os.Getenv("GT_YIELD_TIMEOUT"); envTimeout != "" {
			if _, err := strconv.Atoi(envTimeout); err == nil {
				bdArgs = append(bdArgs, fmt.Sprintf("--timeout=%s", envTimeout))
			}
		}
	}

	bdCmd := bdcmd.Command(bdArgs...)
	bdCmd.Stdin = os.Stdin
	bdCmd.Stdout = os.Stdout
	bdCmd.Stderr = os.Stderr
	return bdCmd.Run()
}
