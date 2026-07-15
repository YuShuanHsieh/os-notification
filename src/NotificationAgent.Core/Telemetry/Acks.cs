using System.Text.Json;
using System.Text.Json.Serialization;

namespace NotificationAgent.Core.Telemetry;

/// <summary>Exact status strings from design §10. The agent never emits
/// "published" or "unobserved" — those are backend-side classifications.</summary>
public static class AckStatuses
{
    public const string ObservedByAgent = "observed_by_agent";
    public const string SubmittedToWindows = "submitted_to_windows";
}

public sealed record AckPayload(
    string EventId,
    string DeviceId,
    DateTimeOffset AgentReceivedAt,
    DateTimeOffset? ToastSubmittedAt,
    string Status);

public interface ITelemetryPublisher
{
    ValueTask PublishAckAsync(AckPayload ack, CancellationToken ct = default);
}

public static class AckJson
{
    private static readonly JsonSerializerOptions Options = new(JsonSerializerDefaults.Web)
    {
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
    };

    public static byte[] Serialize(AckPayload ack) =>
        JsonSerializer.SerializeToUtf8Bytes(ack, Options);
}
