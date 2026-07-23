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
