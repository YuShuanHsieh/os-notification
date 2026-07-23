using NATS.Client.Core;

namespace NotificationAgent.Core.Nats;

/// <summary>Authenticates using a standard NATS .creds file (user JWT + NKey seed) (design §3).</summary>
public sealed class CredsFileNatsAuthProvider : INatsAuthProvider
{
    private readonly string _credsFilePath;

    public CredsFileNatsAuthProvider(string credsFilePath) => _credsFilePath = credsFilePath;

    public NatsAuthOpts GetAuthOpts() => new() { CredsFile = _credsFilePath };
}
