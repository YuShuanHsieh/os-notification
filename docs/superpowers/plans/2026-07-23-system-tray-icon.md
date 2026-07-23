# System Tray Icon Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Windows system tray icon to the agent with a right-click menu showing the running version and a "Close" item that reliably terminates the process (graceful shutdown with a hard-kill fallback), per [issue #10](https://github.com/YuShuanHsieh/os-notification/issues/10).

**Architecture:** A new `TrayApplicationContext : ApplicationContext` owns the `NotifyIcon`, its context menu, and the app's lifetime via `Application.Run`. `Program.cs` hands it a `Func<CancellationToken, Task<AgentHost>>` start delegate instead of directly awaiting `AgentHost.StartAsync` — WinForms' message loop replaces the current `Console.CancelKeyPress`/`AppDomain.ProcessExit`/`TaskCompletionSource` shutdown wiring. Two small, WinForms-independent helpers (`VersionInfo`, `TrayShutdown`) carry the only unit-testable logic; the tray context itself is UI-only and verified by manual Windows smoke test, matching how `WindowsToastRenderer`/`MsalIdentityProvider` are already handled in this repo.

**Tech Stack:** .NET 10, `System.Windows.Forms` (via `<UseWindowsForms>true</UseWindowsForms>`, no new NuGet package — ships with the Windows targeting pack already referenced via `EnableWindowsTargeting`), xUnit.

**Design doc:** `docs/superpowers/specs/2026-07-23-system-tray-icon-design.md` (referenced below as "the design doc").

## Global Constraints

