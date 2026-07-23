# NATS WebSocket (wss://) connection with pluggable auth

## Purpose

The agent currently connects to NATS over plain, unauthenticated TCP: `AgentHost` hardcodes `NatsConnection(new NatsOpts { Url = options.NatsUrl })` with no credentials, and `NatsUrl` defaults to `nats://127.0.0.1:4222`. This works for local development but not against a NATS deployment that requires authentication and/or is only reachable through a WebSocket-terminating load balancer or reverse proxy.

This change adds:
1. Support for `wss://` (NATS WebSocket) connection URLs.
2. A switchable `INatsAuthProvider` abstraction, mirroring the existing `IIdentityProvider` pattern, with two implementations:
   - **Creds file** (`NotificationAgent.Core`, default/simple) — a standard NATS `.creds` file (JWT + NKey seed).
   - **External auth service** (`NotificationAgent.Windows`, enterprise) — calls an external HTTPS API, reusing the same AAD identity already acquired via `MsalIdentityProvider`, to obtain a NATS JWT at connect time.

Source: [issue #7](https://github.com/YuShuanHsieh/os-notification/issues/7).

## Scope

- **In scope:** `wss://` transport support, `INatsAuthProvider` abstraction, creds-file provider (Core), external-auth-service provider (Windows), startup wiring/env vars, error handling, tests.
- **Out of scope:** the external auth service's actual backend implementation (this repo only calls it); JetStream/replay/persistent delivery; any change to the online-only, at-most-once, best-effort delivery model; persisting the NKey seed across process restarts (noted as a Phase-2 follow-up, see below).

## 1. Transport: `wss://`

No code change. `NatsOpts.Url` (from `NATS.Client.Core` 2.8.2, referenced via the `NATS.Net` package) already scheme-detects the transport — `ws://`/`wss://` route through its WebSocket connection path automatically, the same way `nats://`/`tls://` route through plain/TLS TCP. `AgentOptions.NatsUrl` / `NOTIFY_NATS_URL` is a free-form URL string today and needs no change to accept `wss://host/path`.

Only documentation changes: note in the README and `context/configuration-and-runtime.md` that `NOTIFY_NATS_URL` may point at a `wss://` endpoint (e.g. behind a load balancer that doesn't pass through raw TCP).

## 2. `INatsAuthProvider` abstraction

New file `src/NotificationAgent.Core/Nats/INatsAuthProvider.cs`:

```csharp
namespace NotificationAgent.Core.Nats;

/// <summary>Resolves NATS authentication options for the agent's connection (design: NATS WebSocket + pluggable auth).
/// Mirrors the IIdentityProvider pattern: a simple default lives in Core, enterprise-specific
/// implementations live in a host project.</summary>
public interface INatsAuthProvider
{
    NatsAuthOpts GetAuthOpts();
}
```

`GetAuthOpts()` is synchronous and called once, at connect time. It's synchronous because the interesting async work (fetching a token on each *reconnect*) doesn't need to happen here — see §4.

### `AgentHost` wiring

`AgentHost.StartAsync` gains an optional parameter, defaulting to `null` (today's unauthenticated behavior — no `NoAuthNatsAuthProvider` implementation needed):

```csharp
public static async Task<AgentHost> StartAsync(
    AgentOptions options,
    IIdentityProvider identityProvider,
    IToastRenderer renderer,
    INatsAuthProvider? authProvider = null,
    CancellationToken ct = default)
{
    var identity = await identityProvider.GetIdentityAsync(ct).ConfigureAwait(false);
    var natsOpts = new NatsOpts { Url = options.NatsUrl };
    if (authProvider is not null)
    {
        natsOpts = natsOpts with { AuthOpts = authProvider.GetAuthOpts() };
    }

    var nats = new NatsConnection(natsOpts);
    // ... unchanged from here
}
```

The resulting `NatsAuthOpts` (including any `AuthCredCallback` delegate, see §4) lives on the `NatsConnection` for its lifetime. `NatsConnection`'s own reconnect logic (already relied on today — "reconnects resume with future events only") reuses it on every reconnect attempt automatically. No changes to `SubscribeLoopAsync` or `DisposeAsync`.

## 3. `CredsFileNatsAuthProvider` (Core, default/simple method)

New file `src/NotificationAgent.Core/Nats/CredsFileNatsAuthProvider.cs`:

```csharp
namespace NotificationAgent.Core.Nats;

/// <summary>Authenticates using a standard NATS .creds file (user JWT + NKey seed).</summary>
public sealed class CredsFileNatsAuthProvider : INatsAuthProvider
{
    private readonly string _credsFilePath;

    public CredsFileNatsAuthProvider(string credsFilePath) => _credsFilePath = credsFilePath;

    public NatsAuthOpts GetAuthOpts() => new() { CredsFile = _credsFilePath };
}
```

`NatsAuthOpts.CredsFile` is a built-in property of `NATS.Client.Core` — it handles loading and parsing the `.creds` file itself, no custom parsing needed.

This provider has no Windows/OS dependency, so it's usable from **both** hosts. Wired via a new env var, presence-based (mirroring `NOTIFY_AAD_CLIENT_ID`'s selection pattern):

- `NOTIFY_NATS_CREDS_FILE` — path to a `.creds` file. If set, both `ConsoleHost/Program.cs` and `Windows/Program.cs` construct `CredsFileNatsAuthProvider` and pass it to `AgentHost.StartAsync`.

## 4. `ExternalAuthServiceNatsAuthProvider` (Windows, enterprise method)

New file `src/NotificationAgent.Windows/ExternalAuthServiceNatsAuthProvider.cs`, following the `MsalIdentityProvider` pattern of living in the Windows host because it depends on the same MSAL app/account used for identity.

### Why an NKey is generated locally

Standard NATS decentralized auth needs two things at connect time: a JWT, and the NKey seed (private key) used to sign the server's nonce challenge. The seed must never leave the device. So the flow is:

1. Generate an NKey user keypair locally (via `NATS.NKeys`, already a transitive dependency of `NATS.Net`).
2. Send only the **public** key (plus an AAD access token) to the external auth service.
3. The service mints a JWT bound to that public key and returns it.
4. Locally, pair the server-issued JWT with the locally-held seed: `NatsAuthCred.FromJwt(jwt, seed)`. The NATS client uses the seed to sign the connect-time nonce; the seed itself is never transmitted.

**Persistence:** for this POC, the NKey pair is generated fresh **per process start** and held in memory only — not persisted to disk. This keeps the change small while the backend auth-service contract is still undefined (explicitly out of scope for issue #7). Persisting it under `%LOCALAPPDATA%\DesktopNotificationAgent` (mirroring `DeviceIdStore`'s pattern) so the agent presents a stable NATS identity across restarts is a natural Phase-2 follow-up once the backend exists and can confirm whether a stable per-device NATS identity is wanted.

### Reusing AAD identity

`MsalIdentityProvider` already silently acquires a token for the `User.Read` scope to resolve the Entra object ID. Calling the external auth service is a different concern and likely needs a distinct app-registered API scope, so this design adds a **second silent MSAL acquisition** for a caller-supplied scope, rather than reusing the `User.Read` token. Concretely, `ExternalAuthServiceNatsAuthProvider` takes a `Func<CancellationToken, Task<string>> accessTokenProvider` delegate (constructed in `Windows/Program.cs` by wrapping a second `AcquireTokenSilent` call against the same `PublicClientApplication`/account MSAL already resolved), keeping this provider decoupled from MSAL's concrete types.

### Shape

```csharp
namespace NotificationAgent.Windows;

/// <summary>Authenticates NATS via an external HTTPS auth service, reusing the agent's AAD identity.
/// The NATS JWT is refreshed on every connect/reconnect via AuthCredCallback (design §4).</summary>
public sealed class ExternalAuthServiceNatsAuthProvider : INatsAuthProvider
{
    private readonly Uri _authServiceUrl;
    private readonly Func<CancellationToken, Task<string>> _accessTokenProvider;
    private readonly HttpClient _httpClient;
    private readonly string _seed;      // NKey private seed, generated once, kept in memory only
    private readonly string _publicKey; // corresponding NKey public key

    // constructor generates the NKey pair via NATS.NKeys

    public NatsAuthOpts GetAuthOpts() => new()
    {
        AuthCredCallback = async (_, ct) =>
        {
            var aadToken = await _accessTokenProvider(ct).ConfigureAwait(false);
            var jwt = await FetchJwtAsync(aadToken, ct).ConfigureAwait(false);
            return NatsAuthCred.FromJwt(jwt, _seed);
        },
    };

    private async Task<string> FetchJwtAsync(string aadToken, CancellationToken ct)
    {
        // POST _authServiceUrl, Authorization: Bearer {aadToken}, body { "nkeyPublicKey": _publicKey }
        // response { "jwt": "..." } -> return jwt
    }
}
```

`NatsOpts.AuthOpts.AuthCredCallback` is invoked by `NATS.Client.Core` itself on every connect **and** every reconnect attempt — this is what gives us token refresh without any manual timer or reconnect-and-swap logic, directly answering the open question from issue #7 about refresh/expiry behavior.

### Assumed HTTP contract (flag for confirmation once the backend exists)

- `POST {NOTIFY_NATS_AUTH_SERVICE_URL}`
- Header: `Authorization: Bearer {aadAccessToken}`
- Body: `{"nkeyPublicKey": "..."}`
- Response (200): `{"jwt": "..."}`
- Non-2xx or malformed response → throw, propagated out of the callback (see §5).

This is a reasonable default given the backend doesn't exist yet; adjust once the real API is specified.

### Startup wiring (`Windows/Program.cs`)

New env vars, read alongside the existing `NOTIFY_AAD_CLIENT_ID`/`NOTIFY_AAD_TENANT_ID` handling:

- `NOTIFY_NATS_AUTH_SERVICE_URL` — HTTPS endpoint of the external auth service. Presence selects this method.
- `NOTIFY_NATS_AUTH_SERVICE_SCOPE` — AAD scope requested for the token used to call the auth service. Required when `NOTIFY_NATS_AUTH_SERVICE_URL` is set.

If `NOTIFY_NATS_AUTH_SERVICE_URL` is set but `NOTIFY_AAD_CLIENT_ID` is not (no AAD identity available to reuse) or `NOTIFY_NATS_AUTH_SERVICE_SCOPE` is missing, fail fast at startup with a descriptive `InvalidOperationException`, same style as `EnvironmentIdentityProvider`'s missing-`NOTIFY_USER_ID` check.

Selection precedence in `Windows/Program.cs`: `NOTIFY_NATS_AUTH_SERVICE_URL` set → `ExternalAuthServiceNatsAuthProvider`; else `NOTIFY_NATS_CREDS_FILE` set → `CredsFileNatsAuthProvider`; else → no auth provider (`null`, today's behavior).

## 5. Error handling

- **Creds file missing/unreadable:** surfaced by the NATS client as a connect failure. `AgentHost.StartAsync`'s existing catch-dispose-rethrow path around `ConnectAsync` already handles this; no new handling needed.
- **AAD token acquisition fails inside `AuthCredCallback`:** let the exception propagate. The NATS client treats a failed callback as a failed connect attempt and retries per its own reconnect policy — consistent with the online-only, best-effort delivery model; no custom retry/backoff added on top.
- **Auth service HTTP call fails** (non-2xx, timeout, malformed JSON): wrap in a descriptive exception thrown from the callback; same propagation-to-reconnect behavior as above.
- **Misconfiguration at startup** (`NOTIFY_NATS_AUTH_SERVICE_URL` set without `NOTIFY_AAD_CLIENT_ID` or `NOTIFY_NATS_AUTH_SERVICE_SCOPE`): fail fast with `InvalidOperationException` before attempting to connect.

## 6. Testing plan

- **`CredsFileNatsAuthProvider`:** pure unit test — construct with a path, assert `GetAuthOpts().CredsFile` equals it. No NATS server needed.
- **`ExternalAuthServiceNatsAuthProvider`:** unit test using a stub `HttpMessageHandler` and a stub `accessTokenProvider` delegate. Assert the callback POSTs the expected header/body, maps a successful response into a `NatsAuthCred` built from the returned JWT and the locally-held seed, and that HTTP failures/non-2xx responses propagate as exceptions out of the callback. No real MSAL or network calls.
- **`AgentHost` wiring:** a focused test asserting that when a non-null `INatsAuthProvider` stub is passed to `StartAsync`, the constructed connection's `NatsOpts.AuthOpts` reflects the stub's `GetAuthOpts()` result. No live server needed for this assertion.
- **`NatsIntegrationTests.cs`:** unchanged in shape — still skips without a local server on `127.0.0.1:4222`. Not extended to cover `wss://` or auth, since that needs a specially configured NATS server; out of scope for this repo's existing "plain local NATS" integration test.
- **Docs:** update the README configuration table and `context/configuration-and-runtime.md` with the new env vars (`NOTIFY_NATS_CREDS_FILE`, `NOTIFY_NATS_AUTH_SERVICE_URL`, `NOTIFY_NATS_AUTH_SERVICE_SCOPE`) and the `wss://` transport note.

## Non-goals / explicitly deferred

- Persisting the NKey seed across process restarts (Phase-2 follow-up once the backend auth-service contract is confirmed).
- Any change to the external auth service's own implementation — this repo only calls it.
- JetStream, replay, or any other change to the online-only, at-most-once, best-effort delivery model.
- Explicit transport validation/logging beyond what the NATS client already does via URL scheme detection.
