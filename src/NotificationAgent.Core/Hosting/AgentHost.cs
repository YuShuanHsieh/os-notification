using System.Globalization;
using NATS.Client.Core;
using NotificationAgent.Core.Aggregation;
using NotificationAgent.Core.Dedup;
using NotificationAgent.Core.Identity;
using NotificationAgent.Core.Nats;
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

    public string Subject
    {
        get;
    }

    /// <summary>Mirrors the sibling Go implementation's <c>Host.DroppedQueueFull()</c>
    /// accessor for cross-language observability parity.</summary>
    public long DroppedQueueFull => _pipeline.DroppedQueueFull;

    /// <summary>Mirrors the sibling Go implementation's <c>Host.DroppedBucketOverflow()</c>
    /// accessor for cross-language observability parity.</summary>
    public long DroppedBucketOverflow => _aggregator.DroppedBucketOverflow;

    private AgentHost(NatsConnection nats, EventPipeline pipeline, Aggregator aggregator, string subject)
    {
        _nats = nats;
        _pipeline = pipeline;
        _aggregator = aggregator;
        Subject = subject;
    }

    /// <summary>Builds connection options, applying auth if a provider is configured (design §2).</summary>
    internal static NatsOpts BuildNatsOpts(AgentOptions options, INatsAuthProvider? authProvider)
    {
        var opts = new NatsOpts { Url = options.NatsUrl };
        return authProvider is null ? opts : opts with
        {
            AuthOpts = authProvider.GetAuthOpts(),
        };
    }

    /// <summary>Rejects a subject template with no <c>{0}</c> placeholder for the resolved
    /// user ID. Without this, <c>string.Format</c> would silently emit the template
    /// unformatted (e.g. a settings-file/env value of <c>"notify.&gt;"</c> would succeed as-is)
    /// instead of failing startup clearly — potentially creating an accidental wildcard-style
    /// subscription. Mirrors an equivalent guard in the Go implementation of this same product
    /// (golang/internal/host/host.go's validateSubjectTemplate), added after a security review.
    ///
    /// This formats a sentinel value through the template rather than doing a naive substring
    /// search for <c>"{0}"</c>: a template like <c>"notify.{{0}}.desktop"</c> contains that
    /// substring in its raw text but, per composite format string escaping rules, <c>{{</c>/
    /// <c>}}</c> mean a literal brace, so <c>string.Format</c> would actually emit the literal
    /// text <c>notify.{0}.desktop</c> verbatim with no real substitution — a subtly broken
    /// subject the substring check couldn't catch.</summary>
    internal static void ValidateSubjectTemplate(string subjectTemplate)
    {
        const string sentinel = "__SUBJECT_TEMPLATE_PLACEHOLDER_SENTINEL__";
        string formatted;
        try
        {
            formatted = string.Format(CultureInfo.InvariantCulture, subjectTemplate, sentinel);
        }
        catch (FormatException ex)
        {
            throw new InvalidOperationException(
                $"Subject template '{subjectTemplate}' is not a valid format string.", ex);
        }

        if (!formatted.Contains(sentinel, StringComparison.Ordinal))
        {
            throw new InvalidOperationException(
                $"Subject template '{subjectTemplate}' must contain a {{0}} placeholder for " +
                "the user ID.");
        }
    }

    public static async Task<AgentHost> StartAsync(
        AgentOptions options,
        IIdentityProvider identityProvider,
        IToastRenderer renderer,
        INatsAuthProvider? authProvider = null,
        IAgentMetrics? metrics = null,
        CancellationToken ct = default)
    {
        ValidateSubjectTemplate(options.SubjectTemplate);

        var identity = await identityProvider.GetIdentityAsync(ct).ConfigureAwait(false);
        var nats = new NatsConnection(BuildNatsOpts(options, authProvider));
        try
        {
            await nats.ConnectAsync().ConfigureAwait(false);

            var telemetry = new NatsTelemetryPublisher(nats, options.AckSubject);
            var dedup = new DeduplicationCache(capacity: 10_000, ttl: TimeSpan.FromMinutes(10));
            var (pipeline, aggregator) = AgentPipelineFactory.Create(
                new PipelineOptions(),
                new AggregatorOptions(),
                dedup,
                renderer,
                telemetry,
                identity.DeviceId,
                TimeProvider.System,
                metrics ?? NullAgentMetrics.Instance);
            pipeline.Start();

            var subject = string.Format(CultureInfo.InvariantCulture, options.SubjectTemplate, identity.UserId);
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
            {
                _pipeline.TryEnqueue(new ReceivedEvent(data, DateTimeOffset.UtcNow));
            }
        }
    }

    public async ValueTask DisposeAsync()
    {
        _cts.Cancel();
        if (_subscription is not null)
        {
            try
            {
                await _subscription.ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
            }
            catch
            { /* a faulted subscribe loop must not block shutdown (best-effort) */
            }
        }

        await _pipeline.DisposeAsync().ConfigureAwait(false);
        await _aggregator.DisposeAsync().ConfigureAwait(false);
        await _nats.DisposeAsync().ConfigureAwait(false);
        _cts.Dispose();
    }
}
