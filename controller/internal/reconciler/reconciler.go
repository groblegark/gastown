// Package reconciler diffs desired agent bead state (from daemon) against
// actual K8s pod state and creates/deletes pods to converge.
package reconciler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/steveyegge/gastown/controller/internal/config"
	"github.com/steveyegge/gastown/controller/internal/daemonclient"
	"github.com/steveyegge/gastown/controller/internal/podmanager"
)

// SpecBuilder constructs an AgentPodSpec from config, bead identity, and metadata.
// The metadata map may contain per-bead overrides (e.g., image).
type SpecBuilder func(cfg *config.Config, rig, role, agentName string, metadata map[string]string) podmanager.AgentPodSpec

// Reconciler diffs desired state (agent beads) against actual state (K8s pods)
// and creates/deletes pods to converge.
type Reconciler struct {
	lister         daemonclient.BeadLister
	pods           podmanager.Manager
	cfg            *config.Config
	logger         *slog.Logger
	specBuilder    SpecBuilder
	mu             sync.Mutex // prevent concurrent reconciles
	digestTracker  *ImageDigestTracker
	upgradeTracker *UpgradeTracker
}

// New creates a Reconciler.
func New(
	lister daemonclient.BeadLister,
	pods podmanager.Manager,
	cfg *config.Config,
	logger *slog.Logger,
	specBuilder SpecBuilder,
) *Reconciler {
	return &Reconciler{
		lister:         lister,
		pods:           pods,
		cfg:            cfg,
		logger:         logger,
		specBuilder:    specBuilder,
		digestTracker:  NewImageDigestTracker(logger, 5*time.Minute),
		upgradeTracker: NewUpgradeTracker(logger),
	}
}

// DigestTracker returns the image digest tracker for external callers
// (e.g., periodic registry refresh from the main loop).
func (r *Reconciler) DigestTracker() *ImageDigestTracker {
	return r.digestTracker
}

// Reconcile performs a single reconciliation pass:
// 1. List desired beads from daemon
// 2. List actual pods from K8s
// 3. Create missing pods, delete orphan pods, recreate failed pods
func (r *Reconciler) Reconcile(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Get desired state from daemon.
	beads, err := r.lister.ListAgentBeads(ctx)
	if err != nil {
		// Fail-safe: if we can't reach the daemon, do NOT delete any pods.
		return fmt.Errorf("listing agent beads: %w", err)
	}

	// Build desired pod name set.
	desired := make(map[string]daemonclient.AgentBead)
	for _, b := range beads {
		podName := fmt.Sprintf("gt-%s-%s-%s", b.Rig, b.Role, b.AgentName)
		desired[podName] = b
	}

	// Get actual state from K8s.
	actual, err := r.pods.ListAgentPods(ctx, r.cfg.Namespace, map[string]string{
		podmanager.LabelApp: podmanager.LabelAppValue,
	})
	if err != nil {
		return fmt.Errorf("listing agent pods: %w", err)
	}

	actualMap := make(map[string]corev1.Pod)
	for _, p := range actual {
		// Only consider pods with the gastown.io/agent label — this
		// excludes the controller itself and other infrastructure pods
		// that share the app.kubernetes.io/name=gastown label.
		if _, ok := p.Labels[podmanager.LabelAgent]; !ok {
			continue
		}
		actualMap[p.Name] = p
	}

	// Delete orphan pods (exist in K8s but not in desired).
	// Guard: if daemon returned zero beads but pods exist, this is likely a
	// transient daemon issue (restart, query race, etc.). Refuse to mass-delete
	// to prevent an "orphan storm" that kills all agent pods.
	if len(desired) == 0 && len(actualMap) > 0 {
		r.logger.Warn("desired state is empty but agent pods exist — skipping orphan deletion to prevent mass kill",
			"actual_pods", len(actualMap))
	} else {
		for name, pod := range actualMap {
			if _, ok := desired[name]; !ok {
				r.logger.Info("deleting orphan pod", "pod", name)
				if err := r.pods.DeleteAgentPod(ctx, name, pod.Namespace); err != nil {
					return fmt.Errorf("deleting orphan pod %s: %w", name, err)
				}
			}
		}
	}

	// Count active (non-Failed, non-orphan) pods for concurrency limiting.
	// Only count pods that are in the desired set — orphans were just deleted.
	activePods := 0
	for name, pod := range actualMap {
		if _, inDesired := desired[name]; inDesired && pod.Status.Phase != corev1.PodFailed {
			activePods++
		}
	}

	// Clean stale upgrade entries (pods deleted but never recreated).
	r.upgradeTracker.CleanStaleUpgrades(10 * time.Minute)
	r.upgradeTracker.Reset()

	// Phase 1: Scan all pods for drift and register with upgrade tracker.
	// This builds the full picture before making any upgrade decisions.
	driftReasons := make(map[string]string)
	for name, bead := range desired {
		pod, exists := actualMap[name]
		if !exists || pod.Status.Phase == corev1.PodFailed {
			continue // Missing or failed pods are handled in phase 2
		}
		desiredSpec := r.specBuilder(r.cfg, bead.Rig, bead.Role, bead.AgentName, bead.Metadata)
		desiredSpec.BeadID = bead.ID
		reason := podDriftReason(desiredSpec, &pod, r.digestTracker)
		if reason != "" {
			driftReasons[name] = reason
			r.upgradeTracker.RegisterDrift(name, bead.Role)
		}

		// Clear upgrade tracking for pods that have been successfully recreated.
		if IsPodReady(&pod) {
			r.upgradeTracker.ClearUpgrading(name)
		}
	}

	// Create missing pods and recreate failed pods.
	// Respect SpawnBurstLimit (max pods created per pass) and
	// MaxConcurrentPods (total active pod cap).
	burstLimit := r.cfg.SpawnBurstLimit
	if burstLimit <= 0 {
		burstLimit = 3 // safety default
	}
	created := 0

	for name, bead := range desired {
		if pod, exists := actualMap[name]; exists {
			// Pod exists. Check if it's in a terminal failed state.
			if pod.Status.Phase == corev1.PodFailed {
				r.logger.Info("deleting failed pod for recreation", "pod", name)
				if err := r.pods.DeleteAgentPod(ctx, name, pod.Namespace); err != nil {
					return fmt.Errorf("deleting failed pod %s: %w", name, err)
				}
				activePods-- // no longer active after deletion
				// Fall through to create.
			} else if reason, hasDrift := driftReasons[name]; hasDrift {
				// Pod has spec drift. Use role-aware upgrade strategy.
				if !r.upgradeTracker.CanUpgrade(name, bead.Role) {
					r.logger.Info("spec drift detected but upgrade deferred by strategy",
						"pod", name, "role", bead.Role, "reason", reason)
					continue
				}
				r.logger.Info("spec drift detected, upgrading pod",
					"pod", name, "role", bead.Role, "reason", reason)
				if err := r.pods.DeleteAgentPod(ctx, name, pod.Namespace); err != nil {
					return fmt.Errorf("deleting pod for update %s: %w", name, err)
				}
				r.upgradeTracker.MarkUpgrading(name)
				activePods-- // no longer active after deletion
				// Fall through to create with new spec.
			} else {
				continue
			}
		}

		// Check burst limit.
		if created >= burstLimit {
			r.logger.Info("spawn burst limit reached, deferring remaining pods",
				"limit", burstLimit, "deferred", name)
			continue
		}

		// Check max concurrent pods.
		if r.cfg.MaxConcurrentPods > 0 && activePods >= r.cfg.MaxConcurrentPods {
			r.logger.Info("max concurrent pods reached, deferring pod",
				"limit", r.cfg.MaxConcurrentPods, "active", activePods, "deferred", name)
			continue
		}

		// Create the pod.
		spec := r.specBuilder(r.cfg, bead.Rig, bead.Role, bead.AgentName, bead.Metadata)
		spec.BeadID = bead.ID
		r.logger.Info("creating pod", "pod", name)
		if err := r.pods.CreateAgentPod(ctx, spec); err != nil {
			return fmt.Errorf("creating pod %s: %w", name, err)
		}
		created++
		activePods++
	}

	if created > 0 || len(desired) > len(actualMap) {
		r.logger.Info("reconcile pass complete",
			"created", created, "active", activePods,
			"desired", len(desired), "burst_limit", burstLimit)
	}

	return nil
}

