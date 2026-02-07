package rpcserver

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"connectrpc.com/connect"

	gastownv1 "github.com/steveyegge/gastown/gen/gastown/v1"
	"github.com/steveyegge/gastown/gen/gastown/v1/gastownv1connect"

	"github.com/steveyegge/gastown/internal/beads"
)

// SlingServer implements the SlingService.
type SlingServer struct {
	townRoot string
}

var _ gastownv1connect.SlingServiceHandler = (*SlingServer)(nil)

// NewSlingServer creates a new SlingServer.
func NewSlingServer(townRoot string) *SlingServer {
	return &SlingServer{townRoot: townRoot}
}

func (s *SlingServer) Sling(
	ctx context.Context,
	req *connect.Request[gastownv1.SlingRequest],
) (*connect.Response[gastownv1.SlingResponse], error) {
	beadID := req.Msg.BeadId
	if beadID == "" {
		return nil, invalidArg("bead_id", "bead ID is required")
	}

	target := req.Msg.Target
	if target == "" {
		return nil, invalidArg("target", "target rig is required")
	}

	// Build gt sling command
	args := []string{"sling", beadID, target}

	if req.Msg.Args != "" {
		args = append(args, "--args", req.Msg.Args)
	}
	if req.Msg.Subject != "" {
		args = append(args, "--subject", req.Msg.Subject)
	}
	if req.Msg.Message != "" {
		args = append(args, "--message", req.Msg.Message)
	}
	if req.Msg.Create {
		args = append(args, "--create")
	}
	if req.Msg.Force {
		args = append(args, "--force")
	}
	if req.Msg.NoConvoy {
		args = append(args, "--no-convoy")
	}
	if req.Msg.Convoy != "" {
		args = append(args, "--convoy", req.Msg.Convoy)
	}
	if req.Msg.NoMerge {
		args = append(args, "--no-merge")
	}
	if req.Msg.MergeStrategy != gastownv1.MergeStrategy_MERGE_STRATEGY_UNSPECIFIED {
		strategy := mergeStrategyToString(req.Msg.MergeStrategy)
		if strategy != "" {
			args = append(args, "--merge", strategy)
		}
	}
	if req.Msg.Owned {
		args = append(args, "--owned")
	}
	if req.Msg.Account != "" {
		args = append(args, "--account", req.Msg.Account)
	}
	if req.Msg.Agent != "" {
		args = append(args, "--agent", req.Msg.Agent)
	}

	cmd := exec.CommandContext(ctx, "gt", args...)
	cmd.Dir = s.townRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, cmdError("sling", err, output)
	}

	// Parse output for response data
	resp := &gastownv1.SlingResponse{
		BeadId: beadID,
	}

	outputStr := string(output)

	// Extract target agent from output
	if idx := strings.Index(outputStr, "Slinging"); idx != -1 {
		// Parse "Slinging <bead> to <target>..."
		lines := strings.Split(outputStr, "\n")
		for _, line := range lines {
			if strings.Contains(line, "Slinging") && strings.Contains(line, " to ") {
				parts := strings.Split(line, " to ")
				if len(parts) >= 2 {
					resp.TargetAgent = strings.TrimSuffix(strings.TrimSpace(parts[1]), "...")
				}
			}
		}
	}

	// Check for polecat spawn
	if strings.Contains(outputStr, "Polecat") && strings.Contains(outputStr, "spawned") {
		resp.PolecatSpawned = true
		// Extract polecat name
		lines := strings.Split(outputStr, "\n")
		for _, line := range lines {
			if strings.Contains(line, "Allocated polecat:") {
				parts := strings.Split(line, ":")
				if len(parts) >= 2 {
					resp.PolecatName = strings.TrimSpace(parts[1])
				}
			}
		}
	}

	// Check for convoy
	if strings.Contains(outputStr, "convoy") {
		lines := strings.Split(outputStr, "\n")
		for _, line := range lines {
			if strings.Contains(line, "Created convoy") || strings.Contains(line, "Added to convoy") {
				// Extract convoy ID (format: "convoy 🚚 hq-cv-xxx")
				if idx := strings.Index(line, "hq-cv-"); idx != -1 {
					end := idx + 12 // hq-cv-xxxxx
					if end > len(line) {
						end = len(line)
					}
					resp.ConvoyId = line[idx:end]
					resp.ConvoyCreated = strings.Contains(line, "Created")
				}
			}
		}
	}

	// Get bead title
	client := beads.New(beads.GetTownBeadsPath(s.townRoot))
	if issue, err := client.Show(beadID); err == nil && issue != nil {
		resp.BeadTitle = issue.Title
	}

	return connect.NewResponse(resp), nil
}

