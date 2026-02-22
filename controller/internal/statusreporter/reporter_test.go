package statusreporter

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

// --- mock BeadUpdater for HTTPReporter tests ---

type mockBeadUpdater struct {
	notesCalls      []mockNotesCall
	stateCalls      []mockStateCall
	notesErr        error
	stateErr        error
}

type mockNotesCall struct {
	beadID string
	notes  string
}

type mockStateCall struct {
	beadID string
	state  string
}

func (m *mockBeadUpdater) UpdateBeadNotes(_ context.Context, beadID, notes string) error {
	m.notesCalls = append(m.notesCalls, mockNotesCall{beadID, notes})
	return m.notesErr
}

func (m *mockBeadUpdater) UpdateAgentState(_ context.Context, beadID, state string) error {
	m.stateCalls = append(m.stateCalls, mockStateCall{beadID, state})
	return m.stateErr
}

// --- PhaseToAgentState tests ---

func TestPhaseToAgentState(t *testing.T) {
	tests := []struct {
		phase string
		want  string
	}{
		{"Pending", "spawning"},
		{"Running", "working"},
		{"Succeeded", "done"},
		{"Failed", "failed"},
		{"Unknown", ""},
		{"", ""},
		{"SomethingElse", ""},
	}

	for _, tt := range tests {
		t.Run(tt.phase, func(t *testing.T) {
			got := PhaseToAgentState(tt.phase)
			if got != tt.want {
				t.Errorf("PhaseToAgentState(%q) = %q, want %q", tt.phase, got, tt.want)
			}
		})
	}
}

// --- isPodReady tests ---

