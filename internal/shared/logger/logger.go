// Package logger builds the structured JSON logger shared by Vault Chat
// services (ARCHITECTURE.md section 7.1).
package logger

import (
	"log/slog"
	"os"
)

// New returns a JSON logger writing to stdout at the given level
// (debug | info | warn | error). Unknown levels fall back to info. Records
// logged with a context carrying a request id include it as request_id.
func New(level string) *slog.Logger {
	levels := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	}
	parsed, ok := levels[level]
	if !ok {
		parsed = slog.LevelInfo
	}
	return slog.New(NewRequestIDHandler(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parsed})))
}
