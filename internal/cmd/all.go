package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

// Flags for the all command.
var (
	allAwakeOnly bool
	allForce     bool
	allJSON      bool
)

var allCmd = &cobra.Command{
	Use:     "all",
	GroupID: GroupAgents,
	Short:   "Batch operations across multiple polecats",
	Long: `Batch operations across multiple polecats.

Spec patterns:
  Toast              Specific polecat (infer rig from cwd)
  gastown/Toast      Specific rig/polecat
  gastown/*          All polecats in a rig
  *                  All polecats everywhere

Examples:
  gt all status
  gt all start gastown/*
  gt all stop --force
  gt all status --json`,
	RunE: requireSubcommand,
}

var allStartCmd = &cobra.Command{
	Use:   "start [specs...]",
	Short: "Start sessions for multiple polecats",
	Long: `Start sessions for multiple polecats.

With no specs, starts all polecats everywhere.
Use --awake-only to only start polecats that already have sessions running.

Examples:
  gt all start
  gt all start gastown/*
  gt all start gastown/Toast gastown/Furiosa`,
	RunE: runAllStart,
}

var allStopCmd = &cobra.Command{
	Use:   "stop [specs...]",
	Short: "Stop sessions for multiple polecats",
	Long: `Stop sessions for multiple polecats.

With no specs, stops all polecats everywhere.
Use --force to force-kill sessions.

Examples:
  gt all stop
  gt all stop gastown/*
  gt all stop --force`,
	RunE: runAllStop,
}

var allStatusCmd = &cobra.Command{
	Use:   "status [specs...]",
	Short: "Show status of multiple polecats",
	Long: `Show status of multiple polecats.

With no specs, shows all polecats everywhere.

Examples:
  gt all status
  gt all status gastown/*
  gt all status --json`,
	RunE: runAllStatus,
}

var allAttachCmd = &cobra.Command{
	Use:   "attach <spec>",
	Short: "Attach to a polecat session",
	Long: `Attach to a polecat session.

Only one session can be attached at a time (interactive).

Examples:
  gt all attach gastown/Toast`,
	Args: cobra.ExactArgs(1),
	RunE: runAllAttach,
}

var allRunCmd = &cobra.Command{
	Use:   "run <command> [specs...]",
	Short: "Inject a command into multiple polecat sessions",
	Long: `Inject a command into multiple running polecat sessions.

Sends the given text as input to each polecat's active session.

Examples:
  gt all run "git status" gastown/*
  gt all run "bd ready"`,
	Args: cobra.MinimumNArgs(1),
	RunE: runAllRun,
}

func init() {
	// Start flags
	allStartCmd.Flags().BoolVar(&allAwakeOnly, "awake-only", false, "Only start polecats with existing sessions")

	// Stop flags
	allStopCmd.Flags().BoolVarP(&allForce, "force", "f", false, "Force-kill sessions")

	// Status flags
	allStatusCmd.Flags().BoolVar(&allJSON, "json", false, "Output as JSON")

	// Add subcommands
	allCmd.AddCommand(allStartCmd)
	allCmd.AddCommand(allStopCmd)
	allCmd.AddCommand(allStatusCmd)
	allCmd.AddCommand(allAttachCmd)
	allCmd.AddCommand(allRunCmd)

	rootCmd.AddCommand(allCmd)
}

// expandedPolecat represents a resolved polecat target for batch operations.
type expandedPolecat struct {
	RigName     string
	PolecatName string
	Rig         *rig.Rig
	Polecat     *polecat.Polecat
}

// expandSpecs resolves spec patterns to a list of polecats.
// With no specs, returns all polecats across all rigs.
func expandSpecs(specs []string) ([]expandedPolecat, error) {
	// Default to "*" if no specs given
	if len(specs) == 0 {
		specs = []string{"*"}
	}

	var results []expandedPolecat
	seen := make(map[string]bool) // "rig/polecat" dedup

	for _, spec := range specs {
		expanded, err := expandOneSpec(spec)
		if err != nil {
			return nil, fmt.Errorf("expanding %q: %w", spec, err)
		}
		for _, ep := range expanded {
			key := ep.RigName + "/" + ep.PolecatName
			if !seen[key] {
				seen[key] = true
				results = append(results, ep)
			}
		}
	}

	return results, nil
}

