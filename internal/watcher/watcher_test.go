package watcher

import (
	"context"
	"log/slog"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// newTestWatcher builds a K8sWatcher wired to the given channel, with
// startTime set to now. It does NOT start a Run loop.
func newTestWatcher(evCh chan FailureEvent) *K8sWatcher {
	fakeClient := fake.NewSimpleClientset()
	w := NewK8sWatcher(fakeClient, "test-ns", evCh, slog.New(slog.Default().Handler()))
	return w
}

func TestNoopWatcher_RunReturnsCanceledOnCancel(t *testing.T) {
	w := &NoopWatcher{}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- w.Run(ctx)
	}()

	cancel()

	err := <-done
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}


func TestK8sWatcher_EmitsOOMKilledEvent(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	evCh := make(chan FailureEvent, 10)

	w := NewK8sWatcher(fakeClient, "test-ns", evCh, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- w.Run(ctx)
	}()

	// Give the informer time to start and sync.
	time.Sleep(100 * time.Millisecond)

	// Create an OOMKilled pod in test-ns.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oom-pod",
			Namespace: "test-ns",
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "app",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"},
					},
				},
			},
		},
	}

	if _, err := fakeClient.CoreV1().Pods("test-ns").Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create pod: %v", err)
	}

	// Poll the events channel with a timeout.
	timeout := time.After(2 * time.Second)
	for {
		select {
		case e := <-evCh:
			if e.FailureType != FailureTypeOOMKilled {
				t.Errorf("FailureType = %q, want %q", e.FailureType, FailureTypeOOMKilled)
			}
			if e.PodName != "oom-pod" {
				t.Errorf("PodName = %q, want %q", e.PodName, "oom-pod")
			}
			// Success — cancel and verify Run returns.
			cancel()
			err := <-runDone
			if err != context.Canceled {
				t.Errorf("Run() returned %v, want context.Canceled", err)
			}
			return
		case <-timeout:
			t.Fatal("timed out waiting for OOMKilled FailureEvent")
		}
	}
}

// TestK8sWatcher_SkipsAddFuncBeforeSync verifies that handlePodUpdate does not
// emit events while the synced gate is false (simulating the initial list replay).
func TestK8sWatcher_SkipsAddFuncBeforeSync(t *testing.T) {
	evCh := make(chan FailureEvent, 10)
	w := newTestWatcher(evCh)
	// synced is false by default (zero value of atomic.Bool).

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "stale-pod",
			Namespace: "test-ns",
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "app",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"},
					},
				},
			},
		},
	}

	w.handlePodUpdate(nil, pod)

	select {
	case e := <-evCh:
		t.Errorf("expected no event before sync, got: %+v", e)
	default:
		// correct — channel is empty
	}
}

// TestK8sWatcher_ProcessesAddFuncAfterSync verifies that handlePodUpdate emits
// events once the synced gate is set to true.
func TestK8sWatcher_ProcessesAddFuncAfterSync(t *testing.T) {
	evCh := make(chan FailureEvent, 10)
	w := newTestWatcher(evCh)
	w.synced.Store(true)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "new-pod",
			Namespace: "test-ns",
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "app",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"},
					},
				},
			},
		},
	}

	w.handlePodUpdate(nil, pod)

	select {
	case e := <-evCh:
		if e.FailureType != FailureTypeImagePullBackOff {
			t.Errorf("FailureType = %q, want %q", e.FailureType, FailureTypeImagePullBackOff)
		}
		if e.PodName != "new-pod" {
			t.Errorf("PodName = %q, want %q", e.PodName, "new-pod")
		}
	default:
		t.Error("expected a FailureEvent after sync, got none")
	}
}

// TestK8sWatcher_SkipsStaleK8sEvent verifies that handleEventAdd drops events
// whose LastTimestamp predates the watcher's startTime.
func TestK8sWatcher_SkipsStaleK8sEvent(t *testing.T) {
	evCh := make(chan FailureEvent, 10)
	w := newTestWatcher(evCh)

	staleTime := w.startTime.Add(-5 * time.Minute)
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "stale-event",
			Namespace: "test-ns",
		},
		InvolvedObject: corev1.ObjectReference{Name: "some-pod"},
		Type:           corev1.EventTypeWarning,
		Reason:         "Failed",
		Message:        "pull image failed",
		LastTimestamp:  metav1.Time{Time: staleTime},
	}

	w.handleEventAdd(event)

	select {
	case e := <-evCh:
		t.Errorf("expected no event for stale k8s event, got: %+v", e)
	default:
		// correct — channel is empty
	}
}

// TestK8sWatcher_ProcessesFreshK8sEvent verifies that handleEventAdd emits an
// event when the LastTimestamp is after the watcher's startTime.
func TestK8sWatcher_ProcessesFreshK8sEvent(t *testing.T) {
	evCh := make(chan FailureEvent, 10)
	w := newTestWatcher(evCh)

	freshTime := time.Now() // guaranteed to be after startTime set in NewK8sWatcher
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fresh-event",
			Namespace: "test-ns",
		},
		InvolvedObject: corev1.ObjectReference{Name: "fresh-pod"},
		Type:           corev1.EventTypeWarning,
		Reason:         "Failed",
		Message:        "pull image failed",
		LastTimestamp:  metav1.Time{Time: freshTime},
	}

	w.handleEventAdd(event)

	select {
	case e := <-evCh:
		if e.FailureType != FailureTypeImagePullBackOff {
			t.Errorf("FailureType = %q, want %q", e.FailureType, FailureTypeImagePullBackOff)
		}
		if e.PodName != "fresh-pod" {
			t.Errorf("PodName = %q, want %q", e.PodName, "fresh-pod")
		}
	default:
		t.Error("expected a FailureEvent for fresh k8s event, got none")
	}
}
