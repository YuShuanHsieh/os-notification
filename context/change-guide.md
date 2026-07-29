# Change Guide

Use this map to identify the complete change surface before editing. Read the
referenced context documents for contracts and validation expectations.

## Add or change an inbound field

Review and usually update:

1. `Serialization/EventParser.cs` wire DTO and mapping.
2. `Models/InboundNotification.cs` if the normalized pipeline needs the field.
3. `EventParserTests.cs` for valid, missing/default, and malformed behavior.
4. Downstream content/renderer/tool code that consumes or produces the field.
5. [`contracts-and-invariants.md`](contracts-and-invariants.md) and the human
   `README.md` if the public wire shape changes.

`tools/TestPublisher` is the repository's sample producer; keep its payload useful
for exercising new user-visible fields.

## Change batching, priority, replacement, or deduplication

- Parsing/default semantics live in `EventParser`.
- Deduplication happens before the first acknowledgement in `EventPipeline`.
- Aggregation behavior lives in `Aggregator`; renderer-neutral summary text lives
  in `ToastContentFactory`.
- Time-dependent tests belong in `AggregatorTests` or
  `DeduplicationCacheTests` and should use fake time.
- Re-check bounds, ack timing, concurrency, disposal/flush behavior, and whether a
  batch still acknowledges every source event.

## Add a renderer or operating-system host

- Implement `IToastRenderer` outside Core.
- Keep content normalization in Core where it is platform-independent.
- Put native APIs, startup rules, and packaging in the host project.
- Validate externally supplied URLs before passing them to native launch or media
  APIs.
- Add host-specific tests and document whether the project belongs in the
  cross-platform solution.

## Change identity or configuration

- Preserve the `IIdentityProvider` boundary and do not substitute the OS account
  name for application identity, except the already-documented, narrowly scoped
  Windows-head exceptions (C#, Rust, Go — see `contracts-and-invariants.md`).
  Do not broaden that exception to the console/dev hosts or to new call sites
  without updating the contract and this guidance together.
- Trace environment ownership across `AgentOptions`,
  `EnvironmentIdentityProvider`, Windows `Program.cs`, `MsalIdentityProvider`, and
  `TestPublisher`.
- Update [`configuration-and-runtime.md`](configuration-and-runtime.md) and the
  root `README.md` for new or changed operator-facing configuration.

## Change acknowledgement or NATS behavior

- Trace `AgentHost` -> `EventPipeline` -> aggregator render callback ->
  `NatsTelemetryPublisher`/`AckJson`.
- Check both ack phases, timestamps, one-ack-per-source behavior for batches,
  cancellation, and failure semantics.
- Contract changes require exact serialization tests and live NATS validation when
  available.
- Introducing JetStream, replay, retry, persistence, or guaranteed delivery is an
  architectural/product-contract change, not a local transport refactor.

## Change toast content

- Use `ToastContentFactory` for platform-neutral title/message/batch behavior.
- Use `WindowsToastContentFactory` only for Windows XML and native capabilities.
- Maintain grapheme-based length limits and HTTPS safety rules.
- Update Core content tests and Windows XML tests for any field that crosses both
  layers.

## Completion checklist

- The changed behavior has a focused test or a stated reason it cannot be
  automated.
- Relevant project builds/tests ran, with live NATS and real Windows coverage
  reported accurately.
- Cross-platform Core and bounded-resource guarantees remain intact.
- Public contracts, README instructions, and `context/` agree with the code.
- No unrelated worktree changes or generated artifacts were modified.
