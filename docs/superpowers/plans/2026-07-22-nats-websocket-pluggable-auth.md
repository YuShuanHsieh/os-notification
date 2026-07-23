# NATS WebSocket (wss://) with Pluggable Auth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the agent connect to NATS over `wss://` and authenticate via either a `.creds` file (Core, default) or an external HTTPS auth service that reuses the agent's AAD identity (Windows, enterprise), per [issue #7](https://github.com/YuShuanHsieh/os-notification/issues/7).

**Architecture:** A new `INatsAuthProvider` interface (mirroring `IIdentityProvider`) returns a `NATS.Client.Core.NatsAuthOpts`. `AgentHost.StartAsync` takes an optional `INatsAuthProvider?` and applies it to `NatsOpts.AuthOpts` before connecting. `wss://` needs no code — the NATS client scheme-detects it from `NOTIFY_NATS_URL`. Two providers: `CredsFileNatsAuthProvider` (Core) sets `NatsAuthOpts.CredsFile`; `ExternalAuthServiceNatsAuthProvider` (Windows) sets `NatsAuthOpts.AuthCredCallback`, a delegate the NATS client re-invokes on every connect/reconnect, giving free token refresh.

**Tech Stack:** .NET 10, `NATS.Net` 2.* (`NATS.Client.Core` 2.8.2, `NATS.NKeys` 1.0.1 — both transitive, no new package references needed), `Microsoft.Identity.Client` (MSAL/WAM, already a Windows dependency), xUnit.

**Design doc:** `docs/superpowers/specs/2026-07-22-nats-websocket-pluggable-auth-design.md` (referenced below as "the design doc"; §N below refers to its numbered sections).

## Global Constraints

- Keep `NotificationAgent.Core` free of Windows-specific dependencies (AGENTS.md). `INatsAuthProvider` and `CredsFileNatsAuthProvider` go in Core; `ExternalAuthServiceNatsAuthProvider` (which needs MSAL) goes in `NotificationAgent.Windows`.
- Preserve the online-only, at-most-once, best-effort delivery model — this change only affects connection/auth, not delivery semantics.
- `NATS.Client.Core.NatsOpts` and `NatsAuthOpts` are sealed record classes (support `with` expressions); `NatsAuthOpts.CredsFile` (`string`) and `NatsAuthOpts.AuthCredCallback` (`Func<Uri, CancellationToken, ValueTask<NatsAuthCred>>`) are the two hooks used. `NatsAuthCred` is a record struct — `NatsAuthCred.FromJwt(jwt, seed)` is a public static factory; its properties are internal, but structural `Equals` still works for tests.
- `NATS.NKeys.KeyPair.CreatePair(PrefixByte.User)` generates a keypair; `.GetSeed()` / `.GetPublicKey()` read it out as strings. `NATS.NKeys` is a transitive dependency of `NATS.Client.Core` (not excluded from compile/runtime in its nuspec), reachable from both `NotificationAgent.Core` and `NotificationAgent.Windows` without any new `PackageReference`.
- `System.Net.Http.Json` (`JsonContent.Create`, `ReadFromJsonAsync`) ships in the `net10.0` shared framework — no new `PackageReference` needed for it either.
- New env vars, presence-based selection (no explicit mode switch), consistent with `NOTIFY_*` conventions: `NOTIFY_NATS_CREDS_FILE` (both hosts), `NOTIFY_NATS_AUTH_SERVICE_URL` and `NOTIFY_NATS_AUTH_SERVICE_SCOPE` (Windows only, requires `NOTIFY_AAD_CLIENT_ID` too).
- Analyzer warnings fail the build (`Directory.Build.props`: `TreatWarningsAsErrors=true` + StyleCop). Existing public members without XML doc comments (e.g. `AgentOptions` properties) build cleanly, so new members only need doc comments where existing sibling types already carry them (interfaces/classes, not every property) — follow that precedent, don't over-document.
- Run `dotnet format <project> --verify-no-changes --no-restore` before each commit per AGENTS.md.

---

### Task 1: `INatsAuthProvider` + `CredsFileNatsAuthProvider` (Core)

**Files:**
- Create: `src/NotificationAgent.Core/Nats/INatsAuthProvider.cs`
- Create: `src/NotificationAgent.Core/Nats/CredsFileNatsAuthProvider.cs`
- Test: `tests/NotificationAgent.Core.Tests/CredsFileNatsAuthProviderTests.cs`

**Interfaces:**
- Produces: `NotificationAgent.Core.Nats.INatsAuthProvider` with `NatsAuthOpts GetAuthOpts();`. `NotificationAgent.Core.Nats.CredsFileNatsAuthProvider(string credsFilePath)` implementing it.

- [ ] **Step 1: Write the failing test**

