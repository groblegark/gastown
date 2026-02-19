package reconciler

import (
	"log/slog"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestRoleUpgradeStrategy(t *testing.T) {
	tests := []struct {
		role string
		want UpgradeStrategy
	}{
		{"polecat", UpgradeSkip},
		{"witness", UpgradeLast},
		{"crew", UpgradeRolling},
		{"refinery", UpgradeRolling},
		{"mayor", UpgradeRolling},
		{"deacon", UpgradeRolling},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			got := roleUpgradeStrategy(tt.role)
			if got != tt.want {
				t.Errorf("roleUpgradeStrategy(%q) = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

func TestExtractRoleFromPodName(t *testing.T) {
	tests := []struct {
		podName string
		want    string
	}{
		{"gt-gastown-polecat-furiosa", "polecat"},
		{"gt-gastown-witness-main", "witness"},
		{"gt-gastown-crew-toolbox", "crew"},
		{"gt-gastown-refinery-main", "refinery"},
		{"gt-beads-polecat-obsidian", "polecat"},
		{"gt", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.podName, func(t *testing.T) {
			got := extractRoleFromPodName(tt.podName)
			if got != tt.want {
				t.Errorf("extractRoleFromPodName(%q) = %q, want %q", tt.podName, got, tt.want)
			}
		})
	}
}

func TestUpgradeTracker_PolecatsSkipped(t *testing.T) {
	tracker := NewUpgradeTracker(testLogger())

	tracker.RegisterDrift("gt-gastown-polecat-furiosa", "polecat")

	// Polecats should never be allowed to upgrade
	if tracker.CanUpgrade("gt-gastown-polecat-furiosa", "polecat") {
		t.Error("polecats should not be upgraded for drift")
	}
}

func TestUpgradeTracker_RollingOneAtATime(t *testing.T) {
	tracker := NewUpgradeTracker(testLogger())

	tracker.RegisterDrift("gt-gastown-crew-toolbox", "crew")
	tracker.RegisterDrift("gt-gastown-crew-decision", "crew")

	// First crew pod can upgrade
	if !tracker.CanUpgrade("gt-gastown-crew-toolbox", "crew") {
		t.Error("first crew pod should be allowed to upgrade")
	}

	// Mark it as upgrading
	tracker.MarkUpgrading("gt-gastown-crew-toolbox")

	// Second crew pod should be deferred
	if tracker.CanUpgrade("gt-gastown-crew-decision", "crew") {
		t.Error("second crew pod should be deferred while first is upgrading")
	}

	// Clear the first upgrade
	tracker.ClearUpgrading("gt-gastown-crew-toolbox")

	// Now the second can proceed
	if !tracker.CanUpgrade("gt-gastown-crew-decision", "crew") {
		t.Error("second crew pod should be allowed after first is done")
	}
}

func TestUpgradeTracker_WitnessLast(t *testing.T) {
	tracker := NewUpgradeTracker(testLogger())

	// Register drift for both crew and witness
	tracker.RegisterDrift("gt-gastown-crew-toolbox", "crew")
	tracker.RegisterDrift("gt-gastown-witness-main", "witness")

	// Witness should be deferred because crew hasn't upgraded yet
	if tracker.CanUpgrade("gt-gastown-witness-main", "witness") {
		t.Error("witness should be deferred until crew upgrades complete")
	}

	// Crew can upgrade
	if !tracker.CanUpgrade("gt-gastown-crew-toolbox", "crew") {
		t.Error("crew should be allowed to upgrade")
	}

	// Simulate crew upgrade complete: clear pending and upgrading
	tracker.pendingByRole["crew"] = nil
	tracker.ClearUpgrading("gt-gastown-crew-toolbox")

	// Now witness should be allowed
	if !tracker.CanUpgrade("gt-gastown-witness-main", "witness") {
		t.Error("witness should be allowed after crew upgrade completes")
	}
}

func TestUpgradeTracker_AllNonLastUpgraded(t *testing.T) {
	tracker := NewUpgradeTracker(testLogger())

	// No pending upgrades = all upgraded
	if !tracker.AllNonLastUpgraded() {
		t.Error("empty tracker should report all non-last upgraded")
	}

	// Add crew pending
	tracker.RegisterDrift("gt-gastown-crew-toolbox", "crew")
	if tracker.AllNonLastUpgraded() {
		t.Error("should not be all upgraded with crew pending")
	}

	// Clear crew pending
	tracker.pendingByRole["crew"] = nil
	if !tracker.AllNonLastUpgraded() {
		t.Error("should be all upgraded after clearing crew pending")
	}

	// Add witness pending (should not affect the check)
	tracker.RegisterDrift("gt-gastown-witness-main", "witness")
	if !tracker.AllNonLastUpgraded() {
		t.Error("witness pending should not affect AllNonLastUpgraded")
	}
}

func TestUpgradeTracker_CleanStaleUpgrades(t *testing.T) {
	tracker := NewUpgradeTracker(testLogger())

	// Add a stale upgrade
	tracker.upgrading["gt-gastown-crew-toolbox"] = time.Now().Add(-15 * time.Minute)

	// Clean with 10 minute timeout
	tracker.CleanStaleUpgrades(10 * time.Minute)

	if _, exists := tracker.upgrading["gt-gastown-crew-toolbox"]; exists {
		t.Error("stale upgrade should have been cleaned")
	}
}

func TestUpgradeTracker_ResetClearsPending(t *testing.T) {
	tracker := NewUpgradeTracker(testLogger())

	tracker.RegisterDrift("gt-gastown-crew-toolbox", "crew")
	if len(tracker.pendingByRole["crew"]) != 1 {
		t.Error("should have 1 pending")
	}

	tracker.Reset()
	if len(tracker.pendingByRole["crew"]) != 0 {
		t.Error("reset should clear pending")
	}
}

func TestIsPodReady(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want bool
	}{
		{
			name: "running and ready",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodReady, Status: corev1.ConditionTrue},
					},
				},
			},
			want: true,
		},
		{
			name: "running but not ready",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodReady, Status: corev1.ConditionFalse},
					},
				},
			},
			want: false,
		},
		{
			name: "pending",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Status:     corev1.PodStatus{Phase: corev1.PodPending},
			},
			want: false,
		},
		{
			name: "failed",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{Phase: corev1.PodFailed},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsPodReady(tt.pod)
			if got != tt.want {
				t.Errorf("IsPodReady() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUpgradeTracker_DifferentRolesIndependent(t *testing.T) {
	tracker := NewUpgradeTracker(testLogger())

	tracker.RegisterDrift("gt-gastown-crew-toolbox", "crew")
	tracker.RegisterDrift("gt-gastown-refinery-main", "refinery")

	// Both should be independently upgradable
	if !tracker.CanUpgrade("gt-gastown-crew-toolbox", "crew") {
		t.Error("crew should be upgradable independently")
	}
	if !tracker.CanUpgrade("gt-gastown-refinery-main", "refinery") {
		t.Error("refinery should be upgradable independently")
	}

	// Mark crew upgrading - shouldn't affect refinery
	tracker.MarkUpgrading("gt-gastown-crew-toolbox")
	if !tracker.CanUpgrade("gt-gastown-refinery-main", "refinery") {
		t.Error("refinery should not be blocked by crew upgrade")
	}
}
