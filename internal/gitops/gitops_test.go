package gitops

import (
	"context"
	"testing"

	"github.com/mhever/gitops-remediator/internal/diagnostician"
	"github.com/mhever/gitops-remediator/internal/watcher"
)

func TestNoopGitOps_OpenPRReturnsEmptyAndNilError(t *testing.T) {
	g := &NoopGitOps{}
	req := PRRequest{
		Diag: diagnostician.Diagnosis{
			FailureType: "OOMKilled",
			Remediable:  true,
		},
		Event: watcher.FailureEvent{
			Namespace: "default",
			PodName:   "test-pod",
		},
		Diff: "some diff",
	}

	url, err := g.OpenPR(context.Background(), req)
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if url != "" {
		t.Errorf("expected empty string, got: %q", url)
	}
}
