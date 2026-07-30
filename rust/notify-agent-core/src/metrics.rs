//! Metrics-recording trait for the agent's three critical operational
//! counters/histogram. Mirrors the established pattern for pluggable,
//! host-supplied behavior in this crate (`IdentityProvider`, `ToastRenderer`,
//! `NatsAuthProvider`): a small trait lives here in Core, a trivial no-op
//! default lives here too, and any real implementation (e.g. an
//! OpenTelemetry-backed one) is supplied by whichever head needs it — see
//! `notify-agent-windows/src/otel_metrics.rs`. This deliberately keeps the
//! `opentelemetry`/`opentelemetry-otlp` crates out of this crate's (and the
//! console head's) dependency tree.
//!
//! # Crash-safety contract
//!
//! Every method here returns `()`, not `Result` — recording a metric is
//! never allowed to be a fallible operation from the caller's point of view.
//! Implementations MUST NOT panic: no `.unwrap()`/`.expect()` on anything
//! that can fail from external conditions (a bad endpoint, a network issue,
//! an SDK error), no arithmetic overflow, no out-of-bounds indexing. Any
//! internal failure must be swallowed (optionally logged via
//! `tracing::warn!`) rather than propagated, so a metrics-recording failure
//! can never interrupt event processing, dedup, aggregation, or
//! acknowledgement publishing (the actual business logic this trait's call
//! sites sit next to in `pipeline.rs`/`aggregator.rs`).
pub trait AgentMetrics: Send + Sync {
    /// One valid, first-seen event was accepted into the pipeline (fires at
    /// the same point the observed_by_agent acknowledgement does).
    fn record_event_received(&self);

    /// One event was dropped. `reason` is `"queue_full"` or
    /// `"bucket_overflow"` (matching the existing dropped_queue_full /
    /// dropped_bucket_overflow counters this call sits alongside).
    fn record_event_dropped(&self, reason: &str);

    /// One event's end-to-end agent processing latency, in seconds
    /// (toast_submitted_at - agent_received_at for that specific event).
    /// Fires once per event represented in a successfully rendered toast —
    /// once per event in a batch, not once per toast.
    fn record_render_duration(&self, seconds: f64);
}

/// No-op default. Used by the console head (which never configures
/// metrics) and as the Windows head's own fallback if OpenTelemetry setup
/// fails, so metrics-recording call sites in core never need an `Option`
/// check — there is always some `Arc<dyn AgentMetrics>` to call.
pub struct NullAgentMetrics;

impl AgentMetrics for NullAgentMetrics {
    fn record_event_received(&self) {}
    fn record_event_dropped(&self, _reason: &str) {}
    fn record_render_duration(&self, _seconds: f64) {}
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn null_agent_metrics_never_panics() {
        let m = NullAgentMetrics;
        m.record_event_received();
        m.record_event_dropped("queue_full");
        m.record_event_dropped("bucket_overflow");
        m.record_render_duration(1.5);
    }
}
