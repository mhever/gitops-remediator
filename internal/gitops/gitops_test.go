package gitops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhever/gitops-remediator/internal/diagnostician"
	"github.com/mhever/gitops-remediator/internal/patcher"
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
	}

	url, err := g.OpenPR(context.Background(), req)
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if url != "" {
		t.Errorf("expected empty string, got: %q", url)
	}
}

// setupBareRepo creates a bare git repo at remotePath and populates it with
// the OOM fixture manifest at test/deployment.yaml, then returns the path.
func setupBareRepo(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	remotePath := t.TempDir()
	srcPath := t.TempDir()

	// Init bare repo
	if _, err := runGit(ctx, "", "init", "--bare", "--initial-branch=main", remotePath); err != nil {
		// Try without --initial-branch (older git)
		if _, err2 := runGit(ctx, "", "init", "--bare", remotePath); err2 != nil {
			t.Fatalf("git init --bare: %v", err)
		}
	}

	// Clone from bare remote into srcPath
	if _, err := runGit(ctx, "", "clone", remotePath, srcPath); err != nil {
		t.Fatalf("git clone: %v", err)
	}

	// Copy the OOM fixture
	if err := os.MkdirAll(filepath.Join(srcPath, "test"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	oomFixture := "../patcher/testdata/deployment-oom.yaml"
	content, err := os.ReadFile(oomFixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	dst := filepath.Join(srcPath, "test", "deployment.yaml")
	if err := os.WriteFile(dst, content, 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// Initial commit and push
	if _, err := runGit(ctx, srcPath, "add", "."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := runGit(ctx, srcPath,
		"-c", "user.email=test@test",
		"-c", "user.name=test",
		"commit", "-m", "initial",
	); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	if _, err := runGit(ctx, srcPath, "push", "origin", "HEAD:main"); err != nil {
		t.Fatalf("git push: %v", err)
	}

	return remotePath
}

func TestGitHubGitOps_OpenPR_Success(t *testing.T) {
	ctx := context.Background()
	remotePath := setupBareRepo(t)

	// httptest server for GitHub API
	var authHeader string
	var requestBody map[string]string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/repos/owner/testrepo/pulls" {
			authHeader = r.Header.Get("Authorization")
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &requestBody)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"html_url": "https://github.com/owner/testrepo/pull/1"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	g := NewGitHubGitOps("owner/testrepo", "test-token", patcher.NewManifestPatcher(), logger)
	g.cloneURL = remotePath
	g.baseGitHubURL = ts.URL

	req := PRRequest{
		Diag: diagnostician.Diagnosis{
			FailureType: "OOMKilled",
			PatchType:   "memory_limit",
			PatchValue:  "256Mi",
			Remediable:  true,
		},
		Event: watcher.FailureEvent{
			PodName:   "sample-app-abc12-xyz99",
			Namespace: "remediator-test",
		},
	}

	prURL, err := g.OpenPR(ctx, req)
	if err != nil {
		t.Fatalf("OpenPR: %v", err)
	}

	if prURL != "https://github.com/owner/testrepo/pull/1" {
		t.Errorf("expected PR URL 'https://github.com/owner/testrepo/pull/1', got %q", prURL)
	}

	if authHeader != "token test-token" {
		t.Errorf("expected Authorization 'token test-token', got %q", authHeader)
	}

	// Verify the remote has the remediation branch
	out, err := runGit(ctx, "", "ls-remote", "--heads", remotePath)
	if err != nil {
		t.Fatalf("git ls-remote: %v", err)
	}
	if !strings.Contains(string(out), "remediation/OOMKilled-sample-app") {
		t.Errorf("expected remote to have a remediation/OOMKilled-sample-app-* branch, got:\n%s", string(out))
	}
}

func TestGitHubGitOps_PatcherError(t *testing.T) {
	ctx := context.Background()
	remotePath := setupBareRepo(t)

	apiCalled := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalled = true
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Use NoopPatcher — then override with an error patcher
	g := NewGitHubGitOps("owner/testrepo", "test-token", &errorPatcher{}, logger)
	g.cloneURL = remotePath
	g.baseGitHubURL = ts.URL

	req := PRRequest{
		Diag: diagnostician.Diagnosis{
			FailureType: "OOMKilled",
			PatchType:   "memory_limit",
			PatchValue:  "256Mi",
			Remediable:  true,
		},
		Event: watcher.FailureEvent{
			PodName:   "sample-app-abc12-xyz99",
			Namespace: "remediator-test",
		},
	}

	_, err := g.OpenPR(ctx, req)
	if err == nil {
		t.Error("expected error when patcher fails, got nil")
	}
	if apiCalled {
		t.Error("expected GitHub API to NOT be called when patcher fails")
	}
}

func TestGitHubGitOps_GitHubAPIError(t *testing.T) {
	ctx := context.Background()
	remotePath := setupBareRepo(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprintf(w, `{"message": "Validation Failed"}`)
	}))
	defer ts.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	g := NewGitHubGitOps("owner/testrepo", "test-token", patcher.NewManifestPatcher(), logger)
	g.cloneURL = remotePath
	g.baseGitHubURL = ts.URL

	req := PRRequest{
		Diag: diagnostician.Diagnosis{
			FailureType: "OOMKilled",
			PatchType:   "memory_limit",
			PatchValue:  "256Mi",
			Remediable:  true,
		},
		Event: watcher.FailureEvent{
			PodName:   "sample-app-abc12-xyz99",
			Namespace: "remediator-test",
		},
	}

	_, err := g.OpenPR(ctx, req)
	if err == nil {
		t.Error("expected error when GitHub API returns 422, got nil")
	}
}

// errorPatcher always returns an error.
type errorPatcher struct{}

func (e *errorPatcher) Apply(ctx context.Context, repoDir string, diag diagnostician.Diagnosis, event watcher.FailureEvent) (*patcher.PatchResult, error) {
	return nil, fmt.Errorf("patcher: simulated error")
}
