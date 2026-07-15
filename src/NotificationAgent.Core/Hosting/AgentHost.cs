using NATS.Client.Core;
using NotificationAgent.Core.Aggregation;
using NotificationAgent.Core.Dedup;
using NotificationAgent.Core.Identity;
using NotificationAgent.Core.Pipeline;
using NotificationAgent.Core.Rendering;
using NotificationAgent.Core.Telemetry;

namespace NotificationAgent.Core.Hosting;

public sealed class AgentOptions
{
    public string NatsUrl { get; init; } = "nats://127.0.0.1:4222";
    public string SubjectTemplate { get; init; } = "notify.user.{0}.desktop"; // design §4
    public string AckSubject { get; init; } = "notify.ack.desktop";

    public static AgentOptions FromEnvironment() => new()
    {
        NatsUrl = Environment.GetEnvironmentVariable("NOTIFY_NATS_URL") ?? "nats://127.0.0.1:4222",
        SubjectTemplate = Environment.GetEnvironmentVariable("NOTIFY_SUBJECT_TEMPLATE") ?? "notify.user.{0}.desktop",
        AckSubject = Environment.GetEnvironmentVariable("NOTIFY_ACK_SUBJECT") ?? "notify.ack.desktop",
    };
}

public sealed class NatsTelemetryPublisher : ITelemetryPublisher
{
    private readonly INatsConnection _nats;
    private readonly string _subject;

    public NatsTelemetryPublisher(INatsConnection nats, string subject)
    {
        _nats = nats;
        _subject = subject;
    }

    public async ValueTask PublishAckAsync(AckPayload ack, CancellationToken ct = default) =>
        await _nats.PublishAsync(_subject, AckJson.Serialize(ack), cancellationToken: ct)
            .ConfigureAwait(false);
}

/// <summary>Composition root: identity → NATS connection → pipeline → live subscription.
/// A plain Core NATS subscription: reconnects resume with future events only (design §6.2).</summary>
public sealed class AgentHost : IAsyncDisposable
{
    private readonly NatsConnection _nats;
    private readonly EventPipeline _pipeline;
    private readonly Aggregator _aggregator;
    private readonly CancellationTokenSource _cts = new();
    private Task? _subscription;

    public string Subject { get; }

    private AgentHost(NatsConnection nats, EventPipeline pipeline, Aggregator aggregator, string subject)
    {
        _nats = nats;
        _pipeline = pipeline;
        _aggregator = aggregator;
        Subject = subject;
    }

    public static async Task<AgentHost> StartAsync(AgentOptions options,
        IIdentityProvider identityProvider, IToastRenderer renderer, CancellationToken ct = default)
    {
        var identity = await identityProvider.GetIdentityAsync(ct).ConfigureAwait(false);
        var nats = new NatsConnection(new NatsOpts { Url = options.NatsUrl });
        try
        {
            await nats.ConnectAsync().ConfigureAwait(false);

            var telemetry = new NatsTelemetryPublisher(nats, options.AckSubject);
            var dedup = new DeduplicationCache(capacity: 10_000, ttl: TimeSpan.FromMinutes(10));
            var (pipeline, aggregator) = AgentPipelineFactory.Create(
                new PipelineOptions(), new AggregatorOptions(), dedup,
                renderer, telemetry, identity.DeviceId, TimeProvider.System);
            pipeline.Start();

            var subject = string.Format(options.SubjectTemplate, identity.UserId);
            var host = new AgentHost(nats, pipeline, aggregator, subject);
            host._subscription = Task.Run(() => host.SubscribeLoopAsync(host._cts.Token), CancellationToken.None);
            return host;
        }
        catch
        {
            await nats.DisposeAsync().ConfigureAwait(false);
            throw;
        }
    }

    private async Task SubscribeLoopAsync(CancellationToken ct)
    {
        await foreach (var msg in _nats.SubscribeAsync<byte[]>(Subject, cancellationToken: ct)
            .ConfigureAwait(false))
        {
            if (msg.Data is { Length: > 0 } data)
                _pipeline.TryEnqueue(new ReceivedEvent(data, DateTimeOffset.UtcNow));
        }
    }

    public async ValueTask DisposeAsync()
    {
        _cts.Cancel();
        if (_subscription is not null)
        {
            try { await _subscription.ConfigureAwait(false); }
            catch (OperationCanceledException) { }
        }
        await _pipeline.DisposeAsync().ConfigureAwait(false);
        await _aggregator.DisposeAsync().ConfigureAwait(false);
        await _nats.DisposeAsync().ConfigureAwait(false);
        _cts.Dispose();
    }
}
