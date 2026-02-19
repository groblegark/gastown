// Example Gas Town RPC client demonstrating common operations.
//
// Usage:
//
//	go run main.go --url http://localhost:8443 --api-key my-key [command]
//
// Commands:
//
//	status          Show town status
//	health          Health check
//	agents          List running agents
//	issues          List open issues
//	ready           Show ready-to-work issues
//	decisions       List pending decisions
//	watch-decisions Stream decisions in real-time
//	peek <agent>    Peek at agent terminal output
//	sling <bead> <target>  Assign work to an agent
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"connectrpc.com/connect"

	gastownv1 "github.com/steveyegge/gastown/gen/gastown/v1"
	"github.com/steveyegge/gastown/gen/gastown/v1/gastownv1connect"
)

func main() {
	url := envOrDefault("GT_RPC_URL", "http://localhost:8443")
	apiKey := envOrDefault("GT_API_KEY", "")

	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s [command] [args...]\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "Commands: status, health, agents, issues, ready, decisions, watch-decisions, peek, sling")
		os.Exit(1)
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	interceptor := authInterceptor(apiKey)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	switch os.Args[1] {
	case "status":
		cmdStatus(ctx, httpClient, url, interceptor)
	case "health":
		cmdHealth(ctx, httpClient, url, interceptor)
	case "agents":
		cmdAgents(ctx, httpClient, url, interceptor)
	case "issues":
		cmdIssues(ctx, httpClient, url, interceptor)
	case "ready":
		cmdReady(ctx, httpClient, url, interceptor)
	case "decisions":
		cmdDecisions(ctx, httpClient, url, interceptor)
	case "watch-decisions":
		cmdWatchDecisions(ctx, httpClient, url, interceptor)
	case "peek":
		if len(os.Args) < 3 {
			log.Fatal("Usage: peek <agent-address>")
		}
		cmdPeek(ctx, httpClient, url, interceptor, os.Args[2])
	case "sling":
		if len(os.Args) < 4 {
			log.Fatal("Usage: sling <bead-id> <target>")
		}
		cmdSling(ctx, httpClient, url, interceptor, os.Args[2], os.Args[3])
	default:
		log.Fatalf("Unknown command: %s", os.Args[1])
	}
}

func cmdStatus(ctx context.Context, httpClient *http.Client, url string, interceptor connect.Interceptor) {
	client := gastownv1connect.NewStatusServiceClient(httpClient, url, connect.WithInterceptors(interceptor))
	resp, err := client.GetTownStatus(ctx, connect.NewRequest(&gastownv1.GetTownStatusRequest{Fast: true}))
	if err != nil {
		log.Fatalf("GetTownStatus: %v", err)
	}
	printJSON(resp.Msg)
}

func cmdHealth(ctx context.Context, httpClient *http.Client, url string, interceptor connect.Interceptor) {
	client := gastownv1connect.NewStatusServiceClient(httpClient, url, connect.WithInterceptors(interceptor))
	resp, err := client.HealthCheck(ctx, connect.NewRequest(&gastownv1.HealthCheckRequest{}))
	if err != nil {
		log.Fatalf("HealthCheck: %v", err)
	}
	fmt.Printf("Status: %s\n", resp.Msg.Status)
	for _, c := range resp.Msg.Components {
		status := "OK"
		if !c.Healthy {
			status = "FAIL"
		}
		fmt.Printf("  %-10s %s (%dms) %s\n", c.Name, status, c.LatencyMs, c.Message)
	}
}

func cmdAgents(ctx context.Context, httpClient *http.Client, url string, interceptor connect.Interceptor) {
	client := gastownv1connect.NewAgentServiceClient(httpClient, url, connect.WithInterceptors(interceptor))
	resp, err := client.ListAgents(ctx, connect.NewRequest(&gastownv1.ListAgentsRequest{
		IncludeGlobal: true,
	}))
	if err != nil {
		log.Fatalf("ListAgents: %v", err)
	}
	fmt.Printf("%d agents (%d running)\n\n", resp.Msg.Total, resp.Msg.Running)
	for _, a := range resp.Msg.Agents {
		state := a.State.String()
		work := "(idle)"
		if a.HookedBead != "" {
			work = fmt.Sprintf("→ %s: %s", a.HookedBead, a.HookedTitle)
		}
		fmt.Printf("  %-35s %-18s %s\n", a.Address, state, work)
	}
}

func cmdIssues(ctx context.Context, httpClient *http.Client, url string, interceptor connect.Interceptor) {
	client := gastownv1connect.NewBeadsServiceClient(httpClient, url, connect.WithInterceptors(interceptor))
	resp, err := client.ListIssues(ctx, connect.NewRequest(&gastownv1.ListIssuesRequest{
		Status: "open",
		Limit:  20,
	}))
	if err != nil {
		log.Fatalf("ListIssues: %v", err)
	}
	fmt.Printf("%d open issues\n\n", resp.Msg.Total)
	for _, issue := range resp.Msg.Issues {
		fmt.Printf("  [P%d] %-12s %-15s %s\n",
			issue.Priority, issue.Id, issue.Status.String(), issue.Title)
	}
}

