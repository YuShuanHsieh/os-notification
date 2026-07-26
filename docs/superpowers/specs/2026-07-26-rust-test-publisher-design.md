# Rust Test Publisher — Design

**Date:** 2026-07-26
**Status:** Approved (brainstorming session with user)
**Scope:** New crate `rust/test-publisher` — a Rust port of `tools/TestPublisher/Program.cs`. Neither agent changes. New worktree, branch `agent/rust-test-publisher` off `main`.

## Goal

Give the Rust workspace its own dev-side event producer, at full feature parity with the C# `TestPublisher`, so publishing test events no longer requires the .NET SDK when working purely in Rust. The C# tool stays in place (kept as an alternative, per `../tools/TestPublisher`); this is an addition, not a replacement.

## Crate layout

- `rust/test-publisher` — new binary crate, added to `rust/Cargo.toml` workspace `members`.
- Standalone: does not depend on `notify-agent-core` and is not depended on by the other crates.
- Dependencies (all already present elsewhere in the workspace / `Cargo.lock`, so no new crates are introduced): `async-nats` (`websockets` feature, matching `notify-agent-core`), `tokio` (`full`), `serde_json`, `anyhow`, `chrono` (`serde` feature, for RFC3339 UTC timestamps), `rand` (already resolved transitively; used to generate the hex `eventId`, replacing C#'s `Guid.NewGuid():N`).
- CLI parsing is hand-rolled (`std::env::args`), matching the style already used in `notify-agent-console` / `notify-agent-windows` — no `clap`.

## CLI (identical surface to the C# tool)

```bash
cargo run -p test-publisher -- <userId> [title] [message] [priority] [count] [imageUrl]   # legacy
cargo run -p test-publisher -- <userId> --scenario <name> [--flags]
cargo run -p test-publisher -- <userId> [--flags]
```

- `userId` is the required first positional; missing/flag-shaped first arg → usage error, exit 2.
- `--scenario` is mutually exclusive with legacy positionals beyond `userId`.
- Precedence: built-in defaults → scenario preset → legacy positionals → named flags (flags always win).
- Unknown flag or missing flag value → usage error, exit 2.
- Flags: `--title --message --secondary --type --priority --count --image-url --image-shape (circle|square) --action-label --action-url --agg-key --dedup-key --replaceable (no value) --delay-ms --scenario`.
- Scenarios: `presence`, `invoice`, `progress`, `batch`, `dedup` — same field values and `EXPECT:` hint strings as the C# `PublishSpec.ApplyScenario` table (see `2026-07-22-testpublisher-scenarios-design.md`).

## Payload rules (unchanged from C#)

`schemaVersion` = `"1.1"` iff an image is present, else `"1.0"`. `content` omits absent optionals (never emits `null`). `action` included only when both label and url are set. `classification` = `{ priority, aggregationKey (default = type), deduplicationKey (default = eventId), replaceable }`. `timestamps` = `{ producerCreatedAt, serverPublishedAt }`, both `Utc::now()` in RFC3339. Keys are camelCase verbatim, matching the JSON the Rust and C# agents both already parse.

## Runtime behavior (unchanged from C#)

- Env vars: `NOTIFY_NATS_URL` (default `nats://127.0.0.1:4222`), `NOTIFY_ACK_SUBJECT` (default `notify.ack.desktop`).
- Publish subject: `notify.user.{userId}.desktop`.
- Subscribes to the ack subject first, waits 300ms for the subscription to settle, then publishes.
- Prints `[PUB] <eventId> -> <subject> (priority=...)` per publish and `[ACK] <raw body>` per ack received.
- Honors `--delay-ms` between messages in a multi-message run.
- After the last publish, waits 12s (to outlast the 10s aggregation window) before cancelling the ack watcher and exiting 0.
- If the scenario set an `Expect` string, prints `EXPECT: <text>` before publishing.

## Implementation shape

- `PublishSpec` struct holding every field with `Default`-style construction (`PublishSpec::defaults(user_id)`).
- `apply_scenario(&mut spec, name) -> bool`.
- Positional/flag parsing functions mirroring `Program.cs`'s structure, operating on `std::env::args().skip(1)`.
- A per-event `messages: Option<Vec<String>>` field (used by `progress`/`batch`) vs. `count` replication otherwise, same as C#.
- Publish/ack-watch loop using `async_nats::connect` + `subscribe` + `publish`, structured like `notify-agent-core`'s NATS usage.

## Testing

Unit tests for the pure logic (`PublishSpec` defaults, scenario application, positional/flag parsing, payload JSON shape) — no NATS required, mirroring the unit-test style already used in `notify-agent-core`. No NATS integration test, matching the C# tool (which has none — it's a dev tool, verified live).

## Docs

Update `rust/README.md`: show `cargo run -p test-publisher -- u_demo --scenario presence` as the primary publish command in the "Run the console head" section, keep the existing `dotnet run --project ../tools/TestPublisher` instructions as an alternative. Add `test-publisher` to the crate table at the top of the README.

## Verification

`cargo build` / `cargo test` clean across the workspace. Live matrix against the Rust console head + NATS on 4222 — run all 5 scenarios and confirm each `EXPECT` outcome, plus one legacy-form invocation and one mixed-form invocation (usage error, exit 2) to prove parity with the C# tool's documented behavior.

## Out of scope

Changes to either agent (`notify-agent-core`, `notify-agent-console`, `notify-agent-windows`); changes to or removal of `tools/TestPublisher` (C#); a raw/arbitrary-payload publish mode.
