package diagnostician

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

// newTestDiagnostician creates an OpenRouterDiagnostician pointing at the given test server URL.
func newTestDiagnostician(t *testing.T, serverURL string) *OpenRouterDiagnostician {
	t.Helper()
	logPath := t.TempDir() + "/diag.log"
	d := NewOpenRouterDiagnostician("test-api-key", logPath, nil, slog.Default())
	d.baseURL = serverURL
	return d
}

func makeChatResponse(content string, promptTokens, completionTokens, totalTokens int) []byte {
	resp := chatResponse{}
	resp.Choices = []struct {
		Message chatMessage `json:"message"`
	}{
		{Message: chatMessage{Role: "assistant", Content: content}},
	}
	resp.Usage.PromptTokens = promptTokens
	resp.Usage.CompletionTokens = completionTokens
	resp.Usage.TotalTokens = totalTokens
	b, _ := json.Marshal(resp)
	return b
}

func TestOpenRouterDiagnostician_SuccessRemediable(t *testing.T) {
	diagJSON := `{
		"failure_type": "OOMKilled",
		"root_cause": "container exceeded memory limit",
		"remediable": true,
		"patch_type": "memory_limit",
		"patch_value": "256Mi",
		"reasoning_summary": "container was OOMKilled, increase memory limit"
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "bad content-type", http.StatusBadRequest)
			return
		}
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if req.Model != "deepseek/deepseek-r1" {
			http.Error(w, "wrong model", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(makeChatResponse(diagJSON, 100, 200, 300))
	}))
	defer server.Close()

	d := newTestDiagnostician(t, server.URL)
	bundle := collector.DiagnosticBundle{Content: "test bundle content"}

	diagnosis, err := d.Diagnose(context.Background(), bundle)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if diagnosis == nil {
		t.Fatal("expected non-nil diagnosis")
	}
	if diagnosis.FailureType != "OOMKilled" {
		t.Errorf("expected failure_type OOMKilled, got: %s", diagnosis.FailureType)
	}
	if !diagnosis.Remediable {
		t.Error("expected remediable=true")
	}
	if diagnosis.PatchType != "memory_limit" {
		t.Errorf("expected patch_type memory_limit, got: %s", diagnosis.PatchType)
	}
	if diagnosis.PatchValue != "256Mi" {
		t.Errorf("expected patch_value 256Mi, got: %s", diagnosis.PatchValue)
	}

	// Verify log file was written with PROMPT and RESPONSE.
	logData, err := os.ReadFile(d.logPath)
	if err != nil {
		t.Fatalf("expected log file to exist, got error: %v", err)
	}
	logStr := string(logData)
	if !strings.Contains(logStr, "PROMPT") {
		t.Error("log file should contain PROMPT entry")
	}
	if !strings.Contains(logStr, "RESPONSE") {
		t.Error("log file should contain RESPONSE entry")
	}
}

func TestOpenRouterDiagnostician_SuccessNonRemediable(t *testing.T) {
	diagJSON := `{
		"failure_type": "CrashLoopBackOff",
		"root_cause": "application panic in main goroutine",
		"remediable": false,
		"escalation_reason": "code panic detected in logs — requires developer intervention",
		"reasoning_summary": "stack trace found in logs, not a config issue"
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(makeChatResponse(diagJSON, 50, 100, 150))
	}))
	defer server.Close()

	d := newTestDiagnostician(t, server.URL)
	bundle := collector.DiagnosticBundle{Content: "panic bundle"}

	diagnosis, err := d.Diagnose(context.Background(), bundle)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if diagnosis.Remediable {
		t.Error("expected remediable=false")
	}
	if diagnosis.EscalationReason == "" {
		t.Error("expected non-empty escalation_reason")
	}
}

func TestOpenRouterDiagnostician_NonRemediable_NoError(t *testing.T) {
	diagJSON := `{
		"failure_type": "ImagePullBackOff",
		"root_cause": "image registry authentication failure",
		"remediable": false,
		"escalation_reason": "auth failure cannot be fixed by manifest patch",
		"reasoning_summary": "registry returned 401"
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(makeChatResponse(diagJSON, 30, 60, 90))
	}))
	defer server.Close()

	d := newTestDiagnostician(t, server.URL)
	bundle := collector.DiagnosticBundle{Content: "auth failure bundle"}

	_, err := d.Diagnose(context.Background(), bundle)
	if err != nil {
		t.Errorf("non-remediable response must return nil error, got: %v", err)
	}
}

func TestOpenRouterDiagnostician_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal server error"}`))
	}))
	defer server.Close()

	d := newTestDiagnostician(t, server.URL)
	bundle := collector.DiagnosticBundle{Content: "some content"}

	_, err := d.Diagnose(context.Background(), bundle)
	if err == nil {
		t.Fatal("expected error for HTTP 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should contain status code 500, got: %v", err)
	}
}

func TestOpenRouterDiagnostician_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(makeChatResponse("this is not json at all", 10, 5, 15))
	}))
	defer server.Close()

	d := newTestDiagnostician(t, server.URL)
	bundle := collector.DiagnosticBundle{Content: "some content"}

	_, err := d.Diagnose(context.Background(), bundle)
	if err == nil {
		t.Fatal("expected error for malformed JSON response, got nil")
	}
}

func TestOpenRouterDiagnostician_CodeFenceStripping(t *testing.T) {
	diagJSON := `{
		"failure_type": "OOMKilled",
		"root_cause": "container exceeded memory limit",
		"remediable": true,
		"patch_type": "memory_limit",
		"patch_value": "512Mi",
		"reasoning_summary": "OOMKilled, increased memory"
	}`

	tests := []struct {
		name    string
		wrapped string
	}{
		{
			name:    "json code fence",
			wrapped: "```json\n" + diagJSON + "\n```",
		},
		{
			name:    "plain code fence",
			wrapped: "```\n" + diagJSON + "\n```",
		},
		{
			name:    "trailing newline after closing fence",
			wrapped: "```json\n" + diagJSON + "\n```\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapped := tt.wrapped
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write(makeChatResponse(wrapped, 100, 200, 300))
			}))
			defer server.Close()

			d := newTestDiagnostician(t, server.URL)
			bundle := collector.DiagnosticBundle{Content: "oom bundle"}

			diagnosis, err := d.Diagnose(context.Background(), bundle)
			if err != nil {
				t.Fatalf("expected nil error after code fence stripping, got: %v", err)
			}
			if diagnosis.FailureType != "OOMKilled" {
				t.Errorf("expected OOMKilled, got: %s", diagnosis.FailureType)
			}
			if diagnosis.PatchValue != "512Mi" {
				t.Errorf("expected 512Mi, got: %s", diagnosis.PatchValue)
			}
		})
	}
}
