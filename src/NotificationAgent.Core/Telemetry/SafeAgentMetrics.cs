namespace NotificationAgent.Core.Telemetry;

/// <summary>Exception-safe wrappers around every <see cref="IAgentMetrics"/> call site in
/// Core. A metrics-recording failure must never interrupt event processing, dedup,
/// aggregation, or acknowledgement publishing -- so every call into an <see
/// cref="IAgentMetrics"/> implementation from Core goes through one of these instead of
/// calling the interface directly. Exceptions are intentionally swallowed rather than
/// logged: Core stays logging-free by design (no <c>ILogger</c> is reliably available at
/// every one of these call sites), and re-throwing would defeat the entire point of the
/// guard.</summary>
internal static class SafeAgentMetrics
{
    public static void SafeRecordEventReceived(this IAgentMetrics metrics) =>
        Safe(metrics.RecordEventReceived);

    public static void SafeRecordEventDropped(this IAgentMetrics metrics, string reason) =>
        Safe(() => metrics.RecordEventDropped(reason));

    public static void SafeRecordRenderDuration(this IAgentMetrics metrics, double seconds) =>
        Safe(() => metrics.RecordRenderDuration(seconds));

    private static void Safe(Action recordAction)
    {
        try
        {
            recordAction();
        }
        catch
        {
            // Intentionally swallowed -- see type-level doc comment.
        }
    }
}
