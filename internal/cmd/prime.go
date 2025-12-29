package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/lock"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/templates"
	"github.com/steveyegge/gastown/internal/workspace"
)

// Role represents a detected agent role.
type Role string

const (
	RoleMayor    Role = "mayor"
	RoleDeacon   Role = "deacon"
	RoleWitness  Role = "witness"
	RoleRefinery Role = "refinery"
	RolePolecat  Role = "polecat"
	RoleCrew     Role = "crew"
	RoleUnknown  Role = "unknown"
)

var primeCmd = &cobra.Command{
	Use:     "prime",
	GroupID: GroupDiag,
	Short:   "Output role context for current directory",
	Long: `Detect the agent role from the current directory and output context.

Role detection:
  - Town root or mayor/rig/ → Mayor context
  - <rig>/witness/rig/ → Witness context
  - <rig>/refinery/rig/ → Refinery context
  - <rig>/polecats/<name>/ → Polecat context

This command is typically used in shell prompts or agent initialization.`,
	RunE: runPrime,
}

func init() {
	rootCmd.AddCommand(primeCmd)
}

// RoleContext is an alias for RoleInfo for backward compatibility.
// New code should use RoleInfo directly.
type RoleContext = RoleInfo

func runPrime(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting current directory: %w", err)
	}

	// Find town root
	townRoot, err := workspace.FindFromCwd()
	if err != nil {
		return fmt.Errorf("finding workspace: %w", err)
	}
	if townRoot == "" {
		return fmt.Errorf("not in a Gas Town workspace")
	}

	// Get role using env-aware detection
	roleInfo, err := GetRoleWithContext(cwd, townRoot)
	if err != nil {
		return fmt.Errorf("detecting role: %w", err)
	}

	// Warn prominently if there's a role/cwd mismatch
	if roleInfo.Mismatch {
		fmt.Printf("\n%s\n", style.Bold.Render("⚠️  ROLE/LOCATION MISMATCH"))
		fmt.Printf("You are %s (from $GT_ROLE) but your cwd suggests %s.\n",
			style.Bold.Render(string(roleInfo.Role)),
			style.Bold.Render(string(roleInfo.CwdRole)))
		fmt.Printf("Expected home: %s\n", roleInfo.Home)
		fmt.Printf("Actual cwd:    %s\n", cwd)
		fmt.Println()
		fmt.Println("This can cause commands to misbehave. Either:")
		fmt.Println("  1. cd to your home directory, OR")
		fmt.Println("  2. Use absolute paths for gt/bd commands")
		fmt.Println()
	}

	// Build RoleContext for compatibility with existing code
	ctx := RoleContext{
		Role:     roleInfo.Role,
		Rig:      roleInfo.Rig,
		Polecat:  roleInfo.Polecat,
		TownRoot: townRoot,
		WorkDir:  cwd,
	}

	// Check and acquire identity lock for worker roles
	if err := acquireIdentityLock(ctx); err != nil {
		return err
	}

	// Ensure beads redirect exists for worktree-based roles
	ensureBeadsRedirect(ctx)

	// Report agent state as running (ZFC: agents self-report state)
	reportAgentState(ctx, "running")

	// Output context
	if err := outputPrimeContext(ctx); err != nil {
		return err
	}

	// Output handoff content if present
	outputHandoffContent(ctx)

	// Output attachment status (for autonomous work detection)
	outputAttachmentStatus(ctx)

	// Check for slung work on hook (from gt sling)
	// If found, we're in autonomous mode - skip normal startup directive
	hasSlungWork := checkSlungWork(ctx)

	// Output molecule context if working on a molecule step
	outputMoleculeContext(ctx)

	// Run bd prime to output beads workflow context
	runBdPrime(cwd)

	// Run gt mail check --inject to inject any pending mail
	runMailCheckInject(cwd)

	// Output startup directive for roles that should announce themselves
	// Skip if in autonomous mode (slung work provides its own directive)
	if !hasSlungWork {
		outputStartupDirective(ctx)
	}

	return nil
}

func detectRole(cwd, townRoot string) RoleInfo {
	ctx := RoleInfo{
		Role:     RoleUnknown,
		TownRoot: townRoot,
		WorkDir:  cwd,
		Source:   "cwd",
	}

	// Get relative path from town root
	relPath, err := filepath.Rel(townRoot, cwd)
	if err != nil {
		return ctx
	}

	// Normalize and split path
	relPath = filepath.ToSlash(relPath)
	parts := strings.Split(relPath, "/")

	// Check for mayor role
	// At town root, or in mayor/ or mayor/rig/
	if relPath == "." || relPath == "" {
		ctx.Role = RoleMayor
		return ctx
	}
	if len(parts) >= 1 && parts[0] == "mayor" {
		ctx.Role = RoleMayor
		return ctx
	}

	// Check for deacon role: deacon/
	if len(parts) >= 1 && parts[0] == "deacon" {
		ctx.Role = RoleDeacon
		return ctx
	}

	// At this point, first part should be a rig name
	if len(parts) < 1 {
		return ctx
	}
	rigName := parts[0]
	ctx.Rig = rigName

	// Check for witness: <rig>/witness/rig/
	if len(parts) >= 2 && parts[1] == "witness" {
		ctx.Role = RoleWitness
		return ctx
	}

	// Check for refinery: <rig>/refinery/rig/
	if len(parts) >= 2 && parts[1] == "refinery" {
		ctx.Role = RoleRefinery
		return ctx
	}

	// Check for polecat: <rig>/polecats/<name>/
	if len(parts) >= 3 && parts[1] == "polecats" {
		ctx.Role = RolePolecat
		ctx.Polecat = parts[2]
		return ctx
	}

	// Check for crew: <rig>/crew/<name>/
	if len(parts) >= 3 && parts[1] == "crew" {
		ctx.Role = RoleCrew
		ctx.Polecat = parts[2] // Use Polecat field for crew member name
		return ctx
	}

	// Default: could be rig root - treat as unknown
	return ctx
}

