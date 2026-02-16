package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/activity"
	"github.com/steveyegge/gastown/internal/bdcmd"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/workspace"
)

// Command timeout constants
const (
	cmdTimeout   = 15 * time.Second // timeout for most commands (bd can be slow with large datasets)
	ghCmdTimeout = 10 * time.Second // longer timeout for GitHub API calls
)

// runCmd executes a command with a timeout and returns stdout.
// Returns empty buffer on timeout or error.
func runCmd(timeout time.Duration, name string, args ...string) (*bytes.Buffer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("%s timed out after %v", name, timeout)
		}
		return nil, err
	}
	return &stdout, nil
}

// runBdCmd executes a bd command with cmdTimeout in the specified beads directory.
func runBdCmd(beadsDir string, args ...string) (*bytes.Buffer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	cmd := bdcmd.CommandContextInDir(ctx, beadsDir, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	err := cmd.Run()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("bd timed out after %v", cmdTimeout)
		}
		// If we got some output, return it anyway (bd may exit non-zero with warnings)
		if stdout.Len() > 0 {
			return &stdout, nil
		}
		return nil, err
	}
	return &stdout, nil
}

// LiveConvoyFetcher fetches convoy data from beads.
type LiveConvoyFetcher struct {
	townRoot  string
	townBeads string
}

// NewLiveConvoyFetcher creates a fetcher for the current workspace.
func NewLiveConvoyFetcher() (*LiveConvoyFetcher, error) {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return nil, fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	return &LiveConvoyFetcher{
		townRoot:  townRoot,
		townBeads: filepath.Join(townRoot, ".beads"),
	}, nil
}

// FetchConvoys fetches all open convoys with their activity data.
func (f *LiveConvoyFetcher) FetchConvoys() ([]ConvoyRow, error) {
	// List all open convoy-type issues
	stdout, err := runBdCmd(f.townBeads, "list", "--type=convoy", "--status=open", "--json")
	if err != nil {
		return nil, fmt.Errorf("listing convoys: %w", err)
	}

	var convoys []struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Status    string `json:"status"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &convoys); err != nil {
		return nil, fmt.Errorf("parsing convoy list: %w", err)
	}

	// Build convoy rows with activity data
	rows := make([]ConvoyRow, 0, len(convoys))
	for _, c := range convoys {
		row := ConvoyRow{
			ID:     c.ID,
			Title:  c.Title,
			Status: c.Status,
		}

		// Get tracked issues for progress and activity calculation
		tracked := f.getTrackedIssues(c.ID)
		row.Total = len(tracked)

		var mostRecentActivity time.Time
		var mostRecentUpdated time.Time
		var hasAssignee bool
		for _, t := range tracked {
			if t.Status == "closed" {
				row.Completed++
			}
			// Track most recent activity from workers
			if t.LastActivity.After(mostRecentActivity) {
				mostRecentActivity = t.LastActivity
			}
			// Track most recent updated_at as fallback
			if t.UpdatedAt.After(mostRecentUpdated) {
				mostRecentUpdated = t.UpdatedAt
			}
			if t.Assignee != "" {
				hasAssignee = true
			}
		}

		row.Progress = fmt.Sprintf("%d/%d", row.Completed, row.Total)

		// Calculate activity info from most recent worker activity
		if !mostRecentActivity.IsZero() {
			// Have session activity from assigned workers
			row.LastActivity = activity.Calculate(mostRecentActivity)
		} else if !hasAssignee {
			// No assignees found in beads - try fallback to any running polecat activity
			// This handles cases where bd update --assignee didn't persist or wasn't returned
			if polecatActivity := f.getAllPolecatActivity(); polecatActivity != nil {
				info := activity.Calculate(*polecatActivity)
				info.FormattedAge = info.FormattedAge + " (polecat active)"
				row.LastActivity = info
			} else if !mostRecentUpdated.IsZero() {
				// Fall back to issue updated_at if no polecats running
				info := activity.Calculate(mostRecentUpdated)
				info.FormattedAge = info.FormattedAge + " (unassigned)"
				row.LastActivity = info
			} else {
				row.LastActivity = activity.Info{
					FormattedAge: "unassigned",
					ColorClass:   activity.ColorUnknown,
				}
			}
		} else {
			// Has assignee but no active session
			row.LastActivity = activity.Info{
				FormattedAge: "idle",
				ColorClass:   activity.ColorUnknown,
			}
		}

		// Calculate work status based on progress and activity
		row.WorkStatus = calculateWorkStatus(row.Completed, row.Total, row.LastActivity.ColorClass)

		// Get tracked issues for expandable view
		row.TrackedIssues = make([]TrackedIssue, len(tracked))
		for i, t := range tracked {
			row.TrackedIssues[i] = TrackedIssue{
				ID:       t.ID,
				Title:    t.Title,
				Status:   t.Status,
				Assignee: t.Assignee,
			}
		}

		rows = append(rows, row)
	}

	return rows, nil
}

// trackedIssueInfo holds info about an issue being tracked by a convoy.
type trackedIssueInfo struct {
	ID           string
	Title        string
	Status       string
	Assignee     string
	LastActivity time.Time
	UpdatedAt    time.Time // Fallback for activity when no assignee
}

// getTrackedIssues fetches tracked issues for a convoy.
func (f *LiveConvoyFetcher) getTrackedIssues(convoyID string) []trackedIssueInfo {
	// Use bd CLI instead of direct sqlite3 access to support both SQLite and Dolt backends
	cmd := bdcmd.Command("dep", "list", convoyID, "--json")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return nil
	}

	var deps []struct {
		DependsOnID string `json:"depends_on_id"`
		Type        string `json:"type"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &deps); err != nil {
		return nil
	}

	// Filter for "tracks" type dependencies and collect issue IDs (normalize external refs)
	issueIDs := make([]string, 0, len(deps))
	for _, dep := range deps {
		if dep.Type != "tracks" {
			continue
		}
		issueID := dep.DependsOnID
		if strings.HasPrefix(issueID, "external:") {
			parts := strings.SplitN(issueID, ":", 3)
			if len(parts) == 3 {
				issueID = parts[2]
			}
		}
		issueIDs = append(issueIDs, issueID)
	}

	// Batch fetch issue details
	details := f.getIssueDetailsBatch(issueIDs)

	// Get worker activity based on assignees
	workers := f.getWorkersFromAssignees(details)

	// Build result
	result := make([]trackedIssueInfo, 0, len(issueIDs))
	for _, id := range issueIDs {
		info := trackedIssueInfo{ID: id}

		if d, ok := details[id]; ok {
			info.Title = d.Title
			info.Status = d.Status
			info.Assignee = d.Assignee
			info.UpdatedAt = d.UpdatedAt
		} else {
			info.Title = "(external)"
			info.Status = "unknown"
		}

		if w, ok := workers[id]; ok && w.LastActivity != nil {
			info.LastActivity = *w.LastActivity
		}

		result = append(result, info)
	}

	return result
}

