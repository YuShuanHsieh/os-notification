using System.Text;
using System.Text.Json;
using NotificationAgent.Core.Telemetry;
using Xunit;

namespace NotificationAgent.Core.Tests;

public class AckJsonTests
{
    [Fact]
    public void Serializes_submitted_ack_in_design_doc_shape()
    {
        var ack = new AckPayload("evt-12345", "d-456",
            DateTimeOffset.Parse("2026-07-15T08:30:00.190Z"),
            DateTimeOffset.Parse("2026-07-15T08:30:00.205Z"),
            AckStatuses.SubmittedToWindows);

        using var doc = JsonDocument.Parse(AckJson.Serialize(ack));
        var root = doc.RootElement;
        Assert.Equal("evt-12345", root.GetProperty("eventId").GetString());
        Assert.Equal("d-456", root.GetProperty("deviceId").GetString());
        Assert.Equal(
            DateTimeOffset.Parse("2026-07-15T08:30:00.190Z"),
            root.GetProperty("agentReceivedAt").GetDateTimeOffset());
        Assert.Equal(
            DateTimeOffset.Parse("2026-07-15T08:30:00.205Z"),
            root.GetProperty("toastSubmittedAt").GetDateTimeOffset());
        Assert.Equal("submitted_to_windows", root.GetProperty("status").GetString());
    }

    [Fact]
    public void Observed_ack_omits_null_toastSubmittedAt()
    {
        var ack = new AckPayload("evt-1", "d-1",
            DateTimeOffset.Parse("2026-07-15T08:30:00.190Z"), null, AckStatuses.ObservedByAgent);

        using var doc = JsonDocument.Parse(AckJson.Serialize(ack));
        Assert.Equal("observed_by_agent", doc.RootElement.GetProperty("status").GetString());
        Assert.False(doc.RootElement.TryGetProperty("toastSubmittedAt", out _));
    }
}
