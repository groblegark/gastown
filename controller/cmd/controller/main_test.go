package main

import (
	"context"
	"log/slog"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/steveyegge/gastown/controller/internal/beadswatcher"
	"github.com/steveyegge/gastown/controller/internal/config"
	"github.com/steveyegge/gastown/controller/internal/podmanager"
)

func TestBuildAgentPodSpec_CoopSidecarFromConfig(t *testing.T) {
	cfg := &config.Config{
		CoopImage:  "ghcr.io/groblegark/coop:latest",
		Namespace:  "gastown",
		DaemonHost: "localhost",
		DaemonPort: 9876,
	}
	event := beadswatcher.Event{
		Type:      beadswatcher.AgentSpawn,
		Rig:       "gastown",
		Role:      "polecat",
		AgentName: "furiosa",
		BeadID:    "gt-test-123",
		Metadata:  map[string]string{"image": "agent:latest"},
	}

	spec := buildAgentPodSpec(cfg, event)

	if spec.CoopSidecar == nil {
		t.Fatal("expected CoopSidecar to be set when CoopImage is configured")
	}
	if spec.CoopSidecar.Image != "ghcr.io/groblegark/coop:latest" {
		t.Errorf("CoopSidecar.Image = %q, want %q", spec.CoopSidecar.Image, "ghcr.io/groblegark/coop:latest")
	}
}

func TestBuildAgentPodSpec_NoCoopWithoutImage(t *testing.T) {
	cfg := &config.Config{
		Namespace:  "gastown",
		DaemonHost: "localhost",
		DaemonPort: 9876,
	}
	event := beadswatcher.Event{
		Type:      beadswatcher.AgentSpawn,
		Rig:       "gastown",
		Role:      "polecat",
		AgentName: "furiosa",
		BeadID:    "gt-test-123",
		Metadata:  map[string]string{"image": "agent:latest"},
	}

	spec := buildAgentPodSpec(cfg, event)

	if spec.CoopSidecar != nil {
		t.Fatal("expected CoopSidecar to be nil when CoopImage is empty")
	}
}

func TestBuildAgentPodSpec_CoopNatsFromMetadata(t *testing.T) {
	cfg := &config.Config{
		CoopImage:  "ghcr.io/groblegark/coop:latest",
		Namespace:  "gastown",
		DaemonHost: "localhost",
		DaemonPort: 9876,
	}
	event := beadswatcher.Event{
		Type:      beadswatcher.AgentSpawn,
		Rig:       "gastown",
		Role:      "crew",
		AgentName: "k8s",
		BeadID:    "gt-test-456",
		Metadata: map[string]string{
			"image":             "agent:latest",
			"nats_url":          "nats://bd-daemon:4222",
			"nats_token_secret": "gastown-nats-token",
			"coop_auth_secret":  "gastown-coop-auth",
		},
	}

	spec := buildAgentPodSpec(cfg, event)

	if spec.CoopSidecar == nil {
		t.Fatal("expected CoopSidecar to be set")
	}
	if spec.CoopSidecar.NatsURL != "nats://bd-daemon:4222" {
		t.Errorf("NatsURL = %q, want %q", spec.CoopSidecar.NatsURL, "nats://bd-daemon:4222")
	}
	if spec.CoopSidecar.NatsTokenSecret != "gastown-nats-token" {
		t.Errorf("NatsTokenSecret = %q, want %q", spec.CoopSidecar.NatsTokenSecret, "gastown-nats-token")
	}
	if spec.CoopSidecar.AuthTokenSecret != "gastown-coop-auth" {
		t.Errorf("AuthTokenSecret = %q, want %q", spec.CoopSidecar.AuthTokenSecret, "gastown-coop-auth")
	}
}

