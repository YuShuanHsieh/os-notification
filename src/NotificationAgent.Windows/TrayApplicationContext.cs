// src/NotificationAgent.Windows/TrayApplicationContext.cs
using Microsoft.Extensions.Logging;
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
    private readonly ILogger? _logger;
    private volatile AgentHost? _host;

    public TrayApplicationContext(Func<CancellationToken, Task<AgentHost>> startAgent, ILogger? logger = null)
    {
        _logger = logger;
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
            Icon = TryExtractApplicationIcon(Application.ExecutablePath) ?? SystemIcons.Application,
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

    // Icon.ExtractAssociatedIcon can throw (not just return null) in some conditions -- e.g.
    // path resolution failing before the extraction itself runs. A failed icon load must
    // never crash startup, so fall back to SystemIcons.Application on any exception, not
    // only a null result. Internal (rather than inlined) and parameterized so it's directly
    // unit testable with a deliberately bad path, without depending on WinForms application
    // state.
    internal static Icon? TryExtractApplicationIcon(string executablePath)
    {
        try
        {
            return Icon.ExtractAssociatedIcon(executablePath);
        }
        catch (Exception)
        {
            return null;
        }
    }

    private async Task StartAgentAsync(Func<CancellationToken, Task<AgentHost>> startAgent)
    {
        try
        {
            _host = await startAgent(CancellationToken.None);
            _logger?.AgentStarted(_host.Subject);
        }
        catch (Exception ex)
        {
            // Best-effort: a failed start must not crash the tray (design: system tray icon).
            // No ConfigureAwait(false) above: the continuation must resume on the UI thread
            // (see the timer comment above) so this write is thread-safe.
            _logger?.AgentStartFailed(ex);
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
