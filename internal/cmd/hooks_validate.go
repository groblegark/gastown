package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/hooks"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

var (
	validateAll  bool
	validateJSON bool
)

var hooksValidateCmd = &cobra.Command{
	Use:     "test",
	Aliases: []string{"validate"},
	Short:   "Validate hook configuration",
	Long: `Validate Claude Code hook configuration.

Checks hooks in .claude/settings.json for common issues:
  - Commands reference executables that exist in PATH
  - Event types are recognized
  - Hook structure is well-formed

By default, validates hooks in the current worktree.
Use --all to validate all hooks across the entire workspace.

Examples:
  gt hooks test            # Validate current worktree hooks
  gt hooks test --all      # Validate all hooks in workspace
  gt hooks test --json     # JSON output`,
	RunE: runHooksValidate,
}

func init() {
	hooksCmd.AddCommand(hooksValidateCmd)
	hooksValidateCmd.Flags().BoolVarP(&validateAll, "all", "a", false, "Validate all hooks across the workspace")
	hooksValidateCmd.Flags().BoolVar(&validateJSON, "json", false, "Output as JSON")
}

// ValidationResult represents the result of validating a single hook.
type ValidationResult struct {
	Agent    string   `json:"agent"`
	Event    string   `json:"event"`
	Matcher  string   `json:"matcher,omitempty"`
	Command  string   `json:"command"`
	Issues   []string `json:"issues,omitempty"`
	Valid    bool     `json:"valid"`
}

// ValidationOutput is the JSON output structure.
type ValidationOutput struct {
	TownRoot string             `json:"town_root,omitempty"`
	Results  []ValidationResult `json:"results"`
	Summary  ValidationSummary  `json:"summary"`
}

// ValidationSummary summarizes the validation.
type ValidationSummary struct {
	Total   int `json:"total"`
	Valid   int `json:"valid"`
	Invalid int `json:"invalid"`
}

func runHooksValidate(cmd *cobra.Command, args []string) error {
	if validateAll {
		return validateAllHooks()
	}
	return validateCurrentHooks()
}

func validateCurrentHooks() error {
	settingsPath, err := findSettingsFile()
	if err != nil {
		return err
	}

	results, err := validateSettingsFile(settingsPath, "current")
	if err != nil {
		return err
	}

	if validateJSON {
		return outputValidationJSON("", results)
	}
	return outputValidationHuman("", results)
}

func validateAllHooks() error {
	townRoot, err := workspace.FindFromCwd()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	// Use the existing hook discovery to find all settings files
	hookInfos, err := discoverHooks(townRoot)
	if err != nil {
		return fmt.Errorf("discovering hooks: %w", err)
	}

	// Group by location and validate
	seen := make(map[string]bool)
	var allResults []ValidationResult

	for _, info := range hookInfos {
		if seen[info.Location] {
			continue
		}
		seen[info.Location] = true

		results, err := validateSettingsFile(info.Location, info.Agent)
		if err != nil {
			allResults = append(allResults, ValidationResult{
				Agent:   info.Agent,
				Event:   "N/A",
				Command: info.Location,
				Issues:  []string{fmt.Sprintf("parse error: %v", err)},
				Valid:   false,
			})
			continue
		}
		allResults = append(allResults, results...)
	}

	if validateJSON {
		return outputValidationJSON(townRoot, allResults)
	}
	return outputValidationHuman(townRoot, allResults)
}

