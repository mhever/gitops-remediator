package diagnostician

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

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

// Diagnose returns an empty Diagnosis without making any API calls.
func (n *NoopDiagnostician) Diagnose(ctx context.Context, bundle collector.DiagnosticBundle) (*Diagnosis, error) {
	return &Diagnosis{}, nil
}

// DeepSeekDiagnostician calls DeepSeek R1 via the OpenAI-compatible API.
type DeepSeekDiagnostician struct {
	apiKey     string
	logPath    string
	httpClient *http.Client
	logger     *slog.Logger
	// baseURL is the base URL for the DeepSeek API. Defaults to "https://api.deepseek.com".
	// Override in tests via a constructor option or by setting the field directly.
	baseURL string
}

// NewDeepSeekDiagnostician creates a new DeepSeekDiagnostician.
// httpClient may be nil, in which case a default client with 120s timeout is used.
func NewDeepSeekDiagnostician(apiKey, logPath string, httpClient *http.Client, logger *slog.Logger) *DeepSeekDiagnostician {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 120 * time.Second}
	}
	return &DeepSeekDiagnostician{
		apiKey:     apiKey,
		logPath:    logPath,
		httpClient: httpClient,
		logger:     logger,
		baseURL:    "https://api.deepseek.com",
	}
}

var _ Diagnostician = (*DeepSeekDiagnostician)(nil)

// Diagnose sends the bundle to DeepSeek R1 and returns a structured Diagnosis.
// It logs the full prompt and response to logPath before returning.
func (d *DeepSeekDiagnostician) Diagnose(ctx context.Context, bundle collector.DiagnosticBundle) (*Diagnosis, error) {
	userPrompt := fmt.Sprintf(userPromptTemplate, bundle.Content)

	// Build the full prompt string for logging.
	fullPromptForLog := fmt.Sprintf("=== SYSTEM ===\n%s\n\n=== USER ===\n%s", systemPrompt, userPrompt)
	d.diagLog("PROMPT", fullPromptForLog, "")

	reqBody := chatRequest{
		Model: "deepseek-reasoner",
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	}

	reqBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("diagnostician: marshal request: %w", err)
	}

	url := d.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBytes))
	if err != nil {
		return nil, fmt.Errorf("diagnostician: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+d.apiKey)

	httpResp, err := d.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("diagnostician: http request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("diagnostician: read response body: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		snippet := respBody
		if len(snippet) > 500 {
			snippet = snippet[:500]
		}
		return nil, fmt.Errorf("diagnostician: unexpected status %d: %s", httpResp.StatusCode, string(snippet))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("diagnostician: unmarshal chat response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("diagnostician: no choices in response")
	}

	rawJSON := chatResp.Choices[0].Message.Content

	tokensStr := fmt.Sprintf("prompt=%d completion=%d total=%d",
		chatResp.Usage.PromptTokens,
		chatResp.Usage.CompletionTokens,
		chatResp.Usage.TotalTokens,
	)
	d.diagLog("RESPONSE", rawJSON, tokensStr)

	// Strip backtick code fences if present.
	stripped := strings.TrimSpace(rawJSON)
	if strings.HasPrefix(stripped, "```json") {
		stripped = strings.TrimPrefix(stripped, "```json")
		stripped = strings.TrimSuffix(stripped, "```")
		stripped = strings.TrimSpace(stripped)
	} else if strings.HasPrefix(stripped, "```") {
		stripped = strings.TrimPrefix(stripped, "```")
		stripped = strings.TrimSuffix(stripped, "```")
		stripped = strings.TrimSpace(stripped)
	}

	var diagnosis Diagnosis
	if err := json.Unmarshal([]byte(stripped), &diagnosis); err != nil {
		return nil, fmt.Errorf("diagnostician: unmarshal diagnosis JSON: %w", err)
	}

	if !diagnosis.Remediable {
		d.logger.Warn("diagnosis: escalating non-remediable failure",
			"failure_type", diagnosis.FailureType,
			"root_cause", diagnosis.RootCause,
			"escalation_reason", diagnosis.EscalationReason,
			"action", "escalated",
		)
	}

	return &diagnosis, nil
}
