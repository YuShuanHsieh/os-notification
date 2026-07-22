# Toast Image Content Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let applications attach an optional circular avatar image to a notification (`content.image.url`), rendered on Windows via `AppNotificationBuilder.SetAppLogoOverride`, so the agent can produce Teams-presence-style toasts (avatar + title + body).

**Architecture:** Purely additive change threaded through the existing pipeline: `EventParser` parses the new optional `content.image.url` string into `InboundNotification.ImageUrl` (unvalidated, same tolerance as `action.url`); `ToastContentFactory` carries it into `ToastRequest.ImageUrl` (latest-wins for batches); `WindowsToastRenderer` validates it as an https URL and calls `SetAppLogoOverride(uri, AppNotificationImageCrop.Circle)`; `ConsoleToastRenderer` prints it for local dev visibility. A generic `HttpsUrlPolicy` (renamed from `ActionUrlPolicy`) validates both `action.url` and `content.image.url` at render time.

**Tech Stack:** .NET 8, `System.Text.Json`, xUnit, Windows App SDK (`Microsoft.Windows.AppNotifications.Builder`).

**Design doc:** `docs/superpowers/specs/2026-07-22-toast-image-content-design.md`

## Global Constraints

- `content.image` and `content.image.url` are both optional; omitting either must render exactly as today (no regression to existing text-only toasts).
- `content.image.url` must be an absolute `https` URL — validated with the same rule as `action.url`: well-formed, no embedded userinfo, valid host, ≤ 2048 chars (`HttpsUrlPolicy.MaxUrlLength`).
- Image URL is carried through unvalidated at parse time (`EventParser`) — an invalid/malformed URL must never fail parsing of an otherwise-valid event. Validation happens only at render time in `WindowsToastRenderer`; an invalid URL there means no image is set, not a failed toast.
- Image is always rendered with a **circular** crop (`AppNotificationImageCrop.Circle`) — no per-event crop selection.
- No `schemaVersion` bump — this is additive to schema version `1.0`.
- Do not change the existing "do not change casually" invariants: channel capacity 500 / 2 workers, 32 KB payload / depth-16 JSON limits, title/message grapheme limits (120/500), ack status strings `observed_by_agent` / `submitted_to_windows`.
- `NotificationAgent.Core` and `NotificationAgent.ConsoleHost` must keep building and testing on Linux with `dotnet build` / `dotnet test`. `NotificationAgent.Windows` is Windows-only, not in the solution, and cannot be built or tested on this machine — verify it by code inspection only.

---

## File Structure

| File | Change |
|---|---|
| `src/NotificationAgent.Core/Rendering/ActionUrlPolicy.cs` → `HttpsUrlPolicy.cs` | Renamed (Task 1) |
| `tests/NotificationAgent.Core.Tests/ActionUrlPolicyTests.cs` → `HttpsUrlPolicyTests.cs` | Renamed (Task 1) |
| `src/NotificationAgent.Windows/WindowsToastRenderer.cs` | Update `ActionUrlPolicy` reference (Task 1); add `SetAppLogoOverride` call (Task 4) |
| `src/NotificationAgent.Core/Models/InboundNotification.cs` | Add `ImageUrl` field (Task 2) |
| `src/NotificationAgent.Core/Serialization/EventParser.cs` | Add `WireImage`, `WireContent.Image`, map to `ImageUrl` (Task 2) |
| `tests/NotificationAgent.Core.Tests/EventParserTests.cs` | New/updated image-parsing tests (Task 2) |
| `tests/NotificationAgent.Core.Tests/ToastContentFactoryTests.cs` | `Event()` helper gets `imageUrl` param (Task 2, compile fix); new image assertions (Task 3) |
| `src/NotificationAgent.Core/Rendering/ToastRequest.cs` | Add `ImageUrl` field (Task 3) |
| `src/NotificationAgent.Core/Rendering/ToastContentFactory.cs` | Pass `ImageUrl` through in `FromSingle`/`FromBatch` (Task 3) |
| `src/NotificationAgent.ConsoleHost/ConsoleToastRenderer.cs` | Print image URL line (Task 4) |
| `tools/TestPublisher/Program.cs` | Optional `imageUrl` CLI arg for manual end-to-end testing (Task 4) |
| `README.md` | Document `content.image.url` in wire contracts section (Task 5) |

