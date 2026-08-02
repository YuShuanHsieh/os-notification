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

    internal sealed class RecordingMetrics : IAgentMetrics
    {
        // One shared gate for every field: pipeline worker threads can write any of these
        // concurrently, so reads (including WaitUntilAsync's polling predicate and the final
        // assertions) must take the same lock as the writers, not just enumerate/read the
        // fields directly -- otherwise `Assert.Single(RenderDurations)` etc. can race a
        // concurrent Add or observe a stale value.
        public object Gate { get; } = new();

        public int EventsReceived
        {
            get; private set;
        }

        public List<string> Dropped { get; } = new();

        public List<double> RenderDurations { get; } = new();

        public void RecordEventReceived()
        {
            lock (Gate)
            {
                EventsReceived++;
            }
        }

        public void RecordEventDropped(string reason)
        {
            lock (Gate)
            {
                Dropped.Add(reason);
            }
        }

        public void RecordRenderDuration(double seconds)
        {
            lock (Gate)
            {
                RenderDurations.Add(seconds);
            }
        }
    }

    /// <summary>Every method throws -- used to prove the crash-safety guarantee: a
    /// throwing <see cref="IAgentMetrics"/> implementation must never be allowed to
    /// interrupt normal pipeline/aggregator operation.</summary>
    internal sealed class ThrowingMetrics : IAgentMetrics
    {
        public void RecordEventReceived() => throw new InvalidOperationException("boom: received");

        public void RecordEventDropped(string reason) => throw new InvalidOperationException("boom: dropped");

        public void RecordRenderDuration(double seconds) => throw new InvalidOperationException("boom: duration");
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

    private static ReceivedEvent NormalEvent(string id, string aggKey) => new(
        Encoding.UTF8.GetBytes(
        $"{{\"eventId\":\"{id}\",\"target\":{{\"userId\":\"u1\"}}," +
        $"\"content\":{{\"title\":\"T\",\"message\":\"M\"}}," +
        $"\"classification\":{{\"priority\":\"normal\",\"aggregationKey\":\"{aggKey}\"," +
        $"\"deduplicationKey\":\"{id}\"}}}}"), ReceivedAt);

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

    [Fact]
    public async Task Valid_event_records_received_and_render_duration_metrics()
    {
        var telemetry = new RecordingTelemetry();
        var renderer = new RecordingRenderer();
        var metrics = new RecordingMetrics();
        var (pipeline, aggregator) = AgentPipelineFactory.Create(
            new PipelineOptions(), new AggregatorOptions(),
            new DeduplicationCache(100, TimeSpan.FromMinutes(10)),
            renderer, telemetry, "d-456", new FakeTimeProvider(), metrics);
        await using var a1 = aggregator;
        await using var _p = pipeline;
        pipeline.Start();

        Assert.True(pipeline.TryEnqueue(CriticalEvent("evt-1")));
        await WaitUntilAsync(() =>
        {
            lock (metrics.Gate)
            {
                return metrics.RenderDurations.Count == 1;
            }
        });

        lock (metrics.Gate)
        {
            Assert.Equal(1, metrics.EventsReceived);
            var expectedSeconds = (renderer.SubmitAt - ReceivedAt).TotalSeconds;
            Assert.Equal(expectedSeconds, Assert.Single(metrics.RenderDurations));
            Assert.Empty(metrics.Dropped);
        }
    }

    [Fact]
    public void Queue_full_drop_records_event_dropped_metric_with_queue_full_reason()
    {
        var telemetry = new RecordingTelemetry();
        var metrics = new RecordingMetrics();
        var (pipeline, _) = AgentPipelineFactory.Create(
            new PipelineOptions { QueueCapacity = 2 }, new AggregatorOptions(),
            new DeduplicationCache(100, TimeSpan.FromMinutes(10)),
            new RecordingRenderer(), telemetry, "d-1", new FakeTimeProvider(), metrics);

        // Never started → nothing drains the channel.
        Assert.True(pipeline.TryEnqueue(CriticalEvent("e1")));
        Assert.True(pipeline.TryEnqueue(CriticalEvent("e2")));
        Assert.False(pipeline.TryEnqueue(CriticalEvent("e3")));

        Assert.Equal("queue_full", Assert.Single(metrics.Dropped));
    }

    [Fact]
    public async Task Bucket_overflow_drop_records_event_dropped_metric_with_bucket_overflow_reason()
    {
        var telemetry = new RecordingTelemetry();
        var metrics = new RecordingMetrics();
        var (pipeline, aggregator) = AgentPipelineFactory.Create(
            new PipelineOptions(), new AggregatorOptions { MaxBuckets = 1 },
            new DeduplicationCache(100, TimeSpan.FromMinutes(10)),
            new RecordingRenderer(), telemetry, "d-1", new FakeTimeProvider(), metrics);
        await using var a1 = aggregator;
        await using var _p = pipeline;
        pipeline.Start();

        // Two distinct (non-critical) aggregation keys with MaxBuckets = 1: the second
        // bucket overflows and is dropped.
        pipeline.TryEnqueue(NormalEvent("evt-a", "agg-a"));
        pipeline.TryEnqueue(NormalEvent("evt-b", "agg-b"));
        await WaitUntilAsync(() =>
        {
            lock (metrics.Gate)
            {
                return metrics.Dropped.Count == 1;
            }
        });

        lock (metrics.Gate)
        {
            Assert.Equal("bucket_overflow", Assert.Single(metrics.Dropped));
        }
    }

    [Fact]
    public async Task Throwing_metrics_implementation_never_crashes_the_pipeline()
    {
        // The crash-safety guarantee: even a metrics implementation whose every method
        // throws must not interrupt event processing, dedup, aggregation, or ack
        // publishing. Enqueue must still succeed and both acks must still be published.
        var telemetry = new RecordingTelemetry();
        var renderer = new RecordingRenderer();
        var (pipeline, aggregator) = AgentPipelineFactory.Create(
            new PipelineOptions(), new AggregatorOptions(),
            new DeduplicationCache(100, TimeSpan.FromMinutes(10)),
            renderer, telemetry, "d-456", new FakeTimeProvider(), new ThrowingMetrics());
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
    }

    [Fact]
    public void Throwing_metrics_implementation_never_crashes_queue_full_drop_reporting()
    {
        var telemetry = new RecordingTelemetry();
        var (pipeline, _) = AgentPipelineFactory.Create(
            new PipelineOptions { QueueCapacity = 2 }, new AggregatorOptions(),
            new DeduplicationCache(100, TimeSpan.FromMinutes(10)),
            new RecordingRenderer(), telemetry, "d-1", new FakeTimeProvider(), new ThrowingMetrics());

        Assert.True(pipeline.TryEnqueue(CriticalEvent("e1")));
        Assert.True(pipeline.TryEnqueue(CriticalEvent("e2")));

        // Must not throw despite ThrowingMetrics.RecordEventDropped always throwing.
        Assert.False(pipeline.TryEnqueue(CriticalEvent("e3")));
        Assert.Equal(1, pipeline.DroppedQueueFull);
    }
}
