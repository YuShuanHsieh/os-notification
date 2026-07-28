using Microsoft.Extensions.Logging;
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
        HttpClient httpClient,
        ILogger? logger = null)
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

            var uri = new Uri(authServiceUrl);
            if (uri.Scheme != Uri.UriSchemeHttps)
            {
                throw new InvalidOperationException(
                    "NOTIFY_NATS_AUTH_SERVICE_URL must use https (the AAD bearer token would " +
                    "otherwise be sent in cleartext).");
            }

            logger?.NatsAuthModeExternalService(authServiceUrl);
            return new ExternalAuthServiceNatsAuthProvider(
                uri,
                ct => msalIdentity.GetAccessTokenAsync(authServiceScope, ct),
                httpClient);
        }

        if (credsFile is { Length: > 0 })
        {
            logger?.NatsAuthModeCredsFile(credsFile);
            return new CredsFileNatsAuthProvider(credsFile);
        }

        logger?.NatsAuthModeNone();
        return null;
    }
}
