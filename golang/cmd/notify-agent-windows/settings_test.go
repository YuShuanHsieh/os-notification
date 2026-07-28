package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/YuShuanHsieh/os-notification/golang/internal/host"
)

func noEnv(string) string { return "" }

func envMap(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

func TestLoadSettingsFile_MissingFileUsesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")

	s, ok := LoadSettingsFile(path)
	if ok {
		t.Fatalf("LoadSettingsFile(%q) ok = true, want false for a missing file", path)
	}
	if s != (Settings{}) {
		t.Fatalf("LoadSettingsFile(%q) = %+v, want zero value", path, s)
	}
}

func TestLoadSettingsFile_MalformedJSONUsesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeFile(t, path, `{ this is not valid json `)

	s, ok := LoadSettingsFile(path)
	if ok {
		t.Fatalf("LoadSettingsFile ok = true, want false for malformed JSON")
	}
	if s != (Settings{}) {
		t.Fatalf("LoadSettingsFile = %+v, want zero value on malformed JSON", s)
	}
}

func TestLoadSettingsFile_PartialFileLeavesRestDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeFile(t, path, `{"natsUrl": "nats://example:4222", "logLevel": "debug"}`)

	s, ok := LoadSettingsFile(path)
	if !ok {
		t.Fatalf("LoadSettingsFile ok = false, want true for a valid file")
	}
	if s.NatsURL != "nats://example:4222" {
		t.Errorf("NatsURL = %q, want %q", s.NatsURL, "nats://example:4222")
	}
	if s.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", s.LogLevel, "debug")
	}
	if s.SubjectTemplate != "" || s.AckSubject != "" || s.NatsCredsFile != "" || s.DeviceID != "" {
		t.Errorf("unset fields should stay blank, got %+v", s)
	}
}

func TestParseSettings_AllFields(t *testing.T) {
	data := []byte(`{
		"natsUrl": "nats://host:4222",
		"subjectTemplate": "notify.user.%s.custom",
		"ackSubject": "notify.ack.custom",
		"natsCredsFile": "C:\\creds\\agent.creds",
		"deviceId": "d-override",
		"logLevel": "warn"
	}`)
	s, err := ParseSettings(data)
	if err != nil {
		t.Fatalf("ParseSettings: %v", err)
	}
	want := Settings{
		NatsURL:         "nats://host:4222",
		SubjectTemplate: "notify.user.%s.custom",
		AckSubject:      "notify.ack.custom",
		NatsCredsFile:   `C:\creds\agent.creds`,
		DeviceID:        "d-override",
		LogLevel:        "warn",
	}
	if s != want {
		t.Fatalf("ParseSettings = %+v, want %+v", s, want)
	}
}

