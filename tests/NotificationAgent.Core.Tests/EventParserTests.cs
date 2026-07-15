using System.Text;
using System.Text.Json;
using NotificationAgent.Core.Models;
using NotificationAgent.Core.Serialization;
using Xunit;

namespace NotificationAgent.Core.Tests;

public class EventParserTests
{
    private static readonly DateTimeOffset ReceivedAt = DateTimeOffset.Parse("2026-07-15T08:30:00.190Z");
    private readonly EventParser _parser = new();

    // Exact example payload from design doc §7
    private const string DocExample = """
        {
          "schemaVersion": "1.0",
          "eventId": "evt-12345",
          "notificationType": "billing.invoice.ready",
          "target": { "userId": "u_7f92a845" },
          "content": {
            "title": "Invoice ready",
            "message": "Invoice INV-8492 is ready for review.",
            "secondaryText": "Contoso Billing"
          },
          "action": { "label": "View invoice", "url": "https://app.example.com/invoices/8492" },
          "classification": {
            "priority": "normal",
            "aggregationKey": "billing.invoice.ready",
            "deduplicationKey": "invoice.ready:8492",
            "replaceable": false
          },
          "timestamps": {
            "producerCreatedAt": "2026-07-15T08:30:00.100Z",
            "serverPublishedAt": "2026-07-15T08:30:00.150Z"
          }
        }
        """;

    [Fact]
    public void Parses_doc_example_payload()
    {
        var ok = _parser.TryParse(Encoding.UTF8.GetBytes(DocExample), ReceivedAt, out var n, out var error);

        Assert.True(ok, error);
        Assert.NotNull(n);
        Assert.Equal("evt-12345", n!.EventId);
        Assert.Equal("u_7f92a845", n.UserId);
        Assert.Equal("Invoice ready", n.Title);
        Assert.Equal("Invoice INV-8492 is ready for review.", n.Message);
        Assert.Equal("Contoso Billing", n.SecondaryText);
        Assert.Equal("View invoice", n.ActionLabel);
        Assert.Equal("https://app.example.com/invoices/8492", n.ActionUrl);
        Assert.Equal(EventPriority.Normal, n.Priority);
        Assert.Equal("billing.invoice.ready", n.AggregationKey);
        Assert.Equal("invoice.ready:8492", n.DeduplicationKey);
        Assert.False(n.Replaceable);
        Assert.Equal(DateTimeOffset.Parse("2026-07-15T08:30:00.100Z"), n.ProducerCreatedAt);
        Assert.Equal(DateTimeOffset.Parse("2026-07-15T08:30:00.150Z"), n.ServerPublishedAt);
        Assert.Equal(ReceivedAt, n.ReceivedAt);
    }

    [Theory]
    [InlineData("critical", EventPriority.Critical)]
    [InlineData("important", EventPriority.Important)]
    [InlineData("normal", EventPriority.Normal)]
    [InlineData("garbage", EventPriority.Normal)] // unknown priority degrades to normal
    public void Maps_priority_strings(string priority, EventPriority expected)
    {
        var payload = Encoding.UTF8.GetBytes(
            "{\"eventId\":\"e1\",\"target\":{\"userId\":\"u1\"}," +
            "\"content\":{\"title\":\"t\",\"message\":\"m\"}," +
            "\"classification\":{\"priority\":\"" + priority + "\"}}");
        Assert.True(_parser.TryParse(payload, ReceivedAt, out var n, out _));
        Assert.Equal(expected, n!.Priority);
    }

    [Fact]
    public void Applies_defaults_for_missing_optional_fields()
    {
        var json = """
            {"eventId":"e1","notificationType":"a.b",
             "target":{"userId":"u1"},"content":{"title":"t","message":"m"}}
            """;
        Assert.True(_parser.TryParse(Encoding.UTF8.GetBytes(json), ReceivedAt, out var n, out _));
        Assert.Equal("e1", n!.DeduplicationKey);      // defaults to eventId
        Assert.Equal("a.b", n.AggregationKey);        // defaults to notificationType
        Assert.Equal(EventPriority.Normal, n.Priority);
        Assert.False(n.Replaceable);
        Assert.Null(n.ActionLabel);
        Assert.Null(n.ProducerCreatedAt);
    }

    [Theory]
    [InlineData("""{"target":{"userId":"u1"},"content":{"title":"t","message":"m"}}""", "eventId")]
    [InlineData("""{"eventId":"e1","content":{"title":"t","message":"m"}}""", "target.userId")]
    [InlineData("""{"eventId":"e1","target":{"userId":"u1"},"content":{"message":"m"}}""", "content.title")]
    [InlineData("""{"eventId":"e1","target":{"userId":"u1"},"content":{"title":"t"}}""", "content.message")]
    public void Rejects_missing_required_fields(string json, string expectedInError)
    {
        var ok = _parser.TryParse(Encoding.UTF8.GetBytes(json), ReceivedAt, out _, out var error);
        Assert.False(ok);
        Assert.Contains(expectedInError, error);
    }

    [Fact]
    public void Rejects_payload_over_32kb()
    {
        var big = new byte[EventParser.MaxPayloadBytes + 1];
        var ok = _parser.TryParse(big, ReceivedAt, out _, out var error);
        Assert.False(ok);
        Assert.Contains("exceeds", error);
    }

    [Fact]
    public void Rejects_json_deeper_than_16_levels()
    {
        var json = string.Concat(Enumerable.Repeat("""{"a":""", 20)) + "1"
                 + string.Concat(Enumerable.Repeat("}", 20));
        var ok = _parser.TryParse(Encoding.UTF8.GetBytes(json), ReceivedAt, out _, out var error);
        Assert.False(ok);
        Assert.Contains("json", error, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public void Rejects_malformed_and_empty_payloads()
    {
        Assert.False(_parser.TryParse(Encoding.UTF8.GetBytes("not json"), ReceivedAt, out _, out _));
        Assert.False(_parser.TryParse(ReadOnlySpan<byte>.Empty, ReceivedAt, out _, out _));
        Assert.False(_parser.TryParse(Encoding.UTF8.GetBytes("null"), ReceivedAt, out _, out _));
    }
}
