package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/plugin"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

// Plugin command flags
var (
	pluginListJSON     bool
	pluginShowJSON     bool
	pluginStatusJSON   bool
	pluginRunForce     bool
	pluginRunDryRun    bool
	pluginHistoryJSON  bool
	pluginHistoryLimit int
)

var pluginCmd = &cobra.Command{
	Use:     "plugin",
	GroupID: GroupConfig,
	Short:   "Plugin management",
	Long: `Manage plugins that run during Deacon patrol cycles.

Plugins are periodic automation tasks defined by plugin.md files with TOML frontmatter.

PLUGIN LOCATIONS:
  ~/gt/plugins/           Town-level plugins (universal, apply everywhere)
  <rig>/plugins/          Rig-level plugins (project-specific)

GATE TYPES:
  cooldown    Run if enough time has passed (e.g., 1h)
  cron        Run on a schedule (e.g., "0 9 * * *")
  condition   Run if a check command returns exit 0
  event       Run on events (e.g., startup)
  manual      Never auto-run, trigger explicitly

Examples:
  gt plugin list                    # List all discovered plugins
  gt plugin show <name>             # Show plugin details
  gt plugin list --json             # JSON output`,
	RunE: requireSubcommand,
}

var pluginListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all discovered plugins",
	Long: `List all plugins from town and rig plugin directories.

Plugins are discovered from:
  - ~/gt/plugins/ (town-level)
  - <rig>/plugins/ for each registered rig

When a plugin exists at both levels, the rig-level version takes precedence.

Examples:
  gt plugin list              # Human-readable output
  gt plugin list --json       # JSON output for scripting`,
	RunE: runPluginList,
}

var pluginShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show plugin details",
	Long: `Show detailed information about a plugin.

Displays the plugin's configuration, gate settings, and instructions.

Examples:
  gt plugin show rebuild-gt
  gt plugin show rebuild-gt --json`,
	Args: cobra.ExactArgs(1),
	RunE: runPluginShow,
}

var pluginRunCmd = &cobra.Command{
	Use:   "run <name>",
	Short: "Manually trigger plugin execution",
	Long: `Manually trigger a plugin to run.

By default, checks if the gate would allow execution and informs you
if it wouldn't. Use --force to bypass gate checks.

Examples:
  gt plugin run rebuild-gt              # Run if gate allows
  gt plugin run rebuild-gt --force      # Bypass gate check
  gt plugin run rebuild-gt --dry-run    # Show what would happen`,
	Args: cobra.ExactArgs(1),
	RunE: runPluginRun,
}

var pluginHistoryCmd = &cobra.Command{
	Use:   "history <name>",
	Short: "Show plugin execution history",
	Long: `Show recent execution history for a plugin.

Queries ephemeral beads (wisps) that record plugin runs.

Examples:
  gt plugin history rebuild-gt
  gt plugin history rebuild-gt --json
  gt plugin history rebuild-gt --limit 20`,
	Args: cobra.ExactArgs(1),
	RunE: runPluginHistory,
}

var pluginStatusCmd = &cobra.Command{
	Use:   "status <name>",
	Short: "Show plugin operational status",
	Long: `Show the operational status of a plugin including gate state and run history.

Displays whether the plugin's gate is open, last run result, and
recent execution statistics.

Examples:
  gt plugin status rebuild-gt
  gt plugin status rebuild-gt --json`,
	Args: cobra.ExactArgs(1),
	RunE: runPluginStatus,
}

func init() {
	// List subcommand flags
	pluginListCmd.Flags().BoolVar(&pluginListJSON, "json", false, "Output as JSON")

	// Show subcommand flags
	pluginShowCmd.Flags().BoolVar(&pluginShowJSON, "json", false, "Output as JSON")

	// Status subcommand flags
	pluginStatusCmd.Flags().BoolVar(&pluginStatusJSON, "json", false, "Output as JSON")

	// Run subcommand flags
	pluginRunCmd.Flags().BoolVar(&pluginRunForce, "force", false, "Bypass gate check")
	pluginRunCmd.Flags().BoolVar(&pluginRunDryRun, "dry-run", false, "Show what would happen without executing")

	// History subcommand flags
	pluginHistoryCmd.Flags().BoolVar(&pluginHistoryJSON, "json", false, "Output as JSON")
	pluginHistoryCmd.Flags().IntVar(&pluginHistoryLimit, "limit", 10, "Maximum number of runs to show")

	// Add subcommands
	pluginCmd.AddCommand(pluginListCmd)
	pluginCmd.AddCommand(pluginShowCmd)
	pluginCmd.AddCommand(pluginStatusCmd)
	pluginCmd.AddCommand(pluginRunCmd)
	pluginCmd.AddCommand(pluginHistoryCmd)

	rootCmd.AddCommand(pluginCmd)
}

