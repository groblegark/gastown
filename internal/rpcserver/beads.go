package rpcserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"

	gastownv1 "github.com/steveyegge/gastown/gen/gastown/v1"
	"github.com/steveyegge/gastown/gen/gastown/v1/gastownv1connect"

	"github.com/steveyegge/gastown/internal/beads"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// BeadsServer implements the BeadsService.
type BeadsServer struct {
	townRoot string
}

var _ gastownv1connect.BeadsServiceHandler = (*BeadsServer)(nil)

// NewBeadsServer creates a new BeadsServer.
func NewBeadsServer(townRoot string) *BeadsServer {
	return &BeadsServer{townRoot: townRoot}
}

// issueToProto converts a beads.Issue to a proto Issue.
func issueToProto(issue *beads.Issue) *gastownv1.Issue {
	if issue == nil {
		return nil
	}

	protoIssue := &gastownv1.Issue{
		Id:              issue.ID,
		Title:           issue.Title,
		Description:     issue.Description,
		Priority:        int32(issue.Priority),
		Parent:          issue.Parent,
		Assignee:        issue.Assignee,
		CreatedBy:       issue.CreatedBy,
		Labels:          issue.Labels,
		Children:        issue.Children,
		DependsOn:       issue.DependsOn,
		Blocks:          issue.Blocks,
		BlockedBy:       issue.BlockedBy,
		DependencyCount: int32(issue.DependencyCount),
		DependentCount:  int32(issue.DependentCount),
		BlockedByCount:  int32(issue.BlockedByCount),
		HookBead:        issue.HookBead,
		AgentState:      issue.AgentState,
	}

	// Convert status
	protoIssue.Status = statusToProto(issue.Status)

	// Convert type
	protoIssue.Type = typeToProto(issue.Type)

	// Convert timestamps
	if issue.CreatedAt != "" {
		if t, err := time.Parse(time.RFC3339, issue.CreatedAt); err == nil {
			protoIssue.CreatedAt = timestamppb.New(t)
		}
	}
	if issue.UpdatedAt != "" {
		if t, err := time.Parse(time.RFC3339, issue.UpdatedAt); err == nil {
			protoIssue.UpdatedAt = timestamppb.New(t)
		}
	}
	if issue.ClosedAt != "" {
		if t, err := time.Parse(time.RFC3339, issue.ClosedAt); err == nil {
			protoIssue.ClosedAt = timestamppb.New(t)
		}
	}

	return protoIssue
}

func statusToProto(status string) gastownv1.IssueStatus {
	switch strings.ToLower(status) {
	case "open":
		return gastownv1.IssueStatus_ISSUE_STATUS_OPEN
	case "in_progress":
		return gastownv1.IssueStatus_ISSUE_STATUS_IN_PROGRESS
	case "blocked":
		return gastownv1.IssueStatus_ISSUE_STATUS_BLOCKED
	case "deferred":
		return gastownv1.IssueStatus_ISSUE_STATUS_DEFERRED
	case "closed":
		return gastownv1.IssueStatus_ISSUE_STATUS_CLOSED
	default:
		return gastownv1.IssueStatus_ISSUE_STATUS_UNSPECIFIED
	}
}

func typeToProto(issueType string) gastownv1.IssueType {
	switch strings.ToLower(issueType) {
	case "task":
		return gastownv1.IssueType_ISSUE_TYPE_TASK
	case "bug":
		return gastownv1.IssueType_ISSUE_TYPE_BUG
	case "feature":
		return gastownv1.IssueType_ISSUE_TYPE_FEATURE
	case "epic":
		return gastownv1.IssueType_ISSUE_TYPE_EPIC
	case "chore":
		return gastownv1.IssueType_ISSUE_TYPE_CHORE
	case "merge-request":
		return gastownv1.IssueType_ISSUE_TYPE_MERGE_REQUEST
	case "molecule":
		return gastownv1.IssueType_ISSUE_TYPE_MOLECULE
	case "gate":
		return gastownv1.IssueType_ISSUE_TYPE_GATE
	case "message":
		return gastownv1.IssueType_ISSUE_TYPE_MESSAGE
	case "decision":
		return gastownv1.IssueType_ISSUE_TYPE_DECISION
	case "convoy":
		return gastownv1.IssueType_ISSUE_TYPE_CONVOY
	default:
		return gastownv1.IssueType_ISSUE_TYPE_UNSPECIFIED
	}
}