func TestIsPodReady(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want bool
	}{
		{
			name: "ready pod",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodReady, Status: corev1.ConditionTrue},
					},
				},
			},
			want: true,
		},
		{
			name: "not ready pod",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodReady, Status: corev1.ConditionFalse},
					},
				},
			},
			want: false,
		},
		{
			name: "no conditions",
			pod:  &corev1.Pod{},
			want: false,
		},
		{
			name: "other conditions only",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
					},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPodReady(tt.pod)
			if got != tt.want {
				t.Errorf("isPodReady() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- StubReporter tests ---

func TestStubReporter_ReportPodStatus(t *testing.T) {
	r := NewStubReporter(slog.Default())

	err := r.ReportPodStatus(context.Background(), "gt-gastown-polecat-furiosa", PodStatus{
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
	if err != nil {
		t.Errorf("SyncAll() error = %v, want nil", err)
	}
}

func TestStubReporter_Metrics(t *testing.T) {
	r := NewStubReporter(slog.Default())
	m := r.Metrics()
	if m.StatusReportsTotal != 0 || m.StatusReportErrors != 0 {
		t.Errorf("Metrics() should be zero for stub")
	}
}

// --- BdReporter tests ---

func TestBdReporter_ReportPodStatus_SkipsUnknownPhase(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := NewBdReporter(BdConfig{BdBinary: "false"}, client, slog.Default())

	// Unknown phase should be skipped without error.
	err := r.ReportPodStatus(context.Background(), "gt-gastown-polecat-test", PodStatus{
		PodName: "gt-gastown-polecat-test",
		Phase:   "Unknown",
	})
	if err != nil {
		t.Errorf("ReportPodStatus(Unknown) = %v, want nil", err)
	}

	m := r.Metrics()
	if m.StatusReportsTotal != 1 {
		t.Errorf("reports total = %d, want 1", m.StatusReportsTotal)
	}
	if m.StatusReportErrors != 0 {
		t.Errorf("report errors = %d, want 0", m.StatusReportErrors)
	}
}

func TestBdReporter_ReportPodStatus_BdFailure(t *testing.T) {
	client := fake.NewSimpleClientset()
	// Use "false" binary which always exits 1.
	r := NewBdReporter(BdConfig{BdBinary: "false"}, client, slog.Default())

	err := r.ReportPodStatus(context.Background(), "gt-gastown-polecat-test", PodStatus{
		PodName: "gt-gastown-polecat-test",
		Phase:   "Running",
	})
	if err == nil {
		t.Error("ReportPodStatus() should return error when bd fails")
	}

	m := r.Metrics()
	if m.StatusReportErrors != 1 {
		t.Errorf("report errors = %d, want 1", m.StatusReportErrors)
	}
}

func TestBdReporter_ReportPodStatus_Success(t *testing.T) {
	client := fake.NewSimpleClientset()
	// Use "true" binary which always exits 0.
	r := NewBdReporter(BdConfig{BdBinary: "true"}, client, slog.Default())

	err := r.ReportPodStatus(context.Background(), "gt-gastown-polecat-test", PodStatus{
		PodName: "gt-gastown-polecat-test",
		Phase:   "Running",
	})
	if err != nil {
		t.Errorf("ReportPodStatus() = %v, want nil", err)
	}

	m := r.Metrics()
	if m.StatusReportsTotal != 1 {
		t.Errorf("reports total = %d, want 1", m.StatusReportsTotal)
	}
	if m.StatusReportErrors != 0 {
		t.Errorf("report errors = %d, want 0", m.StatusReportErrors)
	}
}

func TestBdReporter_ReportPodStatus_AllPhases(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := NewBdReporter(BdConfig{BdBinary: "true"}, client, slog.Default())

	phases := []struct {
		phase string
		state string
	}{
		{"Pending", "spawning"},
		{"Running", "working"},
		{"Succeeded", "done"},
		{"Failed", "failed"},
	}

	for _, p := range phases {
		err := r.ReportPodStatus(context.Background(), "gt-gastown-polecat-test", PodStatus{
			PodName: "gt-gastown-polecat-test",
			Phase:   p.phase,
		})
		if err != nil {
			t.Errorf("ReportPodStatus(%s) = %v, want nil", p.phase, err)
		}
	}

	m := r.Metrics()
	if m.StatusReportsTotal != 4 {
		t.Errorf("reports total = %d, want 4", m.StatusReportsTotal)
	}
}

func TestBdReporter_DefaultBdBinary(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := NewBdReporter(BdConfig{}, client, slog.Default())
	if r.cfg.BdBinary != "bd" {
		t.Errorf("default BdBinary = %q, want %q", r.cfg.BdBinary, "bd")
	}
}

// --- SyncAll tests ---

func TestBdReporter_SyncAll_NoPods(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := NewBdReporter(BdConfig{BdBinary: "true", Namespace: "gastown"}, client, slog.Default())

	err := r.SyncAll(context.Background())
	if err != nil {
		t.Errorf("SyncAll() = %v, want nil", err)
	}

	m := r.Metrics()
	if m.SyncAllRuns != 1 {
		t.Errorf("sync runs = %d, want 1", m.SyncAllRuns)
	}
}

func TestBdReporter_SyncAll_WithPods(t *testing.T) {
	pods := []runtime.Object{
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "gt-gastown-polecat-furiosa",
				Namespace: "gastown",
				Labels: map[string]string{
					"app.kubernetes.io/name": "gastown",
					"gastown.io/rig":         "gastown",
					"gastown.io/role":        "polecat",
					"gastown.io/agent":       "furiosa",
				},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				Conditions: []corev1.PodCondition{
					{Type: corev1.PodReady, Status: corev1.ConditionTrue},
				},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "gt-gastown-crew-colonization",
				Namespace: "gastown",
				Labels: map[string]string{
					"app.kubernetes.io/name": "gastown",
					"gastown.io/rig":         "gastown",
					"gastown.io/role":        "crew",
					"gastown.io/agent":       "colonization",
				},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
			},
		},
	}

	client := fake.NewSimpleClientset(pods...)
	r := NewBdReporter(BdConfig{BdBinary: "true", Namespace: "gastown"}, client, slog.Default())

	err := r.SyncAll(context.Background())
	if err != nil {
		t.Errorf("SyncAll() = %v, want nil", err)
	}

	m := r.Metrics()
	if m.StatusReportsTotal != 2 {
		t.Errorf("reports total = %d, want 2", m.StatusReportsTotal)
	}
	if m.SyncAllRuns != 1 {
		t.Errorf("sync runs = %d, want 1", m.SyncAllRuns)
	}
}

func TestBdReporter_SyncAll_SkipsMissingLabels(t *testing.T) {
	pods := []runtime.Object{
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "unrelated-pod",
				Namespace: "gastown",
				Labels: map[string]string{
					"app.kubernetes.io/name": "gastown",
					// Missing gastown.io labels.
				},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	}

	client := fake.NewSimpleClientset(pods...)
	r := NewBdReporter(BdConfig{BdBinary: "true", Namespace: "gastown"}, client, slog.Default())

	err := r.SyncAll(context.Background())
	if err != nil {
		t.Errorf("SyncAll() = %v, want nil", err)
	}

	m := r.Metrics()
	if m.StatusReportsTotal != 0 {
		t.Errorf("reports total = %d, want 0 (pod should be skipped)", m.StatusReportsTotal)
	}
}

func TestBdReporter_SyncAll_BdFailure(t *testing.T) {
	pods := []runtime.Object{
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "gt-gastown-polecat-test",
				Namespace: "gastown",
				Labels: map[string]string{
					"app.kubernetes.io/name": "gastown",
					"gastown.io/rig":         "gastown",
					"gastown.io/role":        "polecat",
					"gastown.io/agent":       "test",
				},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	}

	client := fake.NewSimpleClientset(pods...)
	// "false" binary will fail.
	r := NewBdReporter(BdConfig{BdBinary: "false", Namespace: "gastown"}, client, slog.Default())

	err := r.SyncAll(context.Background())
	if err == nil {
		t.Error("SyncAll() should return error when bd fails")
	}

	m := r.Metrics()
	if m.SyncAllErrors != 1 {
		t.Errorf("sync errors = %d, want 1", m.SyncAllErrors)
	}
}

// --- BackendMetadata tests ---

func TestBdReporter_ReportBackendMetadata_Success(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := NewBdReporter(BdConfig{BdBinary: "true"}, client, slog.Default())

	err := r.ReportBackendMetadata(context.Background(), "gt-gastown-polecat-test", BackendMetadata{
		PodName:   "gt-gastown-polecat-test",
		Namespace: "gastown",
		Backend:   "coop",
		CoopURL:   "http://gt-gastown-polecat-test.gastown.svc.cluster.local:8080",
	})
	if err != nil {
		t.Errorf("ReportBackendMetadata() = %v, want nil", err)
	}
}

func TestBdReporter_ReportBackendMetadata_BdFailure(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := NewBdReporter(BdConfig{BdBinary: "false"}, client, slog.Default())

	err := r.ReportBackendMetadata(context.Background(), "gt-gastown-polecat-test", BackendMetadata{
		Backend: "coop",
		CoopURL: "http://example.com:8080",
	})
	if err == nil {
		t.Error("ReportBackendMetadata() should return error when bd fails")
	}
	m := r.Metrics()
	if m.StatusReportErrors != 1 {
		t.Errorf("report errors = %d, want 1", m.StatusReportErrors)
	}
}

func TestBdReporter_ReportBackendMetadata_EmptyMetadata(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := NewBdReporter(BdConfig{BdBinary: "false"}, client, slog.Default())

	// Empty metadata should be a no-op (no bd call).
	err := r.ReportBackendMetadata(context.Background(), "gt-gastown-polecat-test", BackendMetadata{})
	if err != nil {
		t.Errorf("ReportBackendMetadata(empty) = %v, want nil", err)
	}
}

func TestStubReporter_ReportBackendMetadata(t *testing.T) {
	r := NewStubReporter(slog.Default())
	err := r.ReportBackendMetadata(context.Background(), "gt-test", BackendMetadata{
		Backend: "coop",
	})
	if err != nil {
		t.Errorf("stub ReportBackendMetadata() = %v, want nil", err)
	}
}

// --- agentBeadID tests ---

func TestAgentBeadID_AnnotationTakesPrecedence(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"gastown.io/bead-id": "custom-bead-id",
			},
			Labels: map[string]string{
				"gastown.io/rig":   "gastown",
				"gastown.io/role":  "polecat",
				"gastown.io/agent": "furiosa",
			},
		},
	}
	got := agentBeadID(pod)
	if got != "custom-bead-id" {
		t.Errorf("agentBeadID() = %q, want %q", got, "custom-bead-id")
	}
}