func validateSettingsFile(path, agent string) ([]ValidationResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var settings struct {
		Hooks map[string][]hooks.HookEntry `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	var results []ValidationResult

	for eventStr, entries := range settings.Hooks {
		event := hooks.Event(eventStr)
		for _, entry := range entries {
			for _, hook := range entry.Hooks {
				result := ValidationResult{
					Agent:   agent,
					Event:   eventStr,
					Matcher: entry.Matcher,
					Command: hook.Command,
					Valid:   true,
				}

				// Validate event type
				if !event.IsValid() {
					result.Issues = append(result.Issues, fmt.Sprintf("unknown event type %q", eventStr))
					result.Valid = false
				}

				// Validate hook type
				if hook.Type != "command" {
					result.Issues = append(result.Issues, fmt.Sprintf("unknown hook type %q (expected \"command\")", hook.Type))
					result.Valid = false
				}

				// Validate command is not empty
				if hook.Command == "" {
					result.Issues = append(result.Issues, "empty command")
					result.Valid = false
					results = append(results, result)
					continue
				}

				// Check if the first executable in the command exists
				issues := validateCommand(hook.Command)
				if len(issues) > 0 {
					result.Issues = append(result.Issues, issues...)
					result.Valid = false
				}

				results = append(results, result)
			}
		}
	}

	return results, nil
}

// validateCommand checks if the command's executable is available.
func validateCommand(command string) []string {
	var issues []string

	// Strip common shell patterns to find the actual executable
	cmd := command

	// Remove env var exports at the start
	for strings.HasPrefix(cmd, "export ") {
		idx := strings.Index(cmd, "&&")
		if idx == -1 {
			break
		}
		cmd = strings.TrimSpace(cmd[idx+2:])
	}

	// Remove variable assignments
	cmd = strings.TrimSpace(cmd)
	for {
		if len(cmd) > 0 && cmd[0] == '_' || (len(cmd) > 0 && cmd[0] >= 'A' && cmd[0] <= 'Z') {
			eqIdx := strings.Index(cmd, "=")
			spIdx := strings.Index(cmd, " ")
			if eqIdx > 0 && (spIdx == -1 || eqIdx < spIdx) {
				// This looks like VAR=value, skip to next command after &&
				andIdx := strings.Index(cmd, "&&")
				if andIdx != -1 {
					cmd = strings.TrimSpace(cmd[andIdx+2:])
					continue
				}
			}
		}
		break
	}

	// Handle subshell patterns: (cmd || true)
	cmd = strings.TrimPrefix(cmd, "(")

	// Get the first word as the executable
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return issues
	}

	executable := parts[0]

	// Skip shell builtins and special cases
	builtins := map[string]bool{
		"echo": true, "printf": true, "cd": true, "export": true,
		"source": true, ".": true, "test": true, "[": true,
		"true": true, "false": true, "read": true, "eval": true,
	}
	if builtins[executable] {
		return issues
	}

	// Check if executable exists in PATH
	if _, err := exec.LookPath(executable); err != nil {
		// Don't flag as invalid — just warn. The hook might use PATH modifications.
		issues = append(issues, fmt.Sprintf("%q not found in current PATH (may work with PATH modifications in command)", executable))
	}

	return issues
}

func outputValidationJSON(townRoot string, results []ValidationResult) error {
	summary := ValidationSummary{Total: len(results)}
	for _, r := range results {
		if r.Valid {
			summary.Valid++
		} else {
			summary.Invalid++
		}
	}

	data, err := json.MarshalIndent(ValidationOutput{
		TownRoot: townRoot,
		Results:  results,
		Summary:  summary,
	}, "", "  ")
	if err != nil {
		return err
	}

	fmt.Println(string(data))
	return nil
}

func outputValidationHuman(townRoot string, results []ValidationResult) error {
	if len(results) == 0 {
		fmt.Println(style.Dim.Render("No hooks found to validate"))
		return nil
	}

	fmt.Printf("\n%s Hook Validation\n", style.Bold.Render("🔍"))
	if townRoot != "" {
		fmt.Printf("Scope: %s\n", style.Dim.Render(townRoot))
	}
	fmt.Println()

	valid, invalid := 0, 0
	// Group by agent
	byAgent := make(map[string][]ValidationResult)
	var agentOrder []string
	for _, r := range results {
		if _, seen := byAgent[r.Agent]; !seen {
			agentOrder = append(agentOrder, r.Agent)
		}
		byAgent[r.Agent] = append(byAgent[r.Agent], r)
	}

	for _, agent := range agentOrder {
		agentResults := byAgent[agent]
		fmt.Printf("  %s %s\n", style.Bold.Render("▸"), agent)

		for _, r := range agentResults {
			if r.Valid {
				valid++
				fmt.Printf("    %s %s %s\n",
					style.Success.Render("✓"),
					r.Event,
					style.Dim.Render(truncateCommand(r.Command, 50)))
			} else {
				invalid++
				fmt.Printf("    %s %s %s\n",
					style.Error.Render("✗"),
					r.Event,
					style.Dim.Render(truncateCommand(r.Command, 50)))
				for _, issue := range r.Issues {
					fmt.Printf("      %s %s\n", style.Warning.Render("→"), issue)
				}
			}
		}
		fmt.Println()
	}

	if invalid > 0 {
		fmt.Printf("%s %d valid, %d with issues\n", style.Warning.Render("Result:"), valid, invalid)
	} else {
		fmt.Printf("%s All %d hooks valid\n", style.Success.Render("Result:"), valid)
	}

	// Show path hint for fixing issues
	if invalid > 0 {
		fmt.Printf("\n%s\n", style.Dim.Render("Note: PATH warnings may be false positives if the hook modifies PATH before executing."))
	}

	return nil
}

// relativeSettingsPath returns a human-readable relative path.
func relativeSettingsPath(settingsPath string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return settingsPath
	}
	rel, err := filepath.Rel(home, settingsPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return settingsPath
	}
	return "~/" + rel
}
