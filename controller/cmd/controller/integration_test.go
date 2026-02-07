//go:build integration

// Integration tests for the Gas Town K8s controller.
//
// These tests verify the controller's ability to create and delete pods on a
// real K8s cluster based on beads lifecycle events. They require:
//   - A valid kubeconfig with access to the e2e cluster
//   - The gastown-test namespace to exist
//   - Network access to the K8s API server
//
// Run with: go test -tags=integration -timeout=5m ./cmd/controller/...
//
// CRITICAL: All tests use namespace "gastown-test". NEVER use "gastown" (production).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/steveyegge/gastown/controller/internal/beadswatcher"
	"github.com/steveyegge/gastown/controller/internal/config"
	"github.com/steveyegge/gastown/controller/internal/podmanager"
	"github.com/steveyegge/gastown/controller/internal/statusreporter"
)

const (
	// testNamespace is the ONLY namespace used for integration tests.
	// NEVER change this to "gastown" — that is the production namespace.
	testNamespace = "gastown-test"

	// testImage is a minimal image that runs and exits cleanly.
	// pause:3.9 is widely available and does not require auth.
	testImage = "registry.k8s.io/pause:3.9"

	// testRig is the rig name used in test agent specs.
	testRig = "gastown"

	// Timeouts for waiting on K8s operations.
	podCreateTimeout = 60 * time.Second
	podDeleteTimeout = 60 * time.Second
	pollInterval     = 2 * time.Second
)

// testEnv holds shared test infrastructure.
type testEnv struct {
	client  kubernetes.Interface
	pods    podmanager.Manager
	status  statusreporter.Reporter
	cfg     *config.Config
	logger  *slog.Logger
	t       *testing.T
}

// setupTestEnv creates a test environment with a real K8s client.
func setupTestEnv(t *testing.T) *testEnv {
	t.Helper()

	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home, _ := os.UserHomeDir()
		kubeconfig = home + "/.kube/config"
	}

	restCfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Skipf("skipping integration test: cannot build kubeconfig: %v", err)
	}

	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		t.Skipf("skipping integration test: cannot create K8s client: %v", err)
	}

	// Verify we can reach the cluster and the test namespace exists.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = client.CoreV1().Namespaces().Get(ctx, testNamespace, metav1.GetOptions{})
	if err != nil {
		t.Skipf("skipping integration test: namespace %q not accessible: %v", testNamespace, err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pods := podmanager.New(client, logger)
	status := statusreporter.NewStubReporter(logger)

	cfg := &config.Config{
		Namespace:    testNamespace,
		DefaultImage: testImage,
		DaemonHost:   "localhost",
		DaemonPort:   9876,
	}

	return &testEnv{
		client: client,
		pods:   pods,
		status: status,
		cfg:    cfg,
		logger: logger,
		t:      t,
	}
}

// uniqueAgentName generates a unique agent name for a test to avoid conflicts.
func uniqueAgentName(t *testing.T, prefix string) string {
	t.Helper()
	// Use a short suffix from the current time to ensure uniqueness.
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%100000)
	return fmt.Sprintf("%s-%s", prefix, suffix)
}

// cleanupPod deletes a test pod and waits for it to be gone.
func (env *testEnv) cleanupPod(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), podDeleteTimeout)
	defer cancel()

	err := env.client.CoreV1().Pods(testNamespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		env.t.Logf("cleanup: failed to delete pod %s: %v", name, err)
		return
	}

	// Wait for pod to be gone.
	env.waitForPodGone(ctx, name)
}

// waitForPod polls until a pod exists and matches the given phase, or times out.
func (env *testEnv) waitForPod(ctx context.Context, name string) *corev1.Pod {
	env.t.Helper()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			env.t.Fatalf("timed out waiting for pod %s to exist", name)
			return nil
		case <-ticker.C:
			pod, err := env.client.CoreV1().Pods(testNamespace).Get(ctx, name, metav1.GetOptions{})
			if err == nil {
				return pod
			}
			if !errors.IsNotFound(err) {
				env.t.Logf("waiting for pod %s: %v", name, err)
			}
		}
	}
}

