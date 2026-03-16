package patcher

import (
	"context"

	"github.com/mhever/gitops-remediator/internal/diagnostician"
	"github.com/mhever/gitops-remediator/internal/watcher"
)

// Patcher applies a patch to the GitOps repository manifest.
type Patcher interface {
	Apply(ctx context.Context, diag diagnostician.Diagnosis, event watcher.FailureEvent) error
}

// NoopPatcher satisfies Patcher without doing anything.
type NoopPatcher struct{}

var _ Patcher = (*NoopPatcher)(nil)

func (n *NoopPatcher) Apply(ctx context.Context, diag diagnostician.Diagnosis, event watcher.FailureEvent) error {
	return nil
}