func outputPrimeContext(ctx RoleContext) error {
	// Try to use templates first
	tmpl, err := templates.New()
	if err != nil {
		// Fall back to hardcoded output if templates fail
		return outputPrimeContextFallback(ctx)
	}

	// Map role to template name
	var roleName string
	switch ctx.Role {
	case RoleMayor:
		roleName = "mayor"
	case RoleDeacon:
		roleName = "deacon"
	case RoleWitness:
		roleName = "witness"
	case RoleRefinery:
		roleName = "refinery"
	case RolePolecat:
		roleName = "polecat"
	case RoleCrew:
		roleName = "crew"
	default:
		// Unknown role - use fallback
		return outputPrimeContextFallback(ctx)
	}

	// Build template data
	data := templates.RoleData{
		Role:     roleName,
		RigName:  ctx.Rig,
		TownRoot: ctx.TownRoot,
		WorkDir:  ctx.WorkDir,
		Polecat:  ctx.Polecat,
	}

	// Render and output
	output, err := tmpl.RenderRole(roleName, data)
	if err != nil {
		return fmt.Errorf("rendering template: %w", err)
	}

	fmt.Print(output)
	return nil
}

func outputPrimeContextFallback(ctx RoleContext) error {
	switch ctx.Role {
	case RoleMayor:
		outputMayorContext(ctx)
	case RoleWitness:
		outputWitnessContext(ctx)
	case RoleRefinery:
		outputRefineryContext(ctx)
	case RolePolecat:
		outputPolecatContext(ctx)
	case RoleCrew:
		outputCrewContext(ctx)
	default:
		outputUnknownContext(ctx)
	}
	return nil
}

func outputMayorContext(ctx RoleContext) {
	fmt.Printf("%s\n\n", style.Bold.Render("# Mayor Context"))
	fmt.Println("You are the **Mayor** - the global coordinator of Gas Town.")
	fmt.Println()
	fmt.Println("## Responsibilities")
	fmt.Println("- Coordinate work across all rigs")
	fmt.Println("- Delegate to Refineries, not directly to polecats")
	fmt.Println("- Monitor overall system health")
	fmt.Println()
	fmt.Println("## Key Commands")
	fmt.Println("- `gt mail inbox` - Check your messages")
	fmt.Println("- `gt mail read <id>` - Read a specific message")
	fmt.Println("- `gt status` - Show overall town status")
	fmt.Println("- `gt rigs` - List all rigs")
	fmt.Println("- `bd ready` - Issues ready to work")
	fmt.Println()
	fmt.Println("## Startup")
	fmt.Println("Check for handoff messages with 🤝 HANDOFF in subject - continue predecessor's work.")
	fmt.Println()
	fmt.Printf("Town root: %s\n", style.Dim.Render(ctx.TownRoot))
}

func outputWitnessContext(ctx RoleContext) {
	fmt.Printf("%s\n\n", style.Bold.Render("# Witness Context"))
	fmt.Printf("You are the **Witness** for rig: %s\n\n", style.Bold.Render(ctx.Rig))
	fmt.Println("## Responsibilities")
	fmt.Println("- Monitor polecat health via heartbeat")
	fmt.Println("- Spawn replacement agents for stuck polecats")
	fmt.Println("- Report rig status to Mayor")
	fmt.Println()
	fmt.Println("## Key Commands")
	fmt.Println("- `gt witness status` - Show witness status")
	fmt.Println("- `gt polecats` - List polecats in this rig")
	fmt.Println()
	fmt.Printf("Rig: %s\n", style.Dim.Render(ctx.Rig))
}

func outputRefineryContext(ctx RoleContext) {
	fmt.Printf("%s\n\n", style.Bold.Render("# Refinery Context"))
	fmt.Printf("You are the **Refinery** for rig: %s\n\n", style.Bold.Render(ctx.Rig))
	fmt.Println("## Responsibilities")
	fmt.Println("- Process the merge queue for this rig")
	fmt.Println("- Merge polecat work to integration branch")
	fmt.Println("- Resolve merge conflicts")
	fmt.Println("- Land completed swarms to main")
	fmt.Println()
	fmt.Println("## Key Commands")
	fmt.Println("- `gt merge queue` - Show pending merges")
	fmt.Println("- `gt merge next` - Process next merge")
	fmt.Println()
	fmt.Printf("Rig: %s\n", style.Dim.Render(ctx.Rig))
}

func outputPolecatContext(ctx RoleContext) {
	fmt.Printf("%s\n\n", style.Bold.Render("# Polecat Context"))
	fmt.Printf("You are polecat **%s** in rig: %s\n\n",
		style.Bold.Render(ctx.Polecat), style.Bold.Render(ctx.Rig))
	fmt.Println("## Startup Protocol")
	fmt.Println("1. Run `gt prime` - loads context and checks mail automatically")
	fmt.Println("2. Check inbox - if mail shown, read with `gt mail read <id>`")
	fmt.Println("3. Look for '📋 Work Assignment' messages for your task")
	fmt.Println("4. If no mail, check `bd list --status=in_progress` for existing work")
	fmt.Println()
	fmt.Println("## Key Commands")
	fmt.Println("- `gt mail inbox` - Check your inbox for work assignments")
	fmt.Println("- `bd show <issue>` - View your assigned issue")
	fmt.Println("- `bd close <issue>` - Mark issue complete")
	fmt.Println("- `gt done` - Signal work ready for merge")
	fmt.Println()
	fmt.Printf("Polecat: %s | Rig: %s\n",
		style.Dim.Render(ctx.Polecat), style.Dim.Render(ctx.Rig))
}