// issueDetail holds basic issue info.
type issueDetail struct {
	ID        string
	Title     string
	Status    string
	Assignee  string
	UpdatedAt time.Time
}

// getIssueDetailsBatch fetches details for multiple issues.
func (f *LiveConvoyFetcher) getIssueDetailsBatch(issueIDs []string) map[string]*issueDetail {
	result := make(map[string]*issueDetail)
	if len(issueIDs) == 0 {
		return result
	}

	args := append([]string{"show"}, issueIDs...)
	args = append(args, "--json")

	stdout, err := runCmd(cmdTimeout, "bd", args...)
	if err != nil {
		return result
	}

	var issues []struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Status    string `json:"status"`
		Assignee  string `json:"assignee"`
		UpdatedAt string `json:"updated_at"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &issues); err != nil {
		return result
	}

	for _, issue := range issues {
		detail := &issueDetail{
			ID:       issue.ID,
			Title:    issue.Title,
			Status:   issue.Status,
			Assignee: issue.Assignee,
		}
		// Parse updated_at timestamp
		if issue.UpdatedAt != "" {
			if t, err := time.Parse(time.RFC3339, issue.UpdatedAt); err == nil {
				detail.UpdatedAt = t
			}
		}
		result[issue.ID] = detail
	}

	return result
}

// workerDetail holds worker info including last activity.
type workerDetail struct {
	Worker       string
	LastActivity *time.Time
}

// getWorkersFromAssignees gets worker activity based on issue assignees.
// Assignees are in format "rigname/polecats/polecatname".
func (f *LiveConvoyFetcher) getWorkersFromAssignees(details map[string]*issueDetail) map[string]*workerDetail {
	result := make(map[string]*workerDetail)

	// Collect unique assignees and map them to issue IDs
	assigneeToIssues := make(map[string][]string)
	for issueID, detail := range details {
		if detail == nil || detail.Assignee == "" {
			continue
		}
		assigneeToIssues[detail.Assignee] = append(assigneeToIssues[detail.Assignee], issueID)
	}

	if len(assigneeToIssues) == 0 {
		return result
	}

	// For each unique assignee, look up session activity
	for assignee, issueIDs := range assigneeToIssues {
		activity := f.getSessionActivityForAssignee(assignee)
		if activity == nil {
			continue
		}

		// Apply this activity to all issues assigned to this worker
		for _, issueID := range issueIDs {
			result[issueID] = &workerDetail{
				Worker:       assignee,
				LastActivity: activity,
			}
		}
	}

	return result
}

// getSessionActivityForAssignee returns nil — session activity is not available.
func (f *LiveConvoyFetcher) getSessionActivityForAssignee(_ string) *time.Time {
	return nil
}

// getAllPolecatActivity returns nil — session activity is not available.
func (f *LiveConvoyFetcher) getAllPolecatActivity() *time.Time {
	return nil
}

// calculateWorkStatus determines the work status based on progress and activity.
// Returns: "complete", "active", "stale", "stuck", or "waiting"
func calculateWorkStatus(completed, total int, activityColor string) string {
	// Check if all work is done
	if total > 0 && completed == total {
		return "complete"
	}

	// Determine status based on activity color
	switch activityColor {
	case activity.ColorGreen:
		return "active"
	case activity.ColorYellow:
		return "stale"
	case activity.ColorRed:
		return "stuck"
	default:
		return "waiting"
	}
}

// FetchMergeQueue fetches open PRs from registered rigs.
func (f *LiveConvoyFetcher) FetchMergeQueue() ([]MergeQueueRow, error) {
	// Load registered rigs from config
	rigsConfigPath := filepath.Join(f.townRoot, "mayor", "rigs.json")
	rigsConfig, err := config.LoadRigsConfig(rigsConfigPath)
	if err != nil {
		return nil, fmt.Errorf("loading rigs config: %w", err)
	}

	var result []MergeQueueRow

	for rigName, entry := range rigsConfig.Rigs {
		// Convert git URL to owner/repo format for gh CLI
		repoPath := gitURLToRepoPath(entry.GitURL)
		if repoPath == "" {
			continue
		}

		prs, err := f.fetchPRsForRepo(repoPath, rigName)
		if err != nil {
			// Non-fatal: continue with other repos
			continue
		}
		result = append(result, prs...)
	}

	return result, nil
}

// gitURLToRepoPath converts a git URL to owner/repo format.
// Supports HTTPS (https://github.com/owner/repo.git) and
// SSH (git@github.com:owner/repo.git) formats.
func gitURLToRepoPath(gitURL string) string {
	// Handle HTTPS format: https://github.com/owner/repo.git
	if strings.HasPrefix(gitURL, "https://github.com/") {
		path := strings.TrimPrefix(gitURL, "https://github.com/")
		path = strings.TrimSuffix(path, ".git")
		return path
	}

	// Handle SSH format: git@github.com:owner/repo.git
	if strings.HasPrefix(gitURL, "git@github.com:") {
		path := strings.TrimPrefix(gitURL, "git@github.com:")
		path = strings.TrimSuffix(path, ".git")
		return path
	}

	// Unsupported format
	return ""
}

// prResponse represents the JSON response from gh pr list.
type prResponse struct {
	Number            int    `json:"number"`
	Title             string `json:"title"`
	URL               string `json:"url"`
	Mergeable         string `json:"mergeable"`
	StatusCheckRollup []struct {
		State      string `json:"state"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	} `json:"statusCheckRollup"`
}

