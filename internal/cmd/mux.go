package cmd

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
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

Requires a coop-broker pod running in the K8s namespace (GT_K8S_NAMESPACE).
The auth token is read from the local daemon connection config.

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

	// Find the coop-broker service.
	svcName, err := findBrokerService(ns)
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

// findBrokerService locates the coop-broker K8s Service in the given namespace.
// Uses the same label selector as the pod lookup but targets the Service object,
// which is stable across pod restarts and scales.
func findBrokerService(ns string) (string, error) {
	out, err := exec.Command("kubectl", "get", "svc", "-n", ns,
		"-l", "app.kubernetes.io/component=coop-broker",
		"-o", "jsonpath={.items[0].metadata.name}").Output()
	if err != nil {
		return "", fmt.Errorf("no coop-broker service found in namespace %s: %w", ns, err)
	}

	name := strings.TrimSpace(string(out))
	if name == "" {
		return "", fmt.Errorf("no coop-broker service found in namespace %s", ns)
	}
	return name, nil
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