func outputCrewContext(ctx RoleContext) {
	fmt.Printf("%s\n\n", style.Bold.Render("# Crew Worker Context"))
	fmt.Printf("You are crew worker **%s** in rig: %s\n\n",
		style.Bold.Render(ctx.Polecat), style.Bold.Render(ctx.Rig))
	fmt.Println("## About Crew Workers")
	fmt.Println("- Persistent workspace (not auto-garbage-collected)")
	fmt.Println("- User-managed (not Witness-monitored)")
	fmt.Println("- Long-lived identity across sessions")
	fmt.Println()
	fmt.Println("## Key Commands")
	fmt.Println("- `gt mail inbox` - Check your inbox")
	fmt.Println("- `bd ready` - Available issues")
	fmt.Println("- `bd show <issue>` - View issue details")
	fmt.Println("- `bd close <issue>` - Mark issue complete")
	fmt.Println()
	fmt.Printf("Crew: %s | Rig: %s\n",
		style.Dim.Render(ctx.Polecat), style.Dim.Render(ctx.Rig))
}

func outputUnknownContext(ctx RoleContext) {
	fmt.Printf("%s\n\n", style.Bold.Render("# Gas Town Context"))
	fmt.Println("Could not determine specific role from current directory.")
	fmt.Println()
	if ctx.Rig != "" {
		fmt.Printf("You appear to be in rig: %s\n\n", style.Bold.Render(ctx.Rig))
	}
	fmt.Println("Navigate to a specific agent directory:")
	fmt.Println("- `<rig>/polecats/<name>/` - Polecat role")
	fmt.Println("- `<rig>/witness/rig/` - Witness role")
	fmt.Println("- `<rig>/refinery/rig/` - Refinery role")
	fmt.Println("- Town root or `mayor/` - Mayor role")
	fmt.Println()
	fmt.Printf("Town root: %s\n", style.Dim.Render(ctx.TownRoot))
}

// outputHandoffContent reads and displays the pinned handoff bead for the role.
func outputHandoffContent(ctx RoleContext) {
	if ctx.Role == RoleUnknown {
		return
	}

	// Get role key for handoff bead lookup
	roleKey := string(ctx.Role)

	bd := beads.New(ctx.TownRoot)
	issue, err := bd.FindHandoffBead(roleKey)
	if err != nil {
		// Silently skip if beads lookup fails (might not be a beads repo)
		return
	}
	if issue == nil || issue.Description == "" {
		// No handoff content
		return
	}

	// Display handoff content
	fmt.Println()
	fmt.Printf("%s\n\n", style.Bold.Render("## 🤝 Handoff from Previous Session"))
	fmt.Println(issue.Description)
	fmt.Println()
	fmt.Println(style.Dim.Render("(Clear with: gt rig reset --handoff)"))
}

// runBdPrime runs `bd prime` and outputs the result.
// This provides beads workflow context to the agent.
func runBdPrime(workDir string) {
	cmd := exec.Command("bd", "prime")
	cmd.Dir = workDir

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil // Ignore stderr

	if err := cmd.Run(); err != nil {
		// Silently skip if bd prime fails (beads might not be available)
		return
	}

	output := strings.TrimSpace(stdout.String())
	if output != "" {
		fmt.Println()
		fmt.Println(output)
	}
}

