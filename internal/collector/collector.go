package collector

import (
	"context"

	"github.com/mhever/gitops-remediator/internal/watcher"
)

// DiagnosticBundle is the assembled context sent to the Diagnostician.
type DiagnosticBundle struct {
	// Content is a structured plain-text block (not JSON).
	Content string
}

// Collector assembles a DiagnosticBundle from a FailureEvent.
type Collector interface {
	Collect(ctx context.Context, event watcher.FailureEvent) (*DiagnosticBundle, error)
}

// NoopCollector satisfies Collector without doing anything.
type NoopCollector struct{}

var _ Collector = (*NoopCollector)(nil)

func (n *NoopCollector) Collect(ctx context.Context, event watcher.FailureEvent) (*DiagnosticBundle, error) {
	return &DiagnosticBundle{Content: ""}, nil
}
