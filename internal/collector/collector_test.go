package collector

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/mhever/gitops-remediator/internal/watcher"
)

func TestNoopCollector_CollectReturnsNonNilBundle(t *testing.T) {
	c := &NoopCollector{}
	event := watcher.FailureEvent{
		Namespace:   "default",
		PodName:     "test-pod",
		FailureType: watcher.FailureTypeOOMKilled,
	}

	bundle, err := c.Collect(context.Background(), event)
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if bundle == nil {
		t.Fatal("expected non-nil bundle, got nil")
	}
}

func TestNoopCollector_CollectWithCancelledContext(t *testing.T) {
	// NoopCollector intentionally ignores context; it should still return a
	// non-nil bundle and nil error even when the context is already cancelled.
	c := &NoopCollector{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	event := watcher.FailureEvent{
		Namespace:   "default",
		PodName:     "test-pod",
		FailureType: watcher.FailureTypeCrashLoopBackOff,
	}

	bundle, err := c.Collect(ctx, event)
	if err != nil {
		t.Errorf("expected nil error even with cancelled context, got: %v", err)
	}
	if bundle == nil {
		t.Fatal("expected non-nil bundle even with cancelled context, got nil")
	}
}

// newTestPod creates an OOMKilled pod for use in K8sCollector tests.
func newTestPod() *corev1.Pod {
	restartCount := int32(5)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sample-app-abc123",
			Namespace: "remediator-test",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "app",
					Image: "myapp:latest",
					Env: []corev1.EnvVar{
						{Name: "SECRET_KEY", Value: "do-not-leak-this"},
						{Name: "DB_HOST", Value: "db.internal"},
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("64Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("200m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:         "app",
					Ready:        false,
					RestartCount: restartCount,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							Reason: "OOMKilled",
						},
					},
				},
			},
		},
	}
}

func newTestEvent() watcher.FailureEvent {
	return watcher.FailureEvent{
		Namespace:     "remediator-test",
		PodName:       "sample-app-abc123",
		ContainerName: "app",
		FailureType:   watcher.FailureTypeOOMKilled,
		Timestamp:     time.Date(2026, 3, 16, 18, 0, 0, 0, time.UTC),
	}
}

func TestK8sCollector_BundleContainsSections(t *testing.T) {
	pod := newTestPod()
	fakeClient := fake.NewSimpleClientset(pod)
	col := NewK8sCollector(fakeClient, noopLogger())

	bundle, err := col.Collect(context.Background(), newTestEvent())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bundle == nil {
		t.Fatal("expected non-nil bundle")
	}

	expectedSections := []string{
		"=== FAILURE EVENT ===",
		"=== POD SPEC (sanitized) ===",
		"=== POD STATUS ===",
		"=== RESOURCE LIMITS ===",
		"=== RECENT EVENTS (last 5) ===",
		"=== CONTAINER LOGS (last 100 lines) ===",
	}
	for _, section := range expectedSections {
		if !strings.Contains(bundle.Content, section) {
			t.Errorf("bundle content missing section: %q", section)
		}
	}
}

func TestK8sCollector_BundleContainsContainerLogHeader(t *testing.T) {
	pod := newTestPod()
	fakeClient := fake.NewSimpleClientset(pod)
	col := NewK8sCollector(fakeClient, noopLogger())

	bundle, err := col.Collect(context.Background(), newTestEvent())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(bundle.Content, "--- app ---") {
		t.Errorf("bundle content missing container log header '--- app ---'")
	}
}

func TestK8sCollector_EnvVarsRedacted(t *testing.T) {
	pod := newTestPod()
	fakeClient := fake.NewSimpleClientset(pod)
	col := NewK8sCollector(fakeClient, noopLogger())

	bundle, err := col.Collect(context.Background(), newTestEvent())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Literal env var values must not appear in the bundle.
	if strings.Contains(bundle.Content, "do-not-leak-this") {
		t.Error("bundle content contains literal env var value 'do-not-leak-this' — PII not redacted")
	}
	if strings.Contains(bundle.Content, "db.internal") {
		t.Error("bundle content contains literal env var value 'db.internal' — PII not redacted")
	}

	// Keys must still appear.
	if !strings.Contains(bundle.Content, "SECRET_KEY") {
		t.Error("bundle content missing env var key 'SECRET_KEY'")
	}
	if !strings.Contains(bundle.Content, "DB_HOST") {
		t.Error("bundle content missing env var key 'DB_HOST'")
	}
}

func TestK8sCollector_MissingPod(t *testing.T) {
	// Empty fake client — no pods exist.
	fakeClient := fake.NewSimpleClientset()
	col := NewK8sCollector(fakeClient, noopLogger())

	_, err := col.Collect(context.Background(), newTestEvent())
	if err == nil {
		t.Error("expected error when pod does not exist, got nil")
	}
}