```csharp
// tests/NotificationAgent.Core.Tests/CredsFileNatsAuthProviderTests.cs
using NotificationAgent.Core.Nats;
using Xunit;

namespace NotificationAgent.Core.Tests;

public class CredsFileNatsAuthProviderTests
{
    [Fact]
    public void GetAuthOpts_sets_creds_file_path()
    {
        var provider = new CredsFileNatsAuthProvider("/etc/nats/user.creds");

        var opts = provider.GetAuthOpts();

        Assert.Equal("/etc/nats/user.creds", opts.CredsFile);
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `dotnet test tests/NotificationAgent.Core.Tests/NotificationAgent.Core.Tests.csproj --filter "FullyQualifiedName~CredsFileNatsAuthProviderTests"`
Expected: FAIL (build error — `NotificationAgent.Core.Nats.CredsFileNatsAuthProvider` does not exist)

- [ ] **Step 3: Write minimal implementation**

```csharp
// src/NotificationAgent.Core/Nats/INatsAuthProvider.cs
using NATS.Client.Core;

namespace NotificationAgent.Core.Nats;

/// <summary>Resolves NATS authentication options for the agent's connection (design §2).
/// Mirrors the IIdentityProvider pattern: a simple default lives in Core, enterprise-specific
/// implementations live in a host project.</summary>
public interface INatsAuthProvider
{
    NatsAuthOpts GetAuthOpts();
}
```

```csharp
// src/NotificationAgent.Core/Nats/CredsFileNatsAuthProvider.cs
using NATS.Client.Core;

namespace NotificationAgent.Core.Nats;

/// <summary>Authenticates using a standard NATS .creds file (user JWT + NKey seed) (design §3).</summary>
public sealed class CredsFileNatsAuthProvider : INatsAuthProvider
{
    private readonly string _credsFilePath;

    public CredsFileNatsAuthProvider(string credsFilePath) => _credsFilePath = credsFilePath;

