package collector

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
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

func TestCollect_PodNotFound(t *testing.T) {
	// Empty fake client — no pods exist, so Get returns NotFound.
	fakeClient := fake.NewSimpleClientset()
	col := NewK8sCollector(fakeClient, noopLogger())

	_, err := col.Collect(context.Background(), newTestEvent())
	if err == nil {
		t.Fatal("expected error when pod does not exist, got nil")
	}
	if !errors.Is(err, ErrPodGone) {
		t.Errorf("expected errors.Is(err, ErrPodGone) to be true, got: %v", err)
	}
}

func TestCollect_IncludesPreviousImage(t *testing.T) {
	ns := "remediator-test"

	// rs-old was created earlier and has the previous image (revision 1).
	rsOld := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "rs-old",
			Namespace:   ns,
			Annotations: map[string]string{"deployment.kubernetes.io/revision": "1"},
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Deployment", Name: "my-deploy"},
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "app", Image: "ghcr.io/org/app:sha-abc123"},
					},
				},
			},
		},
	}

	// rs-new is the current RS (revision 2).
	rsNew := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "rs-new",
			Namespace:   ns,
			Annotations: map[string]string{"deployment.kubernetes.io/revision": "2"},
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Deployment", Name: "my-deploy"},
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "app", Image: "ghcr.io/org/app:sha-broken"},
					},
				},
			},
		},
	}

	// The failing pod is owned by rs-new.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sample-app-abc123",
			Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "rs-new"},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "app",
					Image: "ghcr.io/org/app:sha-broken",
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
		},
	}

	fakeClient := fake.NewSimpleClientset(pod, rsNew, rsOld)
	col := NewK8sCollector(fakeClient, noopLogger())

	event := watcher.FailureEvent{
		Namespace:     ns,
		PodName:       "sample-app-abc123",
		ContainerName: "app",
		FailureType:   watcher.FailureTypeImagePullBackOff,
		Timestamp:     time.Date(2026, 3, 16, 18, 0, 0, 0, time.UTC),
	}

	bundle, err := col.Collect(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(bundle.Content, "sha-abc123") {
		t.Errorf("expected bundle to contain previous image tag 'sha-abc123', got:\n%s", bundle.Content)
	}
	if !strings.Contains(bundle.Content, "=== PREVIOUS IMAGE ===") {
		t.Errorf("expected bundle to contain '=== PREVIOUS IMAGE ===' section, got:\n%s", bundle.Content)
	}
}

func TestCollect_PreviousImageSkipsSameTag(t *testing.T) {
	// Regression test: when the most recent previous RS also has the same
	// (broken) image tag as the current failing pod, previousImage must skip
	// it and return the tag from the older, genuinely different RS.
	ns := "remediator-test"

	// rs-good: revision 1, has a known-good tag.
	rsGood := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "rs-good",
			Namespace:   ns,
			Annotations: map[string]string{"deployment.kubernetes.io/revision": "1"},
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Deployment", Name: "my-deploy"},
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "app", Image: "ghcr.io/org/app:sha-good"},
					},
				},
			},
		},
	}

	// rs-also-bad: revision 2, also has the broken tag (previous failed deployment attempt).
	rsAlsoBad := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "rs-also-bad",
			Namespace:   ns,
			Annotations: map[string]string{"deployment.kubernetes.io/revision": "2"},
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Deployment", Name: "my-deploy"},
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "app", Image: "ghcr.io/org/app:sha-broken"},
					},
				},
			},
		},
	}

	// rs-new: current RS (revision 3, owned by the failing pod).
	rsNew := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "rs-new",
			Namespace:   ns,
			Annotations: map[string]string{"deployment.kubernetes.io/revision": "3"},
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Deployment", Name: "my-deploy"},
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "app", Image: "ghcr.io/org/app:sha-broken"},
					},
				},
			},
		},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sample-app-abc123",
			Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "rs-new"},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "app",
					Image: "ghcr.io/org/app:sha-broken",
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
		Status: corev1.PodStatus{Phase: corev1.PodFailed},
	}

	fakeClient := fake.NewSimpleClientset(pod, rsNew, rsAlsoBad, rsGood)
	col := NewK8sCollector(fakeClient, noopLogger())

	event := watcher.FailureEvent{
		Namespace:     ns,
		PodName:       "sample-app-abc123",
		ContainerName: "app",
		FailureType:   watcher.FailureTypeImagePullBackOff,
		Timestamp:     time.Date(2026, 3, 16, 18, 0, 0, 0, time.UTC),
	}

	bundle, err := col.Collect(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Must contain the good tag, not the broken one (in the PREVIOUS IMAGE section).
	if !strings.Contains(bundle.Content, "sha-good") {
		t.Errorf("expected bundle to contain 'sha-good' (skipping same-tag RS), got:\n%s", bundle.Content)
	}
	if !strings.Contains(bundle.Content, "=== PREVIOUS IMAGE ===") {
		t.Errorf("expected bundle to contain '=== PREVIOUS IMAGE ===' section")
	}
}

