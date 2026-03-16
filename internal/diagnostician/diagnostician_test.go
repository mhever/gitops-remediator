package diagnostician

import (
	"context"
	"testing"

	"github.com/mhever/gitops-remediator/internal/collector"
)

func TestNoopDiagnostician_DiagnoseReturnsNonNilDiagnosis(t *testing.T) {
	d := &NoopDiagnostician{}
	bundle := collector.DiagnosticBundle{Content: "some content"}

	diagnosis, err := d.Diagnose(context.Background(), bundle)
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if diagnosis == nil {
		t.Fatal("expected non-nil diagnosis, got nil")
	}
}
