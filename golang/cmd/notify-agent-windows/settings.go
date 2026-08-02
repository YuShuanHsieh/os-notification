// Settings-file support for the Windows head: an operator-configurable JSON
// file that supplies base configuration so a deployed agent doesn't have to
// be launched with environment variables set. Environment variables still
// win when set (see Resolve* below) -- the file only fills in what the
// environment doesn't specify.
//
// This file has no `//go:build windows` constraint, unlike renderer.go's
// defaultAppDataDir/defaultSettingsFilePath (which resolve the real
// %LOCALAPPDATA%-based file path): the JSON parsing and precedence logic
// here does nothing OS-specific, so it stays unit-testable on any platform,
// same as toastscript.go's split for the PowerShell script builder.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/YuShuanHsieh/os-notification/golang/internal/host"
	"github.com/YuShuanHsieh/os-notification/golang/internal/loglevel"
)

// Settings is the on-disk shape of settings.json. Every field is optional;
// a field that is absent or blank is treated as "not configured" here, the
// same way EnvIdentity/host.OptionsFromEnv treat an unset/blank environment
// variable. This intentionally only covers the fields the Go Windows head
// actually acts on -- it has no AAD/device-code identity and no
// external-auth-service NATS auth mode (see README.md "Known gaps"), so
// unlike C#/Rust's settings surface, there is no field for either here.
type Settings struct {
	NatsURL         string `json:"natsUrl"`
	SubjectTemplate string `json:"subjectTemplate"`
	AckSubject      string `json:"ackSubject"`
	NatsCredsFile   string `json:"natsCredsFile"`
	DeviceID        string `json:"deviceId"`
	LogLevel        string `json:"logLevel"`

	// OtelEnabled, OtelExporterEndpoint, and OtelServiceName configure this
	// head's optional OpenTelemetry metrics export (see otelmetrics.go).
	// OtelEnabled is a plain bool (unlike the string fields above, which use
	// "blank" to mean "not configured"): the built-in default is itself
	// false, so a settings-file field absent from the JSON (decoding to the
	// bool zero value, false) and one explicitly set to false are
	// indistinguishable in effect, and no tri-state ("not configured" vs.
	// "configured off") representation is needed.
	OtelEnabled          bool   `json:"otelEnabled"`
	OtelExporterEndpoint string `json:"otelExporterEndpoint"`
	OtelServiceName      string `json:"otelServiceName"`
}

// ParseSettings parses raw settings.json bytes into a Settings value.
func ParseSettings(data []byte) (Settings, error) {
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{}, fmt.Errorf("parse settings json: %w", err)
	}
	return s, nil
}

// settingsLoadDiagnostic captures one log-worthy event from LoadSettingsFile
// as data instead of logging it directly. LoadSettingsFile necessarily runs
// before main.go can call slog.SetDefault (the log level to configure comes
// from the settings it loads), so anything it logged directly would go
// through slog's *default* logger -- wrong level threshold, wrong handler --
// rather than the one main.go configures immediately afterward. The caller
// replays these through Log once the real logger is in place.
type settingsLoadDiagnostic struct {
	level slog.Level
	msg   string
	args  []any
}

// Log emits the diagnostic through slog's current default logger. Callers
// should only invoke this after slog.SetDefault has installed the properly
// configured logger, so the message honors the resolved level/format.
func (d settingsLoadDiagnostic) Log() {
	slog.Default().Log(context.Background(), d.level, d.msg, d.args...)
}

// LoadSettingsFile reads and parses the settings file at path. It never
// returns an error to the caller -- a missing file, an unreadable file, and
// malformed JSON all fall back to a zero Settings{} (i.e. every field
// "not configured", so ResolveHostOptions/ResolveCredsFile/ResolveDeviceID/
// ResolveLogLevel fall through to their own defaults) plus a returned
// diagnostic describing what happened, never a startup failure. A missing
// file is Debug-level (the common, expected case when no settings file has
// been deployed yet); an existing-but-unreadable-or-malformed file is
// Warn-level, since that more likely reflects an operator mistake worth
// noticing. The diagnostic is returned rather than logged here -- see
// settingsLoadDiagnostic -- so the caller can log it through the real,
// settings-configured logger instead of slog's default one.
func LoadSettingsFile(path string) (Settings, bool, []settingsLoadDiagnostic) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Settings{}, false, []settingsLoadDiagnostic{
				{level: slog.LevelDebug, msg: "settings file not found, using defaults", args: []any{"path", path}},
			}
		}
		return Settings{}, false, []settingsLoadDiagnostic{
			{level: slog.LevelWarn, msg: "settings file could not be read, using defaults", args: []any{"path", path, "error", err}},
		}
	}

	s, err := ParseSettings(data)
	if err != nil {
		return Settings{}, false, []settingsLoadDiagnostic{
			{level: slog.LevelWarn, msg: "settings file could not be parsed, using defaults", args: []any{"path", path, "error", err}},
		}
	}
	return s, true, nil
}

// isBlank reports whether s is empty or contains only whitespace, matching
// the "blank means unset" treatment used throughout this port
// (host.getEnvOr, identity.EnvIdentity).
func isBlank(s string) bool {
	return strings.TrimSpace(s) == ""
}

