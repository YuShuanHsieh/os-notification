// src/NotificationAgent.Windows/OpenTelemetryAgentMetrics.cs
using System.Diagnostics.Metrics;
using Microsoft.Extensions.Logging;
using NotificationAgent.Core.Telemetry;
using OpenTelemetry;
using OpenTelemetry.Metrics;
using OpenTelemetry.Resources;

namespace NotificationAgent.Windows;

/// <summary>OpenTelemetry-backed <see cref="IAgentMetrics"/> implementation (feature: OTel
/// metrics). This is the only place in the codebase that references the <c>OpenTelemetry</c>
/// NuGet package -- Core and the console host stay free of it, matching the existing
/// pattern for <c>IToastRenderer</c>/<c>IIdentityProvider</c>/<c>INatsAuthProvider</c>: the
/// interface lives in Core, only the Windows head supplies a concrete, vendor-specific
/// implementation.
///
/// Every public method is wrapped in its own try/catch as defense in depth: Core's
/// <c>SafeAgentMetrics</c> already guards every call into <see cref="IAgentMetrics"/>, but
/// the "never crash the application" requirement applies to this type directly too, so it
/// does not rely solely on callers behaving.</summary>
public sealed class OpenTelemetryAgentMetrics : IAgentMetrics, IDisposable
{
    private const string MeterName = "NotificationAgent.Windows";

    private readonly Meter _meter;
    private readonly Counter<long> _eventsReceived;
    private readonly Counter<long> _eventsDropped;
    private readonly Histogram<double> _renderDuration;
    private readonly MeterProvider? _meterProvider;

    private OpenTelemetryAgentMetrics(Meter meter, MeterProvider? meterProvider)
    {
        _meter = meter;
        _meterProvider = meterProvider;

        // Dot-namespaced, no type/unit suffix baked into the name -- matches OTel semantic-convention
        // guidance (e.g. "http.server.request.duration") and this product's Go implementation.
        // A Prometheus-style "_total"/"_seconds" suffix belongs to the *exposition format*, not the
        // instrument name: an OTel-to-Prometheus bridge already appends it, so baking it in here risks
        // a doubled suffix (e.g. "..._total_total") once bridged. Keep these three names identical
        // across all three language implementations of this agent.
        _eventsReceived = _meter.CreateCounter<long>(
            "agent.events.received",
            unit: "1",
            description: "Count of valid, first-seen events accepted into the pipeline.");
        _eventsDropped = _meter.CreateCounter<long>(
            "agent.events.dropped",
            unit: "1",
            description: "Count of events dropped, tagged by reason (queue_full, bucket_overflow).");
        _renderDuration = _meter.CreateHistogram<double>(
            "agent.render.duration",
            unit: "s",
            description: "End-to-end agent processing latency per event, from receipt to toast submission.");
    }

    /// <summary>Builds a real OpenTelemetry-backed metrics implementation from resolved
    /// settings, or falls back to <see cref="NullAgentMetrics.Instance"/> if telemetry is
    /// disabled, unconfigured, or setup fails for any reason (bad endpoint URI, SDK
    /// initialization failure, etc.) -- metrics setup must never prevent the agent from
    /// starting and running normally.</summary>
    public static IAgentMetrics Create(
        bool enabled, string? exporterEndpoint, string serviceName, ILogger? logger = null)
    {
        if (!enabled)
        {
            return NullAgentMetrics.Instance;
        }

        if (exporterEndpoint is not { Length: > 0 })
        {
            logger?.OtelEnabledWithoutEndpoint();
            return NullAgentMetrics.Instance;
        }

        try
        {
            var meter = new Meter(MeterName);
            var meterProvider = Sdk.CreateMeterProviderBuilder()
                .AddMeter(MeterName)
                .ConfigureResource(r => r.AddService(serviceName))
                .AddOtlpExporter(o => o.Endpoint = new Uri(exporterEndpoint))
                .Build();
            return new OpenTelemetryAgentMetrics(meter, meterProvider);
        }
        catch (Exception ex)
        {
            // Anything here -- malformed URI, SDK init failure, whatever -- must fall back
            // to no-op metrics rather than fail startup (feature requirement: telemetry
            // misconfiguration must never take down the agent).
            logger?.OtelSetupFailed(ex);
            return NullAgentMetrics.Instance;
        }
    }

    public void RecordEventReceived()
    {
        try
        {
            _eventsReceived.Add(1);
        }
        catch
        {
            // See type-level doc comment: never let a metrics call crash the caller.
        }
    }

    public void RecordEventDropped(string reason)
    {
        try
        {
            _eventsDropped.Add(1, new KeyValuePair<string, object?>("reason", reason));
        }
        catch
        {
            // See type-level doc comment: never let a metrics call crash the caller.
        }
    }

    public void RecordRenderDuration(double seconds)
    {
        try
        {
            _renderDuration.Record(seconds);
        }
        catch
        {
            // See type-level doc comment: never let a metrics call crash the caller.
        }
    }

    public void Dispose()
    {
        try
        {
            _meterProvider?.Dispose();
        }
        catch
        {
            // Shutdown-time metrics teardown must never throw.
        }

        _meter.Dispose();
    }
}
