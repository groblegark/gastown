package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/bdcmd"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/terminal"
	"github.com/steveyegge/gastown/internal/workspace"
)

// beadInfo holds status and assignee for a bead.
type beadInfo struct {
	Title    string `json:"title"`
	Status   string `json:"status"`
	Assignee string `json:"assignee"`
}

// verifyBeadExists checks that the bead exists using bd show.
// Uses bd's native prefix-based routing via routes.jsonl - do NOT set BEADS_DIR
// as that overrides routing and breaks resolution of rig-level beads.
//
// Uses --allow-stale to find beads when database is out of sync.
// For existence checks, stale data is acceptable - we just need to know it exists.
func verifyBeadExists(beadID string) error {
	cmd := bdcmd.Command( "show", beadID, "--json", "--allow-stale")
	// Run from town root so bd can find routes.jsonl for prefix-based routing.
	// Do NOT set BEADS_DIR - that overrides routing and breaks rig bead resolution.
	if townRoot, err := workspace.FindFromCwd(); err == nil {
		cmd.Dir = townRoot
	}
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("bead '%s' not found (bd show failed)", beadID)
	}
	if len(out) == 0 {
		return fmt.Errorf("bead '%s' not found (empty response)", beadID)
	}
	return nil
}

// getBeadInfo returns status and assignee for a bead.
// Uses bd's native prefix-based routing via routes.jsonl.
// Uses --allow-stale for consistency with verifyBeadExists.
func getBeadInfo(beadID string) (*beadInfo, error) {
	cmd := bdcmd.Command( "show", beadID, "--json", "--allow-stale")
	// Run from town root so bd can find routes.jsonl for prefix-based routing.
	if townRoot, err := workspace.FindFromCwd(); err == nil {
		cmd.Dir = townRoot
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("bead '%s' not found", beadID)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("bead '%s' not found (empty response)", beadID)
	}
	// bd show --json returns an array (issue + dependents), take first element
	var infos []beadInfo
	if err := json.Unmarshal(out, &infos); err != nil {
		return nil, fmt.Errorf("parsing bead info: %w", err)
	}
	if len(infos) == 0 {
		return nil, fmt.Errorf("bead '%s' not found", beadID)
	}
	return &infos[0], nil
}

// storeArgsInBead stores args in the bead's description using attached_args field.
// This enables no-tmux mode where agents discover args via gt prime / bd show.
func storeArgsInBead(beadID, args string) error {
	// Get the bead to preserve existing description content
	showCmd := bdcmd.Command( "show", beadID, "--json", "--allow-stale")
	out, err := showCmd.Output()
	if err != nil {
		return fmt.Errorf("fetching bead: %w", err)
	}
	if len(out) == 0 {
		return fmt.Errorf("bead not found (empty response)")
	}

	// Parse the bead
	var issues []beads.Issue
	if err := json.Unmarshal(out, &issues); err != nil {
		if os.Getenv("GT_TEST_ATTACHED_MOLECULE_LOG") == "" {
			return fmt.Errorf("parsing bead: %w", err)
		}
	}
	issue := &beads.Issue{}
	if len(issues) > 0 {
		issue = &issues[0]
	} else if os.Getenv("GT_TEST_ATTACHED_MOLECULE_LOG") == "" {
		return fmt.Errorf("bead not found")
	}

	// Get or create attachment fields
	fields := beads.ParseAttachmentFields(issue)
	if fields == nil {
		fields = &beads.AttachmentFields{}
	}

	// Set the args
	fields.AttachedArgs = args

	// Update the description
	newDesc := beads.SetAttachmentFields(issue, fields)
	if logPath := os.Getenv("GT_TEST_ATTACHED_MOLECULE_LOG"); logPath != "" {
		_ = os.WriteFile(logPath, []byte(newDesc), 0644)
	}

	// Update the bead
	updateCmd := bdcmd.Command( "update", beadID, "--description="+newDesc)
	updateCmd.Stderr = os.Stderr
	if err := updateCmd.Run(); err != nil {
		return fmt.Errorf("updating bead description: %w", err)
	}

	return nil
}

