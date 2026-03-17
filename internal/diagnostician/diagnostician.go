package diagnostician

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
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

// Diagnostician sends a DiagnosticBundle to an LLM via OpenRouter and returns a Diagnosis.
type Diagnostician interface {
	Diagnose(ctx context.Context, bundle collector.DiagnosticBundle) (*Diagnosis, error)
}

// Pinger can perform a lightweight connectivity check against the LLM API.
type Pinger interface {
	Ping(ctx context.Context) error
}

// NoopDiagnostician satisfies Diagnostician without doing anything.
type NoopDiagnostician struct{}

var _ Diagnostician = (*NoopDiagnostician)(nil)

// Diagnose returns an empty Diagnosis without making any API calls.
func (n *NoopDiagnostician) Diagnose(ctx context.Context, bundle collector.DiagnosticBundle) (*Diagnosis, error) {
	return &Diagnosis{}, nil
}

// OpenRouterDiagnostician calls DeepSeek R1 via the OpenRouter OpenAI-compatible API.
type OpenRouterDiagnostician struct {
	apiKey     string
	logPath    string
	httpClient *http.Client
	logger     *slog.Logger
	// baseURL is the base URL for the OpenRouter API. Defaults to "https://openrouter.ai/api/v1".
	// Override in tests via a constructor option or by setting the field directly.
	baseURL string
	// model is the model identifier to use. Defaults to "deepseek/deepseek-r1".
	model string
	// LogDisabled is true when the log directory could not be created at
	// construction time. diagLog checks this flag and returns immediately
	// without logging, avoiding repeated ERROR log lines.
	LogDisabled bool
}

// NewOpenRouterDiagnostician creates a new OpenRouterDiagnostician.
// httpClient may be nil, in which case a default client with 120s timeout is used.
// If the log directory cannot be created, LogDisabled is set to true and a
// single WARN is emitted; diagLog will be a no-op for the lifetime of this
// instance.
func NewOpenRouterDiagnostician(apiKey, logPath string, httpClient *http.Client, logger *slog.Logger) *OpenRouterDiagnostician {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 120 * time.Second}
	}
	d := &OpenRouterDiagnostician{
		apiKey:     apiKey,
		logPath:    logPath,
		httpClient: httpClient,
		logger:     logger,
		baseURL:    "https://openrouter.ai/api/v1",
		model:      "deepseek/deepseek-r1",
	}
	// Check if logPath itself is a directory (e.g. DIAGNOSTICIAN_LOG_PATH=/tmp/).
	if fi, err := os.Stat(logPath); err == nil && fi.IsDir() {
		logger.Warn("DIAGNOSTICIAN_LOG_PATH is a directory, not a file — prompt/response logging disabled. Set it to a file path, e.g. /tmp/remediator-diagnostician.log",
			"path", logPath)
		d.LogDisabled = true
		return d
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		logger.Warn("diagnostician log directory unavailable, prompt/response logging disabled",
			"path", filepath.Dir(logPath), "error", err)
		d.LogDisabled = true
	}
	return d
}

var _ Diagnostician = (*OpenRouterDiagnostician)(nil)
var _ Pinger = (*OpenRouterDiagnostician)(nil)

// Ping sends a minimal request to OpenRouter to verify connectivity and API key validity.
// It is intended to be called once on startup. Returns nil on success.
func (d *OpenRouterDiagnostician) Ping(ctx context.Context) error {
	reqBody := chatRequest{
		Model:     d.model,
		MaxTokens: 10,
		Messages: []chatMessage{
			{Role: "user", Content: "Reply with the single word ok."},
		},
	}

	reqBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("ping: marshal request: %w", err)
	}

	url := d.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBytes))
	if err != nil {
		return fmt.Errorf("ping: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+d.apiKey)
	httpReq.Header.Set("HTTP-Referer", "https://github.com/mhever/gitops-remediator")
	httpReq.Header.Set("X-Title", "gitops-remediator")

	httpResp, err := d.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("ping: http request: %w", err)
	}
	defer httpResp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 1024))
	if httpResp.StatusCode != http.StatusOK {
		snippet := body
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return fmt.Errorf("ping: unexpected status %d: %s", httpResp.StatusCode, string(snippet))
	}
	return nil
}

// Diagnose sends the bundle to DeepSeek R1 via OpenRouter and returns a structured Diagnosis.
// It logs the full prompt and response to logPath before returning.
func (d *OpenRouterDiagnostician) Diagnose(ctx context.Context, bundle collector.DiagnosticBundle) (*Diagnosis, error) {
	userPrompt := fmt.Sprintf(userPromptTemplate, bundle.Content)

	// Build the full prompt string for logging.
	fullPromptForLog := fmt.Sprintf("=== SYSTEM ===\n%s\n\n=== USER ===\n%s", systemPrompt, userPrompt)
	d.diagLog("PROMPT", fullPromptForLog, "")

	reqBody := chatRequest{
		Model: d.model,
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
	httpReq.Header.Set("HTTP-Referer", "https://github.com/mhever/gitops-remediator")
	httpReq.Header.Set("X-Title", "gitops-remediator")

	httpResp, err := d.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("diagnostician: http request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(httpResp.Body, 10<<20))
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

	// Strip code fences and extract the JSON object
	stripped := strings.TrimSpace(rawJSON)
	if start := strings.Index(stripped, "{"); start != -1 {
		if end := strings.LastIndex(stripped, "}"); end > start {
			stripped = stripped[start : end+1]
		}
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
