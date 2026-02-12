package podmanager

import (
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// ResourceCaps holds maximum resource limits for sidecars.
// Parsed from controller config (SidecarMaxCPU, SidecarMaxMemory).
type ResourceCaps struct {
	MaxCPU    resource.Quantity
	MaxMemory resource.Quantity
}

// ParseResourceCaps parses string-based resource caps into quantities.
// Empty strings result in zero-value quantities (no cap enforced).
func ParseResourceCaps(maxCPU, maxMemory string) ResourceCaps {
	caps := ResourceCaps{}
	if maxCPU != "" {
		caps.MaxCPU = resource.MustParse(maxCPU)
	}
	if maxMemory != "" {
		caps.MaxMemory = resource.MustParse(maxMemory)
	}
	return caps
}

// ClampResources enforces resource caps on the given requirements.
// If a limit exceeds the cap, it is clamped down and a warning is logged.
// Requests are also clamped if they exceed the cap (requests should never exceed limits).
// Zero-value caps are treated as "no cap".
func ClampResources(reqs corev1.ResourceRequirements, caps ResourceCaps, logger *slog.Logger) corev1.ResourceRequirements {
	if caps.MaxCPU.IsZero() && caps.MaxMemory.IsZero() {
		return reqs
	}

	result := reqs.DeepCopy()

	if !caps.MaxCPU.IsZero() {
		clampQuantity(result.Limits, corev1.ResourceCPU, caps.MaxCPU, "cpu limit", logger)
		clampQuantity(result.Requests, corev1.ResourceCPU, caps.MaxCPU, "cpu request", logger)
	}

	if !caps.MaxMemory.IsZero() {
		clampQuantity(result.Limits, corev1.ResourceMemory, caps.MaxMemory, "memory limit", logger)
		clampQuantity(result.Requests, corev1.ResourceMemory, caps.MaxMemory, "memory request", logger)
	}

	return *result
}

// clampQuantity clamps a single resource in a resource list to the given max.
func clampQuantity(list corev1.ResourceList, name corev1.ResourceName, max resource.Quantity, label string, logger *slog.Logger) {
	if list == nil {
		return
	}
	val, ok := list[name]
	if !ok {
		return
	}
	if val.Cmp(max) > 0 {
		logger.Warn("clamping sidecar resource to cap",
			"resource", label,
			"requested", val.String(),
			"cap", max.String(),
		)
		list[name] = max.DeepCopy()
	}
}
