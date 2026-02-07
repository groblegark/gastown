package podmanager

import (
	"context"
	"log/slog"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestAgentPodSpec_PodName(t *testing.T) {
	spec := AgentPodSpec{Rig: "gastown", Role: "polecat", AgentName: "furiosa"}
	want := "gt-gastown-polecat-furiosa"
	if got := spec.PodName(); got != want {
		t.Errorf("PodName() = %q, want %q", got, want)
	}
}

func TestAgentPodSpec_Labels(t *testing.T) {
	spec := AgentPodSpec{Rig: "beads", Role: "crew", AgentName: "quartz"}
	labels := spec.Labels()

	checks := map[string]string{
		LabelApp:   LabelAppValue,
		LabelRig:   "beads",
		LabelRole:  "crew",
		LabelAgent: "quartz",
	}
	for k, want := range checks {
		if got := labels[k]; got != want {
			t.Errorf("Labels()[%q] = %q, want %q", k, got, want)
		}
	}
}

func TestK8sManager_CreateAndGet(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := New(client, slog.Default())

	spec := AgentPodSpec{
		Rig:       "gastown",
		Role:      "polecat",
		AgentName: "furiosa",
		Image:     "gastown-agent:latest",
		Namespace: "gastown",
		Env:       map[string]string{"BD_DAEMON_HOST": "localhost"},
	}

	ctx := context.Background()

	if err := mgr.CreateAgentPod(ctx, spec); err != nil {
		t.Fatalf("CreateAgentPod() error: %v", err)
	}

	pod, err := mgr.GetAgentPod(ctx, "gt-gastown-polecat-furiosa", "gastown")
	if err != nil {
		t.Fatalf("GetAgentPod() error: %v", err)
	}
	if pod.Name != "gt-gastown-polecat-furiosa" {
		t.Errorf("pod name = %q, want %q", pod.Name, "gt-gastown-polecat-furiosa")
	}
	if pod.Spec.RestartPolicy != "Never" {
		t.Errorf("polecat restart policy = %q, want Never", pod.Spec.RestartPolicy)
	}
}

func TestK8sManager_ListByLabels(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := New(client, slog.Default())
	ctx := context.Background()

	for _, name := range []string{"a", "b"} {
		spec := AgentPodSpec{
			Rig: "gastown", Role: "polecat", AgentName: name,
			Image: "agent:latest", Namespace: "gastown",
		}
		if err := mgr.CreateAgentPod(ctx, spec); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	pods, err := mgr.ListAgentPods(ctx, "gastown", map[string]string{
		LabelRole: "polecat",
		LabelRig:  "gastown",
	})
	if err != nil {
		t.Fatalf("ListAgentPods() error: %v", err)
	}
	if len(pods) != 2 {
		t.Errorf("got %d pods, want 2", len(pods))
	}
}

func TestK8sManager_Delete(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := New(client, slog.Default())
	ctx := context.Background()

	spec := AgentPodSpec{
		Rig: "gastown", Role: "crew", AgentName: "colonization",
		Image: "agent:latest", Namespace: "gastown",
	}
	if err := mgr.CreateAgentPod(ctx, spec); err != nil {
		t.Fatal(err)
	}

	if err := mgr.DeleteAgentPod(ctx, "gt-gastown-crew-colonization", "gastown"); err != nil {
		t.Fatalf("DeleteAgentPod() error: %v", err)
	}

	_, err := mgr.GetAgentPod(ctx, "gt-gastown-crew-colonization", "gastown")
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

func TestRestartPolicyForRole(t *testing.T) {
	tests := []struct {
		role string
		want string
	}{
		{"polecat", "Never"},
		{"crew", "Always"},
		{"witness", "Always"},
		{"refinery", "Always"},
		{"mayor", "Always"},
		{"deacon", "Always"},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			got := restartPolicyForRole(tt.role)
			if string(got) != tt.want {
				t.Errorf("restartPolicyForRole(%q) = %q, want %q", tt.role, got, tt.want)
			}
		})
	}
}

func TestK8sManager_CreateSetsEnvVars(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := New(client, slog.Default())
	ctx := context.Background()

	spec := AgentPodSpec{
		Rig: "gastown", Role: "witness", AgentName: "w1",
		Image: "agent:latest", Namespace: "gastown",
		Env: map[string]string{"BD_DAEMON_HOST": "daemon.svc"},
	}
	if err := mgr.CreateAgentPod(ctx, spec); err != nil {
		t.Fatal(err)
	}

	pod, _ := client.CoreV1().Pods("gastown").Get(ctx, "gt-gastown-witness-w1", metav1.GetOptions{})
	envMap := make(map[string]string)
	for _, e := range pod.Spec.Containers[0].Env {
		envMap[e.Name] = e.Value
	}

	required := []string{"GT_ROLE", "GT_RIG", "GT_AGENT", "BD_DAEMON_HOST"}
	for _, key := range required {
		if _, ok := envMap[key]; !ok {
			t.Errorf("missing env var %s", key)
		}
	}
}
