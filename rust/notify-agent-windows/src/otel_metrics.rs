//! OpenTelemetry-backed `AgentMetrics` implementation (feature: OTel
//! metrics). Configuration comes from the settings file
//! (`%LOCALAPPDATA%\DesktopNotificationAgent\settings.json`, via
//! `settings::resolved_otel_settings`), with the usual env-var-over-file
//! precedence — see that function's callers.
//!
//! # Crash-safety
//!
//! Per the feature's hard requirement, a metrics-recording failure must
//! never crash the agent or interrupt event processing. This module takes
//! the "by construction" approach documented on
//! `notify_agent_core::metrics::AgentMetrics`: rather than wrapping call
//! sites in `std::panic::catch_unwind` (an anti-pattern for routine
//! same-thread calls — that tool is for FFI/thread boundaries, not
//! ordinary Rust code that simply shouldn't panic), every fallible step
//! here is handled explicitly:
//!
//! - `init` never panics and never returns an `Err`: every SDK call that
//!   returns a `Result` is matched explicitly, logging a `tracing::warn!`
//!   and falling back to `NullAgentMetrics` on any failure (a malformed
//!   endpoint, an exporter build error, anything else). Startup always
//!   proceeds either way.
//! - `OtelAgentMetrics`'s three `record_*` methods call straight through to
//!   `opentelemetry::metrics::{Counter, Histogram}::add`/`record`, which
//!   the OTel API defines as infallible (they return `()`, not `Result`,
//!   and are documented as non-blocking/best-effort by design — an
//!   unreachable collector is handled inside the SDK's own background
//!   exporter thread, never surfaced to the caller). There is no fallible
//!   step left here to guard once the instruments exist.
//!
//! This module's real (non-test) code is only reachable from `main.rs`'s
//! `#[cfg(windows)] mod win`; on other targets nothing calls `init` outside
//! `#[cfg(test)]`, so `dead_code` is suppressed here rather than for the
//! whole crate — same pattern as `settings.rs`.
#![cfg_attr(not(windows), allow(dead_code))]

pub struct OtelAgentMetrics {
    events_received: opentelemetry::metrics::Counter<u64>,
    events_dropped: opentelemetry::metrics::Counter<u64>,
    render_duration: opentelemetry::metrics::Histogram<f64>,
}

/// A shutdown-capable handle for the `MeterProvider` `init` builds, so a
/// caller can flush pending metrics before process exit. Deliberately
/// doesn't leak `opentelemetry_sdk` types past this module's public API:
/// callers just hold this and call `shutdown()`. Holds nothing (a no-op
/// `shutdown()`) when `init` fell back to `NullAgentMetrics` -- there is no
/// live provider to flush/tear down in that case.
#[derive(Clone, Default)]
pub struct MetricsShutdown(Option<opentelemetry_sdk::metrics::SdkMeterProvider>);

impl MetricsShutdown {
    /// Flushes pending metrics and tears down the underlying `MeterProvider`
    /// (a no-op if `init` returned the disabled/no-op path). Must be called
    /// after `AgentHost::shutdown()` and before the process exits: the
    /// `PeriodicReader` only exports on its own interval or on shutdown, so
    /// skipping this silently drops whatever was recorded since the last
    /// interval.
    pub fn shutdown(&self) {
        if let Some(provider) = &self.0 {
            if let Err(error) = provider.shutdown() {
                tracing::warn!(%error, "otel metrics: shutdown/flush failed");
            }
        }
    }
}

impl notify_agent_core::metrics::AgentMetrics for OtelAgentMetrics {
    fn record_event_received(&self) {
        self.events_received.add(1, &[]);
    }

    fn record_event_dropped(&self, reason: &str) {
        self.events_dropped.add(
            1,
            &[opentelemetry::KeyValue::new("reason", reason.to_string())],
        );
    }

    fn record_render_duration(&self, seconds: f64) {
        self.render_duration.record(seconds, &[]);
    }
}