// outputStartupDirective outputs role-specific instructions for the agent.
// This tells agents like Mayor to announce themselves on startup.
func outputStartupDirective(ctx RoleContext) {
	switch ctx.Role {
	case RoleMayor:
		fmt.Println()
		fmt.Println("---")
		fmt.Println()
		fmt.Println("**STARTUP PROTOCOL**: You are the Mayor. Please:")
		fmt.Println("1. Announce: \"Mayor, checking in.\"")
		fmt.Println("2. Check mail: `gt mail inbox` - look for 🤝 HANDOFF messages")
		fmt.Println("3. Check for attached work: `gt mol status`")
		fmt.Println("   - If mol attached → **RUN IT** (no human input needed)")
		fmt.Println("   - If no mol → await user instruction")
	case RoleWitness:
		fmt.Println()
		fmt.Println("---")
		fmt.Println()
		fmt.Println("**STARTUP PROTOCOL**: You are the Witness. Please:")
		fmt.Println("1. Announce: \"Witness, checking in.\"")
		fmt.Println("2. Check mail: `gt mail inbox` - look for 🤝 HANDOFF messages")
		fmt.Println("3. Check for attached patrol: `gt mol status`")
		fmt.Println("   - If mol attached → **RUN IT** (resume from current step)")
		fmt.Println("   - If no mol → create patrol: `bd mol wisp mol-witness-patrol`")
	case RolePolecat:
		fmt.Println()
		fmt.Println("---")
		fmt.Println()
		fmt.Println("**STARTUP PROTOCOL**: You are a polecat. Please:")
		fmt.Printf("1. Announce: \"%s Polecat %s, checking in.\"\n", ctx.Rig, ctx.Polecat)
		fmt.Println("2. Check mail: `gt mail inbox`")
		fmt.Println("3. If there's a 🤝 HANDOFF message, read it for context")
		fmt.Println("4. Check for attached work: `gt mol status`")
		fmt.Println("   - If mol attached → **RUN IT** (you were spawned with this work)")
		fmt.Println("   - If no mol → ERROR: polecats must have work attached; escalate to Witness")
	case RoleRefinery:
		fmt.Println()
		fmt.Println("---")
		fmt.Println()
		fmt.Println("**STARTUP PROTOCOL**: You are the Refinery. Please:")
		fmt.Println("1. Announce: \"Refinery, checking in.\"")
		fmt.Println("2. Check mail: `gt mail inbox` - look for 🤝 HANDOFF messages")
		fmt.Println("3. Check for attached patrol: `gt mol status`")
		fmt.Println("   - If mol attached → **RUN IT** (resume from current step)")
		fmt.Println("   - If no mol → create patrol: `bd mol wisp mol-refinery-patrol`")
	case RoleCrew:
		fmt.Println()
		fmt.Println("---")
		fmt.Println()
		fmt.Println("**STARTUP PROTOCOL**: You are a crew worker. Please:")
		fmt.Printf("1. Announce: \"%s Crew %s, checking in.\"\n", ctx.Rig, ctx.Polecat)
		fmt.Println("2. Check mail: `gt mail inbox`")
		fmt.Println("3. If there's a 🤝 HANDOFF message, read it and continue the work")
		fmt.Println("4. Check for attached work: `gt mol status`")
		fmt.Println("   - If attachment found → **RUN IT** (no human input needed)")
		fmt.Println("   - If no attachment → await user instruction")
	case RoleDeacon:
		fmt.Println()
		fmt.Println("---")
		fmt.Println()
		fmt.Println("**STARTUP PROTOCOL**: You are the Deacon. Please:")
		fmt.Println("1. Announce: \"Deacon, checking in.\"")
		fmt.Println("2. Signal awake: `gt deacon heartbeat \"starting patrol\"`")
		fmt.Println("3. Check mail: `gt mail inbox` - look for 🤝 HANDOFF messages")
		fmt.Println("4. Check for attached patrol: `gt mol status`")
		fmt.Println("   - If mol attached → **RUN IT** (resume from current step)")
		fmt.Println("   - If no mol → create patrol: `bd mol wisp mol-deacon-patrol`")
	}
}

// runMailCheckInject runs `gt mail check --inject` and outputs the result.
// This injects any pending mail into the agent's context.
func runMailCheckInject(workDir string) {
	cmd := exec.Command("gt", "mail", "check", "--inject")
	cmd.Dir = workDir

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil // Ignore stderr

	if err := cmd.Run(); err != nil {
		// Silently skip if mail check fails
		return
	}

	output := strings.TrimSpace(stdout.String())
	if output != "" {
		fmt.Println()
		fmt.Println(output)
	}
}

// outputAttachmentStatus checks for attached work molecule and outputs status.
// This is key for the autonomous overnight work pattern.
// The Propulsion Principle: "If you find something on your hook, YOU RUN IT."
func outputAttachmentStatus(ctx RoleContext) {
	// Skip only unknown roles - all valid roles can have pinned work
	if ctx.Role == RoleUnknown {
		return
	}

	// Check for pinned beads with attachments
	b := beads.New(ctx.WorkDir)

	// Build assignee string based on role (same as getAgentIdentity)
	assignee := getAgentIdentity(ctx)
	if assignee == "" {
		return
	}

	// Find pinned beads for this agent
	pinnedBeads, err := b.List(beads.ListOptions{
		Status:   beads.StatusPinned,
		Assignee: assignee,
		Priority: -1,
	})
	if err != nil || len(pinnedBeads) == 0 {
		// No pinned beads - interactive mode
		return
	}

	// Check first pinned bead for attachment
	attachment := beads.ParseAttachmentFields(pinnedBeads[0])
	if attachment == nil || attachment.AttachedMolecule == "" {
		// No attachment - interactive mode
		return
	}

	// Has attached work - output prominently with current step
	fmt.Println()
	fmt.Printf("%s\n\n", style.Bold.Render("## 🎯 ATTACHED WORK DETECTED"))
	fmt.Printf("Pinned bead: %s\n", pinnedBeads[0].ID)
	fmt.Printf("Attached molecule: %s\n", attachment.AttachedMolecule)
	if attachment.AttachedAt != "" {
		fmt.Printf("Attached at: %s\n", attachment.AttachedAt)
	}
	if attachment.AttachedArgs != "" {
		fmt.Println()
		fmt.Printf("%s\n", style.Bold.Render("📋 ARGS (use these to guide execution):"))
		fmt.Printf("  %s\n", attachment.AttachedArgs)
	}
	fmt.Println()

	// Show current step from molecule
	showMoleculeExecutionPrompt(ctx.WorkDir, attachment.AttachedMolecule)
}

// MoleculeCurrentOutput represents the JSON output of bd mol current.
type MoleculeCurrentOutput struct {
	MoleculeID    string `json:"molecule_id"`
	MoleculeTitle string `json:"molecule_title"`
	NextStep      *struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Status      string `json:"status"`
	} `json:"next_step"`
	Completed int `json:"completed"`
	Total     int `json:"total"`
}

