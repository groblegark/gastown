package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/terminal"
)

var (
	coopBrowser bool
	coopURL     bool
)

var coopCmd = &cobra.Command{
	Use:     "coop <target>",
	GroupID: GroupAgents,
	Short:   "Connect to a K8s agent's terminal via coop",
	Long: `Attach to (or open) a K8s agent's coop web terminal.

Target formats:
  mayor                        Town-level mayor
  gastown/witness               Rig witness
  gastown/polecats/nux          Polecat by name
  gastown/crew/max              Crew member
  gt-gastown-polecat-nux        Direct pod name

By default, attaches an interactive terminal (coop attach).
Use --browser to open the web terminal in a browser instead.
Use --url to just print the local URL (for scripting).`,
	Args: cobra.ExactArgs(1),
	RunE: runCoop,
}

func init() {
	rootCmd.AddCommand(coopCmd)
	coopCmd.Flags().BoolVarP(&coopBrowser, "browser", "b", false, "Open web terminal in browser instead of attaching")
	coopCmd.Flags().BoolVar(&coopURL, "url", false, "Print local URL and keep port-forward running (for scripting)")
}

func runCoop(cmd *cobra.Command, args []string) error {
	target := args[0]

	// Resolve target to pod name.
	podName, ns := resolveCoopTarget(target)
	if podName == "" {
		if os.Getenv("GT_K8S_NAMESPACE") == "" {
			return fmt.Errorf("GT_K8S_NAMESPACE not set — required for K8s pod discovery")
		}
		return fmt.Errorf("could not find running K8s pod for %q in namespace %q", target, os.Getenv("GT_K8S_NAMESPACE"))
	}

	fmt.Printf("%s Connecting to %s in %s...\n",
		style.Bold.Render("☸"), style.Bold.Render(podName), ns)

	// Set up port-forward.
	conn := terminal.NewCoopPodConnection(terminal.CoopPodConnectionConfig{
		PodName:   podName,
		Namespace: ns,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := conn.Open(ctx); err != nil {
		return fmt.Errorf("connecting to pod: %w", err)
	}
	defer conn.Close()

	localURL := conn.LocalURL()
	fmt.Printf("  Port-forward: localhost:%d → %s:8080\n", conn.LocalPort(), podName)

	if coopURL {
		// Just print URL and block until interrupted.
		fmt.Println(localURL)
		fmt.Fprintf(os.Stderr, "  Press Ctrl+C to stop port-forward\n")
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh
		return nil
	}

	if coopBrowser {
		return openBrowserAndBlock(localURL)
	}

	// Default: coop attach (interactive terminal).
	coopPath, err := findCoopBinary()
	if err != nil {
		return err
	}

	fmt.Printf("  Detach: Ctrl+]\n\n")

	// Exec into coop attach — replaces this process.
	return syscall.Exec(coopPath, []string{"coop", "attach", localURL}, os.Environ())
}

// resolveCoopTarget converts a target string to (podName, namespace).
// Supports:
//   - Direct pod names: "gt-gastown-polecat-nux"
//   - Role paths: "gastown/polecats/nux", "gastown/witness", "mayor"
//   - Short forms: "mayor", "deacon"
func resolveCoopTarget(target string) (string, string) {
	ns := getConnectedNamespace()
	if ns == "" {
		return "", ""
	}

	// Try resolving via bead metadata (written by the controller's status reporter).
	info, err := terminal.ResolveAgentPodInfo(target)
	if err == nil && info.PodName != "" {
		namespace := info.Namespace
		if namespace == "" {
			namespace = ns
		}
		return info.PodName, namespace
	}

	// If target is a direct pod name (gt-* prefix), use it with the env namespace.
	if strings.HasPrefix(target, "gt-") {
		return target, ns
	}

	return "", ""
}

