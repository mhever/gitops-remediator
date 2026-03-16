package diagnostician

import (
	"fmt"
	"os"
	"time"
)

// diagLog appends a structured entry to the diagnostician log file.
// Each entry includes a timestamp, a label ("PROMPT" or "RESPONSE"), and the content.
// For RESPONSE entries, tokens should be in the format "prompt=N completion=N total=N".
// For PROMPT entries, tokens should be an empty string.
// Errors opening or writing the log file are not fatal — they are logged to slog
// but do not block the main Diagnose flow.
func (d *OpenRouterDiagnostician) diagLog(label, content, tokens string) {
	f, err := os.OpenFile(d.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		d.logger.Error("diagLog: failed to open log file", "path", d.logPath, "error", err)
		return
	}
	defer f.Close()

	ts := time.Now().UTC().Format(time.RFC3339)
	header := fmt.Sprintf("=== %s %s ===\n", ts, label)
	if _, err := fmt.Fprint(f, header); err != nil {
		d.logger.Error("diagLog: failed to write header", "error", err)
		return
	}
	if _, err := fmt.Fprintf(f, "%s\n", content); err != nil {
		d.logger.Error("diagLog: failed to write content", "error", err)
		return
	}
	if tokens != "" {
		if _, err := fmt.Fprintf(f, "tokens: %s\n", tokens); err != nil {
			d.logger.Error("diagLog: failed to write tokens", "error", err)
			return
		}
	}
	if _, err := fmt.Fprint(f, "\n"); err != nil {
		d.logger.Error("diagLog: failed to write trailing newline", "error", err)
	}
}
