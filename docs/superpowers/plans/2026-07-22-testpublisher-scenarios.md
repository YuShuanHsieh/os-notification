# TestPublisher Scenario Presets Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `docs/superpowers/specs/2026-07-22-testpublisher-scenarios-design.md`: scenario presets (`presence|invoice|progress|batch|dedup`) with EXPECT hints plus full named-flag overrides in `tools/TestPublisher`, legacy positional form preserved byte-for-byte.

**Architecture:** Single-file rewrite of `tools/TestPublisher/Program.cs`: a `PublishSpec` holding every field with today's defaults → precedence chain (defaults → scenario → legacy positionals → flags, with scenario × positionals mutually exclusive) → the existing NATS publish/ack-watch loop generalized over a per-event message list.

**Tech Stack:** existing (.NET 8, NATS.Net). No new dependencies.

## Global Constraints

- Legacy invocation `<userId> [title] [message] [priority] [count] [imageUrl]` produces byte-identical payloads to today (same defaults: secondaryText "TestPublisher", type "billing.invoice.ready", action View/invoices-8492).
- Payload rules unchanged: `schemaVersion` `"1.1"` iff image present; content via `Dictionary<string, object>` (absent optionals omitted, never null); action only when both label+url set; camelCase verbatim; unique `eventId` per event; `deduplicationKey` = eventId unless pinned.
- `--scenario` + legacy positionals (beyond userId) → usage error exit 2; unknown flag/scenario or bad numeric → usage error exit 2 (no unhandled exceptions — this also fixes the old `int.Parse` crash on bad count).
- Scenario payloads and EXPECT lines exactly per the spec's table.
- Work on branch `rust-agent` in worktree `/home/cjamhe01385/os-notification/.worktrees/rust-agent`. `export PATH="$HOME/.dotnet:$PATH"` (and `$HOME/.cargo/bin` for the Rust console head). NATS live on 4222 (not ours to manage). Kill agents with `kill -INT`; no orphans.

---

### Task 1: Rewrite TestPublisher with scenarios + flags, verify the live matrix

**Files:**
- Modify: `tools/TestPublisher/Program.cs` (full replacement below)

**Interfaces:**
- Consumes: NATS.Client.Core (unchanged usage).
- Produces: the CLI per the spec. No library surface.

- [ ] **Step 1: Replace `tools/TestPublisher/Program.cs` with:**