// storeDispatcherInBead stores the dispatcher agent ID in the bead's description.
// This enables polecats to notify the dispatcher when work is complete.
func storeDispatcherInBead(beadID, dispatcher string) error {
	if dispatcher == "" {
		return nil
	}

	// Get the bead to preserve existing description content
	showCmd := bdcmd.Command( "show", beadID, "--json")
	out, err := showCmd.Output()
	if err != nil {
		return fmt.Errorf("fetching bead: %w", err)
	}

	// Parse the bead
	var issues []beads.Issue
	if err := json.Unmarshal(out, &issues); err != nil {
		return fmt.Errorf("parsing bead: %w", err)
	}
	if len(issues) == 0 {
		return fmt.Errorf("bead not found")
	}
	issue := &issues[0]

	// Get or create attachment fields
	fields := beads.ParseAttachmentFields(issue)
	if fields == nil {
		fields = &beads.AttachmentFields{}
	}

	// Set the dispatcher
	fields.DispatchedBy = dispatcher

	// Update the description
	newDesc := beads.SetAttachmentFields(issue, fields)

	// Update the bead
	updateCmd := bdcmd.Command( "update", beadID, "--description="+newDesc)
	updateCmd.Stderr = os.Stderr
	if err := updateCmd.Run(); err != nil {
		return fmt.Errorf("updating bead description: %w", err)
	}

	return nil
}

// storeAttachedMoleculeInBead sets the attached_molecule field in a bead's description.
// This is required for gt hook to recognize that a molecule is attached to the bead.
// Called after bonding a formula wisp to a bead via "gt sling <formula> --on <bead>".
func storeAttachedMoleculeInBead(beadID, moleculeID string) error {
	if moleculeID == "" {
		return nil
	}
	logPath := os.Getenv("GT_TEST_ATTACHED_MOLECULE_LOG")
	if logPath != "" {
		_ = os.WriteFile(logPath, []byte("called"), 0644)
	}

	issue := &beads.Issue{}
	if logPath == "" {
		// Get the bead to preserve existing description content
		showCmd := bdcmd.Command( "show", beadID, "--json")
		out, err := showCmd.Output()
		if err != nil {
			return fmt.Errorf("fetching bead: %w", err)
		}

		// Parse the bead
		var issues []beads.Issue
		if err := json.Unmarshal(out, &issues); err != nil {
			return fmt.Errorf("parsing bead: %w", err)
		}
		if len(issues) == 0 {
			return fmt.Errorf("bead not found")
		}
		issue = &issues[0]
	}

	// Get or create attachment fields
	fields := beads.ParseAttachmentFields(issue)
	if fields == nil {
		fields = &beads.AttachmentFields{}
	}

	// Set the attached molecule
	fields.AttachedMolecule = moleculeID
	if fields.AttachedAt == "" {
		fields.AttachedAt = time.Now().UTC().Format(time.RFC3339)
	}

	// Update the description
	newDesc := beads.SetAttachmentFields(issue, fields)
	if logPath != "" {
		_ = os.WriteFile(logPath, []byte(newDesc), 0644)
	}

	// Update the bead
	updateCmd := bdcmd.Command( "update", beadID, "--description="+newDesc)
	updateCmd.Stderr = os.Stderr
	if err := updateCmd.Run(); err != nil {
		return fmt.Errorf("updating bead description: %w", err)
	}

	return nil
}

// storeNoMergeInBead sets the no_merge field in a bead's description.
// When set, gt done will skip the merge queue and keep work on the feature branch.
// This is useful for upstream contributions or when human review is needed before merge.
func storeNoMergeInBead(beadID string, noMerge bool) error {
	if !noMerge {
		return nil
	}

	// Get the bead to preserve existing description content
	showCmd := bdcmd.Command( "show", beadID, "--json")
	out, err := showCmd.Output()
	if err != nil {
		return fmt.Errorf("fetching bead: %w", err)
	}

	// Parse the bead
	var issues []beads.Issue
	if err := json.Unmarshal(out, &issues); err != nil {
		return fmt.Errorf("parsing bead: %w", err)
	}
	if len(issues) == 0 {
		return fmt.Errorf("bead not found")
	}
	issue := &issues[0]

	// Get or create attachment fields
	fields := beads.ParseAttachmentFields(issue)
	if fields == nil {
		fields = &beads.AttachmentFields{}
	}

	// Set the no_merge flag
	fields.NoMerge = true

	// Update the description
	newDesc := beads.SetAttachmentFields(issue, fields)

	// Update the bead
	updateCmd := bdcmd.Command( "update", beadID, "--description="+newDesc)
	updateCmd.Stderr = os.Stderr
	if err := updateCmd.Run(); err != nil {
		return fmt.Errorf("updating bead description: %w", err)
	}

	return nil
}

