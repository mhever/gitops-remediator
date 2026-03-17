package gitops

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

var _ Pinger = (*GitHubGitOps)(nil)

// Ping verifies that the GitHub API is reachable, the configured repository
// exists, and the token has at least read access to it.
// It performs a single GET /repos/{owner}/{repo} request.
func (g *GitHubGitOps) Ping(ctx context.Context) error {
	url := fmt.Sprintf("%s/repos/%s", g.baseGitHubURL, g.gitOpsRepo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("github ping: build request: %w", err)
	}
	req.Header.Set("Authorization", "token "+g.githubToken)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("github ping: http request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return fmt.Errorf("github ping: invalid or expired token (401)")
	case http.StatusNotFound:
		return fmt.Errorf("github ping: repository %q not found or token lacks access (404)", g.gitOpsRepo)
	default:
		snippet := body
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return fmt.Errorf("github ping: unexpected status %d: %s", resp.StatusCode, string(snippet))
	}
}
