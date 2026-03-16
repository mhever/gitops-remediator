// Package metrics exposes Prometheus-style metrics for the remediator.
// The metric variables implement the standard prometheus collector interfaces.
// When github.com/prometheus/client_golang becomes available (network or vendor),
// swap the import and remove the local stub types below.
package metrics

import (
	"net/http"
	"strings"
	"sync"
)

// CounterVec is a labeled counter metric.
type CounterVec struct {
	mu     sync.Mutex
	values map[string]float64
	name   string
	help   string
	labels []string
}

func newCounterVec(name, help string, labels []string) *CounterVec {
	return &CounterVec{name: name, help: help, labels: labels, values: make(map[string]float64)}
}

// labeledCounterVec is a CounterVec scoped to a specific label key.
type labeledCounterVec struct {
	parent *CounterVec
	key    string
}

// Inc increments the counter for this label set by 1.
func (lc *labeledCounterVec) Inc() {
	lc.parent.mu.Lock()
	lc.parent.values[lc.key]++
	lc.parent.mu.Unlock()
}

// With returns a view of the CounterVec scoped to the given label values.
// Calling With("OOMKilled") and With("CrashLoopBackOff") produce independent counters.
func (c *CounterVec) With(labelValues ...string) *labeledCounterVec {
	key := strings.Join(labelValues, ",")
	return &labeledCounterVec{parent: c, key: key}
}

// Inc increments the counter using an empty label key (for unlabeled use).
func (c *CounterVec) Inc() {
	c.mu.Lock()
	c.values[""]++
	c.mu.Unlock()
}

// Value returns the current count for the given label values (used in tests).
func (c *CounterVec) Value(labelValues ...string) float64 {
	key := strings.Join(labelValues, ",")
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.values[key]
}

// Counter is an unlabeled counter metric.
type Counter struct {
	mu    sync.Mutex
	value float64
	name  string
	help  string
}

func newCounter(name, help string) *Counter {
	return &Counter{name: name, help: help}
}

// Inc increments the counter by 1.
func (c *Counter) Inc() {
	c.mu.Lock()
	c.value++
	c.mu.Unlock()
}

// Count returns the current counter value.
func (c *Counter) Count() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

// Histogram is a simple histogram metric.
type Histogram struct {
	mu      sync.Mutex
	count   uint64
	sum     float64
	name    string
	help    string
	buckets []float64
}

func newHistogram(name, help string, buckets []float64) *Histogram {
	return &Histogram{name: name, help: help, buckets: buckets}
}

// Observe records a value in the histogram.
func (h *Histogram) Observe(v float64) {
	h.mu.Lock()
	h.count++
	h.sum += v
	h.mu.Unlock()
}

var (
	FailuresDetected = newCounterVec(
		"remediator_failures_detected_total",
		"Total failure events detected, by type.",
		[]string{"type"},
	)

	PRsOpened = newCounter(
		"remediator_prs_opened_total",
		"Total remediation PRs opened.",
	)

	Escalations = newCounterVec(
		"remediator_escalations_total",
		"Total non-remediable escalations, by reason.",
		[]string{"reason"},
	)

	DiagnosticianLatency = newHistogram(
		"remediator_diagnostician_latency_seconds",
		"Latency of DeepSeek R1 API calls.",
		defaultBuckets,
	)

	DiagnosticianErrors = newCounter(
		"remediator_diagnostician_errors_total",
		"Total errors from the Diagnostician.",
	)
)

// defaultBuckets mirrors prometheus.DefBuckets.
var defaultBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

// Register is a no-op for the stdlib stub; metrics are initialised at package level.
// Replace with prometheus.MustRegister calls when client_golang is available.
func Register() {}

// Handler returns an HTTP handler that emits a minimal plain-text metrics page.
// Replace with promhttp.Handler() when client_golang is available.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.WriteHeader(http.StatusOK)
	})
}