// storeMergeStrategyInBead sets the merge_strategy field in a bead's description.
// Valid strategies: direct (push to main), mr (refinery), local (merge locally).
func storeMergeStrategyInBead(beadID, strategy string) error {
	if strategy == "" {
		return nil
	}

	// Get the bead to preserve existing description content
	showCmd := bdcmd.Command( "show", beadID, "--json")
	out, err := showCmd.Output()
	if err != nil {
		return fmt.Errorf("fetching bead: %w", err)
	}

	// Parse the bead
	var issues []beads.Issue
	if err := json.Unmarshal(out, &issues); err != nil {
		return fmt.Errorf("parsing bead: %w", err)
	}
	if len(issues) == 0 {
		return fmt.Errorf("bead not found")
	}
	issue := &issues[0]

	// Get or create attachment fields
	fields := beads.ParseAttachmentFields(issue)
	if fields == nil {
		fields = &beads.AttachmentFields{}
	}

	// Set the merge strategy
	fields.MergeStrategy = strategy

	// Update the description
	newDesc := beads.SetAttachmentFields(issue, fields)

	// Update the bead
	updateCmd := bdcmd.Command( "update", beadID, "--description="+newDesc)
	updateCmd.Stderr = os.Stderr
	if err := updateCmd.Run(); err != nil {
		return fmt.Errorf("updating bead description: %w", err)
	}

	return nil
}

// storeConvoyOwnedInBead sets the convoy_owned field in a bead's description.
// When set, the convoy is caller-managed and won't be auto-processed by witness/refinery.
func storeConvoyOwnedInBead(beadID string, owned bool) error {
	if !owned {
		return nil
	}

	// Get the bead to preserve existing description content
	showCmd := bdcmd.Command( "show", beadID, "--json")
	out, err := showCmd.Output()
	if err != nil {
		return fmt.Errorf("fetching bead: %w", err)
	}

	// Parse the bead
	var issues []beads.Issue
	if err := json.Unmarshal(out, &issues); err != nil {
		return fmt.Errorf("parsing bead: %w", err)
	}
	if len(issues) == 0 {
		return fmt.Errorf("bead not found")
	}
	issue := &issues[0]

	// Get or create attachment fields
	fields := beads.ParseAttachmentFields(issue)
	if fields == nil {
		fields = &beads.AttachmentFields{}
	}

	// Set the convoy_owned flag
	fields.ConvoyOwned = true

	// Update the description
	newDesc := beads.SetAttachmentFields(issue, fields)

	// Update the bead
	updateCmd := bdcmd.Command( "update", beadID, "--description="+newDesc)
	updateCmd.Stderr = os.Stderr
	if err := updateCmd.Run(); err != nil {
		return fmt.Errorf("updating bead description: %w", err)
	}

	return nil
}

// nudgeViaBackend attempts to nudge a non-tmux agent (Coop/K8s) via the Backend interface.
// Returns true if the nudge was sent successfully.
func nudgeViaBackend(agentID, beadID, subject, args string) bool {
	backend := terminal.ResolveBackend(agentID)
	if _, isCoop := backend.(*terminal.CoopBackend); !isCoop {
		return false // Unknown backend type — caller should use pane-based nudge
	}

	// Build the same prompt as injectStartPrompt
	var prompt string
	if args != "" {
		if subject != "" {
			prompt = fmt.Sprintf("Work slung: %s (%s). Args: %s. Start working now - use these args to guide your execution.", beadID, subject, args)
		} else {
			prompt = fmt.Sprintf("Work slung: %s. Args: %s. Start working now - use these args to guide your execution.", beadID, args)
		}
	} else if subject != "" {
		prompt = fmt.Sprintf("Work slung: %s (%s). Start working on it now - no questions, just begin.", beadID, subject)
	} else {
		prompt = fmt.Sprintf("Work slung: %s. Start working on it now - run `gt hook` to see the hook, then begin.", beadID)
	}

	// Use "claude" as the session name — matches CoopBackend.AddSession convention
	if err := backend.NudgeSession("claude", prompt); err != nil {
		return false
	}
	return true
}

