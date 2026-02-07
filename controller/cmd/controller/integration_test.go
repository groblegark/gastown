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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
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

	// testTimeout is used by fake-client tests.
	testTimeout = 5 * time.Second
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

// ==========================================================================
// E2E Tests (real K8s cluster)
// ==========================================================================

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

func TestIntegration_ControllerDeletesPodFromBeadState(t *testing.T) {
	env := setupTestEnv(t)
	agentName := uniqueAgentName(t, "del")
	podName := fmt.Sprintf("gt-%s-polecat-%s", testRig, agentName)

	defer env.cleanupPod(podName) // safety net

	ctx := context.Background()

	// Step 1: Create the pod via spawn event.
	spawnEvt := makeSpawnEvent(testRig, "polecat", agentName)
	if err := handleEvent(ctx, env.logger, env.cfg, spawnEvt, env.pods, env.status); err != nil {
		t.Fatalf("handleEvent(AgentSpawn) error: %v", err)
	}

	// Wait for pod to exist.
	waitCtx, cancel := context.WithTimeout(ctx, podCreateTimeout)
	defer cancel()
	env.waitForPod(waitCtx, podName)

	// Step 2: Delete the pod via done event.
	doneEvt := makeDoneEvent(testRig, "polecat", agentName)
	if err := handleEvent(ctx, env.logger, env.cfg, doneEvt, env.pods, env.status); err != nil {
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

func TestIntegration_CrashRecoveryRecreatePod(t *testing.T) {
	env := setupTestEnv(t)
	agentName := uniqueAgentName(t, "crash")
	podName := fmt.Sprintf("gt-%s-polecat-%s", testRig, agentName)

	defer env.cleanupPod(podName)

	ctx := context.Background()

	// Step 1: Create pod via spawn event.
	spawnEvt := makeSpawnEvent(testRig, "polecat", agentName)
	if err := handleEvent(ctx, env.logger, env.cfg, spawnEvt, env.pods, env.status); err != nil {
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
	stuckEvt := makeStuckEvent(testRig, "polecat", agentName)
	if err := handleEvent(ctx, env.logger, env.cfg, stuckEvt, env.pods, env.status); err != nil {
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

func TestIntegration_KillEventDeletesPod(t *testing.T) {
	env := setupTestEnv(t)
	agentName := uniqueAgentName(t, "kill")
	podName := fmt.Sprintf("gt-%s-polecat-%s", testRig, agentName)

	defer env.cleanupPod(podName)

	ctx := context.Background()

	// Create pod.
	spawnEvt := makeSpawnEvent(testRig, "polecat", agentName)
	if err := handleEvent(ctx, env.logger, env.cfg, spawnEvt, env.pods, env.status); err != nil {
		t.Fatalf("handleEvent(AgentSpawn) error: %v", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, podCreateTimeout)
	defer cancel()
	env.waitForPod(waitCtx, podName)

	// Kill the agent.
	killEvt := beadswatcher.Event{
		Type:      beadswatcher.AgentKill,
		Rig:       testRig,
		Role:      "polecat",
		AgentName: agentName,
		BeadID:    fmt.Sprintf("gt-%s-polecat-%s", testRig, agentName),
		Metadata: map[string]string{
			"namespace": testNamespace,
		},
	}
	if err := handleEvent(ctx, env.logger, env.cfg, killEvt, env.pods, env.status); err != nil {
		t.Fatalf("handleEvent(AgentKill) error: %v", err)
	}

	// Verify pod is gone.
	deleteCtx, deleteCancel := context.WithTimeout(ctx, podDeleteTimeout)
	defer deleteCancel()
	env.waitForPodGone(deleteCtx, podName)

	t.Logf("pod %s deleted after AgentKill event", podName)
}

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

func TestIntegration_FullLifecycle(t *testing.T) {
	env := setupTestEnv(t)
	agentName := uniqueAgentName(t, "life")
	podName := fmt.Sprintf("gt-%s-polecat-%s", testRig, agentName)

	defer env.cleanupPod(podName)

	ctx := context.Background()

	// Phase 1: Spawn — pod created.
	t.Log("phase 1: spawning agent pod...")
	spawnEvt := makeSpawnEvent(testRig, "polecat", agentName)
	if err := handleEvent(ctx, env.logger, env.cfg, spawnEvt, env.pods, env.status); err != nil {
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
	doneEvt := makeDoneEvent(testRig, "polecat", agentName)
	if err := handleEvent(ctx, env.logger, env.cfg, doneEvt, env.pods, env.status); err != nil {
		t.Fatalf("done error: %v", err)
	}

	deleteCtx, deleteCancel := context.WithTimeout(ctx, podDeleteTimeout)
	defer deleteCancel()
	env.waitForPodGone(deleteCtx, podName)
	t.Log("phase 3 complete: pod gone — full lifecycle verified")
}

// ==========================================================================
// Fake-Client Unit Tests (gt-naa65p.9)
// ==========================================================================

// ---------------------------------------------------------------------------
// Beads Event -> Pod Creation
// ---------------------------------------------------------------------------

func TestIntegration_SpawnCreatesPolecatPod(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	event := spawnEvent("gastown", "polecat", "furiosa")
	if err := handleEvent(ctx, logger, cfg, event, pods, reporter); err != nil {
		t.Fatalf("handleEvent(spawn): %v", err)
	}

	pod, err := client.CoreV1().Pods("gastown").Get(ctx, "gt-gastown-polecat-furiosa", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pod not created: %v", err)
	}

	// Verify polecat-specific properties.
	if pod.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("polecat restart policy = %q, want Never", pod.Spec.RestartPolicy)
	}
	if pod.Labels[podmanager.LabelRole] != "polecat" {
		t.Errorf("role label = %q, want polecat", pod.Labels[podmanager.LabelRole])
	}
}

func TestIntegration_SpawnCreatesCrewPod(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	event := spawnEvent("gastown", "crew", "colonization")
	if err := handleEvent(ctx, logger, cfg, event, pods, reporter); err != nil {
		t.Fatalf("handleEvent(spawn crew): %v", err)
	}

	pod, err := client.CoreV1().Pods("gastown").Get(ctx, "gt-gastown-crew-colonization", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pod not created: %v", err)
	}

	// Crew pods restart on failure.
	if pod.Spec.RestartPolicy != corev1.RestartPolicyAlways {
		t.Errorf("crew restart policy = %q, want Always", pod.Spec.RestartPolicy)
	}
	if pod.Labels[podmanager.LabelRole] != "crew" {
		t.Errorf("role label = %q, want crew", pod.Labels[podmanager.LabelRole])
	}
}

func TestIntegration_SpawnSetsCorrectLabels(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	event := spawnEvent("beads", "witness", "w1")
	if err := handleEvent(ctx, logger, cfg, event, pods, reporter); err != nil {
		t.Fatalf("handleEvent: %v", err)
	}

	pod, err := client.CoreV1().Pods("gastown").Get(ctx, "gt-beads-witness-w1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pod not created: %v", err)
	}

	checks := map[string]string{
		podmanager.LabelApp:   podmanager.LabelAppValue,
		podmanager.LabelRig:   "beads",
		podmanager.LabelRole:  "witness",
		podmanager.LabelAgent: "w1",
	}
	for k, want := range checks {
		if got := pod.Labels[k]; got != want {
			t.Errorf("label %s = %q, want %q", k, got, want)
		}
	}
}

func TestIntegration_SpawnSetsEnvVars(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	event := spawnEvent("gastown", "polecat", "nux")
	if err := handleEvent(ctx, logger, cfg, event, pods, reporter); err != nil {
		t.Fatalf("handleEvent: %v", err)
	}

	pod, err := client.CoreV1().Pods("gastown").Get(ctx, "gt-gastown-polecat-nux", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pod not created: %v", err)
	}

	envMap := make(map[string]string)
	for _, e := range pod.Spec.Containers[0].Env {
		if e.Value != "" {
			envMap[e.Name] = e.Value
		}
	}

	required := map[string]string{
		"GT_ROLE":    "polecat",
		"GT_RIG":     "gastown",
		"GT_AGENT":   "nux",
		"GT_POLECAT": "nux",
	}
	for k, want := range required {
		if got := envMap[k]; got != want {
			t.Errorf("env %s = %q, want %q", k, got, want)
		}
	}

	// BD daemon env vars should be set.
	if _, ok := envMap["BD_DAEMON_HOST"]; !ok {
		t.Error("missing BD_DAEMON_HOST env var")
	}
	if envMap["BEADS_AUTO_START_DAEMON"] != "false" {
		t.Error("BEADS_AUTO_START_DAEMON should be false")
	}
}

func TestIntegration_SpawnWithSecretEnv(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	event := spawnEvent("gastown", "polecat", "furiosa")
	event.Metadata["api_key_secret"] = "gastown-api-keys"
	event.Metadata["api_key_secret_key"] = "ANTHROPIC_API_KEY"
	if err := handleEvent(ctx, logger, cfg, event, pods, reporter); err != nil {
		t.Fatalf("handleEvent: %v", err)
	}

	pod, err := client.CoreV1().Pods("gastown").Get(ctx, "gt-gastown-polecat-furiosa", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pod not created: %v", err)
	}

	// Find the secret env var.
	found := false
	for _, e := range pod.Spec.Containers[0].Env {
		if e.Name == "ANTHROPIC_API_KEY" && e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
			if e.ValueFrom.SecretKeyRef.Name != "gastown-api-keys" {
				t.Errorf("secret name = %q, want %q", e.ValueFrom.SecretKeyRef.Name, "gastown-api-keys")
			}
			found = true
			break
		}
	}
	if !found {
		t.Error("ANTHROPIC_API_KEY secret env var not found")
	}
}

func TestIntegration_MultiplePolecatSpawns(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	names := []string{"furiosa", "nux", "rictus", "valkyrie", "slit"}
	for _, name := range names {
		event := spawnEvent("gastown", "polecat", name)
		if err := handleEvent(ctx, logger, cfg, event, pods, reporter); err != nil {
			t.Fatalf("handleEvent(spawn %s): %v", name, err)
		}
	}

	// Verify all pods exist.
	podList, err := client.CoreV1().Pods("gastown").List(ctx, metav1.ListOptions{
		LabelSelector: "gastown.io/role=polecat",
	})
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(podList.Items) != len(names) {
		t.Errorf("got %d polecat pods, want %d", len(podList.Items), len(names))
	}
}

func TestIntegration_SpawnReportsStatus(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	event := spawnEvent("gastown", "polecat", "furiosa")
	if err := handleEvent(ctx, logger, cfg, event, pods, reporter); err != nil {
		t.Fatalf("handleEvent: %v", err)
	}

	reports := reporter.Reports()
	if len(reports) != 1 {
		t.Fatalf("got %d reports, want 1", len(reports))
	}
	r := reports[0]
	if r.AgentName != "gt-gastown-polecat-furiosa" {
		t.Errorf("agent name = %q, want gt-gastown-polecat-furiosa", r.AgentName)
	}
	if r.Status.Phase != "Pending" {
		t.Errorf("status phase = %q, want Pending", r.Status.Phase)
	}
	if r.Status.Ready {
		t.Error("status should not be ready on spawn")
	}
}

// ---------------------------------------------------------------------------
// Pod Lifecycle -> Beads Update
// ---------------------------------------------------------------------------

func TestIntegration_DoneDeletesPodAndReportsSucceeded(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	// First spawn a pod.
	if err := handleEvent(ctx, logger, cfg, spawnEvent("gastown", "polecat", "furiosa"), pods, reporter); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	// Then signal done.
	if err := handleEvent(ctx, logger, cfg, doneEvent("gastown", "polecat", "furiosa"), pods, reporter); err != nil {
		t.Fatalf("done: %v", err)
	}

	// Pod should be deleted.
	_, err := client.CoreV1().Pods("gastown").Get(ctx, "gt-gastown-polecat-furiosa", metav1.GetOptions{})
	if err == nil {
		t.Error("pod should be deleted after AgentDone")
	}

	// Check status report: spawn reports Pending, done reports Succeeded.
	reports := reporter.Reports()
	if len(reports) < 2 {
		t.Fatalf("got %d reports, want >= 2", len(reports))
	}
	last := reports[len(reports)-1]
	if last.Status.Phase != "Succeeded" {
		t.Errorf("done status phase = %q, want Succeeded", last.Status.Phase)
	}
}

func TestIntegration_KillDeletesPodAndReportsFailed(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	// Spawn then kill.
	if err := handleEvent(ctx, logger, cfg, spawnEvent("gastown", "polecat", "rictus"), pods, reporter); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if err := handleEvent(ctx, logger, cfg, killEvent("gastown", "polecat", "rictus"), pods, reporter); err != nil {
		t.Fatalf("kill: %v", err)
	}

	// Pod should be deleted.
	_, err := client.CoreV1().Pods("gastown").Get(ctx, "gt-gastown-polecat-rictus", metav1.GetOptions{})
	if err == nil {
		t.Error("pod should be deleted after AgentKill")
	}

	// Kill reports Failed status.
	reports := reporter.Reports()
	last := reports[len(reports)-1]
	if last.Status.Phase != "Failed" {
		t.Errorf("kill status phase = %q, want Failed", last.Status.Phase)
	}
}

func TestIntegration_StuckRestartsPod(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	// Spawn a pod.
	if err := handleEvent(ctx, logger, cfg, spawnEvent("gastown", "crew", "colonization"), pods, reporter); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	// Mark it stuck — should delete and recreate.
	if err := handleEvent(ctx, logger, cfg, stuckEvent("gastown", "crew", "colonization"), pods, reporter); err != nil {
		t.Fatalf("stuck: %v", err)
	}

	// Pod should still exist (recreated).
	pod, err := client.CoreV1().Pods("gastown").Get(ctx, "gt-gastown-crew-colonization", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pod should exist after restart: %v", err)
	}
	if pod.Labels[podmanager.LabelAgent] != "colonization" {
		t.Error("restarted pod has wrong labels")
	}

	// Status should show Pending (restarted).
	reports := reporter.Reports()
	last := reports[len(reports)-1]
	if last.Status.Phase != "Pending" {
		t.Errorf("stuck status phase = %q, want Pending", last.Status.Phase)
	}
	if last.Status.Message != "restarted due to stuck detection" {
		t.Errorf("stuck message = %q, want restart message", last.Status.Message)
	}
}

func TestIntegration_StuckOnNonexistentPodCreatesNew(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	// Send stuck event for a pod that doesn't exist yet — should still create.
	if err := handleEvent(ctx, logger, cfg, stuckEvent("gastown", "polecat", "ghost"), pods, reporter); err != nil {
		t.Fatalf("stuck on nonexistent pod: %v", err)
	}

	// Pod should be created.
	_, err := client.CoreV1().Pods("gastown").Get(ctx, "gt-gastown-polecat-ghost", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pod should exist after stuck-on-nonexistent: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Event Metadata and Namespace Handling
// ---------------------------------------------------------------------------

func TestIntegration_EventMetadataOverridesNamespace(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	event := spawnEvent("beads", "polecat", "quartz")
	event.Metadata["namespace"] = "beads-ns"
	if err := handleEvent(ctx, logger, cfg, event, pods, reporter); err != nil {
		t.Fatalf("handleEvent: %v", err)
	}

	// Pod should be in the custom namespace, not the default.
	_, err := client.CoreV1().Pods("beads-ns").Get(ctx, "gt-beads-polecat-quartz", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pod should be in beads-ns: %v", err)
	}

	// Verify NOT in default namespace.
	_, err = client.CoreV1().Pods("gastown").Get(ctx, "gt-beads-polecat-quartz", metav1.GetOptions{})
	if err == nil {
		t.Error("pod should not be in default namespace")
	}
}

func TestIntegration_EventMetadataOverridesImage(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	event := spawnEvent("gastown", "polecat", "furiosa")
	event.Metadata["image"] = "custom-agent:v2"
	if err := handleEvent(ctx, logger, cfg, event, pods, reporter); err != nil {
		t.Fatalf("handleEvent: %v", err)
	}

	pod, err := client.CoreV1().Pods("gastown").Get(ctx, "gt-gastown-polecat-furiosa", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pod not created: %v", err)
	}

	if pod.Spec.Containers[0].Image != "custom-agent:v2" {
		t.Errorf("image = %q, want custom-agent:v2", pod.Spec.Containers[0].Image)
	}
}

func TestIntegration_ServiceAccountFromMetadata(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	event := spawnEvent("gastown", "crew", "admin")
	event.Metadata["service_account"] = "gastown-admin"
	if err := handleEvent(ctx, logger, cfg, event, pods, reporter); err != nil {
		t.Fatalf("handleEvent: %v", err)
	}

	pod, err := client.CoreV1().Pods("gastown").Get(ctx, "gt-gastown-crew-admin", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pod not created: %v", err)
	}

	if pod.Spec.ServiceAccountName != "gastown-admin" {
		t.Errorf("ServiceAccountName = %q, want gastown-admin", pod.Spec.ServiceAccountName)
	}
}

func TestIntegration_ConfigMapFromMetadata(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	event := spawnEvent("gastown", "witness", "w1")
	event.Metadata["configmap"] = "gastown-config"
	if err := handleEvent(ctx, logger, cfg, event, pods, reporter); err != nil {
		t.Fatalf("handleEvent: %v", err)
	}

	pod, err := client.CoreV1().Pods("gastown").Get(ctx, "gt-gastown-witness-w1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pod not created: %v", err)
	}

	// Check ConfigMap volume exists.
	found := false
	for _, v := range pod.Spec.Volumes {
		if v.Name == podmanager.VolumeBeadsConfig && v.ConfigMap != nil {
			if v.ConfigMap.Name != "gastown-config" {
				t.Errorf("configmap name = %q, want gastown-config", v.ConfigMap.Name)
			}
			found = true
		}
	}
	if !found {
		t.Error("configmap volume not found")
	}
}

// ---------------------------------------------------------------------------
// Failure Scenarios
// ---------------------------------------------------------------------------

func TestIntegration_K8sCreateErrorDoesNotCrash(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	// Inject a create failure via reactor.
	client.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated K8s API unavailable")
	})

	event := spawnEvent("gastown", "polecat", "furiosa")
	err := handleEvent(ctx, logger, cfg, event, pods, reporter)
	if err == nil {
		t.Fatal("expected error from failed pod creation")
	}

	// On error, create returns early. So no status report should exist.
	reports := reporter.Reports()
	if len(reports) != 0 {
		t.Errorf("got %d reports, want 0 (create failed)", len(reports))
	}
}

func TestIntegration_K8sDeleteErrorReturned(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	// Spawn a pod first.
	if err := handleEvent(ctx, logger, cfg, spawnEvent("gastown", "polecat", "nux"), pods, reporter); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	// Inject a delete failure.
	client.PrependReactor("delete", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated delete failure")
	})

	// Done event should return error but still report status.
	err := handleEvent(ctx, logger, cfg, doneEvent("gastown", "polecat", "nux"), pods, reporter)
	if err == nil {
		t.Fatal("expected error from failed delete")
	}

	// Status should still be reported even on delete failure (code reports regardless).
	reports := reporter.Reports()
	var foundSucceeded bool
	for _, r := range reports {
		if r.Status.Phase == "Succeeded" {
			foundSucceeded = true
		}
	}
	if !foundSucceeded {
		t.Error("expected Succeeded status report even on delete failure")
	}
}

func TestIntegration_DuplicateSpawnFailsGracefully(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	event := spawnEvent("gastown", "polecat", "furiosa")

	// First spawn succeeds.
	if err := handleEvent(ctx, logger, cfg, event, pods, reporter); err != nil {
		t.Fatalf("first spawn: %v", err)
	}

	// Second spawn for same agent should fail (pod already exists).
	err := handleEvent(ctx, logger, cfg, event, pods, reporter)
	if err == nil {
		t.Error("expected error from duplicate spawn, got nil")
	}
}

func TestIntegration_DeleteNonexistentPodReturnsError(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	// Done event for a pod that doesn't exist.
	err := handleEvent(ctx, logger, cfg, doneEvent("gastown", "polecat", "nonexistent"), pods, reporter)
	if err == nil {
		t.Fatal("expected error deleting nonexistent pod")
	}

	// Status should still be reported.
	reports := reporter.Reports()
	var foundSucceeded bool
	for _, r := range reports {
		if r.Status.Phase == "Succeeded" {
			foundSucceeded = true
		}
	}
	if !foundSucceeded {
		t.Error("should report Succeeded status even for nonexistent pod")
	}
}

func TestIntegration_UnknownEventTypeIgnored(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	event := beadswatcher.Event{
		Type:      beadswatcher.EventType("unknown_type"),
		Rig:       "gastown",
		Role:      "polecat",
		AgentName: "mystery",
	}

	// Unknown event types should not error.
	if err := handleEvent(ctx, logger, cfg, event, pods, reporter); err != nil {
		t.Fatalf("unknown event should not error: %v", err)
	}

	// No pods should be created.
	podList, err := client.CoreV1().Pods("gastown").List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(podList.Items) != 0 {
		t.Errorf("got %d pods, want 0", len(podList.Items))
	}
}

// ---------------------------------------------------------------------------
// Full Controller Loop
// ---------------------------------------------------------------------------

func TestIntegration_FullRunLoop(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	cfg.SyncInterval = 24 * time.Hour // disable periodic sync for this test
	watcher := newChannelWatcher(16)
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start controller in background.
	runDone := make(chan error, 1)
	go func() {
		runDone <- run(ctx, logger, cfg, watcher, pods, reporter)
	}()

	// Give the controller a moment to start.
	time.Sleep(50 * time.Millisecond)

	// 1. Spawn a polecat.
	watcher.Send(spawnEvent("gastown", "polecat", "furiosa"))
	time.Sleep(50 * time.Millisecond)

	pod, err := waitForPod(ctx, client, "gt-gastown-polecat-furiosa", "gastown", testTimeout)
	if err != nil {
		t.Fatalf("pod not created: %v", err)
	}
	if pod.Labels[podmanager.LabelRole] != "polecat" {
		t.Errorf("wrong role label: %s", pod.Labels[podmanager.LabelRole])
	}

	// 2. Spawn a crew member.
	watcher.Send(spawnEvent("gastown", "crew", "colonization"))
	time.Sleep(50 * time.Millisecond)

	_, err = waitForPod(ctx, client, "gt-gastown-crew-colonization", "gastown", testTimeout)
	if err != nil {
		t.Fatalf("crew pod not created: %v", err)
	}

	// 3. Signal polecat done.
	watcher.Send(doneEvent("gastown", "polecat", "furiosa"))
	time.Sleep(50 * time.Millisecond)

	if err := waitForNoPod(ctx, client, "gt-gastown-polecat-furiosa", "gastown", testTimeout); err != nil {
		t.Fatalf("polecat pod not deleted: %v", err)
	}

	// 4. Crew is still running.
	_, err = client.CoreV1().Pods("gastown").Get(ctx, "gt-gastown-crew-colonization", metav1.GetOptions{})
	if err != nil {
		t.Error("crew pod should still exist")
	}

	// 5. Kill crew.
	watcher.Send(killEvent("gastown", "crew", "colonization"))
	time.Sleep(50 * time.Millisecond)

	if err := waitForNoPod(ctx, client, "gt-gastown-crew-colonization", "gastown", testTimeout); err != nil {
		t.Fatalf("crew pod not deleted: %v", err)
	}

	// Verify status reports.
	reports := reporter.Reports()
	if len(reports) < 4 {
		t.Errorf("expected at least 4 status reports, got %d", len(reports))
	}

	// Shutdown.
	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Logf("run() returned (expected): %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run() did not stop within timeout")
	}
}

func TestIntegration_FullRunLoopStuckRestart(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	cfg.SyncInterval = 24 * time.Hour
	watcher := newChannelWatcher(16)
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- run(ctx, logger, cfg, watcher, pods, reporter)
	}()
	time.Sleep(50 * time.Millisecond)

	// Spawn a crew member.
	watcher.Send(spawnEvent("gastown", "crew", "colonization"))
	time.Sleep(50 * time.Millisecond)

	_, err := waitForPod(ctx, client, "gt-gastown-crew-colonization", "gastown", testTimeout)
	if err != nil {
		t.Fatalf("pod not created: %v", err)
	}

	// Mark it stuck — controller should restart (delete + create).
	watcher.Send(stuckEvent("gastown", "crew", "colonization"))
	time.Sleep(100 * time.Millisecond)

	// Pod should still exist (recreated).
	_, err = client.CoreV1().Pods("gastown").Get(ctx, "gt-gastown-crew-colonization", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pod should exist after stuck restart: %v", err)
	}

	// Verify stuck-related status report.
	reports := reporter.Reports()
	var foundRestart bool
	for _, r := range reports {
		if r.Status.Message == "restarted due to stuck detection" {
			foundRestart = true
		}
	}
	if !foundRestart {
		t.Error("expected restart status report")
	}

	cancel()
	<-runDone
}