/// Builds the OpenTelemetry metrics pipeline from the (already
/// env-over-file-resolved) settings, or returns a no-op implementation —
/// immediately, touching no OTel SDK types at all — when telemetry is
/// disabled or no exporter endpoint is configured. Never panics, never
/// fails startup: any SDK setup error is logged and swallowed, falling
/// back to `NullAgentMetrics` just like the disabled case.
pub fn init(
    settings: &crate::settings::Settings,
) -> (
    std::sync::Arc<dyn notify_agent_core::metrics::AgentMetrics>,
    MetricsShutdown,
) {
    let resolved = crate::settings::resolved_otel_settings(settings);
    if !resolved.enabled || resolved.exporter_endpoint.is_empty() {
        tracing::debug!(
            "otel metrics: disabled (otelEnabled=false or no exporter endpoint configured)"
        );
        return (
            std::sync::Arc::new(notify_agent_core::metrics::NullAgentMetrics),
            MetricsShutdown::default(),
        );
    }

    match build(&resolved) {
        Ok((metrics, provider)) => {
            tracing::info!(
                endpoint = %resolved.exporter_endpoint,
                service_name = %resolved.service_name,
                "otel metrics: enabled"
            );
            (
                std::sync::Arc::new(metrics),
                MetricsShutdown(Some(provider)),
            )
        }
        Err(error) => {
            tracing::warn!(
                %error,
                endpoint = %resolved.exporter_endpoint,
                "otel metrics: setup failed, continuing without metrics"
            );
            (
                std::sync::Arc::new(notify_agent_core::metrics::NullAgentMetrics),
                MetricsShutdown::default(),
            )
        }
    }
}