// getPluginScanner creates a scanner with town root and all rig names.
func getPluginScanner() (*plugin.Scanner, string, error) {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return nil, "", fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	// Load rigs config to get rig names
	rigsConfig, err := loadRigsConfigBeadsFirst(townRoot)
	if err != nil {
		rigsConfig = &config.RigsConfig{Rigs: make(map[string]config.RigEntry)}
	}

	// Extract rig names
	rigNames := make([]string, 0, len(rigsConfig.Rigs))
	for name := range rigsConfig.Rigs {
		rigNames = append(rigNames, name)
	}
	sort.Strings(rigNames)

	scanner := plugin.NewScanner(townRoot, rigNames)
	return scanner, townRoot, nil
}

func runPluginList(cmd *cobra.Command, args []string) error {
	scanner, townRoot, err := getPluginScanner()
	if err != nil {
		return err
	}

	plugins, err := scanner.DiscoverAll()
	if err != nil {
		return fmt.Errorf("discovering plugins: %w", err)
	}

	// Sort plugins by name
	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].Name < plugins[j].Name
	})

	if pluginListJSON {
		return outputPluginListJSON(plugins)
	}

	return outputPluginListText(plugins, townRoot)
}

func outputPluginListJSON(plugins []*plugin.Plugin) error {
	summaries := make([]plugin.PluginSummary, len(plugins))
	for i, p := range plugins {
		summaries[i] = p.Summary()
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(summaries)
}

func outputPluginListText(plugins []*plugin.Plugin, townRoot string) error {
	if len(plugins) == 0 {
		fmt.Printf("%s No plugins discovered\n", style.Dim.Render("○"))
		fmt.Printf("\n  Plugin directories:\n")
		fmt.Printf("    %s/plugins/\n", townRoot)
		fmt.Printf("\n  Create a plugin by adding a directory with plugin.md\n")
		return nil
	}

	fmt.Printf("%s Discovered %d plugin(s)\n\n", style.Success.Render("●"), len(plugins))

	// Group by location
	townPlugins := make([]*plugin.Plugin, 0)
	rigPlugins := make(map[string][]*plugin.Plugin)

	for _, p := range plugins {
		if p.Location == plugin.LocationTown {
			townPlugins = append(townPlugins, p)
		} else {
			rigPlugins[p.RigName] = append(rigPlugins[p.RigName], p)
		}
	}

	// Print town-level plugins
	if len(townPlugins) > 0 {
		fmt.Printf("  %s\n", style.Bold.Render("Town-level plugins:"))
		for _, p := range townPlugins {
			printPluginSummary(p)
		}
		fmt.Println()
	}

	// Print rig-level plugins by rig
	rigNames := make([]string, 0, len(rigPlugins))
	for name := range rigPlugins {
		rigNames = append(rigNames, name)
	}
	sort.Strings(rigNames)

	for _, rigName := range rigNames {
		fmt.Printf("  %s\n", style.Bold.Render(fmt.Sprintf("Rig %s:", rigName)))
		for _, p := range rigPlugins[rigName] {
			printPluginSummary(p)
		}
		fmt.Println()
	}

	return nil
}

func printPluginSummary(p *plugin.Plugin) {
	gateType := "manual"
	if p.Gate != nil && p.Gate.Type != "" {
		gateType = string(p.Gate.Type)
	}

	desc := p.Description
	if len(desc) > 50 {
		desc = desc[:47] + "..."
	}

	fmt.Printf("    %s %s\n", style.Bold.Render(p.Name), style.Dim.Render(fmt.Sprintf("[%s]", gateType)))
	if desc != "" {
		fmt.Printf("      %s\n", style.Dim.Render(desc))
	}
}

func runPluginShow(cmd *cobra.Command, args []string) error {
	name := args[0]

	scanner, _, err := getPluginScanner()
	if err != nil {
		return err
	}

	p, err := scanner.GetPlugin(name)
	if err != nil {
		return err
	}

	if pluginShowJSON {
		return outputPluginShowJSON(p)
	}

	return outputPluginShowText(p)
}

func outputPluginShowJSON(p *plugin.Plugin) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}