// ResolveHostOptions layers Settings-file values under envOpts (an already
// environment-resolved host.Options, e.g. host.OptionsFromEnv()'s result),
// per the documented precedence: environment variable (if set and
// non-blank) > settings file value (if present and non-blank) > built-in
// default. Because envOpts already carries "env-value-or-default" per
// field, this only needs to know whether the *environment* actually set
// each field (via getenv) to decide whether the settings file is allowed to
// override the default that's currently sitting in envOpts. getenv is
// injected so this stays testable without mutating process environment
// (production callers pass os.Getenv).
func ResolveHostOptions(getenv func(string) string, envOpts host.Options, s Settings) host.Options {
	result := envOpts
	if isBlank(getenv("NOTIFY_NATS_URL")) && !isBlank(s.NatsURL) {
		result.NatsURL = s.NatsURL
	}
	if isBlank(getenv("NOTIFY_SUBJECT_TEMPLATE")) && !isBlank(s.SubjectTemplate) {
		result.SubjectTemplate = s.SubjectTemplate
	}
	if isBlank(getenv("NOTIFY_ACK_SUBJECT")) && !isBlank(s.AckSubject) {
		result.AckSubject = s.AckSubject
	}
	return result
}

// ResolveCredsFile applies the same env-then-file precedence to the NATS
// creds file path. A blank result means "no auth", same as today. A
// whitespace-only settings-file value is treated the same as an absent one
// (matching isBlank's "blank means unset" rule, which env values already get
// via getenv's !isBlank(v) check above) -- otherwise a stray
// `"natsCredsFile": "   "` in settings.json would "win" over falling through
// to no-auth instead of being ignored like it should be.
func ResolveCredsFile(getenv func(string) string, s Settings) string {
	if v := getenv("NOTIFY_NATS_CREDS_FILE"); !isBlank(v) {
		return v
	}
	if isBlank(s.NatsCredsFile) {
		return ""
	}
	return s.NatsCredsFile
}

// ResolveDeviceID applies the same env-then-file precedence to the device
// ID override. A blank result means "no override" -- the caller (the
// Windows identity provider) falls back to its own hostname-derived
// default. A whitespace-only settings-file value is treated the same as an
// absent one (see ResolveCredsFile's doc comment for why), so it correctly
// falls through to that hostname-derived default instead of "winning" as a
// literal whitespace device ID.
func ResolveDeviceID(getenv func(string) string, s Settings) string {
	if v := getenv("NOTIFY_DEVICE_ID"); !isBlank(v) {
		return v
	}
	if isBlank(s.DeviceID) {
		return ""
	}
	return s.DeviceID
}

// defaultLogLevel is the level used when neither NOTIFY_LOG_LEVEL nor the
// settings file's logLevel field parse to a recognized value.
const defaultLogLevel = slog.LevelInfo

// ResolveLogLevel applies the same env-then-file precedence to the log
// level, parsing the winning value via internal/loglevel. An unparseable or
// blank value at one tier falls through to the next; if nothing parses, it
// defaults to defaultLogLevel.
func ResolveLogLevel(getenv func(string) string, s Settings) slog.Level {
	if lvl, ok := loglevel.Parse(getenv("NOTIFY_LOG_LEVEL")); ok {
		return lvl
	}
	if lvl, ok := loglevel.Parse(s.LogLevel); ok {
		return lvl
	}
	return defaultLogLevel
}

// defaultOtelServiceName is the built-in default for the otelServiceName
// field/NOTIFY_OTEL_SERVICE_NAME override, used whenever neither is set.
const defaultOtelServiceName = "notify-agent-windows-golang"

// ResolveOtelEnabled applies the same env-then-file precedence to whether
// OpenTelemetry metrics export is enabled. NOTIFY_OTEL_ENABLED is parsed via
// strconv.ParseBool (accepting "1"/"t"/"T"/"TRUE"/"true"/"True" and the
// analogous false spellings); an unset, blank, or unparseable env value
// falls through to the settings file's otelEnabled bool. The built-in
// default (when neither tier resolves it) is false -- OpenTelemetry export
// is opt-in, never on by default.
func ResolveOtelEnabled(getenv func(string) string, s Settings) bool {
	if v := getenv("NOTIFY_OTEL_ENABLED"); !isBlank(v) {
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return b
		}
	}
	return s.OtelEnabled
}

// ResolveOtelExporterEndpoint applies the same env-then-file precedence to
// the OTLP metrics exporter endpoint. A blank result means "no endpoint
// configured" -- InitMetrics (otelmetrics.go) treats that the same as
// OtelEnabled being false: metrics stay off, never a startup failure.
func ResolveOtelExporterEndpoint(getenv func(string) string, s Settings) string {
	if v := getenv("NOTIFY_OTEL_EXPORTER_ENDPOINT"); !isBlank(v) {
		return v
	}
	if isBlank(s.OtelExporterEndpoint) {
		return ""
	}
	return s.OtelExporterEndpoint
}

// ResolveOtelServiceName applies the same env-then-file precedence to the
// OpenTelemetry service name attribute. Falls back to
// defaultOtelServiceName when neither tier sets a non-blank value.
func ResolveOtelServiceName(getenv func(string) string, s Settings) string {
	if v := getenv("NOTIFY_OTEL_SERVICE_NAME"); !isBlank(v) {
		return v
	}
	if isBlank(s.OtelServiceName) {
		return defaultOtelServiceName
	}
	return s.OtelServiceName
}
