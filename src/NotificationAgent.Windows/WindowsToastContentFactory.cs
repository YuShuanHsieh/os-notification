using CommunityToolkit.WinUI.Notifications;
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

        if (toast.ActionLabel is not null
            && ActionUrlPolicy.TryCreate(toast.ActionUrl, out var actionUri))
        {
            builder.AddButton(new ToastButton()
                .SetContent(toast.ActionLabel)
                .SetProtocolActivation(actionUri));
        }

        return builder.GetToastContent();
    }
}
