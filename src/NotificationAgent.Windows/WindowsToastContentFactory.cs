using Microsoft.Toolkit.Uwp.Notifications;
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

        if (HttpsUrlPolicy.TryCreate(toast.ImageUrl, out var imageUri))
            builder.AddAppLogoOverride(imageUri, ToastGenericAppLogoCrop.Circle);

        if (!string.IsNullOrWhiteSpace(toast.ActionLabel)
            && HttpsUrlPolicy.TryCreate(toast.ActionUrl, out var actionUri))
        {
            builder.AddButton(new ToastButton()
                .SetContent(toast.ActionLabel)
                .SetProtocolActivation(actionUri));
        }

        return builder.GetToastContent();
    }
}