func (s *SlingServer) SlingFormula(
	ctx context.Context,
	req *connect.Request[gastownv1.SlingFormulaRequest],
) (*connect.Response[gastownv1.SlingFormulaResponse], error) {
	formula := req.Msg.Formula
	if formula == "" {
		return nil, invalidArg("formula", "formula name is required")
	}

	// Build gt sling command for formula
	args := []string{"sling", formula}

	if req.Msg.Target != "" {
		args = append(args, req.Msg.Target)
	}
	if req.Msg.OnBead != "" {
		args = append(args, "--on", req.Msg.OnBead)
	}
	for k, v := range req.Msg.Vars {
		args = append(args, "--var", fmt.Sprintf("%s=%s", k, v))
	}
	if req.Msg.Args != "" {
		args = append(args, "--args", req.Msg.Args)
	}
	if req.Msg.Subject != "" {
		args = append(args, "--subject", req.Msg.Subject)
	}
	if req.Msg.Message != "" {
		args = append(args, "--message", req.Msg.Message)
	}
	if req.Msg.Create {
		args = append(args, "--create")
	}
	if req.Msg.Force {
		args = append(args, "--force")
	}
	if req.Msg.Account != "" {
		args = append(args, "--account", req.Msg.Account)
	}
	if req.Msg.Agent != "" {
		args = append(args, "--agent", req.Msg.Agent)
	}

	cmd := exec.CommandContext(ctx, "gt", args...)
	cmd.Dir = s.townRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, cmdError("sling formula", err, output)
	}

	resp := &gastownv1.SlingFormulaResponse{
		BeadId: req.Msg.OnBead,
	}

	outputStr := string(output)

	// Extract wisp ID
	if strings.Contains(outputStr, "wisp created:") {
		lines := strings.Split(outputStr, "\n")
		for _, line := range lines {
			if strings.Contains(line, "wisp created:") {
				parts := strings.Split(line, ":")
				if len(parts) >= 2 {
					resp.WispId = strings.TrimSpace(parts[len(parts)-1])
				}
			}
		}
	}

	// Extract target agent
	if strings.Contains(outputStr, " to ") {
		lines := strings.Split(outputStr, "\n")
		for _, line := range lines {
			if strings.Contains(line, "Slinging") && strings.Contains(line, " to ") {
				parts := strings.Split(line, " to ")
				if len(parts) >= 2 {
					resp.TargetAgent = strings.TrimSuffix(strings.TrimSpace(parts[1]), "...")
				}
			}
		}
	}

	// Check for polecat spawn
	if strings.Contains(outputStr, "Polecat") && strings.Contains(outputStr, "spawned") {
		resp.PolecatSpawned = true
		lines := strings.Split(outputStr, "\n")
		for _, line := range lines {
			if strings.Contains(line, "Allocated polecat:") {
				parts := strings.Split(line, ":")
				if len(parts) >= 2 {
					resp.PolecatName = strings.TrimSpace(parts[1])
				}
			}
		}
	}

	return connect.NewResponse(resp), nil
}

