package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/terminal"
)

var switchEnvFlag []string

var switchCmd = &cobra.Command{
	Use:   "switch <session>",
	Short: "Switch a K8s agent session with new environment variables",
	Long: `Triggers a coop session switch for a K8s agent, restarting the agent
process with updated environment variables. The agent's transport connections
survive the switch.

Use this to rotate credentials or update environment without restarting the pod.

Examples:
  gt switch hq-mayor --env GIT_TOKEN=ghp_...
  gt switch gt-gastown-crew-k8s --env KEY1=val1 --env KEY2=val2`,
	Args: cobra.ExactArgs(1),
	RunE: runSwitch,
}

func init() {
	switchCmd.Flags().StringArrayVarP(&switchEnvFlag, "env", "e", nil, "Environment variable in KEY=VALUE format (repeatable)")
	rootCmd.AddCommand(switchCmd)
}

func runSwitch(cmd *cobra.Command, args []string) error {
	session := args[0]

	// Parse env vars.
	envMap := make(map[string]string)
	for _, kv := range switchEnvFlag {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid --env format %q, expected KEY=VALUE", kv)
		}
		envMap[parts[0]] = parts[1]
	}

	// Resolve the backend for this session.
	backend := terminal.ResolveBackend(session)

	cfg := terminal.SwitchConfig{
		ExtraEnv: envMap,
	}

	if err := backend.SwitchSession(session, cfg); err != nil {
		return fmt.Errorf("switching session %s: %w", session, err)
	}

	fmt.Printf("Session %s switched successfully", session)
	if len(envMap) > 0 {
		keys := make([]string, 0, len(envMap))
		for k := range envMap {
			keys = append(keys, k)
		}
		fmt.Printf(" (updated: %s)", strings.Join(keys, ", "))
	}
	fmt.Println()

	return nil
}
