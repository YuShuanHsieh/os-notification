// OpenTelemetry-backed metrics.AgentMetrics implementation for the Windows
// head, and the only place in this repository that imports any
// go.opentelemetry.io/otel* package -- internal/host/pipeline/aggregator
// only depend on the small internal/metrics interface, and
// cmd/notify-agent-console never touches OpenTelemetry at all (see
// internal/metrics's package doc). No `//go:build windows` constraint: OTel
// SDK setup is pure Go with no Windows-specific APIs involved, so this
// stays buildable/testable on any platform, same as settings.go.
package main

import (
	"context"
	"os"
	"strings"

	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/YuShuanHsieh/os-notification/golang/internal/metrics"
)

// meterName identifies this instrumentation library to the OTel SDK/
// exporter -- distinct from otelServiceName (the resource attribute
// identifying the running process/service as a whole).
const meterName = "notify-agent-windows"

var _ metrics.AgentMetrics = (*otelAgentMetrics)(nil)

// otelAgentMetrics is the real OpenTelemetry-backed metrics.AgentMetrics
// implementation, constructed only by InitMetrics when telemetry is enabled
// and configured. Every method wraps its OTel instrument call in its own
// defer/recover, on top of the safeRecord guard every caller in
// internal/host already applies around any AgentMetrics call -- deliberate
// belt-and-suspenders per this feature's explicit, user-mandated
// requirement that metrics-recording code must never be able to crash the
// application. The stable OTel Go metric API's Add/Record calls are
// documented as non-blocking and don't themselves return errors, but this
// wrapping stays anyway as defense in depth (e.g. against a panic while
// building the attribute Set).
type otelAgentMetrics struct {
	eventsReceived otelmetric.Int64Counter
	eventsDropped  otelmetric.Int64Counter
	renderSeconds  otelmetric.Float64Histogram
}

// RecordEventReceived implements metrics.AgentMetrics.
func (m *otelAgentMetrics) RecordEventReceived() {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("otelmetrics: recovered from panic in RecordEventReceived", "panic", r)
		}
	}()
	m.eventsReceived.Add(context.Background(), 1)
}

// RecordEventDropped implements metrics.AgentMetrics.
func (m *otelAgentMetrics) RecordEventDropped(reason string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("otelmetrics: recovered from panic in RecordEventDropped", "panic", r, "reason", reason)
		}
	}()
	m.eventsDropped.Add(context.Background(), 1, otelmetric.WithAttributes(attribute.String("reason", reason)))
}

// RecordRenderDuration implements metrics.AgentMetrics.
func (m *otelAgentMetrics) RecordRenderDuration(seconds float64) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("otelmetrics: recovered from panic in RecordRenderDuration", "panic", r)
		}
	}()
	m.renderSeconds.Record(context.Background(), seconds)
}

// InitMetrics resolves this head's OpenTelemetry configuration from s (env
// vars NOTIFY_OTEL_ENABLED/NOTIFY_OTEL_EXPORTER_ENDPOINT/
// NOTIFY_OTEL_SERVICE_NAME win over the settings file, per
// ResolveOtelEnabled/ResolveOtelExporterEndpoint/ResolveOtelServiceName) and,
// if enabled with a non-blank exporter endpoint, builds a real
// OTLP/HTTP-backed metrics.AgentMetrics. If telemetry is disabled or the
// endpoint is blank, this returns metrics.NullAgentMetrics{} immediately
// without touching any OTel SDK type -- zero overhead when off. Any failure
// constructing the exporter/provider/instruments (or an unexpected panic
// anywhere in this sequence) is logged via slog.Error and swallowed,
// falling back to metrics.NullAgentMetrics{} -- a telemetry
// misconfiguration must never abort agent startup or be treated as fatal by
// main.go.
func InitMetrics(ctx context.Context, s Settings) (result metrics.AgentMetrics) {
	result = metrics.NullAgentMetrics{}
	defer func() {
		if r := recover(); r != nil {
			slog.Error("otelmetrics: recovered from panic during InitMetrics, falling back to no-op metrics", "panic", r)
			result = metrics.NullAgentMetrics{}
		}
	}()

	enabled := ResolveOtelEnabled(os.Getenv, s)
	endpoint := ResolveOtelExporterEndpoint(os.Getenv, s)
	if !enabled || strings.TrimSpace(endpoint) == "" {
		slog.Debug("otelmetrics: disabled or no exporter endpoint configured, using no-op metrics", "enabled", enabled, "endpointConfigured", strings.TrimSpace(endpoint) != "")
		return metrics.NullAgentMetrics{}
	}
	serviceName := ResolveOtelServiceName(os.Getenv, s)

	exporter, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpoint(endpoint),
		otlpmetrichttp.WithInsecure(), // this agent has no TLS-cert configuration surface of its own; operators pointing at an HTTPS collector should terminate TLS in front of it
	)
	if err != nil {
		slog.Error("otelmetrics: failed to create OTLP metric exporter, falling back to no-op metrics", "error", err, "endpoint", endpoint)
		return metrics.NullAgentMetrics{}
	}

	res := resource.NewWithAttributes("", attribute.String("service.name", serviceName))
	reader := sdkmetric.NewPeriodicReader(exporter)
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader), sdkmetric.WithResource(res))
	otel.SetMeterProvider(provider)

	meter := otel.Meter(meterName)
	eventsReceived, err := meter.Int64Counter(
		"agent.events.received",
		otelmetric.WithDescription("Count of valid, first-seen events accepted into the pipeline"),
	)
	if err != nil {
		slog.Error("otelmetrics: failed to create events-received counter, falling back to no-op metrics", "error", err)
		return metrics.NullAgentMetrics{}
	}
	eventsDropped, err := meter.Int64Counter(
		"agent.events.dropped",
		otelmetric.WithDescription("Count of dropped events, tagged by reason (queue_full, bucket_overflow)"),
	)
	if err != nil {
		slog.Error("otelmetrics: failed to create events-dropped counter, falling back to no-op metrics", "error", err)
		return metrics.NullAgentMetrics{}
	}
	renderSeconds, err := meter.Float64Histogram(
		"agent.render.duration",
		otelmetric.WithDescription("End-to-end agent processing latency per event represented in a rendered toast"),
		otelmetric.WithUnit("s"),
	)
	if err != nil {
		slog.Error("otelmetrics: failed to create render-duration histogram, falling back to no-op metrics", "error", err)
		return metrics.NullAgentMetrics{}
	}

	slog.Info("otelmetrics: OpenTelemetry metrics export enabled", "endpoint", endpoint, "serviceName", serviceName)
	return &otelAgentMetrics{
		eventsReceived: eventsReceived,
		eventsDropped:  eventsDropped,
		renderSeconds:  renderSeconds,
	}
}
