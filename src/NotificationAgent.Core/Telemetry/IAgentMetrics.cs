namespace NotificationAgent.Core.Telemetry;

/// <summary>Records the three critical operational metrics for this agent. Implementations
/// (e.g. an OpenTelemetry-backed one, supplied by the Windows head) must never throw --
/// a metrics-recording failure must never be allowed to interrupt event processing, dedup,
/// aggregation, or acknowledgement publishing. Core itself never trusts implementations to
/// honor that contract: every call site wraps calls into this interface defensively (see
/// <c>EventPipeline</c>/<c>Aggregator</c>/<c>AgentPipelineFactory</c>).</summary>
public interface IAgentMetrics
{
    /// <summary>One valid, first-seen event was accepted into the pipeline (fires at the
    /// same point the observed_by_agent acknowledgement does).</summary>
    void RecordEventReceived();

    /// <summary>One event was dropped. <paramref name="reason"/> is "queue_full" or
    /// "bucket_overflow" (matching the existing DroppedQueueFull/DroppedBucketOverflow
    /// counters this call sits alongside).</summary>
    void RecordEventDropped(string reason);

    /// <summary>One event's end-to-end agent processing latency, in seconds
    /// (toastSubmittedAt - agentReceivedAt for that specific event), fires once per
    /// event represented in a successfully rendered toast (once per event in a batch,
    /// not once per toast).</summary>
    void RecordRenderDuration(double seconds);
}

/// <summary>No-op default. Used by the console host (which never configures metrics) and
/// as the Windows head's own fallback if OpenTelemetry setup fails, so metrics-recording
/// call sites in Core never need a null check.</summary>
public sealed class NullAgentMetrics : IAgentMetrics
{
    public static readonly NullAgentMetrics Instance = new();

    private NullAgentMetrics()
    {
    }

    public void RecordEventReceived()
    {
    }

    public void RecordEventDropped(string reason)
    {
    }

    public void RecordRenderDuration(double seconds)
    {
    }
}
