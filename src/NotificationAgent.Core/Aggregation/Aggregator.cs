using NotificationAgent.Core.Models;
using NotificationAgent.Core.Rendering;
using NotificationAgent.Core.Telemetry;

namespace NotificationAgent.Core.Aggregation;

public sealed class AggregatorOptions
{
    public int MaxBuckets { get; init; } = 100;

    public TimeSpan ImportantWindow { get; init; } = TimeSpan.FromSeconds(2);

    public TimeSpan NormalWindow { get; init; } = TimeSpan.FromSeconds(10);
}

/// <summary>Owns priority handling, batching, and latest-state replacement (ADR-007).
/// Best-effort: render failures are swallowed, bucket overflow drops events.</summary>
public sealed class Aggregator : IAsyncDisposable
{
    private sealed class Bucket
    {
        public List<InboundNotification> Events { get; } = new();

        public ITimer? Timer
        {
            get; set;
        }
    }

    private readonly object _gate = new();
    private readonly Dictionary<(string Key, EventPriority Priority), Bucket> _buckets = new();
    private readonly AggregatorOptions _options;
    private readonly TimeProvider _time;
    private readonly Func<ToastRequest, ValueTask> _renderAsync;
    private readonly IAgentMetrics _metrics;
    private long _droppedBucketOverflow;

    public long DroppedBucketOverflow => Interlocked.Read(ref _droppedBucketOverflow);

    public Aggregator(
        AggregatorOptions options,
        TimeProvider time,
        Func<ToastRequest, ValueTask> renderAsync,
        IAgentMetrics? metrics = null)
    {
        _options = options;
        _time = time;
        _renderAsync = renderAsync;
        _metrics = metrics ?? NullAgentMetrics.Instance;
    }

    public async ValueTask AddAsync(InboundNotification n)
    {
        if (n.Priority == EventPriority.Critical)
        {
            await RenderSafeAsync(ToastContentFactory.FromSingle(n)).ConfigureAwait(false);
            return;
        }

        lock (_gate)
        {
            var key = (n.AggregationKey, n.Priority);
            if (!_buckets.TryGetValue(key, out var bucket))
            {
                if (_buckets.Count >= _options.MaxBuckets)
                {
                    Interlocked.Increment(ref _droppedBucketOverflow);
                    _metrics.SafeRecordEventDropped("bucket_overflow");
                    return;
                }

                bucket = new Bucket();
                _buckets[key] = bucket;
                var window = n.Priority == EventPriority.Important
                    ? _options.ImportantWindow : _options.NormalWindow;
                bucket.Timer = _time.CreateTimer(_ => Flush(key), null, window, Timeout.InfiniteTimeSpan);
            }

            if (n.Replaceable)
            {
                bucket.Events.Clear();
            }

            bucket.Events.Add(n);
        }
    }

    private void Flush((string Key, EventPriority Priority) key)
    {
        List<InboundNotification>? events = null;
        lock (_gate)
        {
            if (_buckets.Remove(key, out var bucket))
            {
                bucket.Timer?.Dispose();
                events = bucket.Events;
            }
        }

        if (events is { Count: > 0 })
        {
            var pending = RenderSafeAsync(ToastContentFactory.FromBatch(events));
            if (!pending.IsCompleted)
            {
                _ = pending.AsTask();
            }
        }
    }

    private async ValueTask RenderSafeAsync(ToastRequest toast)
    {
        try
        {
            await _renderAsync(toast).ConfigureAwait(false);
        }
        catch
        { /* best-effort delivery: a render failure must not crash the agent */
        }
    }

    public async ValueTask DisposeAsync()
    {
        List<(string, EventPriority)> keys;
        lock (_gate)
        {
            keys = _buckets.Keys.ToList();
        }

        foreach (var key in keys)
        {
            Flush(key);
        }

        await Task.CompletedTask;
    }
}
