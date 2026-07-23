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
