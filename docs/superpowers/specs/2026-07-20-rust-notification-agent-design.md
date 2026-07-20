# Rust Notification Agent — Design

**Date:** 2026-07-20
**Status:** Approved (brainstorming session with user)
**Reference implementation:** the C# agent in this repo (`src/NotificationAgent.*`), built from `docs/superpowers/plans/2026-07-15-windows-desktop-notification-agent.md` and `windows_desktop_notification_agent_core_nats_design.html`.

## Goal

A Rust port of the desktop notification agent that speaks the **identical wire API** — same NATS subjects, same §7 event JSON, same ack payloads — so the backend cannot distinguish it from the C# agent, and that produces a **runnable Windows exe cross-compiled from Linux** (the capability the .NET/WinAppSDK toolchain cannot provide).

User decisions from brainstorming:
1. **Scope:** full agent — core library, Linux console head, Windows toast head. The existing C# `tools/TestPublisher` is reused for e2e (wire format is language-neutral); no Rust port of it.
2. **Identity:** device-code OIDC flow replaces WAM (no MSAL/broker exists for Rust); env-var identity remains the dev/default path, same selection logic as C#.
3. **Not strict behavioral parity:** the three defects deferred to Phase 2 in the C# agent are **fixed from the start** in Rust (see "Deliberate improvements").
4. **Structure:** Cargo workspace inside this repo under `rust/`.

## Wire contract (identical to C#, pinned by tests)

- Subscribe: `notify.user.{userId}.desktop` (plain Core NATS, no JetStream). Template overridable via `NOTIFY_SUBJECT_TEMPLATE`.
- Ack publish: `notify.ack.desktop` (overridable via `NOTIFY_ACK_SUBJECT`). Payload camelCase: `eventId`, `deviceId`, `agentReceivedAt`, `toastSubmittedAt` (omitted when null), `status`. Statuses exactly `observed_by_agent` and `submitted_to_windows`; never `published`/`unobserved`.
- Event schema: §7 shape; parse defaults: `deduplicationKey`→`eventId`, `aggregationKey`→`notificationType` (→`"unknown"` if both absent), unknown priority→normal. Required: `eventId`, `target.userId`, `content.title`, `content.message` (reject with field named in error).
- Limits: payload ≤ 32768 bytes; JSON depth ≤ 16 (pre-parse brace/bracket-depth scan over bytes, string-aware); title 120 / message 500 extended grapheme clusters, `…` counted inside the limit.
- Pipeline constants: bounded queue 500, 2 workers, 100 aggregation buckets max, important window 2s, normal window 10s, dedup cache 10 000 entries / 10 min TTL.
- Delivery: online-only, at-most-once, best-effort. Drops counted (`dropped_queue_full`, `dropped_bucket_overflow`), never unbounded buffering.

## Workspace layout

```
rust/
├── Cargo.toml                  # workspace = ["notify-agent-core", "notify-agent-console", "notify-agent-windows"]
├── notify-agent-core/          # lib crate — all logic, fully testable on Linux
│   └── src/
│       ├── model.rs            # InboundNotification, Priority
│       ├── parser.rs           # parse_event(bytes, received_at, seq) -> Result<InboundNotification, ParseError>
│       ├── dedup.rs            # DedupCache::try_add(key) — bounded + TTL, ports C# semantics
│       ├── grapheme.rs         # truncate(s, max_graphemes)
│       ├── toast.rs            # ToastRequest, ToastRenderer trait, content factory (from_single/from_batch)
│       ├── aggregator.rs       # priority routing, batch windows, replaceable, bucket cap, JoinSet of renders
│       ├── pipeline.rs         # bounded mpsc(500), 2 workers: parse → dedup → observed ack → aggregate
│       ├── ack.rs              # AckPayload serde + statuses + TelemetryPublisher trait
│       ├── identity.rs         # IdentityProvider trait, EnvIdentity, DeviceCodeIdentity (OIDC)
│       └── host.rs             # AgentHost: async-nats connect/subscribe → pipeline; NatsTelemetry; graceful shutdown
├── notify-agent-console/       # bin — Linux dev head printing [TOAST] blocks (format mirrors C# ConsoleToastRenderer)
└── notify-agent-windows/       # bin — WinRT toasts; windows-rs deps under [target.'cfg(windows)'.dependencies];
                                #   non-Windows build compiles a stub main, so Linux `cargo build` stays green
```

