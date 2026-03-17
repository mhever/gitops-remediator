package gitops

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
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
	// Compute the branch name first so we can check for duplicates before
	// doing any expensive git work.
	branchName := fmt.Sprintf("remediation/%s-%s-%d",
		req.Diag.FailureType,
		req.Event.PodName,
		req.Event.Timestamp.Unix(),
	)

	// Check whether this branch already exists on GitHub. A 200 means another
	// pipeline run already handled this event — skip it as a duplicate.
	exists, err := g.branchExists(ctx, branchName)
	if err != nil {
		// A flaky check must not block remediation — log a warning and proceed.
		g.logger.Warn("branch existence check failed, proceeding with remediation",
			"branch", branchName, "error", err)
	} else if exists {
		return "", ErrBranchExists
	}

	// Write a temporary git credentials file so the token is never embedded in
	// process arguments (which would be visible in /proc/<pid>/cmdline).
	var credEnv []string
	cloneURL := fmt.Sprintf("https://github.com/%s.git", g.gitOpsRepo)
	if g.cloneURL != "" {
		// Test override: local path, no credentials needed.
		cloneURL = g.cloneURL
	} else {
		credFile, err := os.CreateTemp("", "git-creds-*")
		if err != nil {
			return "", fmt.Errorf("gitops: creating credential file: %w", err)
		}
		defer os.Remove(credFile.Name())
		_, err = fmt.Fprintf(credFile, "https://x-access-token:%s@github.com\n", g.githubToken)
		credFile.Close()
		if err != nil {
			return "", fmt.Errorf("gitops: writing credential file: %w", err)
		}
		credEnv = []string{
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=credential.helper",
			"GIT_CONFIG_VALUE_0=store --file=" + credFile.Name(),
		}
	}

	tmpDir, err := os.MkdirTemp("", "gitops-remediator-*")
	if err != nil {
		return "", fmt.Errorf("gitops: create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Clone into tmpDir
	if _, err := runGit(ctx, "", credEnv, "clone", cloneURL, tmpDir); err != nil {
		return "", fmt.Errorf("gitops: clone: %w", err)
	}

	// Create branch.
	if _, err := runGit(ctx, tmpDir, nil, "checkout", "-b", branchName); err != nil {
		return "", fmt.Errorf("gitops: create branch: %w", err)
	}

	// Apply patch
	result, err := g.patcher.Apply(ctx, tmpDir, req.Diag, req.Event)
	if err != nil {
		return "", fmt.Errorf("gitops: apply patch: %w", err)
	}

	// Stage the patched file
	if _, err := runGit(ctx, tmpDir, nil, "add", result.FilePath); err != nil {
		return "", fmt.Errorf("gitops: git add: %w", err)
	}

	// Verify something was actually staged. If the proposed patch value is
	// identical to what is already in the repo (e.g. kubectl set image was used
	// to force-set a broken image on the cluster without touching the GitOps
	// repo), git commit would fail with "nothing to commit". Detect this early
	// and return ErrNothingToCommit so the caller can log WARN and skip.
	staged, err := runGit(ctx, tmpDir, nil, "diff", "--cached", "--name-only")
	if err != nil {
		return "", fmt.Errorf("gitops: git diff cached: %w", err)
	}
	if strings.TrimSpace(string(staged)) == "" {
		return "", ErrNothingToCommit
	}

	// Commit
	commitMsg := fmt.Sprintf("fix: auto-remediate %s for %s", req.Diag.FailureType, req.Event.PodName)
	if _, err := runGit(ctx, tmpDir, nil,
		"-c", "user.email=remediator@gitops-remediator",
		"-c", "user.name=gitops-remediator",
		"commit", "-m", commitMsg,
	); err != nil {
		return "", fmt.Errorf("gitops: git commit: %w", err)
	}

	// Push
	if _, err := runGit(ctx, tmpDir, credEnv, "push", "origin", branchName); err != nil {
		return "", fmt.Errorf("gitops: git push: %w", err)
	}

	// Open PR
	prURL, err := g.createPR(ctx, branchName, req, result.Diff)
	if err != nil {
		return "", fmt.Errorf("gitops: create PR: %w", err)
	}

	return prURL, nil
}

// branchExists checks whether branchName already exists on the remote GitHub
// repository. Returns (true, nil) on 200, (false, nil) on 404, and
// (false, err) on any other status or transport error.
func (g *GitHubGitOps) branchExists(ctx context.Context, branchName string) (bool, error) {
	url := fmt.Sprintf("%s/repos/%s/branches/%s", g.baseGitHubURL, g.gitOpsRepo, branchName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Errorf("branchExists: build request: %w", err)
	}
	req.Header.Set("Authorization", "token "+g.githubToken)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("branchExists: http request: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(io.LimitReader(resp.Body, 1024))

	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("branchExists: unexpected status %d for branch %q", resp.StatusCode, branchName)
	}
}
