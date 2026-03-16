package gitops

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/mhever/gitops-remediator/internal/patcher"
)

// GitHubGitOps implements GitOps using system git and the GitHub REST API.
type GitHubGitOps struct {
	gitOpsRepo    string // "owner/repo"
	githubToken   string
	patcher       patcher.Patcher
	baseGitHubURL string // default "https://api.github.com"; overridable in tests
	cloneURL      string // if non-empty, overrides the derived clone URL (for tests)
	httpClient    *http.Client
	logger        *slog.Logger
}

// NewGitHubGitOps creates a GitHubGitOps. httpClient may be nil (default used).
func NewGitHubGitOps(gitOpsRepo, githubToken string, p patcher.Patcher, logger *slog.Logger) *GitHubGitOps {
	return &GitHubGitOps{
		gitOpsRepo:    gitOpsRepo,
		githubToken:   githubToken,
		patcher:       p,
		baseGitHubURL: "https://api.github.com",
		httpClient:    &http.Client{Timeout: 60 * time.Second},
		logger:        logger,
	}
}

var _ GitOps = (*GitHubGitOps)(nil)

// OpenPR clones the GitOps repo, applies the patch, commits to a new branch,
// pushes, and opens a GitHub PR. Returns the PR HTML URL.
func (g *GitHubGitOps) OpenPR(ctx context.Context, req PRRequest) (string, error) {
	cloneURL := g.cloneURL
	if cloneURL == "" {
		cloneURL = fmt.Sprintf("https://%s@github.com/%s.git", g.githubToken, g.gitOpsRepo)
	}

	tmpDir, err := os.MkdirTemp("", "gitops-remediator-*")
	if err != nil {
		return "", fmt.Errorf("gitops: create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Clone into tmpDir
	if _, err := runGit(ctx, "", "clone", cloneURL, tmpDir); err != nil {
		return "", fmt.Errorf("gitops: clone: %w", err)
	}

	branchName := fmt.Sprintf("remediation/%s-%s-%d",
		req.Diag.FailureType,
		req.Event.PodName,
		time.Now().Unix(),
	)

	// Create branch
	if _, err := runGit(ctx, tmpDir, "checkout", "-b", branchName); err != nil {
		return "", fmt.Errorf("gitops: create branch: %w", err)
	}

	// Apply patch
	result, err := g.patcher.Apply(ctx, tmpDir, req.Diag, req.Event)
	if err != nil {
		return "", fmt.Errorf("gitops: apply patch: %w", err)
	}

	// Stage the patched file
	if _, err := runGit(ctx, tmpDir, "add", result.FilePath); err != nil {
		return "", fmt.Errorf("gitops: git add: %w", err)
	}

	// Commit
	commitMsg := fmt.Sprintf("fix: auto-remediate %s for %s", req.Diag.FailureType, req.Event.PodName)
	if _, err := runGit(ctx, tmpDir,
		"-c", "user.email=remediator@gitops-remediator",
		"-c", "user.name=gitops-remediator",
		"commit", "-m", commitMsg,
	); err != nil {
		return "", fmt.Errorf("gitops: git commit: %w", err)
	}

	// Push
	if _, err := runGit(ctx, tmpDir, "push", "origin", branchName); err != nil {
		return "", fmt.Errorf("gitops: git push: %w", err)
	}

	// Open PR
	prURL, err := g.createPR(ctx, branchName, req, result.Diff)
	if err != nil {
		return "", fmt.Errorf("gitops: create PR: %w", err)
	}

	return prURL, nil
}
