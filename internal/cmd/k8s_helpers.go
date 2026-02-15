package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/steveyegge/gastown/internal/rpcclient"
	"github.com/steveyegge/gastown/internal/style"
)

// stopK8sPod deletes a K8s pod by name. The controller will handle cleanup.
// For agents managed by the controller, deleting the pod triggers proper shutdown.
func stopK8sPod(podName, namespace, displayName string) error {
	fmt.Printf("%s Stopping %s K8s pod %s in %s...\n",
		style.Bold.Render("☸"), displayName, podName, namespace)

	cmd := exec.Command("kubectl", "delete", "pod", podName, "-n", namespace, "--grace-period=30")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("deleting pod %s: %w", podName, err)
	}

	fmt.Printf("%s %s pod deleted.\n", style.Bold.Render("✓"), displayName)
	return nil
}

// runAgentStatusRemote queries agent status via daemon RPC.
// agentAddr is the address used in RPC (e.g., "mayor", "deacon").
// beadID is the bead ID used for direct lookup (e.g., "hq-mayor").
func runAgentStatusRemote(client *rpcclient.Client, agentAddr, displayName, beadID string) error {
	// Use ListAgents with a broad filter, then match by bead ID.
	agents, _, _, err := client.ListAgents(context.Background(), "", "", true, true)
	if err != nil {
		return fmt.Errorf("querying agents: %w", err)
	}

	for _, a := range agents {
		// Match on name or address containing the agent type.
		if a.Name == agentAddr || a.Address == agentAddr || a.Type == agentAddr {
			state := a.State
			if state == "" {
				state = "unknown"
			}
			icon := style.Dim.Render("○")
			stateLabel := state
			switch state {
			case "running", "working":
				icon = style.Bold.Render("●")
				stateLabel = style.Bold.Render(state)
			case "spawning":
				icon = style.Bold.Render("⟳")
				stateLabel = style.Bold.Render(state)
			case "stopping", "stopped":
				icon = style.Dim.Render("○")
			}

			fmt.Printf("%s %s is %s (remote)\n", icon, displayName, stateLabel)
			if a.Address != "" {
				fmt.Printf("  Address: %s\n", a.Address)
			}
			if a.StartedAt != "" {
				fmt.Printf("  Started: %s\n", a.StartedAt)
			}
			if a.LastActivity != "" {
				fmt.Printf("  Last activity: %s\n", a.LastActivity)
			}
			fmt.Printf("\nAttach with: %s\n", style.Dim.Render(fmt.Sprintf("gt %s attach", agentAddr)))
			return nil
		}
	}

	fmt.Printf("%s %s is %s (remote)\n",
		style.Dim.Render("○"), displayName, "not running")
	return nil
}
