using System.Globalization;
using System.Text;
using System.Text.Json;
using NATS.Client.Core;

// Usage:
//   TestPublisher <userId> [title] [message] [priority] [count] [imageUrl]              (legacy)
//   TestPublisher <userId> --scenario presence|invoice|progress|batch|dedup [--flags]
//   TestPublisher <userId> [--flags]
// Flags: --title --message --secondary --type --priority --count --image-url
//        --image-shape circle|square --action-label --action-url --agg-key
//        --dedup-key --replaceable --delay-ms
if (args.Length == 0 || args[0].StartsWith("--", StringComparison.Ordinal))
{
    return Usage("first argument must be <userId>");
}

var spec = PublishSpec.Defaults(args[0]);
var rest = args[1..];
var positionals = rest.TakeWhile(a => !a.StartsWith("--", StringComparison.Ordinal)).ToArray();
var flags = rest[positionals.Length..];

string? scenario = null;
for (var i = 0; i < flags.Length; i++)
{
    if (flags[i] == "--scenario")
    {
        if (i + 1 >= flags.Length)
        {
            return Usage("--scenario needs a value");
        }

        scenario = flags[i + 1];
    }
}

if (scenario is not null && positionals.Length > 0)
{
    return Usage("--scenario cannot be combined with legacy positional arguments");
}

if (scenario is not null && !PublishSpec.ApplyScenario(spec, scenario))
{
    return Usage($"unknown scenario '{scenario}' (presence|invoice|progress|batch|dedup)");
}

// Legacy positionals: [title] [message] [priority] [count] [imageUrl]
if (positionals.Length > 5)
{
    return Usage("too many legacy positional arguments");
}

if (positionals.Length > 0)
{
    spec.Title = positionals[0];
}

if (positionals.Length > 1)
{
    spec.Message = positionals[1];
}

if (positionals.Length > 2)
{
    spec.Priority = positionals[2];
}

if (positionals.Length > 3)
{
    if (!int.TryParse(positionals[3], NumberStyles.Integer, CultureInfo.InvariantCulture, out var c) || c < 1)
    {
        return Usage("count must be a positive integer");
    }

    spec.Count = c;
}

if (positionals.Length > 4)
{
    spec.ImageUrl = positionals[4];
}

// Named flags override everything.
for (var i = 0; i < flags.Length; i++)
{
    string Next()
    {
        if (i + 1 >= flags.Length)
        {
            throw new ArgumentException($"{flags[i]} needs a value");
        }

        return flags[++i];
    }

    try
    {
        switch (flags[i])
        {
            case "--scenario":
                i++;
                break; // already applied
            case "--title":
                spec.Title = Next();
                break;
            case "--message":
                spec.Message = Next();
                spec.Messages = null;
                break;
            case "--secondary":
                spec.Secondary = Next();
                break;
            case "--type":
                spec.Type = Next();
                break;
            case "--priority":
                spec.Priority = Next();
                break;
            case "--count":
                if (!int.TryParse(Next(), NumberStyles.Integer, CultureInfo.InvariantCulture, out var count) || count < 1)
                {
                    return Usage("--count must be a positive integer");
                }

                spec.Count = count;
                spec.Messages = null;
                break;
            case "--image-url":
                spec.ImageUrl = Next();
                break;
            case "--image-shape":
                var shape = Next();
                if (shape is not ("circle" or "square"))
                {
                    return Usage("--image-shape must be circle or square");
                }

                spec.ImageShape = shape;
                break;
            case "--action-label":
                spec.ActionLabel = Next();
                break;
            case "--action-url":
                spec.ActionUrl = Next();
                break;
            case "--agg-key":
                spec.AggKey = Next();
                break;
            case "--dedup-key":
                spec.DedupKey = Next();
                break;
            case "--replaceable":
                spec.Replaceable = true;
                break;
            case "--delay-ms":
                if (!int.TryParse(Next(), NumberStyles.Integer, CultureInfo.InvariantCulture, out var delay) || delay < 0)
                {
                    return Usage("--delay-ms must be a non-negative integer");
                }

                spec.DelayMs = delay;
                break;
            default:
                return Usage($"unknown flag '{flags[i]}'");
        }
    }
    catch (ArgumentException e)
    {
        return Usage(e.Message);
    }
}

var natsUrl = Environment.GetEnvironmentVariable("NOTIFY_NATS_URL") ?? "nats://127.0.0.1:4222";
var subject = $"notify.user.{spec.UserId}.desktop";
var ackSubject = Environment.GetEnvironmentVariable("NOTIFY_ACK_SUBJECT") ?? "notify.ack.desktop";

if (spec.Expect is not null)
{
    Console.WriteLine($"EXPECT: {spec.Expect}");
}

await using var nats = new NatsConnection(new NatsOpts { Url = natsUrl });

using var ackCts = new CancellationTokenSource();
var ackWatcher = Task.Run(async () =>
{
    try
    {
        await foreach (var msg in nats.SubscribeAsync<byte[]>(ackSubject, cancellationToken: ackCts.Token))
        {
            Console.WriteLine($"[ACK] {Encoding.UTF8.GetString(msg.Data!)}");
        }
    }
    catch (OperationCanceledException)
    {
    }
});
await Task.Delay(300); // let the ack subscription settle

