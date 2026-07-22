# Remove Windows App SDK Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Windows App SDK notifications with Windows Community Toolkit notifications so the Windows head builds with the standalone .NET 10 SDK and no longer deploys the Windows App SDK runtime.

**Architecture:** Keep the existing `IToastRenderer` boundary and `ToastRequest` data contract. Isolate toolkit XML construction in an internal factory, submit through `ToastNotificationManagerCompat`, and test payload construction without displaying a Windows notification.

**Tech Stack:** .NET 10, Microsoft.Toolkit.Uwp.Notifications 7.1.3 (explicit legacy compatibility dependency), System.Drawing.Common 10.0.10 security override, Windows toast notifications, xUnit

---

## Task 1: Add Failing Notification-Content Tests

**Files:**
- Create: `tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj`
- Create: `tests/NotificationAgent.Windows.Tests/WindowsToastContentFactoryTests.cs`
- Modify: `src/NotificationAgent.Windows/NotificationAgent.Windows.csproj`

- [x] **Step 1: Create the Windows notification test project**

Create `tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj`:

```xml
<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net10.0-windows10.0.19041.0</TargetFramework>
    <EnableWindowsTargeting>true</EnableWindowsTargeting>
    <ImplicitUsings>enable</ImplicitUsings>
    <Nullable>enable</Nullable>
    <IsPackable>false</IsPackable>
    <IsTestProject>true</IsTestProject>
  </PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Microsoft.NET.Test.Sdk" Version="17.8.0" />
    <PackageReference Include="xunit" Version="2.5.3" />
    <PackageReference Include="xunit.runner.visualstudio" Version="2.5.3" />
  </ItemGroup>
  <ItemGroup>
    <Using Include="Xunit" />
    <ProjectReference Include="..\..\src\NotificationAgent.Windows\NotificationAgent.Windows.csproj" />
  </ItemGroup>
</Project>
```

Expose internal Windows-head types to the test assembly by adding to `src/NotificationAgent.Windows/NotificationAgent.Windows.csproj`:

```xml
<ItemGroup>
  <InternalsVisibleTo Include="NotificationAgent.Windows.Tests" />
</ItemGroup>
```

Keep the Windows test project outside `NotificationAgent.sln`, matching the existing rule that Windows-specific projects are built separately from the cross-platform solution.

- [x] **Step 2: Write tests for generated toast content**

Create `tests/NotificationAgent.Windows.Tests/WindowsToastContentFactoryTests.cs`:

```csharp
using System.Xml.Linq;
using NotificationAgent.Core.Models;
using NotificationAgent.Core.Rendering;
using NotificationAgent.Windows;

namespace NotificationAgent.Windows.Tests;

public sealed class WindowsToastContentFactoryTests
{
    [Fact]
    public void Create_IncludesTextAndAttribution()
    {
        var document = CreateDocument(ActionUrl: null);
        var text = document.Descendants("text").ToArray();
        Assert.Equal("Title", text[0].Value);
        Assert.Equal("Message", text[1].Value);
        Assert.Equal("App", text.Single(node =>
            (string?)node.Attribute("placement") == "attribution").Value);
    }

    [Fact]
    public void Create_AddsProtocolButtonForValidHttpsAction()
    {
        var action = Assert.Single(CreateDocument().Descendants("action"));
        Assert.Equal("Open", (string?)action.Attribute("content"));
        Assert.Equal("protocol", (string?)action.Attribute("activationType"));
        Assert.Equal("https://example.com/item", (string?)action.Attribute("arguments"));
    }

    [Theory]
    [InlineData(null)]
    [InlineData("http://example.com/item")]
    [InlineData("file:///C:/Windows/System32/cmd.exe")]
    public void Create_OmitsActionForMissingOrUnsafeUrl(string? actionUrl)
    {
        Assert.Empty(CreateDocument(ActionUrl: actionUrl).Descendants("action"));
    }

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("   ")]
    public void Create_OmitsActionForMissingOrBlankLabel(string? actionLabel)
    {
        Assert.Empty(CreateDocument(ActionLabel: actionLabel).Descendants("action"));
    }

    private static XDocument CreateDocument(
        string? ActionLabel = "Open",
        string? ActionUrl = "https://example.com/item")
    {
        var request = new ToastRequest(
            "Title", "Message", "App", ActionLabel, ActionUrl,
            Array.Empty<InboundNotification>());
        return XDocument.Parse(WindowsToastContentFactory.Create(request).GetContent());
    }
}
```

- [x] **Step 3: Run the tests and verify the red state**

Run:

```bash
env DOTNET_CLI_HOME=/tmp/dotnet10-cli-home /tmp/dotnet10/dotnet test tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj
```

Expected: FAIL because `WindowsToastContentFactory` does not exist and the existing Windows App SDK integration invokes unsupported packaging targets on Linux.