func protoToTypeString(t gastownv1.IssueType) string {
	switch t {
	case gastownv1.IssueType_ISSUE_TYPE_TASK:
		return "task"
	case gastownv1.IssueType_ISSUE_TYPE_BUG:
		return "bug"
	case gastownv1.IssueType_ISSUE_TYPE_FEATURE:
		return "feature"
	case gastownv1.IssueType_ISSUE_TYPE_EPIC:
		return "epic"
	case gastownv1.IssueType_ISSUE_TYPE_CHORE:
		return "chore"
	case gastownv1.IssueType_ISSUE_TYPE_MERGE_REQUEST:
		return "merge-request"
	case gastownv1.IssueType_ISSUE_TYPE_MOLECULE:
		return "molecule"
	case gastownv1.IssueType_ISSUE_TYPE_GATE:
		return "gate"
	case gastownv1.IssueType_ISSUE_TYPE_MESSAGE:
		return "message"
	case gastownv1.IssueType_ISSUE_TYPE_DECISION:
		return "decision"
	case gastownv1.IssueType_ISSUE_TYPE_CONVOY:
		return "convoy"
	default:
		return ""
	}
}

func (s *BeadsServer) ListIssues(
	ctx context.Context,
	req *connect.Request[gastownv1.ListIssuesRequest],
) (*connect.Response[gastownv1.ListIssuesResponse], error) {
	b := beads.New(s.townRoot)

	opts := beads.ListOptions{
		Status:     req.Msg.Status,
		Priority:   int(req.Msg.Priority),
		Parent:     req.Msg.Parent,
		Assignee:   req.Msg.Assignee,
		NoAssignee: req.Msg.NoAssignee,
	}

	// Handle type filter
	if req.Msg.Type != gastownv1.IssueType_ISSUE_TYPE_UNSPECIFIED {
		opts.Type = protoToTypeString(req.Msg.Type)
	}

	// Handle label filter
	if req.Msg.Label != "" {
		opts.Label = req.Msg.Label
	}

	issues, err := b.List(opts)
	if err != nil {
		return nil, classifyErr("listing issues", err)
	}

	var protoIssues []*gastownv1.Issue
	for _, issue := range issues {
		protoIssues = append(protoIssues, issueToProto(issue))
	}

	// Apply limit/offset if specified
	total := int32(len(protoIssues))
	if req.Msg.Offset > 0 && int(req.Msg.Offset) < len(protoIssues) {
		protoIssues = protoIssues[req.Msg.Offset:]
	}
	if req.Msg.Limit > 0 && int(req.Msg.Limit) < len(protoIssues) {
		protoIssues = protoIssues[:req.Msg.Limit]
	}

	return connect.NewResponse(&gastownv1.ListIssuesResponse{
		Issues: protoIssues,
		Total:  total,
	}), nil
}

func (s *BeadsServer) GetIssue(
	ctx context.Context,
	req *connect.Request[gastownv1.GetIssueRequest],
) (*connect.Response[gastownv1.GetIssueResponse], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("issue ID required"))
	}

	b := beads.New(s.townRoot)

	issue, err := b.Show(req.Msg.Id)
	if err != nil {
		return nil, notFoundOrInternal("getting issue "+req.Msg.Id, err)
	}

	return connect.NewResponse(&gastownv1.GetIssueResponse{
		Issue: issueToProto(issue),
	}), nil
}

