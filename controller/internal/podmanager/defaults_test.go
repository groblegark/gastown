package podmanager

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestMergePodDefaults_BothNil(t *testing.T) {
	result := MergePodDefaults(nil, nil)
	if result == nil {
		t.Fatal("MergePodDefaults(nil, nil) should return non-nil")
	}
}

func TestMergePodDefaults_BaseOnly(t *testing.T) {
	base := &PodDefaults{Image: "base:latest"}
	result := MergePodDefaults(base, nil)
	if result.Image != "base:latest" {
		t.Errorf("Image = %q, want %q", result.Image, "base:latest")
	}
}

func TestMergePodDefaults_OverrideOnly(t *testing.T) {
	override := &PodDefaults{Image: "override:latest"}
	result := MergePodDefaults(nil, override)
	if result.Image != "override:latest" {
		t.Errorf("Image = %q, want %q", result.Image, "override:latest")
	}
}

func TestMergePodDefaults_OverrideWins(t *testing.T) {
	base := &PodDefaults{
		Image:              "base:latest",
		ServiceAccountName: "base-sa",
		ConfigMapName:      "base-config",
	}
	override := &PodDefaults{
		Image: "override:v2",
	}

	result := MergePodDefaults(base, override)
	if result.Image != "override:v2" {
		t.Errorf("Image = %q, want %q", result.Image, "override:v2")
	}
	if result.ServiceAccountName != "base-sa" {
		t.Errorf("ServiceAccountName = %q, want %q (preserved from base)", result.ServiceAccountName, "base-sa")
	}
	if result.ConfigMapName != "base-config" {
		t.Errorf("ConfigMapName = %q, want %q (preserved from base)", result.ConfigMapName, "base-config")
	}
}

func TestMergePodDefaults_EnvMerge(t *testing.T) {
	base := &PodDefaults{
		Env: map[string]string{"A": "1", "B": "2"},
	}
	override := &PodDefaults{
		Env: map[string]string{"B": "override", "C": "3"},
	}

	result := MergePodDefaults(base, override)
	if result.Env["A"] != "1" {
		t.Errorf("Env[A] = %q, want %q", result.Env["A"], "1")
	}
	if result.Env["B"] != "override" {
		t.Errorf("Env[B] = %q, want %q (override wins)", result.Env["B"], "override")
	}
	if result.Env["C"] != "3" {
		t.Errorf("Env[C] = %q, want %q", result.Env["C"], "3")
	}
}

func TestMergePodDefaults_ResourcesMerge(t *testing.T) {
	base := &PodDefaults{
		Resources: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
		},
	}
	override := &PodDefaults{
		Resources: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("1"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("4"),
			},
		},
	}

	result := MergePodDefaults(base, override)
	if result.Resources == nil {
		t.Fatal("Resources should not be nil")
	}

	// CPU request should be overridden to "1".
	cpuReq := result.Resources.Requests[corev1.ResourceCPU]
	if cpuReq.String() != "1" {
		t.Errorf("CPU request = %s, want 1", cpuReq.String())
	}

	// Memory request preserved from base.
	memReq := result.Resources.Requests[corev1.ResourceMemory]
	if memReq.String() != "1Gi" {
		t.Errorf("Memory request = %s, want 1Gi (from base)", memReq.String())
	}

	// Limits added from override.
	cpuLimit := result.Resources.Limits[corev1.ResourceCPU]
	if cpuLimit.String() != "4" {
		t.Errorf("CPU limit = %s, want 4", cpuLimit.String())
	}
}

func TestMergePodDefaults_WorkspaceStorageOverride(t *testing.T) {
	base := &PodDefaults{
		WorkspaceStorage: &WorkspaceStorageSpec{Size: "5Gi", StorageClassName: "gp2"},
	}
	override := &PodDefaults{
		WorkspaceStorage: &WorkspaceStorageSpec{Size: "20Gi", StorageClassName: "gp3"},
	}

	result := MergePodDefaults(base, override)
	if result.WorkspaceStorage.Size != "20Gi" {
		t.Errorf("WorkspaceStorage.Size = %q, want %q", result.WorkspaceStorage.Size, "20Gi")
	}
	if result.WorkspaceStorage.StorageClassName != "gp3" {
		t.Errorf("WorkspaceStorage.StorageClassName = %q, want %q", result.WorkspaceStorage.StorageClassName, "gp3")
	}
}