// detectCloneRoot finds the root of the current git clone.
func detectCloneRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not in a git repository")
	}
	return strings.TrimSpace(string(out)), nil
}

// detectActor returns the current agent's actor string for event logging.
func detectActor() string {
	roleInfo, err := GetRole()
	if err != nil {
		return "unknown"
	}
	return roleInfo.ActorString()
}

// agentIDToBeadID converts an agent ID to its corresponding agent bead ID.
// Uses canonical naming for town-level agent beads: hq-<town>-<rig>-<role>-<name>
// All agent beads are now stored in town beads with hq- prefix (fixes gt-w7sr31).
// This ensures consistent lookup regardless of which rig's database is active.
// townRoot is needed to look up the town name for the ID.
func agentIDToBeadID(agentID, townRoot string) string {
	// Normalize: strip trailing slash (resolveSelfTarget returns "mayor/" not "mayor")
	agentID = strings.TrimSuffix(agentID, "/")

	// Handle simple cases (town-level agents with hq- prefix)
	if agentID == "mayor" {
		return beads.MayorBeadIDTown()
	}
	if agentID == "deacon" {
		return beads.DeaconBeadIDTown()
	}

	// Parse path-style agent IDs
	parts := strings.Split(agentID, "/")
	if len(parts) < 2 {
		return ""
	}

	rig := parts[0]

	// Get town name for town-level agent bead IDs (gt-w7sr31)
	// All rig-level agents now use hq-<town>-<rig>-<role>[-<name>] format
	townName, err := workspace.GetTownName(townRoot)
	if err != nil {
		// Fall back to empty town name (generates hq-<rig>-<role>-<name>)
		townName = ""
	}

	switch {
	case len(parts) == 2 && parts[1] == "witness":
		return beads.WitnessBeadIDTown(townName, rig)
	case len(parts) == 2 && parts[1] == "refinery":
		return beads.RefineryBeadIDTown(townName, rig)
	case len(parts) == 3 && parts[1] == "crew":
		return beads.CrewBeadIDTown(townName, rig, parts[2])
	case len(parts) == 3 && parts[1] == "polecats":
		prefix := beads.GetPrefixForRig(townRoot, rig)
		return beads.PolecatBeadIDWithPrefix(prefix, rig, parts[2])
	case len(parts) == 3 && parts[0] == "deacon" && parts[1] == "dogs":
		// Dogs are town-level agents with hq- prefix
		return beads.DogBeadIDTown(parts[2])
	default:
		return ""
	}
}

