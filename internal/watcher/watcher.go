package watcher

import (
	"context"
	"time"
)

// FailureType is an enum of the failure modes the remediator handles.
type FailureType string

const (
	FailureTypeOOMKilled        FailureType = "OOMKilled"
	FailureTypeCrashLoopBackOff FailureType = "CrashLoopBackOff"
	FailureTypeImagePullBackOff FailureType = "ImagePullBackOff"
)

// FailureEvent is emitted by the Watcher when a monitored failure is detected.
type FailureEvent struct {
	Namespace     string
	PodName       string
	ContainerName string
	FailureType   FailureType
	RawReason     string
	Timestamp     time.Time
}

// Watcher watches a Kubernetes namespace for failure events.
type Watcher interface {
	// Run starts the watcher. It blocks until ctx is cancelled.
	Run(ctx context.Context) error
}

// NoopWatcher satisfies Watcher without doing anything.
// Used during Phase 0 scaffold and as a test double.
type NoopWatcher struct{}

// Compile-time interface check.
var _ Watcher = (*NoopWatcher)(nil)

func (n *NoopWatcher) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