func TestMergePodDefaults_TolerationsOverride(t *testing.T) {
	base := &PodDefaults{
		Tolerations: []corev1.Toleration{
			{Key: "old", Value: "v1"},
		},
	}
	override := &PodDefaults{
		Tolerations: []corev1.Toleration{
			{Key: "new", Value: "v2"},
		},
	}

	result := MergePodDefaults(base, override)
	if len(result.Tolerations) != 1 {
		t.Fatalf("got %d tolerations, want 1", len(result.Tolerations))
	}
	if result.Tolerations[0].Key != "new" {
		t.Errorf("toleration key = %q, want %q (override replaces)", result.Tolerations[0].Key, "new")
	}
}

func TestMergePodDefaults_SecretEnvOverride(t *testing.T) {
	base := &PodDefaults{
		SecretEnv: []SecretEnvSource{
			{EnvName: "OLD_SECRET", SecretName: "s1", SecretKey: "k1"},
		},
	}
	override := &PodDefaults{
		SecretEnv: []SecretEnvSource{
			{EnvName: "NEW_SECRET", SecretName: "s2", SecretKey: "k2"},
		},
	}

	result := MergePodDefaults(base, override)
	if len(result.SecretEnv) != 1 {
		t.Fatalf("got %d secret envs, want 1", len(result.SecretEnv))
	}
	if result.SecretEnv[0].EnvName != "NEW_SECRET" {
		t.Errorf("SecretEnv[0].EnvName = %q, want %q", result.SecretEnv[0].EnvName, "NEW_SECRET")
	}
}

func TestApplyDefaults_FillsMissing(t *testing.T) {
	spec := &AgentPodSpec{
		Rig: "gastown", Role: "polecat", AgentName: "test",
		Namespace: "gastown",
	}
	defaults := &PodDefaults{
		Image:              "default-agent:latest",
		ServiceAccountName: "default-sa",
		ConfigMapName:      "default-config",
		Env:                map[string]string{"DEFAULT_VAR": "val"},
		SecretEnv: []SecretEnvSource{
			{EnvName: "API_KEY", SecretName: "keys", SecretKey: "api"},
		},
	}

	ApplyDefaults(spec, defaults)

	if spec.Image != "default-agent:latest" {
		t.Errorf("Image = %q, want %q", spec.Image, "default-agent:latest")
	}
	if spec.ServiceAccountName != "default-sa" {
		t.Errorf("ServiceAccountName = %q, want %q", spec.ServiceAccountName, "default-sa")
	}
	if spec.ConfigMapName != "default-config" {
		t.Errorf("ConfigMapName = %q, want %q", spec.ConfigMapName, "default-config")
	}
	if spec.Env["DEFAULT_VAR"] != "val" {
		t.Errorf("Env[DEFAULT_VAR] = %q, want %q", spec.Env["DEFAULT_VAR"], "val")
	}
	if len(spec.SecretEnv) != 1 {
		t.Fatalf("got %d secret envs, want 1", len(spec.SecretEnv))
	}
}

func TestApplyDefaults_SpecOverridesDefaults(t *testing.T) {
	spec := &AgentPodSpec{
		Rig: "gastown", Role: "crew", AgentName: "test",
		Image:              "custom:v1",
		Namespace:          "gastown",
		ServiceAccountName: "custom-sa",
		Env:                map[string]string{"SHARED": "spec-value"},
		SecretEnv: []SecretEnvSource{
			{EnvName: "API_KEY", SecretName: "custom-keys", SecretKey: "api"},
		},
	}
	defaults := &PodDefaults{
		Image:              "default:latest",
		ServiceAccountName: "default-sa",
		Env:                map[string]string{"SHARED": "default-value", "DEFAULT_ONLY": "val"},
		SecretEnv: []SecretEnvSource{
			{EnvName: "API_KEY", SecretName: "default-keys", SecretKey: "api"},
			{EnvName: "GIT_TOKEN", SecretName: "git", SecretKey: "token"},
		},
	}

	ApplyDefaults(spec, defaults)

	if spec.Image != "custom:v1" {
		t.Errorf("Image = %q, want %q (spec should win)", spec.Image, "custom:v1")
	}
	if spec.ServiceAccountName != "custom-sa" {
		t.Errorf("ServiceAccountName = %q, want %q (spec should win)", spec.ServiceAccountName, "custom-sa")
	}
	if spec.Env["SHARED"] != "spec-value" {
		t.Errorf("Env[SHARED] = %q, want %q (spec should win)", spec.Env["SHARED"], "spec-value")
	}
	if spec.Env["DEFAULT_ONLY"] != "val" {
		t.Errorf("Env[DEFAULT_ONLY] = %q, want %q (from defaults)", spec.Env["DEFAULT_ONLY"], "val")
	}

	// API_KEY from spec should be kept; GIT_TOKEN from defaults appended.
	if len(spec.SecretEnv) != 2 {
		t.Fatalf("got %d secret envs, want 2", len(spec.SecretEnv))
	}
	// First should be spec's API_KEY with custom-keys.
	if spec.SecretEnv[0].SecretName != "custom-keys" {
		t.Errorf("SecretEnv[0].SecretName = %q, want %q", spec.SecretEnv[0].SecretName, "custom-keys")
	}
}