---

### Task 1: Rename `ActionUrlPolicy` → `HttpsUrlPolicy`

Pure mechanical rename — no behavior change. This must land first because Task 4 needs a generically-named policy for both `action.url` and `content.image.url`.

**Files:**
- Modify: `src/NotificationAgent.Core/Rendering/ActionUrlPolicy.cs` → renamed to `src/NotificationAgent.Core/Rendering/HttpsUrlPolicy.cs`
- Modify: `tests/NotificationAgent.Core.Tests/ActionUrlPolicyTests.cs` → renamed to `tests/NotificationAgent.Core.Tests/HttpsUrlPolicyTests.cs`
- Modify: `src/NotificationAgent.Windows/WindowsToastRenderer.cs:21` (reference to the class)

**Interfaces:**
- Produces: `public static class HttpsUrlPolicy { public const int MaxUrlLength = 2048; public static bool TryCreate(string? value, out Uri uri); }` — same signature as before, new name. Later tasks (4) call `HttpsUrlPolicy.TryCreate`.

- [ ] **Step 1: Rename the policy file and class**

```bash
git mv src/NotificationAgent.Core/Rendering/ActionUrlPolicy.cs src/NotificationAgent.Core/Rendering/HttpsUrlPolicy.cs
```

Edit the file's contents — only the class name changes, nothing else:

```csharp
namespace NotificationAgent.Core.Rendering;

public static class HttpsUrlPolicy
{
    public const int MaxUrlLength = 2048;

    public static bool TryCreate(string? value, out Uri uri)
    {
        uri = null!;

        if (string.IsNullOrWhiteSpace(value)
            || value.Length > MaxUrlLength
            || !Uri.TryCreate(value, UriKind.Absolute, out var candidate)
            || !candidate.IsWellFormedOriginalString()
            || candidate.Scheme != Uri.UriSchemeHttps
            || string.IsNullOrWhiteSpace(candidate.Host)
            || !string.IsNullOrEmpty(candidate.UserInfo)
            || Uri.CheckHostName(candidate.IdnHost) == UriHostNameType.Unknown)
        {
            return false;
        }

        uri = candidate;
        return true;
    }
}
```

- [ ] **Step 2: Rename the test file and class**

```bash
git mv tests/NotificationAgent.Core.Tests/ActionUrlPolicyTests.cs tests/NotificationAgent.Core.Tests/HttpsUrlPolicyTests.cs
```

Edit the file's contents — rename the class and every `ActionUrlPolicy` reference to `HttpsUrlPolicy`:

```csharp
using NotificationAgent.Core.Rendering;

namespace NotificationAgent.Core.Tests;

public sealed class HttpsUrlPolicyTests
{
    [Theory]
    [InlineData("https://example.com")]
    [InlineData("https://example.com/path")]
    [InlineData("https://example.com/path?one=1&two=2")]
    [InlineData("https://localhost:8443/path")]
    [InlineData("https://127.0.0.1/path")]
    [InlineData("https://[::1]/path")]
    public void TryCreate_AcceptsValidHttpsUrl(string value)
    {
        var result = HttpsUrlPolicy.TryCreate(value, out var uri);

        Assert.True(result);
        Assert.Equal(Uri.UriSchemeHttps, uri.Scheme);
    }

    [Theory]
    [InlineData("")]
    [InlineData("not-a-url")]
    [InlineData("http://example.com")]
    [InlineData("file:///C:/Windows/System32/cmd.exe")]
    [InlineData("javascript:alert(1)")]
    [InlineData("https://")]
    [InlineData("https://user:password@example.com")]
    [InlineData(@"https:\\example.com\path")]
    public void TryCreate_RejectsUnsafeOrMalformedUrl(string value)
    {
        Assert.False(HttpsUrlPolicy.TryCreate(value, out _));
    }

    [Fact]
    public void TryCreate_RejectsOversizedUrl()
    {
        var value = "https://example.com/" +
                    new string('a', HttpsUrlPolicy.MaxUrlLength);

        Assert.False(HttpsUrlPolicy.TryCreate(value, out _));
    }
}
```

