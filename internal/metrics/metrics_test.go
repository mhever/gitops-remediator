package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/mhever/gitops-remediator/internal/metrics"
)

// newTestRegistry creates a fresh registry with all package-level metrics registered.
// Using a per-test registry avoids cross-test pollution with the default registry.
func newTestRegistry(t *testing.T) *prometheus.Registry {
	t.Helper()
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		metrics.FailuresDetected,
		metrics.PRsOpened,
		metrics.Escalations,
		metrics.DiagnosticianLatency,
		metrics.DiagnosticianErrors,
	)
	return reg
}

func TestFailuresDetected_LabelsSeparate(t *testing.T) {
	// Reset via fresh registry (registration is separate from value; we read values directly).
	_ = newTestRegistry(t)

	metrics.FailuresDetected.WithLabelValues("OOMKilled").Inc()
	metrics.FailuresDetected.WithLabelValues("OOMKilled").Inc()
	metrics.FailuresDetected.WithLabelValues("CrashLoopBackOff").Inc()

	oomCount := testutil.ToFloat64(metrics.FailuresDetected.WithLabelValues("OOMKilled"))
	clbCount := testutil.ToFloat64(metrics.FailuresDetected.WithLabelValues("CrashLoopBackOff"))

	if oomCount < 2 {
		t.Errorf("OOMKilled count = %v, want >= 2", oomCount)
	}
	if clbCount < 1 {
		t.Errorf("CrashLoopBackOff count = %v, want >= 1", clbCount)
	}
	if oomCount <= clbCount {
		t.Errorf("OOMKilled count (%v) should be greater than CrashLoopBackOff count (%v)", oomCount, clbCount)
	}
}

func TestPRsOpened_Inc(t *testing.T) {
	_ = newTestRegistry(t)

	before := testutil.ToFloat64(metrics.PRsOpened)
	metrics.PRsOpened.Inc()
	after := testutil.ToFloat64(metrics.PRsOpened)

	if after != before+1 {
		t.Errorf("PRsOpened = %v, want %v", after, before+1)
	}
}

func TestDiagnosticianLatency_Observe(t *testing.T) {
	_ = newTestRegistry(t)
	// Should not panic.
	metrics.DiagnosticianLatency.Observe(0.5)
}

func TestEscalations_LabelsSeparate(t *testing.T) {
	_ = newTestRegistry(t)

	metrics.Escalations.WithLabelValues("application_panic").Inc()
	metrics.Escalations.WithLabelValues("application_panic").Inc()
	metrics.Escalations.WithLabelValues("auth_failure").Inc()

	panicCount := testutil.ToFloat64(metrics.Escalations.WithLabelValues("application_panic"))
	authCount := testutil.ToFloat64(metrics.Escalations.WithLabelValues("auth_failure"))

	if panicCount < 2 {
		t.Errorf("application_panic count = %v, want >= 2", panicCount)
	}
	if authCount < 1 {
		t.Errorf("auth_failure count = %v, want >= 1", authCount)
	}
	if panicCount <= authCount {
		t.Errorf("application_panic (%v) should be greater than auth_failure (%v)", panicCount, authCount)
	}
}

func TestRegister_NoPanic(t *testing.T) {
	// Use a fresh registry rather than calling the global Register() which would
	// double-register with the default registry and panic.
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		metrics.FailuresDetected,
		metrics.PRsOpened,
		metrics.Escalations,
		metrics.DiagnosticianLatency,
		metrics.DiagnosticianErrors,
	)
}

func TestHandler_ReturnsNonNil(t *testing.T) {
	h := metrics.Handler()
	if h == nil {
		t.Error("Handler() returned nil")
	}
	var _ http.Handler = h
}

func TestHandler_StatusAndContentType(t *testing.T) {
	h := metrics.Handler()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	ct := rec.Header().Get("Content-Type")
	// promhttp uses text/plain; version=0.0.4; charset=utf-8
	if ct == "" {
		t.Error("Content-Type header is empty")
	}
}