// fetchPRsForRepo fetches open PRs for a single repo.
func (f *LiveConvoyFetcher) fetchPRsForRepo(repoFull, repoShort string) ([]MergeQueueRow, error) {
	stdout, err := runCmd(ghCmdTimeout, "gh", "pr", "list",
		"--repo", repoFull,
		"--state", "open",
		"--json", "number,title,url,mergeable,statusCheckRollup")
	if err != nil {
		return nil, fmt.Errorf("fetching PRs for %s: %w", repoFull, err)
	}

	var prs []prResponse
	if err := json.Unmarshal(stdout.Bytes(), &prs); err != nil {
		return nil, fmt.Errorf("parsing PRs for %s: %w", repoFull, err)
	}

	result := make([]MergeQueueRow, 0, len(prs))
	for _, pr := range prs {
		row := MergeQueueRow{
			Number: pr.Number,
			Repo:   repoShort,
			Title:  pr.Title,
			URL:    pr.URL,
		}

		// Determine CI status from statusCheckRollup
		row.CIStatus = determineCIStatus(pr.StatusCheckRollup)

		// Determine mergeable status
		row.Mergeable = determineMergeableStatus(pr.Mergeable)

		// Determine color class based on overall status
		row.ColorClass = determineColorClass(row.CIStatus, row.Mergeable)

		result = append(result, row)
	}

	return result, nil
}

