# System tray icon with version display and force close

## Purpose

The agent currently runs as a headless console-mode process with no UI at all (`src/NotificationAgent.Windows/Program.cs` — `WinExe` output, no window, no message loop). There's no way for a user or developer to tell it's running, see which version is deployed, or shut it down short of killing the process or signing out. This adds a system tray icon with a right-click menu showing the running version and a Close item that reliably terminates the process, even if the agent itself is stuck or has failed to start.

Source: [issue #10](https://github.com/YuShuanHsieh/os-notification/issues/10).

## Scope

- **In scope:** a `NotifyIcon` in the Windows system tray for the life of the process; a right-click context menu with a version label and a Close item; Close performs a graceful shutdown with a hard-kill fallback if it hangs.
- **Out of scope:** custom icon artwork (uses a placeholder for now), an automated/CI-driven versioning scheme (manual `<Version>` bump for now), any change to `AgentHost`, `AgentHost.DisposeAsync`, or Core project code, any additional tray functionality beyond version + Close (no settings, no pause/resume, no notification history).

## Architecture

`Program.cs` restructures around a new `TrayApplicationContext` class (`NotificationAgent.Windows`) that owns the `NotifyIcon` and the app's lifetime via WinForms:

```text
Program.cs (main thread)
  ├─ single-instance mutex check (unchanged, stays first)
  ├─ build AgentOptions / identity / authProvider (unchanged, stays as-is)
  └─ Application.Run(new TrayApplicationContext(startAgent: ct => AgentHost.StartAsync(...)))
       │
       ▼
TrayApplicationContext : ApplicationContext
  ├─ constructs NotifyIcon (visible immediately, placeholder icon, tooltip)
  ├─ builds ContextMenuStrip: "Version <x.y.z>" (disabled label) + separator + "Close"
  ├─ starts AgentHost.StartAsync(...) as a background Task right after the icon is shown
  │     ├─ on success: keeps the AgentHost reference for later disposal
  │     └─ on failure: tooltip flags the failure; tray + Close remain usable
  └─ Close click → TrayShutdown.CloseAsync(graceful-then-timeout) → ExitThread() → Environment.Exit(0)
```

`Application.Run` requires `<UseWindowsForms>true</UseWindowsForms>` in `NotificationAgent.Windows.csproj` (a lightweight addition — no XAML/WinUI runtime, consistent with the project's existing "unpackaged, minimal runtime deps" posture already established for `Microsoft.Toolkit.Uwp.Notifications`).

The existing `AgentOptions`/identity/`authProvider` construction in `Program.cs` (env var reads, `MsalIdentityProvider`, `NatsAuthSelection`) is untouched — only the tail end changes from `await AgentHost.StartAsync(...); await shutdown.Task;` to handing a start delegate to the tray context and letting WinForms drive the app's lifetime. The single-instance mutex check stays first, before `Application.Run` starts, so a second launch attempt exits immediately without ever showing a second tray icon.

## Components

### `TrayApplicationContext` (`src/NotificationAgent.Windows/TrayApplicationContext.cs`)

`sealed class TrayApplicationContext : ApplicationContext`:

- **Constructor**: `TrayApplicationContext(Func<CancellationToken, Task<AgentHost>> startAgent)`. Taking a delegate (rather than the raw `AgentOptions`/identity/auth pieces) keeps this class ignorant of composition — `Program.cs` still owns wiring, matching the existing separation of concerns in the codebase.
- **`NotifyIcon`**: `Icon = SystemIcons.Application` (placeholder), `Text` = `"Desktop Notification Agent"` (tooltip, well under WinForms' 63-char limit), `Visible = true` set immediately on construction — before the agent finishes starting, so the icon appears without waiting on NATS connect.
- **Context menu** (`ContextMenuStrip`, assigned to `NotifyIcon.ContextMenuStrip`):
  - A disabled `ToolStripMenuItem` reading `"Version " + VersionInfo.Current` — disabled so it renders as a label, not a clickable action.
  - A separator.
  - `"Close"` — wired to `OnCloseClicked`, which calls `TrayShutdown.CloseAsync` (below) then exits.
- **Agent lifetime**: the background start `Task` (from `startAgent`) is awaited internally. On success, the resulting `AgentHost` is stored for disposal on Close. On failure, the tooltip is updated to flag it (e.g., appended `" (agent failed to start)"`); the tray icon and Close item remain fully usable either way — a broken startup is exactly when the tray's Close button is needed most.

### `VersionInfo` (small static helper, same file or a sibling file)

Reads the assembly's version once (backed by a new `<Version>` property in `NotificationAgent.Windows.csproj`, manually bumped per release — this repo has no CI/release pipeline yet, so a git-derived versioning scheme would add build-system complexity with no consumer) and formats it as e.g. `"1.2.3"`.

### `TrayShutdown` (`src/NotificationAgent.Windows/TrayShutdown.cs`, `internal static`)

```csharp
internal static async Task CloseAsync(Func<Task> disposeAsync, TimeSpan timeout)
{
    Task dispose;
    try
    {
        dispose = disposeAsync();
    }
    catch
    {
        return;
    }

    await Task.WhenAny(dispose, Task.Delay(timeout)).ConfigureAwait(false);
}
```

Extracted as its own pure, WinForms-independent unit specifically so the graceful-then-timeout race is unit-testable (see Testing). `TrayApplicationContext.OnCloseClicked` calls this with `_host?.DisposeAsync().AsTask() ?? Task.CompletedTask` (a no-op immediately-completed task when the agent never started) and a 5-second timeout, then unconditionally calls `ExitThread()` followed by `Environment.Exit(0)` — regardless of whether disposal finished or the timeout won. Any exception from `disposeAsync` is swallowed (matching `AgentHost.DisposeAsync`'s own existing best-effort, non-throwing posture) — Close must never fail to close. The `NotifyIcon` is hidden (`Visible = false`) at the very start of `OnCloseClicked`, before the async work, so there's no lingering icon during shutdown.

5 seconds is long enough for a normal graceful shutdown (pipeline drain + NATS disconnect are already fast, best-effort operations per `AgentHost.DisposeAsync`) and short enough that a hung shutdown doesn't make Close feel broken. `ExitThread()` stops the WinForms message loop; the subsequent `Environment.Exit(0)` guarantees the process actually terminates even if some non-foreground thread (e.g. a stuck NATS reconnect attempt) would otherwise keep it alive — this is the "force" half of "force close."

## Error handling

- **Agent startup failure** (`AgentHost.StartAsync` throws — e.g. NATS unreachable, identity resolution fails): caught inside `TrayApplicationContext`, never crashes the process. Tooltip flags it; Close still works, skipping agent disposal since there's no `AgentHost` to dispose.
- **Close click**: graceful-then-timeout-hard-kill via `TrayShutdown.CloseAsync` as described above. Never throws, never hangs past the timeout.
- **Single-instance mutex**: unchanged behavior — a second launch attempt exits before `Application.Run` is ever called.

## Testing

- **`VersionInfo`**: unit test that it formats a given version as `"1.2.3"`, plus a sensible fallback if unset (matching the csproj default of `1.0.0.0`).
- **`TrayShutdown.CloseAsync`**: unit tested with no WinForms/UI involvement — one test with a fast `disposeAsync` (completes well within the timeout, confirm it's awaited/completes normally), one with a slow/hung `disposeAsync` (never completes) asserting `CloseAsync` still returns once the timeout elapses. Same short-real-delay testing style already used in `ExternalAuthServiceNatsAuthProviderTests.Callback_times_out_when_auth_service_is_slow`.
- **`TrayApplicationContext` itself** (`NotifyIcon`/`ContextMenuStrip` construction, `Application.Run` wiring): not unit-testable — needs a live Windows desktop session, same as `WindowsToastRenderer`/`MsalIdentityProvider` today. Verified by a manual Windows smoke-test checklist added to the README (alongside the existing "Verify the avatar image renders correctly" section): launch the agent, confirm the tray icon appears immediately, right-click shows "Version x.y.z" + Close, clicking Close terminates the process (checked via Task Manager) within the timeout window; and for the failure path, confirm Close still works when `NOTIFY_NATS_URL` points at an unreachable server (agent startup fails, tray/Close remain functional).
- No changes needed to `AgentHost`, `AgentHost.DisposeAsync`, or any Core project code — this feature is entirely additive within `NotificationAgent.Windows`.

## Non-goals / explicitly deferred

- Custom icon artwork/branding (uses `SystemIcons.Application` as a placeholder).
- CI/git-tag-driven versioning (manual `<Version>` bump in the csproj for now).
- Any additional tray functionality: no settings menu, no pause/resume, no notification history, no balloon-tip notifications beyond the tooltip failure flag.
- Any change to delivery semantics, `AgentHost`, or Core project code.
- `NotificationAgent.Windows.Tests` can no longer run (`dotnet test`) on a Linux dev machine once `<UseWindowsForms>true</UseWindowsForms>` is set — the whole assembly then requires the Windows-only `Microsoft.WindowsDesktop.App` runtime to execute, not just compile. `dotnet build` still works (covered by `EnableWindowsTargeting`). Verifying this project's tests (including the pre-existing ones unrelated to the tray icon) now requires a real Windows machine or a Windows CI runner. Accepted tradeoff, discovered during implementation — see the implementation plan's task ledger for the alternative considered (splitting into two projects).
