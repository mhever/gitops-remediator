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