func cmdReady(ctx context.Context, httpClient *http.Client, url string, interceptor connect.Interceptor) {
	client := gastownv1connect.NewBeadsServiceClient(httpClient, url, connect.WithInterceptors(interceptor))
	resp, err := client.GetReadyIssues(ctx, connect.NewRequest(&gastownv1.GetReadyIssuesRequest{
		Limit: 10,
	}))
	if err != nil {
		log.Fatalf("GetReadyIssues: %v", err)
	}
	fmt.Printf("%d ready issues\n\n", resp.Msg.Total)
	for _, issue := range resp.Msg.Issues {
		assignee := "(unassigned)"
		if issue.Assignee != "" {
			assignee = issue.Assignee
		}
		fmt.Printf("  [P%d] %-12s %-30s %s\n",
			issue.Priority, issue.Id, issue.Title, assignee)
	}
}

func cmdDecisions(ctx context.Context, httpClient *http.Client, url string, interceptor connect.Interceptor) {
	client := gastownv1connect.NewDecisionServiceClient(httpClient, url, connect.WithInterceptors(interceptor))
	resp, err := client.ListPending(ctx, connect.NewRequest(&gastownv1.ListPendingRequest{}))
	if err != nil {
		log.Fatalf("ListPending: %v", err)
	}
	fmt.Printf("%d pending decisions\n\n", resp.Msg.Total)
	for _, d := range resp.Msg.Decisions {
		urgency := d.Urgency.String()
		fmt.Printf("  [%s] %s\n    %s\n", urgency, d.Id, d.Question)
		for i, opt := range d.Options {
			rec := ""
			if opt.Recommended {
				rec = " (recommended)"
			}
			fmt.Printf("    %d. %s%s\n", i+1, opt.Label, rec)
		}
		fmt.Println()
	}
}

func cmdWatchDecisions(ctx context.Context, httpClient *http.Client, url string, interceptor connect.Interceptor) {
	client := gastownv1connect.NewDecisionServiceClient(httpClient, url, connect.WithInterceptors(interceptor))
	stream, err := client.WatchDecisions(ctx, connect.NewRequest(&gastownv1.WatchDecisionsRequest{}))
	if err != nil {
		log.Fatalf("WatchDecisions: %v", err)
	}
	fmt.Println("Watching for decisions (Ctrl-C to stop)...")
	for stream.Receive() {
		d := stream.Msg()
		fmt.Printf("\n[%s] New decision: %s\n  %s\n", d.Urgency.String(), d.Id, d.Question)
		for i, opt := range d.Options {
			fmt.Printf("  %d. %s\n", i+1, opt.Label)
		}
	}
	if err := stream.Err(); err != nil {
		log.Fatalf("Stream error: %v", err)
	}
}

func cmdPeek(ctx context.Context, httpClient *http.Client, url string, interceptor connect.Interceptor, agent string) {
	client := gastownv1connect.NewAgentServiceClient(httpClient, url, connect.WithInterceptors(interceptor))
	resp, err := client.PeekAgent(ctx, connect.NewRequest(&gastownv1.PeekAgentRequest{
		Agent: agent,
		Lines: 50,
	}))
	if err != nil {
		log.Fatalf("PeekAgent: %v", err)
	}
	if !resp.Msg.Exists {
		fmt.Fprintf(os.Stderr, "Agent session not found: %s\n", agent)
		os.Exit(1)
	}
	fmt.Print(resp.Msg.Output)
}

func cmdSling(ctx context.Context, httpClient *http.Client, url string, interceptor connect.Interceptor, beadID, target string) {
	client := gastownv1connect.NewSlingServiceClient(httpClient, url, connect.WithInterceptors(interceptor))
	resp, err := client.Sling(ctx, connect.NewRequest(&gastownv1.SlingRequest{
		BeadId: beadID,
		Target: target,
		Create: true,
	}))
	if err != nil {
		log.Fatalf("Sling: %v", err)
	}
	fmt.Printf("Slung %s → %s\n", resp.Msg.BeadId, resp.Msg.TargetAgent)
	if resp.Msg.PolecatSpawned {
		fmt.Printf("Spawned polecat: %s\n", resp.Msg.PolecatName)
	}
	if resp.Msg.ConvoyId != "" {
		fmt.Printf("Convoy: %s\n", resp.Msg.ConvoyId)
	}
}

// authInterceptor adds the API key header to all requests.
func authInterceptor(apiKey string) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if apiKey != "" {
				req.Header().Set("X-GT-API-Key", apiKey)
			}
			return next(ctx, req)
		}
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func printJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		log.Fatal(err)
	}
}
