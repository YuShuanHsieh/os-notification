// src/NotificationAgent.Windows/WindowsUsernameIdentityProvider.cs
using System.Security.Cryptography;
using System.Security.Principal;
using System.Text;
using Microsoft.Extensions.Logging;
using NotificationAgent.Core.Identity;

namespace NotificationAgent.Windows;

/// <summary>Default Windows-head identity when AAD isn't configured (feature: derive
/// identity from the current Windows identity's SAM-compatible, domain-qualified name,
/// replacing the <c>NOTIFY_USER_ID</c> requirement).
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
    /// <param name="getRawUsername">Raw username source; defaults to the SAM-compatible,
    /// domain-qualified name of the current Windows identity (<c>DOMAIN\username</c>, or
    /// <c>MACHINENAME\username</c> when not domain-joined) via
    /// <see cref="WindowsIdentity.GetCurrent"/>'s <c>.Name</c>, falling back to
    /// <see cref="Environment.UserName"/> (which never carries a domain) if that throws or
    /// comes back blank. Overridable purely so this class is unit testable without
    /// depending on the real OS account.</param>
    public WindowsUsernameIdentityProvider(
        string? deviceIdOverride = null,
        ILogger? logger = null,
        Func<string>? getRawUsername = null)
    {
        _deviceIdOverride = deviceIdOverride;
        _logger = logger;
        _getRawUsername = getRawUsername ?? GetDomainQualifiedUsernameOrFallback;
    }

    /// <summary>Resolves the SAM-compatible, domain-qualified current-user name
    /// (<c>DOMAIN\username</c> / <c>MACHINENAME\username</c>) via
    /// <see cref="WindowsIdentity.GetCurrent"/>. Falls back to the bare
    /// <see cref="Environment.UserName"/> (no domain) if <see cref="WindowsIdentity.GetCurrent"/>
    /// throws or returns a null/empty name — e.g. no Windows Desktop runtime, or a token
    /// without an accessible name — so an edge case that previously never surfaced a domain
    /// doesn't turn into a hard startup failure it wasn't before.</summary>
    private static string GetDomainQualifiedUsernameOrFallback()
    {
        try
        {
            var name = WindowsIdentity.GetCurrent().Name;
            if (!string.IsNullOrWhiteSpace(name))
            {
                return name;
            }
        }
        catch
        {
            // Fall through to the Environment.UserName fallback below.
        }

        return Environment.UserName;
    }

    public ValueTask<AgentIdentity> GetIdentityAsync(CancellationToken ct = default)
    {
        _logger?.IdentityModeWindowsUsername();

        // The raw value is the SAM-compatible, domain-qualified name (DOMAIN\username or
        // MACHINENAME\username) by default — see GetDomainQualifiedUsernameOrFallback above —
        // and is deliberately kept intact rather than stripped to the bare username: two
        // different domains' identically-named accounts (e.g. CORP\jdoe and CONTOSO\jdoe)
        // must resolve to different identities, not collide. The backslash separator is just
        // untrusted OS input like every other character here, and falls out naturally in the
        // sanitize step below (mapped to '_' like any other disallowed character).
        var candidate = _getRawUsername().Trim().ToLowerInvariant();

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
        // is also why the domain qualifier is deliberately retained rather than stripped: it
        // is part of the normalized string the hash is computed over, so two same-named
        // accounts in different domains (or one domain-joined and one local) now hash
        // differently. This exact algorithm (lowercase, trim, sanitize, append 8 hex chars of
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
