// Package metrics defines a small, OpenTelemetry-free interface for the
// three operational metrics this agent records (events received, events
// dropped, and render latency). It exists so internal/host (and, through
// it, internal/pipeline and internal/aggregator) never has to import any
// go.opentelemetry.io/otel* package -- following the same "define the
// interface in internal/*, let the head that needs a real implementation
// supply it" pattern already used by identity.Provider, toast.Renderer, and
// natsauth.Provider (see e.g. internal/identity's package doc). Only
// cmd/notify-agent-windows constructs a real OpenTelemetry-backed
// AgentMetrics; cmd/notify-agent-console never touches this package's
// NullAgentMetrics is used unconditionally, and internal/host defaults to it
// whenever no metrics implementation is supplied.
package metrics

// AgentMetrics records the three critical operational metrics for this
// agent. Implementations (e.g. an OpenTelemetry-backed one, supplied by the
// Windows head) must never panic -- a metrics-recording failure must never
// be allowed to interrupt event processing, dedup, aggregation, or
// acknowledgement publishing. Every caller in this repository additionally
// wraps every call to these methods in its own defer/recover as an explicit
// extra safety net (see internal/host's safeRecord), on top of whatever
// panic-safety an implementation provides internally -- this is a
// deliberate, unusual, user-mandated requirement: metrics-recording code
// must never be able to crash the application, even via an unrecovered
// panic somehow escaping the implementation itself.
type AgentMetrics interface {
	// RecordEventReceived fires once per valid, first-seen event accepted
	// into the pipeline -- the same point the observed_by_agent
	// acknowledgement is published.
	RecordEventReceived()

	// RecordEventDropped fires once per dropped event. reason is
	// "queue_full" or "bucket_overflow", matching the existing
	// DroppedQueueFull/DroppedBucketOverflow counters this call sits
	// alongside.
	RecordEventDropped(reason string)

	// RecordRenderDuration fires once per event represented in a
	// successfully rendered toast (once per event in a batch, not once per
	// toast), with that event's own end-to-end agent processing latency in
	// seconds (toastSubmittedAt - that event's AgentReceivedAt).
	RecordRenderDuration(seconds float64)
}

// NullAgentMetrics is a no-op AgentMetrics. Used by the console head (which
// never configures metrics), by internal/host/pipeline/aggregator whenever
// no AgentMetrics is supplied, and as the Windows head's own fallback if
// OpenTelemetry setup fails -- so metrics-recording call sites never need a
// nil check of their own.
type NullAgentMetrics struct{}

// RecordEventReceived implements AgentMetrics.
func (NullAgentMetrics) RecordEventReceived() {}

// RecordEventDropped implements AgentMetrics.
func (NullAgentMetrics) RecordEventDropped(reason string) {}

// RecordRenderDuration implements AgentMetrics.
func (NullAgentMetrics) RecordRenderDuration(seconds float64) {}
