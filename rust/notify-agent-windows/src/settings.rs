//! Operator-configurable settings file for the Windows head, read once at
//! startup as a base configuration layer beneath environment variables:
//! `%LOCALAPPDATA%\DesktopNotificationAgent\settings.json` — the same
//! per-user directory `main.rs`'s `device_id()` and `WindowsToastRenderer`'s
//! image cache already use. The Linux console head is unaffected; it keeps
//! using environment variables only.
//!
//! Precedence for every field: environment variable (if set and non-blank) >
//! settings file value (if present and non-blank) > built-in default. A
//! missing file or malformed JSON never crashes startup — both fall back to
//! defaults with a logged `tracing::warn!`.
//!
//! This module deliberately does not touch `AgentConfig::from_env` in
//! `notify-agent-core` (shared with the console head, which must keep
//! pure-env behavior); instead `agent_config` below builds an `AgentConfig`
//! by layering settings-file values under environment variables itself.
//!
//! Parsing/precedence logic here is plain structs + `serde`, with no Windows
//! API calls, so it is exercised by `cargo test` on any platform, including
//! this Linux dev machine. Only `default_path` depends on a Windows-specific
//! environment variable (`LOCALAPPDATA`), and it degrades to an `Err` (never
//! a panic) when that variable is unset.
//!
//! Every function here is only called from `main.rs`'s `#[cfg(windows)] mod
//! win` on a real Windows build; on other targets nothing calls them outside
//! `#[cfg(test)]`, so `dead_code` is suppressed here rather than for the
//! whole crate.
#![cfg_attr(not(windows), allow(dead_code))]

use notify_agent_core::host::AgentConfig;

#[derive(Debug, Default, Clone, PartialEq, serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Settings {
    pub nats_url: Option<String>,
    pub subject_template: Option<String>,
    pub ack_subject: Option<String>,
    pub nats_creds_file: Option<String>,
    pub nats_auth_service_url: Option<String>,
    pub nats_auth_service_scope: Option<String>,
    pub aad_client_id: Option<String>,
    pub aad_tenant_id: Option<String>,
    pub device_id: Option<String>,
    pub log_level: Option<String>,
    /// Feature: OTel metrics. Default `false` (opentelemetry SDK setup never
    /// runs, zero overhead) unless overridden here or by `NOTIFY_OTEL_ENABLED`.
    pub otel_enabled: Option<bool>,
    /// OTLP HTTP metrics exporter endpoint, e.g. `http://localhost:4318`. A
    /// blank/absent value (the default) disables metrics entirely, same as
    /// `otel_enabled: false` — see `otel_metrics::init`.
    pub otel_exporter_endpoint: Option<String>,
    pub otel_service_name: Option<String>,
}

/// Parses settings JSON. Malformed input yields `Err` with a diagnostic
/// message describing the problem; callers fall back to
/// `Settings::default()`.
///
/// Returns a plain `Result<Settings, String>` rather than logging directly:
/// this runs during `load()`/`load_from_path()`, which in turn run in
/// `main.rs` *before* the `tracing` subscriber is installed (the log filter
/// itself can come from the settings file being loaded here — a
/// chicken-and-egg problem). A `tracing::warn!` call made before any
/// subscriber exists is silently dropped, so diagnostics are returned as
/// data instead and logged by the caller once a subscriber is up (see
/// `main.rs`).
pub fn parse(json: &str) -> Result<Settings, String> {
    serde_json::from_str(json).map_err(|error| {
        format!("settings file: malformed JSON, falling back to defaults: {error}")
    })
}

/// Reads and parses the settings file at `path`. Any failure — missing file,
/// unreadable, malformed JSON — yields `Settings::default()`; never panics.
/// Diagnostics describing any non-fatal failure are returned alongside the
/// settings rather than logged here (see `parse`'s doc comment for why).
pub fn load_from_path(path: &std::path::Path) -> (Settings, Vec<String>) {
    match std::fs::read_to_string(path) {
        Ok(contents) => match parse(&contents) {
            Ok(settings) => (settings, Vec::new()),
            Err(message) => (Settings::default(), vec![message]),
        },
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            (Settings::default(), Vec::new())
        }
        Err(error) => (
            Settings::default(),
            vec![format!(
                "settings file: could not read {}, falling back to defaults: {error}",
                path.display()
            )],
        ),
    }
}

