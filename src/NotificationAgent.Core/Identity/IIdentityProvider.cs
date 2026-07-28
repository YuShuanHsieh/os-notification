namespace NotificationAgent.Core.Identity;

/// <summary>Resolves the immutable application user ID and device ID (design §8).
/// The Windows account name is not used as identity for the primary identity paths: AAD/MSAL
/// sign-in (Windows production) and environment-variable identity (console/dev, and this
/// interface's own <see cref="EnvironmentIdentityProvider"/>) are both independent of the OS
/// account. The one deliberate, narrow exception is the Windows head's fallback identity when
/// AAD isn't configured (<c>NotificationAgent.Windows.WindowsUsernameIdentityProvider</c>),
/// which derives a default identity from the Windows username so operators are not forced to
/// also set <c>NOTIFY_USER_ID</c>; see context/architecture.md and
/// context/contracts-and-invariants.md.</summary>
public interface IIdentityProvider
{
    ValueTask<AgentIdentity> GetIdentityAsync(CancellationToken ct = default);
}

public sealed record AgentIdentity(string UserId, string DeviceId);

/// <summary>Development identity from environment variables. NOTIFY_USER_ID is
/// required; NOTIFY_DEVICE_ID defaults to a machine-derived value.</summary>
public sealed class EnvironmentIdentityProvider : IIdentityProvider
{
    public ValueTask<AgentIdentity> GetIdentityAsync(CancellationToken ct = default)
    {
        var userId = Environment.GetEnvironmentVariable("NOTIFY_USER_ID");
        if (string.IsNullOrWhiteSpace(userId))
        {
            throw new InvalidOperationException("NOTIFY_USER_ID is not set");
        }

        var deviceId = Environment.GetEnvironmentVariable("NOTIFY_DEVICE_ID");
        if (string.IsNullOrWhiteSpace(deviceId))
        {
            deviceId = $"d-{Environment.MachineName.ToLowerInvariant()}";
        }

        return ValueTask.FromResult(new AgentIdentity(userId, deviceId));
    }
}