// expandOneSpec resolves a single spec pattern.
func expandOneSpec(spec string) ([]expandedPolecat, error) {
	// Case 1: "*" — all polecats in all rigs
	if spec == "*" {
		return expandAllRigs()
	}

	// Case 2: "rig/*" — all polecats in a specific rig
	if strings.HasSuffix(spec, "/*") {
		rigName := strings.TrimSuffix(spec, "/*")
		return expandRig(rigName)
	}

	// Case 3: "rig/polecat" — specific polecat
	if strings.Contains(spec, "/") {
		rigName, polecatName, err := parseAddress(spec)
		if err != nil {
			return nil, err
		}
		return expandSingle(rigName, polecatName)
	}

	// Case 4: bare name — infer rig from cwd
	townRoot, err := workspace.FindFromCwd()
	if err != nil || townRoot == "" {
		return nil, fmt.Errorf("cannot infer rig for %q: not in a workspace", spec)
	}
	inferredRig, err := inferRigFromCwd(townRoot)
	if err != nil || inferredRig == "" {
		return nil, fmt.Errorf("cannot infer rig for %q: not in a rig directory", spec)
	}
	return expandSingle(inferredRig, spec)
}

// expandAllRigs returns all polecats across all rigs.
func expandAllRigs() ([]expandedPolecat, error) {
	rigs, _, err := getAllRigs()
	if err != nil {
		return nil, err
	}

	var results []expandedPolecat
	for _, r := range rigs {
		expanded, err := expandRigPolecats(r)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping rig %s: %v\n", r.Name, err)
			continue
		}
		results = append(results, expanded...)
	}
	return results, nil
}

// expandRig returns all polecats in a specific rig.
func expandRig(rigName string) ([]expandedPolecat, error) {
	_, r, err := getRig(rigName)
	if err != nil {
		return nil, err
	}
	return expandRigPolecats(r)
}

// expandRigPolecats lists all polecats in a rig.
func expandRigPolecats(r *rig.Rig) ([]expandedPolecat, error) {
	polecatGit := git.NewGit(r.Path)
	mgr := polecat.NewManager(r, polecatGit)

	polecats, err := mgr.List()
	if err != nil {
		return nil, fmt.Errorf("listing polecats in %s: %w", r.Name, err)
	}

	var results []expandedPolecat
	for _, p := range polecats {
		results = append(results, expandedPolecat{
			RigName:     r.Name,
			PolecatName: p.Name,
			Rig:         r,
			Polecat:     p,
		})
	}
	return results, nil
}

// expandSingle resolves a single rig/polecat pair.
func expandSingle(rigName, polecatName string) ([]expandedPolecat, error) {
	mgr, r, err := getPolecatManager(rigName)
	if err != nil {
		return nil, err
	}

	p, err := mgr.Get(polecatName)
	if err != nil {
		return nil, fmt.Errorf("polecat %q not found in rig %q", polecatName, rigName)
	}

	return []expandedPolecat{{
		RigName:     rigName,
		PolecatName: polecatName,
		Rig:         r,
		Polecat:     p,
	}}, nil
}

// filterAwake filters to only polecats with running sessions.
func filterAwake(targets []expandedPolecat) []expandedPolecat {
	var awake []expandedPolecat
	for _, t := range targets {
		sm := polecat.NewSessionManager(t.Rig)
		running, _ := sm.IsRunning(t.PolecatName)
		if running {
			awake = append(awake, t)
		}
	}
	return awake
}

// allResult tracks the outcome of a batch operation on one polecat.
type allResult struct {
	Address string
	Err     error
}

// runForAll executes an action against all targets in parallel, collecting errors.
func runForAll(targets []expandedPolecat, action func(expandedPolecat) error) []allResult {
	results := make([]allResult, len(targets))
	var wg sync.WaitGroup

	for i, t := range targets {
		wg.Add(1)
		go func(idx int, target expandedPolecat) {
			defer wg.Done()
			results[idx] = allResult{
				Address: target.RigName + "/" + target.PolecatName,
				Err:     action(target),
			}
		}(i, t)
	}

	wg.Wait()
	return results
}

// reportResults prints batch operation results and returns an error if any failed.
func reportResults(verb string, results []allResult) error {
	var succeeded, failed int
	var errs []string

	for _, r := range results {
		if r.Err != nil {
			failed++
			errs = append(errs, fmt.Sprintf("  %s: %v", r.Address, r.Err))
		} else {
			succeeded++
		}
	}

	if succeeded > 0 {
		fmt.Printf("%s %s %d polecat(s).\n", style.SuccessPrefix, verb, succeeded)
	}

	if failed > 0 {
		fmt.Printf("\n%s %d failed:\n", style.Warning.Render("Warning:"), failed)
		for _, e := range errs {
			fmt.Println(e)
		}
		return fmt.Errorf("%d operation(s) failed", failed)
	}

	return nil
}

func runAllStart(cmd *cobra.Command, args []string) error {
	targets, err := expandSpecs(args)
	if err != nil {
		return err
	}

	if allAwakeOnly {
		targets = filterAwake(targets)
	}

	if len(targets) == 0 {
		fmt.Println("No polecats to start.")
		return nil
	}

	fmt.Printf("Starting %d polecat(s)...\n", len(targets))

	results := runForAll(targets, func(t expandedPolecat) error {
		sm := polecat.NewSessionManager(t.Rig)
		return sm.Start(t.PolecatName, polecat.SessionStartOptions{})
	})

	return reportResults("Started", results)
}