// waitForPodReady polls until a pod exists and is in Running phase.
func (env *testEnv) waitForPodReady(ctx context.Context, name string) *corev1.Pod {
	env.t.Helper()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			env.t.Fatalf("timed out waiting for pod %s to be Running", name)
			return nil
		case <-ticker.C:
			pod, err := env.client.CoreV1().Pods(testNamespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				continue
			}
			if pod.Status.Phase == corev1.PodRunning {
				return pod
			}
			env.t.Logf("pod %s phase: %s", name, pod.Status.Phase)
		}
	}
}

// waitForPodGone polls until a pod no longer exists.
func (env *testEnv) waitForPodGone(ctx context.Context, name string) {
	env.t.Helper()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			env.t.Fatalf("timed out waiting for pod %s to be deleted", name)
			return
		case <-ticker.C:
			_, err := env.client.CoreV1().Pods(testNamespace).Get(ctx, name, metav1.GetOptions{})
			if errors.IsNotFound(err) {
				return
			}
		}
	}
}

// makeSpawnEvent creates an AgentSpawn event for the given agent.
func makeSpawnEvent(rig, role, name string) beadswatcher.Event {
	return beadswatcher.Event{
		Type:      beadswatcher.AgentSpawn,
		Rig:       rig,
		Role:      role,
		AgentName: name,
		BeadID:    fmt.Sprintf("gt-%s-%s-%s", rig, role, name),
		Metadata: map[string]string{
			"namespace": testNamespace,
			"image":     testImage,
		},
	}
}

// makeDoneEvent creates an AgentDone event for the given agent.
func makeDoneEvent(rig, role, name string) beadswatcher.Event {
	return beadswatcher.Event{
		Type:      beadswatcher.AgentDone,
		Rig:       rig,
		Role:      role,
		AgentName: name,
		BeadID:    fmt.Sprintf("gt-%s-%s-%s", rig, role, name),
		Metadata: map[string]string{
			"namespace": testNamespace,
		},
	}
}

// makeStuckEvent creates an AgentStuck event for the given agent.
func makeStuckEvent(rig, role, name string) beadswatcher.Event {
	return beadswatcher.Event{
		Type:      beadswatcher.AgentStuck,
		Rig:       rig,
		Role:      role,
		AgentName: name,
		BeadID:    fmt.Sprintf("gt-%s-%s-%s", rig, role, name),
		Metadata: map[string]string{
			"namespace": testNamespace,
			"image":     testImage,
		},
	}
}

// ---------- Test: Pod creation from bead state ----------

func TestIntegration_ControllerCreatesPodFromBeadState(t *testing.T) {
	env := setupTestEnv(t)
	agentName := uniqueAgentName(t, "spawn")
	podName := fmt.Sprintf("gt-%s-polecat-%s", testRig, agentName)

	// Ensure cleanup regardless of test outcome.
	defer env.cleanupPod(podName)

	// Send a spawn event through handleEvent.
	event := makeSpawnEvent(testRig, "polecat", agentName)
	ctx := context.Background()

	err := handleEvent(ctx, env.logger, env.cfg, event, env.pods, env.status)
	if err != nil {
		t.Fatalf("handleEvent(AgentSpawn) error: %v", err)
	}

	// Verify: pod should exist in K8s within the timeout.
	waitCtx, cancel := context.WithTimeout(ctx, podCreateTimeout)
	defer cancel()

	pod := env.waitForPod(waitCtx, podName)
	if pod == nil {
		t.Fatalf("pod %s was not created", podName)
	}

	// Verify pod metadata.
	if pod.Namespace != testNamespace {
		t.Errorf("pod namespace = %q, want %q", pod.Namespace, testNamespace)
	}
	if pod.Labels[podmanager.LabelApp] != podmanager.LabelAppValue {
		t.Errorf("label %s = %q, want %q", podmanager.LabelApp, pod.Labels[podmanager.LabelApp], podmanager.LabelAppValue)
	}
	if pod.Labels[podmanager.LabelRig] != testRig {
		t.Errorf("label %s = %q, want %q", podmanager.LabelRig, pod.Labels[podmanager.LabelRig], testRig)
	}
	if pod.Labels[podmanager.LabelRole] != "polecat" {
		t.Errorf("label %s = %q, want %q", podmanager.LabelRole, pod.Labels[podmanager.LabelRole], "polecat")
	}
	if pod.Labels[podmanager.LabelAgent] != agentName {
		t.Errorf("label %s = %q, want %q", podmanager.LabelAgent, pod.Labels[podmanager.LabelAgent], agentName)
	}

	// Verify pod spec.
	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(pod.Spec.Containers))
	}
	container := pod.Spec.Containers[0]
	if container.Image != testImage {
		t.Errorf("container image = %q, want %q", container.Image, testImage)
	}
	if pod.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restart policy = %q, want Never (polecat)", pod.Spec.RestartPolicy)
	}

	t.Logf("pod %s created successfully in namespace %s", podName, testNamespace)
}