// determineCIStatus evaluates the overall CI status from status checks.
func determineCIStatus(checks []struct {
	State      string `json:"state"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}) string {
	if len(checks) == 0 {
		return "pending"
	}

	hasFailure := false
	hasPending := false

	for _, check := range checks {
		// Check conclusion first (for completed checks)
		switch check.Conclusion {
		case "failure", "cancelled", "timed_out", "action_required": //nolint:misspell // GitHub API returns "cancelled" (British spelling)
			hasFailure = true
		case "success", "skipped", "neutral":
			// Pass
		default:
			// Check status for in-progress checks
			switch check.Status {
			case "queued", "in_progress", "waiting", "pending", "requested":
				hasPending = true
			}
			// Also check state field
			switch check.State {
			case "FAILURE", "ERROR":
				hasFailure = true
			case "PENDING", "EXPECTED":
				hasPending = true
			}
		}
	}

	if hasFailure {
		return "fail"
	}
	if hasPending {
		return "pending"
	}
	return "pass"
}

// determineMergeableStatus converts GitHub's mergeable field to display value.
func determineMergeableStatus(mergeable string) string {
	switch strings.ToUpper(mergeable) {
	case "MERGEABLE":
		return "ready"
	case "CONFLICTING":
		return "conflict"
	default:
		return "pending"
	}
}

// determineColorClass determines the row color based on CI and merge status.
func determineColorClass(ciStatus, mergeable string) string {
	if ciStatus == "fail" || mergeable == "conflict" {
		return "mq-red"
	}
	if ciStatus == "pending" || mergeable == "pending" {
		return "mq-yellow"
	}
	if ciStatus == "pass" && mergeable == "ready" {
		return "mq-green"
	}
	return "mq-yellow"
}

// FetchWorkers fetches all registered workers (polecats and refinery) with issue data from beads.
func (f *LiveConvoyFetcher) FetchWorkers() ([]WorkerRow, error) {
	// Load registered rigs
	rigsConfigPath := filepath.Join(f.townRoot, "mayor", "rigs.json")
	rigsConfig, err := config.LoadRigsConfig(rigsConfigPath)
	if err != nil {
		return nil, fmt.Errorf("loading rigs config: %w", err)
	}

	// Pre-fetch assigned issues map: assignee -> (issueID, title)
	assignedIssues := f.getAssignedIssuesMap()

	var workers []WorkerRow

	for rigName := range rigsConfig.Rigs {
		rigPath := filepath.Join(f.townRoot, rigName)

		// Enumerate polecats from filesystem
		polecatsDir := filepath.Join(rigPath, "polecats")
		if entries, err := os.ReadDir(polecatsDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
					continue
				}
				workerName := e.Name()
				assignee := fmt.Sprintf("%s/polecats/%s", rigName, workerName)

				var issueID, issueTitle, workStatus string
				if issue, ok := assignedIssues[assignee]; ok {
					issueID = issue.ID
					issueTitle = issue.Title
					workStatus = "working"
				} else {
					workStatus = "idle"
				}

				workers = append(workers, WorkerRow{
					Name:       workerName,
					Rig:        rigName,
					IssueID:    issueID,
					IssueTitle: issueTitle,
					WorkStatus: workStatus,
					AgentType:  "polecat",
				})
			}
		}

		// Check for refinery
		refineryPath := filepath.Join(rigPath, "refinery", "rig")
		if _, err := os.Stat(refineryPath); err == nil {
			mergeQueueCount := f.getMergeQueueCount()
			workStatus := "idle"
			if mergeQueueCount > 0 {
				workStatus = "working"
			}
			statusHint := f.getRefineryStatusHint(mergeQueueCount)

			workers = append(workers, WorkerRow{
				Name:       "refinery",
				Rig:        rigName,
				StatusHint: statusHint,
				WorkStatus: workStatus,
				AgentType:  "refinery",
			})
		}
	}

	// Sort by rig, then name
	sort.Slice(workers, func(i, j int) bool {
		if workers[i].Rig != workers[j].Rig {
			return workers[i].Rig < workers[j].Rig
		}
		return workers[i].Name < workers[j].Name
	})

	return workers, nil
}

// assignedIssue holds issue info for the assigned issues map.
type assignedIssue struct {
	ID    string
	Title string
}

// getAssignedIssuesMap returns a map of assignee -> assigned issue.
// Queries beads for all in_progress issues with assignees.
func (f *LiveConvoyFetcher) getAssignedIssuesMap() map[string]assignedIssue {
	result := make(map[string]assignedIssue)

	// Query all in_progress issues (these are the ones being worked on)
	stdout, err := runBdCmd(f.townBeads, "list", "--status=in_progress", "--json")
	if err != nil {
		return result // Return empty map on error
	}

	var issues []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Assignee string `json:"assignee"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &issues); err != nil {
		return result
	}

	for _, issue := range issues {
		if issue.Assignee != "" {
			result[issue.Assignee] = assignedIssue{
				ID:    issue.ID,
				Title: issue.Title,
			}
		}
	}

	return result
}



// getMergeQueueCount returns the total number of open PRs across all repos.
func (f *LiveConvoyFetcher) getMergeQueueCount() int {
	mergeQueue, err := f.FetchMergeQueue()
	if err != nil {
		return 0
	}
	return len(mergeQueue)
}

// getRefineryStatusHint returns appropriate status for refinery based on merge queue.
func (f *LiveConvoyFetcher) getRefineryStatusHint(mergeQueueCount int) string {
	if mergeQueueCount == 0 {
		return "Idle - Waiting for PRs"
	}
	if mergeQueueCount == 1 {
		return "Processing 1 PR"
	}
	return fmt.Sprintf("Processing %d PRs", mergeQueueCount)
}

// truncateStatusHint truncates a status hint to 60 characters with ellipsis.
func truncateStatusHint(line string) string {
	if len(line) > 60 {
		return line[:57] + "..."
	}
	return line
}


