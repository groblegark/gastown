// Package statusreporter syncs K8s pod status back to beads via BD Daemon.
// This gives beads visibility into pod health, allowing existing Gas Town
// health monitoring (Witness, Deacon) to incorporate K8s state.
package statusreporter

import (
	"context"
	"fmt"
	"log/slog"
)

// PodStatus represents the K8s pod state to report back to beads.
type PodStatus struct {
	PodName   string
	Namespace string
	Phase     string // Pending, Running, Succeeded, Failed, Unknown
	Ready     bool
	Message   string
}

// Reporter syncs pod status back to beads.
type Reporter interface {
	// ReportPodStatus sends a single pod's status to beads.
	ReportPodStatus(ctx context.Context, agentName string, status PodStatus) error

	// SyncAll reconciles all agent pod statuses with beads.
	SyncAll(ctx context.Context) error
}

// StubReporter is a no-op implementation for scaffolding.
// TODO(gt-naa65p.7): Replace with real BD Daemon RPC implementation.
type StubReporter struct {
	logger *slog.Logger
}

// NewStubReporter creates a reporter that logs but takes no action.
func NewStubReporter(logger *slog.Logger) *StubReporter {
	return &StubReporter{logger: logger}
}

// ReportPodStatus logs the status but does not send to beads.
func (r *StubReporter) ReportPodStatus(_ context.Context, agentName string, status PodStatus) error {
	r.logger.Debug("stub: would report pod status",
		"agent", agentName, "pod", status.PodName, "phase", status.Phase, "ready", status.Ready)
	return nil
}

// SyncAll is a no-op in the stub.
func (r *StubReporter) SyncAll(_ context.Context) error {
	r.logger.Debug("stub: would sync all pod statuses to beads")
	return fmt.Errorf("stub reporter: SyncAll not implemented")
}