func runAllStop(cmd *cobra.Command, args []string) error {
	targets, err := expandSpecs(args)
	if err != nil {
		return err
	}

	// Filter to running sessions only
	targets = filterAwake(targets)

	if len(targets) == 0 {
		fmt.Println("No running polecat sessions to stop.")
		return nil
	}

	fmt.Printf("Stopping %d polecat(s)...\n", len(targets))

	results := runForAll(targets, func(t expandedPolecat) error {
		sm := polecat.NewSessionManager(t.Rig)
		return sm.Stop(t.PolecatName, allForce)
	})

	return reportResults("Stopped", results)
}

// AllStatusItem represents a polecat in batch status output.
type AllStatusItem struct {
	Rig            string        `json:"rig"`
	Polecat        string        `json:"polecat"`
	State          polecat.State `json:"state"`
	Issue          string        `json:"issue,omitempty"`
	SessionRunning bool          `json:"session_running"`
	Branch         string        `json:"branch,omitempty"`
}

func runAllStatus(cmd *cobra.Command, args []string) error {
	targets, err := expandSpecs(args)
	if err != nil {
		return err
	}

	if len(targets) == 0 {
		fmt.Println("No polecats found.")
		return nil
	}

	// Collect status for each polecat
	var items []AllStatusItem
	for _, t := range targets {
		sm := polecat.NewSessionManager(t.Rig)
		running, _ := sm.IsRunning(t.PolecatName)

		item := AllStatusItem{
			Rig:            t.RigName,
			Polecat:        t.PolecatName,
			State:          t.Polecat.State,
			Issue:          t.Polecat.Issue,
			SessionRunning: running,
			Branch:         t.Polecat.Branch,
		}
		items = append(items, item)
	}

	// JSON output
	if allJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(items)
	}

	// Human-readable output
	fmt.Printf("%s\n\n", style.Bold.Render("Batch Polecat Status"))

	// Group by rig
	byRig := make(map[string][]AllStatusItem)
	var rigOrder []string
	for _, item := range items {
		if _, exists := byRig[item.Rig]; !exists {
			rigOrder = append(rigOrder, item.Rig)
		}
		byRig[item.Rig] = append(byRig[item.Rig], item)
	}

	for _, rigName := range rigOrder {
		rigItems := byRig[rigName]
		fmt.Printf("  %s\n", style.Bold.Render(rigName))
		for _, item := range rigItems {
			// Session indicator
			var sessionIcon string
			if item.SessionRunning {
				sessionIcon = style.Success.Render("●")
			} else {
				sessionIcon = style.Dim.Render("○")
			}

			// State color
			stateStr := string(item.State)
			switch item.State {
			case polecat.StateWorking:
				stateStr = style.Info.Render(stateStr)
			case polecat.StateStuck:
				stateStr = style.Warning.Render(stateStr)
			case polecat.StateDone:
				stateStr = style.Success.Render(stateStr)
			default:
				stateStr = style.Dim.Render(stateStr)
			}

			fmt.Printf("    %s %-16s %s\n", sessionIcon, item.Polecat, stateStr)
			if item.Issue != "" {
				fmt.Printf("      %s\n", style.Dim.Render(item.Issue))
			}
		}
		fmt.Println()
	}

	// Summary
	running := 0
	for _, item := range items {
		if item.SessionRunning {
			running++
		}
	}
	fmt.Printf("Total: %d polecats, %d running\n", len(items), running)

	return nil
}

func runAllAttach(cmd *cobra.Command, args []string) error {
	targets, err := expandSpecs([]string{args[0]})
	if err != nil {
		return err
	}

	if len(targets) == 0 {
		return fmt.Errorf("no polecat found matching %q", args[0])
	}

	t := targets[0]
	sm := polecat.NewSessionManager(t.Rig)
	return sm.Attach(t.PolecatName)
}

func runAllRun(cmd *cobra.Command, args []string) error {
	command := args[0]
	specs := args[1:]

	targets, err := expandSpecs(specs)
	if err != nil {
		return err
	}

	// Filter to running sessions
	targets = filterAwake(targets)

	if len(targets) == 0 {
		fmt.Println("No running polecat sessions to inject into.")
		return nil
	}

	fmt.Printf("Injecting command into %d polecat(s)...\n", len(targets))

	results := runForAll(targets, func(t expandedPolecat) error {
		sm := polecat.NewSessionManager(t.Rig)
		return sm.Inject(t.PolecatName, command)
	})

	return reportResults("Injected into", results)
}

// inferRigFromCwd is defined in crew_helpers.go