// FetchMail fetches recent mail messages from the beads database.
func (f *LiveConvoyFetcher) FetchMail() ([]MailRow, error) {
	// List all message-type issues (mail)
	stdout, err := runBdCmd(f.townBeads, "list", "--type=message", "--json", "--limit=50")
	if err != nil {
		return nil, fmt.Errorf("listing mail: %w", err)
	}

	var messages []struct {
		ID        string   `json:"id"`
		Title     string   `json:"title"`
		Status    string   `json:"status"`
		CreatedAt string   `json:"created_at"`
		Priority  int      `json:"priority"`
		Assignee  string   `json:"assignee"`   // "to" address stored here
		CreatedBy string   `json:"created_by"` // "from" address
		Labels    []string `json:"labels"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &messages); err != nil {
		return nil, fmt.Errorf("parsing mail list: %w", err)
	}

	rows := make([]MailRow, 0, len(messages))
	for _, m := range messages {
		// Parse timestamp
		var timestamp time.Time
		var age string
		var sortKey int64
		if m.CreatedAt != "" {
			if t, err := time.Parse(time.RFC3339, m.CreatedAt); err == nil {
				timestamp = t
				age = formatMailAge(time.Since(t))
				sortKey = t.Unix()
			}
		}

		// Determine priority string
		priorityStr := "normal"
		switch m.Priority {
		case 0:
			priorityStr = "urgent"
		case 1:
			priorityStr = "high"
		case 2:
			priorityStr = "normal"
		case 3, 4:
			priorityStr = "low"
		}

		// Determine message type from labels
		msgType := "notification"
		for _, label := range m.Labels {
			if label == "task" || label == "reply" || label == "scavenge" {
				msgType = label
				break
			}
		}

		// Format from/to addresses for display
		from := formatAgentAddress(m.CreatedBy)
		to := formatAgentAddress(m.Assignee)

		rows = append(rows, MailRow{
			ID:        m.ID,
			From:      from,
			FromRaw:   m.CreatedBy,
			To:        to,
			Subject:   m.Title,
			Timestamp: timestamp.Format("15:04"),
			Age:       age,
			Priority:  priorityStr,
			Type:      msgType,
			Read:      m.Status == "closed",
			SortKey:   sortKey,
		})
	}

	// Sort by timestamp, newest first
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].SortKey > rows[j].SortKey
	})

	return rows, nil
}

// formatMailAge returns a human-readable age string.
func formatMailAge(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// formatAgentAddress shortens agent addresses for display.
// "gastown/polecats/Toast" -> "Toast (gastown)"
// "mayor/" -> "Mayor"
func formatAgentAddress(addr string) string {
	if addr == "" {
		return "—"
	}
	if addr == "mayor/" || addr == "mayor" {
		return "Mayor"
	}

	parts := strings.Split(addr, "/")
	if len(parts) >= 3 && parts[1] == "polecats" {
		return fmt.Sprintf("%s (%s)", parts[2], parts[0])
	}
	if len(parts) >= 3 && parts[1] == "crew" {
		return fmt.Sprintf("%s (%s/crew)", parts[2], parts[0])
	}
	if len(parts) >= 2 {
		return fmt.Sprintf("%s/%s", parts[0], parts[len(parts)-1])
	}
	return addr
}

// FetchRigs returns all registered rigs with their agent counts.
func (f *LiveConvoyFetcher) FetchRigs() ([]RigRow, error) {
	// Load rigs config from mayor/rigs.json
	rigsConfigPath := filepath.Join(f.townRoot, "mayor", "rigs.json")
	rigsConfig, err := config.LoadRigsConfig(rigsConfigPath)
	if err != nil {
		return nil, fmt.Errorf("loading rigs config: %w", err)
	}

	var rows []RigRow
	for name, entry := range rigsConfig.Rigs {
		row := RigRow{
			Name:   name,
			GitURL: entry.GitURL,
		}

		rigPath := filepath.Join(f.townRoot, name)

		// Count polecats
		polecatsDir := filepath.Join(rigPath, "polecats")
		if entries, err := os.ReadDir(polecatsDir); err == nil {
			for _, e := range entries {
				if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
					row.PolecatCount++
				}
			}
		}

		// Count crew
		crewDir := filepath.Join(rigPath, "crew")
		if entries, err := os.ReadDir(crewDir); err == nil {
			for _, e := range entries {
				if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
					row.CrewCount++
				}
			}
		}

		// Check for witness
		witnessPath := filepath.Join(rigPath, "witness")
		if _, err := os.Stat(witnessPath); err == nil {
			row.HasWitness = true
		}

		// Check for refinery
		refineryPath := filepath.Join(rigPath, "refinery", "rig")
		if _, err := os.Stat(refineryPath); err == nil {
			row.HasRefinery = true
		}

		rows = append(rows, row)
	}

	// Sort by name
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Name < rows[j].Name
	})

	return rows, nil
}

// FetchDogs returns all dogs in the kennel with their state.
func (f *LiveConvoyFetcher) FetchDogs() ([]DogRow, error) {
	kennelPath := filepath.Join(f.townRoot, "deacon", "dogs")

	entries, err := os.ReadDir(kennelPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No kennel yet
		}
		return nil, fmt.Errorf("reading kennel: %w", err)
	}

	var rows []DogRow
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}

		// Read dog state file
		stateFile := filepath.Join(kennelPath, name, ".dog.json")
		data, err := os.ReadFile(stateFile)
		if err != nil {
			continue // Not a valid dog
		}

		var state struct {
			Name       string            `json:"name"`
			State      string            `json:"state"`
			LastActive time.Time         `json:"last_active"`
			Work       string            `json:"work,omitempty"`
			Worktrees  map[string]string `json:"worktrees,omitempty"`
		}
		if err := json.Unmarshal(data, &state); err != nil {
			continue
		}

		rows = append(rows, DogRow{
			Name:       state.Name,
			State:      state.State,
			Work:       state.Work,
			LastActive: formatMailAge(time.Since(state.LastActive)),
			RigCount:   len(state.Worktrees),
		})
	}

	// Sort by name
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Name < rows[j].Name
	})

	return rows, nil
}

// FetchEscalations returns open escalations needing attention.
func (f *LiveConvoyFetcher) FetchEscalations() ([]EscalationRow, error) {
	// List open escalations
	stdout, err := runBdCmd(f.townBeads, "list", "--label=gt:escalation", "--status=open", "--json")
	if err != nil {
		return nil, nil // No escalations or bd not available
	}

	var issues []struct {
		ID          string   `json:"id"`
		Title       string   `json:"title"`
		CreatedAt   string   `json:"created_at"`
		CreatedBy   string   `json:"created_by"`
		Labels      []string `json:"labels"`
		Description string   `json:"description"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &issues); err != nil {
		return nil, fmt.Errorf("parsing escalations: %w", err)
	}

	var rows []EscalationRow
	for _, issue := range issues {
		row := EscalationRow{
			ID:          issue.ID,
			Title:       issue.Title,
			EscalatedBy: formatAgentAddress(issue.CreatedBy),
			Severity:    "medium", // default
		}

		// Parse severity from labels
		for _, label := range issue.Labels {
			if strings.HasPrefix(label, "severity:") {
				row.Severity = strings.TrimPrefix(label, "severity:")
			}
			if label == "acked" {
				row.Acked = true
			}
		}

		// Calculate age
		if issue.CreatedAt != "" {
			if t, err := time.Parse(time.RFC3339, issue.CreatedAt); err == nil {
				row.Age = formatMailAge(time.Since(t))
			}
		}

		rows = append(rows, row)
	}

	// Sort by severity (critical first), then by age
	severityOrder := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
	sort.Slice(rows, func(i, j int) bool {
		si, sj := severityOrder[rows[i].Severity], severityOrder[rows[j].Severity]
		return si < sj
	})

	return rows, nil
}

