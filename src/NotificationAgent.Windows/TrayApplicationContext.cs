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
            // Reuses the icon already embedded via <ApplicationIcon> (Assets/app.ico) in the
            // .csproj, rather than duplicating it as a separate embedded resource — falls back
            // to the system placeholder only if extraction ever fails.
            Icon = Icon.ExtractAssociatedIcon(Application.ExecutablePath) ?? SystemIcons.Application,
            Text = BaseTooltip,
            ContextMenuStrip = menu,
            Visible = true,
        };

        // Defer starting the agent until the WinForms message loop is actually running:
        // Application.Run installs the UI SynchronizationContext before it starts pumping
        // messages, and a Forms Timer only ticks from within that running loop. Starting
        // the async chain from there (instead of directly in the constructor, which runs
        // before Application.Run is even called) lets StartAgentAsync's continuation
        // resume on the UI thread, so it can safely touch _notifyIcon on failure.
        var startTimer = new System.Windows.Forms.Timer { Interval = 1 };
        startTimer.Tick += (_, _) =>
        {
            startTimer.Stop();
            startTimer.Dispose();
            _ = StartAgentAsync(startAgent);
        };
        startTimer.Start();
    }

    private async Task StartAgentAsync(Func<CancellationToken, Task<AgentHost>> startAgent)
    {
        try
        {
            _host = await startAgent(CancellationToken.None);
        }
        catch
        {
            // Best-effort: a failed start must not crash the tray (design: system tray icon).
            // No ConfigureAwait(false) above: the continuation must resume on the UI thread
            // (see the timer comment above) so this write is thread-safe.
            _notifyIcon.Text = $"{BaseTooltip} (agent failed to start)";
        }
    }

    private async Task OnCloseClickedAsync()
    {
        _notifyIcon.Visible = false;
        var host = _host;
        await TrayShutdown.CloseAsync(
            host is null ? () => Task.CompletedTask : () => host.DisposeAsync().AsTask(),
            CloseTimeout);
        ExitThread();
        Environment.Exit(0);
    }
}
