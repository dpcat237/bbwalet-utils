package logger

import (
	"bytes"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewHandler_EmitsTextFormat(t *testing.T) {
	var buf bytes.Buffer

	slog.New(newHandler(&buf)).Info("hello", "key", "value")

	out := buf.String()
	require.Contains(t, out, "msg=hello")
	require.Contains(t, out, "key=value")
}

func TestInit_DoesNotPanic(t *testing.T) {
	require.NotPanics(t, func() { Init(io.Discard) })
	require.NotNil(t, slog.Default())
}