func TestAgentBeadID_SingletonRoles(t *testing.T) {
	tests := []struct {
		role string
		want string
	}{
		{"mayor", "gt-mayor"},
		{"deacon", "gt-deacon"},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"gastown.io/rig":   "town",
						"gastown.io/role":  tt.role,
						"gastown.io/agent": "hq",
					},
				},
			}
			got := agentBeadID(pod)
			if got != tt.want {
				t.Errorf("agentBeadID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAgentBeadID_RegularAgent(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"gastown.io/rig":   "gastown",
				"gastown.io/role":  "polecat",
				"gastown.io/agent": "furiosa",
			},
		},
	}
	got := agentBeadID(pod)
	if got != "gt-gastown-polecat-furiosa" {
		t.Errorf("agentBeadID() = %q, want %q", got, "gt-gastown-polecat-furiosa")
	}
}

func TestAgentBeadID_CrewAgent(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"gastown.io/rig":   "gastown",
				"gastown.io/role":  "crew",
				"gastown.io/agent": "colonization",
			},
		},
	}
	got := agentBeadID(pod)
	if got != "gt-gastown-crew-colonization" {
		t.Errorf("agentBeadID() = %q, want %q", got, "gt-gastown-crew-colonization")
	}
}

func TestAgentBeadID_NoLabelsNoAnnotation(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{},
	}
	got := agentBeadID(pod)
	if got != "gt---" {
		t.Errorf("agentBeadID() = %q, want %q", got, "gt---")
	}
}

