package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/hooks"
	"github.com/steveyegge/gastown/internal/style"
)

var (
	fireTimeout int
	fireJSON    bool
)

var hooksFireCmd = &cobra.Command{
	Use:   "fire <event>",
	Short: "Manually fire a hook event for testing",
	Long: `Fire a Claude Code hook event manually.

Reads hooks from .claude/settings.json in the current directory and executes
all hooks registered for the given event. Useful for testing hook commands
without waiting for the actual Claude Code trigger.

Event types:
  SessionStart     - Session initialization
  PreCompact       - Before context compaction
  UserPromptSubmit - Before user prompt processing
  PreToolUse       - Before tool execution
  PostToolUse      - After tool execution
  Stop             - Session shutdown

Examples:
  gt hooks fire SessionStart            # Fire SessionStart hooks
  gt hooks fire Stop                    # Fire Stop hooks
  gt hooks fire PostToolUse --timeout 10 # Fire with 10s timeout
  gt hooks fire SessionStart --json     # JSON output`,
	Args: cobra.ExactArgs(1),
	RunE: runHooksFire,
}

func init() {
	hooksCmd.AddCommand(hooksFireCmd)
	hooksFireCmd.Flags().IntVar(&fireTimeout, "timeout", 30, "Timeout in seconds for each hook command")
	hooksFireCmd.Flags().BoolVar(&fireJSON, "json", false, "Output results as JSON")
}

// FireOutput is the JSON output structure.
type FireOutput struct {
	Event   string             `json:"event"`
	Results []hooks.HookResult `json:"results"`
	Summary FireSummary        `json:"summary"`
}

// FireSummary summarizes hook execution results.
type FireSummary struct {
	Total   int `json:"total"`
	Success int `json:"success"`
	Failed  int `json:"failed"`
}

func runHooksFire(cmd *cobra.Command, args []string) error {
	event := hooks.Event(args[0])
	if !event.IsValid() {
		return fmt.Errorf("unknown event %q; valid events: %v", args[0], hooks.AllEvents())
	}

	// Find settings file
	settingsPath, err := findSettingsFile()
	if err != nil {
		return err
	}

	timeout := time.Duration(fireTimeout) * time.Second
	runner := hooks.NewRunner(settingsPath, hooks.WithTimeout(timeout))

	ctx := context.Background()
	results, err := runner.Fire(ctx, event)
	if err != nil {
		return fmt.Errorf("firing %s: %w", event, err)
	}

	if len(results) == 0 {
		if fireJSON {
			data, _ := json.MarshalIndent(FireOutput{
				Event:   string(event),
				Results: []hooks.HookResult{},
				Summary: FireSummary{},
			}, "", "  ")
			fmt.Println(string(data))
		} else {
			fmt.Printf("%s No hooks registered for %s\n", style.Dim.Render("○"), event)
		}
		return nil
	}

	if fireJSON {
		return outputFireJSON(event, results)
	}
	return outputFireHuman(event, results)
}

func outputFireJSON(event hooks.Event, results []hooks.HookResult) error {
	summary := FireSummary{Total: len(results)}
	for _, r := range results {
		if r.Success {
			summary.Success++
		} else {
			summary.Failed++
		}
	}

	data, err := json.MarshalIndent(FireOutput{
		Event:   string(event),
		Results: results,
		Summary: summary,
	}, "", "  ")
	if err != nil {
		return err
	}

	fmt.Println(string(data))
	return nil
}

func outputFireHuman(event hooks.Event, results []hooks.HookResult) error {
	fmt.Printf("\n%s Firing %s\n\n", style.Bold.Render("⚡"), event)

	succeeded, failed := 0, 0
	for _, r := range results {
		icon := style.Success.Render("✓")
		if !r.Success {
			icon = style.Error.Render("✗")
			failed++
		} else {
			succeeded++
		}

		matcherStr := ""
		if r.Matcher != "" {
			matcherStr = fmt.Sprintf(" [%s]", r.Matcher)
		}

		fmt.Printf("  %s %s%s\n", icon, truncateCommand(r.Command, 70), style.Dim.Render(matcherStr))

		if r.ExitCode != 0 {
			fmt.Printf("    %s exit %d\n", style.Dim.Render("→"), r.ExitCode)
		}
		if r.Stderr != "" {
			fmt.Printf("    %s %s\n", style.Dim.Render("stderr:"), truncateCommand(r.Stderr, 60))
		}
		if r.Error != "" {
			fmt.Printf("    %s %s\n", style.Error.Render("error:"), r.Error)
		}
		if r.Stdout != "" {
			// Show first line of stdout
			lines := splitFirstLine(r.Stdout)
			fmt.Printf("    %s %s\n", style.Dim.Render("stdout:"), truncateCommand(lines, 60))
		}
	}

	fmt.Println()
	if failed > 0 {
		fmt.Printf("%s %d/%d hooks failed\n", style.Error.Render("Result:"), failed, len(results))
	} else {
		fmt.Printf("%s %d/%d hooks succeeded\n", style.Success.Render("Result:"), succeeded, len(results))
	}

	return nil
}

// findSettingsFile looks for .claude/settings.json starting from cwd.
func findSettingsFile() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting working directory: %w", err)
	}

	// Check current directory first
	path := filepath.Join(cwd, ".claude", "settings.json")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}

	// Walk up to find settings
	dir := cwd
	for {
		path = filepath.Join(dir, ".claude", "settings.json")
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("no .claude/settings.json found (searched from %s)", cwd)
}

func splitFirstLine(s string) string {
	for i, c := range s {
		if c == '\n' {
			return s[:i]
		}
	}
	return s
}