// showMoleculeExecutionPrompt calls bd mol current and shows the current step
// with execution instructions. This is the core of the Propulsion Principle.
func showMoleculeExecutionPrompt(workDir, moleculeID string) {
	// Call bd mol current with JSON output
	cmd := exec.Command("bd", "--no-daemon", "mol", "current", moleculeID, "--json")
	cmd.Dir = workDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Fall back to simple message if bd mol current fails
		fmt.Println(style.Bold.Render("→ PROPULSION PRINCIPLE: Work is on your hook. RUN IT."))
		fmt.Println("  Begin working on this molecule immediately.")
		fmt.Printf("  Check status with: bd mol current %s\n", moleculeID)
		return
	}

	// Parse JSON output - it's an array with one element
	var outputs []MoleculeCurrentOutput
	if err := json.Unmarshal(stdout.Bytes(), &outputs); err != nil || len(outputs) == 0 {
		// Fall back to simple message
		fmt.Println(style.Bold.Render("→ PROPULSION PRINCIPLE: Work is on your hook. RUN IT."))
		fmt.Println("  Begin working on this molecule immediately.")
		return
	}
	output := outputs[0]

	// Show molecule progress
	fmt.Printf("**Progress:** %d/%d steps complete\n\n",
		output.Completed, output.Total)

	// Show current step if available
	if output.NextStep != nil {
		step := output.NextStep
		fmt.Printf("%s\n\n", style.Bold.Render("## 🎬 CURRENT STEP: "+step.Title))
		fmt.Printf("**Step ID:** %s\n", step.ID)
		fmt.Printf("**Status:** %s (ready to execute)\n\n", step.Status)

		// Show step description if available
		if step.Description != "" {
			fmt.Println("### Instructions")
			fmt.Println()
			// Indent the description for readability
			lines := strings.Split(step.Description, "\n")
			for _, line := range lines {
				fmt.Printf("%s\n", line)
			}
			fmt.Println()
		}

		// The propulsion directive
		fmt.Println(style.Bold.Render("→ EXECUTE THIS STEP NOW."))
		fmt.Println()
		fmt.Println("When complete:")
		fmt.Printf("  1. Close the step: bd close %s\n", step.ID)
		fmt.Println("  2. Check for next step: bd ready")
		fmt.Println("  3. Continue until molecule complete")
	} else {
		// No next step - molecule may be complete
		fmt.Println(style.Bold.Render("✓ MOLECULE COMPLETE"))
		fmt.Println()
		fmt.Println("All steps are done. You may:")
		fmt.Println("  - Report completion to supervisor")
		fmt.Println("  - Check for new work: bd ready")
	}
}

// outputMoleculeContext checks if the agent is working on a molecule step and shows progress.
func outputMoleculeContext(ctx RoleContext) {
	// Applies to polecats, crew workers, deacon, witness, and refinery
	if ctx.Role != RolePolecat && ctx.Role != RoleCrew && ctx.Role != RoleDeacon && ctx.Role != RoleWitness && ctx.Role != RoleRefinery {
		return
	}

	// For Deacon, use special patrol molecule handling
	if ctx.Role == RoleDeacon {
		outputDeaconPatrolContext(ctx)
		return
	}

	// For Witness, use special patrol molecule handling (auto-bonds on startup)
	if ctx.Role == RoleWitness {
		outputWitnessPatrolContext(ctx)
		return
	}

	// For Refinery, use special patrol molecule handling (auto-bonds on startup)
	if ctx.Role == RoleRefinery {
		outputRefineryPatrolContext(ctx)
		return
	}

	// Check for in-progress issues
	b := beads.New(ctx.WorkDir)
	issues, err := b.List(beads.ListOptions{
		Status:   "in_progress",
		Assignee: ctx.Polecat,
		Priority: -1,
	})
	if err != nil || len(issues) == 0 {
		return
	}

	// Check if any in-progress issue is a molecule step
	for _, issue := range issues {
		moleculeID := parseMoleculeMetadata(issue.Description)
		if moleculeID == "" {
			continue
		}

		// Get the parent (root) issue ID
		rootID := issue.Parent
		if rootID == "" {
			continue
		}

		// This is a molecule step - show context
		fmt.Println()
		fmt.Printf("%s\n\n", style.Bold.Render("## 🧬 Molecule Workflow"))
		fmt.Printf("You are working on a molecule step.\n")
		fmt.Printf("  Current step: %s\n", issue.ID)
		fmt.Printf("  Molecule: %s\n", moleculeID)
		fmt.Printf("  Root issue: %s\n\n", rootID)

		// Show molecule progress by finding sibling steps
		showMoleculeProgress(b, rootID)

		fmt.Println()
		fmt.Println("**Molecule Work Loop:**")
		fmt.Println("1. Complete current step, then `bd close " + issue.ID + "`")
		fmt.Println("2. Check for next steps: `bd ready --parent " + rootID + "`")
		fmt.Println("3. Work on next ready step(s)")
		fmt.Println("4. When all steps done, run `gt done`")
		break // Only show context for first molecule step found
	}
}

// parseMoleculeMetadata extracts molecule info from a step's description.
// Looks for lines like:
//
//	instantiated_from: mol-xyz
func parseMoleculeMetadata(description string) string {
	lines := strings.Split(description, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "instantiated_from:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "instantiated_from:"))
		}
	}
	return ""
}

// showMoleculeProgress displays the progress through a molecule's steps.
func showMoleculeProgress(b *beads.Beads, rootID string) {
	if rootID == "" {
		return
	}

	// Find all children of the root issue
	children, err := b.List(beads.ListOptions{
		Parent:   rootID,
		Status:   "all",
		Priority: -1,
	})
	if err != nil || len(children) == 0 {
		return
	}

	total := len(children)
	done := 0
	inProgress := 0
	var readySteps []string

	for _, child := range children {
		switch child.Status {
		case "closed":
			done++
		case "in_progress":
			inProgress++
		case "open":
			// Check if ready (no open dependencies)
			if len(child.DependsOn) == 0 {
				readySteps = append(readySteps, child.ID)
			}
		}
	}

	fmt.Printf("Progress: %d/%d steps complete", done, total)
	if inProgress > 0 {
		fmt.Printf(" (%d in progress)", inProgress)
	}
	fmt.Println()

	if len(readySteps) > 0 {
		fmt.Printf("Ready steps: %s\n", strings.Join(readySteps, ", "))
	}
}

