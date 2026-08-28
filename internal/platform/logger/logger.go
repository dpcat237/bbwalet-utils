// Package logger configures the process-wide slog logger.
package logger

import (
	"io"
	"log/slog"
	"os"
)

// Init installs a text slog handler that writes to stderr as the default
// logger. Call it once during startup, before the first log call.
func Init() {
	slog.SetDefault(slog.New(newHandler(os.Stderr)))
}

func newHandler(w io.Writer) slog.Handler {
	return slog.NewTextHandler(w, nil)
}