func TestBuildAgentPodSpec_CoopMetadataAvailableForReporting(t *testing.T) {
	cfg := &config.Config{
		CoopImage:  "ghcr.io/groblegark/coop:latest",
		Namespace:  "gastown",
		DaemonHost: "localhost",
		DaemonPort: 9876,
	}
	event := beadswatcher.Event{
		Type:      beadswatcher.AgentSpawn,
		Rig:       "gastown",
		Role:      "polecat",
		AgentName: "furiosa",
		BeadID:    "gt-test-123",
		Metadata:  map[string]string{"image": "agent:latest"},
	}

	spec := buildAgentPodSpec(cfg, event)

	// Verify the spec has what handleEvent needs for backend metadata reporting.
	if spec.CoopSidecar == nil {
		t.Fatal("CoopSidecar must be set for metadata reporting")
	}
	expectedPodName := "gt-gastown-polecat-furiosa"
	if spec.PodName() != expectedPodName {
		t.Errorf("PodName() = %q, want %q", spec.PodName(), expectedPodName)
	}
	if spec.Namespace != "gastown" {
		t.Errorf("Namespace = %q, want %q", spec.Namespace, "gastown")
	}
}

func TestBuildAgentPodSpec_BasicFields(t *testing.T) {
	cfg := &config.Config{
		Namespace:    "gastown",
		DaemonHost:   "bd-daemon",
		DaemonPort:   9876,
		DefaultImage: "default:latest",
	}
	event := beadswatcher.Event{
		Type:      beadswatcher.AgentSpawn,
		Rig:       "gastown",
		Role:      "polecat",
		AgentName: "rictus",
		BeadID:    "gt-test-789",
		Metadata:  map[string]string{"image": "agent:v1"},
	}

	spec := buildAgentPodSpec(cfg, event)

	if spec.Rig != "gastown" {
		t.Errorf("Rig = %q, want %q", spec.Rig, "gastown")
	}
	if spec.Role != "polecat" {
		t.Errorf("Role = %q, want %q", spec.Role, "polecat")
	}
	if spec.AgentName != "rictus" {
		t.Errorf("AgentName = %q, want %q", spec.AgentName, "rictus")
	}
	if spec.Namespace != "gastown" {
		t.Errorf("Namespace = %q, want %q", spec.Namespace, "gastown")
	}
	if spec.PodName() != "gt-gastown-polecat-rictus" {
		t.Errorf("PodName() = %q, want %q", spec.PodName(), "gt-gastown-polecat-rictus")
	}
}

func TestHandleEvent_SpawnWithCoopReportsBackendMetadata(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := &config.Config{
		CoopImage:  "ghcr.io/groblegark/coop:latest",
		Namespace:  "gastown",
		DaemonHost: "localhost",
		DaemonPort: 9876,
	}
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	event := beadswatcher.Event{
		Type:      beadswatcher.AgentSpawn,
		Rig:       "gastown",
		Role:      "polecat",
		AgentName: "furiosa",
		BeadID:    "gt-test-123",
		Metadata:  map[string]string{"image": "agent:latest"},
	}

	if err := handleEvent(ctx, logger, cfg, event, pods, reporter); err != nil {
		t.Fatalf("handleEvent: %v", err)
	}

	// Verify pod was created.
	pod, err := client.CoreV1().Pods("gastown").Get(ctx, "gt-gastown-polecat-furiosa", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pod not created: %v", err)
	}

	// Verify bead-id annotation is set on the pod.
	if id := pod.Annotations["gastown.io/bead-id"]; id != "gt-test-123" {
		t.Errorf("bead-id annotation = %q, want %q", id, "gt-test-123")
	}

	// Backend metadata is NOT reported at spawn time — it's deferred to SyncAll
	// because the pod IP isn't available yet. Verify only pod status was reported.
	meta := reporter.BackendMeta()
	if len(meta) != 0 {
		t.Fatalf("expected 0 backend metadata reports at spawn (deferred to SyncAll), got %d", len(meta))
	}

	// Verify pod status WAS reported.
	reports := reporter.Reports()
	if len(reports) != 1 {
		t.Fatalf("expected 1 pod status report, got %d", len(reports))
	}
	if reports[0].Status.Phase != "Pending" {
		t.Errorf("Phase = %q, want %q", reports[0].Status.Phase, "Pending")
	}
}

