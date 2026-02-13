package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/terminal"
)

var terminalServerCmd = &cobra.Command{
	Use:     "terminal-server",
	GroupID: GroupServices,
	Short:   "Manage K8s terminal connections",
	Long: `Terminal server bridges local sessions to K8s pod screen sessions.

It discovers agent pods via beads polling, creates local sessions that
pipe to each pod's screen session via kubectl exec, and monitors connection
health. Existing gt commands (nudge, peek) work unchanged because the
terminal server creates sessions with the expected naming convention.

By default, discovers all agent pods across all rigs and town-level agents
(mayor, deacon). Use --rig to filter to a single rig.`,
	RunE: runTerminalServer,
}

var (
	tsRig            string
	tsNamespace      string
	tsKubeConfig     string
	tsPollInterval   time.Duration
	tsHealthInterval time.Duration
	tsScreenSession  string
)

func init() {
	terminalServerCmd.Flags().StringVar(&tsRig, "rig", "", "Filter to a single rig (default: all rigs + town-level agents)")
	terminalServerCmd.Flags().StringVar(&tsNamespace, "namespace", "gastown-test", "K8s namespace")
	terminalServerCmd.Flags().StringVar(&tsKubeConfig, "kubeconfig", "", "Path to kubeconfig (default: ~/.kube/config)")
	terminalServerCmd.Flags().DurationVar(&tsPollInterval, "poll-interval", 10*time.Second, "Beads discovery poll interval")
	terminalServerCmd.Flags().DurationVar(&tsHealthInterval, "health-interval", 5*time.Second, "Connection health check interval")
	terminalServerCmd.Flags().StringVar(&tsScreenSession, "screen-session", "agent", "Screen session name inside pods")

	rootCmd.AddCommand(terminalServerCmd)
}

func runTerminalServer(cmd *cobra.Command, args []string) error {
	scope := "all rigs"
	if tsRig != "" {
		scope = "rig " + tsRig
	}
	fmt.Printf("%s Terminal server starting for %s (namespace: %s)\n",
		style.Bold.Render("●"),
		style.Bold.Render(scope),
		tsNamespace,
	)
	fmt.Printf("  Poll interval: %s, Health interval: %s\n", tsPollInterval, tsHealthInterval)

	srv := terminal.NewServer(terminal.ServerConfig{
		Rig:            tsRig,
		Namespace:      tsNamespace,
		KubeConfig:     tsKubeConfig,
		PollInterval:   tsPollInterval,
		HealthInterval: tsHealthInterval,
		ScreenSession:  tsScreenSession,
	})

	// Set up signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		fmt.Printf("\n%s Received %s, shutting down...\n", style.Bold.Render("●"), sig)
		cancel()
	}()

	return srv.Run(ctx)
}