func TestApplyDefaults_NilDefaults(t *testing.T) {
	spec := &AgentPodSpec{
		Rig: "gastown", Role: "polecat", AgentName: "test",
		Image: "original:latest", Namespace: "gastown",
	}
	ApplyDefaults(spec, nil)
	if spec.Image != "original:latest" {
		t.Errorf("Image changed from %q", "original:latest")
	}
}

func TestDefaultPodDefaultsForRole_Polecat(t *testing.T) {
	d := DefaultPodDefaultsForRole("polecat")
	if d.WorkspaceStorage != nil {
		t.Error("polecats should not have workspace storage")
	}
	if d.Resources == nil {
		t.Fatal("Resources should not be nil")
	}
}

func TestDefaultPodDefaultsForRole_Crew(t *testing.T) {
	d := DefaultPodDefaultsForRole("crew")
	if d.WorkspaceStorage == nil {
		t.Fatal("crew should have workspace storage")
	}
	if d.WorkspaceStorage.Size != "10Gi" {
		t.Errorf("workspace size = %q, want %q", d.WorkspaceStorage.Size, "10Gi")
	}
}

func TestDefaultPodDefaultsForRole_Witness(t *testing.T) {
	d := DefaultPodDefaultsForRole("witness")
	if d.WorkspaceStorage == nil {
		t.Fatal("witness should have workspace storage")
	}
	if d.WorkspaceStorage.Size != "5Gi" {
		t.Errorf("workspace size = %q, want %q", d.WorkspaceStorage.Size, "5Gi")
	}
}

func TestDefaultPodDefaultsForRole_Mayor(t *testing.T) {
	d := DefaultPodDefaultsForRole("mayor")
	if d.WorkspaceStorage == nil {
		t.Fatal("mayor should have workspace storage")
	}
	if d.WorkspaceStorage.Size != "10Gi" {
		t.Errorf("workspace size = %q, want %q", d.WorkspaceStorage.Size, "10Gi")
	}
	if d.WorkspaceStorage.StorageClassName != "gp2" {
		t.Errorf("storage class = %q, want %q", d.WorkspaceStorage.StorageClassName, "gp2")
	}
	if d.Resources == nil {
		t.Fatal("Resources should not be nil")
	}
	// GT_SCOPE and BD_ACTOR are set in buildEnvVars, not defaults.
	if d.NodeSelector["kubernetes.io/arch"] != "amd64" {
		t.Errorf("NodeSelector[arch] = %q, want %q", d.NodeSelector["kubernetes.io/arch"], "amd64")
	}
}

func TestDefaultPodDefaultsForRole_Deacon(t *testing.T) {
	d := DefaultPodDefaultsForRole("deacon")
	if d.WorkspaceStorage == nil {
		t.Fatal("deacon should have workspace storage")
	}
	if d.WorkspaceStorage.Size != "10Gi" {
		t.Errorf("workspace size = %q, want %q", d.WorkspaceStorage.Size, "10Gi")
	}
	if d.WorkspaceStorage.StorageClassName != "gp2" {
		t.Errorf("storage class = %q, want %q", d.WorkspaceStorage.StorageClassName, "gp2")
	}
	if d.Resources == nil {
		t.Fatal("Resources should not be nil")
	}
	// GT_SCOPE and BD_ACTOR are set in buildEnvVars, not defaults.
	if d.NodeSelector["kubernetes.io/arch"] != "amd64" {
		t.Errorf("NodeSelector[arch] = %q, want %q", d.NodeSelector["kubernetes.io/arch"], "amd64")
	}
}

