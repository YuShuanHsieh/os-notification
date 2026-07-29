package loglevel

import (
	"log/slog"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		want   slog.Level
		wantOK bool
	}{
		{"debug", "debug", slog.LevelDebug, true},
		{"info", "info", slog.LevelInfo, true},
		{"warn", "warn", slog.LevelWarn, true},
		{"warning alias", "warning", slog.LevelWarn, true},
		{"error", "error", slog.LevelError, true},
		{"uppercase", "DEBUG", slog.LevelDebug, true},
		{"mixed case with whitespace", "  Info  ", slog.LevelInfo, true},
		{"blank", "", 0, false},
		{"whitespace only", "   ", 0, false},
		{"unrecognized", "verbose", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Parse(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("Parse(%q) ok = %v, want %v", tt.in, ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Fatalf("Parse(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
