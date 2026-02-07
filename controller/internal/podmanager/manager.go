// Package podmanager handles K8s pod CRUD for Gas Town agents.
// It translates beads lifecycle decisions into pod create/delete operations.
// The pod manager never makes lifecycle decisions — it executes them.
package podmanager

import (
	"context"
	"fmt"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

const (
	// Label keys for agent pods.
	LabelApp   = "app.kubernetes.io/name"
	LabelRig   = "gastown.io/rig"
	LabelRole  = "gastown.io/role"
	LabelAgent = "gastown.io/agent"

	// LabelAppValue is the app label value for all gastown pods.
	LabelAppValue = "gastown"
)

// AgentPodSpec describes the desired pod for an agent.
type AgentPodSpec struct {
	Rig       string
	Role      string // polecat, crew, witness, refinery, mayor, deacon
	AgentName string
	Image     string
	Namespace string
	Env       map[string]string
}

// PodName returns the canonical pod name: gt-{rig}-{role}-{name}.
func (s *AgentPodSpec) PodName() string {
	return fmt.Sprintf("gt-%s-%s-%s", s.Rig, s.Role, s.AgentName)
}

// Labels returns the standard label set for this agent pod.
func (s *AgentPodSpec) Labels() map[string]string {
	return map[string]string{
		LabelApp:   LabelAppValue,
		LabelRig:   s.Rig,
		LabelRole:  s.Role,
		LabelAgent: s.AgentName,
	}
}

// Manager creates, deletes, and lists agent pods in K8s.
type Manager interface {
	CreateAgentPod(ctx context.Context, spec AgentPodSpec) error
	DeleteAgentPod(ctx context.Context, name, namespace string) error
	ListAgentPods(ctx context.Context, namespace string, labelSelector map[string]string) ([]corev1.Pod, error)
	GetAgentPod(ctx context.Context, name, namespace string) (*corev1.Pod, error)
}

// K8sManager implements Manager using client-go.
type K8sManager struct {
	client kubernetes.Interface
	logger *slog.Logger
}

// New creates a pod manager backed by a K8s client.
func New(client kubernetes.Interface, logger *slog.Logger) *K8sManager {
	return &K8sManager{client: client, logger: logger}
}

// CreateAgentPod creates a pod for the given agent spec.
func (m *K8sManager) CreateAgentPod(ctx context.Context, spec AgentPodSpec) error {
	pod := m.buildPod(spec)
	m.logger.Info("creating agent pod",
		"pod", pod.Name, "rig", spec.Rig, "role", spec.Role, "agent", spec.AgentName)

	_, err := m.client.CoreV1().Pods(spec.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating pod %s: %w", pod.Name, err)
	}
	return nil
}

// DeleteAgentPod deletes a pod by name and namespace.
func (m *K8sManager) DeleteAgentPod(ctx context.Context, name, namespace string) error {
	m.logger.Info("deleting agent pod", "pod", name, "namespace", namespace)
	return m.client.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

// ListAgentPods lists pods matching the given labels.
func (m *K8sManager) ListAgentPods(ctx context.Context, namespace string, labelSelector map[string]string) ([]corev1.Pod, error) {
	sel := labels.Set(labelSelector).String()
	list, err := m.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: sel,
	})
	if err != nil {
		return nil, fmt.Errorf("listing pods with selector %s: %w", sel, err)
	}
	return list.Items, nil
}

// GetAgentPod gets a single pod by name.
func (m *K8sManager) GetAgentPod(ctx context.Context, name, namespace string) (*corev1.Pod, error) {
	return m.client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
}

func (m *K8sManager) buildPod(spec AgentPodSpec) *corev1.Pod {
	envVars := []corev1.EnvVar{
		{Name: "GT_ROLE", Value: spec.Role},
		{Name: "GT_RIG", Value: spec.Rig},
		{Name: "GT_AGENT", Value: spec.AgentName},
	}
	for k, v := range spec.Env {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.PodName(),
			Namespace: spec.Namespace,
			Labels:    spec.Labels(),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "agent",
					Image: spec.Image,
					Env:   envVars,
				},
			},
			RestartPolicy: restartPolicyForRole(spec.Role),
		},
	}
}

// restartPolicyForRole returns the appropriate restart policy.
// Polecats are one-shot (Never); all others restart on failure.
func restartPolicyForRole(role string) corev1.RestartPolicy {
	if role == "polecat" {
		return corev1.RestartPolicyNever
	}
	return corev1.RestartPolicyAlways
}
