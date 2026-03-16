package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mhever/gitops-remediator/config"
	"github.com/mhever/gitops-remediator/internal/collector"
	"github.com/mhever/gitops-remediator/internal/diagnostician"
	"github.com/mhever/gitops-remediator/internal/gitops"
	"github.com/mhever/gitops-remediator/internal/metrics"
	"github.com/mhever/gitops-remediator/internal/patcher"
	"github.com/mhever/gitops-remediator/internal/watcher"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	slog.Info("starting gitops-remediator", "namespace", cfg.Namespace, "metrics_addr", cfg.MetricsAddr)

	metrics.Register()

	// Wire up stub implementations (replaced phase by phase).
	var w watcher.Watcher = &watcher.NoopWatcher{}
	var c collector.Collector = &collector.NoopCollector{}
	var d diagnostician.Diagnostician = &diagnostician.NoopDiagnostician{}
	var p patcher.Patcher = &patcher.NoopPatcher{}
	var g gitops.GitOps = &gitops.NoopGitOps{}

	// Suppress "declared and not used" — stubs will be wired in later phases.
	_ = c
	_ = d
	_ = p
	_ = g

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if err := w.Run(ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		slog.Error("watcher exited with error", "error", err)
		os.Exit(1)
	}

	slog.Info("shutting down")
}
