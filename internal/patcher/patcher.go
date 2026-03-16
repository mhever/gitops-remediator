package patcher

import (
	"context"

	"github.com/mhever/gitops-remediator/internal/diagnostician"
	"github.com/mhever/gitops-remediator/internal/watcher"
)

// PatchResult is the output of a successful patch operation.
type PatchResult struct {
	FilePath   string // repo-relative path of the patched file
	OldContent []byte
	NewContent []byte
	Diff       string // unified diff (oldContent vs newContent)
}

// Patcher locates the correct manifest in a cloned repo and applies the patch.
type Patcher interface {
	Apply(ctx context.Context, repoDir string, diag diagnostician.Diagnosis, event watcher.FailureEvent) (*PatchResult, error)
}

// NoopPatcher satisfies Patcher without doing anything.
type NoopPatcher struct{}

var _ Patcher = (*NoopPatcher)(nil)

// Apply returns an empty PatchResult without modifying anything.
func (n *NoopPatcher) Apply(ctx context.Context, repoDir string, diag diagnostician.Diagnosis, event watcher.FailureEvent) (*PatchResult, error) {
	return &PatchResult{}, nil
}