func outputPluginShowText(p *plugin.Plugin) error {
	fmt.Printf("%s %s\n", style.Bold.Render("Plugin:"), p.Name)
	fmt.Printf("%s %s\n", style.Bold.Render("Path:"), p.Path)

	if p.Description != "" {
		fmt.Printf("%s %s\n", style.Bold.Render("Description:"), p.Description)
	}

	// Location
	locStr := string(p.Location)
	if p.RigName != "" {
		locStr = fmt.Sprintf("%s (%s)", p.Location, p.RigName)
	}
	fmt.Printf("%s %s\n", style.Bold.Render("Location:"), locStr)

	fmt.Printf("%s %d\n", style.Bold.Render("Version:"), p.Version)

	// Gate
	fmt.Println()
	fmt.Printf("%s\n", style.Bold.Render("Gate:"))
	if p.Gate != nil {
		fmt.Printf("  Type: %s\n", p.Gate.Type)
		if p.Gate.Duration != "" {
			fmt.Printf("  Duration: %s\n", p.Gate.Duration)
		}
		if p.Gate.Schedule != "" {
			fmt.Printf("  Schedule: %s\n", p.Gate.Schedule)
		}
		if p.Gate.Check != "" {
			fmt.Printf("  Check: %s\n", p.Gate.Check)
		}
		if p.Gate.On != "" {
			fmt.Printf("  On: %s\n", p.Gate.On)
		}
	} else {
		fmt.Printf("  Type: manual (no gate section)\n")
	}

	// Tracking
	if p.Tracking != nil {
		fmt.Println()
		fmt.Printf("%s\n", style.Bold.Render("Tracking:"))
		if len(p.Tracking.Labels) > 0 {
			fmt.Printf("  Labels: %s\n", strings.Join(p.Tracking.Labels, ", "))
		}
		fmt.Printf("  Digest: %v\n", p.Tracking.Digest)
	}

	// Execution
	if p.Execution != nil {
		fmt.Println()
		fmt.Printf("%s\n", style.Bold.Render("Execution:"))
		if p.Execution.Timeout != "" {
			fmt.Printf("  Timeout: %s\n", p.Execution.Timeout)
		}
		fmt.Printf("  Notify on failure: %v\n", p.Execution.NotifyOnFailure)
		if p.Execution.Severity != "" {
			fmt.Printf("  Severity: %s\n", p.Execution.Severity)
		}
	}

	// Instructions preview
	if p.Instructions != "" {
		fmt.Println()
		fmt.Printf("%s\n", style.Bold.Render("Instructions:"))
		lines := strings.Split(p.Instructions, "\n")
		preview := lines
		if len(lines) > 10 {
			preview = lines[:10]
		}
		for _, line := range preview {
			fmt.Printf("  %s\n", line)
		}
		if len(lines) > 10 {
			fmt.Printf("  %s\n", style.Dim.Render(fmt.Sprintf("... (%d more lines)", len(lines)-10)))
		}
	}

	return nil
}