- This feature is entirely additive within `NotificationAgent.Windows` — no changes to `NotificationAgent.Core`, `AgentHost`, or `AgentHost.DisposeAsync`.
- Analyzer warnings fail the build (`Directory.Build.props`: `TreatWarningsAsErrors=true` + StyleCop + `AnalysisLevel=latest`).
- `<UseWindowsForms>true</UseWindowsForms>` + `<ImplicitUsings>enable</ImplicitUsings>` together auto-generate global usings for `System`, `System.Windows.Forms`, `System.Drawing`, `System.Threading`, `System.Threading.Tasks`, `System.Collections.Generic`, `System.IO`, `System.Linq`, `System.Net.Http` (verified empirically) — **do not add explicit `using System.Windows.Forms;` or `using System.Drawing;`** in new files; that triggers CS0105 ("using directive is unnecessary") which fails the build under `TreatWarningsAsErrors`.
- Close timeout: 5 seconds (`TimeSpan.FromSeconds(5)`), exact value from the design doc.
- Tray tooltip base text: `"Desktop Notification Agent"`; on agent-start failure, becomes `"Desktop Notification Agent (agent failed to start)"` (both under WinForms' ~63-char tooltip limit).
- `InternalsVisibleTo` for `NotificationAgent.Windows.Tests` already exists in `NotificationAgent.Windows.csproj` — no new `InternalsVisibleTo` entries needed for this plan's `internal` types.
- Validation commands (per `AGENTS.md`), run from the repo root with `export PATH="/tmp/dotnet10:$PATH"; export DOTNET_CLI_HOME=/tmp/dotnet-cli-home-tmp` first if `dotnet` isn't already on `PATH`:
  - `dotnet build src/NotificationAgent.Windows/NotificationAgent.Windows.csproj`
  - `dotnet test tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj`
  - `dotnet format tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj --verify-no-changes --no-restore`

---

### Task 1: `<Version>` property + `VersionInfo`

**Files:**
- Modify: `src/NotificationAgent.Windows/NotificationAgent.Windows.csproj`
- Create: `src/NotificationAgent.Windows/VersionInfo.cs`
- Test: `tests/NotificationAgent.Windows.Tests/VersionInfoTests.cs`

**Interfaces:**
- Produces: `NotificationAgent.Windows.VersionInfo` with `internal static string Current { get; }` and `internal static string Format(Version? version)`.

- [ ] **Step 1: Add the `<Version>` property to the csproj**

In `src/NotificationAgent.Windows/NotificationAgent.Windows.csproj`, add a `<Version>` line right after `<AssemblyName>`:

```xml
    <OutputType>WinExe</OutputType>
    <AssemblyName>DesktopAgent</AssemblyName>
    <Version>0.1.0</Version>
    <TargetFramework>net10.0-windows10.0.19041.0</TargetFramework>
```

- [ ] **Step 2: Write the failing test**

```csharp
// tests/NotificationAgent.Windows.Tests/VersionInfoTests.cs
using NotificationAgent.Windows;
using Xunit;

namespace NotificationAgent.Windows.Tests;

public class VersionInfoTests
{
    [Theory]
    [InlineData(1, 2, 3, "1.2.3")]
    [InlineData(0, 1, 0, "0.1.0")]
    public void Format_returns_major_minor_build(int major, int minor, int build, string expected)
    {
        var version = new Version(major, minor, build);

        Assert.Equal(expected, VersionInfo.Format(version));
    }

    [Fact]
    public void Format_returns_unknown_when_version_is_null()
    {
        Assert.Equal("unknown", VersionInfo.Format(null));
    }
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `dotnet test tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj --filter "FullyQualifiedName~VersionInfoTests"`
Expected: FAIL (build error — `NotificationAgent.Windows.VersionInfo` does not exist)

- [ ] **Step 4: Write the implementation**

```csharp
// src/NotificationAgent.Windows/VersionInfo.cs
namespace NotificationAgent.Windows;

/// <summary>Formats the running assembly's version for the tray menu (design: system tray icon).</summary>
internal static class VersionInfo
{
    internal static string Current => Format(typeof(VersionInfo).Assembly.GetName().Version);

    internal static string Format(Version? version) =>
        version is null ? "unknown" : $"{version.Major}.{version.Minor}.{version.Build}";
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `dotnet test tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj --filter "FullyQualifiedName~VersionInfoTests"`
Expected: PASS (3 tests)

- [ ] **Step 6: Build and format check**

Run: `dotnet build src/NotificationAgent.Windows/NotificationAgent.Windows.csproj && dotnet format tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj --verify-no-changes --no-restore`
Expected: both succeed with no diagnostics

- [ ] **Step 7: Commit**

```bash
git add src/NotificationAgent.Windows/NotificationAgent.Windows.csproj \
        src/NotificationAgent.Windows/VersionInfo.cs \
        tests/NotificationAgent.Windows.Tests/VersionInfoTests.cs
git commit -m "feat(windows): add Version csproj property and VersionInfo helper"
```

---

### Task 2: `TrayShutdown` (graceful-then-timeout race)

**Files:**
- Create: `src/NotificationAgent.Windows/TrayShutdown.cs`
- Test: `tests/NotificationAgent.Windows.Tests/TrayShutdownTests.cs`

**Interfaces:**
- Produces: `NotificationAgent.Windows.TrayShutdown` with `internal static Task CloseAsync(Func<Task> disposeAsync, TimeSpan timeout)`.

- [ ] **Step 1: Write the failing tests**

```csharp
// tests/NotificationAgent.Windows.Tests/TrayShutdownTests.cs
using NotificationAgent.Windows;
using Xunit;

namespace NotificationAgent.Windows.Tests;

public class TrayShutdownTests
{
    [Fact]
    public async Task CloseAsync_awaits_dispose_when_it_completes_within_timeout()
    {
        var completed = false;
        async Task DisposeAsync()
        {
            await Task.Delay(10);
            completed = true;
        }

        await TrayShutdown.CloseAsync(DisposeAsync, TimeSpan.FromSeconds(5));

        Assert.True(completed);
    }

    [Fact]
    public async Task CloseAsync_returns_once_timeout_elapses_even_if_dispose_never_completes()
    {
        var tcs = new TaskCompletionSource();

        await TrayShutdown.CloseAsync(() => tcs.Task, TimeSpan.FromMilliseconds(50));

        Assert.False(tcs.Task.IsCompleted);
    }

    [Fact]
    public async Task CloseAsync_does_not_throw_when_dispose_faults()
    {
        Task DisposeAsync() => Task.FromException(new InvalidOperationException("boom"));

        await TrayShutdown.CloseAsync(DisposeAsync, TimeSpan.FromSeconds(5));
    }
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `dotnet test tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj --filter "FullyQualifiedName~TrayShutdownTests"`
Expected: FAIL (build error — `NotificationAgent.Windows.TrayShutdown` does not exist)

- [ ] **Step 3: Write the implementation**

```csharp
// src/NotificationAgent.Windows/TrayShutdown.cs
namespace NotificationAgent.Windows;

/// <summary>Graceful-then-timeout shutdown race, extracted so it's unit-testable without any
/// WinForms/UI involvement (design: system tray icon). Task.WhenAny does not observe or rethrow
/// an exception from the losing/faulted task, so a failing disposeAsync is swallowed by design —
/// Close must never fail to close.</summary>
internal static class TrayShutdown
{
    internal static async Task CloseAsync(Func<Task> disposeAsync, TimeSpan timeout)
    {
        var dispose = disposeAsync();
        await Task.WhenAny(dispose, Task.Delay(timeout)).ConfigureAwait(false);
    }
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `dotnet test tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj --filter "FullyQualifiedName~TrayShutdownTests"`
Expected: PASS (3 tests)

- [ ] **Step 5: Build and format check**

Run: `dotnet build src/NotificationAgent.Windows/NotificationAgent.Windows.csproj && dotnet format tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj --verify-no-changes --no-restore`
Expected: both succeed with no diagnostics

- [ ] **Step 6: Commit**

```bash
git add src/NotificationAgent.Windows/TrayShutdown.cs \
        tests/NotificationAgent.Windows.Tests/TrayShutdownTests.cs
git commit -m "feat(windows): add TrayShutdown graceful-then-timeout close race"
```

---

### Task 3: `TrayApplicationContext`

**Files:**
- Modify: `src/NotificationAgent.Windows/NotificationAgent.Windows.csproj`
- Create: `src/NotificationAgent.Windows/TrayApplicationContext.cs`

**Interfaces:**
- Consumes: `VersionInfo.Current` (Task 1), `TrayShutdown.CloseAsync` (Task 2), `NotificationAgent.Core.Hosting.AgentHost` (existing — `public static Task<AgentHost> StartAsync(...)`, `public ValueTask DisposeAsync()`).
- Produces: `public sealed class TrayApplicationContext : ApplicationContext` with constructor `TrayApplicationContext(Func<CancellationToken, Task<AgentHost>> startAgent)`.

This task has no automated tests — `NotifyIcon`/`ContextMenuStrip`/`Application.Run` need a live Windows desktop session, matching this repo's existing precedent for `WindowsToastRenderer`/`MsalIdentityProvider` (no unit tests; verified by manual Windows smoke test, added in Task 5). Verification here is a clean build plus careful self-review, since there's no automated safety net for this specific class.

- [ ] **Step 1: Enable WinForms in the csproj**

In `src/NotificationAgent.Windows/NotificationAgent.Windows.csproj`, add `<UseWindowsForms>true</UseWindowsForms>` right after the `EnableWindowsTargeting` line:

```xml
    <!-- Allows compile on non-Windows build hosts; runtime is Windows-only. -->
    <EnableWindowsTargeting>true</EnableWindowsTargeting>
    <UseWindowsForms>true</UseWindowsForms>
```

- [ ] **Step 2: Build to confirm the csproj change alone compiles**

Run: `dotnet build src/NotificationAgent.Windows/NotificationAgent.Windows.csproj`
Expected: succeeds with no diagnostics (no code uses WinForms types yet)

- [ ] **Step 3: Write `TrayApplicationContext`**

```csharp
// src/NotificationAgent.Windows/TrayApplicationContext.cs
using NotificationAgent.Core.Hosting;

namespace NotificationAgent.Windows;

/// <summary>Owns the tray icon, its context menu, and the app's lifetime via the WinForms
/// message loop (design: system tray icon, issue #10). No visible window — the NotifyIcon
/// alone keeps Application.Run alive.</summary>
public sealed class TrayApplicationContext : ApplicationContext
{
    private const string BaseTooltip = "Desktop Notification Agent";
    private static readonly TimeSpan CloseTimeout = TimeSpan.FromSeconds(5);

    private readonly NotifyIcon _notifyIcon;
    private volatile AgentHost? _host;

    public TrayApplicationContext(Func<CancellationToken, Task<AgentHost>> startAgent)
    {
        var versionItem = new ToolStripMenuItem($"Version {VersionInfo.Current}")
        {
            Enabled = false,
        };
        var closeItem = new ToolStripMenuItem("Close");
        closeItem.Click += (_, _) => _ = OnCloseClickedAsync();

        var menu = new ContextMenuStrip();
        menu.Items.Add(versionItem);
        menu.Items.Add(new ToolStripSeparator());
        menu.Items.Add(closeItem);

        _notifyIcon = new NotifyIcon
        {
            Icon = SystemIcons.Application,
            Text = BaseTooltip,
            ContextMenuStrip = menu,
            Visible = true,
        };

        _ = StartAgentAsync(startAgent);
    }

    private async Task StartAgentAsync(Func<CancellationToken, Task<AgentHost>> startAgent)
    {
        try
        {
            _host = await startAgent(CancellationToken.None).ConfigureAwait(false);
        }
        catch
        {
            // Best-effort: a failed start must not crash the tray (design: system tray icon).
            _notifyIcon.Text = $"{BaseTooltip} (agent failed to start)";
        }
    }

    private async Task OnCloseClickedAsync()
    {
        _notifyIcon.Visible = false;
        var host = _host;
        await TrayShutdown.CloseAsync(
            host is null ? () => Task.CompletedTask : () => host.DisposeAsync().AsTask(),
            CloseTimeout).ConfigureAwait(false);
        ExitThread();
        Environment.Exit(0);
    }
}
```

Notes for the implementer:
- Do **not** add `using System.Windows.Forms;` or `using System.Drawing;` — both are already implicit global usings once `UseWindowsForms=true` is set (see Global Constraints). Adding them explicitly causes a duplicate-using warning that fails the build.
- `_host` is `volatile` because it's written from the `StartAgentAsync` background continuation and read from the UI-thread `OnCloseClickedAsync` click handler — `volatile` guarantees the read sees the write without needing full lock-based synchronization for this simple reference assignment.
- `closeItem.Click += (_, _) => _ = OnCloseClickedAsync();` deliberately discards the returned `Task` (event handlers can't be `async` return-`Task` themselves) — this is the standard, analyzer-clean pattern for fire-and-forget from a UI event handler (avoids `async void` and avoids CS4014 "not awaited" warnings, since the discard is explicit).

- [ ] **Step 4: Build to confirm it compiles**

Run: `dotnet build src/NotificationAgent.Windows/NotificationAgent.Windows.csproj`
Expected: succeeds with no diagnostics

- [ ] **Step 5: Run the full Windows test suite (confirms no regression from the csproj/WinForms change)**

Run: `dotnet test tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj`
Expected: all existing tests still pass

- [ ] **Step 6: Format check**

Run: `dotnet format tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj --verify-no-changes --no-restore`
Expected: succeeds with no diagnostics

- [ ] **Step 7: Commit**

```bash
git add src/NotificationAgent.Windows/NotificationAgent.Windows.csproj \
        src/NotificationAgent.Windows/TrayApplicationContext.cs
git commit -m "feat(windows): add TrayApplicationContext (tray icon, menu, close)"
```

---

### Task 4: Wire `Program.cs` to the tray context

**Files:**
- Modify: `src/NotificationAgent.Windows/Program.cs`

**Interfaces:**
- Consumes: `TrayApplicationContext(Func<CancellationToken, Task<AgentHost>> startAgent)` (Task 3).

This is an intentional behavior change: the current `Console.CancelKeyPress` / `AppDomain.ProcessExit` / `TaskCompletionSource`-based shutdown wiring is removed entirely and replaced by the tray's "Close" item (Task 3) as the app's shutdown trigger, matching the design doc's architecture. `Console.CancelKeyPress` was already effectively dead for a normally-launched `WinExe` app (no console window attached), so this isn't a loss of a working code path — it's replacing an approach that never had a real trigger with one that does (the tray icon).

- [ ] **Step 1: Replace the file**

```csharp
// src/NotificationAgent.Windows/Program.cs
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
IIdentityProvider identity = (IIdentityProvider?)msalIdentity ?? new EnvironmentIdentityProvider();

var authProvider = NatsAuthSelection.Select(
    Environment.GetEnvironmentVariable("NOTIFY_NATS_AUTH_SERVICE_URL")?.Trim(),
    Environment.GetEnvironmentVariable("NOTIFY_NATS_AUTH_SERVICE_SCOPE")?.Trim(),
    Environment.GetEnvironmentVariable("NOTIFY_NATS_CREDS_FILE")?.Trim(),
    msalIdentity,
    new HttpClient());

Application.Run(new TrayApplicationContext(
    ct => AgentHost.StartAsync(options, identity, new WindowsToastRenderer(), authProvider, ct)));
```

- [ ] **Step 2: Build to confirm it compiles**

Run: `dotnet build src/NotificationAgent.Windows/NotificationAgent.Windows.csproj`
Expected: succeeds with no diagnostics

- [ ] **Step 3: Run the full Windows test suite**

Run: `dotnet test tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj`
Expected: all tests pass (including Tasks 1–2's new ones)

- [ ] **Step 4: Format check**

Run: `dotnet format tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj --verify-no-changes --no-restore`
Expected: succeeds with no diagnostics

- [ ] **Step 5: Commit**

```bash
git add src/NotificationAgent.Windows/Program.cs
git commit -m "feat(windows): run the agent under a tray icon instead of headless"
```

---

### Task 5: Documentation

**Files:**
- Modify: `README.md`

**Interfaces:** None (docs only).

- [ ] **Step 1: Add a manual Windows smoke-test section**

In `README.md`, immediately after the existing "Verify the avatar image renders correctly (Windows)" section (which ends with the `http://not-https.example.com/x.jpg` negative-case paragraph), add:

```markdown
### Verify the tray icon and Close button (Windows)

With the Windows head running (above), confirm the tray icon and its Close action:

1. Look for the app's icon in the Windows system tray (it may be under the "^" overflow arrow).
2. Right-click it — the menu shows "Version 0.1.0" (disabled, not clickable) and a "Close" item.
3. Click "Close". Within a few seconds the tray icon disappears and the process exits — confirm `DesktopAgent.exe` is gone from Task Manager.
4. **Failure-path check:** point `NOTIFY_NATS_URL` at an unreachable server (e.g. `nats://127.0.0.1:4223`) and relaunch. The tray icon should still appear, its tooltip should mention the agent failed to start, and "Close" should still terminate the process within the same few seconds.
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: add tray icon manual verification steps"
```

---

## Self-Review Notes

- **Spec coverage:** Architecture (tray owns lifetime via `Application.Run`, agent starts as background task) → Tasks 3–4. `TrayApplicationContext` component (icon, menu, version label, close wiring) → Task 3. `VersionInfo` → Task 1. `TrayShutdown` graceful-then-timeout race → Task 2. Error handling (startup failure keeps tray usable; Close never throws/hangs past timeout) → Task 3. Testing plan (unit tests for the two pure helpers, manual checklist for the UI class) → Tasks 1, 2, 5.
- **Placeholder scan:** none found — every step has complete code; the design doc's "placeholder icon" (non-goal: custom branding) is reflected as the literal `SystemIcons.Application` in Task 3's code, not left as a TBD.
- **Type consistency:** `Func<CancellationToken, Task<AgentHost>>` is identical in Task 3's constructor signature and Task 4's call site (`ct => AgentHost.StartAsync(...)`). `VersionInfo.Current`/`Format` and `TrayShutdown.CloseAsync` signatures match between their defining tasks (1, 2) and their Task 3 usage.