- [ ] **Step 3: Update the reference in `WindowsToastRenderer.cs`**

At `src/NotificationAgent.Windows/WindowsToastRenderer.cs:21`, change:

```csharp
        if (toast.ActionLabel is not null
            && ActionUrlPolicy.TryCreate(toast.ActionUrl, out var actionUri))
```

to:

```csharp
        if (toast.ActionLabel is not null
            && HttpsUrlPolicy.TryCreate(toast.ActionUrl, out var actionUri))
```

This file is in `NotificationAgent.Windows`, which is not in the solution and cannot be built here — verify by reading the diff carefully; there is no automated check available on this machine for this file.

- [ ] **Step 4: Run tests to verify the rename didn't break anything**

Run: `dotnet test --filter "FullyQualifiedName~HttpsUrlPolicyTests"`
Expected: `Passed! - Failed: 0, Passed: 15, Skipped: 0`

Also run the full Linux-buildable suite to make sure nothing else references the old name:

Run: `dotnet build && dotnet test`
Expected: build succeeds, all tests pass (no `ActionUrlPolicy` symbol left anywhere referenced by Core/ConsoleHost/Core.Tests).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: rename ActionUrlPolicy to HttpsUrlPolicy

Generalizes the existing https-only URL validation so it can be reused
for content.image.url, not just action.url. No behavior change."
```

---

### Task 2: Wire schema — `InboundNotification.ImageUrl` and `EventParser`

**Files:**
- Modify: `src/NotificationAgent.Core/Models/InboundNotification.cs:6-20`
- Modify: `src/NotificationAgent.Core/Serialization/EventParser.cs:52-109`
- Modify: `tests/NotificationAgent.Core.Tests/ToastContentFactoryTests.cs:9-16` (compile fix only — `Event()` helper needs a new parameter slot; Task 3 adds the meaningful image assertions)
- Modify: `tests/NotificationAgent.Core.Tests/EventParserTests.cs`

**Interfaces:**
- Consumes: nothing new from earlier tasks.
- Produces: `InboundNotification.ImageUrl` (`string?`) — Task 3's `ToastContentFactory.FromSingle`/`FromBatch` read this field.

- [ ] **Step 1: Add `ImageUrl` to `InboundNotification` (scaffolding)**

Edit `src/NotificationAgent.Core/Models/InboundNotification.cs`:

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
```

- [ ] **Step 2: Fix the `ToastContentFactoryTests.Event()` helper so the solution still compiles**

This helper builds `InboundNotification` positionally; it must gain a matching `imageUrl` slot now that the record has one more member. Task 3 will exercise this parameter with real assertions — this step only restores compilation.

Edit `tests/NotificationAgent.Core.Tests/ToastContentFactoryTests.cs`, replacing the `Event` helper:

```csharp
    internal static InboundNotification Event(
        string id = "e1", string title = "Title", string message = "Message",
        EventPriority priority = EventPriority.Normal, string aggKey = "agg.key",
        string? dedupKey = null, bool replaceable = false,
        string? actionLabel = "Open", string? actionUrl = "https://example.com/x",
        string? imageUrl = null) =>
        new(id, "u1", title, message, "App", imageUrl, actionLabel, actionUrl, priority,
            aggKey, dedupKey ?? id, replaceable, null, null,
            DateTimeOffset.Parse("2026-07-15T08:30:00.190Z"));
```

Run: `dotnet build`
Expected: succeeds (Core, ConsoleHost, Core.Tests, TestPublisher all compile).

- [ ] **Step 3: Write failing tests in `EventParserTests`**

Edit `tests/NotificationAgent.Core.Tests/EventParserTests.cs`:

1. Add `Assert.Null(n.ImageUrl);` to the end of `Parses_doc_example_payload` (the doc example has no image — confirms it stays null when omitted):

