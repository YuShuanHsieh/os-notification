# Contracts and Invariants

Treat the items here as compatibility boundaries. If a feature intentionally
changes one, update its implementation, focused tests, human documentation, and
this file together.

## Delivery contract

- Transport is plain Core NATS, not JetStream.
- Delivery is online-only, at-most-once, and best-effort.
- Intake overload and aggregation-bucket overflow drop events rather than growing
  memory without a bound.
- Reconnect receives future messages only; it does not replay missed messages.

## Subjects and identity

- Default inbound subject template: `notify.user.{0}.desktop`.
- Default acknowledgement subject: `notify.ack.desktop`.
- `{0}` is formatted with the application user ID returned by
  `IIdentityProvider`.
- Production identity uses the Entra object ID prefixed with `u_` (C#/Rust Windows,
  via AAD/MSAL or device-code sign-in); the device ID is stable per install. The
  console/dev host's identity is simply the value of `NOTIFY_USER_ID`, unchanged —
  operators conventionally set that variable to something already shaped like
  `u_<id>`, but no provider code prepends `u_` itself.
- Deliberate, documented exception: when AAD isn't configured, the C# and Rust
  Windows heads each derive a default identity from the Windows username instead of
  requiring `NOTIFY_USER_ID`, via
  `NotificationAgent.Windows.WindowsUsernameIdentityProvider` in C# and
  `rust/notify-agent-windows/src/windows_identity.rs`'s `WindowsUsernameIdentity` in
  Rust. The username is normalized (domain prefix stripped, lowercased, trimmed)
  then **sanitized**, not rejected: every character outside `[a-z0-9_-]` (including
  wildcard/delimiter characters like `.`, `*`, `>`, and whitespace — Windows account
  names may legitimately contain spaces) is replaced with `_`, since an unsanitized
  value could otherwise turn a per-user subscription into an accidental wildcard
  subscription, or (for a value containing whitespace) get silently misrouted by
  NATS's whitespace-tokenized `SUB` wire format. Because that sanitization alone is
  lossy — two different usernames can sanitize to the same string (e.g.
  `"user.name"` and `"user_name"` both become `user_name`) — an 8-hex-character
  suffix of `SHA-256` over the *normalized, pre-sanitization* username is appended,
  so the final `u_{sanitized}_{hash8}` stays injective (collision-resistant) even
  when the readable prefix collides. This exception is scoped to each Windows head;
  the console host and the AAD/MSAL or device-code sign-in paths are unaffected.
  `notify-agent-core::identity::EnvIdentity` (Rust's shared, cross-platform
  `NOTIFY_USER_ID` provider) is unchanged and still backs the Rust console head. The
  Go Windows head (`golang/cmd/notify-agent-windows`) applies the identical
  sanitize-plus-hash derivation unconditionally rather than as an AAD fallback,
  since this Go port has no AAD/MSAL/device-code identity path at all (see
  `identity.go`/`identity_windows.go` and `golang/internal/identity`'s package
  doc). `golang/internal/identity.EnvIdentity` (the Go console head's
  `NOTIFY_USER_ID` provider) is unchanged.

## Inbound JSON

`EventParser` uses web JSON naming and currently requires nonblank:

- `eventId`
- `target.userId`
- `content.title`
- `content.message`

Recognized optional groups are:

- `schemaVersion`, `notificationType`
- `content.secondaryText`, `content.image.url`
- `action.label`, `action.url`
- `classification.priority`, `aggregationKey`, `deduplicationKey`, `replaceable`
- `timestamps.producerCreatedAt`, `timestamps.serverPublishedAt`

Defaults are deliberate: missing/unknown priority becomes `Normal`; missing
aggregation key uses `notificationType` (or `unknown`); missing deduplication key
uses `eventId`; missing `replaceable` becomes `false`. `schemaVersion` is parsed but
is not currently used for version rejection.

## Acknowledgements

Acknowledgements serialize as camelCase:

- `eventId`
- `deviceId`
- `agentReceivedAt`
- `toastSubmittedAt` (omitted when null)
- `status`

The agent emits exactly `observed_by_agent` after parse/dedup and
`submitted_to_windows` after renderer submission. It does not emit backend-side
statuses such as `published` or `unobserved`.

## Limits and timing

| Invariant | Current value | Owner |
|---|---:|---|
| Maximum payload | 32 KiB | `EventParser.MaxPayloadBytes` |
| Maximum JSON depth | 16 | `EventParser.MaxJsonDepth` |
| Intake capacity | 500 events | `PipelineOptions.QueueCapacity` |
| Worker count | 2 | `PipelineOptions.WorkerCount` |
| Deduplication capacity | 10,000 keys | `AgentHost.StartAsync` |
| Deduplication TTL | 10 minutes | `AgentHost.StartAsync` |
| Aggregation buckets | 100 | `AggregatorOptions.MaxBuckets` |
| Important window | 2 seconds | `AggregatorOptions.ImportantWindow` |
| Normal window | 10 seconds | `AggregatorOptions.NormalWindow` |
| Title limit | 120 grapheme clusters | `ToastContentFactory` |
| Message limit | 500 grapheme clusters | `ToastContentFactory` |
| HTTPS URL length | 2,048 characters | `HttpsUrlPolicy.MaxUrlLength` |

Critical notifications bypass batching. Aggregation buckets are keyed by both
aggregation key and priority. Text truncation must remain grapheme-aware so emoji
and other composed Unicode characters are not split.

## URL safety boundary

Windows image and action URLs must be absolute, well-formed HTTPS URLs with a
recognized host and no user information. Invalid image URLs degrade to text-only;
invalid/missing action URLs or labels omit the action. Keep this validation in the
shared `HttpsUrlPolicy` so image and action behavior cannot drift.
