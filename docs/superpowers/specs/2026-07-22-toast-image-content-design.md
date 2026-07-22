# Toast image content (avatar-style images in notifications)

## Purpose

Today, applications publishing to `notify.user.{userId}.desktop` can only send `content.title`, `content.message`, and `content.secondaryText`. The agent renders these as plain text toasts (title + message + attribution + one action button) — see `WindowsToastRenderer`.

Reference target: notifications like Microsoft Teams' presence toast — https://i0.wp.com/office365itpros.com/wp-content/uploads/2024/06/Notify-when-available-notification.jpg — which pairs a circular avatar image with title/body text ("Tony Redmond" / "is now available"). This design adds an optional image to the wire contract so applications can produce that style of notification, using the Windows App SDK's `AppNotificationBuilder.SetAppLogoOverride`.

## Scope

- **In scope:** one optional circular avatar-style image per notification, sourced from an https URL, rendered via `AppLogoOverride`.
- **Out of scope (not this change):** hero images, inline body images, per-event crop choice (square vs circle), agent-side image download/caching, alt text.

## Wire JSON schema change

Additive, no `schemaVersion` bump. `content` gains an optional nested `image` object:

```json
{
  "schemaVersion": "1.0",
  "eventId": "evt-001",
  "notificationType": "presence.available",
  "target": { "userId": "u_123" },
  "content": {
    "title": "Tony Redmond",
    "message": "is now available",
    "secondaryText": null,
    "image": {
      "url": "https://cdn.example.com/avatars/tony.jpg"
    }
  },
  "action": { "label": "Open chat", "url": "https://teams.example.com/chat/123" },
  "classification": {
    "priority": "normal",
    "aggregationKey": "presence:tony",
    "deduplicationKey": "evt-001",
    "replaceable": true
  },
  "timestamps": {
    "producerCreatedAt": "2026-07-22T10:00:00Z",
    "serverPublishedAt": "2026-07-22T10:00:01Z"
  }
}
```

- `content.image` and `content.image.url` are both optional. Omitting either renders exactly as today (text-only toast).
- `content.image.url` must be an absolute `https` URL — same well-formedness rule already enforced for `action.url` (no embedded userinfo, valid host, ≤ 2048 chars).
- The image is nested as an object (not a flat `content.imageUrl` string) so it can gain fields later (e.g. alt text) without another schema version bump, matching how `action` and `target` are already structured as objects.
- Always rendered as a **circular** crop — no per-event crop selection in this iteration.

## Data flow changes

### `InboundNotification` (Models)

Add one optional field, carried alongside the existing `SecondaryText`:

```csharp
public sealed record InboundNotification(
    string EventId, string UserId, string Title, string Message,
    string? SecondaryText, string? ImageUrl,
    string? ActionLabel, string? ActionUrl,
    EventPriority Priority, string AggregationKey, string DeduplicationKey, bool Replaceable,
    DateTimeOffset? ProducerCreatedAt, DateTimeOffset? ServerPublishedAt, DateTimeOffset ReceivedAt);
```

### `EventParser` (Serialization)

- Add `WireImage { public string? Url { get; set; } }` and a `WireImage? Image` property on `WireContent`.
- The raw `image.url` string is carried straight through to `InboundNotification.ImageUrl`, **unvalidated at parse time** — same permissive pattern already used for `action.url`. A malformed or non-https image URL must not fail parsing of an otherwise-valid event; it's rejected later at render time (image silently omitted) rather than dropping the whole notification.

### `ToastRequest` / `ToastContentFactory` (Rendering)

- Add `ImageUrl` to the `ToastRequest` record.
- `FromSingle`: passes `n.ImageUrl` straight through.
- `FromBatch`: takes `ImageUrl` from the **latest** event in the bucket — the same rule already applied to `SecondaryText`, `ActionLabel`, and `ActionUrl`, since a batch is normally repeated updates about the same subject and the latest avatar is the one that should show.

### `HttpsUrlPolicy` (rename of `ActionUrlPolicy`)

- Pure rename, no behavior change: `ActionUrlPolicy` → `HttpsUrlPolicy` (and its test file `ActionUrlPolicyTests` → `HttpsUrlPolicyTests`). The validation logic (absolute https URL, well-formed, no userinfo, valid host, ≤ `MaxUrlLength` 2048) is generic and now serves two call sites: `action.url` and `content.image.url`.

### `WindowsToastRenderer`

After the existing text/attribution/button setup, before building the notification:

```csharp
if (HttpsUrlPolicy.TryCreate(toast.ImageUrl, out var imageUri))
    builder.SetAppLogoOverride(imageUri, AppNotificationImageCrop.Circle);
```

An invalid or missing image URL means no image is set — the toast still renders as text-only, consistent with how an invalid action URL today just omits the button rather than failing the whole toast.

### `ConsoleToastRenderer` (Linux dev host)

Print the image URL on its own line (e.g. `[image] https://...`) when present, so the image field is visible during local development without a Windows machine.

## Validation / limits

- No new payload-size rule: `content.image.url` is still bounded by the existing 32 KB payload / depth-16 JSON envelope, plus `HttpsUrlPolicy.MaxUrlLength` (2048 chars) at render time.
- No change to the "do not change casually" invariants list (channel capacity, worker count, title/message grapheme limits, ack status strings) — this change only adds one optional field end-to-end.

## Testing plan

- `EventParserTests`: image field round-trips into `InboundNotification.ImageUrl`; missing `content.image` parses fine (null); malformed `content.image.url` (e.g. not an object, wrong type) does not fail parsing of an otherwise-valid event — same tolerance already given to `action.url`.
- `ToastContentFactoryTests`: `FromSingle` carries `ImageUrl`; `FromBatch` takes `ImageUrl` from the latest event in the batch.
- `HttpsUrlPolicyTests` (renamed from `ActionUrlPolicyTests`): existing https/host/userinfo/length cases still pass under the new name; no new cases needed since the same policy instance now serves both URL kinds.
- No new test project. `WindowsToastRenderer` itself has no existing unit tests (it's Windows-only, exercised via the design's manual Windows-head smoke test) — this change follows that existing pattern rather than introducing new test infrastructure for it.

## Non-goals / explicitly deferred

- Hero images, inline body images, multiple images per toast.
- Per-event crop shape (square vs. circle) selection.
- Agent-side image download, caching, retry, or offline fallback — Windows Notification Platform handles the https fetch itself.
- Image alt text / accessibility description field.
