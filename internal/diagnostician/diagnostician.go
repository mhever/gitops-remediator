package diagnostician

import (
	"context"

	"github.com/mhever/gitops-remediator/internal/collector"
)

// Diagnosis is the structured output from the Diagnostician.
type Diagnosis struct {
	FailureType      string `json:"failure_type"`
	RootCause        string `json:"root_cause"`
	Remediable       bool   `json:"remediable"`
	EscalationReason string `json:"escalation_reason,omitempty"`
	PatchType        string `json:"patch_type,omitempty"`
	PatchValue       string `json:"patch_value,omitempty"`
	ReasoningSummary string `json:"reasoning_summary"`
}

// Diagnostician sends a DiagnosticBundle to DeepSeek R1 and returns a Diagnosis.
type Diagnostician interface {
	Diagnose(ctx context.Context, bundle collector.DiagnosticBundle) (*Diagnosis, error)
}

// NoopDiagnostician satisfies Diagnostician without doing anything.
type NoopDiagnostician struct{}

var _ Diagnostician = (*NoopDiagnostician)(nil)

func (n *NoopDiagnostician) Diagnose(ctx context.Context, bundle collector.DiagnosticBundle) (*Diagnosis, error) {
	return &Diagnosis{}, nil
}
