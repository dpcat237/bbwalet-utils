// Package logger configures the process-wide slog logger.
package logger

import (
	"io"
	"log/slog"
)

// Init installs a text slog handler that writes to w as the default logger.
// Call it once during startup, before the first log call; pass os.Stderr in
// production and a buffer in tests.
func Init(w io.Writer) {
	slog.SetDefault(slog.New(newHandler(w)))
}

func newHandler(w io.Writer) slog.Handler {
	return slog.NewTextHandler(w, nil)
}
