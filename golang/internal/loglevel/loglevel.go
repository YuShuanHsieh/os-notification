// Package loglevel parses the small set of level names this product's
// NOTIFY_LOG_LEVEL environment variable and (on the Windows head) the
// settings file's "logLevel" field accept into a log/slog.Level. It is
// shared by both cmd heads (console and Windows) so their logging setup
// follows one consistent convention rather than each parsing independently.
package loglevel

import (
	"log/slog"
	"strings"
)

// Parse parses s (case-insensitive, surrounding whitespace ignored) into a
// slog.Level. It recognizes "debug", "info", "warn"/"warning", and "error".
// It returns ok=false for blank or unrecognized input, leaving the choice of
// fallback default entirely to the caller (this package has no opinion on
// defaults -- see each cmd head's precedence: env > settings file > "info").
func Parse(s string) (level slog.Level, ok bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	default:
		return 0, false
	}
}
