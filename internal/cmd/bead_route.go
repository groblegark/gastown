package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/style"
)

var routeCmd = &cobra.Command{
	Use:   "route",
	Short: "Manage bead routes",
	Long: `Manage routes that map bead ID prefixes to repository paths.

Routes determine where beads are stored based on their ID prefix.
For example, 'gt-' prefix routes to gastown, 'bd-' to beads.

COMMANDS:
  add       Add a route mapping
  remove    Remove a route mapping
  list      List all route mappings

Examples:
  gt bead route add gt- gastown         # Route gt-* beads to gastown
  gt bead route add bd- beads           # Route bd-* beads to beads
  gt bead route list                    # List all routes
  gt bead route remove gt-              # Remove the gt- route`,
	RunE: requireSubcommand,
}

var routeAddCmd = &cobra.Command{
	Use:   "add <prefix> <path>",
	Short: "Add a route mapping",
	Long: `Add a route mapping from a bead ID prefix to a repository path.

The prefix should end with a hyphen (e.g., 'gt-', 'bd-', 'hq-').
The path is the relative path from the town root to the repository.

Examples:
  gt bead route add gt- gastown         # Route gt-* to gastown rig
  gt bead route add bd- beads           # Route bd-* to beads rig
  gt bead route add hq- .               # Route hq-* to town root`,
	Args: cobra.ExactArgs(2),
	RunE: runRouteAdd,
}

var routeRemoveCmd = &cobra.Command{
	Use:   "remove <prefix>",
	Short: "Remove a route mapping",
	Long: `Remove a route mapping by prefix.

The prefix should match exactly what was used when the route was added.

Examples:
  gt bead route remove gt-              # Remove the gt- route
  gt bead route remove custom-          # Remove a custom route`,
	Args: cobra.ExactArgs(1),
	RunE: runRouteRemove,
}

var routeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all route mappings",
	Long: `List all registered bead route mappings.

Shows the prefix-to-path mappings that determine where beads are stored.

Examples:
  gt bead route list                    # Human-readable output
  gt bead route list --json             # JSON output`,
	RunE: runRouteList,
}

var (
	routeScope string
	routeJSON  bool
)

func init() {
	routeAddCmd.Flags().StringVar(&routeScope, "scope", "town", "Route scope: 'town' or 'rig'")

	routeListCmd.Flags().BoolVar(&routeJSON, "json", false, "Output as JSON")

	routeCmd.AddCommand(routeAddCmd)
	routeCmd.AddCommand(routeRemoveCmd)
	routeCmd.AddCommand(routeListCmd)

	beadCmd.AddCommand(routeCmd)
}

// routeInfo represents a route bead's data
type routeInfo struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Prefix   string `json:"prefix"`
	Path     string `json:"path"`
	Scope    string `json:"scope"`
	Status   string `json:"status"`
	Labels   []string `json:"labels"`
}

func runRouteAdd(cmd *cobra.Command, args []string) error {
	prefix := args[0]
	path := args[1]

	// Normalize prefix (ensure it ends with -)
	if !strings.HasSuffix(prefix, "-") {
		prefix = prefix + "-"
	}

	// Validate scope
	if routeScope != "town" && routeScope != "rig" {
		return fmt.Errorf("invalid scope %q: must be 'town' or 'rig'", routeScope)
	}

	// Check if route already exists
	existingRoutes, err := queryRoutes()
	if err != nil {
		return fmt.Errorf("checking existing routes: %w", err)
	}
	for _, r := range existingRoutes {
		if r.Prefix == prefix && r.Status != "closed" {
			return fmt.Errorf("route for prefix %q already exists: %s", prefix, r.ID)
		}
	}

	// Create the route bead
	title := fmt.Sprintf("Route: %s → %s", prefix, path)
	description := fmt.Sprintf("Route mapping for bead prefix %s to path %s\n\nScope: %s", prefix, path, routeScope)

	createArgs := []string{
		"create",
		"--type", "route",
		"--title", title,
		"--description", description,
		"--label", fmt.Sprintf("prefix:%s", strings.TrimSuffix(prefix, "-")),
		"--label", fmt.Sprintf("path:%s", path),
		"--label", fmt.Sprintf("scope:%s", routeScope),
		"--silent",
	}

	// Routes should be in town beads (hq- prefix) for town scope
	if routeScope == "town" {
		createArgs = append(createArgs, "--prefix", "hq-")
	}

	createCmd := exec.Command("bd", createArgs...)
	createCmd.Stderr = os.Stderr
	output, err := createCmd.Output()
	if err != nil {
		return fmt.Errorf("creating route bead: %w", err)
	}

	routeID := strings.TrimSpace(string(output))
	fmt.Printf("%s Added route: %s → %s (%s)\n", style.Bold.Render("✓"), prefix, path, routeID)

	return nil
}

