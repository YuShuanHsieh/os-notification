using NATS.Client.Core;

namespace NotificationAgent.Core.Nats;

/// <summary>Resolves NATS authentication options for the agent's connection (design §2).
/// Mirrors the IIdentityProvider pattern: a simple default lives in Core, enterprise-specific
/// implementations live in a host project.</summary>
public interface INatsAuthProvider
{
    NatsAuthOpts GetAuthOpts();
}
