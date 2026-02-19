package reconciler

import (
	"log/slog"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// UpgradeStrategy defines how a role's pods should be updated during drift.
type UpgradeStrategy int

const (
	// UpgradeSkip means don't kill running pods for image drift.
	// New pods spawned after the image change will use the new image.
	// Used for polecats: let running ones finish, new ones get new spec.
	UpgradeSkip UpgradeStrategy = iota

	// UpgradeRolling means update one pod at a time, waiting for the
	// replacement to become Ready before upgrading the next.
	UpgradeRolling

	// UpgradeLast means defer this role until all other non-Last roles
	// have been upgraded. Then apply UpgradeRolling.
	// Used for witness: it monitors other agents, so upgrade it last.
	UpgradeLast
)

// roleUpgradeStrategy returns the upgrade strategy for a given role.
func roleUpgradeStrategy(role string) UpgradeStrategy {
	switch role {
	case "polecat":
		return UpgradeSkip
	case "witness":
		return UpgradeLast
	default:
		// crew, refinery, and other persistent agents use rolling updates.
		return UpgradeRolling
	}
}

// UpgradeTracker tracks the state of an ongoing rolling upgrade across pods.
// It ensures only one pod per role is being upgraded at a time and that
// witness pods are upgraded after all other roles.
type UpgradeTracker struct {
	mu     sync.Mutex
	logger *slog.Logger

	// upgrading tracks pods currently being upgraded (deleted for recreation).
	// Key: pod name, Value: time the upgrade started.
	upgrading map[string]time.Time

	// pendingByRole tracks pods that need upgrading, grouped by role.
	// Key: role, Value: list of pod names needing upgrade.
	pendingByRole map[string][]string
}

// NewUpgradeTracker creates a new upgrade tracker.
func NewUpgradeTracker(logger *slog.Logger) *UpgradeTracker {
	return &UpgradeTracker{
		logger:        logger,
		upgrading:     make(map[string]time.Time),
		pendingByRole: make(map[string][]string),
	}
}

// Reset clears all tracking state. Called at the start of each reconcile pass.
func (t *UpgradeTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pendingByRole = make(map[string][]string)
}

// RegisterDrift records that a pod needs upgrading due to spec drift.
func (t *UpgradeTracker) RegisterDrift(podName, role string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pendingByRole[role] = append(t.pendingByRole[role], podName)
}

// MarkUpgrading records that a pod upgrade has started (pod deleted for recreation).
func (t *UpgradeTracker) MarkUpgrading(podName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.upgrading[podName] = time.Now()
}

// ClearUpgrading removes a pod from the upgrading set (pod recreated and healthy).
func (t *UpgradeTracker) ClearUpgrading(podName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.upgrading, podName)
}

// IsUpgrading returns true if any pod of the given role is currently being upgraded.
func (t *UpgradeTracker) IsUpgrading(role string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for name := range t.upgrading {
		// Pod names follow the pattern gt-<rig>-<role>-<agentName>
		// We check if the role segment matches.
		if podRole := extractRoleFromPodName(name); podRole == role {
			return true
		}
	}
	return false
}

// AllNonLastUpgraded returns true if all roles with non-Last strategies
// have no pending upgrades and nothing is currently upgrading.
func (t *UpgradeTracker) AllNonLastUpgraded() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check if any non-Last role has pending upgrades.
	for role, pods := range t.pendingByRole {
		if roleUpgradeStrategy(role) == UpgradeLast {
			continue
		}
		if len(pods) > 0 {
			return false
		}
	}

	// Check if any non-Last role is currently upgrading.
	for name := range t.upgrading {
		role := extractRoleFromPodName(name)
		if roleUpgradeStrategy(role) != UpgradeLast {
			return false
		}
	}

	return true
}

// CanUpgrade determines whether a specific pod can be upgraded right now,
// based on its role's strategy and the current upgrade state.
func (t *UpgradeTracker) CanUpgrade(podName, role string) bool {
	strategy := roleUpgradeStrategy(role)

	switch strategy {
	case UpgradeSkip:
		// Never upgrade running polecats for drift.
		return false

	case UpgradeLast:
		// Only upgrade witness after all other roles are done.
		if !t.AllNonLastUpgraded() {
			t.logger.Info("deferring witness upgrade until all other roles are upgraded",
				"pod", podName)
			return false
		}
		// Fall through to rolling check.
		return !t.IsUpgrading(role)

	case UpgradeRolling:
		// Only one pod per role at a time.
		return !t.IsUpgrading(role)

	default:
		return false
	}
}

// CleanStaleUpgrades removes entries from the upgrading set that are older
// than the timeout. This handles the case where a pod was deleted but the
// replacement never became healthy.
func (t *UpgradeTracker) CleanStaleUpgrades(timeout time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	for name, started := range t.upgrading {
		if now.Sub(started) > timeout {
			t.logger.Warn("upgrade timed out, clearing stale entry",
				"pod", name, "started", started)
			delete(t.upgrading, name)
		}
	}
}

// extractRoleFromPodName extracts the role segment from a pod name.
// Pod names follow the pattern: gt-<rig>-<role>-<agentName>
// e.g., "gt-gastown-polecat-furiosa" -> "polecat"
//
//	"gt-gastown-witness-main" -> "witness"
func extractRoleFromPodName(podName string) string {
	// Remove "gt-" prefix
	if len(podName) < 4 {
		return ""
	}
	rest := podName[3:] // skip "gt-"

	// Find the rig separator (first dash after prefix removal)
	dashIdx := 0
	for i, c := range rest {
		if c == '-' {
			dashIdx = i
			break
		}
	}
	if dashIdx == 0 {
		return ""
	}

	// rest[dashIdx+1:] is "role-agentName"
	afterRig := rest[dashIdx+1:]

	// Find the role separator (first dash in afterRig)
	for i, c := range afterRig {
		if c == '-' {
			return afterRig[:i]
		}
	}
	return afterRig // no agent name separator, entire string is role
}

// IsPodReady returns true if a pod is in Running phase and all containers
// have passing readiness probes.
func IsPodReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}