// ---------- Test: Pod reaches Running state ----------

func TestIntegration_PodReachesRunningState(t *testing.T) {
	env := setupTestEnv(t)
	agentName := uniqueAgentName(t, "ready")
	podName := fmt.Sprintf("gt-%s-polecat-%s", testRig, agentName)

	defer env.cleanupPod(podName)

	event := makeSpawnEvent(testRig, "polecat", agentName)
	ctx := context.Background()

	if err := handleEvent(ctx, env.logger, env.cfg, event, env.pods, env.status); err != nil {
		t.Fatalf("handleEvent(AgentSpawn) error: %v", err)
	}

	// Wait for pod to reach Running state.
	waitCtx, cancel := context.WithTimeout(ctx, podCreateTimeout)
	defer cancel()

	pod := env.waitForPodReady(waitCtx, podName)
	if pod.Status.Phase != corev1.PodRunning {
		t.Errorf("pod phase = %q, want Running", pod.Status.Phase)
	}

	t.Logf("pod %s reached Running state", podName)
}

// ---------- Test: Pod deletion from bead state ----------

func TestIntegration_ControllerDeletesPodFromBeadState(t *testing.T) {
	env := setupTestEnv(t)
	agentName := uniqueAgentName(t, "del")
	podName := fmt.Sprintf("gt-%s-polecat-%s", testRig, agentName)

	defer env.cleanupPod(podName) // safety net

	ctx := context.Background()

	// Step 1: Create the pod via spawn event.
	spawnEvent := makeSpawnEvent(testRig, "polecat", agentName)
	if err := handleEvent(ctx, env.logger, env.cfg, spawnEvent, env.pods, env.status); err != nil {
		t.Fatalf("handleEvent(AgentSpawn) error: %v", err)
	}

	// Wait for pod to exist.
	waitCtx, cancel := context.WithTimeout(ctx, podCreateTimeout)
	defer cancel()
	env.waitForPod(waitCtx, podName)

	// Step 2: Delete the pod via done event.
	doneEvent := makeDoneEvent(testRig, "polecat", agentName)
	if err := handleEvent(ctx, env.logger, env.cfg, doneEvent, env.pods, env.status); err != nil {
		t.Fatalf("handleEvent(AgentDone) error: %v", err)
	}

	// Step 3: Verify pod is gone.
	deleteCtx, deleteCancel := context.WithTimeout(ctx, podDeleteTimeout)
	defer deleteCancel()
	env.waitForPodGone(deleteCtx, podName)

	// Confirm the pod is truly gone.
	_, err := env.client.CoreV1().Pods(testNamespace).Get(ctx, podName, metav1.GetOptions{})
	if !errors.IsNotFound(err) {
		t.Errorf("expected NotFound after delete, got: %v", err)
	}

	t.Logf("pod %s deleted successfully after AgentDone event", podName)
}

// ---------- Test: Pod info registered correctly ----------