func (s *SlingServer) SlingBatch(
	ctx context.Context,
	req *connect.Request[gastownv1.SlingBatchRequest],
) (*connect.Response[gastownv1.SlingBatchResponse], error) {
	if len(req.Msg.BeadIds) == 0 {
		return nil, invalidArg("bead_ids", "at least one bead ID is required")
	}
	if req.Msg.Rig == "" {
		return nil, invalidArg("rig", "target rig is required for batch sling")
	}

	// Build gt sling command with multiple beads
	args := append([]string{"sling"}, req.Msg.BeadIds...)
	args = append(args, req.Msg.Rig)

	if req.Msg.Args != "" {
		args = append(args, "--args", req.Msg.Args)
	}
	if req.Msg.Message != "" {
		args = append(args, "--message", req.Msg.Message)
	}
	if req.Msg.Force {
		args = append(args, "--force")
	}
	if req.Msg.Account != "" {
		args = append(args, "--account", req.Msg.Account)
	}
	if req.Msg.Agent != "" {
		args = append(args, "--agent", req.Msg.Agent)
	}

	cmd := exec.CommandContext(ctx, "gt", args...)
	cmd.Dir = s.townRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, cmdError("batch sling", err, output)
	}

	resp := &gastownv1.SlingBatchResponse{
		Results: make([]*gastownv1.BatchSlingResult, 0, len(req.Msg.BeadIds)),
	}

	outputStr := string(output)

	// Parse results for each bead
	for _, beadID := range req.Msg.BeadIds {
		result := &gastownv1.BatchSlingResult{
			BeadId:  beadID,
			Success: true, // Assume success if we got here
		}

		// Extract polecat name for this bead from output
		if strings.Contains(outputStr, beadID) {
			// Look for polecat assignment
			lines := strings.Split(outputStr, "\n")
			for i, line := range lines {
				if strings.Contains(line, beadID) && strings.Contains(line, "Slinging") {
					// Check subsequent lines for polecat info
					for j := i; j < len(lines) && j < i+10; j++ {
						if strings.Contains(lines[j], "Allocated polecat:") {
							parts := strings.Split(lines[j], ":")
							if len(parts) >= 2 {
								result.PolecatName = strings.TrimSpace(parts[1])
								result.TargetAgent = fmt.Sprintf("%s/polecats/%s", req.Msg.Rig, result.PolecatName)
							}
							break
						}
					}
				}
			}
		}

		resp.Results = append(resp.Results, result)
		resp.SuccessCount++
	}

	// Extract convoy ID if created
	if strings.Contains(outputStr, "convoy") {
		lines := strings.Split(outputStr, "\n")
		for _, line := range lines {
			if strings.Contains(line, "Created convoy") {
				if idx := strings.Index(line, "hq-cv-"); idx != -1 {
					end := idx + 12
					if end > len(line) {
						end = len(line)
					}
					resp.ConvoyId = line[idx:end]
				}
			}
		}
	}

	return connect.NewResponse(resp), nil
}

func (s *SlingServer) Unsling(
	ctx context.Context,
	req *connect.Request[gastownv1.UnslingRequest],
) (*connect.Response[gastownv1.UnslingResponse], error) {
	beadID := req.Msg.BeadId
	if beadID == "" {
		return nil, invalidArg("bead_id", "bead ID is required")
	}

	// Get current assignee before unsling
	client := beads.New(beads.GetTownBeadsPath(s.townRoot))
	issue, err := client.Show(beadID)
	if err != nil {
		return nil, notFound("bead", beadID)
	}

	previousAgent := ""
	wasIncomplete := false
	if issue != nil {
		previousAgent = issue.Assignee
		wasIncomplete = issue.Status != "closed"
	}

	// Build gt unhook command
	args := []string{"unhook", beadID}
	if req.Msg.Force {
		args = append(args, "--force")
	}

	cmd := exec.CommandContext(ctx, "gt", args...)
	cmd.Dir = s.townRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, cmdError("unsling", err, output)
	}

	return connect.NewResponse(&gastownv1.UnslingResponse{
		BeadId:        beadID,
		PreviousAgent: previousAgent,
		WasIncomplete: wasIncomplete,
	}), nil
}

func (s *SlingServer) GetWorkload(
	ctx context.Context,
	req *connect.Request[gastownv1.GetWorkloadRequest],
) (*connect.Response[gastownv1.GetWorkloadResponse], error) {
	agent := req.Msg.Agent
	if agent == "" {
		return nil, invalidArg("agent", "agent address is required")
	}

	// Query beads with status=hooked and assignee=agent
	client := beads.New(beads.GetTownBeadsPath(s.townRoot))
	issues, err := client.List(beads.ListOptions{
		Status:   "hooked",
		Assignee: agent,
		Priority: -1,
	})
	if err != nil {
		return nil, internalErr("failed to list hooked beads", err)
	}

	resp := &gastownv1.GetWorkloadResponse{
		Beads: make([]*gastownv1.HookedBead, 0, len(issues)),
		Total: int32(len(issues)),
	}

	for _, issue := range issues {
		bead := &gastownv1.HookedBead{
			Id:       issue.ID,
			Title:    issue.Title,
			BeadType: issue.Type,
			Priority: priorityToString(issue.Priority),
		}
		resp.Beads = append(resp.Beads, bead)
	}

	return connect.NewResponse(resp), nil
}

func mergeStrategyToString(ms gastownv1.MergeStrategy) string {
	switch ms {
	case gastownv1.MergeStrategy_MERGE_STRATEGY_DIRECT:
		return "direct"
	case gastownv1.MergeStrategy_MERGE_STRATEGY_MR:
		return "mr"
	case gastownv1.MergeStrategy_MERGE_STRATEGY_LOCAL:
		return "local"
	default:
		return ""
	}
}

func priorityToString(p int) string {
	switch p {
	case 1:
		return "P1"
	case 2:
		return "P2"
	case 3:
		return "P3"
	case 4:
		return "P4"
	default:
		return "P2"
	}
}