/// `%LOCALAPPDATA%\DesktopNotificationAgent\settings.json`. Returns `Err`
/// (never panics) when `LOCALAPPDATA` is unset, e.g. off Windows.
pub fn default_path() -> anyhow::Result<std::path::PathBuf> {
    Ok(std::path::PathBuf::from(std::env::var("LOCALAPPDATA")?)
        .join("DesktopNotificationAgent")
        .join("settings.json"))
}

/// Loads settings from the default per-user location, falling back to
/// defaults if `LOCALAPPDATA` isn't set (rather than failing startup).
/// Returns any diagnostic messages alongside the settings so the caller can
/// log them once a `tracing` subscriber is installed (see `parse`'s doc
/// comment for why this doesn't log directly).
pub fn load() -> (Settings, Vec<String>) {
    match default_path() {
        Ok(path) => load_from_path(&path),
        Err(error) => (
            Settings::default(),
            vec![format!(
                "settings file: LOCALAPPDATA not set, using defaults: {error}"
            )],
        ),
    }
}

/// Env-var-over-file-over-default precedence for a field that always has a
/// value (falls back to `default` when neither source provides a non-blank
/// value).
pub fn resolved_str(env_var: &str, file_value: Option<&str>, default: &str) -> String {
    resolved_opt(env_var, file_value).unwrap_or_else(|| default.to_string())
}

/// Env-var-over-file-over-default precedence for an optional field (blank or
/// unset env var and blank or absent file value both fall through to
/// `None`).
pub fn resolved_opt(env_var: &str, file_value: Option<&str>) -> Option<String> {
    if let Ok(v) = std::env::var(env_var) {
        let trimmed = v.trim();
        if !trimmed.is_empty() {
            return Some(trimmed.to_string());
        }
    }
    file_value
        .map(str::trim)
        .filter(|s| !s.is_empty())
        .map(str::to_string)
}

/// Env-var-over-file-over-default precedence for a boolean field. Unlike
/// `resolved_str`/`resolved_opt`, an env var only overrides when it
/// unambiguously means "true" (`"true"`/`"1"`, case-insensitive); any other
/// value — including an explicit `"false"`, blank, or unset — falls through
/// to the file value (then the built-in default) instead of being treated
/// as an override to `false`. This is a deliberate asymmetry: there is no
/// way to force-disable via env var something the settings file enables,
/// only to force-enable. That's an acceptable trade-off here since the
/// built-in default is already `false` (see `otelEnabled`'s doc comment) —
/// the only env-var use case this must serve is "turn it on for this run".
pub fn resolved_bool(env_var: &str, file_value: Option<bool>, default: bool) -> bool {
    if let Ok(v) = std::env::var(env_var) {
        let trimmed = v.trim().to_ascii_lowercase();
        if trimmed == "true" || trimmed == "1" {
            return true;
        }
    }
    file_value.unwrap_or(default)
}

/// Effective OTel metrics configuration, resolved with the same
/// env-over-file-over-default precedence as every other setting in this
/// file. Consumed by `otel_metrics::init`.
pub struct ResolvedOtelSettings {
    pub enabled: bool,
    pub exporter_endpoint: String,
    pub service_name: String,
}

pub fn resolved_otel_settings(settings: &Settings) -> ResolvedOtelSettings {
    ResolvedOtelSettings {
        enabled: resolved_bool("NOTIFY_OTEL_ENABLED", settings.otel_enabled, false),
        exporter_endpoint: resolved_str(
            "NOTIFY_OTEL_EXPORTER_ENDPOINT",
            settings.otel_exporter_endpoint.as_deref(),
            "",
        ),
        service_name: resolved_str(
            "NOTIFY_OTEL_SERVICE_NAME",
            settings.otel_service_name.as_deref(),
            "notify-agent-windows-rust",
        ),
    }
}

