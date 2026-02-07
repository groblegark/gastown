package statusreporter

import (
	"context"
	"log/slog"
	"testing"
)

func TestStubReporter_ReportPodStatus(t *testing.T) {
	r := NewStubReporter(slog.Default())

	err := r.ReportPodStatus(context.Background(), "furiosa", PodStatus{
		PodName:   "gt-gastown-polecat-furiosa",
		Namespace: "gastown",
		Phase:     "Running",
		Ready:     true,
	})
	if err != nil {
		t.Errorf("ReportPodStatus() error = %v, want nil", err)
	}
}

func TestStubReporter_SyncAll(t *testing.T) {
	r := NewStubReporter(slog.Default())

	err := r.SyncAll(context.Background())
	if err == nil {
		t.Error("SyncAll() should return error for stub")
	}
}