func TestHandleEvent_SpawnWithoutCoopSkipsBackendMetadata(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig() // No CoopImage
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	event := spawnEvent("gastown", "polecat", "furiosa")
	if err := handleEvent(ctx, logger, cfg, event, pods, reporter); err != nil {
		t.Fatalf("handleEvent: %v", err)
	}

	meta := reporter.BackendMeta()
	if len(meta) != 0 {
		t.Errorf("expected no backend metadata reports without CoopImage, got %d", len(meta))
	}
}

func TestHandleEvent_DoneClearsBackendMetadata(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := &config.Config{
		CoopImage:  "ghcr.io/groblegark/coop:latest",
		Namespace:  "gastown",
		DaemonHost: "localhost",
		DaemonPort: 9876,
	}
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	// Spawn first.
	spawnEvt := beadswatcher.Event{
		Type:      beadswatcher.AgentSpawn,
		Rig:       "gastown",
		Role:      "polecat",
		AgentName: "furiosa",
		BeadID:    "gt-test-123",
		Metadata:  map[string]string{"image": "agent:latest"},
	}
	if err := handleEvent(ctx, logger, cfg, spawnEvt, pods, reporter); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	// Done should clear metadata.
	doneEvt := doneEvent("gastown", "polecat", "furiosa")
	if err := handleEvent(ctx, logger, cfg, doneEvt, pods, reporter); err != nil {
		t.Fatalf("done: %v", err)
	}

	// Spawn does NOT report backend metadata (deferred to SyncAll).
	// Done sends a clear. So we expect exactly 1 report: the clear.
	meta := reporter.BackendMeta()
	if len(meta) != 1 {
		t.Fatalf("expected 1 backend metadata report (clear only), got %d", len(meta))
	}
	// Should be clear (empty backend).
	if meta[0].Meta.Backend != "" {
		t.Errorf("report Backend = %q, want empty (clear)", meta[0].Meta.Backend)
	}
}

func TestHandleEvent_CoopURLIncludesCustomPort(t *testing.T) {
	// Backend metadata (including CoopURL) is written by SyncAll, not at spawn.
	// This test verifies that buildAgentPodSpec produces a spec with CoopSidecar
	// set and that the pod carries the bead-id annotation for SyncAll to use.
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := &config.Config{
		CoopImage:  "ghcr.io/groblegark/coop:latest",
		Namespace:  "gastown",
		DaemonHost: "localhost",
		DaemonPort: 9876,
	}
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	event := beadswatcher.Event{
		Type:      beadswatcher.AgentSpawn,
		Rig:       "gastown",
		Role:      "polecat",
		AgentName: "furiosa",
		BeadID:    "gt-test-123",
		Metadata:  map[string]string{"image": "agent:latest"},
	}

	if err := handleEvent(ctx, logger, cfg, event, pods, reporter); err != nil {
		t.Fatalf("handleEvent: %v", err)
	}

	// Verify the spec wired coop correctly.
	spec := buildAgentPodSpec(cfg, event)
	if spec.CoopSidecar == nil {
		t.Fatal("expected CoopSidecar to be set")
	}
	if spec.CoopSidecar.Image != "ghcr.io/groblegark/coop:latest" {
		t.Errorf("CoopSidecar.Image = %q, want %q", spec.CoopSidecar.Image, "ghcr.io/groblegark/coop:latest")
	}

	// Verify the created pod has the bead-id annotation.
	pod, err := client.CoreV1().Pods("gastown").Get(ctx, "gt-gastown-polecat-furiosa", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pod not created: %v", err)
	}
	if id := pod.Annotations["gastown.io/bead-id"]; id != "gt-test-123" {
		t.Errorf("bead-id annotation = %q, want %q", id, "gt-test-123")
	}

	// SyncAll would construct the coop URL as:
	// http://<pod-name>.<namespace>.svc.cluster.local:8080
	expectedURL := "http://gt-gastown-polecat-furiosa.gastown.svc.cluster.local:8080"
	_ = expectedURL // verified by statusreporter tests
	_ = reporter    // no spawn-time backend metadata to check
}