```csharp
        Assert.Equal(DateTimeOffset.Parse("2026-07-15T08:30:00.100Z"), n.ProducerCreatedAt);
        Assert.Equal(DateTimeOffset.Parse("2026-07-15T08:30:00.150Z"), n.ServerPublishedAt);
        Assert.Equal(ReceivedAt, n.ReceivedAt);
        Assert.Null(n.ImageUrl);
    }
```

2. Add `Assert.Null(n.ImageUrl);` to `Applies_defaults_for_missing_optional_fields`:

```csharp
        Assert.Equal(EventPriority.Normal, n.Priority);
        Assert.False(n.Replaceable);
        Assert.Null(n.ActionLabel);
        Assert.Null(n.ProducerCreatedAt);
        Assert.Null(n.ImageUrl);
    }
```

3. Add a new test, right after `Applies_defaults_for_missing_optional_fields`:

```csharp
    [Fact]
    public void Parses_image_url_when_present()
    {
        var json = """
            {"eventId":"e1","target":{"userId":"u1"},
             "content":{"title":"t","message":"m",
                        "image":{"url":"https://cdn.example.com/avatars/tony.jpg"}}}
            """;

        var ok = _parser.TryParse(Encoding.UTF8.GetBytes(json), ReceivedAt, out var n, out var error);

        Assert.True(ok, error);
        Assert.Equal("https://cdn.example.com/avatars/tony.jpg", n!.ImageUrl);
    }
```

- [ ] **Step 4: Run tests to verify `Parses_image_url_when_present` fails**

Run: `dotnet test --filter "FullyQualifiedName~EventParserTests.Parses_image_url_when_present"`
Expected: FAIL — `Assert.Equal() Failure: Expected: https://cdn.example.com/avatars/tony.jpg, Actual: (null)` (the other two assertions added in Step 3 already pass trivially since `ImageUrl` defaults to null — only this new test is meaningfully red).

- [ ] **Step 5: Implement `WireImage` and wire it into `EventParser`**

Edit `src/NotificationAgent.Core/Serialization/EventParser.cs`. In the `TryParse` method, add `ImageUrl:` to the `InboundNotification` construction, right after `SecondaryText`:

```csharp
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
            AggregationKey: string.IsNullOrWhiteSpace(wire.Classification?.AggregationKey)
                ? type : wire.Classification!.AggregationKey!,
            DeduplicationKey: string.IsNullOrWhiteSpace(wire.Classification?.DeduplicationKey)
                ? wire.EventId! : wire.Classification!.DeduplicationKey!,
            Replaceable: wire.Classification?.Replaceable ?? false,
            ProducerCreatedAt: wire.Timestamps?.ProducerCreatedAt,
            ServerPublishedAt: wire.Timestamps?.ServerPublishedAt,
            ReceivedAt: receivedAt);
```

Add `Image` to `WireContent`, and a new `WireImage` class, right after the `WireContent` class definition:

```csharp
    private sealed class WireContent
    {
        public string? Title { get; set; }
        public string? Message { get; set; }
        public string? SecondaryText { get; set; }
        public WireImage? Image { get; set; }
    }

    private sealed class WireImage { public string? Url { get; set; } }
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `dotnet test --filter "FullyQualifiedName~EventParserTests"`
Expected: `Passed! - Failed: 0, Passed: 14, Skipped: 0`

- [ ] **Step 7: Commit**

```bash
git add src/NotificationAgent.Core/Models/InboundNotification.cs \
        src/NotificationAgent.Core/Serialization/EventParser.cs \
        tests/NotificationAgent.Core.Tests/EventParserTests.cs \
        tests/NotificationAgent.Core.Tests/ToastContentFactoryTests.cs
git commit -m "feat: parse optional content.image.url into InboundNotification