    public NatsAuthOpts GetAuthOpts() => new() { CredsFile = _credsFilePath };
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `dotnet test tests/NotificationAgent.Core.Tests/NotificationAgent.Core.Tests.csproj --filter "FullyQualifiedName~CredsFileNatsAuthProviderTests"`
Expected: PASS (1 test)

- [ ] **Step 5: Build and format check**

Run: `dotnet build NotificationAgent.sln && dotnet format NotificationAgent.sln --verify-no-changes --no-restore`
Expected: both succeed with no diagnostics

- [ ] **Step 6: Commit**

```bash
git add src/NotificationAgent.Core/Nats/INatsAuthProvider.cs \
        src/NotificationAgent.Core/Nats/CredsFileNatsAuthProvider.cs \
        tests/NotificationAgent.Core.Tests/CredsFileNatsAuthProviderTests.cs
git commit -m "feat(core): add INatsAuthProvider and CredsFileNatsAuthProvider"
```

---

### Task 2: Wire `INatsAuthProvider` into `AgentHost`

**Files:**
- Modify: `src/NotificationAgent.Core/Hosting/AgentHost.cs:67-101` (`StartAsync`)
- Modify: `src/NotificationAgent.Core/NotificationAgent.Core.csproj` (add `InternalsVisibleTo`)
- Test: `tests/NotificationAgent.Core.Tests/AgentHostNatsOptsTests.cs`

**Interfaces:**
- Consumes: `INatsAuthProvider` from Task 1 (`NotificationAgent.Core.Nats`).
- Produces: `AgentHost.StartAsync(AgentOptions options, IIdentityProvider identityProvider, IToastRenderer renderer, INatsAuthProvider? authProvider = null, CancellationToken ct = default)` — existing 3-arg call sites keep compiling since the new parameter is optional. `internal static NatsOpts AgentHost.BuildNatsOpts(AgentOptions options, INatsAuthProvider? authProvider)` for tests.

- [ ] **Step 1: Add InternalsVisibleTo so the test project can see the internal helper**

Edit `src/NotificationAgent.Core/NotificationAgent.Core.csproj`, add before the closing `</Project>`:

```xml
  <ItemGroup>
    <InternalsVisibleTo Include="NotificationAgent.Core.Tests" />
  </ItemGroup>
```

- [ ] **Step 2: Write the failing test**

```csharp
// tests/NotificationAgent.Core.Tests/AgentHostNatsOptsTests.cs
using NATS.Client.Core;
using NotificationAgent.Core.Hosting;
using NotificationAgent.Core.Nats;
using Xunit;

namespace NotificationAgent.Core.Tests;

public class AgentHostNatsOptsTests
{
    private sealed class StubAuthProvider : INatsAuthProvider
    {
        private readonly NatsAuthOpts _opts;
        public StubAuthProvider(NatsAuthOpts opts) => _opts = opts;
        public NatsAuthOpts GetAuthOpts() => _opts;
    }

    [Fact]
    public void BuildNatsOpts_without_provider_leaves_auth_unset()
    {
        var options = new AgentOptions { NatsUrl = "nats://127.0.0.1:4222" };

        var opts = AgentHost.BuildNatsOpts(options, authProvider: null);

        Assert.Equal("nats://127.0.0.1:4222", opts.Url);
        Assert.Null(opts.AuthOpts.CredsFile);
    }

    [Fact]
    public void BuildNatsOpts_with_provider_applies_its_auth_opts()
    {
        var options = new AgentOptions { NatsUrl = "wss://nats.example.com/ws" };
        var provider = new StubAuthProvider(new NatsAuthOpts { CredsFile = "/x.creds" });

        var opts = AgentHost.BuildNatsOpts(options, provider);

        Assert.Equal("wss://nats.example.com/ws", opts.Url);
        Assert.Equal("/x.creds", opts.AuthOpts.CredsFile);
    }
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `dotnet test tests/NotificationAgent.Core.Tests/NotificationAgent.Core.Tests.csproj --filter "FullyQualifiedName~AgentHostNatsOptsTests"`
Expected: FAIL (build error — `AgentHost.BuildNatsOpts` does not exist)

- [ ] **Step 4: Implement `BuildNatsOpts` and wire it into `StartAsync`**

In `src/NotificationAgent.Core/Hosting/AgentHost.cs`, add `using NotificationAgent.Core.Nats;` to the usings at the top, then replace the `StartAsync` method body's connection setup (currently `var nats = new NatsConnection(new NatsOpts { Url = options.NatsUrl });`) and signature:

```csharp
    public static async Task<AgentHost> StartAsync(
        AgentOptions options,
        IIdentityProvider identityProvider,
        IToastRenderer renderer,
        INatsAuthProvider? authProvider = null,
        CancellationToken ct = default)
    {
        var identity = await identityProvider.GetIdentityAsync(ct).ConfigureAwait(false);
        var nats = new NatsConnection(BuildNatsOpts(options, authProvider));
        try
        {
            await nats.ConnectAsync().ConfigureAwait(false);
```

(the rest of the `try` block is unchanged). Add the helper as a private static method on `AgentHost`, just above `StartAsync`:

```csharp
    /// <summary>Builds connection options, applying auth if a provider is configured (design §2).</summary>
    internal static NatsOpts BuildNatsOpts(AgentOptions options, INatsAuthProvider? authProvider)
    {
        var opts = new NatsOpts { Url = options.NatsUrl };
        return authProvider is null ? opts : opts with { AuthOpts = authProvider.GetAuthOpts() };
    }
```

- [ ] **Step 5: Run test to verify it passes**

Run: `dotnet test tests/NotificationAgent.Core.Tests/NotificationAgent.Core.Tests.csproj --filter "FullyQualifiedName~AgentHostNatsOptsTests"`
Expected: PASS (2 tests)

- [ ] **Step 6: Run the full Core test suite (confirms the optional parameter didn't break existing callers)**

Run: `dotnet test NotificationAgent.sln`
Expected: all tests pass (the `NatsIntegrationTests` test skips with a message if no NATS server is on `127.0.0.1:4222` — report that distinction if it skips)

- [ ] **Step 7: Build and format check**

Run: `dotnet build NotificationAgent.sln && dotnet format NotificationAgent.sln --verify-no-changes --no-restore`
Expected: both succeed with no diagnostics

- [ ] **Step 8: Commit**

```bash
git add src/NotificationAgent.Core/Hosting/AgentHost.cs \
        src/NotificationAgent.Core/NotificationAgent.Core.csproj \
        tests/NotificationAgent.Core.Tests/AgentHostNatsOptsTests.cs
git commit -m "feat(core): apply INatsAuthProvider to the NATS connection in AgentHost"
```

---

### Task 3: Wire `NOTIFY_NATS_CREDS_FILE` into `NotificationAgent.ConsoleHost`

**Files:**
- Modify: `src/NotificationAgent.ConsoleHost/Program.cs`

**Interfaces:**
- Consumes: `CredsFileNatsAuthProvider` (Task 1), `AgentHost.StartAsync`'s new `authProvider` parameter (Task 2).

- [ ] **Step 1: Update Program.cs**

Replace the full file:

```csharp
using NotificationAgent.ConsoleHost;
using NotificationAgent.Core.Hosting;
using NotificationAgent.Core.Identity;
using NotificationAgent.Core.Nats;

var options = AgentOptions.FromEnvironment();
var credsFile = Environment.GetEnvironmentVariable("NOTIFY_NATS_CREDS_FILE")?.Trim();
INatsAuthProvider? authProvider = credsFile is { Length: > 0 }
    ? new CredsFileNatsAuthProvider(credsFile)
    : null;

await using var host = await AgentHost.StartAsync(
    options, new EnvironmentIdentityProvider(), new ConsoleToastRenderer(), authProvider);

Console.WriteLine($"Agent subscribed to {host.Subject} on {options.NatsUrl}. Ctrl+C to exit.");
var shutdown = new TaskCompletionSource();
Console.CancelKeyPress += (_, e) =>
{
    e.Cancel = true;
    shutdown.TrySetResult();
};
await shutdown.Task;
Console.WriteLine("Shutting down.");
```

- [ ] **Step 2: Build to confirm it compiles**

Run: `dotnet build NotificationAgent.sln`
Expected: succeeds with no diagnostics

- [ ] **Step 3: Manual smoke check (no env var set — confirm unauthenticated behavior is unchanged)**

With a local NATS server running (`docker run -d --name nats-test -p 4222:4222 nats:2.10-alpine` if not already running):

```bash
export NOTIFY_USER_ID=u_demo
dotnet run --project src/NotificationAgent.ConsoleHost
```

Expected: prints `Agent subscribed to notify.user.u_demo.desktop on nats://127.0.0.1:4222. Ctrl+C to exit.` exactly as before this change (Ctrl+C to stop). This confirms `NOTIFY_NATS_CREDS_FILE` being unset preserves today's unauthenticated behavior.

- [ ] **Step 4: Format check**

Run: `dotnet format NotificationAgent.sln --verify-no-changes --no-restore`
Expected: succeeds with no diagnostics

- [ ] **Step 5: Commit**

```bash
git add src/NotificationAgent.ConsoleHost/Program.cs
git commit -m "feat(console-host): support NOTIFY_NATS_CREDS_FILE"
```

---

### Task 4: `ExternalAuthServiceNatsAuthProvider` (Windows)

**Files:**
- Create: `src/NotificationAgent.Windows/ExternalAuthServiceNatsAuthProvider.cs`
- Test: `tests/NotificationAgent.Windows.Tests/ExternalAuthServiceNatsAuthProviderTests.cs`

**Interfaces:**
- Consumes: `INatsAuthProvider` (Task 1).
- Produces: `NotificationAgent.Windows.ExternalAuthServiceNatsAuthProvider(Uri authServiceUrl, Func<CancellationToken, Task<string>> accessTokenProvider, HttpClient httpClient)`, implementing `INatsAuthProvider`. Public `string PublicKey { get; }` (the NKey public key, safe to expose). Internal `string Seed { get; }` (test-only visibility, never sent over the network).

- [ ] **Step 1: Write the failing tests**

```csharp
// tests/NotificationAgent.Windows.Tests/ExternalAuthServiceNatsAuthProviderTests.cs
using System.Net;
using System.Net.Http.Json;
using NATS.Client.Core;
using NotificationAgent.Windows;
using Xunit;

namespace NotificationAgent.Windows.Tests;

public class ExternalAuthServiceNatsAuthProviderTests
{
    private sealed class StubHandler : HttpMessageHandler
    {
        private readonly Func<HttpRequestMessage, HttpResponseMessage> _respond;

        public StubHandler(Func<HttpRequestMessage, HttpResponseMessage> respond) => _respond = respond;

        public HttpRequestMessage? LastRequest { get; private set; }

        public string? LastBody { get; private set; }

        protected override async Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request, CancellationToken cancellationToken)
        {
            LastRequest = request;
            LastBody = request.Content is null
                ? null
                : await request.Content.ReadAsStringAsync(cancellationToken);
            return _respond(request);
        }
    }

    private static HttpResponseMessage JsonResponse(HttpStatusCode status, object body) =>
        new(status) { Content = JsonContent.Create(body) };

    private static ExternalAuthServiceNatsAuthProvider CreateProvider(
        StubHandler handler, Func<CancellationToken, Task<string>>? accessTokenProvider = null) =>
        new(
            new Uri("https://auth.example.com/nats-jwt"),
            accessTokenProvider ?? (_ => Task.FromResult("aad-token")),
            new HttpClient(handler));

    [Fact]
    public async Task Callback_posts_public_key_and_bearer_token_then_returns_cred()
    {
        var handler = new StubHandler(_ => JsonResponse(HttpStatusCode.OK, new { jwt = "jwt-1" }));
        var provider = CreateProvider(handler, _ => Task.FromResult("aad-token-1"));

        var cred = await provider.GetAuthOpts().AuthCredCallback!(
            new Uri("nats://server"), CancellationToken.None);

        Assert.Equal(HttpMethod.Post, handler.LastRequest!.Method);
        Assert.Equal("Bearer", handler.LastRequest.Headers.Authorization!.Scheme);
        Assert.Equal("aad-token-1", handler.LastRequest.Headers.Authorization.Parameter);
        Assert.Contains(provider.PublicKey, handler.LastBody);
        Assert.Equal(NatsAuthCred.FromJwt("jwt-1", provider.Seed), cred);
    }

    [Fact]
    public async Task Callback_reuses_the_same_nkey_seed_across_calls()
    {
        var handler = new StubHandler(_ => JsonResponse(HttpStatusCode.OK, new { jwt = "same-jwt" }));
        var provider = CreateProvider(handler);
        var callback = provider.GetAuthOpts().AuthCredCallback!;

        var first = await callback(new Uri("nats://server"), CancellationToken.None);
        var second = await callback(new Uri("nats://server"), CancellationToken.None);

        Assert.Equal(first, second);
    }

    [Fact]
    public async Task Callback_throws_on_non_success_status()
    {
        var handler = new StubHandler(_ => new HttpResponseMessage(HttpStatusCode.Unauthorized));
        var provider = CreateProvider(handler);

        await Assert.ThrowsAsync<InvalidOperationException>(() =>
            provider.GetAuthOpts().AuthCredCallback!(new Uri("nats://server"), CancellationToken.None).AsTask());
    }

    [Fact]
    public async Task Callback_throws_when_response_has_no_jwt()
    {
        var handler = new StubHandler(_ => JsonResponse(HttpStatusCode.OK, new { }));
        var provider = CreateProvider(handler);

        await Assert.ThrowsAsync<InvalidOperationException>(() =>
            provider.GetAuthOpts().AuthCredCallback!(new Uri("nats://server"), CancellationToken.None).AsTask());
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `dotnet test tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj --filter "FullyQualifiedName~ExternalAuthServiceNatsAuthProviderTests"`
Expected: FAIL (build error — `ExternalAuthServiceNatsAuthProvider` does not exist)

- [ ] **Step 3: Write the implementation**

```csharp
// src/NotificationAgent.Windows/ExternalAuthServiceNatsAuthProvider.cs
using System.Net.Http.Headers;
using System.Net.Http.Json;
using NATS.Client.Core;
using NATS.NKeys;
using NotificationAgent.Core.Nats;

namespace NotificationAgent.Windows;

/// <summary>Authenticates NATS via an external HTTPS auth service, reusing the agent's AAD
/// identity (design §4). The NATS JWT is refreshed on every connect/reconnect via
/// AuthCredCallback, which NATS.Client.Core re-invokes automatically.
/// The NKey seed is generated once per process and never leaves the device;
/// only its public key is sent to the auth service.</summary>
public sealed class ExternalAuthServiceNatsAuthProvider : INatsAuthProvider
{
    private readonly Uri _authServiceUrl;
    private readonly Func<CancellationToken, Task<string>> _accessTokenProvider;
    private readonly HttpClient _httpClient;

    public ExternalAuthServiceNatsAuthProvider(
        Uri authServiceUrl,
        Func<CancellationToken, Task<string>> accessTokenProvider,
        HttpClient httpClient)
    {
        _authServiceUrl = authServiceUrl;
        _accessTokenProvider = accessTokenProvider;
        _httpClient = httpClient;

        using var keyPair = KeyPair.CreatePair(PrefixByte.User);
        Seed = keyPair.GetSeed();
        PublicKey = keyPair.GetPublicKey();
    }

    public string PublicKey { get; }

    internal string Seed { get; }

    public NatsAuthOpts GetAuthOpts() => new()
    {
        AuthCredCallback = async (_, ct) =>
        {
            var aadToken = await _accessTokenProvider(ct).ConfigureAwait(false);
            var jwt = await FetchJwtAsync(aadToken, ct).ConfigureAwait(false);
            return NatsAuthCred.FromJwt(jwt, Seed);
        },
    };

    private async Task<string> FetchJwtAsync(string aadToken, CancellationToken ct)
    {
        using var request = new HttpRequestMessage(HttpMethod.Post, _authServiceUrl)
        {
            Content = JsonContent.Create(new { nkeyPublicKey = PublicKey }),
        };
        request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", aadToken);

        using var response = await _httpClient.SendAsync(request, ct).ConfigureAwait(false);
        if (!response.IsSuccessStatusCode)
        {
            throw new InvalidOperationException(
                $"NATS auth service returned {(int)response.StatusCode} {response.ReasonPhrase}.");
        }

        var body = await response.Content.ReadFromJsonAsync<AuthServiceResponse>(ct).ConfigureAwait(false);
        if (body?.Jwt is not { Length: > 0 })
        {
            throw new InvalidOperationException("NATS auth service response did not contain a jwt.");
        }

        return body.Jwt;
    }

    private sealed class AuthServiceResponse
    {
        public string? Jwt { get; set; }
    }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `dotnet test tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj --filter "FullyQualifiedName~ExternalAuthServiceNatsAuthProviderTests"`
Expected: PASS (4 tests)

- [ ] **Step 5: Build and format check**

Run: `dotnet build src/NotificationAgent.Windows/NotificationAgent.Windows.csproj && dotnet test tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj && dotnet format tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj --verify-no-changes --no-restore`
Expected: all succeed with no diagnostics

- [ ] **Step 6: Commit**

```bash
git add src/NotificationAgent.Windows/ExternalAuthServiceNatsAuthProvider.cs \
        tests/NotificationAgent.Windows.Tests/ExternalAuthServiceNatsAuthProviderTests.cs
git commit -m "feat(windows): add ExternalAuthServiceNatsAuthProvider"
```

---

### Task 5: `MsalIdentityProvider.GetAccessTokenAsync` (reuse AAD identity for a second scope)

**Files:**
- Modify: `src/NotificationAgent.Windows/MsalIdentityProvider.cs`

**Interfaces:**
- Produces: `MsalIdentityProvider.GetAccessTokenAsync(string scope, CancellationToken ct = default)` returning `Task<string>` — silently acquires (falling back to interactive) a token for an arbitrary scope, reusing the same WAM-brokered account as `GetIdentityAsync`.

This is a behavior-preserving refactor of `GetIdentityAsync` (factors out the shared "build app, acquire token silently, fall back to interactive" logic) plus one new public method. No new test file: `MsalIdentityProvider` has no existing unit tests because it requires a live Windows WAM broker and signed-in account — this follows that existing precedent (see `context/component-map.md`: "Environment identity contract ... Covered indirectly").

- [ ] **Step 1: Replace the file**

```csharp
// src/NotificationAgent.Windows/MsalIdentityProvider.cs
using Microsoft.Identity.Client;
using Microsoft.Identity.Client.Broker;
using NotificationAgent.Core.Identity;

namespace NotificationAgent.Windows;

/// <summary>WAM-brokered silent sign-in (design §8). The application user ID is the
/// Entra object id ("oid" / AuthenticationResult.UniqueId), never the Windows account name.</summary>
public sealed class MsalIdentityProvider : IIdentityProvider
{
    private readonly string _clientId;
    private readonly string _tenantId;

    public MsalIdentityProvider(string clientId, string tenantId)
    {
        _clientId = clientId;
        _tenantId = tenantId;
    }

    public async ValueTask<AgentIdentity> GetIdentityAsync(CancellationToken ct = default)
    {
        var result = await AcquireTokenAsync(new[] { "User.Read" }, ct).ConfigureAwait(false);
        return new AgentIdentity($"u_{result.UniqueId}", DeviceIdStore.GetOrCreate());
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
            return await app.AcquireTokenInteractive(scopes)
                .ExecuteAsync(ct).ConfigureAwait(false);
        }
    }
}

/// <summary>Stable per-install device id under %LOCALAPPDATA% (ack field deviceId).</summary>
internal static class DeviceIdStore
{
    public static string GetOrCreate()
    {
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
```

- [ ] **Step 2: Build to confirm it compiles**

Run: `dotnet build src/NotificationAgent.Windows/NotificationAgent.Windows.csproj`
Expected: succeeds with no diagnostics

- [ ] **Step 3: Run the existing Windows test suite (confirms no regression)**

Run: `dotnet test tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj`
Expected: PASS (all existing tests, e.g. `WindowsToastContentFactoryTests`)

- [ ] **Step 4: Format check**

Run: `dotnet format tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj --verify-no-changes --no-restore`
Expected: succeeds with no diagnostics

- [ ] **Step 5: Commit**

```bash
git add src/NotificationAgent.Windows/MsalIdentityProvider.cs
git commit -m "refactor(windows): factor out MSAL token acquisition, add GetAccessTokenAsync"
```

---

### Task 6: `NatsAuthSelection` + wire into `NotificationAgent.Windows/Program.cs`

**Files:**
- Create: `src/NotificationAgent.Windows/NatsAuthSelection.cs`
- Modify: `src/NotificationAgent.Windows/Program.cs`
- Test: `tests/NotificationAgent.Windows.Tests/NatsAuthSelectionTests.cs`

**Interfaces:**
- Consumes: `INatsAuthProvider`, `CredsFileNatsAuthProvider` (Task 1); `ExternalAuthServiceNatsAuthProvider` (Task 4); `MsalIdentityProvider.GetAccessTokenAsync` (Task 5); `AgentHost.StartAsync`'s `authProvider` parameter (Task 2).
- Produces: `internal static class NatsAuthSelection` with `internal static INatsAuthProvider? Select(string? authServiceUrl, string? authServiceScope, string? credsFile, MsalIdentityProvider? msalIdentity, HttpClient httpClient)`.

- [ ] **Step 1: Write the failing tests**

```csharp
// tests/NotificationAgent.Windows.Tests/NatsAuthSelectionTests.cs
using NotificationAgent.Windows;
using Xunit;

namespace NotificationAgent.Windows.Tests;

public class NatsAuthSelectionTests
{
    private static readonly HttpClient Http = new();

    [Fact]
    public void Select_returns_null_when_nothing_configured()
    {
        var result = NatsAuthSelection.Select(
            authServiceUrl: null, authServiceScope: null, credsFile: null, msalIdentity: null, Http);

        Assert.Null(result);
    }

    [Fact]
    public void Select_returns_creds_file_provider_when_only_creds_file_set()
    {
        var result = NatsAuthSelection.Select(
            authServiceUrl: null, authServiceScope: null, credsFile: "/x.creds", msalIdentity: null, Http);

        var provider = Assert.IsType<CredsFileNatsAuthProvider>(result);
        Assert.Equal("/x.creds", provider.GetAuthOpts().CredsFile);
    }

    [Fact]
    public void Select_throws_when_auth_service_url_set_without_msal_identity()
    {
        Assert.Throws<InvalidOperationException>(() => NatsAuthSelection.Select(
            authServiceUrl: "https://auth.example.com",
            authServiceScope: "api://x/Nats.Connect",
            credsFile: null,
            msalIdentity: null,
            Http));
    }

    [Fact]
    public void Select_throws_when_auth_service_url_set_without_scope()
    {
        var msal = new MsalIdentityProvider("client-id", "tenant-id");

        Assert.Throws<InvalidOperationException>(() => NatsAuthSelection.Select(
            authServiceUrl: "https://auth.example.com",
            authServiceScope: null,
            credsFile: null,
            msalIdentity: msal,
            Http));
    }

    [Fact]
    public void Select_returns_external_auth_service_provider_when_fully_configured()
    {
        var msal = new MsalIdentityProvider("client-id", "tenant-id");

        var result = NatsAuthSelection.Select(
            authServiceUrl: "https://auth.example.com",
            authServiceScope: "api://x/Nats.Connect",
            credsFile: null,
            msalIdentity: msal,
            Http);

        Assert.IsType<ExternalAuthServiceNatsAuthProvider>(result);
    }
}
```

**Note:** `CredsFileNatsAuthProviderTests`-style access to `NotificationAgent.Core.Nats.CredsFileNatsAuthProvider` needs `using NotificationAgent.Core.Nats;` — add it to the test file's usings alongside `using NotificationAgent.Windows;` before running.

- [ ] **Step 2: Run tests to verify they fail**

Run: `dotnet test tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj --filter "FullyQualifiedName~NatsAuthSelectionTests"`
Expected: FAIL (build error — `NatsAuthSelection` does not exist)

- [ ] **Step 3: Implement `NatsAuthSelection`**

```csharp
// src/NotificationAgent.Windows/NatsAuthSelection.cs
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `dotnet test tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj --filter "FullyQualifiedName~NatsAuthSelectionTests"`
Expected: PASS (5 tests)

- [ ] **Step 5: Wire into Program.cs**

Replace `src/NotificationAgent.Windows/Program.cs` in full:

```csharp
using NotificationAgent.Core.Hosting;
using NotificationAgent.Core.Identity;
using NotificationAgent.Windows;

// One instance per interactive session: "Local\" mutexes are session-scoped,
// so two signed-in users each get their own agent (design §2, ADR-001).
using var singleInstance = new Mutex(
    initiallyOwned: true,
    @"Local\DesktopNotificationAgent", out var isFirstInstance);
if (!isFirstInstance)
{
    return;
}

var options = AgentOptions.FromEnvironment();
var clientId = Environment.GetEnvironmentVariable("NOTIFY_AAD_CLIENT_ID")?.Trim();
var tenantId = Environment.GetEnvironmentVariable("NOTIFY_AAD_TENANT_ID")?.Trim();
MsalIdentityProvider? msalIdentity =
    clientId is { Length: > 0 }
        ? new MsalIdentityProvider(
            clientId,
            tenantId is { Length: > 0 } ? tenantId : "organizations")
        : null;
IIdentityProvider identity = msalIdentity ?? new EnvironmentIdentityProvider();

var authProvider = NatsAuthSelection.Select(
    Environment.GetEnvironmentVariable("NOTIFY_NATS_AUTH_SERVICE_URL")?.Trim(),
    Environment.GetEnvironmentVariable("NOTIFY_NATS_AUTH_SERVICE_SCOPE")?.Trim(),
    Environment.GetEnvironmentVariable("NOTIFY_NATS_CREDS_FILE")?.Trim(),
    msalIdentity,
    new HttpClient());

await using var host = await AgentHost.StartAsync(options, identity, new WindowsToastRenderer(), authProvider);

var shutdown = new TaskCompletionSource();
Console.CancelKeyPress += (_, e) =>
{
    e.Cancel = true;
    shutdown.TrySetResult();
};
AppDomain.CurrentDomain.ProcessExit += (_, _) => shutdown.TrySetResult();
await shutdown.Task;
```

- [ ] **Step 6: Build to confirm it compiles**

Run: `dotnet build src/NotificationAgent.Windows/NotificationAgent.Windows.csproj`
Expected: succeeds with no diagnostics

- [ ] **Step 7: Run the full Windows test suite**

Run: `dotnet test tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj`
Expected: PASS (all tests, including Task 4's and this task's new ones)

- [ ] **Step 8: Format check**

Run: `dotnet format tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj --verify-no-changes --no-restore`
Expected: succeeds with no diagnostics

- [ ] **Step 9: Commit**

```bash
git add src/NotificationAgent.Windows/NatsAuthSelection.cs \
        src/NotificationAgent.Windows/Program.cs \
        tests/NotificationAgent.Windows.Tests/NatsAuthSelectionTests.cs
git commit -m "feat(windows): select NATS auth provider from env vars at startup"
```

---

### Task 7: Documentation

**Files:**
- Modify: `README.md`
- Modify: `context/configuration-and-runtime.md`

**Interfaces:** None (docs only).

- [ ] **Step 1: Update `README.md`'s configuration table**

In the `### Configuration (environment variables)` table, add three rows after `NOTIFY_ACK_SUBJECT`:

```markdown
| `NOTIFY_NATS_CREDS_FILE` | *(unset → no auth)* | Both hosts: path to a NATS `.creds` file |
| `NOTIFY_NATS_AUTH_SERVICE_URL` | *(unset → falls back to `NOTIFY_NATS_CREDS_FILE`, then no auth)* | Windows: HTTPS endpoint that mints a NATS JWT for the agent's AAD identity |
| `NOTIFY_NATS_AUTH_SERVICE_SCOPE` | *(required with `NOTIFY_NATS_AUTH_SERVICE_URL`)* | Windows: AAD scope requested when calling the auth service |
```

Immediately below the table, add a paragraph:

```markdown
`NOTIFY_NATS_URL` also accepts a `wss://` (NATS WebSocket) URL — the NATS client
detects the transport from the URL scheme automatically, so no other configuration
is needed to connect through a WebSocket-terminating load balancer or reverse proxy.

NATS auth is selected presence-based, same style as identity: on Windows,
`NOTIFY_NATS_AUTH_SERVICE_URL` (requires `NOTIFY_AAD_CLIENT_ID` and
`NOTIFY_NATS_AUTH_SERVICE_SCOPE`) takes priority over `NOTIFY_NATS_CREDS_FILE`,
which both hosts support; if neither is set, the connection is unauthenticated
(today's default).
```

- [ ] **Step 2: Update `context/configuration-and-runtime.md`**

Add three rows to its environment variables table (same content as the README table above), and add a bullet under "## Development runtime" or a new short subsection:

```markdown
`AgentHost.StartAsync`'s optional `INatsAuthProvider` (from `NotificationAgent.Core.Nats`)
owns auth configuration: `CredsFileNatsAuthProvider` (Core, both hosts) wraps a `.creds`
file; `ExternalAuthServiceNatsAuthProvider` (Windows only) calls an external HTTPS auth
service, reusing the AAD token already acquired via `MsalIdentityProvider` for a second,
separately configured scope. `NotificationAgent.Windows/NatsAuthSelection.cs` owns the
presence-based selection between them at startup.
```

- [ ] **Step 3: Commit**

```bash
git add README.md context/configuration-and-runtime.md
git commit -m "docs: document wss:// transport and NATS auth env vars"
```

---

## Self-Review Notes

- **Spec coverage:** §1 (transport) → doc-only, Task 7. §2 (interface + AgentHost wiring) → Task 2. §3 (creds file) → Task 1. §4 (external auth service, incl. AAD reuse and startup wiring) → Tasks 4–6. §5 (error handling) → covered inline in Tasks 4 and 6 (HTTP failure, missing-jwt, and startup-misconfiguration exceptions, each with a test). §6 (testing) → one test file per new unit, plus the doc update in Task 7.
- **Placeholder scan:** no TBD/TODO; the design doc's "assumed HTTP contract" is fully specified as concrete request/response shapes in Task 4, not left open.
- **Type consistency:** `INatsAuthProvider.GetAuthOpts()` signature is identical across Tasks 1, 2, 4, 6. `ExternalAuthServiceNatsAuthProvider`'s constructor signature (`Uri`, `Func<CancellationToken, Task<string>>`, `HttpClient`) matches between Task 4's definition and Task 6's `NatsAuthSelection` call site. `MsalIdentityProvider.GetAccessTokenAsync(string scope, CancellationToken ct = default)` matches between Task 5's definition and Task 6's `ct => msalIdentity.GetAccessTokenAsync(authServiceScope, ct)` usage.
