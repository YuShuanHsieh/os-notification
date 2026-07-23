using System.Text;
using Microsoft.Extensions.Time.Testing;
using NotificationAgent.Core.Aggregation;
using NotificationAgent.Core.Dedup;
using NotificationAgent.Core.Pipeline;
using NotificationAgent.Core.Rendering;
using NotificationAgent.Core.Telemetry;
using Xunit;

namespace NotificationAgent.Core.Tests;

public class EventPipelineTests
{
    private sealed class RecordingTelemetry : ITelemetryPublisher
    {
        public List<AckPayload> Acks { get; } = new();

        public ValueTask PublishAckAsync(AckPayload ack, CancellationToken ct = default)
        {
            lock (Acks)
            {
                Acks.Add(ack);
            }

            return ValueTask.CompletedTask;
        }
    }

    private sealed class RecordingRenderer : IToastRenderer
    {
        public List<ToastRequest> Shown { get; } = new();

        public DateTimeOffset SubmitAt { get; set; } = DateTimeOffset.Parse("2026-07-15T08:30:00.205Z");

        public ValueTask<DateTimeOffset> ShowAsync(ToastRequest toast, CancellationToken ct = default)
        {
            lock (Shown)
            {
                Shown.Add(toast);
            }

            return ValueTask.FromResult(SubmitAt);
        }
    }

    private static readonly DateTimeOffset ReceivedAt = DateTimeOffset.Parse("2026-07-15T08:30:00.190Z");

    private static ReceivedEvent CriticalEvent(string id) => new(
        Encoding.UTF8.GetBytes(
        $"{{\"eventId\":\"{id}\",\"target\":{{\"userId\":\"u1\"}}," +
        $"\"content\":{{\"title\":\"T\",\"message\":\"M\"}}," +
        $"\"classification\":{{\"priority\":\"critical\",\"deduplicationKey\":\"{id}\"}}}}"), ReceivedAt);

    private static async Task WaitUntilAsync(Func<bool> condition)
    {
        for (var i = 0; i < 500 && !condition(); i++)
        {
            await Task.Delay(10);
        }

        Assert.True(condition(), "condition not reached within 5s");
    }

    [Fact]
    public async Task Valid_critical_event_flows_to_renderer_with_both_acks()
    {
        var telemetry = new RecordingTelemetry();
        var renderer = new RecordingRenderer();
        var (pipeline, aggregator) = AgentPipelineFactory.Create(
            new PipelineOptions(), new AggregatorOptions(),
            new DeduplicationCache(100, TimeSpan.FromMinutes(10)),
            renderer, telemetry, deviceId: "d-456", new FakeTimeProvider());
        await using var a1 = aggregator;
        await using var _p = pipeline;
        pipeline.Start();

        Assert.True(pipeline.TryEnqueue(CriticalEvent("evt-1")));
        await WaitUntilAsync(() =>
        {
            lock (telemetry.Acks)
            {
                return telemetry.Acks.Count == 2;
            }
        });

        Assert.Single(renderer.Shown);
        var observed = telemetry.Acks.Single(a => a.Status == AckStatuses.ObservedByAgent);
        var submitted = telemetry.Acks.Single(a => a.Status == AckStatuses.SubmittedToWindows);
        Assert.Equal("evt-1", observed.EventId);
        Assert.Equal("d-456", observed.DeviceId);
        Assert.Equal(ReceivedAt, observed.AgentReceivedAt);
        Assert.Null(observed.ToastSubmittedAt);
        Assert.Equal(renderer.SubmitAt, submitted.ToastSubmittedAt);
        Assert.Equal(ReceivedAt, submitted.AgentReceivedAt);
    }

    [Fact]
    public async Task Duplicate_events_are_processed_once()
    {
        var telemetry = new RecordingTelemetry();
        var renderer = new RecordingRenderer();
        var (pipeline, aggregator) = AgentPipelineFactory.Create(
            new PipelineOptions(), new AggregatorOptions(),
            new DeduplicationCache(100, TimeSpan.FromMinutes(10)),
            renderer, telemetry, "d-1", new FakeTimeProvider());
        await using var _a = aggregator;
        await using var _p = pipeline;
        pipeline.Start();

        pipeline.TryEnqueue(CriticalEvent("evt-dup"));
        pipeline.TryEnqueue(CriticalEvent("evt-dup"));
        pipeline.TryEnqueue(CriticalEvent("evt-dup"));
        await WaitUntilAsync(() =>
        {
            lock (telemetry.Acks)
            {
                return telemetry.Acks.Count >= 2;
            }
        });
        await Task.Delay(100); // grace period: no further acks should arrive

        Assert.Single(renderer.Shown);
        Assert.Equal(2, telemetry.Acks.Count); // one observed + one submitted
    }

    [Fact]
    public async Task Invalid_payloads_are_dropped_silently()
    {
        var telemetry = new RecordingTelemetry();
        var renderer = new RecordingRenderer();
        var (pipeline, aggregator) = AgentPipelineFactory.Create(
            new PipelineOptions(), new AggregatorOptions(),
            new DeduplicationCache(100, TimeSpan.FromMinutes(10)),
            renderer, telemetry, "d-1", new FakeTimeProvider());
        await using var a1 = aggregator;
        pipeline.Start();

        pipeline.TryEnqueue(new ReceivedEvent(Encoding.UTF8.GetBytes("garbage"), ReceivedAt));
        pipeline.TryEnqueue(CriticalEvent("evt-ok"));   // proves the worker survived
        await WaitUntilAsync(() =>
        {
            lock (telemetry.Acks)
            {
                return telemetry.Acks.Count == 2;
            }
        });

        await pipeline.DisposeAsync();
        Assert.Single(renderer.Shown);
        Assert.All(telemetry.Acks, a => Assert.Equal("evt-ok", a.EventId));
    }

    [Fact]
    public void TryEnqueue_reports_drop_when_queue_full()
    {
        var telemetry = new RecordingTelemetry();
        var (pipeline, _) = AgentPipelineFactory.Create(
            new PipelineOptions { QueueCapacity = 2 }, new AggregatorOptions(),
            new DeduplicationCache(100, TimeSpan.FromMinutes(10)),
            new RecordingRenderer(), telemetry, "d-1", new FakeTimeProvider());

        // Never started → nothing drains the channel.
        Assert.True(pipeline.TryEnqueue(CriticalEvent("e1")));
        Assert.True(pipeline.TryEnqueue(CriticalEvent("e2")));
        Assert.False(pipeline.TryEnqueue(CriticalEvent("e3")));
        Assert.Equal(1, pipeline.DroppedQueueFull);
    }
}
