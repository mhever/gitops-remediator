package collector

import (
	"io"
	"log/slog"
)

// noopLogger returns an slog.Logger that discards all output.
func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