/// Builds an `AgentConfig` by layering settings-file values under
/// environment variables (see module docs for why this doesn't just call
/// `AgentConfig::from_env`).
pub fn agent_config(settings: &Settings) -> AgentConfig {
    AgentConfig {
        nats_url: resolved_str(
            "NOTIFY_NATS_URL",
            settings.nats_url.as_deref(),
            "nats://127.0.0.1:4222",
        ),
        subject_template: resolved_str(
            "NOTIFY_SUBJECT_TEMPLATE",
            settings.subject_template.as_deref(),
            // Matches AgentConfig::from_env's literal "{0}" placeholder style —
            // deliberately not "{}" (see module docs).
            "notify.user.{0}.desktop",
        ),
        ack_subject: resolved_str(
            "NOTIFY_ACK_SUBJECT",
            settings.ack_subject.as_deref(),
            "notify.ack.desktop",
        ),
    }
}

/// Resolves the effective log directive: `RUST_LOG` if set and non-blank,
/// else the settings file's `logLevel`, else `"info"`. Note this `"info"`
/// default is a deliberate improvement over, not a preserved match for,
/// this head's prior behavior: before this settings file existed, an unset
/// `RUST_LOG` fell through to `EnvFilter::from_default_env()`'s own implicit
/// default, which is `ERROR` on this toolchain, not `INFO`.
pub fn resolve_log_directive(settings: &Settings) -> String {
    if let Ok(v) = std::env::var("RUST_LOG") {
        let trimmed = v.trim();
        if !trimmed.is_empty() {
            return trimmed.to_string();
        }
    }
    settings
        .log_level
        .as_deref()
        .map(str::trim)
        .filter(|s| !s.is_empty())
        .unwrap_or("info")
        .to_string()
}