// outputDeaconPatrolContext shows patrol molecule status for the Deacon.
// Deacon uses wisps (Wisp:true issues in main .beads/) for patrol cycles.
func outputDeaconPatrolContext(ctx RoleContext) {
	cfg := PatrolConfig{
		RoleName:        "deacon",
		PatrolMolName:   "mol-deacon-patrol",
		BeadsDir:        ctx.WorkDir,
		Assignee:        "deacon",
		HeaderEmoji:     "🔄",
		HeaderTitle:     "Patrol Status (Wisp-based)",
		CheckInProgress: false,
		WorkLoopSteps: []string{
			"Check next step: `bd ready`",
			"Execute the step (heartbeat, mail, health checks, etc.)",
			"Close step: `bd close <step-id>`",
			"Check next: `bd ready`",
			"At cycle end (loop-or-exit step):\n   - Generate summary of patrol cycle\n   - Squash: `bd --no-daemon mol squash <mol-id> --summary \"<summary>\"`\n   - Loop back to create new wisp, or exit if context high",
		},
	}
	outputPatrolContext(cfg)
}

// outputWitnessPatrolContext shows patrol molecule status for the Witness.
// Witness AUTO-BONDS its patrol molecule on startup if one isn't already running.
func outputWitnessPatrolContext(ctx RoleContext) {
	cfg := PatrolConfig{
		RoleName:        "witness",
		PatrolMolName:   "mol-witness-patrol",
		BeadsDir:        ctx.WorkDir,
		Assignee:        ctx.Rig + "/witness",
		HeaderEmoji:     "👁",
		HeaderTitle:     "Witness Patrol Status",
		CheckInProgress: true,
		WorkLoopSteps: []string{
			"Check inbox: `gt mail inbox`",
			"Check next step: `bd ready`",
			"Execute the step (survey polecats, inspect, nudge, etc.)",
			"Close step: `bd close <step-id>`",
			"Check next: `bd ready`",
			"At cycle end (burn-or-loop step):\n   - Generate summary of patrol cycle\n   - Squash: `bd --no-daemon mol squash <mol-id> --summary \"<summary>\"`\n   - Loop back to create new wisp, or exit if context high",
		},
	}
	outputPatrolContext(cfg)
}

// outputRefineryPatrolContext shows patrol molecule status for the Refinery.
// Refinery AUTO-BONDS its patrol molecule on startup if one isn't already running.
func outputRefineryPatrolContext(ctx RoleContext) {
	cfg := PatrolConfig{
		RoleName:        "refinery",
		PatrolMolName:   "mol-refinery-patrol",
		BeadsDir:        ctx.WorkDir,
		Assignee:        ctx.Rig + "/refinery",
		HeaderEmoji:     "🔧",
		HeaderTitle:     "Refinery Patrol Status",
		CheckInProgress: true,
		WorkLoopSteps: []string{
			"Check inbox: `gt mail inbox`",
			"Check next step: `bd ready`",
			"Execute the step (queue scan, process branch, tests, merge)",
			"Close step: `bd close <step-id>`",
			"Check next: `bd ready`",
			"At cycle end (burn-or-loop step):\n   - Generate summary of patrol cycle\n   - Squash: `bd --no-daemon mol squash <mol-id> --summary \"<summary>\"`\n   - Loop back to create new wisp, or exit if context high",
		},
	}
	outputPatrolContext(cfg)
}

// checkSlungWork checks for hooked work on the agent's hook.
// If found, displays AUTONOMOUS WORK MODE and tells the agent to execute immediately.
// Returns true if hooked work was found (caller should skip normal startup directive).
func checkSlungWork(ctx RoleContext) bool {
	// Determine agent identity
	agentID := getAgentIdentity(ctx)
	if agentID == "" {
		return false
	}

	// Check for hooked beads (work on the agent's hook)
	b := beads.New(ctx.WorkDir)
	hookedBeads, err := b.List(beads.ListOptions{
		Status:   beads.StatusHooked,
		Assignee: agentID,
		Priority: -1,
	})
	if err != nil || len(hookedBeads) == 0 {
		// No hooked beads - no slung work
		return false
	}

	// Use the first hooked bead (agents typically have one)
	hookedBead := hookedBeads[0]

	// Build the role announcement string
	roleAnnounce := buildRoleAnnouncement(ctx)

	// Found hooked work! Display AUTONOMOUS MODE prominently
	fmt.Println()
	fmt.Printf("%s\n\n", style.Bold.Render("## 🚨 AUTONOMOUS WORK MODE 🚨"))
	fmt.Println("Work is on your hook. After announcing your role, begin IMMEDIATELY.")
	fmt.Println()
	fmt.Println("This is physics, not politeness. Gas Town is a steam engine - you are a piston.")
	fmt.Println("Every moment you wait is a moment the engine stalls. Other agents may be")
	fmt.Println("blocked waiting on YOUR output. The hook IS your assignment. RUN IT.")
	fmt.Println()
	fmt.Println("1. Announce: \"" + roleAnnounce + "\" (ONE line, no elaboration)")
	fmt.Printf("2. Then IMMEDIATELY run: `bd show %s`\n", hookedBead.ID)
	fmt.Println("3. Begin execution - no waiting for user input")
	fmt.Println()
	fmt.Println("**DO NOT:**")
	fmt.Println("- Wait for user response after announcing")
	fmt.Println("- Ask clarifying questions")
	fmt.Println("- Describe what you're going to do")
	fmt.Println("- Check mail first (hook takes priority)")
	fmt.Println()

	// Show the hooked work details
	fmt.Printf("%s\n\n", style.Bold.Render("## Hooked Work"))
	fmt.Printf("  Bead ID: %s\n", style.Bold.Render(hookedBead.ID))
	fmt.Printf("  Title: %s\n", hookedBead.Title)
	if hookedBead.Description != "" {
		// Show first few lines of description
		lines := strings.Split(hookedBead.Description, "\n")
		maxLines := 5
		if len(lines) > maxLines {
			lines = lines[:maxLines]
			lines = append(lines, "...")
		}
		fmt.Println("  Description:")
		for _, line := range lines {
			fmt.Printf("    %s\n", line)
		}
	}
	fmt.Println()

	// Show bead preview using bd show
	fmt.Println("**Bead details:**")
	cmd := exec.Command("bd", "show", hookedBead.ID)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	if cmd.Run() == nil {
		lines := strings.Split(stdout.String(), "\n")
		maxLines := 15
		if len(lines) > maxLines {
			lines = lines[:maxLines]
			lines = append(lines, "...")
		}
		for _, line := range lines {
			fmt.Printf("  %s\n", line)
		}
	}
	fmt.Println()

	return true
}

