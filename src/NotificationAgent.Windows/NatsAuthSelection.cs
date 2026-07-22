using NotificationAgent.Core.Nats;

namespace NotificationAgent.Windows;

/// <summary>Chooses which INatsAuthProvider to use at startup, presence-based on env vars
/// (design §4: startup wiring), mirroring how NOTIFY_AAD_CLIENT_ID selects identity.</summary>
internal static class NatsAuthSelection
{
    internal static INatsAuthProvider? Select(
        string? authServiceUrl,
        string? authServiceScope,
        string? credsFile,
        MsalIdentityProvider? msalIdentity,
        HttpClient httpClient)
    {
        if (authServiceUrl is { Length: > 0 })
        {
            if (msalIdentity is null)
            {
                throw new InvalidOperationException(
                    "NOTIFY_NATS_AUTH_SERVICE_URL requires NOTIFY_AAD_CLIENT_ID " +
                    "(external NATS auth reuses AAD identity).");
            }

            if (authServiceScope is not { Length: > 0 })
            {
                throw new InvalidOperationException(
                    "NOTIFY_NATS_AUTH_SERVICE_URL requires NOTIFY_NATS_AUTH_SERVICE_SCOPE.");
            }

            return new ExternalAuthServiceNatsAuthProvider(
                new Uri(authServiceUrl),
                ct => msalIdentity.GetAccessTokenAsync(authServiceScope, ct),
                httpClient);
        }

        return credsFile is { Length: > 0 } ? new CredsFileNatsAuthProvider(credsFile) : null;
    }
}
