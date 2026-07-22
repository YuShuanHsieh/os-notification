using NotificationAgent.ConsoleHost;
using NotificationAgent.Core.Hosting;
using NotificationAgent.Core.Identity;
using NotificationAgent.Core.Nats;

var options = AgentOptions.FromEnvironment();
var credsFile = Environment.GetEnvironmentVariable("NOTIFY_NATS_CREDS_FILE")?.Trim();
INatsAuthProvider? authProvider = credsFile is { Length: > 0 }
    ? new CredsFileNatsAuthProvider(credsFile)
    : null;

await using var host = await AgentHost.StartAsync(
    options, new EnvironmentIdentityProvider(), new ConsoleToastRenderer(), authProvider);

Console.WriteLine($"Agent subscribed to {host.Subject} on {options.NatsUrl}. Ctrl+C to exit.");
var shutdown = new TaskCompletionSource();
Console.CancelKeyPress += (_, e) =>
{
    e.Cancel = true;
    shutdown.TrySetResult();
};
await shutdown.Task;
Console.WriteLine("Shutting down.");