```csharp
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

if (args.Length == 0 || args[0].StartsWith("--"))
    return Usage("first argument must be <userId>");

var spec = PublishSpec.Defaults(args[0]);
var rest = args[1..];
var positionals = rest.TakeWhile(a => !a.StartsWith("--")).ToArray();
var flags = rest[positionals.Length..];

string? scenario = null;
for (var i = 0; i < flags.Length; i++)
    if (flags[i] == "--scenario")
    {
        if (i + 1 >= flags.Length) return Usage("--scenario needs a value");
        scenario = flags[i + 1];
    }

if (scenario is not null && positionals.Length > 0)
    return Usage("--scenario cannot be combined with legacy positional arguments");
if (scenario is not null && !PublishSpec.ApplyScenario(spec, scenario))
    return Usage($"unknown scenario '{scenario}' (presence|invoice|progress|batch|dedup)");

// Legacy positionals: [title] [message] [priority] [count] [imageUrl]
if (positionals.Length > 0) spec.Title = positionals[0];
if (positionals.Length > 1) spec.Message = positionals[1];
if (positionals.Length > 2) spec.Priority = positionals[2];
if (positionals.Length > 3)
{
    if (!int.TryParse(positionals[3], out var c) || c < 1) return Usage("count must be a positive integer");
    spec.Count = c;
}
if (positionals.Length > 4) spec.ImageUrl = positionals[4];

// Named flags override everything.
for (var i = 0; i < flags.Length; i++)
{
    string Next()
    {
        if (i + 1 >= flags.Length) throw new ArgumentException($"{flags[i]} needs a value");
        return flags[++i];
    }
    try
    {
        switch (flags[i])
        {
            case "--scenario": i++; break; // already applied
            case "--title": spec.Title = Next(); break;
            case "--message": spec.Message = Next(); spec.Messages = null; break;
            case "--secondary": spec.Secondary = Next(); break;
            case "--type": spec.Type = Next(); break;
            case "--priority": spec.Priority = Next(); break;
            case "--count":
                if (!int.TryParse(Next(), out var count) || count < 1) return Usage("--count must be a positive integer");
                spec.Count = count; break;
            case "--image-url": spec.ImageUrl = Next(); break;
            case "--image-shape":
                var shape = Next();
                if (shape is not ("circle" or "square")) return Usage("--image-shape must be circle or square");
                spec.ImageShape = shape; break;
            case "--action-label": spec.ActionLabel = Next(); break;
            case "--action-url": spec.ActionUrl = Next(); break;
            case "--agg-key": spec.AggKey = Next(); break;
            case "--dedup-key": spec.DedupKey = Next(); break;
            case "--replaceable": spec.Replaceable = true; break;
            case "--delay-ms":
                if (!int.TryParse(Next(), out var delay) || delay < 0) return Usage("--delay-ms must be a non-negative integer");
                spec.DelayMs = delay; break;
            default: return Usage($"unknown flag '{flags[i]}'");
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

if (spec.Expect is not null) Console.WriteLine($"EXPECT: {spec.Expect}");

await using var nats = new NatsConnection(new NatsOpts { Url = natsUrl });

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

var messages = spec.Messages ?? Enumerable.Repeat(spec.Message, spec.Count).ToList();
for (var i = 0; i < messages.Count; i++)
{
    var eventId = $"evt-{Guid.NewGuid():N}";
    var content = new Dictionary<string, object>
    {
        ["title"] = spec.Title,
        ["message"] = messages[i],
    };
    if (spec.Secondary is not null) content["secondaryText"] = spec.Secondary;
    if (spec.ImageUrl is not null) content["image"] = new { url = spec.ImageUrl, shape = spec.ImageShape };

    var payload = new Dictionary<string, object>
    {
        ["schemaVersion"] = spec.ImageUrl is not null ? "1.1" : "1.0",
        ["eventId"] = eventId,
        ["notificationType"] = spec.Type,
        ["target"] = new { userId = spec.UserId },
        ["content"] = content,
        ["classification"] = new
        {
            priority = spec.Priority,
            aggregationKey = spec.AggKey ?? spec.Type,
            deduplicationKey = spec.DedupKey ?? eventId,
            replaceable = spec.Replaceable,
        },
        ["timestamps"] = new
        {
            producerCreatedAt = DateTimeOffset.UtcNow,
            serverPublishedAt = DateTimeOffset.UtcNow,
        },
    };
    if (spec.ActionLabel is not null && spec.ActionUrl is not null)
        payload["action"] = new { label = spec.ActionLabel, url = spec.ActionUrl };

    await nats.PublishAsync(subject, JsonSerializer.SerializeToUtf8Bytes(payload));
    Console.WriteLine($"[PUB] {eventId} -> {subject} (priority={spec.Priority})");
    if (spec.DelayMs > 0 && i < messages.Count - 1)
        await Task.Delay(spec.DelayMs);
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

class PublishSpec
{
    public required string UserId { get; set; }
    public string Title { get; set; } = "";
    public string Message { get; set; } = "";
    public string? Secondary { get; set; }
    public string Type { get; set; } = "";
    public string Priority { get; set; } = "";
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
                s.Title = "Tony Redmond"; s.Message = "is now available";
                s.Secondary = "Microsoft Teams"; s.Type = "presence.available";
                s.Priority = "critical";
                s.ImageUrl = "https://i.pravatar.cc/96?u=tony"; s.ImageShape = "circle";
                s.ActionLabel = "Open chat"; s.ActionUrl = "https://teams.example.com/chat/tony";
                s.Expect = "1 avatar toast, 2 acks";
                return true;
            case "invoice":
                s.Title = "Invoice ready"; s.Message = "Invoice INV-8492 is ready for review.";
                s.Secondary = "Contoso Billing"; s.Type = "billing.invoice.ready";
                s.Priority = "normal";
                s.ActionLabel = "View invoice"; s.ActionUrl = "https://app.example.com/invoices/8492";
                s.Expect = "1 toast after ~10s, 2 acks";
                return true;
            case "progress":
                s.Title = "Export job"; s.Type = "job.progress"; s.AggKey = "job.progress";
                s.Priority = "normal"; s.Replaceable = true; s.DelayMs = 100;
                s.Messages = ["10%", "60%", "90%"];
                s.Expect = "after ~10s ONE toast showing 90%";
                return true;
            case "batch":
                s.Title = "Batch demo"; s.AggKey = "demo.batch"; s.Priority = "normal";
                s.DelayMs = 100; s.Messages = ["first", "second", "third"];
                s.Expect = "ONE '3 notifications — demo.batch' toast, 6 acks sharing one toastSubmittedAt";
                return true;
            case "dedup":
                s.Priority = "critical"; s.DedupKey = "dedup-demo"; s.Count = 3;
                s.Expect = "ONE toast, exactly 2 acks (duplicates dropped)";
                return true;
            default:
                return false;
        }
    }
}
```

