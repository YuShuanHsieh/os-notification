# Product Requirements Document — Windows Desktop Notification Agent

**Status:** Extracted from the current implementation (both `src/NotificationAgent.*` and `rust/notify-agent-*`), the original design (`windows_desktop_notification_agent_core_nats_design.html`), and `context/*.md`, as of `main` @ `548f1cd`.
**Owner:** Desktop agent team.
**Scope of this document:** The desktop-side consumer only. A backend notification/publisher service is explicitly out of scope of this repository; `tools/TestPublisher` and `rust/test-publisher` stand in for it during development.

This is a retrospective PRD: it describes the product as the shipped code, tests, and docs currently define it, not a forward-looking proposal. Where the original design's roadmap items remain unbuilt, they're called out explicitly under [Roadmap status](#roadmap-status) rather than folded into the requirements as if delivered.

---

## 1. Problem statement

Backend services need to notify a signed-in Windows user in near-real time — an invoice is ready, a colleague came online, a long job finished — without operating a durable per-user mailbox or presence service. The agent is the thin, disposable client that turns a fire-and-hose event stream into native, actionable Windows toasts, while reporting enough telemetry for the backend to measure whether users are actually seeing what's sent.

## 2. Goals and non-goals

| | |
|---|---|
| **Primary goal** | Deliver low-latency notifications to users who are currently online. |
| **Observability goal** | Measure latency and delivery only for events an agent actually acknowledges — never claim a successful publish means a user saw anything. |
| **Non-goal** | No offline queue, no guaranteed delivery, no user-presence service. |
| **Non-goal** | No backend/producer implementation — this repo owns the desktop consumer only. |
| **Non-goal** | No persistence of notification history, user preferences, or cross-device state (see [Roadmap status](#roadmap-status)). |

## 3. Target users and context

- **End user:** a signed-in, interactive Windows 11 desktop session holder. The agent runs non-elevated, one instance per session, started per user (not per machine).
- **Producer (out of scope, assumed):** an internal backend service that authenticates, normalizes payloads, and publishes to NATS on the user's behalf.
- **Operator/developer:** runs the console or Windows head locally against a NATS server, using `tools/TestPublisher` (C#) or `rust/test-publisher` (Rust) to simulate the backend during development.

## 4. Product boundary and delivery contract

- Transport is **plain Core NATS**, not JetStream — no durable stream, no replay, no offline recovery.
- Delivery is **online-only, at-most-once, best-effort**. If no subscriber is connected when a message publishes, it is silently and permanently discarded. This is an accepted product decision, not a defect.
- The backend must never represent a successful NATS publish as "the user saw this."
- Reconnecting after a drop only resumes future messages; nothing missed during the gap is redelivered.

## 5. System overview

```
Trusted app / backend (out of scope)
  → Notification service (out of scope; TestPublisher / test-publisher stand in for it)
  → Core NATS publish:  notify.user.{userId}.desktop
  → Active agent(s) receive the event
  → Event intake: deserialize, validate, deduplicate
  → observed_by_agent acknowledgement
  → Aggregator: priority routing, batching, replaceable-state handling
  → Renderer: native Windows toast (or console diagnostic output in dev)
  → submitted_to_windows acknowledgement
  → Core NATS publish:  notify.ack.desktop
```

The agent exists as **two parallel, wire-compatible implementations** sharing the same contracts:

| | C# (`src/NotificationAgent.*`) | Rust (`rust/notify-agent-*`) |
|---|---|---|
| Runtime | .NET 10 | Rust 2021, Tokio |
| Cross-platform core | `NotificationAgent.Core` | `notify-agent-core` |
| Linux/dev head | `NotificationAgent.ConsoleHost` | `notify-agent-console` |
| Windows head | `NotificationAgent.Windows` | `notify-agent-windows` |
| Dev event publisher | `tools/TestPublisher` | `rust/test-publisher` (full-parity port) |
| Status | Original POC; still maintained | Full rewrite, now the more actively developed track — has picked up features (tray icon, avatar images, pluggable NATS auth, WebSocket transport) in lockstep with or ahead of C# |

Both implementations are built behind the same two abstraction seams — a toast-renderer interface and an identity-provider interface — so the entire event-processing pipeline is unit-testable on Linux without any Windows dependency, and the only OS-specific code lives in each "Windows head."

## 6. Functional requirements

### 6.1 Event intake and validation

- Subscribe to a per-user NATS subject, default template `notify.user.{0}.desktop`, `{0}` = the resolved application user ID.
- Accept inbound bytes into a **bounded** intake queue (default capacity 500) serviced by a fixed worker pool (default 2 workers). A full queue rejects new events rather than growing unbounded.
- Parse and validate the inbound JSON payload (see [§8 wire contract](#8-wire-contracts)):
  - Reject payloads over **32 KiB**.
  - Reject JSON nested deeper than **16 levels**.
  - Require nonblank `eventId`, `target.userId`, `content.title`, `content.message`; reject otherwise.
  - Apply deliberate defaults for everything else: missing/unrecognized priority → `normal`; missing aggregation key → `notificationType` (or `unknown`); missing dedup key → `eventId`; missing `replaceable` → `false`. `schemaVersion` is parsed but not currently used to reject a payload.
  - Invalid or malformed payloads are dropped silently at this stage — no acknowledgement, no crash, no effect on other events in flight.

### 6.2 Deduplication

- Suppress duplicate events by `deduplicationKey` using a bounded, TTL-based cache (default capacity 10,000 keys, 10-minute TTL).
- A duplicate is dropped before any acknowledgement is sent; the first-seen event is the one that proceeds.

### 6.3 Prioritization, batching, and replacement

Events route into `(aggregationKey, priority)` buckets (bounded to a default of 100 concurrently active buckets):

| Priority | Behavior |
|---|---|
| `critical` | Render immediately, bypassing any batch window. |
| `important` | Batch within a 2-second window. |
| `normal` | Batch within a 10-second window. |
| `replaceable` | Keep only the latest event per key — used for progress/state-style updates where only the newest value matters. |

A batch of N events produces exactly one rendered toast, using the most recently observed event as the "latest" value (worker concurrency makes strict ordering best-effort), but still emits one `submitted_to_windows` acknowledgement per source event in the batch.

### 6.4 Toast rendering

- Render a single, renderer-neutral `ToastRequest` (title, message, optional secondary text, optional avatar image, optional single action button) from either a lone event or a batch summary.
- Enforce **grapheme-cluster-aware** truncation (never split an emoji or composed Unicode character) at 120 characters for the title and 500 for the message.
- Two renderer implementations per language: a Windows-native renderer (real toasts) and a console/diagnostic renderer for Linux development (prints a `[TOAST]` block instead of showing UI; not a fidelity or security equivalent of the Windows path).

### 6.5 Avatar images (schema 1.1)

- An optional `content.image.url` renders as a circular (or square) avatar via the platform's app-logo-override mechanism.
- Only `https://` image URLs are accepted; anything else (or anything that fails download/type/size checks) silently degrades to a text-only toast rather than failing the notification.
- Images are downloaded best-effort with bounded size (3 MB) and time (3 s) limits, and cached (bounded eviction) under a per-user local app-data directory on the Windows heads so repeated avatars don't re-download.
- The console dev renderer does **not** apply this HTTPS gate — it prints whatever URL it's given, by design, as a visibility aid only.

### 6.6 Actions

- An optional single action (`label` + `url`) renders as a toast button. Both fields must be present and the URL must pass the same HTTPS validation as images; otherwise the action is omitted (not the whole toast).
- Clicking the action opens the URL in the system default browser via OS protocol activation.

### 6.7 Acknowledgement telemetry

- Publish exactly two ack statuses per observed event, camelCase JSON, to a configurable subject (default `notify.ack.desktop`):
  - `observed_by_agent` — emitted right after parse + dedup succeed, before aggregation.
  - `submitted_to_windows` — emitted after the renderer successfully hands the toast to the OS; one per **source** event represented in a batch, not one per rendered toast.
- Ack shape: `eventId`, `deviceId`, `agentReceivedAt`, `toastSubmittedAt` (omitted, not null, when not yet available), `status`.
- The agent never emits backend-only statuses (`published`, `unobserved`) — those are the producer's responsibility to infer from the *absence* of an ack.

### 6.8 Identity

- The Windows account name is **never** used as application identity — by explicit product decision.
- Production (Windows, C#): WAM-brokered silent MSAL sign-in; user ID = Entra object ID as `u_{oid}`; device ID = a stable per-install GUID under `%LOCALAPPDATA%`.
- Production (Windows, Rust): device-code OIDC flow when an AAD client ID is configured (no WAM equivalent exists outside .NET), same `u_{oid}`-style resolved identity.
- Development (both, both languages): environment-variable identity — `NOTIFY_USER_ID` (required) and `NOTIFY_DEVICE_ID` (defaults to a machine-name-derived value).

### 6.9 NATS authentication (pluggable)

Presence-based selection, same pattern in both languages:
1. If a Windows external-auth-service URL + AAD scope are configured (Windows only): call an HTTPS auth service that mints a scoped NATS JWT using a silently-acquired AAD token tied to the same signed-in identity; refreshed automatically on every connect/reconnect.
2. Else if a `.creds` file path is configured (either host, either language): authenticate NATS with that standard JWT+NKey credentials file.
3. Else: connect unauthenticated (today's default, unchanged from the original POC).

### 6.10 Transport flexibility

- `NOTIFY_NATS_URL` accepts both raw NATS (`nats://`) and NATS WebSocket (`ws://`/`wss://`); the client detects the scheme automatically. This lets the agent traverse a WebSocket-terminating load balancer or reverse proxy with no other configuration.

### 6.11 Windows host behavior

- Runs unpackaged (no MSIX), non-elevated, one process per interactive session enforced via a session-scoped named mutex.
- Presents as a **system tray application** (both languages): a placeholder tray icon appears immediately at launch — even before NATS connects — with a right-click menu showing the running version (disabled/informational) and a "Close" action. If startup fails (e.g. unreachable NATS), the icon still appears and its tooltip surfaces the failure; Close still terminates the process within a bounded timeout either way.
- No additional notification-runtime package dependency beyond a legacy-compatible toast library (C#) / native Win32 APIs (Rust) — chosen specifically to avoid requiring the Windows App SDK runtime.

### 6.12 Development/test tooling

- `tools/TestPublisher` (C#) and `rust/test-publisher` (Rust, added by PR #18) are full-parity dev-only event publishers: legacy positional args, named flags, and 5 named scenario presets (`presence`, `invoice`, `progress`, `batch`, `dedup`) that each exercise a distinct product behavior (avatar toast, delayed single toast, replaceable-progress collapsing, aggregation-batch summary, dedup-key collapsing) and print an `EXPECT:` line describing the expected outcome. Neither tool is part of the shipped product; both stand in for the not-yet-built backend.

## 7. Non-functional requirements

| Category | Requirement |
|---|---|
| **Resource bounds** | Bounded intake queue (500), fixed worker count (2), bounded dedup cache (10k keys / 10 min TTL), bounded aggregation buckets (100). Overload sheds load (drops events, increments a counter) rather than growing memory unboundedly. |
| **Payload limits** | Max 32 KiB inbound payload; max JSON depth 16; title ≤120 / message ≤500 grapheme clusters; HTTPS URL length ≤2,048 characters. |
| **Availability/robustness** | A single malformed or failing event must not crash the agent or block other events; parsing/rendering failures are contained per-stage. |
| **Security** | Windows image and action URLs must be absolute, well-formed `https://` with a recognized host and no embedded user-info; enforced in one shared policy so image and action validation cannot drift apart. Application identity is never the OS account name. |
| **Portability** | The entire event-processing pipeline (parse → dedup → aggregate → content-shape) must remain free of Windows-specific dependencies in both languages, runnable and testable on Linux. |
| **Testability** | All time-dependent logic takes an injectable clock (`TimeProvider` / fake-time equivalent) so tests never sleep or poll. Both stacks maintain unit coverage for parsing, dedup, aggregation, content shaping, URL policy, and ack serialization, plus an optional live-NATS integration test that self-skips when no server is reachable. |
| **Cross-language parity** | The C# and Rust agents are wire-compatible: same subjects, same JSON payload/ack shape, same limits, same batching/priority semantics, same NATS-auth and transport options. Divergence between them is treated as a defect. |

## 8. Wire contracts

### 8.1 Inbound event (subject `notify.user.{userId}.desktop`)

```json
{
  "schemaVersion": "1.1",
  "eventId": "evt-12345",
  "notificationType": "billing.invoice.ready",
  "target": { "userId": "u_7f92a845" },
  "content": {
    "title": "Invoice ready",
    "message": "Invoice INV-8492 is ready for review.",
    "secondaryText": "Contoso Billing",
    "image": { "url": "https://.../avatar.png", "shape": "circle" }
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
```

`content.image` and `action` are entirely optional groups (omitted, never emitted as `null`); `schemaVersion` is `"1.1"` by convention when an image is present, `"1.0"` otherwise, though the field is not currently enforced on the consuming side.

### 8.2 Acknowledgement (subject `notify.ack.desktop`, default)

```json
{
  "eventId": "evt-12345",
  "deviceId": "d-456",
  "agentReceivedAt": "2026-07-15T08:30:00.190Z",
  "toastSubmittedAt": "2026-07-15T08:30:00.205Z",
  "status": "submitted_to_windows"
}
```

`toastSubmittedAt` and `status: submitted_to_windows` are absent from the first (`observed_by_agent`) ack for the same event.

### 8.3 Latency model (producer-side, out of scope of this repo but load-bearing on the contract)

```
messaging latency         = agentReceivedAt - serverPublishedAt
agent processing latency  = toastSubmittedAt - agentReceivedAt
observed end-to-end       = toastSubmittedAt - producerCreatedAt
agent observation rate    = acknowledged / published
```

Percentiles only ever include acknowledged events; an unobserved publish affects the observation rate, never the latency distribution — the agent has no way to explain *why* an ack didn't arrive, so the backend must not infer a specific cause beyond "unobserved."

## 9. Configuration reference

| Variable | Default | Applies to |
|---|---|---|
| `NOTIFY_NATS_URL` | `nats://127.0.0.1:4222` | Both agents, both dev publishers. Accepts `ws://`/`wss://` too. |
| `NOTIFY_SUBJECT_TEMPLATE` | `notify.user.{0}.desktop` | Agent hosts only — the dev publishers hardcode the same pattern rather than reading this variable (a known, documented asymmetry). |
| `NOTIFY_ACK_SUBJECT` | `notify.ack.desktop` | Agent hosts and dev publishers. |
| `NOTIFY_NATS_CREDS_FILE` | unset → no auth | Both hosts, both languages: path to a `.creds` file. |
| `NOTIFY_NATS_AUTH_SERVICE_URL` | unset → falls back to creds-file, then none | Windows only: external NATS-JWT-minting HTTPS endpoint. |
| `NOTIFY_NATS_AUTH_SERVICE_SCOPE` | required with the URL above | Windows only. |
| `NOTIFY_USER_ID` | required in dev identity mode | Console hosts; Windows fallback identity. |
| `NOTIFY_DEVICE_ID` | machine-name-derived | Environment identity. |
| `NOTIFY_AAD_CLIENT_ID` | unset → env identity | Windows: enables MSAL/WAM (C#) or device-code (Rust) identity. |
| `NOTIFY_AAD_TENANT_ID` | `organizations` | Windows, with a client ID set. |

## 10. Roadmap status

The original design (`windows_desktop_notification_agent_core_nats_design.html`) laid out three phases. Mapping that plan against what's actually implemented today:

**Phase 1 — POC.** ✅ Complete in both languages: per-user agent, local toast rendering, NATS subscription, one producer request shape with one action button, in-memory dedup and basic aggregation. Identity has since diverged positively from the original plan — Rust's device-code flow and both languages' pluggable NATS auth go beyond what Phase 1 originally scoped.

**Phase 2 — Reliability and observability.** Partially complete:
- ✅ Bounded channel + fixed worker pool.
- ✅ Credential handling (pluggable NATS auth, AAD token refresh) — delivered ahead of where the original roadmap placed it.
- ❌ **Persistent** deduplication/aggregation state — still in-memory only; a restart loses all dedup history (explicitly documented as a known gap).
- ❌ Telemetry **dashboards** / latency percentile computation — the agent emits the raw ack telemetry the contract requires, but computing and visualizing p50/p95/p99 is backend work, out of this repo's scope.
- ❌ Explicit memory soft/hard threshold enforcement (150 MB / 250 MB) or a local database cap (100 MB) from the original design — bounded queues/caches exist, but no direct memory-guardrail mechanism is implemented or tested.
- ❌ Durable shutdown/drain guarantee — shutdown is best-effort; in-flight events may be dropped on exit (documented known gap in both READMEs).

**Phase 3 — Enhancements.** Not started: server-side producer validation/rate limiting (inherently backend-side, out of scope here), user category preferences, localization, richer templates, multi-device/session routing, fleet health/feature flags/synthetic probes.

**Delivered beyond the original roadmap:** a full Rust rewrite wire-compatible with the C# agent; avatar/image toasts (schema 1.1) with bounded caching; system tray UX (icon, version display, graceful Close) in both languages; NATS WebSocket transport; a Docker-based offline-capable Windows cross-build pipeline for Rust; scenario-preset dev tooling in both languages.

## 11. Constraints and assumptions

- The agent assumes a trusted producer already validated at the backend — this repo does no producer authentication or payload authorization of its own beyond the wire-shape checks in §6.1.
- `NotificationAgent.Core` (C#) and `notify-agent-core` (Rust) must remain free of Windows-specific dependencies; all OS integration lives behind the renderer/identity seams in a "head" project.
- The Windows Windows-project (C#) is intentionally excluded from the default solution/build so the cross-platform core stays buildable and testable without a Windows machine; both Windows heads can be *compiled* on Linux but only *run and visually verified* on real Windows 10/11.
- Changing the wire JSON shape, ack status strings, or any documented invariant (queue capacity, worker count, batching windows, size/depth limits) is a deliberate product-contract change requiring updated tests and docs in the same change, not a casual refactor.

## 12. Quality scenarios (acceptance-level)

| Scenario | Expected outcome |
|---|---|
| User is offline when an event publishes | Message is silently and permanently discarded; no ack is ever sent. |
| 10,000 events arrive in one minute | Memory stays bounded; excess normal-priority events are aggregated or dropped rather than accepted without limit. |
| Agent reconnects after a drop | Only future live events are received — nothing is replayed. |
| A NATS publish succeeds | That alone proves nothing about delivery — a producer must not represent it as "delivered." |
| An acknowledgement arrives | It is eligible for latency measurement by the backend. |
| Toast has a `critical` priority | Renders immediately, no batch wait. |
| Toast has a bad/`http://` image URL | Toast still renders, without the avatar — never fails outright. |
| Toast action has only a label or only a URL, not both | The action is omitted; title/message/image still render. |
| Windows agent fails to reach NATS at startup | Tray icon still appears; tooltip surfaces the failure; Close still works within its timeout. |

## 13. Primary risks

| Risk | Mitigation |
|---|---|
| Offline event loss | Accepted explicitly as a product decision; the channel is reserved for ephemeral, time-sensitive notifications, not durable messaging. |
| Missing acknowledgement | Classified as "unobserved" by the backend without inferring a specific cause — the agent cannot explain a silence. |
| Notification overload | Bounded intake queue, priority-aware aggregation/batching; server-side rate limiting remains a Phase-3, backend-side item. |
| Windows suppresses or hides the toast (e.g. focus-assist) | The agent only ever reports "submitted to Windows," never "seen by user" — this is a deliberate telemetry-honesty boundary, not a gap. |
| Two implementations drifting apart | `context/contracts-and-invariants.md` and `context/component-map.md` exist specifically to keep the wire contract and bounds identical across languages; any change to one is expected to update the other in the same commit. |

## 14. Out of scope (explicit)

- Any backend/notification-service implementation: producer authentication, payload normalization, rate limiting, trace storage, latency dashboards.
- Offline delivery, message replay, guaranteed/at-least-once delivery, JetStream or any durable NATS stream.
- A user-presence service.
- Multi-device/session routing beyond "every currently-subscribed session for that user gets the toast."
- Localization, user notification-category preferences, rich/templated notification layouts.
- Mobile or non-Windows notification surfaces (the console renderer is a developer diagnostic tool, not a shipped surface).

## 15. Known gaps (as of this document)

- Rust tray UI has no automated test coverage — it requires a live Windows desktop smoke test every time (`context/testing.md`).
- The C# Windows head has not been fully verified end-to-end on a real Windows 11 machine per the root README's "Known gaps" note (compiles and unit-tests clean on Linux; native behavior needs manual confirmation).
- Deduplication and aggregation state are memory-only; any restart silently forgets both.
- Shutdown does not guarantee draining in-flight events; some may be lost on exit.
- `rust/test-publisher`'s `--count`/`--delay-ms` flags accept a narrower integer range than originally planned only after a post-merge fix (PR #18) — now matches the C# tool's `Int32` bound exactly.

## 16. Appendix: component map

| Path | Responsibility |
|---|---|
| `src/NotificationAgent.Core` | C# cross-platform domain/pipeline (no Windows APIs). |
| `src/NotificationAgent.ConsoleHost` | C# Linux/dev entry point + console renderer. |
| `src/NotificationAgent.Windows` | C# Windows entry point, native toast adapter, MSAL/WAM identity, tray. |
| `tools/TestPublisher` | C# dev-only event publisher. |
| `rust/notify-agent-core` | Rust cross-platform pipeline, identity, NATS host/auth, toast contracts. |
| `rust/notify-agent-console` | Rust Linux/dev head + console renderer. |
| `rust/notify-agent-windows` | Rust Windows head, WinRT toast renderer, image cache, system tray. |
| `rust/test-publisher` | Rust dev-only event publisher (full parity with `tools/TestPublisher`). |

For deeper detail than this document provides, see `context/architecture.md`, `context/contracts-and-invariants.md`, `context/configuration-and-runtime.md`, and the dated design records under `docs/superpowers/specs/`.
