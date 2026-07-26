# Architecture

## Product boundary

This repository is the desktop consumer for user-scoped notifications. A backend
publisher is out of scope; `tools/TestPublisher` is a development substitute. The
C# and Rust agents share the same wire contracts and use plain Core NATS, so
delivery is online-only, at-most-once, and best-effort: there is no durable
stream, replay, or offline recovery.

## Event flow

```text
Core NATS: notify.user.{userId}.desktop
  -> AgentHost subscription
  -> EventPipeline bounded channel
  -> EventParser
  -> DeduplicationCache
  -> observed_by_agent acknowledgement
  -> Aggregator
  -> ToastContentFactory
  -> IToastRenderer (console or Windows)
  -> submitted_to_windows acknowledgement
  -> Core NATS: notify.ack.desktop
```

`AgentHost` is the composition root in both implementations. It resolves identity,
opens NATS, constructs the cache/pipeline/aggregator, and owns subscription and
disposal. Start tracing at `src/NotificationAgent.Core/Hosting/AgentHost.cs` for
C# or `rust/notify-agent-core/src/host.rs` for Rust.

## Processing stages

1. `EventPipeline.TryEnqueue` accepts NATS payload bytes into a bounded channel.
   Full queues reject events and increment `DroppedQueueFull`.
2. Two workers parse payloads. Invalid payloads and duplicates stop here without
   rendering or acknowledgements.
3. A valid, first-seen event receives `observed_by_agent` before aggregation.
4. Critical events render immediately. Important and normal events use separate
   `(aggregationKey, priority)` buckets and timed windows. Replaceable events clear
   prior values in their bucket before being added.
5. `ToastContentFactory` creates one renderer-neutral `ToastRequest`. Batch content
   uses the last observed item as the latest item; worker concurrency makes that
   ordering best-effort.
6. A successful renderer submission returns a timestamp and causes one
   `submitted_to_windows` acknowledgement for every source event represented by
   the toast.

Stage-level processing and rendering exceptions are intentionally contained so a
single bad event cannot terminate the agent. This also means failures may result in
no acknowledgement beyond the stage already reached.

## Dependency direction

```text
ConsoleHost ---------> C# Core <--------- C# Windows
                         ^
                    Core.Tests

Rust Console --------> Rust Core <-------- Rust Windows
                         ^
                    Rust Core tests

TestPublisher (standalone NATS development tool)
```

`NotificationAgent.Core` owns models, parsing, bounded processing, aggregation,
content shaping, telemetry contracts, identity abstractions, and hosting. It must
not reference Windows APIs. Hosts supply `IToastRenderer`; identity is supplied
through `IIdentityProvider`.

## Lifecycle notes

- `AgentHost.DisposeAsync` cancels the subscription, disposes the pipeline, flushes
  aggregator buckets, and closes NATS.
- The current shutdown path is best-effort and is not a durable drain guarantee.
- The deduplication cache is in memory only, so restart loses deduplication state.
- The Windows head enforces one process per interactive session with a `Local\`
  mutex.
- The Rust Windows head creates a visible placeholder tray icon before NATS
  startup completes, runs the async agent behind a Win32 message loop, and keeps
  the tray Close action available when startup fails. Close hides the icon,
  attempts graceful shutdown, and forces process exit after its timeout.
- Rust tray implementation details live in
  `rust/notify-agent-windows/src/tray.rs`; its UI behavior requires a live Windows
  desktop smoke test.