func TestResolveHostOptions_Precedence(t *testing.T) {
	envOpts := host.OptionsFromEnv() // built-in defaults, since no env vars set in this test process

	t.Run("file value used when env unset", func(t *testing.T) {
		s := Settings{NatsURL: "nats://from-file:4222"}
		got := ResolveHostOptions(noEnv, envOpts, s)
		if got.NatsURL != "nats://from-file:4222" {
			t.Errorf("NatsURL = %q, want file value", got.NatsURL)
		}
		// Untouched fields still fall back to the built-in default.
		if got.AckSubject != envOpts.AckSubject {
			t.Errorf("AckSubject = %q, want default %q", got.AckSubject, envOpts.AckSubject)
		}
	})

	t.Run("env wins over file", func(t *testing.T) {
		getenv := envMap(map[string]string{"NOTIFY_NATS_URL": "nats://from-env:4222"})
		// Simulate an already-env-resolved Options, as host.OptionsFromEnv would produce.
		resolvedEnvOpts := envOpts
		resolvedEnvOpts.NatsURL = "nats://from-env:4222"

		s := Settings{NatsURL: "nats://from-file:4222"}
		got := ResolveHostOptions(getenv, resolvedEnvOpts, s)
		if got.NatsURL != "nats://from-env:4222" {
			t.Errorf("NatsURL = %q, want env value to win", got.NatsURL)
		}
	})

	t.Run("blank file value does not override default", func(t *testing.T) {
		s := Settings{} // nothing set
		got := ResolveHostOptions(noEnv, envOpts, s)
		if got != envOpts {
			t.Errorf("ResolveHostOptions with blank settings = %+v, want unchanged %+v", got, envOpts)
		}
	})

	t.Run("all three fields layered independently", func(t *testing.T) {
		getenv := envMap(map[string]string{"NOTIFY_ACK_SUBJECT": "notify.ack.env"})
		resolvedEnvOpts := envOpts
		resolvedEnvOpts.AckSubject = "notify.ack.env"

		s := Settings{
			NatsURL:         "nats://from-file:4222",
			SubjectTemplate: "notify.user.%s.file",
			AckSubject:      "notify.ack.file", // should lose to env
		}
		got := ResolveHostOptions(getenv, resolvedEnvOpts, s)
		if got.NatsURL != "nats://from-file:4222" {
			t.Errorf("NatsURL = %q, want file value", got.NatsURL)
		}
		if got.SubjectTemplate != "notify.user.%s.file" {
			t.Errorf("SubjectTemplate = %q, want file value", got.SubjectTemplate)
		}
		if got.AckSubject != "notify.ack.env" {
			t.Errorf("AckSubject = %q, want env value to win over file", got.AckSubject)
		}
	})
}

func TestResolveCredsFile_Precedence(t *testing.T) {
	if got := ResolveCredsFile(noEnv, Settings{NatsCredsFile: "file.creds"}); got != "file.creds" {
		t.Errorf("ResolveCredsFile = %q, want file value when env unset", got)
	}
	getenv := envMap(map[string]string{"NOTIFY_NATS_CREDS_FILE": "env.creds"})
	if got := ResolveCredsFile(getenv, Settings{NatsCredsFile: "file.creds"}); got != "env.creds" {
		t.Errorf("ResolveCredsFile = %q, want env value to win", got)
	}
	if got := ResolveCredsFile(noEnv, Settings{}); got != "" {
		t.Errorf("ResolveCredsFile = %q, want blank when nothing configured", got)
	}
}

func TestResolveDeviceID_Precedence(t *testing.T) {
	if got := ResolveDeviceID(noEnv, Settings{DeviceID: "d-file"}); got != "d-file" {
		t.Errorf("ResolveDeviceID = %q, want file value when env unset", got)
	}
	getenv := envMap(map[string]string{"NOTIFY_DEVICE_ID": "d-env"})
	if got := ResolveDeviceID(getenv, Settings{DeviceID: "d-file"}); got != "d-env" {
		t.Errorf("ResolveDeviceID = %q, want env value to win", got)
	}
	if got := ResolveDeviceID(noEnv, Settings{}); got != "" {
		t.Errorf("ResolveDeviceID = %q, want blank (caller default applies) when nothing configured", got)
	}
}

func TestResolveLogLevel_Precedence(t *testing.T) {
	if got := ResolveLogLevel(noEnv, Settings{}); got != slog.LevelInfo {
		t.Errorf("ResolveLogLevel = %v, want default info", got)
	}
	if got := ResolveLogLevel(noEnv, Settings{LogLevel: "debug"}); got != slog.LevelDebug {
		t.Errorf("ResolveLogLevel = %v, want file value debug", got)
	}
	getenv := envMap(map[string]string{"NOTIFY_LOG_LEVEL": "error"})
	if got := ResolveLogLevel(getenv, Settings{LogLevel: "debug"}); got != slog.LevelError {
		t.Errorf("ResolveLogLevel = %v, want env value to win", got)
	}
	// Unparseable value at either tier falls through rather than erroring.
	if got := ResolveLogLevel(noEnv, Settings{LogLevel: "not-a-level"}); got != slog.LevelInfo {
		t.Errorf("ResolveLogLevel = %v, want fallback to default on unparseable file value", got)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