func TestIntegration_PeriodicSyncReportsAllPods(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	cfg.SyncInterval = 100 * time.Millisecond // Fast sync for test.
	watcher := newChannelWatcher(16)
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Pre-create some pods before starting the controller.
	for _, name := range []string{"alpha", "beta"} {
		spec := podmanager.AgentPodSpec{
			Rig: "gastown", Role: "polecat", AgentName: name,
			Image: "agent:test", Namespace: "gastown",
		}
		if err := pods.CreateAgentPod(ctx, spec); err != nil {
			t.Fatalf("pre-create pod %s: %v", name, err)
		}
	}

	runDone := make(chan error, 1)
	go func() {
		runDone <- run(ctx, logger, cfg, watcher, pods, reporter)
	}()

	// Wait for at least one sync cycle.
	time.Sleep(300 * time.Millisecond)

	if reporter.SyncRunCount() < 1 {
		t.Error("expected at least one periodic sync run")
	}

	// Sync should have reported status for both pods.
	reports := reporter.Reports()
	agents := make(map[string]bool)
	for _, r := range reports {
		agents[r.AgentName] = true
	}
	if !agents["gt-gastown-polecat-alpha"] {
		t.Error("sync did not report alpha")
	}
	if !agents["gt-gastown-polecat-beta"] {
		t.Error("sync did not report beta")
	}

	cancel()
	<-runDone
}

