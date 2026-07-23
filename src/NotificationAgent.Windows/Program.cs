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
MsalIdentityProvider? msalIdentity =
    clientId is { Length: > 0 }
        ? new MsalIdentityProvider(
            clientId,
            tenantId is { Length: > 0 } ? tenantId : "organizations")
        : null;
IIdentityProvider identity = (IIdentityProvider?)msalIdentity ?? new EnvironmentIdentityProvider();

var authProvider = NatsAuthSelection.Select(
    Environment.GetEnvironmentVariable("NOTIFY_NATS_AUTH_SERVICE_URL")?.Trim(),
    Environment.GetEnvironmentVariable("NOTIFY_NATS_AUTH_SERVICE_SCOPE")?.Trim(),
    Environment.GetEnvironmentVariable("NOTIFY_NATS_CREDS_FILE")?.Trim(),
    msalIdentity,
    new HttpClient());

await using var host = await AgentHost.StartAsync(options, identity, new WindowsToastRenderer(), authProvider);

var shutdown = new TaskCompletionSource();
Console.CancelKeyPress += (_, e) =>
{
    e.Cancel = true;
    shutdown.TrySetResult();
};
AppDomain.CurrentDomain.ProcessExit += (_, _) => shutdown.TrySetResult();
await shutdown.Task;