func (s *BeadsServer) CreateIssue(
	ctx context.Context,
	req *connect.Request[gastownv1.CreateIssueRequest],
) (*connect.Response[gastownv1.CreateIssueResponse], error) {
	if req.Msg.Title == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("title required"))
	}

	b := beads.New(s.townRoot)

	opts := beads.CreateOptions{
		Title:       req.Msg.Title,
		Description: req.Msg.Description,
		Priority:    int(req.Msg.Priority),
		Parent:      req.Msg.Parent,
		Actor:       req.Msg.Actor,
		Ephemeral:   req.Msg.Ephemeral,
	}

	// Handle type
	if req.Msg.Type != gastownv1.IssueType_ISSUE_TYPE_UNSPECIFIED {
		opts.Type = protoToTypeString(req.Msg.Type)
	}

	var issue *beads.Issue
	var err error

	if req.Msg.Id != "" {
		// Create with specific ID
		issue, err = b.CreateWithID(req.Msg.Id, opts)
	} else {
		issue, err = b.Create(opts)
	}

	if err != nil {
		return nil, classifyErr("creating issue", err)
	}

	// Handle initial labels
	if len(req.Msg.Labels) > 0 {
		for _, label := range req.Msg.Labels {
			if err := b.AddLabel(issue.ID, label); err != nil {
				// Non-fatal, continue
			}
		}
		// Refresh issue to get labels
		issue, _ = b.Show(issue.ID)
	}

	// Handle initial assignee
	if req.Msg.Assignee != "" {
		assignee := req.Msg.Assignee
		if err := b.Update(issue.ID, beads.UpdateOptions{Assignee: &assignee}); err != nil {
			// Non-fatal
		}
		issue, _ = b.Show(issue.ID)
	}

	return connect.NewResponse(&gastownv1.CreateIssueResponse{
		Issue: issueToProto(issue),
	}), nil
}

func (s *BeadsServer) UpdateIssue(
	ctx context.Context,
	req *connect.Request[gastownv1.UpdateIssueRequest],
) (*connect.Response[gastownv1.UpdateIssueResponse], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("issue ID required"))
	}

	b := beads.New(s.townRoot)

	opts := beads.UpdateOptions{
		AddLabels:    req.Msg.AddLabels,
		RemoveLabels: req.Msg.RemoveLabels,
		SetLabels:    req.Msg.SetLabels,
	}

	if req.Msg.Title != nil {
		opts.Title = req.Msg.Title
	}
	if req.Msg.Status != nil {
		opts.Status = req.Msg.Status
	}
	if req.Msg.Priority != nil {
		p := int(*req.Msg.Priority)
		opts.Priority = &p
	}
	if req.Msg.Description != nil {
		opts.Description = req.Msg.Description
	}
	if req.Msg.Assignee != nil {
		opts.Assignee = req.Msg.Assignee
	}

	if err := b.Update(req.Msg.Id, opts); err != nil {
		return nil, notFoundOrInternal("updating issue "+req.Msg.Id, err)
	}

	// Fetch updated issue
	issue, err := b.Show(req.Msg.Id)
	if err != nil {
		return nil, notFoundOrInternal("fetching updated issue "+req.Msg.Id, err)
	}

	return connect.NewResponse(&gastownv1.UpdateIssueResponse{
		Issue: issueToProto(issue),
	}), nil
}

func (s *BeadsServer) CloseIssues(
	ctx context.Context,
	req *connect.Request[gastownv1.CloseIssuesRequest],
) (*connect.Response[gastownv1.CloseIssuesResponse], error) {
	if len(req.Msg.Ids) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("at least one issue ID required"))
	}

	b := beads.New(s.townRoot)

	var closedCount int32
	var failedIDs []string

	for _, id := range req.Msg.Ids {
		var err error
		if req.Msg.Reason != "" {
			err = b.CloseWithReason(req.Msg.Reason, id)
		} else {
			err = b.Close(id)
		}
		if err != nil {
			failedIDs = append(failedIDs, id)
		} else {
			closedCount++
		}
	}

	return connect.NewResponse(&gastownv1.CloseIssuesResponse{
		ClosedCount: closedCount,
		FailedIds:   failedIDs,
	}), nil
}

func (s *BeadsServer) ReopenIssues(
	ctx context.Context,
	req *connect.Request[gastownv1.ReopenIssuesRequest],
) (*connect.Response[gastownv1.ReopenIssuesResponse], error) {
	if len(req.Msg.Ids) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("at least one issue ID required"))
	}

	b := beads.New(s.townRoot)

	var reopenedCount int32
	var failedIDs []string

	openStatus := "open"
	for _, id := range req.Msg.Ids {
		if err := b.Update(id, beads.UpdateOptions{Status: &openStatus}); err != nil {
			failedIDs = append(failedIDs, id)
		} else {
			reopenedCount++
		}
	}

	return connect.NewResponse(&gastownv1.ReopenIssuesResponse{
		ReopenedCount: reopenedCount,
		FailedIds:     failedIDs,
	}), nil
}