// ---------------------------------------------------------------------------
// Multi-Rig and Cross-Rig
// ---------------------------------------------------------------------------

func TestIntegration_MultiRigPodsIsolated(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	// Spawn in two different rigs.
	if err := handleEvent(ctx, logger, cfg, spawnEvent("gastown", "polecat", "furiosa"), pods, reporter); err != nil {
		t.Fatalf("spawn gastown: %v", err)
	}
	beadsEvt := spawnEvent("beads", "polecat", "quartz")
	if err := handleEvent(ctx, logger, cfg, beadsEvt, pods, reporter); err != nil {
		t.Fatalf("spawn beads: %v", err)
	}

	// Both pods should exist.
	_, err := client.CoreV1().Pods("gastown").Get(ctx, "gt-gastown-polecat-furiosa", metav1.GetOptions{})
	if err != nil {
		t.Error("gastown pod should exist")
	}
	_, err = client.CoreV1().Pods("gastown").Get(ctx, "gt-beads-polecat-quartz", metav1.GetOptions{})
	if err != nil {
		t.Error("beads pod should exist")
	}

	// Deleting gastown polecat should not affect beads polecat.
	if err := handleEvent(ctx, logger, cfg, doneEvent("gastown", "polecat", "furiosa"), pods, reporter); err != nil {
		t.Fatalf("done gastown: %v", err)
	}
	_, err = client.CoreV1().Pods("gastown").Get(ctx, "gt-beads-polecat-quartz", metav1.GetOptions{})
	if err != nil {
		t.Error("beads pod should still exist after gastown polecat done")
	}
}

