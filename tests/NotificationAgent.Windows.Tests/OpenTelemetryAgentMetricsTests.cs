using Microsoft.Extensions.Logging;
using NotificationAgent.Core.Telemetry;
using NotificationAgent.Windows;
using Xunit;

namespace NotificationAgent.Windows.Tests;

public class OpenTelemetryAgentMetricsTests
{
    [Fact]
    public void Create_returns_null_agent_metrics_when_otel_disabled()
    {
        var metrics = OpenTelemetryAgentMetrics.Create(
            enabled: false, exporterEndpoint: "http://collector:4317", serviceName: "svc");

        Assert.Same(NullAgentMetrics.Instance, metrics);
    }

    [Fact]
    public void Create_returns_null_agent_metrics_when_enabled_but_endpoint_blank()
    {
        var metrics = OpenTelemetryAgentMetrics.Create(
            enabled: true, exporterEndpoint: null, serviceName: "svc");

        Assert.Same(NullAgentMetrics.Instance, metrics);
    }

    [Fact]
    public void Create_returns_null_agent_metrics_when_enabled_but_endpoint_whitespace()
    {
        var metrics = OpenTelemetryAgentMetrics.Create(
            enabled: true, exporterEndpoint: "   ", serviceName: "svc");

        Assert.Same(NullAgentMetrics.Instance, metrics);
    }

    [Fact]
    public void Create_falls_back_to_null_agent_metrics_on_malformed_exporter_endpoint()
    {
        // A malformed URI must never fail startup -- Create must catch the Uri
        // construction failure internally and fall back to no-op metrics.
        var metrics = OpenTelemetryAgentMetrics.Create(
            enabled: true, exporterEndpoint: "not a valid uri", serviceName: "svc");

        Assert.Same(NullAgentMetrics.Instance, metrics);
    }

    [Fact]
    public void Create_with_disabled_telemetry_never_throws_when_methods_are_called()
    {
        var metrics = OpenTelemetryAgentMetrics.Create(
            enabled: false, exporterEndpoint: null, serviceName: "svc");

        var exception = Record.Exception(() =>
        {
            metrics.RecordEventReceived();
            metrics.RecordEventDropped("queue_full");
            metrics.RecordRenderDuration(1.5);
        });

        Assert.Null(exception);
    }

    [Fact]
    public void Create_with_valid_endpoint_builds_a_real_instance_and_never_throws_when_called()
    {
        // Construction alone must not require a live OTLP collector: OTLP export failures
        // happen later, asynchronously, on the periodic export timer -- not at Build() time.
        var metrics = OpenTelemetryAgentMetrics.Create(
            enabled: true, exporterEndpoint: "http://127.0.0.1:4317", serviceName: "svc");

        Assert.IsType<OpenTelemetryAgentMetrics>(metrics);

        var exception = Record.Exception(() =>
        {
            metrics.RecordEventReceived();
            metrics.RecordEventDropped("bucket_overflow");
            metrics.RecordRenderDuration(0.25);
        });

        Assert.Null(exception);

        (metrics as IDisposable)?.Dispose();
    }

    [Fact]
    public void Create_logs_a_warning_when_setup_fails()
    {
        var logger = new RecordingLogger();

        var metrics = OpenTelemetryAgentMetrics.Create(
            enabled: true, exporterEndpoint: "not a valid uri", serviceName: "svc", logger);

        Assert.Same(NullAgentMetrics.Instance, metrics);
        var entry = Assert.Single(logger.Entries);
        Assert.Equal(LogLevel.Warning, entry.Level);
    }

    [Fact]
    public void Create_logs_a_warning_when_enabled_without_an_endpoint()
    {
        var logger = new RecordingLogger();

        var metrics = OpenTelemetryAgentMetrics.Create(
            enabled: true, exporterEndpoint: null, serviceName: "svc", logger);

        Assert.Same(NullAgentMetrics.Instance, metrics);
        var entry = Assert.Single(logger.Entries);
        Assert.Equal(LogLevel.Warning, entry.Level);
    }

    private sealed class RecordingLogger : ILogger
    {
        public List<(LogLevel Level, string Message)> Entries { get; } = new();

        public IDisposable? BeginScope<TState>(TState state)
            where TState : notnull => null;

        public bool IsEnabled(LogLevel logLevel) => true;

        public void Log<TState>(
            LogLevel logLevel,
            EventId eventId,
            TState state,
            Exception? exception,
            Func<TState, Exception?, string> formatter) =>
            Entries.Add((logLevel, formatter(state, exception)));
    }
}
