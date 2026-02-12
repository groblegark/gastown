package podmanager

import (
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// PodDefaults holds default pod template values that can be overridden
// at each level of the merge hierarchy:
//   GasTown defaults < Rig overrides < Role overrides < AgentPool template
type PodDefaults struct {
	Image              string
	Resources          *corev1.ResourceRequirements
	ServiceAccountName string
	NodeSelector       map[string]string
	Tolerations        []corev1.Toleration
	Env                map[string]string
	SecretEnv          []SecretEnvSource
	ConfigMapName      string
	WorkspaceStorage   *WorkspaceStorageSpec
	CoopSidecar        *CoopSidecarSpec
	ToolchainSidecar   *ToolchainSidecarSpec
}

// MergePodDefaults merges an override layer onto a base, returning a new PodDefaults.
// Non-zero override values replace base values. Maps and slices are merged (override wins).
func MergePodDefaults(base, override *PodDefaults) *PodDefaults {
	if base == nil && override == nil {
		return &PodDefaults{}
	}
	if base == nil {
		cp := *override
		return &cp
	}
	if override == nil {
		cp := *base
		return &cp
	}

	result := *base

	if override.Image != "" {
		result.Image = override.Image
	}
	if override.Resources != nil {
		result.Resources = mergeResources(result.Resources, override.Resources)
	}
	if override.ServiceAccountName != "" {
		result.ServiceAccountName = override.ServiceAccountName
	}
	if len(override.NodeSelector) > 0 {
		result.NodeSelector = mergeMaps(result.NodeSelector, override.NodeSelector)
	}
	if len(override.Tolerations) > 0 {
		result.Tolerations = override.Tolerations
	}
	if len(override.Env) > 0 {
		result.Env = mergeMaps(result.Env, override.Env)
	}
	if len(override.SecretEnv) > 0 {
		result.SecretEnv = override.SecretEnv
	}
	if override.ConfigMapName != "" {
		result.ConfigMapName = override.ConfigMapName
	}
	if override.WorkspaceStorage != nil {
		result.WorkspaceStorage = override.WorkspaceStorage
	}
	if override.CoopSidecar != nil {
		result.CoopSidecar = override.CoopSidecar
	}
	if override.ToolchainSidecar != nil {
		result.ToolchainSidecar = override.ToolchainSidecar
	}

	return &result
}

// ApplyDefaults applies PodDefaults to an AgentPodSpec, filling in
// any fields that aren't already set on the spec.
func ApplyDefaults(spec *AgentPodSpec, defaults *PodDefaults) {
	if defaults == nil {
		return
	}

	if spec.Image == "" && defaults.Image != "" {
		spec.Image = defaults.Image
	}
	if spec.Resources == nil && defaults.Resources != nil {
		spec.Resources = defaults.Resources
	}
	if spec.ServiceAccountName == "" && defaults.ServiceAccountName != "" {
		spec.ServiceAccountName = defaults.ServiceAccountName
	}
	if len(spec.NodeSelector) == 0 && len(defaults.NodeSelector) > 0 {
		spec.NodeSelector = defaults.NodeSelector
	}
	if len(spec.Tolerations) == 0 && len(defaults.Tolerations) > 0 {
		spec.Tolerations = defaults.Tolerations
	}
	if spec.ConfigMapName == "" && defaults.ConfigMapName != "" {
		spec.ConfigMapName = defaults.ConfigMapName
	}
	if spec.WorkspaceStorage == nil && defaults.WorkspaceStorage != nil {
		spec.WorkspaceStorage = defaults.WorkspaceStorage
	}
	if spec.CoopSidecar == nil && defaults.CoopSidecar != nil {
		spec.CoopSidecar = defaults.CoopSidecar
	}
	if spec.ToolchainSidecar == nil && defaults.ToolchainSidecar != nil {
		spec.ToolchainSidecar = defaults.ToolchainSidecar
	}

	// Merge env maps (spec values take precedence over defaults).
	if len(defaults.Env) > 0 {
		if spec.Env == nil {
			spec.Env = make(map[string]string)
		}
		for k, v := range defaults.Env {
			if _, exists := spec.Env[k]; !exists {
				spec.Env[k] = v
			}
		}
	}

	// Append default secret env sources that aren't already in the spec.
	if len(defaults.SecretEnv) > 0 {
		existing := make(map[string]bool)
		for _, se := range spec.SecretEnv {
			existing[se.EnvName] = true
		}
		for _, se := range defaults.SecretEnv {
			if !existing[se.EnvName] {
				spec.SecretEnv = append(spec.SecretEnv, se)
			}
		}
	}
}

// mergeResources merges resource requirements, with override values taking precedence.
func mergeResources(base, override *corev1.ResourceRequirements) *corev1.ResourceRequirements {
	if base == nil {
		cp := *override
		return &cp
	}

	result := &corev1.ResourceRequirements{
		Requests: mergeResourceList(base.Requests, override.Requests),
		Limits:   mergeResourceList(base.Limits, override.Limits),
	}
	return result
}

func mergeResourceList(base, override corev1.ResourceList) corev1.ResourceList {
	if base == nil && override == nil {
		return nil
	}
	result := make(corev1.ResourceList)
	for k, v := range base {
		result[k] = v
	}
	for k, v := range override {
		result[k] = v
	}
	return result
}

func mergeMaps(base, override map[string]string) map[string]string {
	result := make(map[string]string)
	for k, v := range base {
		result[k] = v
	}
	for k, v := range override {
		result[k] = v
	}
	return result
}

// SidecarProfile is a named toolchain sidecar preset.
type SidecarProfile struct {
	Name      string
	Image     string
	Resources *corev1.ResourceRequirements
}

// ProfileRegistry maps profile names to sidecar specs.
type ProfileRegistry struct {
	profiles map[string]SidecarProfile
}

// NewProfileRegistry creates a registry from a map of profiles.
func NewProfileRegistry(profiles map[string]SidecarProfile) *ProfileRegistry {
	return &ProfileRegistry{profiles: profiles}
}

// Resolve resolves bead metadata into a ToolchainSidecarSpec.
// Returns nil if no sidecar is requested.
func (r *ProfileRegistry) Resolve(meta map[string]string) *ToolchainSidecarSpec {
	// Custom image takes precedence over profile.
	if img := meta["sidecar_image"]; img != "" {
		spec := &ToolchainSidecarSpec{Image: img}
		// Apply resource overrides from metadata if present.
		spec.Resources = parseResourceOverrides(meta)
		return spec
	}
	// Named profile lookup.
	if name := meta["sidecar_profile"]; name != "" && name != "none" {
		if p, ok := r.profiles[name]; ok {
			spec := &ToolchainSidecarSpec{
				Profile:   name,
				Image:     p.Image,
				Resources: p.Resources,
			}
			// Allow metadata to override profile resource defaults.
			if overrides := parseResourceOverrides(meta); overrides != nil {
				spec.Resources = overrides
			}
			return spec
		}
	}
	return nil
}

// HasProfile returns true if the named profile exists.
func (r *ProfileRegistry) HasProfile(name string) bool {
	_, ok := r.profiles[name]
	return ok
}

// ListProfiles returns all registered profile names.
func (r *ProfileRegistry) ListProfiles() []string {
	names := make([]string, 0, len(r.profiles))
	for name := range r.profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// parseResourceOverrides reads sidecar_resources_cpu and sidecar_resources_memory
// from bead metadata and returns ResourceRequirements, or nil if neither is set.
func parseResourceOverrides(meta map[string]string) *corev1.ResourceRequirements {
	cpu := meta["sidecar_resources_cpu"]
	mem := meta["sidecar_resources_memory"]
	if cpu == "" && mem == "" {
		return nil
	}
	res := &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}
	if cpu != "" {
		q := resource.MustParse(cpu)
		res.Requests[corev1.ResourceCPU] = q
		res.Limits[corev1.ResourceCPU] = q
	}
	if mem != "" {
		q := resource.MustParse(mem)
		res.Requests[corev1.ResourceMemory] = q
		res.Limits[corev1.ResourceMemory] = q
	}
	return res
}

// DefaultPodDefaultsForRole returns sensible defaults for a given role.
func DefaultPodDefaultsForRole(role string) *PodDefaults {
	defaults := &PodDefaults{
		Resources: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(DefaultCPURequest),
				corev1.ResourceMemory: resource.MustParse(DefaultMemoryRequest),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(DefaultCPULimit),
				corev1.ResourceMemory: resource.MustParse(DefaultMemoryLimit),
			},
		},
		// Agent image is x86-only; pin to amd64 nodes.
		NodeSelector: map[string]string{
			"kubernetes.io/arch": "amd64",
		},
	}

	switch role {
	case "crew":
		// Crew pods get persistent workspace storage.
		defaults.WorkspaceStorage = &WorkspaceStorageSpec{
			Size:             "10Gi",
			StorageClassName: "gp2",
		}
	case "polecat":
		// Polecats use EmptyDir (no WorkspaceStorage).
	case "witness", "refinery":
		// Singletons get persistent storage for state.
		defaults.WorkspaceStorage = &WorkspaceStorageSpec{
			Size:             "5Gi",
			StorageClassName: "gp2",
		}
	case "mayor", "deacon":
		// Town-level singletons get persistent storage.
		// GT_SCOPE and BD_ACTOR are set in buildEnvVars (not here) to avoid
		// duplicate env vars when ApplyDefaults merges the Env map.
		defaults.WorkspaceStorage = &WorkspaceStorageSpec{
			Size:             "10Gi",
			StorageClassName: "gp2",
		}
	}

	return defaults
}
