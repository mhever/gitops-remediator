package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
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

	// Default to noop implementations; overridden below if k8s is available.
	var col collector.Collector = &collector.NoopCollector{}
	var diag diagnostician.Diagnostician = &diagnostician.NoopDiagnostician{}

	// Try to build a k8s client. Fall back to NoopWatcher if unavailable (e.g. in CI).
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

	if !isK8sWatcher {
		diag = diagnostician.NewDeepSeekDiagnostician(
			cfg.DeepSeekAPIKey,
			cfg.DiagnosticianLogPath,
			nil,
			slog.Default(),
		)
	}

	p := patcher.NewManifestPatcher()
	g := gitops.NewGitHubGitOps(cfg.GitOpsRepo, cfg.GitHubToken, p, slog.Default())

	if isK8sWatcher {
		go func() {
			for e := range evCh {
				slog.Info("failure event detected",
					"type", e.FailureType,
					"namespace", e.Namespace,
					"pod", e.PodName,
					"container", e.ContainerName,
					"reason", e.RawReason,
				)

				bundle, err := col.Collect(context.Background(), e)
				if err != nil {
					slog.Error("collector failed", "error", err, "pod", e.PodName)
					continue
				}

				diagnosis, err := diag.Diagnose(context.Background(), *bundle)
				if err != nil {
					slog.Error("diagnostician failed", "error", err, "pod", e.PodName)
					continue
				}

				if !diagnosis.Remediable {
					// diagnostician already logged the escalation
					continue
				}

				prURL, err := g.OpenPR(context.Background(), gitops.PRRequest{Diag: *diagnosis, Event: e})
				if err != nil {
					slog.Error("failed to open PR", "error", err, "pod", e.PodName)
					continue
				}

				slog.Info("remediation PR opened", "url", prURL, "pod", e.PodName, "type", diagnosis.FailureType)
			}
		}()
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

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
	if runErr != nil && !errors.Is(runErr, context.Canceled) && !errors.Is(runErr, context.DeadlineExceeded) {
		slog.Error("watcher exited with error", "error", runErr)
		os.Exit(1)
	}

	slog.Info("shutting down")
}