/// The fallible half of `init`, split out so every failure path is a plain
/// `?`-propagated `Err` here, with exactly one place (`init`, above)
/// responsible for turning that into a logged warning plus a
/// `NullAgentMetrics` fallback — never a panic, never an aborted startup.
/// Returns the `SdkMeterProvider` alongside the metrics implementation so
/// `init` can hand the caller a shutdown-capable handle.
fn build(
    resolved: &crate::settings::ResolvedOtelSettings,
) -> anyhow::Result<(
    OtelAgentMetrics,
    opentelemetry_sdk::metrics::SdkMeterProvider,
)> {
    use opentelemetry_otlp::WithExportConfig;

    let exporter = opentelemetry_otlp::MetricExporter::builder()
        .with_http()
        .with_endpoint(&resolved.exporter_endpoint)
        .build()
        .map_err(|e| anyhow::anyhow!("building OTLP metric exporter: {e}"))?;

    let reader = opentelemetry_sdk::metrics::PeriodicReader::builder(exporter).build();
    let resource = opentelemetry_sdk::Resource::builder()
        .with_service_name(resolved.service_name.clone())
        .build();
    let provider = opentelemetry_sdk::metrics::SdkMeterProvider::builder()
        .with_resource(resource)
        .with_reader(reader)
        .build();

    // Global, so any other part of the process that reaches for
    // `opentelemetry::global::meter(...)` shares this same provider —
    // matches how this SDK is conventionally wired, and costs nothing extra
    // since this head only ever builds one provider.
    opentelemetry::global::set_meter_provider(provider.clone());

    let meter = opentelemetry::global::meter("notify-agent-windows");
    // Dot-namespaced, no type/unit suffix baked into the name -- matches OTel semantic-convention
    // guidance (e.g. "http.server.request.duration") and this product's Go implementation. A
    // Prometheus-style "_total"/"_seconds" suffix belongs to the *exposition format*, not the
    // instrument name: an OTel-to-Prometheus bridge already appends it, so baking it in here risks
    // a doubled suffix (e.g. "..._total_total") once bridged. Keep these three names identical
    // across all three language implementations of this agent.
    Ok((
        OtelAgentMetrics {
            events_received: meter
                .u64_counter("agent.events.received")
                .with_unit("1")
                .build(),
            events_dropped: meter
                .u64_counter("agent.events.dropped")
                .with_unit("1")
                .build(),
            render_duration: meter
                .f64_histogram("agent.render.duration")
                .with_unit("s")
                .build(),
        },
        provider,
    ))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::settings::Settings;
    use notify_agent_core::metrics::AgentMetrics;
    use std::sync::Mutex;

    /// Serializes tests that touch process-wide env vars, same as
    /// settings.rs's own tests.
    static ENV_LOCK: Mutex<()> = Mutex::new(());

    fn clear(vars: &[&str]) {
        for v in vars {
            std::env::remove_var(v);
        }
    }

    #[test]
    fn disabled_returns_null_metrics_without_touching_otel_sdk() {
        let _guard = ENV_LOCK.lock().unwrap();
        clear(&[
            "NOTIFY_OTEL_ENABLED",
            "NOTIFY_OTEL_EXPORTER_ENDPOINT",
            "NOTIFY_OTEL_SERVICE_NAME",
        ]);
        let (metrics, shutdown) = init(&Settings::default());
        // No live collector, no network configured anywhere in this test:
        // if init() attempted any real OTel SDK setup here, it would either
        // panic or hang. Calling every method proves it's the inert no-op.
        metrics.record_event_received();
        metrics.record_event_dropped("queue_full");
        metrics.record_render_duration(0.5);
        shutdown.shutdown(); // must be a no-op, not a hang/panic
    }

    #[test]
    fn enabled_with_no_endpoint_also_returns_null_metrics() {
        let _guard = ENV_LOCK.lock().unwrap();
        clear(&[
            "NOTIFY_OTEL_ENABLED",
            "NOTIFY_OTEL_EXPORTER_ENDPOINT",
            "NOTIFY_OTEL_SERVICE_NAME",
        ]);
        let settings = Settings {
            otel_enabled: Some(true),
            otel_exporter_endpoint: None, // blank: no endpoint configured
            ..Settings::default()
        };
        let (metrics, _shutdown) = init(&settings);
        metrics.record_event_received(); // must not panic
    }

    #[test]
    fn malformed_endpoint_falls_back_to_null_metrics_without_panicking() {
        let _guard = ENV_LOCK.lock().unwrap();
        clear(&[
            "NOTIFY_OTEL_ENABLED",
            "NOTIFY_OTEL_EXPORTER_ENDPOINT",
            "NOTIFY_OTEL_SERVICE_NAME",
        ]);
        let settings = Settings {
            otel_enabled: Some(true),
            // Deliberately not a parseable URI. Exporter *construction*
            // validates and rejects this synchronously (no network
            // involved), matching opentelemetry-otlp's own
            // `export_builder_error_invalid_http_endpoint` test for the
            // same `InvalidUri` builder error path — see otel_metrics.rs's
            // module docs and this crate's Cargo.toml comment.
            otel_exporter_endpoint: Some("not a valid uri \0".into()),
            ..Settings::default()
        };
        let (metrics, _shutdown) = init(&settings); // must not panic, must not hang
        metrics.record_event_received();
        metrics.record_event_dropped("bucket_overflow");
        metrics.record_render_duration(1.25);
    }

    #[test]
    fn build_succeeds_for_a_syntactically_valid_but_unreachable_endpoint() {
        // Exporter construction doesn't dial the network (see module docs);
        // only the background PeriodicReader's periodic export attempts
        // would, on its own thread, well after this returns. Proves `init`
        // doesn't block on connectivity.
        let resolved = crate::settings::ResolvedOtelSettings {
            enabled: true,
            exporter_endpoint: "http://127.0.0.1:1".to_string(), // valid URL, nothing listening
            service_name: "test-service".to_string(),
        };
        let (metrics, provider) =
            build(&resolved).expect("build must not require a live collector");
        metrics.record_event_received();
        metrics.record_render_duration(0.01);
        // shutdown() attempts a final flush, which dials the (deliberately
        // unreachable) endpoint and can legitimately return an Err here --
        // MetricsShutdown::shutdown() (the real caller, see its doc comment)
        // already tolerates that by logging a warning rather than
        // propagating it. This test only needs to prove the call itself
        // doesn't panic/hang, not that it always succeeds.
        let _ = provider.shutdown();
    }
}