// updateAgentHookBead updates the agent bead's state and hook when work is slung.
// This enables the witness to see that each agent is working.
//
// We run from the polecat's workDir (which redirects to the rig's beads database)
// WITHOUT setting BEADS_DIR, so the redirect mechanism works for gt-* agent beads.
//
// For rig-level beads (same database), we set the hook_bead slot directly.
// For cross-database scenarios (agent in rig db, hook bead in town db),
// the slot set may fail - this is handled gracefully with a warning.
// The work is still correctly attached via `bd update <bead> --assignee=<agent>`.
func updateAgentHookBead(agentID, beadID, workDir, townBeadsDir string) {
	_ = townBeadsDir // Not used - BEADS_DIR breaks redirect mechanism

	// Determine the directory to run bd commands from:
	// - If workDir is provided (polecat's clone path), use it for redirect-based routing
	// - Otherwise fall back to town root
	bdWorkDir := workDir
	townRoot, err := workspace.FindFromCwd()
	if err != nil {
		// Not in a Gas Town workspace - can't update agent bead
		fmt.Fprintf(os.Stderr, "Warning: couldn't find town root to update agent hook: %v\n", err)
		return
	}
	if bdWorkDir == "" {
		bdWorkDir = townRoot
	}

	// Convert agent ID to agent bead ID
	// Format examples (canonical: prefix-rig-role-name):
	//   greenplace/crew/max -> gt-greenplace-crew-max
	//   greenplace/polecats/Toast -> gt-greenplace-polecat-Toast
	//   mayor -> hq-mayor
	//   greenplace/witness -> gt-greenplace-witness
	agentBeadID := agentIDToBeadID(agentID, townRoot)
	if agentBeadID == "" {
		return
	}

	// Resolve the correct working directory for the agent bead.
	// Agent beads with rig-level prefixes (e.g., go-) live in rig databases,
	// not the town database. Use prefix-based resolution to find the correct path.
	// This fixes go-19z: bd slot commands failing for go-* prefixed beads.
	agentWorkDir := beads.ResolveHookDir(townRoot, agentBeadID, bdWorkDir)

	// Run from agentWorkDir WITHOUT BEADS_DIR to enable redirect-based routing.
	// Set hook_bead to the slung work (gt-zecmc: removed agent_state update).
	// Agent liveness is observable from tmux - no need to record it in bead.
	// For cross-database scenarios, slot set may fail gracefully (warning only).
	bd := beads.New(agentWorkDir)
	if err := bd.SetHookBead(agentBeadID, beadID); err != nil {
		// Log warning instead of silent ignore - helps debug cross-beads issues
		fmt.Fprintf(os.Stderr, "Warning: couldn't set agent %s hook: %v\n", agentBeadID, err)
		// Dogs created before canonical IDs need recreation: gt dog rm <name> && gt dog add <name>
		if strings.Contains(agentBeadID, "-dog-") {
			fmt.Fprintf(os.Stderr, "  (Old dog? Recreate with: gt dog rm <name> && gt dog add <name>)\n")
		}
		return
	}
}

// wakeRigAgents wakes the witness for a rig after polecat dispatch.
// This ensures the witness is ready to monitor. The refinery is nudged
// separately when an MR is actually created (by nudgeRefinery).
func wakeRigAgents(rigName string) {
	// Boot the rig (idempotent - no-op if already running)
	_ = runRigBoot(nil, []string{rigName}) // Ignore errors - rig might already be running

	// Nudge witness to clear any backoff
	witnessSession := fmt.Sprintf("gt-%s-witness", rigName)
	backend, sessionKey := resolveBackendForSession(witnessSession)

	// Silent nudge - session might not exist yet
	_ = backend.NudgeSession(sessionKey, "Polecat dispatched - check for work")
}

// nudgeRefinery wakes the refinery for a rig after an MR is created.
// This ensures the refinery picks up the new merge request promptly
// instead of waiting for its next poll cycle.
func nudgeRefinery(rigName, message string) {
	refinerySession := fmt.Sprintf("gt-%s-refinery", rigName)

	// Test hook: log nudge for test observability (same pattern as GT_TEST_ATTACHED_MOLECULE_LOG)
	if logPath := os.Getenv("GT_TEST_NUDGE_LOG"); logPath != "" {
		entry := fmt.Sprintf("nudge:%s:%s\n", refinerySession, message)
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err == nil {
			_, _ = f.WriteString(entry)
			_ = f.Close()
		}
		return // Don't actually nudge tmux in tests
	}

	refBackend, refSessionKey := resolveBackendForSession(refinerySession)
	_ = refBackend.NudgeSession(refSessionKey, message)
}

// isPolecatTarget checks if the target string refers to a polecat.
// Returns true if the target format is "rig/polecats/name".
// This is used to determine if we should respawn a dead polecat
// instead of failing when slinging work.
func isPolecatTarget(target string) bool {
	parts := strings.Split(target, "/")
	return len(parts) >= 3 && parts[1] == "polecats"
}

// parseCrewTarget parses a crew target string and returns the rig name and crew name.
// Returns (rigName, crewName, true) if the target is a valid crew target format "rig/crew/name".
// Returns ("", "", false) otherwise.
// This is used to detect and auto-start stopped crew sessions when slinging work.
func parseCrewTarget(target string) (rigName, crewName string, ok bool) {
	parts := strings.Split(target, "/")
	if len(parts) == 3 && parts[1] == "crew" {
		return parts[0], parts[2], true
	}
	return "", "", false
}

