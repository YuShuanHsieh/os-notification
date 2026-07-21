using CommunityToolkit.WinUI.Notifications;
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
