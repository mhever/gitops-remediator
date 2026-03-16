package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mhever/gitops-remediator/internal/metrics"
)

func TestMetricVarsAreNonNil(t *testing.T) {
	if metrics.FailuresDetected == nil {
		t.Error("FailuresDetected is nil")
	}
	if metrics.PRsOpened == nil {
		t.Error("PRsOpened is nil")
	}
	if metrics.Escalations == nil {
		t.Error("Escalations is nil")
	}
	if metrics.DiagnosticianLatency == nil {
		t.Error("DiagnosticianLatency is nil")
	}
	if metrics.DiagnosticianErrors == nil {
		t.Error("DiagnosticianErrors is nil")
	}
}

func TestHandlerReturnsNonNil(t *testing.T) {
	h := metrics.Handler()
	if h == nil {
		t.Error("Handler() returned nil")
	}
	// Verify it satisfies the http.Handler interface.
	var _ http.Handler = h
}

func TestFailuresDetected_LabelsSeparate(t *testing.T) {
	// Inc OOMKilled twice and CrashLoopBackOff once.
	metrics.FailuresDetected.With("OOMKilled").Inc()
	metrics.FailuresDetected.With("OOMKilled").Inc()
	metrics.FailuresDetected.With("CrashLoopBackOff").Inc()

	oomCount := metrics.FailuresDetected.Value("OOMKilled")
	clbCount := metrics.FailuresDetected.Value("CrashLoopBackOff")

	// Because package-level vars are shared across tests we just assert relative values.
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
	before := metrics.PRsOpened.Count()
	metrics.PRsOpened.Inc()
	after := metrics.PRsOpened.Count()
	if after != before+1 {
		t.Errorf("PRsOpened count = %v, want %v", after, before+1)
	}
}

func TestDiagnosticianLatency_ObserveNoPanic(t *testing.T) {
	// Should not panic.
	metrics.DiagnosticianLatency.Observe(0.5)
}

func TestRegister_NoPanic(t *testing.T) {
	// Should not panic.
	metrics.Register()
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
	if ct != "text/plain; version=0.0.4" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/plain; version=0.0.4")
	}
}