Carried through unvalidated, same as action.url — a malformed image URL
must not fail parsing of an otherwise-valid event; validation happens
at render time."
```

---

### Task 3: `ToastRequest` and `ToastContentFactory` carry the image through

**Files:**
- Modify: `src/NotificationAgent.Core/Rendering/ToastRequest.cs:7-13`
- Modify: `src/NotificationAgent.Core/Rendering/ToastContentFactory.cs:10-30`
- Modify: `tests/NotificationAgent.Core.Tests/ToastContentFactoryTests.cs`

**Interfaces:**
- Consumes: `InboundNotification.ImageUrl` (Task 2).
- Produces: `ToastRequest.ImageUrl` (`string?`) — Task 4's `WindowsToastRenderer` and `ConsoleToastRenderer` read this field.

- [ ] **Step 1: Write failing tests referencing `toast.ImageUrl`**

Edit `tests/NotificationAgent.Core.Tests/ToastContentFactoryTests.cs`. Update `Single_event_maps_fields_directly`:

```csharp
    [Fact]
    public void Single_event_maps_fields_directly()
    {
        var n = Event(imageUrl: "https://cdn.example.com/avatars/tony.jpg");
        var toast = ToastContentFactory.FromSingle(n);

        Assert.Equal("Title", toast.Title);
        Assert.Equal("Message", toast.Message);
        Assert.Equal("App", toast.Attribution);
        Assert.Equal("https://cdn.example.com/avatars/tony.jpg", toast.ImageUrl);
        Assert.Equal("Open", toast.ActionLabel);
        Assert.Equal("https://example.com/x", toast.ActionUrl);
        Assert.Equal(new[] { n }, toast.Sources);
    }
```

Add a new test, right after `Batch_summarizes_count_and_latest_event`:

```csharp
    [Fact]
    public void Batch_takes_image_from_latest_event()
    {
        var batch = new[]
        {
            Event("e1", imageUrl: "https://cdn.example.com/first.jpg"),
            Event("e2", imageUrl: "https://cdn.example.com/second.jpg"),
        };
        var toast = ToastContentFactory.FromBatch(batch);

        Assert.Equal("https://cdn.example.com/second.jpg", toast.ImageUrl);
    }
```

- [ ] **Step 2: Run tests to verify they fail to compile**

Run: `dotnet test --filter "FullyQualifiedName~ToastContentFactoryTests"`
Expected: FAIL — build error `CS1061: 'ToastRequest' does not contain a definition for 'ImageUrl'`.

- [ ] **Step 3: Add `ImageUrl` to `ToastRequest` and wire it through `ToastContentFactory`**

Edit `src/NotificationAgent.Core/Rendering/ToastRequest.cs`:

```csharp
using NotificationAgent.Core.Models;

namespace NotificationAgent.Core.Rendering;

/// <summary>Renderer-ready toast. Sources lists every event this toast represents,
/// so the caller can ack each of them as submitted_to_windows.</summary>
public sealed record ToastRequest(
    string Title,
    string Message,
    string? Attribution,
    string? ImageUrl,
    string? ActionLabel,
    string? ActionUrl,
    IReadOnlyList<InboundNotification> Sources);

public interface IToastRenderer
{
    /// <summary>Submit the toast; returns the submission timestamp (toastSubmittedAt).</summary>
    ValueTask<DateTimeOffset> ShowAsync(ToastRequest toast, CancellationToken ct = default);
}
```

Edit `src/NotificationAgent.Core/Rendering/ToastContentFactory.cs`:

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
            n.SecondaryText, n.ImageUrl, n.ActionLabel, n.ActionUrl, new[] { n });

    /// <summary>Builds one summary toast from a bucket of events. The batch must be in
    /// arrival order — the last element is treated as the latest event and supplies the
    /// message, attribution, and action. Callers append in observed arrival order; under
    /// concurrent intake workers this order is approximate, so "latest" is best-effort.</summary>
    public static ToastRequest FromBatch(IReadOnlyList<InboundNotification> batch)
    {
        if (batch.Count == 0) throw new ArgumentException("batch must not be empty", nameof(batch));
        if (batch.Count == 1) return FromSingle(batch[0]);

        var latest = batch[^1];
        return new ToastRequest(
            GraphemeText.Truncate($"{batch.Count} notifications — {latest.AggregationKey}", MaxTitleGraphemes),
            GraphemeText.Truncate($"Latest: {latest.Message}", MaxMessageGraphemes),
            latest.SecondaryText, latest.ImageUrl, latest.ActionLabel, latest.ActionUrl,
            batch.ToArray());
    }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `dotnet test --filter "FullyQualifiedName~ToastContentFactoryTests"`
Expected: `Passed! - Failed: 0, Passed: 6, Skipped: 0`

Then run the full suite to confirm nothing else regressed (e.g. `AggregatorTests`, which reuses this `Event()` helper):

Run: `dotnet test`
Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add src/NotificationAgent.Core/Rendering/ToastRequest.cs \
        src/NotificationAgent.Core/Rendering/ToastContentFactory.cs \
        tests/NotificationAgent.Core.Tests/ToastContentFactoryTests.cs
git commit -m "feat: carry ImageUrl through ToastRequest and ToastContentFactory

Batches take the image from the latest event, same rule already used
for attribution and action."
```