func runRouteRemove(cmd *cobra.Command, args []string) error {
	prefix := args[0]

	// Normalize prefix
	if !strings.HasSuffix(prefix, "-") {
		prefix = prefix + "-"
	}

	// Find the route bead
	routes, err := queryRoutes()
	if err != nil {
		return fmt.Errorf("querying routes: %w", err)
	}

	var found *routeInfo
	for _, r := range routes {
		if r.Prefix == prefix && r.Status != "closed" {
			found = &r
			break
		}
	}

	if found == nil {
		return fmt.Errorf("no route found for prefix %q", prefix)
	}

	// Close the route bead
	closeCmd := exec.Command("bd", "close", found.ID, "--reason", "Route removed")
	closeCmd.Stderr = os.Stderr
	if err := closeCmd.Run(); err != nil {
		return fmt.Errorf("closing route bead: %w", err)
	}

	fmt.Printf("%s Removed route: %s → %s\n", style.Bold.Render("✓"), prefix, found.Path)

	return nil
}

func runRouteList(cmd *cobra.Command, args []string) error {
	routes, err := queryRoutes()
	if err != nil {
		return fmt.Errorf("querying routes: %w", err)
	}

	// Filter to open routes
	var openRoutes []routeInfo
	for _, r := range routes {
		if r.Status != "closed" {
			openRoutes = append(openRoutes, r)
		}
	}

	if routeJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(openRoutes)
	}

	if len(openRoutes) == 0 {
		fmt.Println("No routes configured.")
		fmt.Println("\nUse 'gt bead route add <prefix> <path>' to add routes.")
		return nil
	}

	fmt.Printf("%s\n\n", style.Bold.Render("Bead Routes:"))
	fmt.Printf("  %-12s %-20s %s\n", "PREFIX", "PATH", "ID")
	fmt.Printf("  %-12s %-20s %s\n", "------", "----", "--")
	for _, r := range openRoutes {
		fmt.Printf("  %-12s %-20s %s\n", r.Prefix, r.Path, r.ID)
	}

	return nil
}

// queryRoutes queries all route-type beads and extracts their metadata
func queryRoutes() ([]routeInfo, error) {
	// Query route beads via bd list
	listCmd := exec.Command("bd", "list", "--type=route", "--json")
	output, err := listCmd.Output()
	if err != nil {
		// If no routes exist, bd list may error or return empty
		return []routeInfo{}, nil
	}

	// Parse the JSON output
	var rawRoutes []struct {
		ID     string   `json:"id"`
		Title  string   `json:"title"`
		Status string   `json:"status"`
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal(output, &rawRoutes); err != nil {
		return nil, fmt.Errorf("parsing route data: %w", err)
	}

	// Extract prefix/path from labels
	routes := make([]routeInfo, 0, len(rawRoutes))
	for _, raw := range rawRoutes {
		r := routeInfo{
			ID:     raw.ID,
			Title:  raw.Title,
			Status: raw.Status,
			Labels: raw.Labels,
		}

		// Parse labels for prefix:, path:, scope:
		for _, label := range raw.Labels {
			switch {
			case strings.HasPrefix(label, "prefix:"):
				r.Prefix = strings.TrimPrefix(label, "prefix:") + "-"
			case strings.HasPrefix(label, "path:"):
				r.Path = strings.TrimPrefix(label, "path:")
			case strings.HasPrefix(label, "scope:"):
				r.Scope = strings.TrimPrefix(label, "scope:")
			}
		}

		routes = append(routes, r)
	}

	return routes, nil
}
