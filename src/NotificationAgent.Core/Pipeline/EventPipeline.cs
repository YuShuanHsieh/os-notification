using System.Threading.Channels;
using NotificationAgent.Core.Aggregation;
using NotificationAgent.Core.Dedup;
using NotificationAgent.Core.Rendering;
using NotificationAgent.Core.Serialization;
using NotificationAgent.Core.Telemetry;

namespace NotificationAgent.Core.Pipeline;

public sealed record ReceivedEvent(byte[] Payload, DateTimeOffset ReceivedAt);

public sealed class PipelineOptions
{
    public int QueueCapacity { get; init; } = 500;  // design §9 baseline

    public int WorkerCount { get; init; } = 2;      // design §9 baseline
}

/// <summary>Bounded intake queue with a fixed worker pool (design §9). Overload drops
/// events at the queue boundary — memory stays bounded, delivery stays best-effort.</summary>
public sealed class EventPipeline : IAsyncDisposable
{
    private readonly Channel<ReceivedEvent> _channel;
    private readonly EventParser _parser = new();
    private readonly DeduplicationCache _dedup;
    private readonly Aggregator _aggregator;
    private readonly ITelemetryPublisher _telemetry;
    private readonly string _deviceId;
    private readonly PipelineOptions _options;
    private readonly List<Task> _workers = new();
    private readonly CancellationTokenSource _cts = new();
    private long _droppedQueueFull;

    public long DroppedQueueFull => Interlocked.Read(ref _droppedQueueFull);

    public EventPipeline(
        PipelineOptions options,
        DeduplicationCache dedup,
        Aggregator aggregator,
        ITelemetryPublisher telemetry,
        string deviceId)
    {
        _options = options;
        _dedup = dedup;
        _aggregator = aggregator;
        _telemetry = telemetry;
        _deviceId = deviceId;

        // Wait mode + TryWrite (never blocks): TryWrite returns false when full, which
        // is our observable drop signal. DropWrite would return true and drop silently,
        // making DroppedQueueFull impossible to count.
        _channel = Channel.CreateBounded<ReceivedEvent>(new BoundedChannelOptions(options.QueueCapacity)
        {
            FullMode = BoundedChannelFullMode.Wait,
            SingleReader = false,
            SingleWriter = false,
        });
    }

    public bool TryEnqueue(ReceivedEvent evt)
    {
        if (_channel.Writer.TryWrite(evt))
        {
            return true;
        }

        Interlocked.Increment(ref _droppedQueueFull);
        return false;
    }

    public void Start()
    {
        for (var i = 0; i < _options.WorkerCount; i++)
        {
            _workers.Add(Task.Run(() => WorkerLoopAsync(_cts.Token)));
        }
    }

    private async Task WorkerLoopAsync(CancellationToken ct)
    {
        await foreach (var received in _channel.Reader.ReadAllAsync(ct).ConfigureAwait(false))
        {
            try
            {
                await ProcessAsync(received, ct).ConfigureAwait(false);
            }
            catch (OperationCanceledException) when (ct.IsCancellationRequested)
            {
                return;
            }
            catch
            {
                // best-effort: one poison event must not kill the worker
            }
        }
    }

    private async ValueTask ProcessAsync(ReceivedEvent received, CancellationToken ct)
    {
        if (!_parser.TryParse(received.Payload, received.ReceivedAt, out var n, out _))
        {
            return;
        }

        if (!_dedup.TryAdd(n!.DeduplicationKey))
        {
            return;
        }

        await _telemetry.PublishAckAsync(
            new AckPayload(n.EventId, _deviceId, n.ReceivedAt, null, AckStatuses.ObservedByAgent),
            ct).ConfigureAwait(false);
        await _aggregator.AddAsync(n).ConfigureAwait(false);
    }

    public async ValueTask DisposeAsync()
    {
        _channel.Writer.TryComplete();
        try
        {
            await Task.WhenAll(_workers).ConfigureAwait(false);
        }
        catch
        { /* worker cancellation during shutdown is fine */
        }

        _cts.Cancel();
        _cts.Dispose();
    }
}

public static class AgentPipelineFactory
{
    /// <summary>Wires renderer + telemetry into the aggregator's flush path and returns
    /// the pipeline plus the aggregator (dispose the pipeline first, then the aggregator).</summary>
    public static (EventPipeline Pipeline, Aggregator Aggregator) Create(
        PipelineOptions pipelineOptions,
        AggregatorOptions aggregatorOptions,
        DeduplicationCache dedup,
        IToastRenderer renderer,
        ITelemetryPublisher telemetry,
        string deviceId,
        TimeProvider timeProvider)
    {
        var aggregator = new Aggregator(aggregatorOptions, timeProvider, async toast =>
        {
            var submittedAt = await renderer.ShowAsync(toast).ConfigureAwait(false);
            foreach (var source in toast.Sources)
            {
                await telemetry.PublishAckAsync(new AckPayload(
                    source.EventId,
                    deviceId,
                    source.ReceivedAt,
                    submittedAt,
                    AckStatuses.SubmittedToWindows)).ConfigureAwait(false);
            }
        });
        var pipeline = new EventPipeline(pipelineOptions, dedup, aggregator, telemetry, deviceId);
        return (pipeline, aggregator);
    }
}
