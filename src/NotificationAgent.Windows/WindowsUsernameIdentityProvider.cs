// src/NotificationAgent.Windows/WindowsUsernameIdentityProvider.cs
using System.Security.Cryptography;
using System.Text;
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
        var candidate = (lastSeparator >= 0 ? raw[(lastSeparator + 1)..] : raw).Trim().ToLowerInvariant();

        if (candidate.Length == 0)
        {
            throw new InvalidOperationException("Windows username resolved to an empty value.");
        }

        // Untrusted OS input feeding directly into "notify.user.{0}.desktop", which flows
        // straight into the NATS wire protocol's whitespace-tokenized
        // `SUB <subject> [queue-group] <sid>` line. Sanitize via an *allowlist*
        // ([a-z0-9_-], everything else mapped to '_') rather than rejecting a denylist of
        // characters: a denylist that only blocks '.'/'*'/'>' still lets an interior space
        // through (Windows account names may legitimately contain spaces, e.g. "John Doe"),
        // and the NATS server then parses everything after the space as a queue-group
        // token, silently truncating the subject and misrouting the subscription with no
        // error logged anywhere. This also stops hard-rejecting extremely common
        // `first.last`-style Windows/AD usernames (sanitized to `first_last` instead) — this
        // identity path is the only one available when no AAD client id is configured, so a
        // hard rejection would otherwise leave those accounts with no way to run the agent.
        //
        // The allowlist mapping is inherently lossy: two different usernames can sanitize to
        // the same string (e.g. "user.name" and "user_name" both become "user_name"), which
        // would otherwise let two different users collide onto one identity/NATS subject. A
        // hash of the *pre-sanitization* normalized username is appended as a suffix so
        // collisions in the human-readable prefix can never collide in the full user id. This
        // exact algorithm (strip domain, lowercase, trim, sanitize, append 8 hex chars of
        // SHA-256(normalized)) is mirrored identically in the sibling Rust and Go
        // implementations of this product so all three agree on one user's identity.
        var sanitized = new string(candidate
            .Select(c => char.IsAsciiLetterOrDigit(c) || c is '_' or '-' ? c : '_')
            .ToArray());
        var hash = SHA256.HashData(Encoding.UTF8.GetBytes(candidate));
        var hash8 = Convert.ToHexString(hash[..4]).ToLowerInvariant();

        var userId = $"u_{sanitized}_{hash8}";
        var deviceId = DeviceIdStore.GetOrCreate(_deviceIdOverride);
        _logger?.IdentityResolvedWindowsUsername(userId, deviceId);
        return ValueTask.FromResult(new AgentIdentity(userId, deviceId));
    }
}