func TestDefaultPodDefaultsForRole_UnknownRole(t *testing.T) {
	d := DefaultPodDefaultsForRole("unknown")
	if d.WorkspaceStorage != nil {
		t.Error("unknown role should not have workspace storage")
	}
	if len(d.Env) != 0 {
		t.Errorf("unknown role should have no env, got %v", d.Env)
	}
	if d.Resources == nil {
		t.Fatal("Resources should still be set for unknown roles")
	}
}

func TestMergePodDefaults_CoopSidecarOverride(t *testing.T) {
	base := &PodDefaults{
		CoopSidecar: &CoopSidecarSpec{Image: "coop:v1"},
	}
	override := &PodDefaults{
		CoopSidecar: &CoopSidecarSpec{Image: "coop:v2"},
	}

	result := MergePodDefaults(base, override)
	if result.CoopSidecar.Image != "coop:v2" {
		t.Errorf("CoopSidecar.Image = %q, want %q", result.CoopSidecar.Image, "coop:v2")
	}
}

func TestMergePodDefaults_ToolchainSidecar(t *testing.T) {
	base := &PodDefaults{
		ToolchainSidecar: &ToolchainSidecarSpec{
			Profile: "toolchain-full",
			Image:   "toolchain:v1",
		},
	}
	override := &PodDefaults{
		ToolchainSidecar: &ToolchainSidecarSpec{
			Profile: "toolchain-minimal",
			Image:   "toolchain:minimal",
		},
	}

	result := MergePodDefaults(base, override)
	if result.ToolchainSidecar == nil {
		t.Fatal("ToolchainSidecar should not be nil")
	}
	if result.ToolchainSidecar.Profile != "toolchain-minimal" {
		t.Errorf("ToolchainSidecar.Profile = %q, want %q", result.ToolchainSidecar.Profile, "toolchain-minimal")
	}
	if result.ToolchainSidecar.Image != "toolchain:minimal" {
		t.Errorf("ToolchainSidecar.Image = %q, want %q", result.ToolchainSidecar.Image, "toolchain:minimal")
	}
}

func TestMergePodDefaults_ToolchainSidecarPreservedFromBase(t *testing.T) {
	base := &PodDefaults{
		ToolchainSidecar: &ToolchainSidecarSpec{
			Profile: "toolchain-full",
			Image:   "toolchain:v1",
		},
	}
	override := &PodDefaults{
		Image: "override:v2",
	}

	result := MergePodDefaults(base, override)
	if result.ToolchainSidecar == nil {
		t.Fatal("ToolchainSidecar should be preserved from base")
	}
	if result.ToolchainSidecar.Profile != "toolchain-full" {
		t.Errorf("ToolchainSidecar.Profile = %q, want %q", result.ToolchainSidecar.Profile, "toolchain-full")
	}
}

func TestApplyDefaults_ToolchainSidecar(t *testing.T) {
	spec := &AgentPodSpec{
		Rig: "gastown", Role: "crew", AgentName: "test",
		Image: "agent:latest", Namespace: "gastown",
	}
	defaults := &PodDefaults{
		ToolchainSidecar: &ToolchainSidecarSpec{
			Profile: "toolchain-full",
			Image:   "toolchain:latest",
		},
	}

	ApplyDefaults(spec, defaults)

	if spec.ToolchainSidecar == nil {
		t.Fatal("ToolchainSidecar should be applied from defaults")
	}
	if spec.ToolchainSidecar.Profile != "toolchain-full" {
		t.Errorf("ToolchainSidecar.Profile = %q, want %q", spec.ToolchainSidecar.Profile, "toolchain-full")
	}
}

func TestApplyDefaults_ToolchainSidecarSpecWins(t *testing.T) {
	spec := &AgentPodSpec{
		Rig: "gastown", Role: "crew", AgentName: "test",
		Image: "agent:latest", Namespace: "gastown",
		ToolchainSidecar: &ToolchainSidecarSpec{
			Image: "custom:v1",
		},
	}
	defaults := &PodDefaults{
		ToolchainSidecar: &ToolchainSidecarSpec{
			Profile: "toolchain-full",
			Image:   "toolchain:latest",
		},
	}

	ApplyDefaults(spec, defaults)

	if spec.ToolchainSidecar.Image != "custom:v1" {
		t.Errorf("ToolchainSidecar.Image = %q, want %q (spec should win)", spec.ToolchainSidecar.Image, "custom:v1")
	}
}

