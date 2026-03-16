package patcher

import (
	"context"
	"testing"

	"github.com/mhever/gitops-remediator/internal/diagnostician"
	"github.com/mhever/gitops-remediator/internal/watcher"
)

func TestNoopPatcher_ApplyReturnsNil(t *testing.T) {
	p := &NoopPatcher{}
	diag := diagnostician.Diagnosis{
		FailureType: "OOMKilled",
		Remediable:  true,
	}
	event := watcher.FailureEvent{
		Namespace: "default",
		PodName:   "test-pod",
	}

	err := p.Apply(context.Background(), diag, event)
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
}