## Task 2: Replace Windows App SDK Notifications

**Files:**
- Create: `src/NotificationAgent.Windows/WindowsToastContentFactory.cs`
- Modify: `src/NotificationAgent.Windows/NotificationAgent.Windows.csproj`
- Modify: `src/NotificationAgent.Windows/WindowsToastRenderer.cs`
- Modify: `src/NotificationAgent.Windows/Program.cs`
- Test: `tests/NotificationAgent.Windows.Tests/WindowsToastContentFactoryTests.cs`

- [x] **Step 1: Replace project dependencies and build properties**

Remove these entries from `src/NotificationAgent.Windows/NotificationAgent.Windows.csproj`:

```xml
<WindowsPackageType>None</WindowsPackageType>
<WindowsAppSDKSelfContained>true</WindowsAppSDKSelfContained>
<PackageReference Include="Microsoft.WindowsAppSDK" Version="1.5.*" />
<PackageReference Include="Microsoft.Windows.SDK.BuildTools" Version="10.0.*" />
```

Add:

```xml
<PackageReference Include="Microsoft.Toolkit.Uwp.Notifications" Version="7.1.3" />
<!-- Override the toolkit's vulnerable System.Drawing.Common 4.7.0 dependency. -->
<PackageReference Include="System.Drawing.Common" Version="10.0.10" />
```

Keep `TargetFramework`, `RuntimeIdentifiers`, `Nullable`, `ImplicitUsings`, and `EnableWindowsTargeting` unchanged.

- [x] **Step 2: Implement notification-content construction**

Create `src/NotificationAgent.Windows/WindowsToastContentFactory.cs`:

```csharp
using Microsoft.Toolkit.Uwp.Notifications;
using NotificationAgent.Core.Rendering;

namespace NotificationAgent.Windows;

internal static class WindowsToastContentFactory
{
    internal static ToastContent Create(ToastRequest toast)
    {
        var builder = new ToastContentBuilder()
            .AddText(toast.Title)
            .AddText(toast.Message);

        if (!string.IsNullOrEmpty(toast.Attribution))
            builder.AddAttributionText(toast.Attribution);

        if (!string.IsNullOrWhiteSpace(toast.ActionLabel)
            && ActionUrlPolicy.TryCreate(toast.ActionUrl, out var actionUri))
        {
            builder.AddButton(new ToastButton()
                .SetContent(toast.ActionLabel)
                .SetProtocolActivation(actionUri));
        }

        return builder.GetToastContent();
    }
}
```

- [x] **Step 3: Replace notification submission**

Replace `src/NotificationAgent.Windows/WindowsToastRenderer.cs` with:

```csharp
using Microsoft.Toolkit.Uwp.Notifications;
using NotificationAgent.Core.Rendering;
using Windows.UI.Notifications;

namespace NotificationAgent.Windows;

public sealed class WindowsToastRenderer : IToastRenderer
{
    public ValueTask<DateTimeOffset> ShowAsync(ToastRequest toast, CancellationToken ct = default)
    {
        var content = WindowsToastContentFactory.Create(toast);
        var notification = new ToastNotification(content.GetXml());
        ToastNotificationManagerCompat.CreateToastNotifier().Show(notification);
        return ValueTask.FromResult(DateTimeOffset.UtcNow);
    }
}
```

- [x] **Step 4: Remove Windows App SDK lifecycle calls**

Replace `src/NotificationAgent.Windows/Program.cs` with:

```csharp
using NotificationAgent.Core.Hosting;
using NotificationAgent.Core.Identity;
using NotificationAgent.Windows;

// One instance per interactive session: "Local\" mutexes are session-scoped,
// so two signed-in users each get their own agent (design §2, ADR-001).
using var singleInstance = new Mutex(initiallyOwned: true,
    @"Local\DesktopNotificationAgent", out var isFirstInstance);
if (!isFirstInstance) return;

var options = AgentOptions.FromEnvironment();
var clientId = Environment.GetEnvironmentVariable("NOTIFY_AAD_CLIENT_ID")?.Trim();
var tenantId = Environment.GetEnvironmentVariable("NOTIFY_AAD_TENANT_ID")?.Trim();
IIdentityProvider identity =
    clientId is { Length: > 0 }
        ? new MsalIdentityProvider(clientId,
            tenantId is { Length: > 0 } ? tenantId : "organizations")
        : new EnvironmentIdentityProvider();

await using var host = await AgentHost.StartAsync(options, identity, new WindowsToastRenderer());

var shutdown = new TaskCompletionSource();
Console.CancelKeyPress += (_, e) => { e.Cancel = true; shutdown.TrySetResult(); };
AppDomain.CurrentDomain.ProcessExit += (_, _) => shutdown.TrySetResult();
await shutdown.Task;
```