// buildRoleAnnouncement creates the role announcement string for autonomous mode.
func buildRoleAnnouncement(ctx RoleContext) string {
	switch ctx.Role {
	case RoleMayor:
		return "Mayor, checking in."
	case RoleDeacon:
		return "Deacon, checking in."
	case RoleWitness:
		return fmt.Sprintf("%s Witness, checking in.", ctx.Rig)
	case RoleRefinery:
		return fmt.Sprintf("%s Refinery, checking in.", ctx.Rig)
	case RolePolecat:
		return fmt.Sprintf("%s Polecat %s, checking in.", ctx.Rig, ctx.Polecat)
	case RoleCrew:
		return fmt.Sprintf("%s Crew %s, checking in.", ctx.Rig, ctx.Polecat)
	default:
		return "Agent, checking in."
	}
}

// getGitRoot returns the root of the current git repository.
func getGitRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// getAgentIdentity returns the agent identity string for hook lookup.
func getAgentIdentity(ctx RoleContext) string {
	switch ctx.Role {
	case RoleCrew:
		return fmt.Sprintf("%s/crew/%s", ctx.Rig, ctx.Polecat)
	case RolePolecat:
		return fmt.Sprintf("%s/polecats/%s", ctx.Rig, ctx.Polecat)
	case RoleMayor:
		return "mayor"
	case RoleDeacon:
		return "deacon"
	case RoleWitness:
		return fmt.Sprintf("%s/witness", ctx.Rig)
	case RoleRefinery:
		return fmt.Sprintf("%s/refinery", ctx.Rig)
	default:
		return ""
	}
}

// acquireIdentityLock checks and acquires the identity lock for worker roles.
// This prevents multiple agents from claiming the same worker identity.
// Returns an error if another agent already owns this identity.
func acquireIdentityLock(ctx RoleContext) error {
	// Only lock worker roles (polecat, crew)
	// Infrastructure roles (mayor, witness, refinery, deacon) are singletons
	// managed by tmux session names, so they don't need file-based locks
	if ctx.Role != RolePolecat && ctx.Role != RoleCrew {
		return nil
	}

	// Create lock for this worker directory
	l := lock.New(ctx.WorkDir)

	// Determine session ID from environment or context
	sessionID := os.Getenv("TMUX_PANE")
	if sessionID == "" {
		// Fall back to a descriptive identifier
		sessionID = fmt.Sprintf("%s/%s", ctx.Rig, ctx.Polecat)
	}

	// Try to acquire the lock
	if err := l.Acquire(sessionID); err != nil {
		if errors.Is(err, lock.ErrLocked) {
			// Another agent owns this identity
			fmt.Printf("\n%s\n\n", style.Bold.Render("⚠️  IDENTITY COLLISION DETECTED"))
			fmt.Printf("Another agent already claims this worker identity.\n\n")

			// Show lock details
			if info, readErr := l.Read(); readErr == nil {
				fmt.Printf("Lock holder:\n")
				fmt.Printf("  PID: %d\n", info.PID)
				fmt.Printf("  Session: %s\n", info.SessionID)
				fmt.Printf("  Acquired: %s\n", info.AcquiredAt.Format("2006-01-02 15:04:05"))
				fmt.Println()
			}

			fmt.Printf("To resolve:\n")
			fmt.Printf("  1. Find the other session and close it, OR\n")
			fmt.Printf("  2. Run: gt doctor --fix (cleans stale locks)\n")
			fmt.Printf("  3. If lock is stale: rm %s/.runtime/agent.lock\n", ctx.WorkDir)
			fmt.Println()

			return fmt.Errorf("cannot claim identity %s/%s: %w", ctx.Rig, ctx.Polecat, err)
		}
		return fmt.Errorf("acquiring identity lock: %w", err)
	}

	return nil
}