func TestCollect_PreviousImageRollbackReuse(t *testing.T) {
	// Regression test: Kubernetes reuses an existing ReplicaSet on rollback,
	// updating its deployment.kubernetes.io/revision annotation rather than
	// creating a new RS. A sort by creationTimestamp would give the wrong order.
	//
	// Scenario (matches real test sequence):
	//   revision 1 — sha-good (created first, low creationTimestamp)
	//   revision 2 — does-nut-exist (bad, created second)
	//   revision 3 — sha-good again (Kubernetes reuses the rev-1 RS; same
	//                creationTimestamp as rev-1, but revision bumped to 3)
	//   revision 4 — does-nutz-exist (current failure)
	//
	// Without revision-based sorting, rev-1/rev-3 sorts LAST (oldest
	// creationTimestamp) and does-nut-exist (rev 2) appears to be the most
	// recent previous — causing the wrong tag to be proposed.
	ns := "remediator-test"

	// rs-good represents the sha-good RS after it was reused for the rollback:
	// its revision annotation is 3 but its creationTimestamp is old (same object).
	rsGood := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "rs-sha-good",
			Namespace:   ns,
			Annotations: map[string]string{"deployment.kubernetes.io/revision": "3"},
			CreationTimestamp: metav1.Time{
				Time: time.Date(2026, 3, 14, 10, 0, 0, 0, time.UTC), // oldest creation time
			},
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Deployment", Name: "my-deploy"},
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "app", Image: "ghcr.io/org/app:sha-good"},
					},
				},
			},
		},
	}

	// rs-bad: revision 2, created more recently than rs-good.
	rsBad := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "rs-does-nut-exist",
			Namespace:   ns,
			Annotations: map[string]string{"deployment.kubernetes.io/revision": "2"},
			CreationTimestamp: metav1.Time{
				Time: time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC),
			},
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Deployment", Name: "my-deploy"},
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "app", Image: "ghcr.io/org/app:does-nut-exist"},
					},
				},
			},
		},
	}

	// rs-current: revision 4, the failing deployment.
	rsCurrent := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "rs-does-nutz-exist",
			Namespace:   ns,
			Annotations: map[string]string{"deployment.kubernetes.io/revision": "4"},
			CreationTimestamp: metav1.Time{
				Time: time.Date(2026, 3, 16, 18, 0, 0, 0, time.UTC),
			},
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Deployment", Name: "my-deploy"},
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "app", Image: "ghcr.io/org/app:does-nutz-exist"},
					},
				},
			},
		},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sample-app-abc123",
			Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "rs-does-nutz-exist"},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "app",
					Image: "ghcr.io/org/app:does-nutz-exist",
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
		Status: corev1.PodStatus{Phase: corev1.PodFailed},
	}

	fakeClient := fake.NewSimpleClientset(pod, rsCurrent, rsBad, rsGood)
	col := NewK8sCollector(fakeClient, noopLogger())

	event := watcher.FailureEvent{
		Namespace:     ns,
		PodName:       "sample-app-abc123",
		ContainerName: "app",
		FailureType:   watcher.FailureTypeImagePullBackOff,
		Timestamp:     time.Date(2026, 3, 16, 18, 0, 0, 0, time.UTC),
	}

	bundle, err := col.Collect(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The most recently active previous RS is rs-good (revision 3, the rollback).
	// It must be preferred over rs-bad (revision 2) even though rs-good has an
	// older creationTimestamp.
	if !strings.Contains(bundle.Content, "sha-good") {
		t.Errorf("expected 'sha-good' (revision 3 rollback RS), got:\n%s", bundle.Content)
	}
	if strings.Contains(bundle.Content, "does-nut-exist") {
		t.Errorf("got 'does-nut-exist' (wrong RS picked by creationTimestamp ordering):\n%s", bundle.Content)
	}
}

func TestCollect_NoPreviousImage(t *testing.T) {
	// Pod has no owner references — previousImage should return "" and
	// the bundle should NOT contain the PREVIOUS IMAGE section.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sample-app-abc123",
			Namespace: "remediator-test",
			// No OwnerReferences.
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "app",
					Image: "ghcr.io/org/app:sha-broken",
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
		},
	}

	fakeClient := fake.NewSimpleClientset(pod)
	col := NewK8sCollector(fakeClient, noopLogger())

	event := watcher.FailureEvent{
		Namespace:     "remediator-test",
		PodName:       "sample-app-abc123",
		ContainerName: "app",
		FailureType:   watcher.FailureTypeImagePullBackOff,
		Timestamp:     time.Date(2026, 3, 16, 18, 0, 0, 0, time.UTC),
	}

	bundle, err := col.Collect(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(bundle.Content, "=== PREVIOUS IMAGE ===") {
		t.Errorf("expected bundle NOT to contain '=== PREVIOUS IMAGE ===' section when pod has no owner references")
	}
}
