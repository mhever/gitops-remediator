package gitops

import (
	"context"
	"errors"

	"github.com/mhever/gitops-remediator/internal/diagnostician"
	"github.com/mhever/gitops-remediator/internal/watcher"
)

// ErrBranchExists is returned by OpenPR when a remediation branch for this
// event already exists, indicating the event is a duplicate.
var ErrBranchExists = errors.New("remediation branch already exists")

// ErrNothingToCommit is returned by OpenPR when the proposed patch produces no
// diff against the GitOps repo. This happens when the repo already reflects the
// desired state — for example, when kubectl set image was used to force a broken
// image directly on the cluster without updating the GitOps repo first.
var ErrNothingToCommit = errors.New("patch produced no changes — repo already reflects desired state")

// PRRequest contains all information needed to open a remediation PR.
type PRRequest struct {
	Diag  diagnostician.Diagnosis
	Event watcher.FailureEvent
}

// GitOps clones the GitOps repo, patches the manifest, commits, and opens a PR.
type GitOps interface {
	OpenPR(ctx context.Context, req PRRequest) (string, error)
}

// Pinger can perform a lightweight connectivity check against the GitOps provider.
type Pinger interface {
	Ping(ctx context.Context) error
}

// NoopGitOps satisfies GitOps without doing anything.
type NoopGitOps struct{}

var _ GitOps = (*NoopGitOps)(nil)

// OpenPR returns an empty URL without doing anything.
func (n *NoopGitOps) OpenPR(ctx context.Context, req PRRequest) (string, error) {
	return "", nil
}
