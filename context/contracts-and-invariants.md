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
- A Windows account name is never the application identity. Production identity
  uses the Entra object ID prefixed with `u_`; the device ID is stable per install.

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