func (s *BeadsServer) SearchIssues(
	ctx context.Context,
	req *connect.Request[gastownv1.SearchIssuesRequest],
) (*connect.Response[gastownv1.SearchIssuesResponse], error) {
	if req.Msg.Query == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("search query required"))
	}

	b := beads.New(s.townRoot)

	// Build search arguments
	args := []string{"search", req.Msg.Query, "--json"}

	if req.Msg.Status != "" {
		args = append(args, "--status="+req.Msg.Status)
	}
	if req.Msg.Type != gastownv1.IssueType_ISSUE_TYPE_UNSPECIFIED {
		args = append(args, "--type="+protoToTypeString(req.Msg.Type))
	}
	if req.Msg.Label != "" {
		args = append(args, "--label="+req.Msg.Label)
	}
	if req.Msg.Assignee != "" {
		args = append(args, "--assignee="+req.Msg.Assignee)
	}
	if req.Msg.PriorityMin > 0 {
		args = append(args, fmt.Sprintf("--priority-min=%d", req.Msg.PriorityMin))
	}
	if req.Msg.PriorityMax > 0 {
		args = append(args, fmt.Sprintf("--priority-max=%d", req.Msg.PriorityMax))
	}
	if req.Msg.Limit > 0 {
		args = append(args, fmt.Sprintf("--limit=%d", req.Msg.Limit))
	}

	out, err := b.Run(args...)
	if err != nil {
		return nil, classifyErr("searching issues", err)
	}

	// Parse JSON output - bd search returns array of issues
	var issues []*beads.Issue
	if err := parseJSON(out, &issues); err != nil {
		// Search might return empty results as text, not JSON error
		return connect.NewResponse(&gastownv1.SearchIssuesResponse{
			Issues: nil,
			Total:  0,
		}), nil
	}

	var protoIssues []*gastownv1.Issue
	for _, issue := range issues {
		protoIssues = append(protoIssues, issueToProto(issue))
	}

	return connect.NewResponse(&gastownv1.SearchIssuesResponse{
		Issues: protoIssues,
		Total:  int32(len(protoIssues)),
	}), nil
}

func (s *BeadsServer) GetReadyIssues(
	ctx context.Context,
	req *connect.Request[gastownv1.GetReadyIssuesRequest],
) (*connect.Response[gastownv1.GetReadyIssuesResponse], error) {
	b := beads.New(s.townRoot)

	var issues []*beads.Issue
	var err error

	if req.Msg.Label != "" {
		issues, err = b.ReadyWithType(req.Msg.Label)
	} else {
		issues, err = b.Ready()
	}

	if err != nil {
		return nil, classifyErr("listing ready issues", err)
	}

	var protoIssues []*gastownv1.Issue
	for _, issue := range issues {
		protoIssues = append(protoIssues, issueToProto(issue))
	}

	// Apply limit
	if req.Msg.Limit > 0 && int(req.Msg.Limit) < len(protoIssues) {
		protoIssues = protoIssues[:req.Msg.Limit]
	}

	return connect.NewResponse(&gastownv1.GetReadyIssuesResponse{
		Issues: protoIssues,
		Total:  int32(len(issues)),
	}), nil
}

func (s *BeadsServer) GetBlockedIssues(
	ctx context.Context,
	req *connect.Request[gastownv1.GetBlockedIssuesRequest],
) (*connect.Response[gastownv1.GetBlockedIssuesResponse], error) {
	b := beads.New(s.townRoot)

	issues, err := b.Blocked()
	if err != nil {
		return nil, classifyErr("listing blocked issues", err)
	}

	var protoIssues []*gastownv1.Issue
	for _, issue := range issues {
		protoIssues = append(protoIssues, issueToProto(issue))
	}

	// Apply limit
	if req.Msg.Limit > 0 && int(req.Msg.Limit) < len(protoIssues) {
		protoIssues = protoIssues[:req.Msg.Limit]
	}

	return connect.NewResponse(&gastownv1.GetBlockedIssuesResponse{
		Issues: protoIssues,
		Total:  int32(len(issues)),
	}), nil
}