// podDriftReason returns a non-empty string describing why the pod needs
// recreation, or "" if the pod matches the desired spec.
func podDriftReason(desired podmanager.AgentPodSpec, actual *corev1.Pod, tracker *ImageDigestTracker) string {
	// Check agent image drift (tag changed).
	if agentChanged(desired.Image, actual) {
		return fmt.Sprintf("agent image changed: %s", desired.Image)
	}
	// Check image digest drift (same tag, different digest — e.g. :latest updated).
	if tracker != nil {
		if reason := digestDrift(desired.Image, actual, tracker); reason != "" {
			return reason
		}
	}
	return ""
}

// digestDrift checks whether the running pod's image digest differs from the
// latest known digest for the same image tag. This detects :latest updates
// that tag-only comparison misses.
func digestDrift(desiredImage string, actual *corev1.Pod, tracker *ImageDigestTracker) string {
	for _, cs := range actual.Status.ContainerStatuses {
		if cs.Name != "agent" {
			continue
		}
		runningDigest := extractDigestFromImageID(cs.ImageID)
		if runningDigest == "" {
			return ""
		}
		// Record this pod's digest. If it's the first pod seen for this image,
		// this becomes the baseline. If it differs from a previously recorded
		// digest, RecordDigest returns true (meaning an update was detected
		// from a registry check or a newer pod).
		tracker.RecordDigest(desiredImage, runningDigest)

		latestDigest := tracker.LatestDigest(desiredImage)
		if latestDigest != "" && latestDigest != runningDigest {
			return fmt.Sprintf("image digest changed: running %s, latest %s",
				truncDigest(runningDigest), truncDigest(latestDigest))
		}
	}
	return ""
}

// agentChanged returns true if the desired agent image differs from the
// running pod's agent container image (compared by tag, not digest).
func agentChanged(desiredImage string, actual *corev1.Pod) bool {
	if desiredImage == "" {
		return false
	}
	for _, c := range actual.Spec.Containers {
		if c.Name == "agent" {
			return c.Image != desiredImage
		}
	}
	return false
}

