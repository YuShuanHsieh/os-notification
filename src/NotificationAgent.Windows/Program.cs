using NotificationAgent.Core.Hosting;
using NotificationAgent.Core.Identity;
using NotificationAgent.Windows;

// One instance per interactive session: "Local\" mutexes are session-scoped,
// so two signed-in users each get their own agent (design §2, ADR-001).
using var singleInstance = new Mutex(
    initiallyOwned: true,
    @"Local\DesktopNotificationAgent", out var isFirstInstance);
if (!isFirstInstance)
{
    return;
}

var options = AgentOptions.FromEnvironment();
var clientId = Environment.GetEnvironmentVariable("NOTIFY_AAD_CLIENT_ID")?.Trim();
var tenantId = Environment.GetEnvironmentVariable("NOTIFY_AAD_TENANT_ID")?.Trim();
IIdentityProvider identity =
    clientId is { Length: > 0 }
        ? new MsalIdentityProvider(
            clientId,
            tenantId is { Length: > 0 } ? tenantId : "organizations")
        : new EnvironmentIdentityProvider();

await using var host = await AgentHost.StartAsync(options, identity, new WindowsToastRenderer());

var shutdown = new TaskCompletionSource();
Console.CancelKeyPress += (_, e) =>
{
    e.Cancel = true;
    shutdown.TrySetResult();
};
AppDomain.CurrentDomain.ProcessExit += (_, _) => shutdown.TrySetResult();
await shutdown.Task;
