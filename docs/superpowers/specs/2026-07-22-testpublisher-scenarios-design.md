# TestPublisher Scenario Presets — Design

**Date:** 2026-07-22
**Status:** Approved (brainstorming session with user)
**Scope:** `tools/TestPublisher/Program.cs` only (dev tool; neither agent changes). Branch `rust-agent`.

## Goal

Let one CLI command exercise every use case of the schema-1.1 notification format — avatar/image toasts, action buttons, priorities, aggregation batching, dedup-key reuse, replaceable progress streams — instead of only the 6 legacy positional args.

## CLI

```bash
dotnet run --project tools/TestPublisher -- <userId> [title] [message] [priority] [count] [imageUrl]   # legacy, unchanged
dotnet run --project tools/TestPublisher -- <userId> --scenario <name> [--flags]
dotnet run --project tools/TestPublisher -- <userId> [--flags]
```

- `userId` stays the required first positional.
- `--scenario` is mutually exclusive with legacy positionals beyond `userId` (mixing → usage error, exit 2).
- Precedence: built-in defaults → scenario preset → legacy positionals → named flags (flags always win).
- Unknown flag or missing flag value → usage error (print usage, exit 2).

### Scenarios (each prints an `EXPECT:` line before publishing)

| Name | Emits | EXPECT line |
|---|---|---|
| `presence` | 1 critical event: title "Tony Redmond", message "is now available", secondaryText "Microsoft Teams", `notificationType "presence.available"`, image `{ url: "https://i.pravatar.cc/96?u=tony", shape: "circle" }`, action `{ "Open chat", "https://teams.example.com/chat/tony" }` | 1 avatar toast, 2 acks |
| `invoice` | 1 normal event = the §7 doc example: type `billing.invoice.ready`, title "Invoice ready", message "Invoice INV-8492 is ready for review.", secondaryText "Contoso Billing", action `{ "View invoice", "https://app.example.com/invoices/8492" }`, no image | 1 toast after ~10s, 2 acks |
| `progress` | 3 normal `replaceable: true` events, aggregationKey `job.progress`, messages `10%`, `60%`, `90%`, title "Export job", 100 ms apart | after ~10s ONE toast showing "90%" |
| `batch` | 3 normal events, aggregationKey `demo.batch`, messages "first", "second", "third", title "Batch demo" | ONE "3 notifications — demo.batch" toast, 6 acks sharing one toastSubmittedAt |
| `dedup` | 3 critical events all with `deduplicationKey: "dedup-demo"` | ONE toast, exactly 2 acks (duplicates drop silently) |

Scenario multi-event runs use unique `eventId`s and the scenario's fixed `deduplicationKey` only where the scenario says so (`dedup`); otherwise dedupKey = eventId (today's rule).

### Named flags (each takes a value unless noted)

`--title`, `--message`, `--secondary`, `--type` (notificationType), `--priority`, `--count`, `--image-url`, `--image-shape` (`circle`|`square`), `--action-label`, `--action-url`, `--agg-key`, `--dedup-key` (fixed key reused across the run's events), `--replaceable` (no value; sets true), `--delay-ms` (inter-event delay, default 0; scenarios may preset it).

## Payload rules (unchanged from today)

`schemaVersion` = `"1.1"` iff an image is present, else `"1.0"`; content built as `Dictionary<string, object>` so absent optionals are omitted (never null); action included only when both label and url are set; camelCase keys verbatim.

## Implementation shape

Single `Program.cs`, hand-rolled parsing, no new dependencies. Structure: a mutable `PublishSpec` record/class holding every field with defaults → `ApplyScenario(spec, name)` → `ApplyLegacyPositionals` → `ApplyFlags` → existing publish/ack-watch loop generalized to read from the spec (per-event message list for multi-message scenarios: `progress`/`batch` carry their three messages; `--count` replicates the single message otherwise). Ack-watcher and `[PUB]` output unchanged.

## Verification

`dotnet build tools/TestPublisher` clean (0 warnings). Live matrix against the Rust console head + NATS on 4222 — run all 5 scenarios and confirm each EXPECT outcome verbatim from the agent log/ack stream; plus one legacy-form invocation to prove back-compat, and one mixed-form invocation to prove the usage error. Transcripts recorded in the implementer report.

## Out of scope

Publishing invalid payloads (`--payload file.json` raw mode); changes to either agent; TestPublisher unit-test infrastructure (none exists; verification is the live matrix).