- [x] **Step 5: Run notification-content tests**

Run:

```bash
env DOTNET_CLI_HOME=/tmp/dotnet10-cli-home /tmp/dotnet10/dotnet test tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj
```

Expected: PASS for all notification-content tests on Linux without Visual Studio packaging tasks.

- [x] **Step 6: Commit the notification migration**

```bash
git add src/NotificationAgent.Windows tests/NotificationAgent.Windows.Tests
git commit -m "refactor: replace Windows App SDK notifications"
```

## Task 3: Update Current Documentation

**Files:**
- Modify: `README.md`

- [x] **Step 1: Update architecture and project descriptions**

Replace the Windows project table row with:

```markdown
| `src/NotificationAgent.Windows` | `net10.0-windows10.0.19041.0` | Production head: Windows Community Toolkit toasts, MSAL/WAM identity, single-instance mutex. **Not in the solution file** — compiled separately; execution is Windows-only. |
```

After the standard solution build commands, add:

````markdown
The Windows head and its notification-content tests are intentionally excluded from the cross-platform solution. They can still be compiled and tested with the standalone .NET 10 SDK on Linux or WSL:

```bash
dotnet build src/NotificationAgent.Windows
dotnet test tests/NotificationAgent.Windows.Tests
```
````

- [x] **Step 2: Update Windows build instructions**

Keep:

```powershell
dotnet build src/NotificationAgent.Windows
```

Replace the Windows-head introduction with:

```markdown
`NotificationAgent.Windows` is deliberately excluded from the solution. It can be compiled on Linux, WSL, or Windows with the standalone .NET 10 SDK, but it can only run and display notifications on Windows 10/11:
```

Replace the paragraph after the PowerShell example with:

```markdown
It runs unpackaged (no MSIX), enforces one instance per session via a `Local\` mutex, submits native toasts through `Microsoft.Toolkit.Uwp.Notifications`, and uses Windows protocol activation to open a validated HTTPS action URL in the default browser. It does not require the Windows App SDK runtime.
```

- [x] **Step 3: Verify documentation scope**

Run:

```bash
rg -n '(Microsoft\.WindowsAppSDK|Windows App SDK|AppNotification)' README.md src tests tools
```

Expected: no active references. Do not alter `docs/superpowers/plans/2026-07-15-windows-desktop-notification-agent.md`.

- [x] **Step 4: Commit documentation**

```bash
git add README.md
git commit -m "docs: describe toolkit notification backend"
```

## Task 4: Full Verification

**Files:**
- Verify: `NotificationAgent.sln`
- Verify: `src/NotificationAgent.Windows/NotificationAgent.Windows.csproj`

- [x] **Step 1: Build and test the complete solution**

Run:

```bash
env DOTNET_CLI_HOME=/tmp/dotnet10-cli-home /tmp/dotnet10/dotnet build NotificationAgent.sln
env DOTNET_CLI_HOME=/tmp/dotnet10-cli-home /tmp/dotnet10/dotnet test NotificationAgent.sln --no-build
```

Expected: both commands exit 0 and all Core tests pass.

Run the Windows notification-content tests separately:

```bash
env DOTNET_CLI_HOME=/tmp/dotnet10-cli-home /tmp/dotnet10/dotnet test tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj
```

Expected: all notification-content tests pass on Linux.

- [x] **Step 2: Build the Windows head directly on Linux**

Run:

```bash
env DOTNET_CLI_HOME=/tmp/dotnet10-cli-home /tmp/dotnet10/dotnet build src/NotificationAgent.Windows/NotificationAgent.Windows.csproj
```

Expected: exit 0 with no reference to `Microsoft.Build.Packaging.Pri.Tasks.dll`, `Microsoft.WindowsAppSDK`, or Visual Studio packaging targets.

- [x] **Step 3: Verify dependency and source removal**

Run:

```bash
rg -n '(Microsoft\.WindowsAppSDK|Windows App SDK|AppNotification)' README.md src tests tools
env DOTNET_CLI_HOME=/tmp/dotnet10-cli-home /tmp/dotnet10/dotnet list src/NotificationAgent.Windows/NotificationAgent.Windows.csproj package --include-transitive
git diff --check agent/fix-cwe-78...HEAD
git status --short
```

Expected: no active Windows App SDK references; the package graph contains `Microsoft.Toolkit.Uwp.Notifications` and no `Microsoft.WindowsAppSDK`; no whitespace errors; clean worktree.

- [ ] **Step 4: Record the Windows smoke test**

On a Windows 10/11 host, run the agent and TestPublisher, then confirm a toast appears and its HTTPS action button opens the expected browser URL. If no Windows host is available, report this manual test as pending rather than claiming it passed.
