using Microsoft.Extensions.Logging;
using Microsoft.Identity.Client;
using Microsoft.Identity.Client.Broker;
using NotificationAgent.Core.Identity;

namespace NotificationAgent.Windows;

/// <summary>WAM-brokered silent sign-in (design §8). The application user ID is the
/// Entra object id ("oid" / AuthenticationResult.UniqueId), never the Windows account name.</summary>
public sealed class MsalIdentityProvider : IIdentityProvider
{
    private static readonly string[] UserReadScopes = { "User.Read" };

    private readonly string _clientId;
    private readonly string _tenantId;
    private readonly string? _deviceIdOverride;
    private readonly ILogger? _logger;

    public MsalIdentityProvider(
        string clientId,
        string tenantId,
        string? deviceIdOverride = null,
        ILogger? logger = null)
    {
        _clientId = clientId;
        _tenantId = tenantId;
        _deviceIdOverride = deviceIdOverride;
        _logger = logger;
    }

    public async ValueTask<AgentIdentity> GetIdentityAsync(CancellationToken ct = default)
    {
        _logger?.IdentityModeAad(_clientId);
        var result = await AcquireTokenAsync(UserReadScopes, ct).ConfigureAwait(false);
        var deviceId = DeviceIdStore.GetOrCreate(_deviceIdOverride);
        _logger?.IdentityResolvedAad(deviceId);
        return new AgentIdentity($"u_{result.UniqueId}", deviceId);
    }

    /// <summary>Silently acquires an access token for an additional scope, reusing the same
    /// WAM-brokered account as GetIdentityAsync (design §4: external NATS auth service reuses
    /// AAD identity instead of a separate, identity-independent credential).</summary>
    public async Task<string> GetAccessTokenAsync(string scope, CancellationToken ct = default)
    {
        var result = await AcquireTokenAsync(new[] { scope }, ct).ConfigureAwait(false);
        return result.AccessToken;
    }

    private async Task<AuthenticationResult> AcquireTokenAsync(string[] scopes, CancellationToken ct)
    {
        var app = PublicClientApplicationBuilder.Create(_clientId)
            .WithAuthority($"https://login.microsoftonline.com/{_tenantId}")
            .WithBroker(new BrokerOptions(BrokerOptions.OperatingSystems.Windows))
            .WithDefaultRedirectUri()
            .Build();

        try
        {
            var accounts = await app.GetAccountsAsync().ConfigureAwait(false);
            var account = accounts.FirstOrDefault()
                ?? PublicClientApplication.OperatingSystemAccount;
            return await app.AcquireTokenSilent(scopes, account)
                .ExecuteAsync(ct).ConfigureAwait(false);
        }
        catch (MsalUiRequiredException)
        {
            // POC fallback; production would surface a sign-in prompt via the app UX.
            _logger?.MsalInteractiveFallback();
            return await app.AcquireTokenInteractive(scopes)
                .ExecuteAsync(ct).ConfigureAwait(false);
        }
    }
}

/// <summary>Stable per-install device id under %LOCALAPPDATA% (ack field deviceId).</summary>
internal static class DeviceIdStore
{
    /// <param name="overrideValue">A non-blank value (from <c>NOTIFY_DEVICE_ID</c> or the
    /// settings file's <c>deviceId</c>, feature: app settings file) wins outright and skips
    /// the file entirely, so operators can pin a reproducible device id.</param>
    public static string GetOrCreate(string? overrideValue = null)
    {
        if (overrideValue is { Length: > 0 })
        {
            return overrideValue;
        }

        var dir = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
            "DesktopNotificationAgent");
        Directory.CreateDirectory(dir);
        var path = Path.Combine(dir, "device-id");
        if (File.Exists(path))
        {
            return File.ReadAllText(path).Trim();
        }

        var id = $"d-{Guid.NewGuid():N}";
        File.WriteAllText(path, id);
        return id;
    }
}
