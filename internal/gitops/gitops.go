package gitops

import (
	"context"

	"github.com/mhever/gitops-remediator/internal/diagnostician"
	"github.com/mhever/gitops-remediator/internal/watcher"
)

// PRRequest contains the information needed to open a remediation PR.
type PRRequest struct {
	Diag  diagnostician.Diagnosis
	Event watcher.FailureEvent
	Diff  string // the YAML patch diff
}

// GitOps clones the GitOps repo, commits a patch, and opens a GitHub PR.
type GitOps interface {
	// OpenPR creates a branch, commits the patch, and opens a GitHub PR.
	// Returns the PR URL.
	OpenPR(ctx context.Context, req PRRequest) (string, error)
}

// NoopGitOps satisfies GitOps without doing anything.
type NoopGitOps struct{}

var _ GitOps = (*NoopGitOps)(nil)

func (n *NoopGitOps) OpenPR(ctx context.Context, req PRRequest) (string, error) {
	return "", nil
}
