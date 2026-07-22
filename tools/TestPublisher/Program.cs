using System.Text;
using System.Text.Json;
using NATS.Client.Core;

// Usage: dotnet run --project tools/TestPublisher -- <userId> [title] [message] [priority] [count] [imageUrl]
var userId = args.Length > 0 ? args[0] : "u_demo";
var title = args.Length > 1 ? args[1] : "Invoice ready";
var message = args.Length > 2 ? args[2] : "Invoice INV-8492 is ready for review.";
var priority = args.Length > 3 ? args[3] : "normal";
var count = args.Length > 4 ? int.Parse(args[4]) : 1;
var imageUrl = args.Length > 5 ? args[5] : null;

var natsUrl = Environment.GetEnvironmentVariable("NOTIFY_NATS_URL") ?? "nats://127.0.0.1:4222";
var subject = $"notify.user.{userId}.desktop";
var ackSubject = Environment.GetEnvironmentVariable("NOTIFY_ACK_SUBJECT") ?? "notify.ack.desktop";

await using var nats = new NatsConnection(new NatsOpts { Url = natsUrl });

// Watch for acks in the background while we publish.
using var ackCts = new CancellationTokenSource();
var ackWatcher = Task.Run(async () =>
{
    try
    {
        await foreach (var msg in nats.SubscribeAsync<byte[]>(ackSubject, cancellationToken: ackCts.Token))
            Console.WriteLine($"[ACK] {Encoding.UTF8.GetString(msg.Data!)}");
    }
    catch (OperationCanceledException) { }
});
await Task.Delay(300); // let the ack subscription settle

for (var i = 0; i < count; i++)
{
    var eventId = $"evt-{Guid.NewGuid():N}";
    var content = new Dictionary<string, object>
    {
        ["title"] = title,
        ["message"] = message,
        ["secondaryText"] = "TestPublisher",
    };
    if (imageUrl is not null)
        content["image"] = new { url = imageUrl, shape = "circle" };

    var payload = new
    {
        schemaVersion = imageUrl is not null ? "1.1" : "1.0",
        eventId,
        notificationType = "billing.invoice.ready",
        target = new { userId },
        content,
        action = new { label = "View", url = "https://app.example.com/invoices/8492" },
        classification = new
        {
            priority,
            aggregationKey = "billing.invoice.ready",
            deduplicationKey = eventId,
            replaceable = false,
        },
        timestamps = new
        {
            producerCreatedAt = DateTimeOffset.UtcNow,
            serverPublishedAt = DateTimeOffset.UtcNow,
        },
    };
    await nats.PublishAsync(subject, JsonSerializer.SerializeToUtf8Bytes(payload));
    Console.WriteLine($"[PUB] {eventId} -> {subject} (priority={priority})");
}

await Task.Delay(TimeSpan.FromSeconds(12)); // outlast the 10s normal batch window
ackCts.Cancel();
await ackWatcher;
