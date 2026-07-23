using NotificationAgent.Core.Models;

namespace NotificationAgent.Core.Rendering;

public static class ToastContentFactory
{
    public const int MaxTitleGraphemes = 120;
    public const int MaxMessageGraphemes = 500;

    public static ToastRequest FromSingle(InboundNotification n) =>
        new(
            GraphemeText.Truncate(n.Title, MaxTitleGraphemes),
            GraphemeText.Truncate(n.Message, MaxMessageGraphemes),
            n.SecondaryText,
            n.ImageUrl,
            n.ActionLabel,
            n.ActionUrl,
            new[] { n });

    /// <summary>Builds one summary toast from a bucket of events. The batch must be in
    /// arrival order — the last element is treated as the latest event and supplies the
    /// message, attribution, and action. Callers append in observed arrival order; under
    /// concurrent intake workers this order is approximate, so "latest" is best-effort.</summary>
    public static ToastRequest FromBatch(IReadOnlyList<InboundNotification> batch)
    {
        if (batch.Count == 0)
        {
            throw new ArgumentException("batch must not be empty", nameof(batch));
        }

        if (batch.Count == 1)
        {
            return FromSingle(batch[0]);
        }

        var latest = batch[^1];
        return new ToastRequest(
            GraphemeText.Truncate($"{batch.Count} notifications — {latest.AggregationKey}", MaxTitleGraphemes),
            GraphemeText.Truncate($"Latest: {latest.Message}", MaxMessageGraphemes),
            latest.SecondaryText,
            latest.ImageUrl,
            latest.ActionLabel,
            latest.ActionUrl,
            batch.ToArray());
    }
}