func TestProfileRegistry_Resolve_CustomImage(t *testing.T) {
	reg := NewProfileRegistry(map[string]SidecarProfile{
		"toolchain-full": {Name: "toolchain-full", Image: "toolchain:latest"},
	})

	meta := map[string]string{
		"sidecar_image": "custom-image:v1",
	}
	spec := reg.Resolve(meta)
	if spec == nil {
		t.Fatal("Resolve should return non-nil for custom image")
	}
	if spec.Image != "custom-image:v1" {
		t.Errorf("Image = %q, want %q", spec.Image, "custom-image:v1")
	}
	if spec.Profile != "" {
		t.Errorf("Profile = %q, want empty for custom image", spec.Profile)
	}
}

func TestProfileRegistry_Resolve_NamedProfile(t *testing.T) {
	profileRes := &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("250m"),
		},
	}
	reg := NewProfileRegistry(map[string]SidecarProfile{
		"toolchain-full": {
			Name:      "toolchain-full",
			Image:     "toolchain:latest",
			Resources: profileRes,
		},
	})

	meta := map[string]string{
		"sidecar_profile": "toolchain-full",
	}
	spec := reg.Resolve(meta)
	if spec == nil {
		t.Fatal("Resolve should return non-nil for named profile")
	}
	if spec.Profile != "toolchain-full" {
		t.Errorf("Profile = %q, want %q", spec.Profile, "toolchain-full")
	}
	if spec.Image != "toolchain:latest" {
		t.Errorf("Image = %q, want %q", spec.Image, "toolchain:latest")
	}
	if spec.Resources != profileRes {
		t.Error("Resources should match profile defaults")
	}
}

func TestProfileRegistry_Resolve_ProfileWithOverrides(t *testing.T) {
	profileRes := &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("250m"),
		},
	}
	reg := NewProfileRegistry(map[string]SidecarProfile{
		"toolchain-full": {
			Name:      "toolchain-full",
			Image:     "toolchain:latest",
			Resources: profileRes,
		},
	})

	meta := map[string]string{
		"sidecar_profile":          "toolchain-full",
		"sidecar_resources_cpu":    "1",
		"sidecar_resources_memory": "2Gi",
	}
	spec := reg.Resolve(meta)
	if spec == nil {
		t.Fatal("Resolve should return non-nil")
	}
	if spec.Resources == nil {
		t.Fatal("Resources should not be nil when overrides present")
	}
	cpuReq := spec.Resources.Requests[corev1.ResourceCPU]
	if cpuReq.String() != "1" {
		t.Errorf("CPU request = %s, want 1 (override)", cpuReq.String())
	}
	memReq := spec.Resources.Requests[corev1.ResourceMemory]
	if memReq.String() != "2Gi" {
		t.Errorf("Memory request = %s, want 2Gi (override)", memReq.String())
	}
}

func TestProfileRegistry_Resolve_None(t *testing.T) {
	reg := NewProfileRegistry(map[string]SidecarProfile{
		"toolchain-full": {Name: "toolchain-full", Image: "toolchain:latest"},
	})

	meta := map[string]string{
		"sidecar_profile": "none",
	}
	spec := reg.Resolve(meta)
	if spec != nil {
		t.Errorf("Resolve should return nil for profile=none, got %+v", spec)
	}
}

func TestProfileRegistry_Resolve_EmptyMetadata(t *testing.T) {
	reg := NewProfileRegistry(map[string]SidecarProfile{
		"toolchain-full": {Name: "toolchain-full", Image: "toolchain:latest"},
	})

	spec := reg.Resolve(map[string]string{})
	if spec != nil {
		t.Errorf("Resolve should return nil for empty metadata, got %+v", spec)
	}
}

func TestProfileRegistry_Resolve_UnknownProfile(t *testing.T) {
	reg := NewProfileRegistry(map[string]SidecarProfile{
		"toolchain-full": {Name: "toolchain-full", Image: "toolchain:latest"},
	})

	meta := map[string]string{
		"sidecar_profile": "nonexistent",
	}
	spec := reg.Resolve(meta)
	if spec != nil {
		t.Errorf("Resolve should return nil for unknown profile, got %+v", spec)
	}
}