func TestIntegration_PodInfoRegistered(t *testing.T) {
	env := setupTestEnv(t)
	agentName := uniqueAgentName(t, "info")
	podName := fmt.Sprintf("gt-%s-polecat-%s", testRig, agentName)

	defer env.cleanupPod(podName)

	event := makeSpawnEvent(testRig, "polecat", agentName)
	ctx := context.Background()

	if err := handleEvent(ctx, env.logger, env.cfg, event, env.pods, env.status); err != nil {
		t.Fatalf("handleEvent(AgentSpawn) error: %v", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, podCreateTimeout)
	defer cancel()
	pod := env.waitForPodReady(waitCtx, podName)

	// Verify the pod info that would be reported to beads.
	if pod.Status.PodIP == "" {
		t.Error("pod IP should be assigned when Running")
	}
	if pod.Spec.NodeName == "" {
		t.Error("pod should be scheduled to a node")
	}

	// Verify security context is correct.
	psc := pod.Spec.SecurityContext
	if psc == nil {
		t.Fatal("pod security context is nil")
	}
	if psc.RunAsUser == nil || *psc.RunAsUser != podmanager.AgentUID {
		t.Errorf("RunAsUser = %v, want %d", psc.RunAsUser, podmanager.AgentUID)
	}
	if psc.RunAsNonRoot == nil || !*psc.RunAsNonRoot {
		t.Error("RunAsNonRoot should be true")
	}

	// Verify env vars are set.
	container := pod.Spec.Containers[0]
	envMap := make(map[string]string)
	for _, e := range container.Env {
		if e.ValueFrom == nil {
			envMap[e.Name] = e.Value
		}
	}
	if envMap["GT_ROLE"] != "polecat" {
		t.Errorf("GT_ROLE = %q, want %q", envMap["GT_ROLE"], "polecat")
	}
	if envMap["GT_RIG"] != testRig {
		t.Errorf("GT_RIG = %q, want %q", envMap["GT_RIG"], testRig)
	}
	if envMap["GT_AGENT"] != agentName {
		t.Errorf("GT_AGENT = %q, want %q", envMap["GT_AGENT"], agentName)
	}

	t.Logf("pod %s has IP=%s, Node=%s", podName, pod.Status.PodIP, pod.Spec.NodeName)
}

// ---------- Test: Crash recovery recreates pod ----------

func TestIntegration_CrashRecoveryRecreatePod(t *testing.T) {
	env := setupTestEnv(t)
	agentName := uniqueAgentName(t, "crash")
	podName := fmt.Sprintf("gt-%s-polecat-%s", testRig, agentName)

	defer env.cleanupPod(podName)

	ctx := context.Background()

	// Step 1: Create pod via spawn event.
	spawnEvent := makeSpawnEvent(testRig, "polecat", agentName)
	if err := handleEvent(ctx, env.logger, env.cfg, spawnEvent, env.pods, env.status); err != nil {
		t.Fatalf("handleEvent(AgentSpawn) error: %v", err)
	}

	// Wait for pod to be running.
	waitCtx, cancel := context.WithTimeout(ctx, podCreateTimeout)
	defer cancel()
	pod := env.waitForPodReady(waitCtx, podName)
	originalUID := pod.UID
	t.Logf("original pod UID: %s", originalUID)

	// Step 2: Simulate crash by deleting the pod directly (kubectl delete pod).
	err := env.client.CoreV1().Pods(testNamespace).Delete(ctx, podName, metav1.DeleteOptions{})
	if err != nil {
		t.Fatalf("failed to simulate pod crash (delete): %v", err)
	}

	// Wait for the pod to be gone.
	deleteCtx, deleteCancel := context.WithTimeout(ctx, podDeleteTimeout)
	defer deleteCancel()
	env.waitForPodGone(deleteCtx, podName)
	t.Logf("pod %s deleted (crash simulated)", podName)

	// Step 3: Controller detects missing pod and recreates via AgentStuck event.
	stuckEvent := makeStuckEvent(testRig, "polecat", agentName)
	if err := handleEvent(ctx, env.logger, env.cfg, stuckEvent, env.pods, env.status); err != nil {
		t.Fatalf("handleEvent(AgentStuck) error: %v", err)
	}

	// Step 4: Verify new pod is created.
	recreateCtx, recreateCancel := context.WithTimeout(ctx, podCreateTimeout)
	defer recreateCancel()
	newPod := env.waitForPod(recreateCtx, podName)

	if newPod.UID == originalUID {
		t.Error("new pod has same UID as original — should be a new pod")
	}

	t.Logf("new pod UID: %s (original: %s) — crash recovery successful", newPod.UID, originalUID)
}

// ---------- Test: Concurrent pod management (scale test) ----------

func TestIntegration_ConcurrentPodManagement(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	const numAgents = 5
	agentNames := make([]string, numAgents)
	podNames := make([]string, numAgents)

	for i := 0; i < numAgents; i++ {
		agentNames[i] = uniqueAgentName(t, fmt.Sprintf("scale%d", i))
		podNames[i] = fmt.Sprintf("gt-%s-polecat-%s", testRig, agentNames[i])
	}

	// Ensure cleanup of all pods.
	defer func() {
		for _, name := range podNames {
			env.cleanupPod(name)
		}
	}()

	// Step 1: Create all 5 agent pods simultaneously.
	t.Log("creating 5 agent pods simultaneously...")
	var createWg sync.WaitGroup
	createErrors := make([]error, numAgents)

	for i := 0; i < numAgents; i++ {
		createWg.Add(1)
		go func(idx int) {
			defer createWg.Done()
			event := makeSpawnEvent(testRig, "polecat", agentNames[idx])
			createErrors[idx] = handleEvent(ctx, env.logger, env.cfg, event, env.pods, env.status)
		}(i)
	}
	createWg.Wait()

	// Check for creation errors.
	for i, err := range createErrors {
		if err != nil {
			t.Errorf("failed to create agent %d (%s): %v", i, agentNames[i], err)
		}
	}

	// Step 2: Verify all pods exist.
	waitCtx, waitCancel := context.WithTimeout(ctx, podCreateTimeout)
	defer waitCancel()

	for i, name := range podNames {
		pod := env.waitForPod(waitCtx, name)
		if pod == nil {
			t.Errorf("pod %d (%s) was not created", i, name)
		}
	}
	t.Logf("all %d pods created successfully", numAgents)

	// Step 3: Verify we can list all pods by label.
	pods, err := env.pods.ListAgentPods(ctx, testNamespace, map[string]string{
		podmanager.LabelApp:  podmanager.LabelAppValue,
		podmanager.LabelRig:  testRig,
		podmanager.LabelRole: "polecat",
	})
	if err != nil {
		t.Fatalf("ListAgentPods error: %v", err)
	}

	// Filter to our test pods (other tests may have pods too).
	testPodCount := 0
	for _, pod := range pods {
		for _, expected := range podNames {
			if pod.Name == expected {
				testPodCount++
				break
			}
		}
	}
	if testPodCount != numAgents {
		t.Errorf("found %d of %d test pods via label query", testPodCount, numAgents)
	}

	// Step 4: Delete all 5 pods simultaneously (set all to done).
	t.Log("deleting all 5 agent pods simultaneously...")
	var deleteWg sync.WaitGroup
	deleteErrors := make([]error, numAgents)

	for i := 0; i < numAgents; i++ {
		deleteWg.Add(1)
		go func(idx int) {
			defer deleteWg.Done()
			event := makeDoneEvent(testRig, "polecat", agentNames[idx])
			deleteErrors[idx] = handleEvent(ctx, env.logger, env.cfg, event, env.pods, env.status)
		}(i)
	}
	deleteWg.Wait()

	for i, err := range deleteErrors {
		if err != nil {
			t.Errorf("failed to delete agent %d (%s): %v", i, agentNames[i], err)
		}
	}

	// Step 5: Verify all pods are cleaned up.
	deleteCtx, deleteCancel := context.WithTimeout(ctx, podDeleteTimeout)
	defer deleteCancel()

	for _, name := range podNames {
		env.waitForPodGone(deleteCtx, name)
	}
	t.Logf("all %d pods cleaned up successfully", numAgents)
}

// ---------- Test: Kill event deletes pod with Failed status ----------

func TestIntegration_KillEventDeletesPod(t *testing.T) {
	env := setupTestEnv(t)
	agentName := uniqueAgentName(t, "kill")
	podName := fmt.Sprintf("gt-%s-polecat-%s", testRig, agentName)

	defer env.cleanupPod(podName)

	ctx := context.Background()

	// Create pod.
	spawnEvent := makeSpawnEvent(testRig, "polecat", agentName)
	if err := handleEvent(ctx, env.logger, env.cfg, spawnEvent, env.pods, env.status); err != nil {
		t.Fatalf("handleEvent(AgentSpawn) error: %v", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, podCreateTimeout)
	defer cancel()
	env.waitForPod(waitCtx, podName)

	// Kill the agent.
	killEvent := beadswatcher.Event{
		Type:      beadswatcher.AgentKill,
		Rig:       testRig,
		Role:      "polecat",
		AgentName: agentName,
		BeadID:    fmt.Sprintf("gt-%s-polecat-%s", testRig, agentName),
		Metadata: map[string]string{
			"namespace": testNamespace,
		},
	}
	if err := handleEvent(ctx, env.logger, env.cfg, killEvent, env.pods, env.status); err != nil {
		t.Fatalf("handleEvent(AgentKill) error: %v", err)
	}

	// Verify pod is gone.
	deleteCtx, deleteCancel := context.WithTimeout(ctx, podDeleteTimeout)
	defer deleteCancel()
	env.waitForPodGone(deleteCtx, podName)

	t.Logf("pod %s deleted after AgentKill event", podName)
}

// ---------- Test: Crew pod gets persistent restart policy ----------

func TestIntegration_CrewPodRestartPolicy(t *testing.T) {
	env := setupTestEnv(t)
	agentName := uniqueAgentName(t, "crew")
	podName := fmt.Sprintf("gt-%s-crew-%s", testRig, agentName)

	defer env.cleanupPod(podName)

	ctx := context.Background()

	event := makeSpawnEvent(testRig, "crew", agentName)
	if err := handleEvent(ctx, env.logger, env.cfg, event, env.pods, env.status); err != nil {
		t.Fatalf("handleEvent(AgentSpawn) error: %v", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, podCreateTimeout)
	defer cancel()
	pod := env.waitForPod(waitCtx, podName)

	if pod.Spec.RestartPolicy != corev1.RestartPolicyAlways {
		t.Errorf("crew restart policy = %q, want Always", pod.Spec.RestartPolicy)
	}

	t.Logf("crew pod %s has RestartPolicy=%s", podName, pod.Spec.RestartPolicy)
}

// ---------- Test: Namespace safety guard ----------

func TestIntegration_NamespaceSafety(t *testing.T) {
	// This test verifies that our test infrastructure ONLY operates in gastown-test.
	// It's a meta-test to catch configuration mistakes.

	if testNamespace == "gastown" {
		t.Fatal("CRITICAL: testNamespace is set to 'gastown' (production). This MUST be 'gastown-test'.")
	}
	if !strings.Contains(testNamespace, "test") {
		t.Fatalf("testNamespace %q does not contain 'test' — safety check failed", testNamespace)
	}
}

// ---------- Test: Full lifecycle (spawn → running → done → gone) ----------

func TestIntegration_FullLifecycle(t *testing.T) {
	env := setupTestEnv(t)
	agentName := uniqueAgentName(t, "life")
	podName := fmt.Sprintf("gt-%s-polecat-%s", testRig, agentName)

	defer env.cleanupPod(podName)

	ctx := context.Background()

	// Phase 1: Spawn — pod created.
	t.Log("phase 1: spawning agent pod...")
	spawnEvent := makeSpawnEvent(testRig, "polecat", agentName)
	if err := handleEvent(ctx, env.logger, env.cfg, spawnEvent, env.pods, env.status); err != nil {
		t.Fatalf("spawn error: %v", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, podCreateTimeout)
	defer cancel()
	pod := env.waitForPod(waitCtx, podName)
	t.Logf("phase 1 complete: pod created (phase=%s)", pod.Status.Phase)

	// Phase 2: Running — pod reaches running state.
	t.Log("phase 2: waiting for Running state...")
	runCtx, runCancel := context.WithTimeout(ctx, podCreateTimeout)
	defer runCancel()
	pod = env.waitForPodReady(runCtx, podName)
	t.Logf("phase 2 complete: pod Running (IP=%s, Node=%s)", pod.Status.PodIP, pod.Spec.NodeName)

	// Phase 3: Done — pod deleted.
	t.Log("phase 3: sending done event...")
	doneEvent := makeDoneEvent(testRig, "polecat", agentName)
	if err := handleEvent(ctx, env.logger, env.cfg, doneEvent, env.pods, env.status); err != nil {
		t.Fatalf("done error: %v", err)
	}

	deleteCtx, deleteCancel := context.WithTimeout(ctx, podDeleteTimeout)
	defer deleteCancel()
	env.waitForPodGone(deleteCtx, podName)
	t.Log("phase 3 complete: pod gone — full lifecycle verified")
}
