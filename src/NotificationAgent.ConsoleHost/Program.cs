using NotificationAgent.ConsoleHost;
using NotificationAgent.Core.Hosting;
using NotificationAgent.Core.Identity;

var options = AgentOptions.FromEnvironment();
await using var host = await AgentHost.StartAsync(
    options, new EnvironmentIdentityProvider(), new ConsoleToastRenderer());

Console.WriteLine($"Agent subscribed to {host.Subject} on {options.NatsUrl}. Ctrl+C to exit.");
var shutdown = new TaskCompletionSource();
Console.CancelKeyPress += (_, e) => { e.Cancel = true; shutdown.TrySetResult(); };
await shutdown.Task;
Console.WriteLine("Shutting down.");