// FetchHealth returns system health status.
func (f *LiveConvoyFetcher) FetchHealth() (*HealthRow, error) {
	row := &HealthRow{}

	// Read deacon heartbeat
	heartbeatFile := filepath.Join(f.townRoot, "deacon", "heartbeat.json")
	if data, err := os.ReadFile(heartbeatFile); err == nil {
		var hb struct {
			LastHeartbeat   time.Time `json:"last_heartbeat"`
			Cycle           int64     `json:"cycle"`
			HealthyAgents   int       `json:"healthy_agents"`
			UnhealthyAgents int       `json:"unhealthy_agents"`
		}
		if err := json.Unmarshal(data, &hb); err == nil {
			row.DeaconCycle = hb.Cycle
			row.HealthyAgents = hb.HealthyAgents
			row.UnhealthyAgents = hb.UnhealthyAgents
			if !hb.LastHeartbeat.IsZero() {
				age := time.Since(hb.LastHeartbeat)
				row.DeaconHeartbeat = formatMailAge(age)
				row.HeartbeatFresh = age < 5*time.Minute
			} else {
				row.DeaconHeartbeat = "no timestamp"
			}
		}
	} else {
		row.DeaconHeartbeat = "no heartbeat"
	}

	// Check pause state
	pauseFile := filepath.Join(f.townRoot, ".runtime", "deacon", "paused.json")
	if data, err := os.ReadFile(pauseFile); err == nil {
		var pause struct {
			Paused bool   `json:"paused"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(data, &pause); err == nil {
			row.IsPaused = pause.Paused
			row.PauseReason = pause.Reason
		}
	}

	return row, nil
}

// FetchQueues returns work queues and their status.
func (f *LiveConvoyFetcher) FetchQueues() ([]QueueRow, error) {
	// List queue-type beads
	stdout, err := runBdCmd(f.townBeads, "list", "--type=queue", "--json")
	if err != nil {
		return nil, nil // No queues or bd not available
	}

	var queues []struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Status      string `json:"status"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &queues); err != nil {
		return nil, fmt.Errorf("parsing queues: %w", err)
	}

	var rows []QueueRow
	for _, q := range queues {
		row := QueueRow{
			Name:   q.Title,
			Status: q.Status,
		}

		// Parse counts from description (key: value format)
		// Best-effort parsing - ignore Sscanf errors as missing/malformed data is acceptable
		for _, line := range strings.Split(q.Description, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "available_count:") {
				_, _ = fmt.Sscanf(line, "available_count: %d", &row.Available)
			} else if strings.HasPrefix(line, "processing_count:") {
				_, _ = fmt.Sscanf(line, "processing_count: %d", &row.Processing)
			} else if strings.HasPrefix(line, "completed_count:") {
				_, _ = fmt.Sscanf(line, "completed_count: %d", &row.Completed)
			} else if strings.HasPrefix(line, "failed_count:") {
				_, _ = fmt.Sscanf(line, "failed_count: %d", &row.Failed)
			} else if strings.HasPrefix(line, "status:") {
				// Override with parsed status if present
				var s string
				_, _ = fmt.Sscanf(line, "status: %s", &s)
				if s != "" {
					row.Status = s
				}
			}
		}

		rows = append(rows, row)
	}

	return rows, nil
}