func (s *BeadsServer) AddDependency(
	ctx context.Context,
	req *connect.Request[gastownv1.AddDependencyRequest],
) (*connect.Response[gastownv1.AddDependencyResponse], error) {
	if req.Msg.IssueId == "" || req.Msg.DependsOnId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("both issue_id and depends_on_id required"))
	}

	b := beads.New(s.townRoot)

	var err error
	switch req.Msg.Type {
	case gastownv1.DependencyType_DEPENDENCY_TYPE_BLOCKS:
		err = b.AddTypedDependency(req.Msg.IssueId, req.Msg.DependsOnId, "blocks")
	case gastownv1.DependencyType_DEPENDENCY_TYPE_TRACKS:
		err = b.AddTypedDependency(req.Msg.IssueId, req.Msg.DependsOnId, "tracks")
	default:
		err = b.AddDependency(req.Msg.IssueId, req.Msg.DependsOnId)
	}

	if err != nil {
		return nil, classifyErr("adding dependency", err)
	}

	return connect.NewResponse(&gastownv1.AddDependencyResponse{
		Success: true,
	}), nil
}

func (s *BeadsServer) RemoveDependency(
	ctx context.Context,
	req *connect.Request[gastownv1.RemoveDependencyRequest],
) (*connect.Response[gastownv1.RemoveDependencyResponse], error) {
	if req.Msg.IssueId == "" || req.Msg.DependsOnId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("both issue_id and depends_on_id required"))
	}

	b := beads.New(s.townRoot)

	if err := b.RemoveDependency(req.Msg.IssueId, req.Msg.DependsOnId); err != nil {
		return nil, classifyErr("removing dependency", err)
	}

	return connect.NewResponse(&gastownv1.RemoveDependencyResponse{
		Success: true,
	}), nil
}

func (s *BeadsServer) ListDependencies(
	ctx context.Context,
	req *connect.Request[gastownv1.ListDependenciesRequest],
) (*connect.Response[gastownv1.ListDependenciesResponse], error) {
	if req.Msg.IssueId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("issue_id required"))
	}

	b := beads.New(s.townRoot)

	// Convert dependency type to string
	var depType string
	switch req.Msg.Type {
	case gastownv1.DependencyType_DEPENDENCY_TYPE_BLOCKS:
		depType = "blocks"
	case gastownv1.DependencyType_DEPENDENCY_TYPE_TRACKS:
		depType = "tracks"
	}

	issues, err := b.ListDependencies(req.Msg.IssueId, req.Msg.Direction, depType)
	if err != nil {
		return nil, classifyErr("listing dependencies", err)
	}

	var protoIssues []*gastownv1.Issue
	for _, issue := range issues {
		protoIssues = append(protoIssues, issueToProto(issue))
	}

	return connect.NewResponse(&gastownv1.ListDependenciesResponse{
		Dependencies: protoIssues,
	}), nil
}

func (s *BeadsServer) AddComment(
	ctx context.Context,
	req *connect.Request[gastownv1.AddCommentRequest],
) (*connect.Response[gastownv1.AddCommentResponse], error) {
	if req.Msg.IssueId == "" || req.Msg.Text == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("issue_id and text required"))
	}

	b := beads.New(s.townRoot)

	// Use bd comments add
	args := []string{"comments", "add", req.Msg.IssueId, req.Msg.Text}
	if req.Msg.Author != "" {
		args = append(args, "--actor="+req.Msg.Author)
	}

	if _, err := b.Run(args...); err != nil {
		return nil, classifyErr("adding comment", err)
	}

	// Return a simple response (bd doesn't return the created comment)
	return connect.NewResponse(&gastownv1.AddCommentResponse{
		Comment: &gastownv1.Comment{
			IssueId:   req.Msg.IssueId,
			Text:      req.Msg.Text,
			Author:    req.Msg.Author,
			CreatedAt: timestamppb.Now(),
		},
	}), nil
}