func runPluginRun(cmd *cobra.Command, args []string) error {
	name := args[0]

	scanner, townRoot, err := getPluginScanner()
	if err != nil {
		return err
	}

	p, err := scanner.GetPlugin(name)
	if err != nil {
		return err
	}

	// Check gate status for cooldown gates
	gateOpen := true
	gateReason := ""
	if p.Gate != nil && p.Gate.Type == plugin.GateCooldown && !pluginRunForce {
		recorder := plugin.NewRecorder(townRoot)
		duration := p.Gate.Duration
		if duration == "" {
			duration = "1h" // default
		}
		count, err := recorder.CountRunsSince(p.Name, duration)
		if err != nil {
			// Log warning but continue
			fmt.Fprintf(os.Stderr, "Warning: checking gate status: %v\n", err)
		} else if count > 0 {
			gateOpen = false
			gateReason = fmt.Sprintf("ran %d time(s) within %s cooldown", count, duration)
		}
	}

	if pluginRunDryRun {
		fmt.Printf("%s Dry run for plugin: %s\n", style.Bold.Render("Plugin:"), p.Name)
		fmt.Printf("%s %s\n", style.Bold.Render("Location:"), p.Path)
		if p.Gate != nil {
			fmt.Printf("%s %s\n", style.Bold.Render("Gate type:"), p.Gate.Type)
		}
		if !gateOpen {
			fmt.Printf("%s %s (use --force to override)\n", style.Warning.Render("Gate closed:"), gateReason)
		} else {
			fmt.Printf("%s Would execute plugin instructions\n", style.Success.Render("Gate open:"))
		}
		return nil
	}

	if !gateOpen && !pluginRunForce {
		fmt.Printf("%s Gate closed: %s\n", style.Warning.Render("⚠"), gateReason)
		fmt.Printf("  Use --force to bypass gate check\n")
		return nil
	}

	// Execute the plugin
	// For manual runs, we print the instructions for the agent/user to execute
	// Automatic execution via dogs is handled by gt-n08ix.2
	fmt.Printf("%s Running plugin: %s\n", style.Success.Render("●"), p.Name)
	if pluginRunForce && !gateOpen {
		fmt.Printf("  %s\n", style.Dim.Render("(gate bypassed with --force)"))
	}
	fmt.Println()
	fmt.Printf("%s\n", style.Bold.Render("Instructions:"))
	fmt.Println(p.Instructions)

	// Record the run
	recorder := plugin.NewRecorder(townRoot)
	beadID, err := recorder.RecordRun(plugin.PluginRunRecord{
		PluginName: p.Name,
		RigName:    p.RigName,
		Result:     plugin.ResultSuccess, // Manual runs are marked success
		Body:       "Manual run via gt plugin run",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to record run: %v\n", err)
	} else {
		fmt.Printf("\n%s Recorded run: %s\n", style.Dim.Render("●"), beadID)
	}

	return nil
}

func runPluginHistory(cmd *cobra.Command, args []string) error {
	name := args[0]

	_, townRoot, err := getPluginScanner()
	if err != nil {
		return err
	}

	recorder := plugin.NewRecorder(townRoot)
	runs, err := recorder.GetRunsSince(name, "")
	if err != nil {
		return fmt.Errorf("querying history: %w", err)
	}

	if runs == nil {
		runs = []*plugin.PluginRunBead{}
	}

	// Apply limit
	if pluginHistoryLimit > 0 && len(runs) > pluginHistoryLimit {
		runs = runs[:pluginHistoryLimit]
	}

	if pluginHistoryJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(runs)
	}

	if len(runs) == 0 {
		fmt.Printf("%s No execution history for plugin: %s\n", style.Dim.Render("○"), name)
		return nil
	}

	fmt.Printf("%s Execution history for %s (%d runs)\n\n", style.Success.Render("●"), name, len(runs))

	for _, run := range runs {
		resultStyle := style.Success
		resultIcon := "✓"
		if run.Result == plugin.ResultFailure {
			resultStyle = style.Error
			resultIcon = "✗"
		} else if run.Result == plugin.ResultSkipped {
			resultStyle = style.Dim
			resultIcon = "○"
		}

		fmt.Printf("  %s %s  %s\n",
			resultStyle.Render(resultIcon),
			run.CreatedAt.Format("2006-01-02 15:04"),
			style.Dim.Render(run.ID))
	}

	return nil
}

// PluginStatusOutput is the JSON output for gt plugin status.
type PluginStatusOutput struct {
	Name     string `json:"name"`
	Location string `json:"location"`
	RigName  string `json:"rig_name,omitempty"`
	GateType string `json:"gate_type"`
	GateOpen bool   `json:"gate_open"`
	GateInfo string `json:"gate_info,omitempty"`
	LastRun  *struct {
		ID        string    `json:"id"`
		Result    string    `json:"result"`
		Timestamp time.Time `json:"timestamp"`
		Age       string    `json:"age"`
	} `json:"last_run,omitempty"`
	Stats struct {
		Total    int `json:"total_24h"`
		Success  int `json:"success_24h"`
		Failures int `json:"failures_24h"`
		Skipped  int `json:"skipped_24h"`
	} `json:"stats"`
}