// --- Context cancellation ---

func TestBdReporter_ReportPodStatus_CancelledContext(t *testing.T) {
	client := fake.NewSimpleClientset()
	// "sleep" binary to test cancellation - use "true" since we're just checking the path.
	r := NewBdReporter(BdConfig{BdBinary: "true"}, client, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	// Should still work since "true" exits immediately before context check.
	err := r.ReportPodStatus(ctx, "gt-gastown-polecat-test", PodStatus{
		PodName: "gt-gastown-polecat-test",
		Phase:   "Running",
	})
	// May or may not error depending on timing. Just ensure no panic.
	_ = err
}

// --- HTTPReporter.ReportPodStatus tests ---

func TestHTTPReporter_ReportPodStatus_UpdatesState(t *testing.T) {
	mock := &mockBeadUpdater{}
	client := fake.NewSimpleClientset()
	r := NewHTTPReporter(mock, client, "gastown", slog.Default())

	err := r.ReportPodStatus(context.Background(), "gt-gastown-polecat-furiosa", PodStatus{
		PodName: "gt-gastown-polecat-furiosa",
		Phase:   "Running",
		Ready:   true,
	})
	if err != nil {
		t.Errorf("ReportPodStatus() error = %v, want nil", err)
	}

	if len(mock.stateCalls) != 1 {
		t.Fatalf("expected 1 state call, got %d", len(mock.stateCalls))
	}
	if mock.stateCalls[0].beadID != "gt-gastown-polecat-furiosa" {
		t.Errorf("beadID = %q, want %q", mock.stateCalls[0].beadID, "gt-gastown-polecat-furiosa")
	}
	if mock.stateCalls[0].state != "working" {
		t.Errorf("state = %q, want %q", mock.stateCalls[0].state, "working")
	}

	m := r.Metrics()
	if m.StatusReportsTotal != 1 {
		t.Errorf("reports total = %d, want 1", m.StatusReportsTotal)
	}
	if m.StatusReportErrors != 0 {
		t.Errorf("report errors = %d, want 0", m.StatusReportErrors)
	}
}

func TestHTTPReporter_ReportPodStatus_AllPhases(t *testing.T) {
	phases := []struct {
		phase string
		state string
	}{
		{"Pending", "spawning"},
		{"Running", "working"},
		{"Succeeded", "done"},
		{"Failed", "failed"},
	}

	for _, p := range phases {
		t.Run(p.phase, func(t *testing.T) {
			mock := &mockBeadUpdater{}
			client := fake.NewSimpleClientset()
			r := NewHTTPReporter(mock, client, "gastown", slog.Default())

			err := r.ReportPodStatus(context.Background(), "gt-test", PodStatus{
				PodName: "gt-test",
				Phase:   p.phase,
			})
			if err != nil {
				t.Errorf("ReportPodStatus(%s) error = %v", p.phase, err)
			}
			if len(mock.stateCalls) != 1 {
				t.Fatalf("expected 1 state call, got %d", len(mock.stateCalls))
			}
			if mock.stateCalls[0].state != p.state {
				t.Errorf("state = %q, want %q", mock.stateCalls[0].state, p.state)
			}
		})
	}
}

func TestHTTPReporter_ReportPodStatus_SkipsUnknownPhase(t *testing.T) {
	mock := &mockBeadUpdater{}
	client := fake.NewSimpleClientset()
	r := NewHTTPReporter(mock, client, "gastown", slog.Default())

	err := r.ReportPodStatus(context.Background(), "gt-test", PodStatus{
		PodName: "gt-test",
		Phase:   "Unknown",
	})
	if err != nil {
		t.Errorf("ReportPodStatus(Unknown) error = %v, want nil", err)
	}

	if len(mock.stateCalls) != 0 {
		t.Errorf("expected 0 state calls for unknown phase, got %d", len(mock.stateCalls))
	}

	m := r.Metrics()
	if m.StatusReportsTotal != 1 {
		t.Errorf("reports total = %d, want 1", m.StatusReportsTotal)
	}
}

func TestHTTPReporter_ReportPodStatus_DaemonError(t *testing.T) {
	mock := &mockBeadUpdater{stateErr: fmt.Errorf("daemon unavailable")}
	client := fake.NewSimpleClientset()
	r := NewHTTPReporter(mock, client, "gastown", slog.Default())

	err := r.ReportPodStatus(context.Background(), "gt-test", PodStatus{
		PodName: "gt-test",
		Phase:   "Running",
	})
	if err == nil {
		t.Error("ReportPodStatus() should return error when daemon fails")
	}

	m := r.Metrics()
	if m.StatusReportErrors != 1 {
		t.Errorf("report errors = %d, want 1", m.StatusReportErrors)
	}
}

func TestHTTPReporter_SyncAll_UpdatesAgentStates(t *testing.T) {
	pods := []runtime.Object{
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "gt-gastown-polecat-furiosa",
				Namespace: "gastown",
				Labels: map[string]string{
					"app.kubernetes.io/name": "gastown",
					"gastown.io/rig":         "gastown",
					"gastown.io/role":        "polecat",
					"gastown.io/agent":       "furiosa",
				},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				Conditions: []corev1.PodCondition{
					{Type: corev1.PodReady, Status: corev1.ConditionTrue},
				},
			},
		},
	}

	mock := &mockBeadUpdater{}
	client := fake.NewSimpleClientset(pods...)
	r := NewHTTPReporter(mock, client, "gastown", slog.Default())

	err := r.SyncAll(context.Background())
	if err != nil {
		t.Errorf("SyncAll() error = %v, want nil", err)
	}

	if len(mock.stateCalls) != 1 {
		t.Fatalf("expected 1 state call, got %d", len(mock.stateCalls))
	}
	if mock.stateCalls[0].beadID != "gt-gastown-polecat-furiosa" {
		t.Errorf("beadID = %q, want %q", mock.stateCalls[0].beadID, "gt-gastown-polecat-furiosa")
	}
	if mock.stateCalls[0].state != "working" {
		t.Errorf("state = %q, want %q", mock.stateCalls[0].state, "working")
	}
}
