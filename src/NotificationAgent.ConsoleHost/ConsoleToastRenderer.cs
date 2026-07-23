using NotificationAgent.Core.Rendering;

namespace NotificationAgent.ConsoleHost;

/// <summary>Dev stand-in for the Windows renderer: prints "toasts" to stdout.</summary>
public sealed class ConsoleToastRenderer : IToastRenderer
{
    public ValueTask<DateTimeOffset> ShowAsync(ToastRequest toast, CancellationToken ct = default)
    {
        Console.WriteLine($"[TOAST] {toast.Title}");
        Console.WriteLine($"        {toast.Message}");
        if (toast.Attribution is not null)
        {
            Console.WriteLine($"        — {toast.Attribution}");
        }

        if (toast.ImageUrl is not null)
        {
            Console.WriteLine($"        [image] {toast.ImageUrl}");
        }

        if (toast.ActionLabel is not null)
        {
            Console.WriteLine($"        [{toast.ActionLabel}] -> {toast.ActionUrl}");
        }

        return ValueTask.FromResult(DateTimeOffset.UtcNow);
    }
}