---

### Task 4: Render the image (Windows + console dev host) and exercise it manually

**Files:**
- Modify: `src/NotificationAgent.Windows/WindowsToastRenderer.cs:9-32`
- Modify: `src/NotificationAgent.ConsoleHost/ConsoleToastRenderer.cs:8-15`
- Modify: `tools/TestPublisher/Program.cs`

**Interfaces:**
- Consumes: `ToastRequest.ImageUrl` (Task 3), `HttpsUrlPolicy.TryCreate` (Task 1).
- Produces: nothing consumed by later tasks — this is a leaf.

Neither `WindowsToastRenderer` nor `ConsoleToastRenderer` has existing unit tests (both are verified manually per the README's "End-to-end smoke on Linux" flow); this task follows that existing pattern rather than introducing new test infrastructure. `NotificationAgent.Windows` is also not buildable on this machine.

- [ ] **Step 1: Add `SetAppLogoOverride` to `WindowsToastRenderer`**

Edit `src/NotificationAgent.Windows/WindowsToastRenderer.cs`:

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

        if (HttpsUrlPolicy.TryCreate(toast.ImageUrl, out var imageUri))
            builder.SetAppLogoOverride(imageUri, AppNotificationImageCrop.Circle);

        if (toast.ActionLabel is not null
            && HttpsUrlPolicy.TryCreate(toast.ActionUrl, out var actionUri))
        {
            builder.AddButton(
                new AppNotificationButton(toast.ActionLabel)
                    .SetInvokeUri(actionUri));
        }

        var notification = builder.BuildNotification();
        AppNotificationManager.Default.Show(notification);
        return ValueTask.FromResult(DateTimeOffset.UtcNow);
    }
}
```

(Note: this also folds in Task 1 Step 3's rename — if that step was already applied, only the new `SetAppLogoOverride` block is new here.)

Verify by inspection: `AppNotificationImageCrop.Circle` and `AppNotificationBuilder.SetAppLogoOverride(Uri, AppNotificationImageCrop)` are part of `Microsoft.Windows.AppNotifications.Builder` (Windows App SDK) — same namespace already imported and used for `AppNotificationButton`/`SetInvokeUri` above. No new using directives needed.

- [ ] **Step 2: Print the image URL in `ConsoleToastRenderer`**

Edit `src/NotificationAgent.ConsoleHost/ConsoleToastRenderer.cs`:

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
        if (toast.ImageUrl is not null) Console.WriteLine($"        [image] {toast.ImageUrl}");
        if (toast.ActionLabel is not null) Console.WriteLine($"        [{toast.ActionLabel}] -> {toast.ActionUrl}");
        return ValueTask.FromResult(DateTimeOffset.UtcNow);
    }
}
```

- [ ] **Step 3: Add an optional image URL argument to `TestPublisher`, for manual end-to-end testing**

Edit `tools/TestPublisher/Program.cs`:

