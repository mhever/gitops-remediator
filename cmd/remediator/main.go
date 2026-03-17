package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/mhever/gitops-remediator/config"
	"github.com/mhever/gitops-remediator/internal/collector"
	"github.com/mhever/gitops-remediator/internal/diagnostician"
	"github.com/mhever/gitops-remediator/internal/gitops"
	"github.com/mhever/gitops-remediator/internal/metrics"
	"github.com/mhever/gitops-remediator/internal/patcher"
	"github.com/mhever/gitops-remediator/internal/watcher"
)

// k8sSetup holds all components that require a live Kubernetes client.
type k8sSetup struct {
	w      watcher.Watcher
	evCh   chan watcher.FailureEvent
	col    collector.Collector
	diag   diagnostician.Diagnostician
	isLive bool // false when falling back to noop implementations
}

// buildK8sSetup attempts to build a live Kubernetes client and wire all
// k8s-dependent components. Falls back to noop implementations on any error.
// Config resolution order: in-cluster -> $KUBECONFIG env var -> ~/.kube/config (clientcmd default)
func buildK8sSetup(cfg *config.Config) k8sSetup {
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		restCfg, err = clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
	}
	if err != nil {
		slog.Warn("could not build k8s client config, using noop implementations", "error", err)
		return k8sSetup{
			w:    &watcher.NoopWatcher{},
			col:  &collector.NoopCollector{},
			diag: &diagnostician.NoopDiagnostician{},
		}
	}

	k8sClient, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		slog.Warn("could not create k8s client, using noop implementations", "error", err)
		return k8sSetup{
			w:    &watcher.NoopWatcher{},
			col:  &collector.NoopCollector{},
			diag: &diagnostician.NoopDiagnostician{},
		}
	}

	evCh := make(chan watcher.FailureEvent, 100)
	return k8sSetup{
		w:      watcher.NewK8sWatcher(k8sClient, cfg.Namespace, evCh, slog.Default()),
		evCh:   evCh,
		col:    collector.NewK8sCollector(k8sClient, slog.Default()),
		diag:   diagnostician.NewOpenRouterDiagnostician(cfg.OpenRouterAPIKey, cfg.DiagnosticianLogPath, nil, slog.Default()),
		isLive: true,
	}
}

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

	metricsServer := &http.Server{
		Addr:    cfg.MetricsAddr,
		Handler: metrics.Handler(),
	}
	go func() {
		slog.Info("metrics server listening", "addr", cfg.MetricsAddr)
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("metrics server failed", "error", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	setup := buildK8sSetup(cfg)

	// Verify OpenRouter connectivity before starting the event loop.
	if pinger, ok := setup.diag.(diagnostician.Pinger); ok {
		pingCtx, pingCancel := context.WithTimeout(ctx, 15*time.Second)
		defer pingCancel()
		if err := pinger.Ping(pingCtx); err != nil {
			slog.Warn("OpenRouter connectivity check failed — remediation will not work until this is resolved",
				"error", err)
		} else {
			slog.Info("OpenRouter connectivity check passed")
		}
	}

	p := patcher.NewManifestPatcher()
	g := gitops.NewGitHubGitOps(cfg.GitOpsRepo, cfg.GitHubToken, p, slog.Default())

	// Verify GitHub connectivity and repository access before starting the event loop.
	ghPingCtx, ghPingCancel := context.WithTimeout(ctx, 10*time.Second)
	defer ghPingCancel()
	if err := g.Ping(ghPingCtx); err != nil {
		slog.Warn("GitHub connectivity check failed — PR creation will not work until this is resolved",
			"error", err)
	} else {
		slog.Info("GitHub connectivity check passed", "repo", cfg.GitOpsRepo)
	}

	var pipelineWg sync.WaitGroup
	if setup.isLive {
		pipelineWg.Add(1)
		go func() {
			defer pipelineWg.Done()
			for e := range setup.evCh {
				slog.Info("failure event detected",
					"type", e.FailureType,
					"namespace", e.Namespace,
					"pod", e.PodName,
					"container", e.ContainerName,
					"reason", e.RawReason,
				)

				metrics.FailuresDetected.WithLabelValues(string(e.FailureType)).Inc()

				bundle, err := setup.col.Collect(ctx, e)
				if err != nil {
					slog.Error("collector failed", "error", err, "pod", e.PodName)
					continue
				}

				start := time.Now()
				diagnosis, err := setup.diag.Diagnose(ctx, *bundle)
				metrics.DiagnosticianLatency.Observe(time.Since(start).Seconds())
				if err != nil {
					slog.Error("diagnostician failed", "error", err, "pod", e.PodName)
					metrics.DiagnosticianErrors.Inc()
					continue
				}

				if !diagnosis.Remediable {
					reason := escalationReason(diagnosis.EscalationReason)
					metrics.Escalations.WithLabelValues(reason).Inc()
					continue
				}

				prURL, err := g.OpenPR(ctx, gitops.PRRequest{Diag: *diagnosis, Event: e})
				if err != nil {
					slog.Error("failed to open PR", "error", err, "pod", e.PodName)
					continue
				}

				metrics.PRsOpened.Inc()
				slog.Info("remediation PR opened", "url", prURL, "pod", e.PodName, "type", diagnosis.FailureType)
			}
		}()
	}

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				slog.Info("watcher running", "namespace", cfg.Namespace)
			case <-ctx.Done():
				return
			}
		}
	}()

	runErr := setup.w.Run(ctx)
	if setup.isLive {
		close(setup.evCh)
	}
	pipelineWg.Wait()
	if runErr != nil && !errors.Is(runErr, context.Canceled) && !errors.Is(runErr, context.DeadlineExceeded) {
		slog.Error("watcher exited with error", "error", runErr)
		os.Exit(1)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("metrics server shutdown error", "error", err)
	}

	slog.Info("shutdown complete")
}

// escalationReason normalises a free-form escalation reason string into one of
// the three Prometheus label values: "application_panic", "auth_failure", "unknown".
func escalationReason(reason string) string {
	lower := strings.ToLower(reason)
	switch {
	case strings.Contains(lower, "panic"):
		return "application_panic"
	case strings.Contains(lower, "auth") || strings.Contains(lower, "unauthorized"):
		return "auth_failure"
	default:
		return "unknown"
	}
}
