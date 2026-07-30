using Microsoft.Extensions.Time.Testing;
using NotificationAgent.Core.Aggregation;
using NotificationAgent.Core.Models;
using NotificationAgent.Core.Rendering;
using Xunit;

namespace NotificationAgent.Core.Tests;

public class AggregatorTests
{
    private readonly FakeTimeProvider _time = new();
    private readonly List<ToastRequest> _rendered = new();

    private Aggregator Create(AggregatorOptions? options = null, EventPipelineTests.RecordingMetrics? metrics = null) =>
        new(options ?? new AggregatorOptions(), _time, toast =>
        {
            lock (_rendered)
            {
                _rendered.Add(toast);
            }

            return ValueTask.CompletedTask;
        }, metrics);

    private static InboundNotification Event(string id, EventPriority priority,
        string aggKey = "agg.key", bool replaceable = false, string message = "m") =>
        ToastContentFactoryTests.Event(id, message: message, priority: priority,
            aggKey: aggKey, replaceable: replaceable);

    [Fact]
    public async Task Critical_renders_immediately()
    {
        await using var agg = Create();
        await agg.AddAsync(Event("e1", EventPriority.Critical));
        var toast = Assert.Single(_rendered);
        Assert.Equal("e1", Assert.Single(toast.Sources).EventId);
    }

    [Fact]
    public async Task Normal_events_batch_and_flush_after_10s()
    {
        await using var agg = Create();
        await agg.AddAsync(Event("e1", EventPriority.Normal));
        await agg.AddAsync(Event("e2", EventPriority.Normal));
        await agg.AddAsync(Event("e3", EventPriority.Normal));
        Assert.Empty(_rendered);                       // window still open

        _time.Advance(TimeSpan.FromSeconds(10));
        var toast = Assert.Single(_rendered);
        Assert.Equal(3, toast.Sources.Count);
        Assert.StartsWith("3 notifications", toast.Title);
    }

    [Fact]
    public async Task Important_flushes_after_2s_normal_does_not()
    {
        await using var agg = Create();
        await agg.AddAsync(Event("i1", EventPriority.Important, aggKey: "imp"));
        await agg.AddAsync(Event("n1", EventPriority.Normal, aggKey: "norm"));

        _time.Advance(TimeSpan.FromSeconds(2));
        Assert.Equal("i1", Assert.Single(Assert.Single(_rendered).Sources).EventId);

        _time.Advance(TimeSpan.FromSeconds(8));
        Assert.Equal(2, _rendered.Count);
    }

    [Fact]
    public async Task Replaceable_keeps_only_latest_value()
    {
        await using var agg = Create();
        await agg.AddAsync(Event("p1", EventPriority.Normal, aggKey: "prog", replaceable: true, message: "10%"));
        await agg.AddAsync(Event("p2", EventPriority.Normal, aggKey: "prog", replaceable: true, message: "60%"));
        await agg.AddAsync(Event("p3", EventPriority.Normal, aggKey: "prog", replaceable: true, message: "90%"));

        _time.Advance(TimeSpan.FromSeconds(10));
        var toast = Assert.Single(_rendered);
        var source = Assert.Single(toast.Sources);     // replaced, not batched
        Assert.Equal("p3", source.EventId);
        Assert.Equal("90%", toast.Message);
    }

    [Fact]
    public async Task Separate_aggregation_keys_produce_separate_toasts()
    {
        await using var agg = Create();
        await agg.AddAsync(Event("a1", EventPriority.Normal, aggKey: "a"));
        await agg.AddAsync(Event("b1", EventPriority.Normal, aggKey: "b"));

        _time.Advance(TimeSpan.FromSeconds(10));
        Assert.Equal(2, _rendered.Count);
    }

    [Fact]
    public async Task Drops_events_beyond_max_buckets()
    {
        await using var agg = Create(new AggregatorOptions { MaxBuckets = 2 });
        await agg.AddAsync(Event("a1", EventPriority.Normal, aggKey: "a"));
        await agg.AddAsync(Event("b1", EventPriority.Normal, aggKey: "b"));
        await agg.AddAsync(Event("c1", EventPriority.Normal, aggKey: "c")); // over cap → dropped

        Assert.Equal(1, agg.DroppedBucketOverflow);
        _time.Advance(TimeSpan.FromSeconds(10));
        Assert.Equal(2, _rendered.Count);
    }

    [Fact]
    public async Task Dispose_flushes_pending_buckets()
    {
        var agg = Create();
        await agg.AddAsync(Event("e1", EventPriority.Normal));
        await agg.DisposeAsync();
        Assert.Single(_rendered);
    }

    [Fact]
    public async Task Bucket_overflow_records_dropped_metric_directly_on_the_aggregator()
    {
        var metrics = new EventPipelineTests.RecordingMetrics();
        await using var agg = Create(new AggregatorOptions { MaxBuckets = 2 }, metrics);
        await agg.AddAsync(Event("a1", EventPriority.Normal, aggKey: "a"));
        await agg.AddAsync(Event("b1", EventPriority.Normal, aggKey: "b"));
        await agg.AddAsync(Event("c1", EventPriority.Normal, aggKey: "c")); // over cap → dropped

        Assert.Equal(1, agg.DroppedBucketOverflow);
        Assert.Equal("bucket_overflow", Assert.Single(metrics.Dropped));
    }

    [Fact]
    public async Task Throwing_metrics_implementation_never_crashes_bucket_overflow_reporting()
    {
        // Crash-safety guarantee: a metrics implementation whose RecordEventDropped throws
        // must not prevent the overflow itself from being tracked/dropped correctly.
        var throwingMetrics = new EventPipelineTests.ThrowingMetrics();
        var aggregator = new Aggregator(new AggregatorOptions { MaxBuckets = 1 }, _time, toast =>
        {
            lock (_rendered)
            {
                _rendered.Add(toast);
            }

            return ValueTask.CompletedTask;
        }, throwingMetrics);
        await using var _ = aggregator;

        await aggregator.AddAsync(Event("a1", EventPriority.Normal, aggKey: "a"));
        await aggregator.AddAsync(Event("b1", EventPriority.Normal, aggKey: "b")); // over cap

        Assert.Equal(1, aggregator.DroppedBucketOverflow);
    }
}