// FetchSessions returns nil — session tracking requires daemon RPC (not yet implemented).
func (f *LiveConvoyFetcher) FetchSessions() ([]SessionRow, error) {
	return nil, nil
}

// FetchHooks returns all hooked beads (work pinned to agents).
func (f *LiveConvoyFetcher) FetchHooks() ([]HookRow, error) {
	// Query all beads with status=hooked
	stdout, err := runBdCmd(f.townBeads, "list", "--status=hooked", "--json", "--limit=0")
	if err != nil {
		return nil, nil // No hooked beads or bd not available
	}

	var beads []struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Assignee  string `json:"assignee"`
		UpdatedAt string `json:"updated_at"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &beads); err != nil {
		return nil, fmt.Errorf("parsing hooked beads: %w", err)
	}

	var rows []HookRow
	for _, bead := range beads {
		row := HookRow{
			ID:       bead.ID,
			Title:    bead.Title,
			Assignee: bead.Assignee,
			Agent:    formatAgentAddress(bead.Assignee),
		}

		// Keep full title - CSS handles overflow

		// Calculate age and stale status
		if bead.UpdatedAt != "" {
			if t, err := time.Parse(time.RFC3339, bead.UpdatedAt); err == nil {
				age := time.Since(t)
				row.Age = formatMailAge(age)
				row.IsStale = age > time.Hour // Stale if hooked > 1 hour
			}
		}

		rows = append(rows, row)
	}

	// Sort by stale first (stuck work), then by age
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].IsStale != rows[j].IsStale {
			return rows[i].IsStale // Stale items first
		}
		return rows[i].Age > rows[j].Age
	})

	return rows, nil
}

// FetchMayor returns nil — mayor status tracking requires daemon RPC (not yet implemented).
func (f *LiveConvoyFetcher) FetchMayor() (*MayorStatus, error) {
	return nil, nil
}

// FetchIssues returns open issues (the backlog).
func (f *LiveConvoyFetcher) FetchIssues() ([]IssueRow, error) {
	// Query open issues (excluding internal types like messages, convoys, queues)
	stdout, err := runBdCmd(f.townBeads, "list", "--status=open", "--json", "--limit=50")
	if err != nil {
		return nil, nil // No issues or bd not available
	}

	var beads []struct {
		ID        string   `json:"id"`
		Title     string   `json:"title"`
		Type      string   `json:"type"`
		Priority  int      `json:"priority"`
		Labels    []string `json:"labels"`
		CreatedAt string   `json:"created_at"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &beads); err != nil {
		return nil, fmt.Errorf("parsing issues: %w", err)
	}

	var rows []IssueRow
	for _, bead := range beads {
		// Skip internal types (messages, convoys, queues, merge-requests, wisps)
		switch bead.Type {
		case "message", "convoy", "queue", "merge-request", "wisp", "agent":
			continue
		}

		row := IssueRow{
			ID:       bead.ID,
			Title:    bead.Title,
			Type:     bead.Type,
			Priority: bead.Priority,
		}

		// Keep full title - CSS handles overflow

		// Format labels (skip internal labels)
		var displayLabels []string
		for _, label := range bead.Labels {
			if !strings.HasPrefix(label, "gt:") && !strings.HasPrefix(label, "internal:") {
				displayLabels = append(displayLabels, label)
			}
		}
		if len(displayLabels) > 0 {
			row.Labels = strings.Join(displayLabels, ", ")
			if len(row.Labels) > 25 {
				row.Labels = row.Labels[:22] + "..."
			}
		}

		// Calculate age
		if bead.CreatedAt != "" {
			if t, err := time.Parse(time.RFC3339, bead.CreatedAt); err == nil {
				row.Age = formatMailAge(time.Since(t))
			}
		}

		rows = append(rows, row)
	}

	// Sort by priority (1=critical first), then by age
	sort.Slice(rows, func(i, j int) bool {
		pi, pj := rows[i].Priority, rows[j].Priority
		if pi == 0 {
			pi = 5 // Treat unset priority as low
		}
		if pj == 0 {
			pj = 5
		}
		if pi != pj {
			return pi < pj
		}
		return rows[i].Age > rows[j].Age // Older first for same priority
	})

	return rows, nil
}