func TestProfileRegistry_Resolve_CustomImageWithResourceOverrides(t *testing.T) {
	reg := NewProfileRegistry(map[string]SidecarProfile{})

	meta := map[string]string{
		"sidecar_image":            "custom:v1",
		"sidecar_resources_cpu":    "500m",
		"sidecar_resources_memory": "1Gi",
	}
	spec := reg.Resolve(meta)
	if spec == nil {
		t.Fatal("Resolve should return non-nil for custom image")
	}
	if spec.Resources == nil {
		t.Fatal("Resources should not be nil with overrides")
	}
	cpuReq := spec.Resources.Requests[corev1.ResourceCPU]
	if cpuReq.String() != "500m" {
		t.Errorf("CPU request = %s, want 500m", cpuReq.String())
	}
}

func TestProfileRegistry_Resolve_CustomImageOverridesProfile(t *testing.T) {
	reg := NewProfileRegistry(map[string]SidecarProfile{
		"toolchain-full": {Name: "toolchain-full", Image: "toolchain:latest"},
	})

	// Both sidecar_image and sidecar_profile set — image wins.
	meta := map[string]string{
		"sidecar_image":   "custom:v1",
		"sidecar_profile": "toolchain-full",
	}
	spec := reg.Resolve(meta)
	if spec == nil {
		t.Fatal("Resolve should return non-nil")
	}
	if spec.Image != "custom:v1" {
		t.Errorf("Image = %q, want %q (custom image should win)", spec.Image, "custom:v1")
	}
	if spec.Profile != "" {
		t.Errorf("Profile = %q, want empty (custom image, not profile)", spec.Profile)
	}
}

func TestProfileRegistry_HasProfile(t *testing.T) {
	reg := NewProfileRegistry(map[string]SidecarProfile{
		"toolchain-full":    {Name: "toolchain-full", Image: "toolchain:latest"},
		"toolchain-minimal": {Name: "toolchain-minimal", Image: "toolchain:minimal"},
	})

	if !reg.HasProfile("toolchain-full") {
		t.Error("HasProfile(toolchain-full) should be true")
	}
	if !reg.HasProfile("toolchain-minimal") {
		t.Error("HasProfile(toolchain-minimal) should be true")
	}
	if reg.HasProfile("nonexistent") {
		t.Error("HasProfile(nonexistent) should be false")
	}
}

func TestProfileRegistry_ListProfiles(t *testing.T) {
	reg := NewProfileRegistry(map[string]SidecarProfile{
		"toolchain-full":    {Name: "toolchain-full", Image: "toolchain:latest"},
		"toolchain-minimal": {Name: "toolchain-minimal", Image: "toolchain:minimal"},
	})

	names := reg.ListProfiles()
	if len(names) != 2 {
		t.Fatalf("got %d profiles, want 2", len(names))
	}
	// Should be sorted.
	if names[0] != "toolchain-full" {
		t.Errorf("names[0] = %q, want %q", names[0], "toolchain-full")
	}
	if names[1] != "toolchain-minimal" {
		t.Errorf("names[1] = %q, want %q", names[1], "toolchain-minimal")
	}
}

func TestMergePodDefaults_ThreeLayerHierarchy(t *testing.T) {
	// Simulate: GasTown defaults < Rig overrides < Role overrides
	town := &PodDefaults{
		Image:              "town-agent:latest",
		ServiceAccountName: "town-sa",
		Env:                map[string]string{"TOWN": "yes", "SHARED": "town"},
	}
	rig := &PodDefaults{
		Env: map[string]string{"SHARED": "rig", "RIG": "gastown"},
	}
	role := &PodDefaults{
		Image: "role-agent:v2",
		Env:   map[string]string{"SHARED": "role"},
	}

	merged := MergePodDefaults(MergePodDefaults(town, rig), role)

	if merged.Image != "role-agent:v2" {
		t.Errorf("Image = %q, want %q (role wins)", merged.Image, "role-agent:v2")
	}
	if merged.ServiceAccountName != "town-sa" {
		t.Errorf("ServiceAccountName = %q, want %q (preserved from town)", merged.ServiceAccountName, "town-sa")
	}
	if merged.Env["SHARED"] != "role" {
		t.Errorf("Env[SHARED] = %q, want %q (role wins)", merged.Env["SHARED"], "role")
	}
	if merged.Env["TOWN"] != "yes" {
		t.Errorf("Env[TOWN] = %q, want %q (from town)", merged.Env["TOWN"], "yes")
	}
	if merged.Env["RIG"] != "gastown" {
		t.Errorf("Env[RIG] = %q, want %q (from rig)", merged.Env["RIG"], "gastown")
	}
}