// reportAgentState updates the agent bead to report the agent's current state.
// This implements ZFC-compliant self-reporting of agent state.
// Agents call this on startup (running) and shutdown (stopped).
// For crew workers, creates the agent bead if it doesn't exist.
func reportAgentState(ctx RoleContext, state string) {
	agentBeadID := getAgentBeadID(ctx)
	if agentBeadID == "" {
		return
	}

	// Use the beads API directly to update agent state
	// This is more reliable than shelling out to bd
	bd := beads.New(ctx.WorkDir)

	// Check if agent bead exists, create if needed (especially for crew workers)
	if _, err := bd.Show(agentBeadID); err != nil {
		// Agent bead doesn't exist - create it
		fields := getAgentFields(ctx, state)
		if fields != nil {
			_, createErr := bd.CreateAgentBead(agentBeadID, agentBeadID, fields)
			if createErr != nil {
				// Silently ignore - beads might not be configured
				return
			}
			// Bead created with initial state, no need to update
			return
		}
	}

	// Update existing agent bead state
	if err := bd.UpdateAgentState(agentBeadID, state, nil); err != nil {
		// Silently ignore errors - don't fail prime if state reporting fails
		return
	}
}

// getAgentFields returns the AgentFields for creating a new agent bead.
func getAgentFields(ctx RoleContext, state string) *beads.AgentFields {
	switch ctx.Role {
	case RoleCrew:
		return &beads.AgentFields{
			RoleType:   "crew",
			Rig:        ctx.Rig,
			AgentState: state,
			RoleBead:   "gt-crew-role",
		}
	case RolePolecat:
		return &beads.AgentFields{
			RoleType:   "polecat",
			Rig:        ctx.Rig,
			AgentState: state,
			RoleBead:   "gt-polecat-role",
		}
	case RoleMayor:
		return &beads.AgentFields{
			RoleType:   "mayor",
			AgentState: state,
			RoleBead:   "gt-mayor-role",
		}
	case RoleDeacon:
		return &beads.AgentFields{
			RoleType:   "deacon",
			AgentState: state,
			RoleBead:   "gt-deacon-role",
		}
	case RoleWitness:
		return &beads.AgentFields{
			RoleType:   "witness",
			Rig:        ctx.Rig,
			AgentState: state,
			RoleBead:   "gt-witness-role",
		}
	case RoleRefinery:
		return &beads.AgentFields{
			RoleType:   "refinery",
			Rig:        ctx.Rig,
			AgentState: state,
			RoleBead:   "gt-refinery-role",
		}
	default:
		return nil
	}
}

// getAgentBeadID returns the agent bead ID for the current role.
// Uses canonical naming: prefix-rig-role-name
// Returns empty string for unknown roles.
func getAgentBeadID(ctx RoleContext) string {
	switch ctx.Role {
	case RoleMayor:
		return beads.MayorBeadID()
	case RoleDeacon:
		return beads.DeaconBeadID()
	case RoleWitness:
		if ctx.Rig != "" {
			return beads.WitnessBeadID(ctx.Rig)
		}
		return ""
	case RoleRefinery:
		if ctx.Rig != "" {
			return beads.RefineryBeadID(ctx.Rig)
		}
		return ""
	case RolePolecat:
		if ctx.Rig != "" && ctx.Polecat != "" {
			return beads.PolecatBeadID(ctx.Rig, ctx.Polecat)
		}
		return ""
	case RoleCrew:
		if ctx.Rig != "" && ctx.Polecat != "" {
			return beads.CrewBeadID(ctx.Rig, ctx.Polecat)
		}
		return ""
	default:
		return ""
	}
}

// ensureBeadsRedirect ensures the .beads/redirect file exists for worktree-based roles.
// This handles cases where git clean or other operations delete the redirect file.
func ensureBeadsRedirect(ctx RoleContext) {
	// Only applies to crew and polecat roles (they use shared beads)
	if ctx.Role != RoleCrew && ctx.Role != RolePolecat {
		return
	}

	// Check if redirect already exists
	beadsDir := filepath.Join(ctx.WorkDir, ".beads")
	redirectPath := filepath.Join(beadsDir, "redirect")

	if _, err := os.Stat(redirectPath); err == nil {
		// Redirect exists, nothing to do
		return
	}

	// Determine the correct redirect path based on role and rig structure
	var redirectContent string

	// Get the rig root (parent of crew/ or polecats/)
	var rigRoot string
	relPath, err := filepath.Rel(ctx.TownRoot, ctx.WorkDir)
	if err != nil {
		return
	}
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	if len(parts) >= 1 {
		rigRoot = filepath.Join(ctx.TownRoot, parts[0])
	} else {
		return
	}

	// Check for shared beads locations in order of preference:
	// 1. rig/mayor/rig/.beads/ (if mayor rig clone exists)
	// 2. rig/.beads/ (rig root beads)
	mayorRigBeads := filepath.Join(rigRoot, "mayor", "rig", ".beads")
	rigRootBeads := filepath.Join(rigRoot, ".beads")

	if _, err := os.Stat(mayorRigBeads); err == nil {
		// Use mayor/rig/.beads
		if ctx.Role == RoleCrew {
			// crew/<name>/.beads -> ../../mayor/rig/.beads
			redirectContent = "../../mayor/rig/.beads"
		} else {
			// polecats/<name>/.beads -> ../../mayor/rig/.beads
			redirectContent = "../../mayor/rig/.beads"
		}
	} else if _, err := os.Stat(rigRootBeads); err == nil {
		// Use rig root .beads
		if ctx.Role == RoleCrew {
			// crew/<name>/.beads -> ../../.beads
			redirectContent = "../../.beads"
		} else {
			// polecats/<name>/.beads -> ../../.beads
			redirectContent = "../../.beads"
		}
	} else {
		// No shared beads found, nothing to redirect to
		return
	}

	// Create .beads directory if needed
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		// Silently fail - not critical
		return
	}

	// Write redirect file
	if err := os.WriteFile(redirectPath, []byte(redirectContent+"\n"), 0644); err != nil {
		// Silently fail - not critical
		return
	}

	// Note: We don't print a message here to avoid cluttering prime output
	// The redirect is silently restored
}
