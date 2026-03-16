package gitops

import (
	"context"

	"github.com/mhever/gitops-remediator/internal/diagnostician"
	"github.com/mhever/gitops-remediator/internal/watcher"
)

// PRRequest contains all information needed to open a remediation PR.
type PRRequest struct {
	Diag  diagnostician.Diagnosis
	Event watcher.FailureEvent
}

// GitOps clones the GitOps repo, patches the manifest, commits, and opens a PR.
type GitOps interface {
	OpenPR(ctx context.Context, req PRRequest) (string, error)
}

// NoopGitOps satisfies GitOps without doing anything.
type NoopGitOps struct{}

var _ GitOps = (*NoopGitOps)(nil)

// OpenPR returns an empty URL without doing anything.
func (n *NoopGitOps) OpenPR(ctx context.Context, req PRRequest) (string, error) {
	return "", nil
}
