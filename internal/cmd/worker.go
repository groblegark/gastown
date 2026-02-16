package cmd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"
)

// WorkerStatusReport is the structured payload sent to the refinery
// when a worker reports its progress.
type WorkerStatusReport struct {
	IssueID    string    `json:"issue_id"`
	Status     string    `json:"status"`              // started|progress|blocked|completed|failed
	Progress   int       `json:"progress,omitempty"`   // 0-100 for progress reports
	Reason     string    `json:"reason,omitempty"`     // for blocked/failed
	Message    string    `json:"message,omitempty"`    // optional detail
	ReportedAt time.Time `json:"reported_at"`
}

var workerMessage string

var workerCmd = &cobra.Command{
	Use:     "worker",
	GroupID: GroupAgents,
	Short:   "Report worker status to refinery",
	Long: `Report worker status from a polecat session to the refinery.

These commands send structured status updates as mail so the refinery
can track progress across all workers in the rig.

Examples:
  gt worker started beads-abc
  gt worker progress beads-abc 75 -m "tests passing"
  gt worker blocked beads-abc "waiting for API key"
  gt worker completed beads-abc
  gt worker failed beads-abc "build error in module X"`,
	RunE: requireSubcommand,
}

var workerStartedCmd = &cobra.Command{
	Use:   "started <issue-id>",
	Short: "Report work started on an issue",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendWorkerReport(args[0], "started", 0, "")
	},
}

var workerProgressCmd = &cobra.Command{
	Use:   "progress <issue-id> <0-100>",
	Short: "Report progress percentage",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		pct, err := strconv.Atoi(args[1])
		if err != nil || pct < 0 || pct > 100 {
			return fmt.Errorf("progress must be 0-100, got %q", args[1])
		}
		return sendWorkerReport(args[0], "progress", pct, "")
	},
}

var workerBlockedCmd = &cobra.Command{
	Use:   "blocked <issue-id> <reason>",
	Short: "Report blocked status with reason",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendWorkerReport(args[0], "blocked", 0, args[1])
	},
}

var workerCompletedCmd = &cobra.Command{
	Use:   "completed <issue-id>",
	Short: "Report task completion",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendWorkerReport(args[0], "completed", 100, "")
	},
}

var workerFailedCmd = &cobra.Command{
	Use:   "failed <issue-id> <reason>",
	Short: "Report task failure",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendWorkerReport(args[0], "failed", 0, args[1])
	},
}

func init() {
	rootCmd.AddCommand(workerCmd)

	// Add -m flag to all subcommands
	for _, sub := range []*cobra.Command{
		workerStartedCmd, workerProgressCmd, workerBlockedCmd,
		workerCompletedCmd, workerFailedCmd,
	} {
		sub.Flags().StringVarP(&workerMessage, "message", "m", "", "Optional detail message")
		workerCmd.AddCommand(sub)
	}
}

// sendWorkerReport builds a WorkerStatusReport and sends it as mail to the refinery.
func sendWorkerReport(issueID, status string, progress int, reason string) error {
	report := WorkerStatusReport{
		IssueID:    issueID,
		Status:     status,
		Progress:   progress,
		Reason:     reason,
		Message:    workerMessage,
		ReportedAt: time.Now(),
	}

	body, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("encoding report: %w", err)
	}

	subject := fmt.Sprintf("[%s] %s: %s", status, issueID, statusLabel(status, progress, reason))

	if err := sendMailDirect("refinery", subject, string(body)); err != nil {
		return fmt.Errorf("sending status report: %w", err)
	}

	fmt.Printf("Reported %s for %s\n", status, issueID)
	return nil
}

// statusLabel returns a human-readable label for the status.
func statusLabel(status string, progress int, reason string) string {
	switch status {
	case "started":
		return "work started"
	case "progress":
		return fmt.Sprintf("%d%% complete", progress)
	case "blocked":
		return reason
	case "completed":
		return "done"
	case "failed":
		return reason
	default:
		return status
	}
}
