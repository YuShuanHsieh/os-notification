// src/NotificationAgent.Windows/WindowsUsernameIdentityProvider.cs
using Microsoft.Extensions.Logging;
using NotificationAgent.Core.Identity;

namespace NotificationAgent.Windows;

/// <summary>Default Windows-head identity when AAD isn't configured (feature: derive
/// identity from the Windows username, replacing the <c>NOTIFY_USER_ID</c> requirement).
///
/// This is a deliberate, narrow exception to the product's original design principle that
/// the Windows account name is never used as identity (see
/// <see cref="IIdentityProvider"/>): the AAD/MSAL path (<see cref="MsalIdentityProvider"/>)
/// remains the primary, intended identity source and still takes priority whenever
/// <c>NOTIFY_AAD_CLIENT_ID</c> is configured. This provider only supplies a usable default
/// for the Windows head when that isn't set, so an operator no longer has to also set
/// <c>NOTIFY_USER_ID</c> (the console/dev host's <see cref="EnvironmentIdentityProvider"/>
/// still requires it, unchanged).</summary>
public sealed class WindowsUsernameIdentityProvider : IIdentityProvider
{
    private static readonly char[] UnsafeSubjectChars = { '.', '*', '>' };

    private readonly Func<string> _getRawUsername;
    private readonly string? _deviceIdOverride;
    private readonly ILogger? _logger;

    /// <param name="deviceIdOverride">A non-blank value (from <c>NOTIFY_DEVICE_ID</c> or the
    /// settings file's <c>deviceId</c>, feature: app settings file) wins over the persisted
    /// per-install device id file.</param>
    /// <param name="logger">Optional structured logger (feature: logging).</param>
    /// <param name="getRawUsername">Raw username source; defaults to
    /// <see cref="Environment.UserName"/>. Overridable purely so this class is unit
    /// testable without depending on the real OS account.</param>
    public WindowsUsernameIdentityProvider(
        string? deviceIdOverride = null,
        ILogger? logger = null,
        Func<string>? getRawUsername = null)
    {
        _deviceIdOverride = deviceIdOverride;
        _logger = logger;
        _getRawUsername = getRawUsername ?? (() => Environment.UserName);
    }

    public ValueTask<AgentIdentity> GetIdentityAsync(CancellationToken ct = default)
    {
        _logger?.IdentityModeWindowsUsername();

        // Environment.UserName does not carry a domain prefix on .NET (it wraps the Win32
        // GetUserName API, not GetUserNameEx), but the raw value is still untrusted OS input
        // feeding straight into subject construction below, so defensively strip one anyway.
        var raw = _getRawUsername();
        var lastSeparator = raw.LastIndexOf('\\');
        var username = (lastSeparator >= 0 ? raw[(lastSeparator + 1)..] : raw).Trim().ToLowerInvariant();

        if (username.Length == 0)
        {
            throw new InvalidOperationException("Windows username resolved to an empty value.");
        }

        // Untrusted OS input feeding directly into "notify.user.{0}.desktop": an
        // unvalidated '.', '*', or '>' could silently turn a per-user subscription into an
        // accidental wildcard subscription receiving every user's events.
        if (username.IndexOfAny(UnsafeSubjectChars) >= 0)
        {
            throw new InvalidOperationException(
                $"Windows username '{username}' contains a character reserved for NATS " +
                "subject routing ('.', '*', or '>') and cannot safely be used to build a " +
                "per-user subject.");
        }

        var userId = $"u_{username}";
        var deviceId = DeviceIdStore.GetOrCreate(_deviceIdOverride);
        _logger?.IdentityResolvedWindowsUsername(userId, deviceId);
        return ValueTask.FromResult(new AgentIdentity(userId, deviceId));
    }
}