// ---------------------------------------------------------------------------
// Security and Pod Spec Validation
// ---------------------------------------------------------------------------

func TestIntegration_SpawnedPodHasSecurityContext(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	event := spawnEvent("gastown", "polecat", "furiosa")
	if err := handleEvent(ctx, logger, cfg, event, pods, reporter); err != nil {
		t.Fatalf("handleEvent: %v", err)
	}

	pod, err := client.CoreV1().Pods("gastown").Get(ctx, "gt-gastown-polecat-furiosa", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pod not found: %v", err)
	}

	// Pod-level security.
	psc := pod.Spec.SecurityContext
	if psc == nil {
		t.Fatal("pod security context is nil")
	}
	if psc.RunAsUser == nil || *psc.RunAsUser != podmanager.AgentUID {
		t.Errorf("RunAsUser should be %d", podmanager.AgentUID)
	}
	if psc.RunAsNonRoot == nil || !*psc.RunAsNonRoot {
		t.Error("RunAsNonRoot should be true")
	}

	// Container-level security.
	csc := pod.Spec.Containers[0].SecurityContext
	if csc == nil {
		t.Fatal("container security context is nil")
	}
	if csc.AllowPrivilegeEscalation == nil || *csc.AllowPrivilegeEscalation {
		t.Error("AllowPrivilegeEscalation should be false")
	}
	if csc.Capabilities == nil || len(csc.Capabilities.Drop) == 0 {
		t.Error("should drop ALL capabilities")
	}
}

