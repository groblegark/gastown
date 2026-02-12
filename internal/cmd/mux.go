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
	ns := os.Getenv("GT_K8S_NAMESPACE")
	if ns == "" {
		return fmt.Errorf("GT_K8S_NAMESPACE not set — required for K8s pod discovery")
	}

	// Find the coop-broker pod.
	podName, err := findBrokerPod(ns)
	if err != nil {
		return err
	}

	fmt.Printf("%s Connecting to coop-broker %s in %s...\n",
		style.Bold.Render("☸"), style.Bold.Render(podName), ns)

	// Set up port-forward.
	conn := terminal.NewCoopPodConnection(terminal.CoopPodConnectionConfig{
		PodName:   podName,
		Namespace: ns,
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

	fmt.Printf("  Port-forward: localhost:%d → %s:8080\n", conn.LocalPort(), podName)

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

// findBrokerPod locates the coop-broker pod in the given namespace.
func findBrokerPod(ns string) (string, error) {
	out, err := exec.Command("kubectl", "get", "pods", "-n", ns,
		"-l", "app.kubernetes.io/component=coop-broker",
		"--field-selector=status.phase=Running",
		"-o", "jsonpath={.items[0].metadata.name}").Output()
	if err != nil {
		return "", fmt.Errorf("no coop-broker pod found in namespace %s: %w", ns, err)
	}

	name := strings.TrimSpace(string(out))
	if name == "" {
		return "", fmt.Errorf("no running coop-broker pod found in namespace %s", ns)
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
