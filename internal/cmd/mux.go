package cmd

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/terminal"
)

var (
	muxURL bool
)

var muxCmd = &cobra.Command{
	Use:     "mux",
	GroupID: GroupAgents,
	Short:   "Open the agent multiplexer dashboard",
	Long: `Opens the coop-broker multiplexer dashboard in a browser.

The mux dashboard shows a live grid of all registered agent pods with
their terminal output, state badges, and credential alerts. Click a
tile to focus it for input, or expand to full screen.

Requires a coop-broker pod running in the connected K8s namespace.
The service name is derived from the town name in daemon config, and
the auth token is read from the local daemon connection config.

Use --url to print the local URL without opening a browser.`,
	RunE: runMux,
}

func init() {
	rootCmd.AddCommand(muxCmd)
	muxCmd.Flags().BoolVar(&muxURL, "url", false, "Print local URL and keep port-forward running (for scripting)")
}

func runMux(cmd *cobra.Command, args []string) error {
	ns := getConnectedNamespace()
	if ns == "" {
		return fmt.Errorf("not connected to a K8s namespace — run 'gt connect <namespace>' first")
	}

	// Resolve the coop-broker service name from daemon config (Helm convention:
	// <release-name>-coop-broker where release-name = town-name).
	svcName, err := resolveBrokerServiceName()
	if err != nil {
		return err
	}

	fmt.Printf("%s Connecting to coop-broker %s in %s...\n",
		style.Bold.Render("☸"), style.Bold.Render(svcName), ns)

	// Set up port-forward to the service (not a specific pod).
	conn := terminal.NewCoopPodConnection(terminal.CoopPodConnectionConfig{
		PodName:   "svc/" + svcName,
		Namespace: ns,
		CoopPort:  9800, // broker service port (values.yaml coopBroker.service.port)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := conn.Open(ctx); err != nil {
		return fmt.Errorf("connecting to coop-broker: %w", err)
	}
	defer conn.Close()

	// Build the mux URL with auth token.
	muxEndpoint := fmt.Sprintf("%s/mux", conn.LocalURL())
	token := getBrokerToken()
	if token != "" {
		muxEndpoint += "?token=" + url.QueryEscape(token)
	}

	fmt.Printf("  Port-forward: localhost:%d → %s:9800\n", conn.LocalPort(), svcName)

	if muxURL {
		fmt.Println(muxEndpoint)
		fmt.Fprintf(os.Stderr, "  Press Ctrl+C to stop port-forward\n")
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		return nil
	}

	// Open in browser and block.
	opener := "xdg-open"
	if _, err := exec.LookPath("open"); err == nil {
		opener = "open"
	}
	fmt.Printf("  Opening %s\n", muxEndpoint)
	openCmd := exec.Command(opener, muxEndpoint)
	if err := openCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  Failed to open browser: %v\n", err)
		fmt.Fprintf(os.Stderr, "  Open manually: %s\n", muxEndpoint)
	}
	fmt.Fprintf(os.Stderr, "  Press Ctrl+C to stop port-forward\n")
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	return nil
}

// resolveBrokerServiceName returns the coop-broker K8s Service name.
// Derives it from the town name in daemon config using the Helm naming
// convention: <release-name>-coop-broker (where release-name = town-name).
// This avoids a kubectl call since the town name is already cached locally.
func resolveBrokerServiceName() (string, error) {
	cfg, err := readGlobalDaemonConfigFull()
	if err != nil {
		return "", fmt.Errorf("reading daemon config: %w", err)
	}
	if cfg.TownName == "" {
		return "", fmt.Errorf("no town name in daemon config — run 'gt connect <namespace>' first")
	}
	return cfg.TownName + "-coop-broker", nil
}

// getBrokerToken returns the auth token for the coop-broker.
// The broker uses the same daemon token, so we read it from the local config.
func getBrokerToken() string {
	_, token, _, err := readGlobalDaemonConfig()
	if err != nil || token == "" {
		// Also check BD_DAEMON_TOKEN env var (K8s pod mode).
		return os.Getenv("BD_DAEMON_TOKEN")
	}
	return token
}