// FetchActivity returns recent activity from the event log.
func (f *LiveConvoyFetcher) FetchActivity() ([]ActivityRow, error) {
	eventsPath := filepath.Join(f.townRoot, ".events.jsonl")

	// Read events file
	data, err := os.ReadFile(eventsPath)
	if err != nil {
		return nil, nil // No events file
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 {
		return nil, nil
	}

	// Take last 20 events (most recent)
	start := 0
	if len(lines) > 20 {
		start = len(lines) - 20
	}

	var rows []ActivityRow
	for i := len(lines) - 1; i >= start; i-- {
		line := lines[i]
		if line == "" {
			continue
		}

		var event struct {
			Timestamp  string                 `json:"ts"`
			Type       string                 `json:"type"`
			Actor      string                 `json:"actor"`
			Payload    map[string]interface{} `json:"payload"`
			Visibility string                 `json:"visibility"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		// Skip audit-only events
		if event.Visibility == "audit" {
			continue
		}

		row := ActivityRow{
			Type:  event.Type,
			Actor: formatAgentAddress(event.Actor),
			Icon:  eventIcon(event.Type),
		}

		// Calculate time ago
		if t, err := time.Parse(time.RFC3339, event.Timestamp); err == nil {
			row.Time = formatMailAge(time.Since(t))
		}

		// Generate human-readable summary
		row.Summary = eventSummary(event.Type, event.Actor, event.Payload)

		rows = append(rows, row)
	}

	return rows, nil
}

// eventIcon returns an emoji for an event type.
func eventIcon(eventType string) string {
	icons := map[string]string{
		"sling":             "🎯",
		"hook":              "🪝",
		"unhook":            "🔓",
		"done":              "✅",
		"mail":              "📬",
		"spawn":             "🦨",
		"kill":              "💀",
		"nudge":             "👉",
		"handoff":           "🤝",
		"session_start":     "▶️",
		"session_end":       "⏹️",
		"session_death":     "☠️",
		"mass_death":        "💥",
		"patrol_started":    "🔍",
		"patrol_complete":   "✔️",
		"escalation_sent":   "⚠️",
		"escalation_acked":  "👍",
		"escalation_closed": "🔕",
		"merge_started":     "🔀",
		"merged":            "✨",
		"merge_failed":      "❌",
		"boot":              "🚀",
		"halt":              "🛑",
	}
	if icon, ok := icons[eventType]; ok {
		return icon
	}
	return "📋"
}

// eventSummary generates a human-readable summary for an event.
func eventSummary(eventType, actor string, payload map[string]interface{}) string {
	shortActor := formatAgentAddress(actor)

	switch eventType {
	case "sling":
		bead, _ := payload["bead"].(string)
		target, _ := payload["target"].(string)
		return fmt.Sprintf("%s slung to %s", bead, formatAgentAddress(target))
	case "done":
		bead, _ := payload["bead"].(string)
		return fmt.Sprintf("%s completed %s", shortActor, bead)
	case "mail":
		to, _ := payload["to"].(string)
		subject, _ := payload["subject"].(string)
		if len(subject) > 25 {
			subject = subject[:22] + "..."
		}
		return fmt.Sprintf("→ %s: %s", formatAgentAddress(to), subject)
	case "spawn":
		return fmt.Sprintf("%s spawned", shortActor)
	case "kill":
		return fmt.Sprintf("%s killed", shortActor)
	case "hook":
		bead, _ := payload["bead"].(string)
		return fmt.Sprintf("%s hooked %s", shortActor, bead)
	case "unhook":
		bead, _ := payload["bead"].(string)
		return fmt.Sprintf("%s unhooked %s", shortActor, bead)
	case "merged":
		branch, _ := payload["branch"].(string)
		return fmt.Sprintf("merged %s", branch)
	case "merge_failed":
		reason, _ := payload["reason"].(string)
		if len(reason) > 30 {
			reason = reason[:27] + "..."
		}
		return fmt.Sprintf("merge failed: %s", reason)
	case "escalation_sent":
		return "escalation created"
	case "session_death":
		role, _ := payload["role"].(string)
		return fmt.Sprintf("%s session died", formatAgentAddress(role))
	case "mass_death":
		count, _ := payload["count"].(float64)
		return fmt.Sprintf("%.0f sessions died", count)
	default:
		return eventType
	}
}
