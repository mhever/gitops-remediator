package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestLoad_OptionalVarsOverrideDefaults(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "x")
	t.Setenv("GITHUB_TOKEN", "x")
	t.Setenv("GITOPS_REPO", "owner/repo")
	t.Setenv("REMEDIATOR_NAMESPACE", "custom-ns")
	t.Setenv("DIAGNOSTICIAN_LOG_PATH", "/tmp/diag.log")
	t.Setenv("METRICS_ADDR", ":8080")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Namespace != "custom-ns" {
		t.Errorf("Namespace = %q, want %q", cfg.Namespace, "custom-ns")
	}
	if cfg.DiagnosticianLogPath != "/tmp/diag.log" {
		t.Errorf("DiagnosticianLogPath = %q, want %q", cfg.DiagnosticianLogPath, "/tmp/diag.log")
	}
	if cfg.MetricsAddr != ":8080" {
		t.Errorf("MetricsAddr = %q, want %q", cfg.MetricsAddr, ":8080")
	}
}

func TestLoad_Defaults(t *testing.T) {
	// Only set required vars; optional vars should use defaults.
	t.Setenv("DEEPSEEK_API_KEY", "sk-test-key")
	t.Setenv("GITHUB_TOKEN", "ghp-test-token")
	t.Setenv("GITOPS_REPO", "owner/repo")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if cfg.Namespace != "remediator-test" {
		t.Errorf("Namespace = %q, want default %q", cfg.Namespace, "remediator-test")
	}
	if cfg.DiagnosticianLogPath != "/var/log/remediator/diagnostician.log" {
		t.Errorf("DiagnosticianLogPath = %q, want default %q", cfg.DiagnosticianLogPath, "/var/log/remediator/diagnostician.log")
	}
	if cfg.MetricsAddr != ":9090" {
		t.Errorf("MetricsAddr = %q, want default %q", cfg.MetricsAddr, ":9090")
	}
}

func TestLoad_MissingDeepSeekAPIKey(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp-test-token")
	t.Setenv("GITOPS_REPO", "owner/repo")
	// DEEPSEEK_API_KEY intentionally not set

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when DEEPSEEK_API_KEY is missing, got nil")
	}
	if !strings.Contains(err.Error(), "DEEPSEEK_API_KEY") {
		t.Errorf("expected error message to contain %q, got: %v", "DEEPSEEK_API_KEY", err)
	}
}

func TestLoad_MissingGitHubToken(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test-key")
	t.Setenv("GITOPS_REPO", "owner/repo")
	// GITHUB_TOKEN intentionally not set

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when GITHUB_TOKEN is missing, got nil")
	}
	if !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Errorf("expected error message to contain %q, got: %v", "GITHUB_TOKEN", err)
	}
}

func TestLoad_MissingGitOpsRepo(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test-key")
	t.Setenv("GITHUB_TOKEN", "ghp-test-token")
	// GITOPS_REPO intentionally not set

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when GITOPS_REPO is missing, got nil")
	}
	if !strings.Contains(err.Error(), "GITOPS_REPO") {
		t.Errorf("expected error message to contain %q, got: %v", "GITOPS_REPO", err)
	}
}

func TestLogValue_RedactsSecrets(t *testing.T) {
	t.Setenv("REMEDIATOR_NAMESPACE", "my-namespace")
	t.Setenv("DEEPSEEK_API_KEY", "sk-secret123")
	t.Setenv("GITHUB_TOKEN", "ghp_faketoken456")
	t.Setenv("GITOPS_REPO", "owner/repo")
	t.Setenv("DIAGNOSTICIAN_LOG_PATH", "/tmp/diag.log")
	t.Setenv("METRICS_ADDR", ":8080")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	logVal := fmt.Sprintf("%v", cfg.LogValue())

	if strings.Contains(logVal, "sk-secret123") {
		t.Errorf("LogValue() must not contain the DeepSeekAPIKey secret, got: %s", logVal)
	}
	if strings.Contains(logVal, "ghp_faketoken456") {
		t.Errorf("LogValue() must not contain the GitHubToken secret, got: %s", logVal)
	}
	if !strings.Contains(logVal, "[REDACTED]") {
		t.Errorf("LogValue() must contain \"[REDACTED]\", got: %s", logVal)
	}
}
