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

	// Default to noop implementations; overridden below if k8s is available.
	var col collector.Collector = &collector.NoopCollector{}
	var diag diagnostician.Diagnostician = &diagnostician.NoopDiagnostician{}

	// Try to build a k8s client. Fall back to NoopWatcher if unavailable (e.g. in CI).
	// Config resolution order: in-cluster -> $KUBECONFIG env var -> ~/.kube/config (clientcmd default)
	var w watcher.Watcher
	var evCh chan watcher.FailureEvent
	var k8sClient kubernetes.Interface
	isK8sWatcher := false
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		kubeconfigPath := os.Getenv("KUBECONFIG")
		restCfg, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	}
	if err != nil {
		slog.Warn("could not build k8s client config, using NoopWatcher", "error", err)
		w = &watcher.NoopWatcher{}
	} else {
		client, clientErr := kubernetes.NewForConfig(restCfg)
		if clientErr != nil {
			slog.Warn("could not create k8s client, using NoopWatcher", "error", clientErr)
			w = &watcher.NoopWatcher{}
		} else {
			k8sClient = client
			evCh = make(chan watcher.FailureEvent, 100)
			w = watcher.NewK8sWatcher(k8sClient, cfg.Namespace, evCh, slog.Default())
			isK8sWatcher = true

			col = collector.NewK8sCollector(k8sClient, slog.Default())
			diag = diagnostician.NewDeepSeekDiagnostician(
				cfg.DeepSeekAPIKey,
				cfg.DiagnosticianLogPath,
				nil,
				slog.Default(),
			)
		}
	}

	p := patcher.NewManifestPatcher()
	g := gitops.NewGitHubGitOps(cfg.GitOpsRepo, cfg.GitHubToken, p, slog.Default())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	var pipelineWg sync.WaitGroup
	if isK8sWatcher {
		pipelineWg.Add(1)
		go func() {
			defer pipelineWg.Done()
			for e := range evCh {
				slog.Info("failure event detected",
					"type", e.FailureType,
					"namespace", e.Namespace,
					"pod", e.PodName,
					"container", e.ContainerName,
					"reason", e.RawReason,
				)

				metrics.FailuresDetected.WithLabelValues(string(e.FailureType)).Inc()

				bundle, err := col.Collect(ctx, e)
				if err != nil {
					slog.Error("collector failed", "error", err, "pod", e.PodName)
					continue
				}

				start := time.Now()
				diagnosis, err := diag.Diagnose(ctx, *bundle)
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

	runErr := w.Run(ctx)
	if isK8sWatcher {
		close(evCh)
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