var messages = spec.Messages ?? Enumerable.Repeat(spec.Message, spec.Count).ToList();
for (var i = 0; i < messages.Count; i++)
{
    var eventId = $"evt-{Guid.NewGuid():N}";
    var content = new Dictionary<string, object>
    {
        ["title"] = spec.Title,
        ["message"] = messages[i],
    };
    if (spec.Secondary is not null)
    {
        content["secondaryText"] = spec.Secondary;
    }

    if (spec.ImageUrl is not null)
    {
        content["image"] = new { url = spec.ImageUrl, shape = spec.ImageShape };
    }

    var payload = new Dictionary<string, object>
    {
        ["schemaVersion"] = spec.ImageUrl is not null ? "1.1" : "1.0",
        ["eventId"] = eventId,
        ["notificationType"] = spec.Type,
        ["target"] = new { userId = spec.UserId },
        ["content"] = content,
    };
    if (spec.ActionLabel is not null && spec.ActionUrl is not null)
    {
        payload["action"] = new { label = spec.ActionLabel, url = spec.ActionUrl };
    }

    payload["classification"] = new
    {
        priority = spec.Priority,
        aggregationKey = spec.AggKey ?? spec.Type,
        deduplicationKey = spec.DedupKey ?? eventId,
        replaceable = spec.Replaceable,
    };
    payload["timestamps"] = new
    {
        producerCreatedAt = DateTimeOffset.UtcNow,
        serverPublishedAt = DateTimeOffset.UtcNow,
    };

    await nats.PublishAsync(subject, JsonSerializer.SerializeToUtf8Bytes(payload));
    Console.WriteLine($"[PUB] {eventId} -> {subject} (priority={spec.Priority})");
    if (spec.DelayMs > 0 && i < messages.Count - 1)
    {
        await Task.Delay(spec.DelayMs);
    }
}

await Task.Delay(TimeSpan.FromSeconds(12)); // outlast the 10s normal batch window
ackCts.Cancel();
await ackWatcher;
return 0;

static int Usage(string error)
{
    Console.Error.WriteLine($"error: {error}");
    Console.Error.WriteLine("usage: TestPublisher <userId> [title] [message] [priority] [count] [imageUrl]");
    Console.Error.WriteLine("       TestPublisher <userId> --scenario presence|invoice|progress|batch|dedup [--flags]");
    Console.Error.WriteLine("       TestPublisher <userId> [--flags]");
    Console.Error.WriteLine("flags: --title --message --secondary --type --priority --count --image-url");
    Console.Error.WriteLine("       --image-shape circle|square --action-label --action-url --agg-key");
    Console.Error.WriteLine("       --dedup-key --replaceable --delay-ms");
    return 2;
}

internal sealed class PublishSpec
{
    public required string UserId { get; set; }

    public string Title { get; set; } = string.Empty;

    public string Message { get; set; } = string.Empty;

    public string? Secondary { get; set; }

    public string Type { get; set; } = string.Empty;

    public string Priority { get; set; } = string.Empty;

    public int Count { get; set; }

    public string? ImageUrl { get; set; }

    public string ImageShape { get; set; } = "circle";

    public string? ActionLabel { get; set; }

    public string? ActionUrl { get; set; }

    public string? AggKey { get; set; }

    public string? DedupKey { get; set; }

    public bool Replaceable { get; set; }

    public int DelayMs { get; set; }

    public List<string>? Messages { get; set; }

    public string? Expect { get; set; }

    /// <summary>Today's legacy defaults — byte-compatible with the pre-scenario tool.</summary>
    public static PublishSpec Defaults(string userId) => new()
    {
        UserId = userId,
        Title = "Invoice ready",
        Message = "Invoice INV-8492 is ready for review.",
        Secondary = "TestPublisher",
        Type = "billing.invoice.ready",
        Priority = "normal",
        Count = 1,
        ActionLabel = "View",
        ActionUrl = "https://app.example.com/invoices/8492",
    };

    public static bool ApplyScenario(PublishSpec s, string name)
    {
        switch (name)
        {
            case "presence":
                s.Title = "Tony Redmond";
                s.Message = "is now available";
                s.Secondary = "Microsoft Teams";
                s.Type = "presence.available";
                s.Priority = "critical";
                s.ImageUrl = "https://i.pravatar.cc/96?u=tony";
                s.ImageShape = "circle";
                s.ActionLabel = "Open chat";
                s.ActionUrl = "https://teams.example.com/chat/tony";
                s.Expect = "1 avatar toast, 2 acks";
                return true;
            case "invoice":
                s.Title = "Invoice ready";
                s.Message = "Invoice INV-8492 is ready for review.";
                s.Secondary = "Contoso Billing";
                s.Type = "billing.invoice.ready";
                s.Priority = "normal";
                s.ActionLabel = "View invoice";
                s.ActionUrl = "https://app.example.com/invoices/8492";
                s.Expect = "1 toast after ~10s, 2 acks";
                return true;
            case "progress":
                s.Title = "Export job";
                s.Type = "job.progress";
                s.AggKey = "job.progress";
                s.Priority = "normal";
                s.Replaceable = true;
                s.DelayMs = 100;
                s.Messages = ["10%", "60%", "90%"];
                s.Expect = "after ~10s ONE toast showing 90%";
                return true;
            case "batch":
                s.Title = "Batch demo";
                s.AggKey = "demo.batch";
                s.Priority = "normal";
                s.DelayMs = 100;
                s.Messages = ["first", "second", "third"];
                s.Expect = "ONE '3 notifications — demo.batch' toast, 6 acks sharing one toastSubmittedAt";
                return true;
            case "dedup":
                s.Priority = "critical";
                s.DedupKey = "dedup-demo";
                s.Count = 3;
                s.Expect = "ONE toast, exactly 2 acks (duplicates dropped)";
                return true;
            default:
                return false;
        }
    }
}