Note one deliberate legacy-shape change: `action` moves from "always present" to "present when label+url set" — with the defaults both set, every legacy invocation still emits it, so legacy payloads are unchanged. Payload/`content` become `Dictionary` (insertion-ordered by System.Text.Json), keys in the same order as today.

- [ ] **Step 2: Build** — `export PATH="$HOME/.dotnet:$PATH" && dotnet build tools/TestPublisher 2>&1 | tail -3` → 0 warnings, 0 errors.

- [ ] **Step 3: Live verification matrix.** Start the Rust console agent once:

```bash
cd /home/cjamhe01385/os-notification/.worktrees/rust-agent/rust
MATRIXLOG=$(mktemp /tmp/tp-matrix.XXXX.log)
NOTIFY_USER_ID=u_matrix cargo run -p notify-agent-console > "$MATRIXLOG" 2>&1 &
AGENT_PID=$!
sleep 3
cd .. && export PATH="$HOME/.dotnet:$PATH"
```

Then run, capturing output of each (`dotnet run --project tools/TestPublisher -- u_matrix ...`):
1. `--scenario presence` → EXPECT line printed; agent log gains `[TOAST] Tony Redmond` with `[image] https://i.pravatar.cc/96?u=tony (circle)`; 2 `[ACK]`s.
2. `--scenario invoice` → after ~10s one `[TOAST] Invoice ready` (attribution `— Contoso Billing`), 2 acks.
3. `--scenario progress` → after ~10s ONE toast whose message is `90%` (single source — not "3 notifications"), 2 acks for the surviving event... **correction**: all three events are distinct dedup keys, so 3 observed acks; the replaceable bucket keeps only the last → 1 submitted ack. Expected acks: 3 observed + 1 submitted. Verify exactly that and note it in the report (the spec's EXPECT line stays as-is on screen; your report records the precise ack counts).
4. `--scenario batch` → ONE `[TOAST] 3 notifications — demo.batch`, 3 observed + 3 submitted acks sharing one `toastSubmittedAt`.
5. `--scenario dedup` → ONE `[TOAST]`, exactly 2 acks total.
6. Legacy back-compat: `u_matrix "Invoice ready" "Legacy check" critical` → behaves exactly as before (1 toast, 2 acks, no EXPECT line).
7. Error paths: `u_matrix "Title" --scenario batch` → usage error exit 2; `u_matrix --scenario nope` → usage error exit 2; `u_matrix --count abc` → usage error exit 2 (check `echo $?`).

Then `kill -INT $AGENT_PID; sleep 2; cat "$MATRIXLOG"` and confirm no orphans (`pgrep -f notify-agent-console` empty).

- [ ] **Step 4: Commit**

```bash
git add tools/TestPublisher
git commit -m "feat(tools): TestPublisher scenario presets and named flags for schema-1.1 use cases"
```

## Spec coverage map

| Spec section | Where |
|---|---|
| CLI grammar + precedence + exclusivity + exit codes | Step 1 parsing block |
| 5 scenarios + EXPECT lines | `ApplyScenario` |
| Named flags | flag loop |
| Payload rules unchanged / legacy byte-compat | Defaults + dictionary build + Step 3.6 |
| Verification matrix | Step 3 |
