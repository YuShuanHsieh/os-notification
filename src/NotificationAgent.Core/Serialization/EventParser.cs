using System.Text.Json;
using NotificationAgent.Core.Models;

namespace NotificationAgent.Core.Serialization;

public sealed class EventParser
{
    public const int MaxPayloadBytes = 32 * 1024;
    public const int MaxJsonDepth = 16;

    private static readonly JsonSerializerOptions Options = new(JsonSerializerDefaults.Web)
    {
        MaxDepth = MaxJsonDepth,
    };

    public bool TryParse(
        ReadOnlySpan<byte> payload,
        DateTimeOffset receivedAt,
        out InboundNotification? notification,
        out string? error)
    {
        notification = null;
        if (payload.Length == 0)
        {
            error = "empty payload";
            return false;
        }

        if (payload.Length > MaxPayloadBytes)
        {
            error = $"payload {payload.Length} bytes exceeds {MaxPayloadBytes}";
            return false;
        }

        WireEvent? wire;
        try
        {
            wire = JsonSerializer.Deserialize<WireEvent>(payload, Options);
        }
        catch (JsonException ex)
        {
            error = $"invalid json: {ex.Message}";
            return false;
        }

        if (wire is null)
        {
            error = "payload is json null";
            return false;
        }

        if (string.IsNullOrWhiteSpace(wire.EventId))
        {
            error = "missing eventId";
            return false;
        }

        if (string.IsNullOrWhiteSpace(wire.Target?.UserId))
        {
            error = "missing target.userId";
            return false;
        }

        if (string.IsNullOrWhiteSpace(wire.Content?.Title))
        {
            error = "missing content.title";
            return false;
        }

        if (string.IsNullOrWhiteSpace(wire.Content?.Message))
        {
            error = "missing content.message";
            return false;
        }

        var type = string.IsNullOrWhiteSpace(wire.NotificationType) ? "unknown" : wire.NotificationType!;
        var priority = wire.Classification?.Priority?.ToLowerInvariant() switch
        {
            "critical" => EventPriority.Critical,
            "important" => EventPriority.Important,
            _ => EventPriority.Normal,
        };
        var aggregationKey = string.IsNullOrWhiteSpace(wire.Classification?.AggregationKey)
            ? type
            : wire.Classification.AggregationKey;
        var deduplicationKey = string.IsNullOrWhiteSpace(wire.Classification?.DeduplicationKey)
            ? wire.EventId
            : wire.Classification.DeduplicationKey;

        notification = new InboundNotification(
            EventId: wire.EventId!,
            UserId: wire.Target!.UserId!,
            Title: wire.Content!.Title!,
            Message: wire.Content.Message!,
            SecondaryText: wire.Content.SecondaryText,
            ImageUrl: wire.Content.Image?.Url,
            ActionLabel: wire.Action?.Label,
            ActionUrl: wire.Action?.Url,
            Priority: priority,
            AggregationKey: aggregationKey!,
            DeduplicationKey: deduplicationKey!,
            Replaceable: wire.Classification?.Replaceable ?? false,
            ProducerCreatedAt: wire.Timestamps?.ProducerCreatedAt,
            ServerPublishedAt: wire.Timestamps?.ServerPublishedAt,
            ReceivedAt: receivedAt);
        error = null;
        return true;
    }

    private sealed class WireEvent
    {
        public string? SchemaVersion
        {
            get; set;
        }

        public string? EventId
        {
            get; set;
        }

        public string? NotificationType
        {
            get; set;
        }

        public WireTarget? Target
        {
            get; set;
        }

        public WireContent? Content
        {
            get; set;
        }

        public WireAction? Action
        {
            get; set;
        }

        public WireClassification? Classification
        {
            get; set;
        }

        public WireTimestamps? Timestamps
        {
            get; set;
        }
    }

    private sealed class WireTarget
    {
        public string? UserId
        {
            get; set;
        }
    }

    private sealed class WireContent
    {
        public string? Title
        {
            get; set;
        }

        public string? Message
        {
            get; set;
        }

        public string? SecondaryText
        {
            get; set;
        }

        public WireImage? Image
        {
            get; set;
        }
    }

    private sealed class WireImage
    {
        public string? Url
        {
            get; set;
        }
    }

    private sealed class WireAction
    {
        public string? Label
        {
            get; set;
        }

        public string? Url
        {
            get; set;
        }
    }

    private sealed class WireClassification
    {
        public string? Priority
        {
            get; set;
        }

        public string? AggregationKey
        {
            get; set;
        }

        public string? DeduplicationKey
        {
            get; set;
        }

        public bool? Replaceable
        {
            get; set;
        }
    }

    private sealed class WireTimestamps
    {
        public DateTimeOffset? ProducerCreatedAt
        {
            get; set;
        }

        public DateTimeOffset? ServerPublishedAt
        {
            get; set;
        }
    }
}