func (s *BeadsServer) ListComments(
	ctx context.Context,
	req *connect.Request[gastownv1.ListCommentsRequest],
) (*connect.Response[gastownv1.ListCommentsResponse], error) {
	if req.Msg.IssueId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("issue_id required"))
	}

	b := beads.New(s.townRoot)

	args := []string{"comments", "list", req.Msg.IssueId, "--json"}
	out, err := b.Run(args...)
	if err != nil {
		// No comments returns empty, not error
		return connect.NewResponse(&gastownv1.ListCommentsResponse{
			Comments: nil,
		}), nil
	}

	// Parse comments - bd comments list returns array
	type bdComment struct {
		ID        string `json:"id"`
		IssueID   string `json:"issue_id"`
		Text      string `json:"text"`
		Author    string `json:"author"`
		CreatedAt string `json:"created_at"`
	}

	var comments []bdComment
	if err := parseJSON(out, &comments); err != nil {
		return connect.NewResponse(&gastownv1.ListCommentsResponse{
			Comments: nil,
		}), nil
	}

	var protoComments []*gastownv1.Comment
	for _, c := range comments {
		pc := &gastownv1.Comment{
			Id:      c.ID,
			IssueId: c.IssueID,
			Text:    c.Text,
			Author:  c.Author,
		}
		if c.CreatedAt != "" {
			if t, err := time.Parse(time.RFC3339, c.CreatedAt); err == nil {
				pc.CreatedAt = timestamppb.New(t)
			}
		}
		protoComments = append(protoComments, pc)
	}

	// Apply limit
	if req.Msg.Limit > 0 && int(req.Msg.Limit) < len(protoComments) {
		protoComments = protoComments[:req.Msg.Limit]
	}

	return connect.NewResponse(&gastownv1.ListCommentsResponse{
		Comments: protoComments,
	}), nil
}

func (s *BeadsServer) ManageLabels(
	ctx context.Context,
	req *connect.Request[gastownv1.ManageLabelsRequest],
) (*connect.Response[gastownv1.ManageLabelsResponse], error) {
	if req.Msg.IssueId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("issue_id required"))
	}

	b := beads.New(s.townRoot)

	// Add labels
	for _, label := range req.Msg.Add {
		if err := b.AddLabel(req.Msg.IssueId, label); err != nil {
			// Continue on error
		}
	}

	// Remove labels
	for _, label := range req.Msg.Remove {
		if _, err := b.Run("label", "remove", req.Msg.IssueId, label); err != nil {
			// Continue on error
		}
	}

	// Fetch updated issue to get current labels
	issue, err := b.Show(req.Msg.IssueId)
	if err != nil {
		return nil, notFoundOrInternal("fetching issue labels "+req.Msg.IssueId, err)
	}

	return connect.NewResponse(&gastownv1.ManageLabelsResponse{
		Labels: issue.Labels,
	}), nil
}

func (s *BeadsServer) GetStats(
	ctx context.Context,
	req *connect.Request[gastownv1.GetStatsRequest],
) (*connect.Response[gastownv1.GetStatsResponse], error) {
	b := beads.New(s.townRoot)

	rawStats, err := b.Stats()
	if err != nil {
		return nil, classifyErr("getting stats", err)
	}

	// Get counts by listing issues
	openIssues, _ := b.List(beads.ListOptions{Status: "open", Priority: -1})
	closedIssues, _ := b.List(beads.ListOptions{Status: "closed", Priority: -1})
	inProgressIssues, _ := b.List(beads.ListOptions{Status: "in_progress", Priority: -1})
	blockedIssues, _ := b.Blocked()

	return connect.NewResponse(&gastownv1.GetStatsResponse{
		TotalIssues:      int32(len(openIssues) + len(closedIssues) + len(inProgressIssues)),
		OpenIssues:       int32(len(openIssues)),
		ClosedIssues:     int32(len(closedIssues)),
		InProgressIssues: int32(len(inProgressIssues)),
		BlockedIssues:    int32(len(blockedIssues)),
		RawStats:         rawStats,
	}), nil
}

// parseJSON is a helper to parse JSON from bd output
func parseJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