/// Builds the `tracing_subscriber::EnvFilter` startup should install, per
/// `resolve_log_directive`'s precedence. Falls back to `"info"` if the
/// resolved directive somehow fails to parse, rather than panicking at
/// startup over a bad log-level string.
pub fn resolve_log_filter(settings: &Settings) -> tracing_subscriber::EnvFilter {
    let directive = resolve_log_directive(settings);
    tracing_subscriber::EnvFilter::try_new(&directive)
        .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("info"))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Mutex;

    /// Serializes tests that touch process-wide env vars — `cargo test` runs
    /// tests in parallel by default, and these vars are read directly by the
    /// functions under test.
    static ENV_LOCK: Mutex<()> = Mutex::new(());

    fn clear(vars: &[&str]) {
        for v in vars {
            std::env::remove_var(v);
        }
    }

    #[test]
    fn missing_file_yields_defaults() {
        let (settings, diagnostics) =
            load_from_path(std::path::Path::new("/nonexistent/settings.json"));
        assert_eq!(settings, Settings::default());
        assert!(diagnostics.is_empty());
    }

    #[test]
    fn malformed_json_yields_defaults_not_a_panic() {
        assert_eq!(parse("{ not json").unwrap_or_default(), Settings::default());
    }

    #[test]
    fn malformed_json_yields_a_diagnostic_message() {
        // Proves the loader surfaces the "malformed JSON" diagnostic as
        // returned data (not a tracing side effect) so callers can log it
        // once a subscriber actually exists — see `parse`'s doc comment.
        let error = parse("{ not json").unwrap_err();
        assert!(
            error.contains("malformed JSON"),
            "diagnostic message was: {error:?}"
        );
    }

    #[test]
    fn load_from_path_surfaces_malformed_json_diagnostic() {
        let dir = std::env::temp_dir().join(format!(
            "notify-agent-windows-settings-test-{:x}",
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("settings.json");
        std::fs::write(&path, "{ not json").unwrap();

        let (settings, diagnostics) = load_from_path(&path);
        assert_eq!(settings, Settings::default());
        assert_eq!(diagnostics.len(), 1);
        assert!(diagnostics[0].contains("malformed JSON"));

        std::fs::remove_dir_all(&dir).ok();
    }

    #[test]
    fn parses_full_schema() {
        let json = r#"{
            "natsUrl": "nats://example:4222",
            "subjectTemplate": "notify.user.{0}.custom",
            "ackSubject": "notify.ack.custom",
            "natsCredsFile": "/creds/file",
            "natsAuthServiceUrl": "https://auth.example/token",
            "natsAuthServiceScope": "api://x/Scope",
            "aadClientId": "client-1",
            "aadTenantId": "tenant-1",
            "deviceId": "d-fixed",
            "logLevel": "debug",
            "otelEnabled": true,
            "otelExporterEndpoint": "http://collector.example:4318",
            "otelServiceName": "custom-service-name"
        }"#;
        let settings = parse(json).unwrap();
        assert_eq!(settings.nats_url.as_deref(), Some("nats://example:4222"));
        assert_eq!(
            settings.subject_template.as_deref(),
            Some("notify.user.{0}.custom")
        );
        assert_eq!(settings.ack_subject.as_deref(), Some("notify.ack.custom"));
        assert_eq!(settings.nats_creds_file.as_deref(), Some("/creds/file"));
        assert_eq!(
            settings.nats_auth_service_url.as_deref(),
            Some("https://auth.example/token")
        );
        assert_eq!(
            settings.nats_auth_service_scope.as_deref(),
            Some("api://x/Scope")
        );
        assert_eq!(settings.aad_client_id.as_deref(), Some("client-1"));
        assert_eq!(settings.aad_tenant_id.as_deref(), Some("tenant-1"));
        assert_eq!(settings.device_id.as_deref(), Some("d-fixed"));
        assert_eq!(settings.log_level.as_deref(), Some("debug"));
        assert_eq!(settings.otel_enabled, Some(true));
        assert_eq!(
            settings.otel_exporter_endpoint.as_deref(),
            Some("http://collector.example:4318")
        );
        assert_eq!(
            settings.otel_service_name.as_deref(),
            Some("custom-service-name")
        );
    }

    #[test]
    fn empty_json_object_is_all_none() {
        assert_eq!(parse("{}").unwrap(), Settings::default());
    }

    #[test]
    fn env_var_wins_over_file_value() {
        let _guard = ENV_LOCK.lock().unwrap();
        clear(&["NOTIFY_TEST_PRECEDENCE"]);
        std::env::set_var("NOTIFY_TEST_PRECEDENCE", "from-env");
        assert_eq!(
            resolved_str("NOTIFY_TEST_PRECEDENCE", Some("from-file"), "from-default"),
            "from-env"
        );
        clear(&["NOTIFY_TEST_PRECEDENCE"]);
    }

    #[test]
    fn env_var_value_is_trimmed_same_as_file_value() {
        // File values are trimmed (see other tests here); env values must be
        // too, for consistency — e.g. NOTIFY_NATS_URL=" nats://host " should
        // not pass leading/trailing whitespace straight through.
        let _guard = ENV_LOCK.lock().unwrap();
        clear(&["NOTIFY_TEST_PRECEDENCE"]);
        std::env::set_var("NOTIFY_TEST_PRECEDENCE", "  from-env  ");
        assert_eq!(
            resolved_opt("NOTIFY_TEST_PRECEDENCE", None),
            Some("from-env".to_string())
        );
        clear(&["NOTIFY_TEST_PRECEDENCE"]);
    }

    #[test]
    fn file_value_wins_over_default_when_env_unset() {
        let _guard = ENV_LOCK.lock().unwrap();
        clear(&["NOTIFY_TEST_PRECEDENCE"]);
        assert_eq!(
            resolved_str("NOTIFY_TEST_PRECEDENCE", Some("from-file"), "from-default"),
            "from-file"
        );
    }

    #[test]
    fn default_wins_when_neither_env_nor_file_set() {
        let _guard = ENV_LOCK.lock().unwrap();
        clear(&["NOTIFY_TEST_PRECEDENCE"]);
        assert_eq!(
            resolved_str("NOTIFY_TEST_PRECEDENCE", None, "from-default"),
            "from-default"
        );
    }

    #[test]
    fn blank_env_var_falls_through_to_file_value() {
        let _guard = ENV_LOCK.lock().unwrap();
        clear(&["NOTIFY_TEST_PRECEDENCE"]);
        std::env::set_var("NOTIFY_TEST_PRECEDENCE", "   ");
        assert_eq!(
            resolved_str("NOTIFY_TEST_PRECEDENCE", Some("from-file"), "from-default"),
            "from-file"
        );
        clear(&["NOTIFY_TEST_PRECEDENCE"]);
    }

    #[test]
    fn blank_file_value_falls_through_to_default() {
        let _guard = ENV_LOCK.lock().unwrap();
        clear(&["NOTIFY_TEST_PRECEDENCE"]);
        assert_eq!(
            resolved_str("NOTIFY_TEST_PRECEDENCE", Some("   "), "from-default"),
            "from-default"
        );
    }

    #[test]
    fn resolved_opt_is_none_when_nothing_set() {
        let _guard = ENV_LOCK.lock().unwrap();
        clear(&["NOTIFY_TEST_PRECEDENCE"]);
        assert_eq!(resolved_opt("NOTIFY_TEST_PRECEDENCE", None), None);
    }

    #[test]
    fn agent_config_uses_defaults_when_nothing_set() {
        let _guard = ENV_LOCK.lock().unwrap();
        clear(&[
            "NOTIFY_NATS_URL",
            "NOTIFY_SUBJECT_TEMPLATE",
            "NOTIFY_ACK_SUBJECT",
        ]);
        let config = agent_config(&Settings::default());
        assert_eq!(config.nats_url, "nats://127.0.0.1:4222");
        assert_eq!(config.subject_template, "notify.user.{0}.desktop");
        assert_eq!(config.ack_subject, "notify.ack.desktop");
    }

    #[test]
    fn agent_config_uses_file_values_when_env_unset() {
        let _guard = ENV_LOCK.lock().unwrap();
        clear(&[
            "NOTIFY_NATS_URL",
            "NOTIFY_SUBJECT_TEMPLATE",
            "NOTIFY_ACK_SUBJECT",
        ]);
        let settings = Settings {
            nats_url: Some("nats://from-file:4222".into()),
            ..Settings::default()
        };
        assert_eq!(agent_config(&settings).nats_url, "nats://from-file:4222");
    }

    #[test]
    fn agent_config_env_overrides_file() {
        let _guard = ENV_LOCK.lock().unwrap();
        clear(&[
            "NOTIFY_NATS_URL",
            "NOTIFY_SUBJECT_TEMPLATE",
            "NOTIFY_ACK_SUBJECT",
        ]);
        std::env::set_var("NOTIFY_NATS_URL", "nats://from-env:4222");
        let settings = Settings {
            nats_url: Some("nats://from-file:4222".into()),
            ..Settings::default()
        };
        assert_eq!(agent_config(&settings).nats_url, "nats://from-env:4222");
        clear(&["NOTIFY_NATS_URL"]);
    }

    #[test]
    fn log_directive_prefers_rust_log_then_settings_then_info() {
        let _guard = ENV_LOCK.lock().unwrap();
        clear(&["RUST_LOG"]);

        assert_eq!(resolve_log_directive(&Settings::default()), "info");

        let settings = Settings {
            log_level: Some("debug".into()),
            ..Settings::default()
        };
        assert_eq!(resolve_log_directive(&settings), "debug");

        std::env::set_var("RUST_LOG", "trace");
        assert_eq!(resolve_log_directive(&settings), "trace");
        clear(&["RUST_LOG"]);
    }

    #[test]
    fn blank_rust_log_falls_through_to_settings() {
        let _guard = ENV_LOCK.lock().unwrap();
        clear(&["RUST_LOG"]);
        std::env::set_var("RUST_LOG", "  ");
        let settings = Settings {
            log_level: Some("warn".into()),
            ..Settings::default()
        };
        assert_eq!(resolve_log_directive(&settings), "warn");
        clear(&["RUST_LOG"]);
    }

    #[test]
    fn resolved_bool_env_true_or_1_overrides_file_and_default() {
        let _guard = ENV_LOCK.lock().unwrap();
        clear(&["NOTIFY_TEST_BOOL"]);
        std::env::set_var("NOTIFY_TEST_BOOL", "true");
        assert!(resolved_bool("NOTIFY_TEST_BOOL", Some(false), false));
        std::env::set_var("NOTIFY_TEST_BOOL", "1");
        assert!(resolved_bool("NOTIFY_TEST_BOOL", None, false));
        std::env::set_var("NOTIFY_TEST_BOOL", "TRUE");
        assert!(
            resolved_bool("NOTIFY_TEST_BOOL", None, false),
            "must be case-insensitive"
        );
        clear(&["NOTIFY_TEST_BOOL"]);
    }

    #[test]
    fn resolved_bool_falls_through_to_file_then_default_for_anything_else() {
        let _guard = ENV_LOCK.lock().unwrap();
        clear(&["NOTIFY_TEST_BOOL"]);
        // Unset env -> file value wins.
        assert!(resolved_bool("NOTIFY_TEST_BOOL", Some(true), false));
        // Unset env, no file value -> default.
        assert!(!resolved_bool("NOTIFY_TEST_BOOL", None, false));
        // An explicit "false" (or any non-true/1 value) does not force an
        // override to false; it falls through to the file value instead —
        // see resolved_bool's doc comment for why.
        std::env::set_var("NOTIFY_TEST_BOOL", "false");
        assert!(resolved_bool("NOTIFY_TEST_BOOL", Some(true), false));
        std::env::set_var("NOTIFY_TEST_BOOL", "garbage");
        assert!(!resolved_bool("NOTIFY_TEST_BOOL", None, false));
        clear(&["NOTIFY_TEST_BOOL"]);
    }

    #[test]
    fn resolved_otel_settings_uses_defaults_when_nothing_set() {
        let _guard = ENV_LOCK.lock().unwrap();
        clear(&[
            "NOTIFY_OTEL_ENABLED",
            "NOTIFY_OTEL_EXPORTER_ENDPOINT",
            "NOTIFY_OTEL_SERVICE_NAME",
        ]);
        let resolved = resolved_otel_settings(&Settings::default());
        assert!(!resolved.enabled);
        assert_eq!(resolved.exporter_endpoint, "");
        assert_eq!(resolved.service_name, "notify-agent-windows-rust");
    }

    #[test]
    fn resolved_otel_settings_uses_file_values_when_env_unset() {
        let _guard = ENV_LOCK.lock().unwrap();
        clear(&[
            "NOTIFY_OTEL_ENABLED",
            "NOTIFY_OTEL_EXPORTER_ENDPOINT",
            "NOTIFY_OTEL_SERVICE_NAME",
        ]);
        let settings = Settings {
            otel_enabled: Some(true),
            otel_exporter_endpoint: Some("http://collector:4318".into()),
            otel_service_name: Some("svc-from-file".into()),
            ..Settings::default()
        };
        let resolved = resolved_otel_settings(&settings);
        assert!(resolved.enabled);
        assert_eq!(resolved.exporter_endpoint, "http://collector:4318");
        assert_eq!(resolved.service_name, "svc-from-file");
    }

    #[test]
    fn resolved_otel_settings_env_overrides_file() {
        let _guard = ENV_LOCK.lock().unwrap();
        clear(&[
            "NOTIFY_OTEL_ENABLED",
            "NOTIFY_OTEL_EXPORTER_ENDPOINT",
            "NOTIFY_OTEL_SERVICE_NAME",
        ]);
        std::env::set_var("NOTIFY_OTEL_ENABLED", "1");
        std::env::set_var("NOTIFY_OTEL_EXPORTER_ENDPOINT", "http://from-env:4318");
        std::env::set_var("NOTIFY_OTEL_SERVICE_NAME", "svc-from-env");
        let settings = Settings {
            otel_enabled: Some(false),
            otel_exporter_endpoint: Some("http://from-file:4318".into()),
            otel_service_name: Some("svc-from-file".into()),
            ..Settings::default()
        };
        let resolved = resolved_otel_settings(&settings);
        assert!(resolved.enabled);
        assert_eq!(resolved.exporter_endpoint, "http://from-env:4318");
        assert_eq!(resolved.service_name, "svc-from-env");
        clear(&[
            "NOTIFY_OTEL_ENABLED",
            "NOTIFY_OTEL_EXPORTER_ENDPOINT",
            "NOTIFY_OTEL_SERVICE_NAME",
        ]);
    }
}
