package collector

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSanitize_LiteralEnvVarsRedacted(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "app",
					Env: []corev1.EnvVar{
						{Name: "SECRET_KEY", Value: "super-secret-value"},
						{Name: "DB_PASS", Value: "my-password"},
					},
				},
			},
		},
	}

	result := sanitize(pod)

	for _, c := range result.Spec.Containers {
		for _, env := range c.Env {
			if env.Value != "[REDACTED]" {
				t.Errorf("expected env var %q value to be [REDACTED], got %q", env.Name, env.Value)
			}
		}
	}

	// Keys must be preserved.
	if result.Spec.Containers[0].Env[0].Name != "SECRET_KEY" {
		t.Errorf("expected env key to be preserved as SECRET_KEY, got %q", result.Spec.Containers[0].Env[0].Name)
	}
	if result.Spec.Containers[0].Env[1].Name != "DB_PASS" {
		t.Errorf("expected env key to be preserved as DB_PASS, got %q", result.Spec.Containers[0].Env[1].Name)
	}
}

func TestSanitize_ValueFromRedacted(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "app",
					Env: []corev1.EnvVar{
						{
							Name: "SECRET_KEY",
							ValueFrom: &corev1.EnvVarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: "my-secret"},
									Key:                  "key",
								},
							},
						},
					},
				},
			},
		},
	}

	result := sanitize(pod)

	env := result.Spec.Containers[0].Env[0]
	if env.ValueFrom != nil {
		t.Error("expected ValueFrom to be nil after sanitize")
	}
	if env.Value != "[REDACTED]" {
		t.Errorf("expected Value to be [REDACTED], got %q", env.Value)
	}
	if env.Name != "SECRET_KEY" {
		t.Errorf("expected Name to be preserved as SECRET_KEY, got %q", env.Name)
	}
}

func TestSanitize_NoEnvVars_NoPanic(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "app"},
			},
		},
	}

	// Should not panic.
	result := sanitize(pod)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Spec.Containers[0].Env) != 0 {
		t.Error("expected empty env in result")
	}
}

func TestSanitize_InitContainersRedacted(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{
				{
					Name: "init",
					Env: []corev1.EnvVar{
						{Name: "INIT_SECRET", Value: "init-secret-value"},
					},
				},
			},
			Containers: []corev1.Container{
				{Name: "app"},
			},
		},
	}

	result := sanitize(pod)

	if len(result.Spec.InitContainers) != 1 {
		t.Fatalf("expected 1 init container, got %d", len(result.Spec.InitContainers))
	}
	env := result.Spec.InitContainers[0].Env[0]
	if env.Value != "[REDACTED]" {
		t.Errorf("expected init container env value to be [REDACTED], got %q", env.Value)
	}
	if env.Name != "INIT_SECRET" {
		t.Errorf("expected init container env key preserved as INIT_SECRET, got %q", env.Name)
	}
}

func TestSanitize_DoesNotModifyOriginal(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "app",
					Env: []corev1.EnvVar{
						{Name: "MY_KEY", Value: "original-value"},
					},
				},
			},
		},
	}

	_ = sanitize(pod)

	// Original pod must be unchanged.
	if pod.Spec.Containers[0].Env[0].Value != "original-value" {
		t.Errorf("sanitize modified original pod: env value is now %q", pod.Spec.Containers[0].Env[0].Value)
	}
}
