# Windows Desktop Notification Agent (C# / Core NATS) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Phase-1 POC of the per-user Windows desktop notification agent from `windows_desktop_notification_agent_core_nats_design.html`: subscribe to a user-scoped Core NATS subject, deduplicate/aggregate/prioritize events, render Windows 11 toast notifications, and publish acknowledgement telemetry.

**Architecture:** A cross-platform .NET 8 core library (`NotificationAgent.Core`) holds the whole pipeline — parser, dedup cache, aggregator, bounded-channel workers, telemetry — behind `IToastRenderer`/`IIdentityProvider` interfaces so every piece is unit-testable on this Linux machine. Two thin heads consume it: a console host (Linux dev/E2E smoke) and a Windows head (`net8.0-windows` + Windows App SDK toasts + WAM/MSAL identity) that is compiled and verified on a Windows 11 machine.

**Tech Stack:** .NET 8 (C# 12), NATS.Net v2 client, System.Text.Json, System.Threading.Channels, xUnit + Microsoft.Extensions.TimeProvider.Testing (FakeTimeProvider), Windows App SDK (`AppNotificationBuilder`), MSAL (`Microsoft.Identity.Client` + Broker/WAM), NATS server via Docker for integration tests.

## Scope note

The design doc describes two subsystems: the **notification service backend** (producer auth, payload normalization, publish, trace records, latency dashboards) and the **desktop agent**. This plan covers the **agent only**, plus a small `TestPublisher` dev tool standing in for the backend so the agent can be exercised end-to-end. Backend trace/latency aggregation, restricted-NATS-credential issuance, persistent dedup state (Phase 2), and memory guardrails (Phase 2) are out of scope; the ack payload emitted here carries every field the backend needs (§10 of the design).

## Global Constraints

Copied from the design doc; every task implicitly includes these.

- Delivery contract: **online-only, at-most-once, best-effort**; events may be dropped under overload, never buffered unboundedly.
- Subscription subject: `notify.user.{userId}.desktop` (plain Core NATS subscription, no JetStream).
- Bounded channel capacity: **500 events**; worker count: **2** (fixed).
- Maximum event payload: **32 KB**; maximum JSON depth: **16**.
- Active aggregate buckets: **100** maximum; overflow drops the event.
- Toast limits: title **120 grapheme clusters**, message **500 grapheme clusters**, 1 action button (product recommendation 1–2, max 5), tag/group max 64 chars.
- Ack statuses (exact strings): `observed_by_agent`, `submitted_to_windows`. Backend-only statuses `published`/`unobserved` are never emitted by the agent.
- Ack payload fields (camelCase JSON): `eventId`, `deviceId`, `agentReceivedAt`, `toastSubmittedAt`, `status`.
- Event payload schema: exactly the §7 JSON shape (`schemaVersion`, `eventId`, `notificationType`, `target.userId`, `content.{title,message,secondaryText}`, `action.{label,url}`, `classification.{priority,aggregationKey,deduplicationKey,replaceable}`, `timestamps.{producerCreatedAt,serverPublishedAt}`).
- Priorities: `critical` → render immediately; `important` → short batch window; `normal` → standard batch window; `replaceable: true` → keep only the latest value per aggregation key.
- Agent is per-user, non-elevated, one instance per Windows session.
- The Windows account name is never used as identity; identity comes from `IIdentityProvider` (MSAL on Windows, env vars in dev).
- Language/runtime: C# on .NET 8. NuGet packages: `NATS.Net` 2.x, `Microsoft.Extensions.TimeProvider.Testing` 8.x, `Microsoft.WindowsAppSDK` 1.5.x, `Microsoft.Identity.Client[.Broker]` 4.x.

## Environment facts (verified 2026-07-15 on this machine)

- No .NET SDK installed → Task 1 installs .NET 8 SDK into `~/.dotnet` via `dotnet-install.sh`. **Every task assumes** `export PATH="$HOME/.dotnet:$PATH"` in the shell.
- No `nats-server` binary; Docker 29.4.1 available → integration tests use `docker run -d --name nats-test -p 4222:4222 nats:2.10-alpine`.
- `/home/cjamhe01385/os-notification` is **not yet a git repository** → Task 1 runs `git init`.
- The Windows head (Task 9) cannot be run here; it is kept **out of the solution file** so `dotnet build`/`dotnet test` on Linux stay green. Final verification of Task 9 happens on a Windows 11 machine.

## File Structure

```
os-notification/
├── NotificationAgent.sln                     # Core, Core.Tests, ConsoleHost, TestPublisher (NOT the Windows head)
├── .gitignore                                # dotnet new gitignore
├── src/
│   ├── NotificationAgent.Core/               # net8.0, cross-platform, no Windows deps
│   │   ├── NotificationAgent.Core.csproj
│   │   ├── Models/InboundNotification.cs     # normalized event record + EventPriority enum
│   │   ├── Serialization/EventParser.cs      # bytes → InboundNotification; size/depth/required-field checks
│   │   ├── Dedup/DeduplicationCache.cs       # bounded, TTL, thread-safe
│   │   ├── Rendering/GraphemeText.cs         # grapheme-cluster truncation
│   │   ├── Rendering/ToastRequest.cs         # renderer input + IToastRenderer
│   │   ├── Rendering/ToastContentFactory.cs  # single/batch → ToastRequest with limits applied
│   │   ├── Aggregation/Aggregator.cs         # priority routing, batch windows, replaceable, 100-bucket cap
│   │   ├── Telemetry/Acks.cs                 # AckPayload, AckStatuses, ITelemetryPublisher, AckJson
│   │   ├── Pipeline/EventPipeline.cs         # bounded channel (500), 2 workers, AgentPipelineFactory
│   │   ├── Identity/IIdentityProvider.cs     # AgentIdentity, EnvironmentIdentityProvider
│   │   └── Hosting/AgentHost.cs              # AgentOptions, NatsTelemetryPublisher, AgentHost composition root
│   ├── NotificationAgent.ConsoleHost/        # net8.0 dev head (Linux): console "toasts"
│   │   ├── NotificationAgent.ConsoleHost.csproj
│   │   ├── ConsoleToastRenderer.cs
│   │   └── Program.cs
│   └── NotificationAgent.Windows/            # net8.0-windows head — NOT in the .sln; built on Windows
│       ├── NotificationAgent.Windows.csproj
│       ├── WindowsToastRenderer.cs
│       ├── MsalIdentityProvider.cs           # + DeviceIdStore
│       └── Program.cs                        # single-instance mutex, AppNotificationManager registration
├── tools/
│   └── TestPublisher/                        # net8.0 console: publish test events, print acks
│       ├── TestPublisher.csproj
│       └── Program.cs
├── tests/
│   └── NotificationAgent.Core.Tests/
│       ├── NotificationAgent.Core.Tests.csproj
│       ├── EventParserTests.cs
│       ├── GraphemeTextTests.cs
│       ├── ToastContentFactoryTests.cs
│       ├── DeduplicationCacheTests.cs
│       ├── AggregatorTests.cs
│       ├── AckJsonTests.cs
│       ├── EventPipelineTests.cs
│       └── NatsIntegrationTests.cs           # skips itself politely when no NATS on localhost:4222
└── docs/superpowers/plans/2026-07-15-windows-desktop-notification-agent.md
```

Interfaces flow: Task 1 defines `InboundNotification`; Task 2 builds `ToastRequest` from it; Tasks 3–5 are independent leaves; Task 6 wires 1–5 into the pipeline; Task 7 puts NATS + hosting around Task 6; Tasks 8–9 are heads/tools on top of Task 7.

---

### Task 1: Toolchain, solution scaffold, event model, and EventParser

**Files:**
- Create: `.gitignore`, `NotificationAgent.sln`
- Create: `src/NotificationAgent.Core/NotificationAgent.Core.csproj`
- Create: `src/NotificationAgent.Core/Models/InboundNotification.cs`
- Create: `src/NotificationAgent.Core/Serialization/EventParser.cs`
- Test: `tests/NotificationAgent.Core.Tests/NotificationAgent.Core.Tests.csproj`, `tests/NotificationAgent.Core.Tests/EventParserTests.cs`

**Interfaces:**
- Consumes: nothing (first task).
- Produces: `enum EventPriority { Normal, Important, Critical }`; `record InboundNotification(string EventId, string UserId, string Title, string Message, string? SecondaryText, string? ActionLabel, string? ActionUrl, EventPriority Priority, string AggregationKey, string DeduplicationKey, bool Replaceable, DateTimeOffset? ProducerCreatedAt, DateTimeOffset? ServerPublishedAt, DateTimeOffset ReceivedAt)`; `class EventParser` with `bool TryParse(ReadOnlySpan<byte> payload, DateTimeOffset receivedAt, out InboundNotification? notification, out string? error)` and constants `EventParser.MaxPayloadBytes == 32768`, `EventParser.MaxJsonDepth == 16`.

- [ ] **Step 1: Install .NET 8 SDK and init repo**

```bash
cd /home/cjamhe01385/os-notification
curl -fsSL https://dot.net/v1/dotnet-install.sh -o /tmp/dotnet-install.sh
bash /tmp/dotnet-install.sh --channel 8.0
export PATH="$HOME/.dotnet:$PATH"
dotnet --version          # Expected: 8.0.4xx
git init
dotnet new gitignore
git add .gitignore docs/ windows_desktop_notification_agent_core_nats_design.html
git commit -m "chore: repo init with design doc and implementation plan"
```

- [ ] **Step 2: Create solution, Core project, test project**

```bash
export PATH="$HOME/.dotnet:$PATH"   # every shell, every task — not repeated below
dotnet new sln -n NotificationAgent
dotnet new classlib -n NotificationAgent.Core -o src/NotificationAgent.Core -f net8.0
rm src/NotificationAgent.Core/Class1.cs
dotnet new xunit -n NotificationAgent.Core.Tests -o tests/NotificationAgent.Core.Tests -f net8.0
rm tests/NotificationAgent.Core.Tests/UnitTest1.cs
dotnet add tests/NotificationAgent.Core.Tests reference src/NotificationAgent.Core
dotnet add tests/NotificationAgent.Core.Tests package Microsoft.Extensions.TimeProvider.Testing --version "8.*"
dotnet sln add src/NotificationAgent.Core tests/NotificationAgent.Core.Tests
dotnet build   # Expected: Build succeeded. 0 Warning(s) 0 Error(s)
```

- [ ] **Step 3: Write the failing parser tests**

Create `tests/NotificationAgent.Core.Tests/EventParserTests.cs`:

```csharp
using System.Text;
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
        var json = $$"""
            {"eventId":"e1","target":{"userId":"u1"},
             "content":{"title":"t","message":"m"},
             "classification":{"priority":"{{priority}}"}}
            """;
        Assert.True(_parser.TryParse(Encoding.UTF8.GetBytes(json), ReceivedAt, out var n, out _));
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
```

- [ ] **Step 4: Run tests to verify they fail**

Run: `dotnet test 2>&1 | tail -5`
Expected: build FAILS with `CS0246: The type or namespace name 'EventParser' could not be found` (compile failure is this stage's "red").

- [ ] **Step 5: Implement model and parser**

Create `src/NotificationAgent.Core/Models/InboundNotification.cs`:

```csharp
namespace NotificationAgent.Core.Models;

public enum EventPriority { Normal, Important, Critical }

/// <summary>Normalized, validated notification event as consumed by the pipeline.</summary>
public sealed record InboundNotification(
    string EventId,
    string UserId,
    string Title,
    string Message,
    string? SecondaryText,
    string? ActionLabel,
    string? ActionUrl,
    EventPriority Priority,
    string AggregationKey,
    string DeduplicationKey,
    bool Replaceable,
    DateTimeOffset? ProducerCreatedAt,
    DateTimeOffset? ServerPublishedAt,
    DateTimeOffset ReceivedAt);
```

Create `src/NotificationAgent.Core/Serialization/EventParser.cs`:

```csharp
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

    public bool TryParse(ReadOnlySpan<byte> payload, DateTimeOffset receivedAt,
        out InboundNotification? notification, out string? error)
    {
        notification = null;
        if (payload.Length == 0) { error = "empty payload"; return false; }
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

        if (wire is null) { error = "payload is json null"; return false; }
        if (string.IsNullOrWhiteSpace(wire.EventId)) { error = "missing eventId"; return false; }
        if (string.IsNullOrWhiteSpace(wire.Target?.UserId)) { error = "missing target.userId"; return false; }
        if (string.IsNullOrWhiteSpace(wire.Content?.Title)) { error = "missing content.title"; return false; }
        if (string.IsNullOrWhiteSpace(wire.Content?.Message)) { error = "missing content.message"; return false; }

        var type = string.IsNullOrWhiteSpace(wire.NotificationType) ? "unknown" : wire.NotificationType!;
        var priority = wire.Classification?.Priority?.ToLowerInvariant() switch
        {
            "critical" => EventPriority.Critical,
            "important" => EventPriority.Important,
            _ => EventPriority.Normal,
        };

        notification = new InboundNotification(
            EventId: wire.EventId!,
            UserId: wire.Target!.UserId!,
            Title: wire.Content!.Title!,
            Message: wire.Content.Message!,
            SecondaryText: wire.Content.SecondaryText,
            ActionLabel: wire.Action?.Label,
            ActionUrl: wire.Action?.Url,
            Priority: priority,
            AggregationKey: string.IsNullOrWhiteSpace(wire.Classification?.AggregationKey)
                ? type : wire.Classification!.AggregationKey!,
            DeduplicationKey: string.IsNullOrWhiteSpace(wire.Classification?.DeduplicationKey)
                ? wire.EventId! : wire.Classification!.DeduplicationKey!,
            Replaceable: wire.Classification?.Replaceable ?? false,
            ProducerCreatedAt: wire.Timestamps?.ProducerCreatedAt,
            ServerPublishedAt: wire.Timestamps?.ServerPublishedAt,
            ReceivedAt: receivedAt);
        error = null;
        return true;
    }

    private sealed class WireEvent
    {
        public string? SchemaVersion { get; set; }
        public string? EventId { get; set; }
        public string? NotificationType { get; set; }
        public WireTarget? Target { get; set; }
        public WireContent? Content { get; set; }
        public WireAction? Action { get; set; }
        public WireClassification? Classification { get; set; }
        public WireTimestamps? Timestamps { get; set; }
    }

    private sealed class WireTarget { public string? UserId { get; set; } }

    private sealed class WireContent
    {
        public string? Title { get; set; }
        public string? Message { get; set; }
        public string? SecondaryText { get; set; }
    }

    private sealed class WireAction { public string? Label { get; set; } public string? Url { get; set; } }

    private sealed class WireClassification
    {
        public string? Priority { get; set; }
        public string? AggregationKey { get; set; }
        public string? DeduplicationKey { get; set; }
        public bool? Replaceable { get; set; }
    }

    private sealed class WireTimestamps
    {
        public DateTimeOffset? ProducerCreatedAt { get; set; }
        public DateTimeOffset? ServerPublishedAt { get; set; }
    }
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `dotnet test 2>&1 | tail -5`
Expected: `Passed!` with 13 passed (1 + 4 theory cases + 1 + 4 theory cases + 3), 0 failed.

- [ ] **Step 7: Commit**

```bash
git add NotificationAgent.sln src/ tests/
git commit -m "feat: event model and parser with 32KB/depth-16/required-field validation"
```

---

### Task 2: Grapheme truncation and toast content factory

**Files:**
- Create: `src/NotificationAgent.Core/Rendering/GraphemeText.cs`
- Create: `src/NotificationAgent.Core/Rendering/ToastRequest.cs`
- Create: `src/NotificationAgent.Core/Rendering/ToastContentFactory.cs`
- Test: `tests/NotificationAgent.Core.Tests/GraphemeTextTests.cs`, `tests/NotificationAgent.Core.Tests/ToastContentFactoryTests.cs`

**Interfaces:**
- Consumes: `InboundNotification` (Task 1).
- Produces: `static string GraphemeText.Truncate(string value, int maxGraphemes)`; `record ToastRequest(string Title, string Message, string? Attribution, string? ActionLabel, string? ActionUrl, IReadOnlyList<InboundNotification> Sources)`; `interface IToastRenderer { ValueTask<DateTimeOffset> ShowAsync(ToastRequest toast, CancellationToken ct = default); }` (returns the toast-submission timestamp); `static ToastRequest ToastContentFactory.FromSingle(InboundNotification n)`; `static ToastRequest ToastContentFactory.FromBatch(IReadOnlyList<InboundNotification> batch)`; constants `ToastContentFactory.MaxTitleGraphemes == 120`, `ToastContentFactory.MaxMessageGraphemes == 500`.

- [ ] **Step 1: Write the failing tests**

Create `tests/NotificationAgent.Core.Tests/GraphemeTextTests.cs`:

```csharp
using NotificationAgent.Core.Rendering;
using Xunit;

namespace NotificationAgent.Core.Tests;

public class GraphemeTextTests
{
    [Fact]
    public void Returns_short_strings_unchanged()
    {
        Assert.Equal("hello", GraphemeText.Truncate("hello", 5));
        Assert.Equal("", GraphemeText.Truncate("", 5));
    }

    [Fact]
    public void Truncates_to_limit_with_ellipsis()
    {
        // 6 chars, limit 5 → 4 kept + "…" = 5 grapheme clusters total
        Assert.Equal("abcd…", GraphemeText.Truncate("abcdef", 5));
    }

    [Fact]
    public void Counts_grapheme_clusters_not_chars()
    {
        // Family emoji (woman+woman+girl+boy joined by U+200D zero-width joiners):
        // 1 grapheme cluster, 11 UTF-16 code units
        var family = "\U0001F469\u200D\U0001F469\u200D\U0001F467\u200D\U0001F466";
        Assert.Equal(family, GraphemeText.Truncate(family, 1));         // fits: 1 cluster
        Assert.Equal(family + family, GraphemeText.Truncate(family + family, 2));
        Assert.Equal(family + "…", GraphemeText.Truncate(family + family + family, 2));
    }
}
```

Create `tests/NotificationAgent.Core.Tests/ToastContentFactoryTests.cs`:

```csharp
using NotificationAgent.Core.Models;
using NotificationAgent.Core.Rendering;
using Xunit;

namespace NotificationAgent.Core.Tests;

public class ToastContentFactoryTests
{
    internal static InboundNotification Event(
        string id = "e1", string title = "Title", string message = "Message",
        EventPriority priority = EventPriority.Normal, string aggKey = "agg.key",
        string? dedupKey = null, bool replaceable = false,
        string? actionLabel = "Open", string? actionUrl = "https://example.com/x") =>
        new(id, "u1", title, message, "App", actionLabel, actionUrl, priority,
            aggKey, dedupKey ?? id, replaceable, null, null,
            DateTimeOffset.Parse("2026-07-15T08:30:00.190Z"));

    [Fact]
    public void Single_event_maps_fields_directly()
    {
        var n = Event();
        var toast = ToastContentFactory.FromSingle(n);

        Assert.Equal("Title", toast.Title);
        Assert.Equal("Message", toast.Message);
        Assert.Equal("App", toast.Attribution);
        Assert.Equal("Open", toast.ActionLabel);
        Assert.Equal("https://example.com/x", toast.ActionUrl);
        Assert.Equal(new[] { n }, toast.Sources);
    }

    [Fact]
    public void Single_event_truncates_title_to_120_and_message_to_500_graphemes()
    {
        var toast = ToastContentFactory.FromSingle(
            Event(title: new string('T', 200), message: new string('M', 600)));

        Assert.Equal(120, new System.Globalization.StringInfo(toast.Title).LengthInTextElements);
        Assert.EndsWith("…", toast.Title);
        Assert.Equal(500, new System.Globalization.StringInfo(toast.Message).LengthInTextElements);
        Assert.EndsWith("…", toast.Message);
    }

    [Fact]
    public void Batch_of_one_behaves_like_single()
    {
        // Field-by-field: ToastRequest holds an IReadOnlyList, so record equality
        // is reference-based on that member and can't be used here.
        var n = Event();
        var toast = ToastContentFactory.FromBatch(new[] { n });
        Assert.Equal("Title", toast.Title);
        Assert.Equal("Message", toast.Message);
        Assert.Same(n, Assert.Single(toast.Sources));
    }

    [Fact]
    public void Batch_summarizes_count_and_latest_event()
    {
        var batch = new[] { Event("e1", message: "first"), Event("e2", message: "second"),
                            Event("e3", message: "third") };
        var toast = ToastContentFactory.FromBatch(batch);

        Assert.Equal("3 notifications — agg.key", toast.Title);
        Assert.Equal("Latest: third", toast.Message);
        Assert.Equal(3, toast.Sources.Count);
        Assert.Equal("Open", toast.ActionLabel); // action of the latest event
    }

    [Fact]
    public void Empty_batch_throws()
    {
        Assert.Throws<ArgumentException>(() =>
            ToastContentFactory.FromBatch(Array.Empty<InboundNotification>()));
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `dotnet test 2>&1 | tail -5`
Expected: build FAILS with `CS0246 ... 'GraphemeText' could not be found`.

- [ ] **Step 3: Implement**

Create `src/NotificationAgent.Core/Rendering/GraphemeText.cs`:

```csharp
using System.Globalization;

namespace NotificationAgent.Core.Rendering;

public static class GraphemeText
{
    /// <summary>Truncate to at most <paramref name="maxGraphemes"/> extended grapheme
    /// clusters (the design doc's "product limit" unit), ellipsis included.</summary>
    public static string Truncate(string value, int maxGraphemes)
    {
        ArgumentOutOfRangeException.ThrowIfLessThan(maxGraphemes, 1);
        var info = new StringInfo(value);
        if (info.LengthInTextElements <= maxGraphemes) return value;
        return info.SubstringByTextElements(0, maxGraphemes - 1) + "…";
    }
}
```

Create `src/NotificationAgent.Core/Rendering/ToastRequest.cs`:

```csharp
using NotificationAgent.Core.Models;

namespace NotificationAgent.Core.Rendering;

/// <summary>Renderer-ready toast. Sources lists every event this toast represents,
/// so the caller can ack each of them as submitted_to_windows.</summary>
public sealed record ToastRequest(
    string Title,
    string Message,
    string? Attribution,
    string? ActionLabel,
    string? ActionUrl,
    IReadOnlyList<InboundNotification> Sources);

public interface IToastRenderer
{
    /// <summary>Submit the toast; returns the submission timestamp (toastSubmittedAt).</summary>
    ValueTask<DateTimeOffset> ShowAsync(ToastRequest toast, CancellationToken ct = default);
}
```

Create `src/NotificationAgent.Core/Rendering/ToastContentFactory.cs`:

```csharp
using NotificationAgent.Core.Models;

namespace NotificationAgent.Core.Rendering;

public static class ToastContentFactory
{
    public const int MaxTitleGraphemes = 120;
    public const int MaxMessageGraphemes = 500;

    public static ToastRequest FromSingle(InboundNotification n) =>
        new(GraphemeText.Truncate(n.Title, MaxTitleGraphemes),
            GraphemeText.Truncate(n.Message, MaxMessageGraphemes),
            n.SecondaryText, n.ActionLabel, n.ActionUrl, new[] { n });

    public static ToastRequest FromBatch(IReadOnlyList<InboundNotification> batch)
    {
        if (batch.Count == 0) throw new ArgumentException("batch must not be empty", nameof(batch));
        if (batch.Count == 1) return FromSingle(batch[0]);

        var latest = batch[^1];
        return new ToastRequest(
            GraphemeText.Truncate($"{batch.Count} notifications — {latest.AggregationKey}", MaxTitleGraphemes),
            GraphemeText.Truncate($"Latest: {latest.Message}", MaxMessageGraphemes),
            latest.SecondaryText, latest.ActionLabel, latest.ActionUrl,
            batch.ToArray());
    }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `dotnet test 2>&1 | tail -5`
Expected: `Passed!` — 21 total tests (13 from Task 1 + 8 new), 0 failed.

- [ ] **Step 5: Commit**

```bash
git add src/NotificationAgent.Core/Rendering tests/
git commit -m "feat: grapheme-limit toast content factory and IToastRenderer contract"
```

---

### Task 3: Bounded TTL deduplication cache

**Files:**
- Create: `src/NotificationAgent.Core/Dedup/DeduplicationCache.cs`
- Test: `tests/NotificationAgent.Core.Tests/DeduplicationCacheTests.cs`

**Interfaces:**
- Consumes: `TimeProvider` (BCL).
- Produces: `class DeduplicationCache` with ctor `(int capacity, TimeSpan ttl, TimeProvider? timeProvider = null)`, `bool TryAdd(string key)` (true = first sighting → process; false = duplicate → drop), `int Count`.

- [ ] **Step 1: Write the failing tests**

Create `tests/NotificationAgent.Core.Tests/DeduplicationCacheTests.cs`:

```csharp
using Microsoft.Extensions.Time.Testing;
using NotificationAgent.Core.Dedup;
using Xunit;

namespace NotificationAgent.Core.Tests;

public class DeduplicationCacheTests
{
    [Fact]
    public void First_add_true_second_false()
    {
        var cache = new DeduplicationCache(capacity: 10, ttl: TimeSpan.FromMinutes(10));
        Assert.True(cache.TryAdd("k1"));
        Assert.False(cache.TryAdd("k1"));
        Assert.True(cache.TryAdd("k2"));
    }

    [Fact]
    public void Key_expires_after_ttl()
    {
        var time = new FakeTimeProvider();
        var cache = new DeduplicationCache(10, TimeSpan.FromMinutes(10), time);

        Assert.True(cache.TryAdd("k1"));
        time.Advance(TimeSpan.FromMinutes(9));
        Assert.False(cache.TryAdd("k1"));      // still within TTL
        time.Advance(TimeSpan.FromMinutes(2)); // now 11 min since insert
        Assert.True(cache.TryAdd("k1"));
    }

    [Fact]
    public void Evicts_oldest_when_over_capacity()
    {
        var cache = new DeduplicationCache(capacity: 2, ttl: TimeSpan.FromHours(1));
        Assert.True(cache.TryAdd("a"));
        Assert.True(cache.TryAdd("b"));
        Assert.True(cache.TryAdd("c"));  // evicts "a"
        Assert.True(cache.TryAdd("a"));  // "a" was forgotten
        Assert.True(cache.Count <= 2);
    }

    [Fact]
    public void Is_thread_safe_under_concurrent_adds()
    {
        var cache = new DeduplicationCache(10_000, TimeSpan.FromMinutes(10));
        var wins = 0;
        Parallel.For(0, 1000, i =>
        {
            if (cache.TryAdd("same-key")) Interlocked.Increment(ref wins);
            cache.TryAdd($"key-{i}");
        });
        Assert.Equal(1, wins); // exactly one thread may win a given key
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `dotnet test 2>&1 | tail -5`
Expected: build FAILS with `CS0246 ... 'DeduplicationCache' could not be found`.

- [ ] **Step 3: Implement**

Create `src/NotificationAgent.Core/Dedup/DeduplicationCache.cs`:

```csharp
namespace NotificationAgent.Core.Dedup;

/// <summary>In-memory duplicate suppression, bounded by entry count and TTL (design §5
/// "Local state": bounded deduplication state). Not persistent — POC scope.</summary>
public sealed class DeduplicationCache
{
    private readonly object _gate = new();
    private readonly Dictionary<string, long> _expiryByKey = new();
    private readonly Queue<(string Key, long ExpiresAtTicks)> _insertionOrder = new();
    private readonly int _capacity;
    private readonly TimeSpan _ttl;
    private readonly TimeProvider _time;

    public DeduplicationCache(int capacity, TimeSpan ttl, TimeProvider? timeProvider = null)
    {
        ArgumentOutOfRangeException.ThrowIfLessThan(capacity, 1);
        _capacity = capacity;
        _ttl = ttl;
        _time = timeProvider ?? TimeProvider.System;
    }

    public bool TryAdd(string key)
    {
        var now = _time.GetUtcNow().UtcTicks;
        lock (_gate)
        {
            PurgeExpired(now);
            if (_expiryByKey.TryGetValue(key, out var existing) && existing > now)
                return false;

            while (_expiryByKey.Count >= _capacity && _insertionOrder.Count > 0)
                DequeueOne();

            var expires = now + _ttl.Ticks;
            _expiryByKey[key] = expires;
            _insertionOrder.Enqueue((key, expires));
            return true;
        }
    }

    public int Count { get { lock (_gate) return _expiryByKey.Count; } }

    private void PurgeExpired(long now)
    {
        while (_insertionOrder.Count > 0 && _insertionOrder.Peek().ExpiresAtTicks <= now)
            DequeueOne();
    }

    private void DequeueOne()
    {
        var (key, expiresAt) = _insertionOrder.Dequeue();
        // A re-added key leaves a stale queue entry behind; only remove on exact match.
        if (_expiryByKey.TryGetValue(key, out var current) && current == expiresAt)
            _expiryByKey.Remove(key);
    }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `dotnet test 2>&1 | tail -5`
Expected: `Passed!` — 25 total, 0 failed.

- [ ] **Step 5: Commit**

```bash
git add src/NotificationAgent.Core/Dedup tests/
git commit -m "feat: bounded TTL deduplication cache"
```

---

### Task 4: Priority-aware aggregator

**Files:**
- Create: `src/NotificationAgent.Core/Aggregation/Aggregator.cs`
- Test: `tests/NotificationAgent.Core.Tests/AggregatorTests.cs`

**Interfaces:**
- Consumes: `InboundNotification`, `EventPriority` (Task 1); `ToastRequest`, `ToastContentFactory` (Task 2); `TimeProvider`.
- Produces: `class AggregatorOptions { int MaxBuckets = 100; TimeSpan ImportantWindow = 2s; TimeSpan NormalWindow = 10s; }`; `class Aggregator : IAsyncDisposable` with ctor `(AggregatorOptions options, TimeProvider time, Func<ToastRequest, ValueTask> renderAsync)`, `ValueTask AddAsync(InboundNotification n)`, `long DroppedBucketOverflow`.

Behavior (design §6.3 + ADR-007):
- `critical` → render immediately, bypassing buckets.
- `important`/`normal` → buffered in a bucket keyed by `(AggregationKey, Priority)`; the bucket flushes once, `ImportantWindow`/`NormalWindow` after its first event, as one `ToastRequest` via `ToastContentFactory.FromBatch`.
- `Replaceable == true` → the bucket keeps only the latest event (progress/state replacement).
- More than `MaxBuckets` concurrent buckets → new events for *new* keys are dropped and counted.

- [ ] **Step 1: Write the failing tests**

Create `tests/NotificationAgent.Core.Tests/AggregatorTests.cs`:

```csharp
using Microsoft.Extensions.Time.Testing;
using NotificationAgent.Core.Aggregation;
using NotificationAgent.Core.Models;
using NotificationAgent.Core.Rendering;
using Xunit;

namespace NotificationAgent.Core.Tests;

public class AggregatorTests
{
    private readonly FakeTimeProvider _time = new();
    private readonly List<ToastRequest> _rendered = new();

    private Aggregator Create(AggregatorOptions? options = null) =>
        new(options ?? new AggregatorOptions(), _time, toast =>
        {
            lock (_rendered) _rendered.Add(toast);
            return ValueTask.CompletedTask;
        });

    private static InboundNotification Event(string id, EventPriority priority,
        string aggKey = "agg.key", bool replaceable = false, string message = "m") =>
        ToastContentFactoryTests.Event(id, message: message, priority: priority,
            aggKey: aggKey, replaceable: replaceable);

    [Fact]
    public async Task Critical_renders_immediately()
    {
        await using var agg = Create();
        await agg.AddAsync(Event("e1", EventPriority.Critical));
        var toast = Assert.Single(_rendered);
        Assert.Equal("e1", Assert.Single(toast.Sources).EventId);
    }

    [Fact]
    public async Task Normal_events_batch_and_flush_after_10s()
    {
        await using var agg = Create();
        await agg.AddAsync(Event("e1", EventPriority.Normal));
        await agg.AddAsync(Event("e2", EventPriority.Normal));
        await agg.AddAsync(Event("e3", EventPriority.Normal));
        Assert.Empty(_rendered);                       // window still open

        _time.Advance(TimeSpan.FromSeconds(10));
        var toast = Assert.Single(_rendered);
        Assert.Equal(3, toast.Sources.Count);
        Assert.StartsWith("3 notifications", toast.Title);
    }

    [Fact]
    public async Task Important_flushes_after_2s_normal_does_not()
    {
        await using var agg = Create();
        await agg.AddAsync(Event("i1", EventPriority.Important, aggKey: "imp"));
        await agg.AddAsync(Event("n1", EventPriority.Normal, aggKey: "norm"));

        _time.Advance(TimeSpan.FromSeconds(2));
        Assert.Equal("i1", Assert.Single(Assert.Single(_rendered).Sources).EventId);

        _time.Advance(TimeSpan.FromSeconds(8));
        Assert.Equal(2, _rendered.Count);
    }

    [Fact]
    public async Task Replaceable_keeps_only_latest_value()
    {
        await using var agg = Create();
        await agg.AddAsync(Event("p1", EventPriority.Normal, aggKey: "prog", replaceable: true, message: "10%"));
        await agg.AddAsync(Event("p2", EventPriority.Normal, aggKey: "prog", replaceable: true, message: "60%"));
        await agg.AddAsync(Event("p3", EventPriority.Normal, aggKey: "prog", replaceable: true, message: "90%"));

        _time.Advance(TimeSpan.FromSeconds(10));
        var toast = Assert.Single(_rendered);
        var source = Assert.Single(toast.Sources);     // replaced, not batched
        Assert.Equal("p3", source.EventId);
        Assert.Equal("90%", toast.Message);
    }

    [Fact]
    public async Task Separate_aggregation_keys_produce_separate_toasts()
    {
        await using var agg = Create();
        await agg.AddAsync(Event("a1", EventPriority.Normal, aggKey: "a"));
        await agg.AddAsync(Event("b1", EventPriority.Normal, aggKey: "b"));

        _time.Advance(TimeSpan.FromSeconds(10));
        Assert.Equal(2, _rendered.Count);
    }

    [Fact]
    public async Task Drops_events_beyond_max_buckets()
    {
        await using var agg = Create(new AggregatorOptions { MaxBuckets = 2 });
        await agg.AddAsync(Event("a1", EventPriority.Normal, aggKey: "a"));
        await agg.AddAsync(Event("b1", EventPriority.Normal, aggKey: "b"));
        await agg.AddAsync(Event("c1", EventPriority.Normal, aggKey: "c")); // over cap → dropped

        Assert.Equal(1, agg.DroppedBucketOverflow);
        _time.Advance(TimeSpan.FromSeconds(10));
        Assert.Equal(2, _rendered.Count);
    }

    [Fact]
    public async Task Dispose_flushes_pending_buckets()
    {
        var agg = Create();
        await agg.AddAsync(Event("e1", EventPriority.Normal));
        await agg.DisposeAsync();
        Assert.Single(_rendered);
    }
}
```

Also make Task 2's test helper visible: in `ToastContentFactoryTests.cs` the `Event(...)` helper is already `internal static` — confirm it compiles from this file (same assembly).

- [ ] **Step 2: Run tests to verify they fail**

Run: `dotnet test 2>&1 | tail -5`
Expected: build FAILS with `CS0246 ... 'Aggregator' could not be found`.

- [ ] **Step 3: Implement**

Create `src/NotificationAgent.Core/Aggregation/Aggregator.cs`:

```csharp
using NotificationAgent.Core.Models;
using NotificationAgent.Core.Rendering;

namespace NotificationAgent.Core.Aggregation;

public sealed class AggregatorOptions
{
    public int MaxBuckets { get; init; } = 100;
    public TimeSpan ImportantWindow { get; init; } = TimeSpan.FromSeconds(2);
    public TimeSpan NormalWindow { get; init; } = TimeSpan.FromSeconds(10);
}

/// <summary>Owns priority handling, batching, and latest-state replacement (ADR-007).
/// Best-effort: render failures are swallowed, bucket overflow drops events.</summary>
public sealed class Aggregator : IAsyncDisposable
{
    private sealed class Bucket
    {
        public List<InboundNotification> Events { get; } = new();
        public ITimer? Timer { get; set; }
    }

    private readonly object _gate = new();
    private readonly Dictionary<(string Key, EventPriority Priority), Bucket> _buckets = new();
    private readonly AggregatorOptions _options;
    private readonly TimeProvider _time;
    private readonly Func<ToastRequest, ValueTask> _renderAsync;
    private long _droppedBucketOverflow;

    public long DroppedBucketOverflow => Interlocked.Read(ref _droppedBucketOverflow);

    public Aggregator(AggregatorOptions options, TimeProvider time, Func<ToastRequest, ValueTask> renderAsync)
    {
        _options = options;
        _time = time;
        _renderAsync = renderAsync;
    }

    public async ValueTask AddAsync(InboundNotification n)
    {
        if (n.Priority == EventPriority.Critical)
        {
            await RenderSafeAsync(ToastContentFactory.FromSingle(n)).ConfigureAwait(false);
            return;
        }

        lock (_gate)
        {
            var key = (n.AggregationKey, n.Priority);
            if (!_buckets.TryGetValue(key, out var bucket))
            {
                if (_buckets.Count >= _options.MaxBuckets)
                {
                    Interlocked.Increment(ref _droppedBucketOverflow);
                    return;
                }
                bucket = new Bucket();
                _buckets[key] = bucket;
                var window = n.Priority == EventPriority.Important
                    ? _options.ImportantWindow : _options.NormalWindow;
                bucket.Timer = _time.CreateTimer(_ => Flush(key), null, window, Timeout.InfiniteTimeSpan);
            }
            if (n.Replaceable) bucket.Events.Clear();
            bucket.Events.Add(n);
        }
    }

    private void Flush((string Key, EventPriority Priority) key)
    {
        List<InboundNotification>? events = null;
        lock (_gate)
        {
            if (_buckets.Remove(key, out var bucket))
            {
                bucket.Timer?.Dispose();
                events = bucket.Events;
            }
        }
        if (events is { Count: > 0 })
        {
            var pending = RenderSafeAsync(ToastContentFactory.FromBatch(events));
            if (!pending.IsCompleted) _ = pending.AsTask();
        }
    }

    private async ValueTask RenderSafeAsync(ToastRequest toast)
    {
        try { await _renderAsync(toast).ConfigureAwait(false); }
        catch { /* best-effort delivery: a render failure must not crash the agent */ }
    }

    public async ValueTask DisposeAsync()
    {
        List<(string, EventPriority)> keys;
        lock (_gate) keys = _buckets.Keys.ToList();
        foreach (var key in keys) Flush(key);
        await Task.CompletedTask;
    }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `dotnet test 2>&1 | tail -5`
Expected: `Passed!` — 32 total, 0 failed. (FakeTimeProvider fires `CreateTimer` callbacks synchronously inside `Advance`, and the test render callback completes synchronously, so no sleeps or polling are needed.)

- [ ] **Step 5: Commit**

```bash
git add src/NotificationAgent.Core/Aggregation tests/
git commit -m "feat: priority-aware aggregator with batch windows, replacement, bucket cap"
```

---

### Task 5: Acknowledgement telemetry contract

**Files:**
- Create: `src/NotificationAgent.Core/Telemetry/Acks.cs`
- Test: `tests/NotificationAgent.Core.Tests/AckJsonTests.cs`

**Interfaces:**
- Consumes: nothing new.
- Produces: `static class AckStatuses { const string ObservedByAgent = "observed_by_agent"; const string SubmittedToWindows = "submitted_to_windows"; }`; `record AckPayload(string EventId, string DeviceId, DateTimeOffset AgentReceivedAt, DateTimeOffset? ToastSubmittedAt, string Status)`; `interface ITelemetryPublisher { ValueTask PublishAckAsync(AckPayload ack, CancellationToken ct = default); }`; `static byte[] AckJson.Serialize(AckPayload ack)`.

- [ ] **Step 1: Write the failing tests**

Create `tests/NotificationAgent.Core.Tests/AckJsonTests.cs`:

```csharp
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
        Assert.Equal(DateTimeOffset.Parse("2026-07-15T08:30:00.190Z"),
            root.GetProperty("agentReceivedAt").GetDateTimeOffset());
        Assert.Equal(DateTimeOffset.Parse("2026-07-15T08:30:00.205Z"),
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `dotnet test 2>&1 | tail -5`
Expected: build FAILS with `CS0246 ... 'AckPayload' could not be found`.

- [ ] **Step 3: Implement**

Create `src/NotificationAgent.Core/Telemetry/Acks.cs`:

```csharp
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `dotnet test 2>&1 | tail -5`
Expected: `Passed!` — 34 total, 0 failed.

- [ ] **Step 5: Commit**

```bash
git add src/NotificationAgent.Core/Telemetry tests/
git commit -m "feat: acknowledgement payload contract and serializer"
```

---

### Task 6: Bounded-channel event pipeline

**Files:**
- Create: `src/NotificationAgent.Core/Pipeline/EventPipeline.cs`
- Test: `tests/NotificationAgent.Core.Tests/EventPipelineTests.cs`

**Interfaces:**
- Consumes: `EventParser` (Task 1), `DeduplicationCache` (Task 3), `Aggregator`/`AggregatorOptions` (Task 4), `ITelemetryPublisher`/`AckPayload`/`AckStatuses` (Task 5), `IToastRenderer`/`ToastRequest` (Task 2).
- Produces: `record ReceivedEvent(byte[] Payload, DateTimeOffset ReceivedAt)`; `class PipelineOptions { int QueueCapacity = 500; int WorkerCount = 2; }`; `class EventPipeline : IAsyncDisposable` with ctor `(PipelineOptions options, DeduplicationCache dedup, Aggregator aggregator, ITelemetryPublisher telemetry, string deviceId)`, `bool TryEnqueue(ReceivedEvent evt)`, `void Start()`, `long DroppedQueueFull`; `static class AgentPipelineFactory` with `(EventPipeline Pipeline, Aggregator Aggregator) Create(PipelineOptions, AggregatorOptions, DeduplicationCache, IToastRenderer, ITelemetryPublisher, string deviceId, TimeProvider)`.

Worker behavior per event: parse (drop invalid) → dedup (drop duplicate) → publish `observed_by_agent` ack → hand to aggregator. The factory wires the aggregator's render callback: `renderer.ShowAsync(toast)` then one `submitted_to_windows` ack per `toast.Sources` entry, each carrying that source's own `ReceivedAt` and the shared `toastSubmittedAt`.

- [ ] **Step 1: Write the failing tests**

Create `tests/NotificationAgent.Core.Tests/EventPipelineTests.cs`:

```csharp
using System.Text;
using Microsoft.Extensions.Time.Testing;
using NotificationAgent.Core.Aggregation;
using NotificationAgent.Core.Dedup;
using NotificationAgent.Core.Pipeline;
using NotificationAgent.Core.Rendering;
using NotificationAgent.Core.Telemetry;
using Xunit;

namespace NotificationAgent.Core.Tests;

public class EventPipelineTests
{
    private sealed class RecordingTelemetry : ITelemetryPublisher
    {
        public List<AckPayload> Acks { get; } = new();
        public ValueTask PublishAckAsync(AckPayload ack, CancellationToken ct = default)
        {
            lock (Acks) Acks.Add(ack);
            return ValueTask.CompletedTask;
        }
    }

    private sealed class RecordingRenderer : IToastRenderer
    {
        public List<ToastRequest> Shown { get; } = new();
        public DateTimeOffset SubmitAt { get; set; } = DateTimeOffset.Parse("2026-07-15T08:30:00.205Z");
        public ValueTask<DateTimeOffset> ShowAsync(ToastRequest toast, CancellationToken ct = default)
        {
            lock (Shown) Shown.Add(toast);
            return ValueTask.FromResult(SubmitAt);
        }
    }

    private static readonly DateTimeOffset ReceivedAt = DateTimeOffset.Parse("2026-07-15T08:30:00.190Z");

    private static ReceivedEvent CriticalEvent(string id) => new(Encoding.UTF8.GetBytes($$"""
        {"eventId":"{{id}}","target":{"userId":"u1"},
         "content":{"title":"T","message":"M"},
         "classification":{"priority":"critical","deduplicationKey":"{{id}}"}}
        """), ReceivedAt);

    private static async Task WaitUntilAsync(Func<bool> condition)
    {
        for (var i = 0; i < 500 && !condition(); i++) await Task.Delay(10);
        Assert.True(condition(), "condition not reached within 5s");
    }

    [Fact]
    public async Task Valid_critical_event_flows_to_renderer_with_both_acks()
    {
        var telemetry = new RecordingTelemetry();
        var renderer = new RecordingRenderer();
        var (pipeline, aggregator) = AgentPipelineFactory.Create(
            new PipelineOptions(), new AggregatorOptions(),
            new DeduplicationCache(100, TimeSpan.FromMinutes(10)),
            renderer, telemetry, deviceId: "d-456", new FakeTimeProvider());
        await using var _p = pipeline;
        await using var _a = aggregator;
        pipeline.Start();

        Assert.True(pipeline.TryEnqueue(CriticalEvent("evt-1")));
        await WaitUntilAsync(() => { lock (telemetry.Acks) return telemetry.Acks.Count == 2; });

        Assert.Single(renderer.Shown);
        var observed = telemetry.Acks.Single(a => a.Status == AckStatuses.ObservedByAgent);
        var submitted = telemetry.Acks.Single(a => a.Status == AckStatuses.SubmittedToWindows);
        Assert.Equal("evt-1", observed.EventId);
        Assert.Equal("d-456", observed.DeviceId);
        Assert.Equal(ReceivedAt, observed.AgentReceivedAt);
        Assert.Null(observed.ToastSubmittedAt);
        Assert.Equal(renderer.SubmitAt, submitted.ToastSubmittedAt);
        Assert.Equal(ReceivedAt, submitted.AgentReceivedAt);
    }

    [Fact]
    public async Task Duplicate_events_are_processed_once()
    {
        var telemetry = new RecordingTelemetry();
        var renderer = new RecordingRenderer();
        var (pipeline, aggregator) = AgentPipelineFactory.Create(
            new PipelineOptions(), new AggregatorOptions(),
            new DeduplicationCache(100, TimeSpan.FromMinutes(10)),
            renderer, telemetry, "d-1", new FakeTimeProvider());
        await using var _p = pipeline;
        await using var _a = aggregator;
        pipeline.Start();

        pipeline.TryEnqueue(CriticalEvent("evt-dup"));
        pipeline.TryEnqueue(CriticalEvent("evt-dup"));
        pipeline.TryEnqueue(CriticalEvent("evt-dup"));
        await WaitUntilAsync(() => { lock (telemetry.Acks) return telemetry.Acks.Count >= 2; });
        await Task.Delay(100); // grace period: no further acks should arrive

        Assert.Single(renderer.Shown);
        Assert.Equal(2, telemetry.Acks.Count); // one observed + one submitted
    }

    [Fact]
    public async Task Invalid_payloads_are_dropped_silently()
    {
        var telemetry = new RecordingTelemetry();
        var renderer = new RecordingRenderer();
        var (pipeline, aggregator) = AgentPipelineFactory.Create(
            new PipelineOptions(), new AggregatorOptions(),
            new DeduplicationCache(100, TimeSpan.FromMinutes(10)),
            renderer, telemetry, "d-1", new FakeTimeProvider());
        await using var _a = aggregator;
        pipeline.Start();

        pipeline.TryEnqueue(new ReceivedEvent(Encoding.UTF8.GetBytes("garbage"), ReceivedAt));
        pipeline.TryEnqueue(CriticalEvent("evt-ok"));   // proves the worker survived
        await WaitUntilAsync(() => { lock (telemetry.Acks) return telemetry.Acks.Count == 2; });

        await pipeline.DisposeAsync();
        Assert.Single(renderer.Shown);
        Assert.All(telemetry.Acks, a => Assert.Equal("evt-ok", a.EventId));
    }

    [Fact]
    public void TryEnqueue_reports_drop_when_queue_full()
    {
        var telemetry = new RecordingTelemetry();
        var (pipeline, _) = AgentPipelineFactory.Create(
            new PipelineOptions { QueueCapacity = 2 }, new AggregatorOptions(),
            new DeduplicationCache(100, TimeSpan.FromMinutes(10)),
            new RecordingRenderer(), telemetry, "d-1", new FakeTimeProvider());
        // Never started → nothing drains the channel.

        Assert.True(pipeline.TryEnqueue(CriticalEvent("e1")));
        Assert.True(pipeline.TryEnqueue(CriticalEvent("e2")));
        Assert.False(pipeline.TryEnqueue(CriticalEvent("e3")));
        Assert.Equal(1, pipeline.DroppedQueueFull);
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `dotnet test 2>&1 | tail -5`
Expected: build FAILS with `CS0246 ... 'AgentPipelineFactory' could not be found`.

- [ ] **Step 3: Implement**

Create `src/NotificationAgent.Core/Pipeline/EventPipeline.cs`:

```csharp
using System.Threading.Channels;
using NotificationAgent.Core.Aggregation;
using NotificationAgent.Core.Dedup;
using NotificationAgent.Core.Serialization;
using NotificationAgent.Core.Rendering;
using NotificationAgent.Core.Telemetry;

namespace NotificationAgent.Core.Pipeline;

public sealed record ReceivedEvent(byte[] Payload, DateTimeOffset ReceivedAt);

public sealed class PipelineOptions
{
    public int QueueCapacity { get; init; } = 500;  // design §9 baseline
    public int WorkerCount { get; init; } = 2;      // design §9 baseline
}

/// <summary>Bounded intake queue with a fixed worker pool (design §9). Overload drops
/// events at the queue boundary — memory stays bounded, delivery stays best-effort.</summary>
public sealed class EventPipeline : IAsyncDisposable
{
    private readonly Channel<ReceivedEvent> _channel;
    private readonly EventParser _parser = new();
    private readonly DeduplicationCache _dedup;
    private readonly Aggregator _aggregator;
    private readonly ITelemetryPublisher _telemetry;
    private readonly string _deviceId;
    private readonly PipelineOptions _options;
    private readonly List<Task> _workers = new();
    private readonly CancellationTokenSource _cts = new();
    private long _droppedQueueFull;

    public long DroppedQueueFull => Interlocked.Read(ref _droppedQueueFull);

    public EventPipeline(PipelineOptions options, DeduplicationCache dedup,
        Aggregator aggregator, ITelemetryPublisher telemetry, string deviceId)
    {
        _options = options;
        _dedup = dedup;
        _aggregator = aggregator;
        _telemetry = telemetry;
        _deviceId = deviceId;
        // Wait mode + TryWrite (never blocks): TryWrite returns false when full, which
        // is our observable drop signal. DropWrite would return true and drop silently,
        // making DroppedQueueFull impossible to count.
        _channel = Channel.CreateBounded<ReceivedEvent>(new BoundedChannelOptions(options.QueueCapacity)
        {
            FullMode = BoundedChannelFullMode.Wait,
            SingleReader = false,
            SingleWriter = false,
        });
    }

    public bool TryEnqueue(ReceivedEvent evt)
    {
        if (_channel.Writer.TryWrite(evt)) return true;
        Interlocked.Increment(ref _droppedQueueFull);
        return false;
    }

    public void Start()
    {
        for (var i = 0; i < _options.WorkerCount; i++)
            _workers.Add(Task.Run(() => WorkerLoopAsync(_cts.Token)));
    }

    private async Task WorkerLoopAsync(CancellationToken ct)
    {
        await foreach (var received in _channel.Reader.ReadAllAsync(ct).ConfigureAwait(false))
        {
            try
            {
                await ProcessAsync(received, ct).ConfigureAwait(false);
            }
            catch (OperationCanceledException) when (ct.IsCancellationRequested)
            {
                return;
            }
            catch
            {
                // best-effort: one poison event must not kill the worker
            }
        }
    }

    private async ValueTask ProcessAsync(ReceivedEvent received, CancellationToken ct)
    {
        if (!_parser.TryParse(received.Payload, received.ReceivedAt, out var n, out _)) return;
        if (!_dedup.TryAdd(n!.DeduplicationKey)) return;

        await _telemetry.PublishAckAsync(
            new AckPayload(n.EventId, _deviceId, n.ReceivedAt, null, AckStatuses.ObservedByAgent),
            ct).ConfigureAwait(false);
        await _aggregator.AddAsync(n).ConfigureAwait(false);
    }

    public async ValueTask DisposeAsync()
    {
        _channel.Writer.TryComplete();
        try { await Task.WhenAll(_workers).ConfigureAwait(false); }
        catch { /* worker cancellation during shutdown is fine */ }
        _cts.Cancel();
        _cts.Dispose();
    }
}

public static class AgentPipelineFactory
{
    /// <summary>Wires renderer + telemetry into the aggregator's flush path and returns
    /// the pipeline plus the aggregator (dispose the pipeline first, then the aggregator).</summary>
    public static (EventPipeline Pipeline, Aggregator Aggregator) Create(
        PipelineOptions pipelineOptions,
        AggregatorOptions aggregatorOptions,
        DeduplicationCache dedup,
        IToastRenderer renderer,
        ITelemetryPublisher telemetry,
        string deviceId,
        TimeProvider timeProvider)
    {
        var aggregator = new Aggregator(aggregatorOptions, timeProvider, async toast =>
        {
            var submittedAt = await renderer.ShowAsync(toast).ConfigureAwait(false);
            foreach (var source in toast.Sources)
            {
                await telemetry.PublishAckAsync(new AckPayload(
                    source.EventId, deviceId, source.ReceivedAt, submittedAt,
                    AckStatuses.SubmittedToWindows)).ConfigureAwait(false);
            }
        });
        var pipeline = new EventPipeline(pipelineOptions, dedup, aggregator, telemetry, deviceId);
        return (pipeline, aggregator);
    }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `dotnet test 2>&1 | tail -5`
Expected: `Passed!` — 38 total, 0 failed.

- [ ] **Step 5: Commit**

```bash
git add src/NotificationAgent.Core/Pipeline tests/
git commit -m "feat: bounded-channel pipeline (500 cap, 2 workers) with ack wiring"
```

---

### Task 7: NATS subscriber, telemetry publisher, identity, and AgentHost

**Files:**
- Create: `src/NotificationAgent.Core/Identity/IIdentityProvider.cs`
- Create: `src/NotificationAgent.Core/Hosting/AgentHost.cs`
- Modify: `src/NotificationAgent.Core/NotificationAgent.Core.csproj` (add NATS.Net)
- Test: `tests/NotificationAgent.Core.Tests/NatsIntegrationTests.cs`

**Interfaces:**
- Consumes: everything from Tasks 1–6; `NATS.Net` package (`NatsConnection`, `NatsOpts`, `INatsConnection.PublishAsync/SubscribeAsync`).
- Produces: `interface IIdentityProvider { ValueTask<AgentIdentity> GetIdentityAsync(CancellationToken ct = default); }`; `record AgentIdentity(string UserId, string DeviceId)`; `class EnvironmentIdentityProvider : IIdentityProvider` (reads `NOTIFY_USER_ID` required, `NOTIFY_DEVICE_ID` optional); `class AgentOptions { string NatsUrl; string SubjectTemplate = "notify.user.{0}.desktop"; string AckSubject = "notify.ack.desktop"; static AgentOptions FromEnvironment(); }` (env vars `NOTIFY_NATS_URL`, `NOTIFY_SUBJECT_TEMPLATE`, `NOTIFY_ACK_SUBJECT`); `class NatsTelemetryPublisher : ITelemetryPublisher` with ctor `(INatsConnection nats, string subject)`; `class AgentHost : IAsyncDisposable` with `static Task<AgentHost> StartAsync(AgentOptions options, IIdentityProvider identityProvider, IToastRenderer renderer, CancellationToken ct = default)` and property `string Subject`.

Decision recorded here (not in the design doc): acks publish to a single shared subject `notify.ack.desktop`; the backend correlates by `eventId` + `deviceId`.

- [ ] **Step 1: Add the NATS.Net package**

```bash
dotnet add src/NotificationAgent.Core package NATS.Net --version "2.*"
dotnet build   # Expected: Build succeeded.
```

- [ ] **Step 2: Write the failing integration test**

Create `tests/NotificationAgent.Core.Tests/NatsIntegrationTests.cs`:

```csharp
using System.Net.Sockets;
using System.Text;
using NATS.Client.Core;
using NotificationAgent.Core.Hosting;
using NotificationAgent.Core.Identity;
using NotificationAgent.Core.Rendering;
using Xunit;
using Xunit.Abstractions;

namespace NotificationAgent.Core.Tests;

/// <summary>Requires a NATS server on localhost:4222
/// (docker run -d --name nats-test -p 4222:4222 nats:2.10-alpine).
/// Tests no-op with a message when the server is absent.</summary>
public class NatsIntegrationTests
{
    private readonly ITestOutputHelper _output;
    public NatsIntegrationTests(ITestOutputHelper output) => _output = output;

    private static bool NatsAvailable()
    {
        try
        {
            using var client = new TcpClient();
            return client.ConnectAsync("127.0.0.1", 4222).Wait(1000);
        }
        catch { return false; }
    }

    private sealed class StubIdentity : IIdentityProvider
    {
        public ValueTask<AgentIdentity> GetIdentityAsync(CancellationToken ct = default) =>
            ValueTask.FromResult(new AgentIdentity("itest-user", "d-itest"));
    }

    private sealed class RecordingRenderer : IToastRenderer
    {
        public List<ToastRequest> Shown { get; } = new();
        public ValueTask<DateTimeOffset> ShowAsync(ToastRequest toast, CancellationToken ct = default)
        {
            lock (Shown) Shown.Add(toast);
            return ValueTask.FromResult(DateTimeOffset.UtcNow);
        }
    }

    [Fact]
    public async Task Published_event_is_rendered_and_acked_end_to_end()
    {
        if (!NatsAvailable())
        {
            _output.WriteLine("SKIPPED: no NATS server on localhost:4222");
            return;
        }

        var options = new AgentOptions { NatsUrl = "nats://127.0.0.1:4222" };
        var renderer = new RecordingRenderer();
        await using var host = await AgentHost.StartAsync(options, new StubIdentity(), renderer);
        Assert.Equal("notify.user.itest-user.desktop", host.Subject);

        await using var probe = new NatsConnection(new NatsOpts { Url = options.NatsUrl });
        var acks = new List<string>();
        var ackReady = new TaskCompletionSource();
        var ackReader = Task.Run(async () =>
        {
            await foreach (var msg in probe.SubscribeAsync<byte[]>(options.AckSubject))
            {
                lock (acks) acks.Add(Encoding.UTF8.GetString(msg.Data!));
                lock (acks) if (acks.Count >= 2) { ackReady.TrySetResult(); break; }
            }
        });
        await Task.Delay(500); // let both subscriptions settle (Core NATS has no replay)

        var eventId = $"evt-{Guid.NewGuid():N}";
        var payload = $$"""
            {"eventId":"{{eventId}}","target":{"userId":"itest-user"},
             "content":{"title":"Integration","message":"Hello"},
             "classification":{"priority":"critical"}}
            """;
        await probe.PublishAsync(host.Subject, Encoding.UTF8.GetBytes(payload));

        var done = await Task.WhenAny(ackReady.Task, Task.Delay(TimeSpan.FromSeconds(10)));
        Assert.True(done == ackReady.Task, "did not receive 2 acks within 10s");
        Assert.Single(renderer.Shown);
        Assert.Contains(acks, a => a.Contains("observed_by_agent") && a.Contains(eventId));
        Assert.Contains(acks, a => a.Contains("submitted_to_windows") && a.Contains(eventId));
        Assert.All(acks, a => Assert.Contains("d-itest", a));
    }
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `dotnet test 2>&1 | tail -5`
Expected: build FAILS with `CS0246 ... 'AgentHost' could not be found`.

- [ ] **Step 4: Implement identity and hosting**

Create `src/NotificationAgent.Core/Identity/IIdentityProvider.cs`:

```csharp
namespace NotificationAgent.Core.Identity;

/// <summary>Resolves the immutable application user ID and device ID (design §8).
/// The Windows account name is never used as identity.</summary>
public interface IIdentityProvider
{
    ValueTask<AgentIdentity> GetIdentityAsync(CancellationToken ct = default);
}

public sealed record AgentIdentity(string UserId, string DeviceId);

/// <summary>Development identity from environment variables. NOTIFY_USER_ID is
/// required; NOTIFY_DEVICE_ID defaults to a machine-derived value.</summary>
public sealed class EnvironmentIdentityProvider : IIdentityProvider
{
    public ValueTask<AgentIdentity> GetIdentityAsync(CancellationToken ct = default)
    {
        var userId = Environment.GetEnvironmentVariable("NOTIFY_USER_ID");
        if (string.IsNullOrWhiteSpace(userId))
            throw new InvalidOperationException("NOTIFY_USER_ID is not set");
        var deviceId = Environment.GetEnvironmentVariable("NOTIFY_DEVICE_ID");
        if (string.IsNullOrWhiteSpace(deviceId))
            deviceId = $"d-{Environment.MachineName.ToLowerInvariant()}";
        return ValueTask.FromResult(new AgentIdentity(userId, deviceId));
    }
}
```

Create `src/NotificationAgent.Core/Hosting/AgentHost.cs`:

```csharp
using NATS.Client.Core;
using NotificationAgent.Core.Aggregation;
using NotificationAgent.Core.Dedup;
using NotificationAgent.Core.Identity;
using NotificationAgent.Core.Pipeline;
using NotificationAgent.Core.Rendering;
using NotificationAgent.Core.Telemetry;

namespace NotificationAgent.Core.Hosting;

public sealed class AgentOptions
{
    public string NatsUrl { get; init; } = "nats://127.0.0.1:4222";
    public string SubjectTemplate { get; init; } = "notify.user.{0}.desktop"; // design §4
    public string AckSubject { get; init; } = "notify.ack.desktop";

    public static AgentOptions FromEnvironment() => new()
    {
        NatsUrl = Environment.GetEnvironmentVariable("NOTIFY_NATS_URL") ?? "nats://127.0.0.1:4222",
        SubjectTemplate = Environment.GetEnvironmentVariable("NOTIFY_SUBJECT_TEMPLATE") ?? "notify.user.{0}.desktop",
        AckSubject = Environment.GetEnvironmentVariable("NOTIFY_ACK_SUBJECT") ?? "notify.ack.desktop",
    };
}

public sealed class NatsTelemetryPublisher : ITelemetryPublisher
{
    private readonly INatsConnection _nats;
    private readonly string _subject;

    public NatsTelemetryPublisher(INatsConnection nats, string subject)
    {
        _nats = nats;
        _subject = subject;
    }

    public async ValueTask PublishAckAsync(AckPayload ack, CancellationToken ct = default) =>
        await _nats.PublishAsync(_subject, AckJson.Serialize(ack), cancellationToken: ct)
            .ConfigureAwait(false);
}

/// <summary>Composition root: identity → NATS connection → pipeline → live subscription.
/// A plain Core NATS subscription: reconnects resume with future events only (design §6.2).</summary>
public sealed class AgentHost : IAsyncDisposable
{
    private readonly NatsConnection _nats;
    private readonly EventPipeline _pipeline;
    private readonly Aggregator _aggregator;
    private readonly CancellationTokenSource _cts = new();
    private Task? _subscription;

    public string Subject { get; }

    private AgentHost(NatsConnection nats, EventPipeline pipeline, Aggregator aggregator, string subject)
    {
        _nats = nats;
        _pipeline = pipeline;
        _aggregator = aggregator;
        Subject = subject;
    }

    public static async Task<AgentHost> StartAsync(AgentOptions options,
        IIdentityProvider identityProvider, IToastRenderer renderer, CancellationToken ct = default)
    {
        var identity = await identityProvider.GetIdentityAsync(ct).ConfigureAwait(false);
        var nats = new NatsConnection(new NatsOpts { Url = options.NatsUrl });
        await nats.ConnectAsync().ConfigureAwait(false);

        var telemetry = new NatsTelemetryPublisher(nats, options.AckSubject);
        var dedup = new DeduplicationCache(capacity: 10_000, ttl: TimeSpan.FromMinutes(10));
        var (pipeline, aggregator) = AgentPipelineFactory.Create(
            new PipelineOptions(), new AggregatorOptions(), dedup,
            renderer, telemetry, identity.DeviceId, TimeProvider.System);
        pipeline.Start();

        var subject = string.Format(options.SubjectTemplate, identity.UserId);
        var host = new AgentHost(nats, pipeline, aggregator, subject);
        host._subscription = Task.Run(() => host.SubscribeLoopAsync(host._cts.Token), CancellationToken.None);
        return host;
    }

    private async Task SubscribeLoopAsync(CancellationToken ct)
    {
        await foreach (var msg in _nats.SubscribeAsync<byte[]>(Subject, cancellationToken: ct)
            .ConfigureAwait(false))
        {
            if (msg.Data is { Length: > 0 } data)
                _pipeline.TryEnqueue(new ReceivedEvent(data, DateTimeOffset.UtcNow));
        }
    }

    public async ValueTask DisposeAsync()
    {
        _cts.Cancel();
        if (_subscription is not null)
        {
            try { await _subscription.ConfigureAwait(false); }
            catch (OperationCanceledException) { }
        }
        await _pipeline.DisposeAsync().ConfigureAwait(false);
        await _aggregator.DisposeAsync().ConfigureAwait(false);
        await _nats.DisposeAsync().ConfigureAwait(false);
        _cts.Dispose();
    }
}
```

- [ ] **Step 5: Start NATS and run the test suite**

```bash
docker run -d --name nats-test -p 4222:4222 nats:2.10-alpine
dotnet test 2>&1 | tail -5
```
Expected: `Passed!` — 39 total, 0 failed (integration test runs for real, not skipped). If NATS.Net 2.x API drifted (e.g. `PublishAsync` overload shape), fix against the installed package's signatures — the semantic contract (publish bytes to subject / async-enumerate subscription) is stable.

- [ ] **Step 6: Verify the skip path, then commit**

```bash
docker rm -f nats-test
dotnet test --filter NatsIntegrationTests 2>&1 | tail -5
# Expected: Passed! 1 passed (no-op with SKIPPED output line)
docker run -d --name nats-test -p 4222:4222 nats:2.10-alpine   # leave running for later tasks
git add src/NotificationAgent.Core tests/
git commit -m "feat: NATS subscriber/ack publisher, identity providers, AgentHost"
```

---

### Task 8: Console host and TestPublisher — end-to-end smoke on Linux

**Files:**
- Create: `src/NotificationAgent.ConsoleHost/NotificationAgent.ConsoleHost.csproj`, `src/NotificationAgent.ConsoleHost/ConsoleToastRenderer.cs`, `src/NotificationAgent.ConsoleHost/Program.cs`
- Create: `tools/TestPublisher/TestPublisher.csproj`, `tools/TestPublisher/Program.cs`

**Interfaces:**
- Consumes: `AgentHost.StartAsync`, `AgentOptions.FromEnvironment`, `EnvironmentIdentityProvider`, `IToastRenderer`, `ToastRequest` (Task 7/2).
- Produces: runnable dev head + producer stand-in; no new library APIs.

- [ ] **Step 1: Create both projects**

```bash
dotnet new console -n NotificationAgent.ConsoleHost -o src/NotificationAgent.ConsoleHost -f net8.0
dotnet add src/NotificationAgent.ConsoleHost reference src/NotificationAgent.Core
dotnet new console -n TestPublisher -o tools/TestPublisher -f net8.0
dotnet add tools/TestPublisher package NATS.Net --version "2.*"
dotnet sln add src/NotificationAgent.ConsoleHost tools/TestPublisher
```

- [ ] **Step 2: Implement the console host**

Create `src/NotificationAgent.ConsoleHost/ConsoleToastRenderer.cs`:

```csharp
using NotificationAgent.Core.Rendering;

namespace NotificationAgent.ConsoleHost;

/// <summary>Dev stand-in for the Windows renderer: prints "toasts" to stdout.</summary>
public sealed class ConsoleToastRenderer : IToastRenderer
{
    public ValueTask<DateTimeOffset> ShowAsync(ToastRequest toast, CancellationToken ct = default)
    {
        Console.WriteLine($"[TOAST] {toast.Title}");
        Console.WriteLine($"        {toast.Message}");
        if (toast.Attribution is not null) Console.WriteLine($"        — {toast.Attribution}");
        if (toast.ActionLabel is not null) Console.WriteLine($"        [{toast.ActionLabel}] -> {toast.ActionUrl}");
        return ValueTask.FromResult(DateTimeOffset.UtcNow);
    }
}
```

Replace `src/NotificationAgent.ConsoleHost/Program.cs`:

```csharp
using NotificationAgent.ConsoleHost;
using NotificationAgent.Core.Hosting;
using NotificationAgent.Core.Identity;

var options = AgentOptions.FromEnvironment();
await using var host = await AgentHost.StartAsync(
    options, new EnvironmentIdentityProvider(), new ConsoleToastRenderer());

Console.WriteLine($"Agent subscribed to {host.Subject} on {options.NatsUrl}. Ctrl+C to exit.");
var shutdown = new TaskCompletionSource();
Console.CancelKeyPress += (_, e) => { e.Cancel = true; shutdown.TrySetResult(); };
await shutdown.Task;
Console.WriteLine("Shutting down.");
```

- [ ] **Step 3: Implement TestPublisher**

Replace `tools/TestPublisher/Program.cs`:

```csharp
using System.Text;
using System.Text.Json;
using NATS.Client.Core;

// Usage: dotnet run --project tools/TestPublisher -- <userId> [title] [message] [priority] [count]
var userId = args.Length > 0 ? args[0] : "u_demo";
var title = args.Length > 1 ? args[1] : "Invoice ready";
var message = args.Length > 2 ? args[2] : "Invoice INV-8492 is ready for review.";
var priority = args.Length > 3 ? args[3] : "normal";
var count = args.Length > 4 ? int.Parse(args[4]) : 1;

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
    var payload = new
    {
        schemaVersion = "1.0",
        eventId,
        notificationType = "billing.invoice.ready",
        target = new { userId },
        content = new { title, message, secondaryText = "TestPublisher" },
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
```

- [ ] **Step 4: Build everything**

Run: `dotnet build 2>&1 | tail -3`
Expected: `Build succeeded. 0 Warning(s) 0 Error(s)`

- [ ] **Step 5: End-to-end smoke test**

```bash
docker start nats-test 2>/dev/null || docker run -d --name nats-test -p 4222:4222 nats:2.10-alpine
NOTIFY_USER_ID=u_demo dotnet run --project src/NotificationAgent.ConsoleHost > /tmp/claude-1000/-home-cjamhe01385-os-notification/1de87ff4-1fde-4b0e-8d8b-1986b04abb49/scratchpad/agent.log 2>&1 &
AGENT_PID=$!
sleep 5
dotnet run --project tools/TestPublisher -- u_demo "Invoice ready" "Smoke test message" critical
kill $AGENT_PID
cat /tmp/claude-1000/-home-cjamhe01385-os-notification/1de87ff4-1fde-4b0e-8d8b-1986b04abb49/scratchpad/agent.log
```
Expected in publisher output: one `[PUB] evt-...` line, then two `[ACK] ...` lines — one containing `"status":"observed_by_agent"` and one `"status":"submitted_to_windows"`.
Expected in `agent.log`: `Agent subscribed to notify.user.u_demo.desktop ...` then `[TOAST] Invoice ready` / `Smoke test message`.

Also verify batching: `dotnet run --project tools/TestPublisher -- u_demo "Job" "step done" normal 3` (with the agent running) → after ~10 s the agent log shows a single `[TOAST] 3 notifications — billing.invoice.ready`.

- [ ] **Step 6: Commit**

```bash
git add src/NotificationAgent.ConsoleHost tools/ NotificationAgent.sln
git commit -m "feat: console dev host and NATS test publisher; e2e smoke verified"
```

---

### Task 9: Windows head — toast renderer, MSAL/WAM identity, single-instance bootstrap

**Files:**
- Create: `src/NotificationAgent.Windows/NotificationAgent.Windows.csproj`
- Create: `src/NotificationAgent.Windows/WindowsToastRenderer.cs`
- Create: `src/NotificationAgent.Windows/MsalIdentityProvider.cs`
- Create: `src/NotificationAgent.Windows/Program.cs`

**Interfaces:**
- Consumes: `AgentHost.StartAsync(AgentOptions, IIdentityProvider, IToastRenderer, CancellationToken)`, `AgentOptions.FromEnvironment()`, `EnvironmentIdentityProvider`, `IIdentityProvider`/`AgentIdentity`, `IToastRenderer`/`ToastRequest` — all from Tasks 2/7.
- Produces: `DesktopAgent` executable for Windows 11. No downstream consumers.

> **Platform note:** this project is deliberately **not** added to `NotificationAgent.sln`, so Linux-side `dotnet build`/`dotnet test` remain green. Compile-verify on this machine with `EnableWindowsTargeting` (works for restore+compile); run-verify on a Windows 11 machine. MSAL requires an Entra app registration (client ID); until one exists, run with `NOTIFY_USER_ID` set to use `EnvironmentIdentityProvider` — the fallback is wired into `Program.cs` below.

- [ ] **Step 1: Create the project**

Create `src/NotificationAgent.Windows/NotificationAgent.Windows.csproj`:

```xml
<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <OutputType>WinExe</OutputType>
    <AssemblyName>DesktopAgent</AssemblyName>
    <TargetFramework>net8.0-windows10.0.19041.0</TargetFramework>
    <RuntimeIdentifiers>win-x64;win-arm64</RuntimeIdentifiers>
    <Nullable>enable</Nullable>
    <ImplicitUsings>enable</ImplicitUsings>
    <!-- Unpackaged (no MSIX) per-user app; WinAppSDK ships self-contained. -->
    <WindowsPackageType>None</WindowsPackageType>
    <WindowsAppSDKSelfContained>true</WindowsAppSDKSelfContained>
    <!-- Allows compile on non-Windows build hosts; runtime is Windows-only. -->
    <EnableWindowsTargeting>true</EnableWindowsTargeting>
  </PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Microsoft.WindowsAppSDK" Version="1.5.*" />
    <PackageReference Include="Microsoft.Windows.SDK.BuildTools" Version="10.0.*" />
    <PackageReference Include="Microsoft.Identity.Client" Version="4.*" />
    <PackageReference Include="Microsoft.Identity.Client.Broker" Version="4.*" />
  </ItemGroup>
  <ItemGroup>
    <ProjectReference Include="..\NotificationAgent.Core\NotificationAgent.Core.csproj" />
  </ItemGroup>
</Project>
```

- [ ] **Step 2: Implement the Windows toast renderer**

Create `src/NotificationAgent.Windows/WindowsToastRenderer.cs`:

```csharp
using Microsoft.Windows.AppNotifications;
using Microsoft.Windows.AppNotifications.Builder;
using NotificationAgent.Core.Rendering;

namespace NotificationAgent.Windows;

public sealed class WindowsToastRenderer : IToastRenderer
{
    public ValueTask<DateTimeOffset> ShowAsync(ToastRequest toast, CancellationToken ct = default)
    {
        // Text/limit budget (design §7): ≤3 text elements, 1 button, XML ≤5KB.
        // Title/message are already grapheme-truncated by ToastContentFactory.
        var builder = new AppNotificationBuilder()
            .AddText(toast.Title)
            .AddText(toast.Message);

        if (!string.IsNullOrEmpty(toast.Attribution))
            builder.SetAttributionText(toast.Attribution);

        if (toast.ActionLabel is not null && toast.ActionUrl is not null)
        {
            builder.AddButton(new AppNotificationButton(toast.ActionLabel)
                .AddArgument("action", "open")
                .AddArgument("url", toast.ActionUrl));
        }

        var notification = builder.BuildNotification();
        AppNotificationManager.Default.Show(notification);
        return ValueTask.FromResult(DateTimeOffset.UtcNow);
    }
}
```

- [ ] **Step 3: Implement MSAL identity + device ID store**

Create `src/NotificationAgent.Windows/MsalIdentityProvider.cs`:

```csharp
using Microsoft.Identity.Client;
using Microsoft.Identity.Client.Broker;
using NotificationAgent.Core.Identity;

namespace NotificationAgent.Windows;

/// <summary>WAM-brokered silent sign-in (design §8). The application user ID is the
/// Entra object id ("oid" / AuthenticationResult.UniqueId), never the Windows account name.</summary>
public sealed class MsalIdentityProvider : IIdentityProvider
{
    private readonly string _clientId;
    private readonly string _tenantId;

    public MsalIdentityProvider(string clientId, string tenantId)
    {
        _clientId = clientId;
        _tenantId = tenantId;
    }

    public async ValueTask<AgentIdentity> GetIdentityAsync(CancellationToken ct = default)
    {
        var app = PublicClientApplicationBuilder.Create(_clientId)
            .WithAuthority($"https://login.microsoftonline.com/{_tenantId}")
            .WithBroker(new BrokerOptions(BrokerOptions.OperatingSystems.Windows))
            .WithDefaultRedirectUri()
            .Build();

        var scopes = new[] { "User.Read" };
        AuthenticationResult result;
        try
        {
            var accounts = await app.GetAccountsAsync().ConfigureAwait(false);
            var account = accounts.FirstOrDefault()
                ?? PublicClientApplication.OperatingSystemAccount;
            result = await app.AcquireTokenSilent(scopes, account)
                .ExecuteAsync(ct).ConfigureAwait(false);
        }
        catch (MsalUiRequiredException)
        {
            // POC fallback; production would surface a sign-in prompt via the app UX.
            result = await app.AcquireTokenInteractive(scopes)
                .ExecuteAsync(ct).ConfigureAwait(false);
        }

        return new AgentIdentity($"u_{result.UniqueId}", DeviceIdStore.GetOrCreate());
    }
}

/// <summary>Stable per-install device id under %LOCALAPPDATA% (ack field deviceId).</summary>
internal static class DeviceIdStore
{
    public static string GetOrCreate()
    {
        var dir = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
            "DesktopNotificationAgent");
        Directory.CreateDirectory(dir);
        var path = Path.Combine(dir, "device-id");
        if (File.Exists(path)) return File.ReadAllText(path).Trim();
        var id = $"d-{Guid.NewGuid():N}";
        File.WriteAllText(path, id);
        return id;
    }
}
```

- [ ] **Step 4: Implement bootstrap**

Create `src/NotificationAgent.Windows/Program.cs`:

```csharp
using System.Diagnostics;
using Microsoft.Windows.AppNotifications;
using NotificationAgent.Core.Hosting;
using NotificationAgent.Core.Identity;
using NotificationAgent.Windows;

// One instance per interactive session: "Local\" mutexes are session-scoped,
// so two signed-in users each get their own agent (design §2, ADR-001).
using var singleInstance = new Mutex(initiallyOwned: true,
    @"Local\DesktopNotificationAgent", out var isFirstInstance);
if (!isFirstInstance) return;

// Handle action-button clicks: only open well-formed http(s) URLs.
AppNotificationManager.Default.NotificationInvoked += (_, invokedArgs) =>
{
    if (invokedArgs.Arguments.TryGetValue("url", out var url)
        && Uri.TryCreate(url, UriKind.Absolute, out var uri)
        && (uri.Scheme == Uri.UriSchemeHttps || uri.Scheme == Uri.UriSchemeHttp))
    {
        Process.Start(new ProcessStartInfo(uri.ToString()) { UseShellExecute = true });
    }
};
AppNotificationManager.Default.Register();

try
{
    var options = AgentOptions.FromEnvironment();
    IIdentityProvider identity =
        Environment.GetEnvironmentVariable("NOTIFY_AAD_CLIENT_ID") is { Length: > 0 } clientId
            ? new MsalIdentityProvider(clientId,
                Environment.GetEnvironmentVariable("NOTIFY_AAD_TENANT_ID") ?? "organizations")
            : new EnvironmentIdentityProvider();

    await using var host = await AgentHost.StartAsync(options, identity, new WindowsToastRenderer());

    var shutdown = new TaskCompletionSource();
    Console.CancelKeyPress += (_, e) => { e.Cancel = true; shutdown.TrySetResult(); };
    AppDomain.CurrentDomain.ProcessExit += (_, _) => shutdown.TrySetResult();
    await shutdown.Task;
}
finally
{
    AppNotificationManager.Default.Unregister();
}
```

- [ ] **Step 5: Compile-verify on Linux**

Run: `dotnet build src/NotificationAgent.Windows 2>&1 | tail -3`
Expected: `Build succeeded.` (thanks to `EnableWindowsTargeting`). If the WindowsAppSDK targets refuse to run on Linux (older 1.5 patch versions did), this step alone moves to the Windows machine — do NOT add the project to the solution either way.

- [ ] **Step 6: Commit**

```bash
git add src/NotificationAgent.Windows
git commit -m "feat: Windows head with App SDK toasts, WAM/MSAL identity, single-instance bootstrap"
```

- [ ] **Step 7: Windows 11 run-verification checklist (manual, on a Windows 11 machine)**

```powershell
# Prereqs: .NET 8 SDK, reachable NATS (e.g. same docker on the network)
git clone <repo>; cd os-notification
$env:NOTIFY_NATS_URL = "nats://<linux-host>:4222"
$env:NOTIFY_USER_ID  = "u_demo"          # env identity until an Entra app registration exists
dotnet run --project src/NotificationAgent.Windows
# From the Linux box:  dotnet run --project tools/TestPublisher -- u_demo "Hi" "From Linux" critical
```
Expected: a native Windows 11 toast "Hi / From Linux" with a **View** button that opens the browser; the Linux publisher prints both acks. Starting a second `DesktopAgent.exe` in the same session exits immediately (mutex). Record results in the PR description.

---

## Verification sweep (after all tasks)

```bash
export PATH="$HOME/.dotnet:$PATH"
docker start nats-test 2>/dev/null || docker run -d --name nats-test -p 4222:4222 nats:2.10-alpine
dotnet build && dotnet test 2>&1 | tail -5   # Expected: 39 tests, 0 failed
docker rm -f nats-test                        # cleanup
```

## Design-doc coverage map (self-review record)

| Design section | Where covered |
|---|---|
| §4 solution strategy 1–6 | Tasks 9 (per-session agent), 9 (WAM/MSAL), 7 (subject + Core NATS sub), 4 (aggregate), 6/7 (acks) |
| §5 building blocks | Bootstrap→T9, Identity→T7/T9, Subscriber→T7, Intake→T1/T6, Aggregator→T4, Renderer→T2/T8/T9, Telemetry→T5/T7, Local state→T3 |
| §6 runtime views | T6 (pipeline), T7 (plain sub ⇒ offline discard & reconnect-future-only), T4 (burst handling) |
| §7 payload + Windows limits | T1 (schema), T2 (120/500 graphemes), T9 (≤3 texts, 1 button) |
| §8 auth | T9 MSAL/WAM + oid-based user id; restricted NATS credential issuance is backend scope (out of scope, noted) |
| §9 performance baselines | 500/2 → T6; 32 KB/depth 16 → T1; 100 buckets → T4; memory/DB guardrails → Phase 2, out of scope |
| §10 delivery semantics | T5 (statuses + payload), T6/T7 (emission); latency math is backend-side, fields provided |
| §11 Phase 1 checklist | All items covered by T1–T9; structured logging kept minimal (console) — acceptable POC interpretation |
| ADR-001..008 | Respected; no presence service, no retention, aggregator owns priorities |
