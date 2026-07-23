namespace NotificationAgent.Windows;

/// <summary>Graceful-then-timeout shutdown race, extracted so it's unit-testable without any
/// WinForms/UI involvement (design: system tray icon). Task.WhenAny does not observe or rethrow
/// an exception from the losing/faulted task, so a failing disposeAsync is swallowed by design —
/// Close must never fail to close.</summary>
internal static class TrayShutdown
{
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
}
