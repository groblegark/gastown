package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

// HandoffAction for handoff command.
type HandoffAction string

const (
	HandoffCycle    HandoffAction = "cycle"    // Restart with handoff mail
	HandoffRestart  HandoffAction = "restart"  // Fresh restart, no handoff
	HandoffShutdown HandoffAction = "shutdown" // Terminate, no restart
)

var handoffCmd = &cobra.Command{
	Use:   "handoff",
	Short: "Request lifecycle action (retirement/restart)",
	Long: `Request a lifecycle action from your manager.

This command initiates graceful retirement:
1. Verifies git state is clean
2. Sends handoff mail to yourself (for cycle)
3. Sends lifecycle request to your manager
4. Sets requesting state and waits for retirement

Your manager (daemon for Mayor/Witness, witness for polecats) will
verify the request and terminate your session. For cycle/restart,
a new session starts and reads your handoff mail to continue work.

Flags:
  --cycle     Restart with handoff mail (default for Mayor/Witness)
  --restart   Fresh restart, no handoff context
  --shutdown  Terminate without restart (default for polecats)

Examples:
  gt handoff           # Use role-appropriate default
  gt handoff --cycle   # Restart with context handoff
  gt handoff --restart # Fresh restart
`,
	RunE: runHandoff,
}

var (
	handoffCycle    bool
	handoffRestart  bool
	handoffShutdown bool
	handoffForce    bool
	handoffMessage  string
)

func init() {
	handoffCmd.Flags().BoolVar(&handoffCycle, "cycle", false, "Restart with handoff mail")
	handoffCmd.Flags().BoolVar(&handoffRestart, "restart", false, "Fresh restart, no handoff")
	handoffCmd.Flags().BoolVar(&handoffShutdown, "shutdown", false, "Terminate without restart")
	handoffCmd.Flags().BoolVarP(&handoffForce, "force", "f", false, "Skip pre-flight checks")
	handoffCmd.Flags().StringVarP(&handoffMessage, "message", "m", "", "Handoff message for successor")

	rootCmd.AddCommand(handoffCmd)
}

func runHandoff(cmd *cobra.Command, args []string) error {
	// Detect our role
	role := detectHandoffRole()
	if role == RoleUnknown {
		return fmt.Errorf("cannot detect agent role (set GT_ROLE or run from known context)")
	}

	// Determine action
	action := determineAction(role)

	fmt.Printf("Agent role: %s\n", style.Bold.Render(string(role)))
	fmt.Printf("Action: %s\n", style.Bold.Render(string(action)))

	// Find workspace
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	// Pre-flight checks (unless forced)
	if !handoffForce {
		if err := preFlightChecks(); err != nil {
			return fmt.Errorf("pre-flight check failed: %w\n\nUse --force to skip checks", err)
		}
	}

	// For cycle, send handoff mail to self
	if action == HandoffCycle {
		if err := sendHandoffMail(role, townRoot); err != nil {
			return fmt.Errorf("sending handoff mail: %w", err)
		}
		fmt.Printf("%s Sent handoff mail to self\n", style.Bold.Render("✓"))
	}

	// Send lifecycle request to manager
	manager := getManager(role)
	if err := sendLifecycleRequest(manager, role, action, townRoot); err != nil {
		return fmt.Errorf("sending lifecycle request: %w", err)
	}
	fmt.Printf("%s Sent %s request to %s\n", style.Bold.Render("✓"), action, manager)

	// Set requesting state
	if err := setRequestingState(role, action, townRoot); err != nil {
		fmt.Printf("Warning: failed to set state: %v\n", err)
	}

	// Wait for retirement
	fmt.Println()
	fmt.Printf("%s Waiting for retirement...\n", style.Dim.Render("◌"))
	fmt.Println(style.Dim.Render("(Manager will terminate this session)"))

	// Block forever - manager will kill us
	select {}
}

// detectHandoffRole figures out what kind of agent we are.
// Uses GT_ROLE env var, tmux session name, or directory context.
func detectHandoffRole() Role {
	// Check GT_ROLE environment variable first
	if role := os.Getenv("GT_ROLE"); role != "" {
		switch strings.ToLower(role) {
		case "mayor":
			return RoleMayor
		case "witness":
			return RoleWitness
		case "refinery":
			return RoleRefinery
		case "polecat":
			return RolePolecat
		case "crew":
			return RoleCrew
		}
	}

	// Check tmux session name
	out, err := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
	if err == nil {
		sessionName := strings.TrimSpace(string(out))
		if sessionName == "gt-mayor" {
			return RoleMayor
		}
		if strings.HasSuffix(sessionName, "-witness") {
			return RoleWitness
		}
		if strings.HasSuffix(sessionName, "-refinery") {
			return RoleRefinery
		}
		// Polecat sessions: gt-<rig>-<name>
		if strings.HasPrefix(sessionName, "gt-") && strings.Count(sessionName, "-") >= 2 {
			return RolePolecat
		}
	}

	// Fall back to directory-based detection
	cwd, err := os.Getwd()
	if err != nil {
		return RoleUnknown
	}

	townRoot, err := workspace.FindFromCwd()
	if err != nil || townRoot == "" {
		return RoleUnknown
	}

	ctx := detectRole(cwd, townRoot)
	return ctx.Role
}

