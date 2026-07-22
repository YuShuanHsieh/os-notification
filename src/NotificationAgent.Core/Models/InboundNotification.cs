namespace NotificationAgent.Core.Models;

public enum EventPriority { Normal, Important, Critical }

/// <summary>Normalized, validated notification event as consumed by the pipeline.</summary>
public sealed record InboundNotification(
    string EventId,
    string UserId,
    string Title,
    string Message,
    string? SecondaryText,
    string? ImageUrl,
    string? ActionLabel,
    string? ActionUrl,
    EventPriority Priority,
    string AggregationKey,
    string DeduplicationKey,
    bool Replaceable,
    DateTimeOffset? ProducerCreatedAt,
    DateTimeOffset? ServerPublishedAt,
    DateTimeOffset ReceivedAt);