func runPluginStatus(cmd *cobra.Command, args []string) error {
	name := args[0]

	scanner, townRoot, err := getPluginScanner()
	if err != nil {
		return err
	}

	p, err := scanner.GetPlugin(name)
	if err != nil {
		return err
	}

	recorder := plugin.NewRecorder(townRoot)

	// Get last run
	lastRun, err := recorder.GetLastRun(p.Name)
	if err != nil {
		lastRun = nil // Non-fatal
	}

	// Get 24h stats
	recentRuns, err := recorder.GetRunsSince(p.Name, "24h")
	if err != nil {
		recentRuns = nil // Non-fatal
	}

	// Evaluate gate status
	gateType := "manual"
	gateOpen := true
	gateInfo := ""
	if p.Gate != nil && p.Gate.Type != "" {
		gateType = string(p.Gate.Type)
	}
	if p.Gate != nil && p.Gate.Type == plugin.GateCooldown {
		duration := p.Gate.Duration
		if duration == "" {
			duration = "1h"
		}
		count, err := recorder.CountRunsSince(p.Name, duration)
		if err == nil && count > 0 {
			gateOpen = false
			gateInfo = fmt.Sprintf("ran %d time(s) within %s cooldown", count, duration)
		} else if err == nil {
			gateInfo = fmt.Sprintf("cooldown %s (ready)", duration)
		}
	} else if p.Gate != nil && p.Gate.Type == plugin.GateManual {
		gateInfo = "manual trigger only"
	} else if p.Gate != nil && p.Gate.Type == plugin.GateCron {
		gateInfo = fmt.Sprintf("schedule: %s", p.Gate.Schedule)
	} else if p.Gate != nil && p.Gate.Type == plugin.GateEvent {
		gateInfo = fmt.Sprintf("on: %s", p.Gate.On)
	} else if p.Gate != nil && p.Gate.Type == plugin.GateCondition {
		gateInfo = fmt.Sprintf("check: %s", p.Gate.Check)
	}

	// Count stats
	successes, failures, skipped := 0, 0, 0
	for _, run := range recentRuns {
		switch run.Result {
		case plugin.ResultSuccess:
			successes++
		case plugin.ResultFailure:
			failures++
		case plugin.ResultSkipped:
			skipped++
		}
	}

	if pluginStatusJSON {
		output := PluginStatusOutput{
			Name:     p.Name,
			Location: string(p.Location),
			RigName:  p.RigName,
			GateType: gateType,
			GateOpen: gateOpen,
			GateInfo: gateInfo,
		}
		output.Stats.Total = len(recentRuns)
		output.Stats.Success = successes
		output.Stats.Failures = failures
		output.Stats.Skipped = skipped
		if lastRun != nil {
			output.LastRun = &struct {
				ID        string    `json:"id"`
				Result    string    `json:"result"`
				Timestamp time.Time `json:"timestamp"`
				Age       string    `json:"age"`
			}{
				ID:        lastRun.ID,
				Result:    string(lastRun.Result),
				Timestamp: lastRun.CreatedAt,
				Age:       formatDuration(time.Since(lastRun.CreatedAt)),
			}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(output)
	}

	// Human-readable output
	fmt.Printf("%s %s\n", style.Bold.Render("Plugin:"), p.Name)

	locStr := string(p.Location)
	if p.RigName != "" {
		locStr = fmt.Sprintf("%s (%s)", p.Location, p.RigName)
	}
	fmt.Printf("%s %s\n", style.Bold.Render("Location:"), locStr)

	// Gate status
	fmt.Println()
	gateIcon := style.Success.Render("●")
	gateLabel := "open"
	if !gateOpen {
		gateIcon = style.Warning.Render("○")
		gateLabel = "closed"
	}
	fmt.Printf("%s %s %s %s\n", style.Bold.Render("Gate:"), gateIcon, gateLabel, style.Dim.Render(fmt.Sprintf("[%s]", gateType)))
	if gateInfo != "" {
		fmt.Printf("  %s\n", style.Dim.Render(gateInfo))
	}

	// Last run
	fmt.Println()
	fmt.Printf("%s\n", style.Bold.Render("Last run:"))
	if lastRun != nil {
		resultIcon := style.Success.Render("✓")
		if lastRun.Result == plugin.ResultFailure {
			resultIcon = style.Error.Render("✗")
		} else if lastRun.Result == plugin.ResultSkipped {
			resultIcon = style.Dim.Render("○")
		}
		age := formatDuration(time.Since(lastRun.CreatedAt))
		fmt.Printf("  %s %s (%s ago)  %s\n", resultIcon, string(lastRun.Result), age, style.Dim.Render(lastRun.ID))
	} else {
		fmt.Printf("  %s\n", style.Dim.Render("No runs recorded"))
	}

	// 24h stats
	fmt.Println()
	fmt.Printf("%s (last 24h)\n", style.Bold.Render("Stats:"))
	if len(recentRuns) == 0 {
		fmt.Printf("  %s\n", style.Dim.Render("No runs in last 24h"))
	} else {
		fmt.Printf("  Total: %d", len(recentRuns))
		if successes > 0 {
			fmt.Printf("  %s", style.Success.Render(fmt.Sprintf("✓ %d", successes)))
		}
		if failures > 0 {
			fmt.Printf("  %s", style.Error.Render(fmt.Sprintf("✗ %d", failures)))
		}
		if skipped > 0 {
			fmt.Printf("  %s", style.Dim.Render(fmt.Sprintf("○ %d", skipped)))
		}
		fmt.Println()
	}

	return nil
}

// formatDuration is defined in session.go
