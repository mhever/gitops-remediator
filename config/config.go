package config

import (
	"errors"
	"log/slog"
	"os"
)

// Config holds all runtime configuration loaded from environment variables.
type Config struct {
	Namespace            string // REMEDIATOR_NAMESPACE, default: "remediator-test"
	DeepSeekAPIKey       string // DEEPSEEK_API_KEY, required
	GitHubToken          string // GITHUB_TOKEN, required
	GitOpsRepo           string // GITOPS_REPO, required (e.g. "owner/repo")
	DiagnosticianLogPath string // DIAGNOSTICIAN_LOG_PATH, default: "/var/log/remediator/diagnostician.log"
	MetricsAddr          string // METRICS_ADDR, default: ":9090"
}

// Load reads configuration from environment variables and returns a Config.
// Returns an error if any required variable is missing.
func Load() (*Config, error) {
	cfg := &Config{
		Namespace:            getEnvOrDefault("REMEDIATOR_NAMESPACE", "remediator-test"),
		DeepSeekAPIKey:       os.Getenv("DEEPSEEK_API_KEY"),
		GitHubToken:          os.Getenv("GITHUB_TOKEN"),
		GitOpsRepo:           os.Getenv("GITOPS_REPO"),
		DiagnosticianLogPath: getEnvOrDefault("DIAGNOSTICIAN_LOG_PATH", "/var/log/remediator/diagnostician.log"),
		MetricsAddr:          getEnvOrDefault("METRICS_ADDR", ":9090"),
	}

	if cfg.DeepSeekAPIKey == "" {
		return nil, errors.New("DEEPSEEK_API_KEY is required but not set")
	}
	if cfg.GitHubToken == "" {
		return nil, errors.New("GITHUB_TOKEN is required but not set")
	}
	if cfg.GitOpsRepo == "" {
		return nil, errors.New("GITOPS_REPO is required but not set")
	}

	return cfg, nil
}

// LogValue implements slog.LogValuer so that Config can be safely logged
// without leaking credential fields (DeepSeekAPIKey, GitHubToken).
func (c *Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("Namespace", c.Namespace),
		slog.String("MetricsAddr", c.MetricsAddr),
		slog.String("DiagnosticianLogPath", c.DiagnosticianLogPath),
		slog.String("GitOpsRepo", c.GitOpsRepo),
		slog.String("DeepSeekAPIKey", "[REDACTED]"),
		slog.String("GitHubToken", "[REDACTED]"),
	)
}

func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
