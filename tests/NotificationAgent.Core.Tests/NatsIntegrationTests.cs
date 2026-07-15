using System.Net.Sockets;
using System.Text;
using NATS.Client.Core;
using NotificationAgent.Core.Hosting;
using NotificationAgent.Core.Identity;
using NotificationAgent.Core.Rendering;
using Xunit;
using Xunit.Abstractions;

namespace NotificationAgent.Core.Tests;

/// <summary>Requires a NATS server on localhost:4222
/// (docker run -d --name nats-test -p 4222:4222 nats:2.10-alpine).
/// Tests no-op with a message when the server is absent.</summary>
public class NatsIntegrationTests
{
    private readonly ITestOutputHelper _output;
    public NatsIntegrationTests(ITestOutputHelper output) => _output = output;

    private static bool NatsAvailable()
    {
        try
        {
            using var client = new TcpClient();
            return client.ConnectAsync("127.0.0.1", 4222).Wait(1000);
        }
        catch { return false; }
    }

    private sealed class StubIdentity : IIdentityProvider
    {
        public ValueTask<AgentIdentity> GetIdentityAsync(CancellationToken ct = default) =>
            ValueTask.FromResult(new AgentIdentity("itest-user", "d-itest"));
    }

    private sealed class RecordingRenderer : IToastRenderer
    {
        public List<ToastRequest> Shown { get; } = new();
        public ValueTask<DateTimeOffset> ShowAsync(ToastRequest toast, CancellationToken ct = default)
        {
            lock (Shown) Shown.Add(toast);
            return ValueTask.FromResult(DateTimeOffset.UtcNow);
        }
    }

    [Fact]
    public async Task Published_event_is_rendered_and_acked_end_to_end()
    {
        if (!NatsAvailable())
        {
            _output.WriteLine("SKIPPED: no NATS server on localhost:4222");
            return;
        }

        var options = new AgentOptions { NatsUrl = "nats://127.0.0.1:4222" };
        var renderer = new RecordingRenderer();
        await using var host = await AgentHost.StartAsync(options, new StubIdentity(), renderer);
        Assert.Equal("notify.user.itest-user.desktop", host.Subject);

        await using var probe = new NatsConnection(new NatsOpts { Url = options.NatsUrl });
        var acks = new List<string>();
        var ackReady = new TaskCompletionSource();
        var ackReader = Task.Run(async () =>
        {
            await foreach (var msg in probe.SubscribeAsync<byte[]>(options.AckSubject))
            {
                lock (acks) acks.Add(Encoding.UTF8.GetString(msg.Data!));
                lock (acks) if (acks.Count >= 2) { ackReady.TrySetResult(); break; }
            }
        });
        await Task.Delay(500); // let both subscriptions settle (Core NATS has no replay)

        var eventId = $"evt-{Guid.NewGuid():N}";
        var payload = $$$"""
            {"eventId":"{{{eventId}}}","target":{"userId":"itest-user"},
             "content":{"title":"Integration","message":"Hello"},
             "classification":{"priority":"critical"}}
            """;
        await probe.PublishAsync(host.Subject, Encoding.UTF8.GetBytes(payload));

        var done = await Task.WhenAny(ackReady.Task, Task.Delay(TimeSpan.FromSeconds(10)));
        Assert.True(done == ackReady.Task, "did not receive 2 acks within 10s");
        Assert.Single(renderer.Shown);
        Assert.Contains(acks, a => a.Contains("observed_by_agent") && a.Contains(eventId));
        Assert.Contains(acks, a => a.Contains("submitted_to_windows") && a.Contains(eventId));
        Assert.All(acks, a => Assert.Contains("d-itest", a));
    }
}
