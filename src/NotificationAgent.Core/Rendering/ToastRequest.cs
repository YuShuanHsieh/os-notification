using NotificationAgent.Core.Models;

namespace NotificationAgent.Core.Rendering;

/// <summary>Renderer-ready toast. Sources lists every event this toast represents,
/// so the caller can ack each of them as submitted_to_windows.</summary>
public sealed record ToastRequest(
    string Title,
    string Message,
    string? Attribution,
    string? ImageUrl,
    string? ActionLabel,
    string? ActionUrl,
    IReadOnlyList<InboundNotification> Sources);

public interface IToastRenderer
{
    /// <summary>Submit the toast; returns the submission timestamp (toastSubmittedAt).</summary>
    ValueTask<DateTimeOffset> ShowAsync(ToastRequest toast, CancellationToken ct = default);
}