func TestIntegration_SpawnedPodHasCorrectVolumes(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := slog.Default()
	cfg := testConfig()
	pods := podmanager.New(client, logger)
	reporter := newRecordingReporter(client, cfg.Namespace, logger)
	ctx := context.Background()

	event := spawnEvent("gastown", "polecat", "furiosa")
	if err := handleEvent(ctx, logger, cfg, event, pods, reporter); err != nil {
		t.Fatalf("handleEvent: %v", err)
	}

	pod, err := client.CoreV1().Pods("gastown").Get(ctx, "gt-gastown-polecat-furiosa", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pod not found: %v", err)
	}

	// Check workspace volume (EmptyDir for polecats).
	volMap := make(map[string]corev1.Volume)
	for _, v := range pod.Spec.Volumes {
		volMap[v.Name] = v
	}
	ws, ok := volMap[podmanager.VolumeWorkspace]
	if !ok {
		t.Fatal("missing workspace volume")
	}
	if ws.EmptyDir == nil {
		t.Error("polecat workspace should be EmptyDir")
	}

	// Check mount paths.
	mountMap := make(map[string]string)
	for _, m := range pod.Spec.Containers[0].VolumeMounts {
		mountMap[m.Name] = m.MountPath
	}
	if mountMap[podmanager.VolumeWorkspace] != podmanager.MountWorkspace {
		t.Errorf("workspace mount = %q, want %q", mountMap[podmanager.VolumeWorkspace], podmanager.MountWorkspace)
	}
	if mountMap[podmanager.VolumeTmp] != podmanager.MountTmp {
		t.Errorf("tmp mount = %q, want %q", mountMap[podmanager.VolumeTmp], podmanager.MountTmp)
	}
}