// FormulaOnBeadResult contains the result of instantiating a formula on a bead.
type FormulaOnBeadResult struct {
	WispRootID string // The wisp root ID (compound root after bonding)
	BeadToHook string // The bead ID to hook (BASE bead, not wisp - lifecycle fix)
}

// InstantiateFormulaOnBead creates a wisp from a formula, bonds it to a bead.
// This is the formula-on-bead pattern used by issue #288 for auto-applying mol-polecat-work.
//
// Parameters:
//   - formulaName: the formula to instantiate (e.g., "mol-polecat-work")
//   - beadID: the base bead to bond the wisp to
//   - title: the bead title (used for --var feature=<title>)
//   - hookWorkDir: working directory for bd commands (polecat's worktree)
//   - townRoot: the town root directory
//   - skipCook: if true, skip cooking (for batch mode optimization where cook happens once)
//   - extraVars: additional --var values supplied by the user
//
// Returns the wisp root ID which should be hooked.
func InstantiateFormulaOnBead(formulaName, beadID, title, hookWorkDir, townRoot string, skipCook bool, extraVars []string) (*FormulaOnBeadResult, error) {
	// Route bd mutations (wisp/bond) to the correct beads context for the target bead.
	formulaWorkDir := beads.ResolveHookDir(townRoot, beadID, hookWorkDir)

	// Step 1: Cook the formula (ensures proto exists)
	if !skipCook {
		cookCmd := bdcmd.Command( "cook", formulaName)
		cookCmd.Dir = formulaWorkDir
		cookCmd.Stderr = os.Stderr
		if err := cookCmd.Run(); err != nil {
			return nil, fmt.Errorf("cooking formula %s: %w", formulaName, err)
		}
	}

	// Step 2: Create wisp with feature and issue variables from bead
	featureVar := fmt.Sprintf("feature=%s", title)
	issueVar := fmt.Sprintf("issue=%s", beadID)
	wispArgs := []string{"mol", "wisp", formulaName, "--var", featureVar, "--var", issueVar}
	for _, variable := range extraVars {
		wispArgs = append(wispArgs, "--var", variable)
	}
	wispArgs = append(wispArgs, "--json")
	wispCmd := bdcmd.Command( wispArgs...)
	wispCmd.Dir = formulaWorkDir
	wispCmd.Env = append(os.Environ(), "GT_ROOT="+townRoot)
	wispCmd.Stderr = os.Stderr
	wispOut, err := wispCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("creating wisp for formula %s: %w", formulaName, err)
	}

	// Parse wisp output to get the root ID
	wispRootID, err := parseWispIDFromJSON(wispOut)
	if err != nil {
		return nil, fmt.Errorf("parsing wisp output: %w", err)
	}

	// Step 3: Bond wisp to original bead (creates compound)
	bondArgs := []string{"mol", "bond", wispRootID, beadID, "--json"}
	bondCmd := bdcmd.Command( bondArgs...)
	bondCmd.Dir = formulaWorkDir
	bondCmd.Stderr = os.Stderr
	bondOut, err := bondCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("bonding formula to bead: %w", err)
	}

	// Parse bond output - the wisp root becomes the compound root
	var bondResult struct {
		RootID string `json:"root_id"`
	}
	if err := json.Unmarshal(bondOut, &bondResult); err == nil && bondResult.RootID != "" {
		wispRootID = bondResult.RootID
	}

	return &FormulaOnBeadResult{
		WispRootID: wispRootID,
		BeadToHook: beadID, // Hook the BASE bead (lifecycle fix: wisp is attached_molecule)
	}, nil
}

// CookFormula cooks a formula to ensure its proto exists.
// This is useful for batch mode where we cook once before processing multiple beads.
func CookFormula(formulaName, workDir string) error {
	cookCmd := bdcmd.Command( "cook", formulaName)
	cookCmd.Dir = workDir
	cookCmd.Stderr = os.Stderr
	return cookCmd.Run()
}