// determineAction picks the action based on flags or role default.
func determineAction(role Role) HandoffAction {
	// Explicit flags take precedence
	if handoffCycle {
		return HandoffCycle
	}
	if handoffRestart {
		return HandoffRestart
	}
	if handoffShutdown {
		return HandoffShutdown
	}

	// Role-based defaults
	switch role {
	case RolePolecat:
		return HandoffShutdown // Ephemeral, work is done
	case RoleMayor, RoleWitness, RoleRefinery:
		return HandoffCycle // Long-running, preserve context
	case RoleCrew:
		return HandoffCycle // Will only send mail, not actually retire
	default:
		return HandoffCycle
	}
}

// preFlightChecks verifies it's safe to retire.
func preFlightChecks() error {
	// Check git status
	cmd := exec.Command("git", "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		// Not a git repo, that's fine
		return nil
	}

	if len(strings.TrimSpace(string(out))) > 0 {
		return fmt.Errorf("uncommitted changes in git working tree")
	}

	return nil
}

// getManager returns the address of our lifecycle manager.
func getManager(role Role) string {
	switch role {
	case RoleMayor, RoleWitness:
		return "daemon/"
	case RolePolecat, RoleRefinery:
		// Would need rig context to determine witness address
		// For now, use a placeholder pattern
		return "<rig>/witness"
	case RoleCrew:
		return "human" // Crew is human-managed
	default:
		return "daemon/"
	}
}

// sendHandoffMail sends a handoff message to ourselves for the successor to read.
func sendHandoffMail(role Role, townRoot string) error {
	// Determine our address
	var selfAddr string
	switch role {
	case RoleMayor:
		selfAddr = "mayor/"
	case RoleWitness:
		selfAddr = "witness/" // Would need rig prefix
	default:
		selfAddr = string(role) + "/"
	}

	// Build handoff message
	subject := "🤝 HANDOFF: Session cycling"
	body := handoffMessage
	if body == "" {
		body = fmt.Sprintf(`Handoff from previous session.

Time: %s
Role: %s
Action: cycle

Check bd ready for pending work.
Check gt mail inbox for messages received during transition.
`, time.Now().Format(time.RFC3339), role)
	}

	// Send via bd mail (syntax: bd mail send <recipient> -s <subject> -m <body>)
	cmd := exec.Command("bd", "mail", "send", selfAddr,
		"-s", subject,
		"-m", body,
	)
	cmd.Dir = townRoot

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}

	return nil
}

// sendLifecycleRequest sends the lifecycle request to our manager.
func sendLifecycleRequest(manager string, role Role, action HandoffAction, townRoot string) error {
	if manager == "human" {
		// Crew is human-managed, just print a message
		fmt.Println(style.Dim.Render("(Crew sessions are human-managed, no lifecycle request sent)"))
		return nil
	}

	subject := fmt.Sprintf("LIFECYCLE: %s requesting %s", role, action)
	body := fmt.Sprintf(`Lifecycle request from %s.

Action: %s
Time: %s

Please verify state and execute lifecycle action.
`, role, action, time.Now().Format(time.RFC3339))

	// Send via bd mail (syntax: bd mail send <recipient> -s <subject> -m <body>)
	cmd := exec.Command("bd", "mail", "send", manager,
		"-s", subject,
		"-m", body,
	)
	cmd.Dir = townRoot

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}

	return nil
}

// setRequestingState updates state.json to indicate we're requesting lifecycle action.
func setRequestingState(role Role, action HandoffAction, townRoot string) error {
	// Determine state file location based on role
	var stateFile string
	switch role {
	case RoleMayor:
		stateFile = filepath.Join(townRoot, "mayor", "state.json")
	case RoleWitness:
		// Would need rig context
		stateFile = filepath.Join(townRoot, "witness", "state.json")
	default:
		// For other roles, use a generic location
		stateFile = filepath.Join(townRoot, ".gastown", "agent-state.json")
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(stateFile), 0755); err != nil {
		return err
	}

	// Read existing state or create new
	state := make(map[string]interface{})
	if data, err := os.ReadFile(stateFile); err == nil {
		json.Unmarshal(data, &state)
	}

	// Set requesting state
	state["requesting_"+string(action)] = true
	state["requesting_time"] = time.Now().Format(time.RFC3339)

	// Write back
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(stateFile, data, 0644)
}
