using Microsoft.Windows.AppNotifications;
using Microsoft.Windows.AppNotifications.Builder;
using NotificationAgent.Core.Rendering;

namespace NotificationAgent.Windows;

public sealed class WindowsToastRenderer : IToastRenderer
{
    public ValueTask<DateTimeOffset> ShowAsync(ToastRequest toast, CancellationToken ct = default)
    {
        // Text/limit budget (design §7): ≤3 text elements, 1 button, XML ≤5KB.
        // Title/message are already grapheme-truncated by ToastContentFactory.
        var builder = new AppNotificationBuilder()
            .AddText(toast.Title)
            .AddText(toast.Message);

        if (!string.IsNullOrEmpty(toast.Attribution))
            builder.SetAttributionText(toast.Attribution);

        if (toast.ActionLabel is not null
            && ActionUrlPolicy.TryCreate(toast.ActionUrl, out var actionUri))
        {
            builder.AddButton(
                new AppNotificationButton(toast.ActionLabel)
                    .SetInvokeUri(actionUri));
        }

        var notification = builder.BuildNotification();
        AppNotificationManager.Default.Show(notification);
        return ValueTask.FromResult(DateTimeOffset.UtcNow);
    }
}