// looksLikeBeadID checks if a string looks like a bead ID.
// Bead IDs have format: prefix-xxxx where prefix is 1-5 lowercase letters and xxxx is alphanumeric.
// Examples: "gt-abc123", "bd-ka761", "hq-cv-abc", "beads-xyz", "ap-qtsup.16"
func looksLikeBeadID(s string) bool {
	// Find the first hyphen
	idx := strings.Index(s, "-")
	if idx < 1 || idx > 5 {
		// No hyphen, or prefix is empty/too long
		return false
	}

	// Check prefix is all lowercase letters
	prefix := s[:idx]
	for _, c := range prefix {
		if c < 'a' || c > 'z' {
			return false
		}
	}

	// Check there's something after the hyphen
	rest := s[idx+1:]
	if len(rest) == 0 {
		return false
	}

	// Check rest starts with alphanumeric and contains only alphanumeric, dots, hyphens
	first := rest[0]
	if !((first >= 'a' && first <= 'z') || (first >= '0' && first <= '9')) {
		return false
	}

	return true
}

// resolveRoleToSession converts a role name or path to a tmux session name.
// Accepts:
//   - Role shortcuts: "crew", "witness", "refinery", "mayor", "deacon"
//   - Full paths: "<rig>/crew/<name>", "<rig>/witness", "<rig>/refinery"
//   - Direct session names (passed through)
//
// For role shortcuts that need context (crew, witness, refinery), it auto-detects from environment.
func resolveRoleToSession(role string) (string, error) {
	// First, check if it's a path format (contains /)
	if strings.Contains(role, "/") {
		return resolvePathToSession(role)
	}

	switch strings.ToLower(role) {
	case "mayor", "may":
		return getMayorSessionName(), nil

	case "deacon", "dea":
		return session.DeaconSessionName(), nil

	case "crew":
		// Try to get rig and crew name from environment or cwd
		rig := os.Getenv("GT_RIG")
		crewName := os.Getenv("GT_CREW")
		if rig == "" || crewName == "" {
			// Try to detect from cwd
			detected, err := detectCrewFromCwd()
			if err == nil {
				rig = detected.rigName
				crewName = detected.crewName
			}
		}
		if rig == "" || crewName == "" {
			return "", fmt.Errorf("cannot determine crew identity - run from crew directory or specify GT_RIG/GT_CREW")
		}
		return fmt.Sprintf("gt-%s-crew-%s", rig, crewName), nil

	case "witness", "wit":
		rig := os.Getenv("GT_RIG")
		if rig == "" {
			return "", fmt.Errorf("cannot determine rig - set GT_RIG or run from rig context")
		}
		return fmt.Sprintf("gt-%s-witness", rig), nil

	case "refinery", "ref":
		rig := os.Getenv("GT_RIG")
		if rig == "" {
			return "", fmt.Errorf("cannot determine rig - set GT_RIG or run from rig context")
		}
		return fmt.Sprintf("gt-%s-refinery", rig), nil

	default:
		// FIX (hq-cc7214.25): Check if the name is a crew member in any rig
		// Before assuming it's a direct session name, scan all rigs for crew/<name>
		townRoot := detectTownRootFromCwd()
		if townRoot != "" {
			rigs, err := os.ReadDir(townRoot)
			if err == nil {
				for _, rigEntry := range rigs {
					if !rigEntry.IsDir() || strings.HasPrefix(rigEntry.Name(), ".") {
						continue
					}
					crewPath := filepath.Join(townRoot, rigEntry.Name(), "crew", role)
					if info, err := os.Stat(crewPath); err == nil && info.IsDir() {
						// Found a crew member with this name
						return fmt.Sprintf("gt-%s-crew-%s", rigEntry.Name(), role), nil
					}
				}
			}
		}
		// Not a crew member - assume it's a direct session name (e.g., gt-gastown-crew-max)
		return role, nil
	}
}