**Trait seams (mirror the C# interfaces):**
- `trait ToastRenderer { async fn show(&self, toast: &ToastRequest) -> anyhow::Result<DateTime<Utc>>; }` — returns submission timestamp.
- `trait IdentityProvider { async fn identity(&self) -> anyhow::Result<AgentIdentity>; }` with `AgentIdentity { user_id, device_id }`.
- `trait TelemetryPublisher { async fn publish_ack(&self, ack: &AckPayload) -> anyhow::Result<()>; }`.

## Stack

| Concern | Crate | Note |
|---|---|---|
| Runtime, channels, timers | tokio (full) | bounded `mpsc::channel(500)`, `try_send` = drop signal |
| NATS | async-nats | official client, Core pub/sub |
| JSON | serde + serde_json | typed wire structs; camelCase rename-all; skip-null |
| Graphemes | unicode-segmentation | extended grapheme clusters |
| OIDC HTTP | reqwest (rustls-tls) | device-code endpoints only; rustls avoids OpenSSL cross-compile pain |
| Windows toasts | windows (windows-rs) | WinRT `Windows.UI.Notifications` + `Windows.Data.Xml.Dom`; Windows App SDK has no Rust projection |
| Logging | tracing + tracing-subscriber | render failures, drops, subscribe-loop death |
| Errors | thiserror (lib) / anyhow (bins) | |
| Timestamps | chrono (serde feature) | RFC3339 ↔ `DateTimeOffset` compatible |

Dedup cache is hand-rolled (~80 lines), porting the review-proven C# semantics exactly: capacity + TTL, lazy purge, insertion-order eviction queue with exact-expiry stale-entry guard. Keeps `tokio::time::pause()` testability. No moka dependency.

## Deliberate improvements over C# (behavioral, not wire)

1. **Total event ordering.** The subscribe loop (single-threaded) stamps a monotonic `seq: u64` on each `ReceivedEvent`. Buckets order by seq; `replaceable` keeps the highest-seq event; batch "latest" is the true latest. Closes the C# two-worker race where a stale progress value could win.
2. **Bounded graceful drain.** Shutdown sequence: close intake channel → workers drain and exit → flush all buckets → `JoinSet` of in-flight render+ack tasks awaited **with a 5s timeout** → NATS connection closed last. Pending toasts/acks are not silently lost on normal shutdown (C# fire-and-forgets them).
3. **No-hang disposal.** The same 5s timeout is the forced-shutdown path: a hung renderer forfeits its ack; the process always exits.
4. (Minor) Subscribe-loop death is logged and terminates the process with exit code 1 instead of leaving a silent zombie agent.

## Windows head

- **AUMID self-registration:** first run writes `HKCU\Software\Classes\AppUserModelId\NotifyAgent.Rust` (`DisplayName` = "Desktop Notification Agent (Rust)"), per-user, no elevation — the unpackaged-app substitute for WinAppSDK's `Register()`. Toasts via `ToastNotificationManager::CreateToastNotifierWithId(aumid)` and hand-built toast XML: ≤3 text elements (title, message, attribution), 1 action button with the URL as activation argument.
- **Activation:** `Activated` event on each `ToastNotification` while the process runs → parse argument, require absolute `http`/`https` URL → `ShellExecuteW`. Post-exit action-center activation (COM activator) is out of scope, matching the C# POC.
- **Single instance:** `CreateMutexW` on `Local\NotifyAgentRust` — deliberately distinct from the C# mutex so the two heads can be compared side by side; docs warn to run only one per user in normal use (both would toast the same events).
- **Device id:** same file as C# — `%LOCALAPPDATA%\DesktopNotificationAgent\device-id` — so acks correlate to one stable device id regardless of which agent runs.

## Identity

Selection logic identical to C# `Program.cs`: `NOTIFY_AAD_CLIENT_ID` set → OIDC device-code flow (`NOTIFY_AAD_TENANT_ID` defaults to `organizations`); otherwise `EnvIdentity` (`NOTIFY_USER_ID` required, `NOTIFY_DEVICE_ID` optional, default `d-{hostname-lowercase}` on Linux / device-id file on Windows).

Device-code flow (hand-rolled on reqwest, 2 endpoints):
1. POST `https://login.microsoftonline.com/{tenant}/oauth2/v2.0/devicecode` (`client_id`, `scope=openid profile`).
2. Print `user_code` + `verification_uri` to console **and** render them as a toast (via the injected `ToastRenderer`, so it works on both heads).
3. Poll `/oauth2/v2.0/token` (`grant_type=urn:ietf:params:oauth:grant-type:device_code`) at `interval` until success, `authorization_pending` continues, anything else fails with a clear error.
4. `user_id = "u_{oid}"`, oid taken from the id_token payload (base64 JSON decode, **no signature validation** — token arrives directly from Entra over TLS and is used only for local identity selection; documented POC trade-off).
5. Tokens held in memory only; no refresh-token persistence. Each OIDC-mode start re-prompts. Persistence is Phase 2.

## Error handling doctrine

Best-effort, identical to C# where wire-visible: invalid payloads and duplicates drop silently (before any ack — a producer-retried duplicate therefore gets no ack, matching C# and the backend's expectations); render failures are logged and swallowed (never kill a worker); queue/bucket overflow drops and counts. Improvements listed above apply only to shutdown/ordering/observability.

## Testing

- **Unit/behavioral (Linux, `cargo test`, ~40 tests ported from the C# suite):** parser (§7 doc example verbatim, priority mapping, defaults, all four required-field rejections, 32KB, depth-16 including an exact-boundary case, malformed/empty/null), grapheme (short/exact/truncate + ZWJ family-emoji clusters), dedup (first/dup, TTL expiry via paused time, capacity eviction, concurrent single-winner), aggregator (critical immediate, 2s/10s windows via `tokio::time::pause`, replaceable-keeps-latest, per-key separation, bucket-cap drops, **seq-ordering under out-of-order arrival**, **drain-with-timeout: pending renders complete on shutdown; hung renderer forfeits after timeout**), ack JSON shape (camelCase names, null omission), pipeline (both acks with correct timestamps/deviceId, dup processed once, poison payload survives, queue-full drop counting).
- **Integration:** `nats_integration.rs` — end-to-end against `localhost:4222`, politely no-ops when the port is closed (same pattern as the C# test). NATS on this machine is provided by a pre-existing container that must not be touched.
- **E2E smoke:** Rust console head + C# `tools/TestPublisher` (critical single → 2 acks + toast; 3 normal → one aggregated toast) — proves cross-language wire parity live.
- **Windows crate:** compiles on Linux for the Windows target; run-verification on a Windows 11 machine (checklist in the plan).

## Build & toolchain

- `rustup` installed to `~/.cargo` (no root). Stable toolchain.
- Linux dev loop: `cargo build && cargo test` in `rust/` — always green (Windows crate is a stub off-Windows).
- Windows artifact from Linux: `rustup target add x86_64-pc-windows-gnu` + `mingw-w64` (apt, sudo believed available — the plan must verify; fallback if no root: `cargo check --target x86_64-pc-windows-gnu` still gives full type-checking of the Windows crate, and the exe builds on any Windows box or CI runner). Output: single static `notify-agent-windows.exe`, no runtime prerequisites.

## Out of scope

Backend subsystems (unchanged from C# plan scope note); token/refresh persistence; COM toast activator; MSIX packaging; replacing or retiring the C# agent (it remains the reference implementation); porting TestPublisher.

## Success criteria

1. `cargo test` green on Linux with the ported + new test suite.
2. Live e2e smoke on Linux: C# TestPublisher → Rust console agent → both ack lines + toast output, including the 3-event aggregation case.
3. `notify-agent-windows.exe` cross-builds from this Linux machine (or, without root, `cargo check --target` passes and the exe is built on Windows).
4. Byte-level ack compatibility demonstrated (the integration test asserts the exact JSON field set the C# test asserts).
5. Windows 11 run-verification checklist executed later on real hardware (toast, button, mutex, device-code sign-in when a client ID exists).