```csharp
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
    var payload = new
    {
        schemaVersion = "1.0",
        eventId,
        notificationType = "billing.invoice.ready",
        target = new { userId },
        content = new { title, message, secondaryText = "TestPublisher", image = imageUrl is null ? null : new { url = imageUrl } },
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

- [ ] **Step 4: Build to confirm everything compiles on Linux**

Run: `dotnet build`
Expected: succeeds for `NotificationAgent.Core`, `NotificationAgent.ConsoleHost`, `NotificationAgent.Core.Tests`, `TestPublisher` (the solution's four Linux-buildable projects). `NotificationAgent.Windows` is not in the solution and is not built by this command.

- [ ] **Step 5: Manually verify the console host prints the image line**

Requires a NATS server reachable at `localhost:4222` (see README "Prerequisites" — `docker run -d --name nats-test -p 4222:4222 nats:2.10-alpine` if not already running).

Terminal 1:

```bash
export NOTIFY_USER_ID=u_demo
dotnet run --project src/NotificationAgent.ConsoleHost
```

Terminal 2:

```bash
dotnet run --project tools/TestPublisher -- u_demo "Tony Redmond" "is now available" critical 1 "https://cdn.example.com/avatars/tony.jpg"
```

Expected in Terminal 1's output: a `[TOAST] Tony Redmond` block that includes the line `        [image] https://cdn.example.com/avatars/tony.jpg`.

- [ ] **Step 6: Commit**

```bash
git add src/NotificationAgent.Windows/WindowsToastRenderer.cs \
        src/NotificationAgent.ConsoleHost/ConsoleToastRenderer.cs \
        tools/TestPublisher/Program.cs
git commit -m "feat: render content.image.url as a circular avatar

Windows head sets AppLogoOverride with Circle crop when the URL passes
HttpsUrlPolicy; console dev host prints it for local visibility;
TestPublisher gained an optional imageUrl arg to exercise the path
end-to-end without a real backend."
```

---

### Task 5: Document `content.image.url` in the README

**Files:**
- Modify: `README.md:60`

**Interfaces:**
- Consumes: nothing (documentation only).
- Produces: nothing.

- [ ] **Step 1: Update the wire contracts line**

At `README.md:60`, change:

```markdown
Inbound events must match the design §7 JSON shape (`schemaVersion`, `eventId`, `notificationType`, `target.userId`, `content.{title,message,secondaryText}`, `action.{label,url}`, `classification.{priority,aggregationKey,deduplicationKey,replaceable}`, `timestamps.{...}`). Acks are camelCase JSON: `eventId`, `deviceId`, `agentReceivedAt`, `toastSubmittedAt` (omitted when null), `status`.
```

to:

```markdown
Inbound events must match the design §7 JSON shape (`schemaVersion`, `eventId`, `notificationType`, `target.userId`, `content.{title,message,secondaryText,image.url}`, `action.{label,url}`, `classification.{priority,aggregationKey,deduplicationKey,replaceable}`, `timestamps.{...}`). `content.image.url` is optional and must be `https`; it renders as a circular avatar via `AppLogoOverride`. Acks are camelCase JSON: `eventId`, `deviceId`, `agentReceivedAt`, `toastSubmittedAt` (omitted when null), `status`.
```

- [ ] **Step 2: Update the TestPublisher usage line**

At `README.md:113`, change:

```markdown
# Usage: dotnet run --project tools/TestPublisher -- <userId> [title] [message] [priority] [count]
```

to:

```markdown
# Usage: dotnet run --project tools/TestPublisher -- <userId> [title] [message] [priority] [count] [imageUrl]
```

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document content.image.url wire field and TestPublisher arg"
```

---

## Verification sweep (after all tasks)

```bash
dotnet build
dotnet test
```

Expected: build succeeds for all four Linux-buildable projects; full test suite passes, including the renamed `HttpsUrlPolicyTests`, updated `EventParserTests`, and updated `ToastContentFactoryTests`. `NotificationAgent.Windows` changes (Task 4, Step 1) are verified by inspection only — flag for a real Windows-11 smoke test before merging, consistent with the README's existing "Known gaps" note that the Windows head has not yet been verified on real hardware.