// resolvePathToSession converts a path like "<rig>/crew/<name>" to a session name.
// Supported formats:
//   - <rig>/crew/<name> -> gt-<rig>-crew-<name>
//   - <rig>/witness -> gt-<rig>-witness
//   - <rig>/refinery -> gt-<rig>-refinery
//   - <rig>/polecats/<name> -> gt-<rig>-<name> (explicit polecat)
//   - <rig>/<name> -> gt-<rig>-<name> (polecat shorthand, if name isn't a known role)
func resolvePathToSession(path string) (string, error) {
	parts := strings.Split(path, "/")

	// Handle <rig>/crew/<name> format
	if len(parts) == 3 && parts[1] == "crew" {
		rig := parts[0]
		name := parts[2]
		return fmt.Sprintf("gt-%s-crew-%s", rig, name), nil
	}

	// Handle <rig>/polecats/<name> format (explicit polecat path)
	if len(parts) == 3 && parts[1] == "polecats" {
		rig := parts[0]
		name := strings.ToLower(parts[2]) // normalize polecat name
		return fmt.Sprintf("gt-%s-%s", rig, name), nil
	}

	// Handle <rig>/<role-or-polecat> format
	if len(parts) == 2 {
		rig := parts[0]
		second := parts[1]
		secondLower := strings.ToLower(second)

		// Check for known roles first
		switch secondLower {
		case "witness":
			return fmt.Sprintf("gt-%s-witness", rig), nil
		case "refinery":
			return fmt.Sprintf("gt-%s-refinery", rig), nil
		case "crew":
			// Just "<rig>/crew" without a name - need more info
			return "", fmt.Errorf("crew path requires name: %s/crew/<name>", rig)
		case "polecats":
			// Just "<rig>/polecats" without a name - need more info
			return "", fmt.Errorf("polecats path requires name: %s/polecats/<name>", rig)
		default:
			// Not a known role - check if it's a crew member before assuming polecat.
			// Crew members exist at <townRoot>/<rig>/crew/<name>.
			// This fixes: gt sling gt-375 gastown/max failing because max is crew, not polecat.
			townRoot := detectTownRootFromCwd()
			if townRoot != "" {
				crewPath := filepath.Join(townRoot, rig, "crew", second)
				if info, err := os.Stat(crewPath); err == nil && info.IsDir() {
					return fmt.Sprintf("gt-%s-crew-%s", rig, second), nil
				}
			}
			// Not a crew member - treat as polecat name (e.g., gastown/nux)
			return fmt.Sprintf("gt-%s-%s", rig, secondLower), nil
		}
	}

	return "", fmt.Errorf("cannot parse path '%s' - expected <rig>/<polecat>, <rig>/crew/<name>, <rig>/witness, or <rig>/refinery", path)
}

// detectTownRootFromCwd walks up from the current directory to find the town root.
// Falls back to GT_TOWN_ROOT or GT_ROOT env vars if cwd detection fails (broken state recovery).
func detectTownRootFromCwd() string {
	// Use workspace.FindFromCwd which handles both primary (mayor/town.json)
	// and secondary (mayor/ directory) markers
	townRoot, err := workspace.FindFromCwd()
	if err == nil && townRoot != "" {
		return townRoot
	}

	// Fallback: try environment variables for town root
	// GT_TOWN_ROOT is set by shell integration, GT_ROOT is set by session manager
	// This enables handoff to work even when cwd detection fails due to
	// detached HEAD, wrong branch, deleted worktree, etc.
	for _, envName := range []string{"GT_TOWN_ROOT", "GT_ROOT"} {
		if envRoot := os.Getenv(envName); envRoot != "" {
			// Verify it's actually a workspace
			if _, statErr := os.Stat(filepath.Join(envRoot, workspace.PrimaryMarker)); statErr == nil {
				return envRoot
			}
			// Try secondary marker too
			if info, statErr := os.Stat(filepath.Join(envRoot, workspace.SecondaryMarker)); statErr == nil && info.IsDir() {
				return envRoot
			}
		}
	}

	return ""
}

// getCurrentTmuxSession returns the current session name.
// Uses GT_SESSION or TMUX_SESSION env vars (K8s-native).
func getCurrentTmuxSession() (string, error) {
	if s := os.Getenv("GT_SESSION"); s != "" {
		return s, nil
	}
	if s := os.Getenv("TMUX_SESSION"); s != "" {
		return s, nil
	}
	return "", fmt.Errorf("GT_SESSION or TMUX_SESSION not set")
}
