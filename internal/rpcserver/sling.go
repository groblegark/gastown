package rpcserver

import (
	"context"
	"fmt"
	"io"

	"connectrpc.com/connect"

	gastownv1 "github.com/steveyegge/gastown/gen/gastown/v1"
	"github.com/steveyegge/gastown/gen/gastown/v1/gastownv1connect"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/sling"
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
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("bead_id is required"))
	}

	target := req.Msg.Target
	if target == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("target is required for RPC sling"))
	}

	mergeStrategy := ""
	if req.Msg.MergeStrategy != gastownv1.MergeStrategy_MERGE_STRATEGY_UNSPECIFIED {
		mergeStrategy = mergeStrategyToString(req.Msg.MergeStrategy)
	}

	result, err := sling.Sling(sling.SlingOptions{
		BeadID:        beadID,
		Target:        target,
		TownRoot:      s.townRoot,
		Args:          req.Msg.Args,
		Subject:       req.Msg.Subject,
		Message:       req.Msg.Message,
		Create:        req.Msg.Create,
		Force:         req.Msg.Force,
		NoConvoy:      req.Msg.NoConvoy,
		Convoy:        req.Msg.Convoy,
		NoMerge:       req.Msg.NoMerge,
		MergeStrategy: mergeStrategy,
		Owned:         req.Msg.Owned,
		Account:       req.Msg.Account,
		Agent:         req.Msg.Agent,
		Output:        io.Discard,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal,
			fmt.Errorf("sling failed: %w", err))
	}

	return connect.NewResponse(&gastownv1.SlingResponse{
		BeadId:         result.BeadID,
		TargetAgent:    result.TargetAgent,
		ConvoyId:       result.ConvoyID,
		PolecatSpawned: result.PolecatSpawned,
		PolecatName:    result.PolecatName,
		BeadTitle:      result.BeadTitle,
		ConvoyCreated:  result.ConvoyCreated,
	}), nil
}

func (s *SlingServer) SlingFormula(
	ctx context.Context,
	req *connect.Request[gastownv1.SlingFormulaRequest],
) (*connect.Response[gastownv1.SlingFormulaResponse], error) {
	formula := req.Msg.Formula
	if formula == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("formula is required"))
	}

	// Convert proto vars map to []string key=value pairs
	var vars []string
	for k, v := range req.Msg.Vars {
		vars = append(vars, fmt.Sprintf("%s=%s", k, v))
	}

	result, err := sling.SlingFormula(sling.FormulaOptions{
		Formula:  formula,
		TownRoot: s.townRoot,
		Target:   req.Msg.Target,
		OnBead:   req.Msg.OnBead,
		Vars:     vars,
		Args:     req.Msg.Args,
		Subject:  req.Msg.Subject,
		Message:  req.Msg.Message,
		Create:   req.Msg.Create,
		Force:    req.Msg.Force,
		Account:  req.Msg.Account,
		Agent:    req.Msg.Agent,
		Output:   io.Discard,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal,
			fmt.Errorf("sling formula failed: %w", err))
	}

	return connect.NewResponse(&gastownv1.SlingFormulaResponse{
		WispId:         result.WispID,
		TargetAgent:    result.TargetAgent,
		BeadId:         result.BeadID,
		ConvoyId:       result.ConvoyID,
		PolecatSpawned: result.PolecatSpawned,
		PolecatName:    result.PolecatName,
	}), nil
}

func (s *SlingServer) SlingBatch(
	ctx context.Context,
	req *connect.Request[gastownv1.SlingBatchRequest],
) (*connect.Response[gastownv1.SlingBatchResponse], error) {
	if len(req.Msg.BeadIds) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("bead_ids is required"))
	}
	if req.Msg.Rig == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("rig is required for batch sling"))
	}

	result, err := sling.SlingBatch(sling.BatchOptions{
		BeadIDs:  req.Msg.BeadIds,
		Rig:      req.Msg.Rig,
		TownRoot: s.townRoot,
		Args:     req.Msg.Args,
		Message:  req.Msg.Message,
		Force:    req.Msg.Force,
		Account:  req.Msg.Account,
		Agent:    req.Msg.Agent,
		Output:   io.Discard,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal,
			fmt.Errorf("batch sling failed: %w", err))
	}

	// Map internal results to proto response
	protoResults := make([]*gastownv1.BatchSlingResult, 0, len(result.Results))
	for _, r := range result.Results {
		protoResults = append(protoResults, &gastownv1.BatchSlingResult{
			BeadId:      r.BeadID,
			Success:     r.Success,
			Error:       r.Error,
			TargetAgent: r.TargetAgent,
			PolecatName: r.PolecatName,
		})
	}

	return connect.NewResponse(&gastownv1.SlingBatchResponse{
		Results:      protoResults,
		ConvoyId:     result.ConvoyID,
		SuccessCount: result.SuccessCount,
		FailureCount: result.FailureCount,
	}), nil
}

func (s *SlingServer) Unsling(
	ctx context.Context,
	req *connect.Request[gastownv1.UnslingRequest],
) (*connect.Response[gastownv1.UnslingResponse], error) {
	beadID := req.Msg.BeadId
	if beadID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("bead_id is required"))
	}

	result, err := sling.Unsling(sling.UnslingOptions{
		BeadID:   beadID,
		Force:    req.Msg.Force,
		TownRoot: s.townRoot,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal,
			fmt.Errorf("unsling failed: %w", err))
	}

	return connect.NewResponse(&gastownv1.UnslingResponse{
		BeadId:        result.BeadID,
		PreviousAgent: result.PreviousAgent,
		WasIncomplete: result.WasIncomplete,
	}), nil
}

func (s *SlingServer) GetWorkload(
	ctx context.Context,
	req *connect.Request[gastownv1.GetWorkloadRequest],
) (*connect.Response[gastownv1.GetWorkloadResponse], error) {
	agent := req.Msg.Agent
	if agent == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("agent is required"))
	}

	// Query beads with status=hooked and assignee=agent
	client := beads.New(beads.GetTownBeadsPath(s.townRoot))
	issues, err := client.List(beads.ListOptions{
		Status:   "hooked",
		Assignee: agent,
		Priority: -1,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("listing hooked beads: %w", err))
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
