package collector

import (
	"context"
	"testing"

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
