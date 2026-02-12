package cmd

import (
	"fmt"
	"os"

	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/terminal"
)

// resolveTargetAgent converts a target spec to agent ID, pane, and hook root.
func resolveTargetAgent(target string) (agentID string, pane string, hookRoot string, err error) {
	// First resolve to session name
	sessionName, err := resolveRoleToSession(target)
	if err != nil {
		return "", "", "", err
	}

	// Convert session name to agent ID format (this doesn't require tmux)
	agentID = sessionToAgentID(sessionName)

	// In K8s-only mode, all agents use Coop backends. No tmux pane or workdir
	// available — the caller handles empty pane gracefully (sling.go falls back
	// to "agent will discover work via gt prime"), and ResolveHookDir falls back
	// to townRoot when hookRoot is empty.
	return agentID, "", "", nil
}

// sessionToAgentID converts a session name to bead ID format.
// Uses session.ParseSessionName for consistent parsing across the codebase.
// Returns the BeadID format (gt-<rig>-<role>-<name>) which matches the
// bead IDs created by the controller for K8s agent pods.
func sessionToAgentID(sessionName string) string {
	identity, err := session.ParseSessionName(sessionName)
	if err != nil {
		// Fallback for unparseable sessions
		return sessionName
	}
	return identity.BeadID()
}

// resolveBackendForSession resolves the Backend for a tmux session name.
// Returns the backend and the session key to use with it ("claude" for Coop).
func resolveBackendForSession(sessionName string) (terminal.Backend, string) {
	agentID := sessionToAgentID(sessionName)
	backend := terminal.ResolveBackend(agentID)
	return backend, "claude"
}

// resolveSelfTarget determines agent identity, pane, and hook root for slinging to self.
func resolveSelfTarget() (agentID string, pane string, hookRoot string, err error) {
	roleInfo, err := GetRole()
	if err != nil {
		return "", "", "", fmt.Errorf("detecting role: %w", err)
	}

	// Build agent identity from role
	// Town-level agents use trailing slash to match addressToIdentity() normalization
	switch roleInfo.Role {
	case RoleMayor:
		agentID = "mayor/"
	case RoleDeacon:
		agentID = "deacon/"
	case RoleWitness:
		agentID = fmt.Sprintf("%s/witness", roleInfo.Rig)
	case RoleRefinery:
		agentID = fmt.Sprintf("%s/refinery", roleInfo.Rig)
	case RolePolecat:
		agentID = fmt.Sprintf("%s/polecats/%s", roleInfo.Rig, roleInfo.Polecat)
	case RoleCrew:
		agentID = fmt.Sprintf("%s/crew/%s", roleInfo.Rig, roleInfo.Polecat)
	default:
		return "", "", "", fmt.Errorf("cannot determine agent identity (role: %s)", roleInfo.Role)
	}

	pane = os.Getenv("TMUX_PANE")
	hookRoot = roleInfo.Home
	if hookRoot == "" {
		// Fallback to git root if home not determined
		hookRoot, err = detectCloneRoot()
		if err != nil {
			return "", "", "", fmt.Errorf("detecting clone root: %w", err)
		}
	}

	return agentID, pane, hookRoot, nil
}
