// Package metrics exposes Prometheus metrics for the remediator.
// Call Register() once at startup to register all metrics with the default registry,
// then use Handler() to serve the /metrics endpoint.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// FailuresDetected counts failure events by type label.
var FailuresDetected *prometheus.CounterVec

// PRsOpened counts remediation PRs successfully opened.
var PRsOpened prometheus.Counter

// Escalations counts non-remediable escalations by reason label.
var Escalations *prometheus.CounterVec

// DiagnosticianLatency records DeepSeek R1 API call latency in seconds.
var DiagnosticianLatency prometheus.Histogram

// DiagnosticianErrors counts errors returned by the Diagnostician.
var DiagnosticianErrors prometheus.Counter

func init() {
	FailuresDetected = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "remediator_failures_detected_total",
		Help: "Total failure events detected by type.",
	}, []string{"type"})

	PRsOpened = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "remediator_prs_opened_total",
		Help: "Total remediation PRs successfully opened.",
	})

	Escalations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "remediator_escalations_total",
		Help: "Total non-remediable escalations by reason.",
	}, []string{"reason"})

	DiagnosticianLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "remediator_diagnostician_latency_seconds",
		Help:    "Latency of DeepSeek R1 API calls in seconds.",
		Buckets: prometheus.DefBuckets,
	})

	DiagnosticianErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "remediator_diagnostician_errors_total",
		Help: "Total errors returned by the Diagnostician.",
	})
}

// Register registers all metrics with the default Prometheus registry.
// It must be called once at process startup before any metric is incremented.
func Register() {
	prometheus.MustRegister(
		FailuresDetected,
		PRsOpened,
		Escalations,
		DiagnosticianLatency,
		DiagnosticianErrors,
	)
}

// Handler returns the Prometheus HTTP handler for the /metrics endpoint.
func Handler() http.Handler {
	return promhttp.Handler()
}
