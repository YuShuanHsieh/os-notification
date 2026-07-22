# Windows Desktop Notification Agent

A per-user Windows desktop agent that subscribes to a user-scoped [Core NATS](https://nats.io/) subject, deduplicates / aggregates / prioritizes notification events, renders them as Windows 11 toast notifications, and publishes acknowledgement telemetry back to NATS.

## Purpose

Backend services publish notification events to `notify.user.{userId}.desktop`. This agent is the desktop-side consumer: it runs once per interactive Windows session, non-elevated, and turns those events into native toasts while reporting delivery telemetry (`observed_by_agent`, `submitted_to_windows`) on `notify.ack.desktop`.

Delivery contract (by design): **online-only, at-most-once, best-effort**. Plain Core NATS subscription — no JetStream, no replay after reconnect; under overload events are dropped rather than buffered unboundedly.

This repository is the Phase-1 POC of the agent only. The notification-service backend (producer auth, publish path, trace/latency dashboards) is out of scope; a small `TestPublisher` tool stands in for it during development.

## High-level architecture

All logic lives in a cross-platform .NET 10 core library behind two small interfaces (`IToastRenderer`, `IIdentityProvider`), so the whole pipeline is unit-testable on Linux. Two thin "heads" consume it: a console host for Linux development and the real Windows head.

```
NATS  notify.user.{userId}.desktop
  │
  ▼
AgentHost (composition root: identity → NATS connection → pipeline)
  │
  ▼
EventPipeline ── bounded channel (500 events, drop on overflow), 2 workers
  │   ├─ EventParser          bytes → InboundNotification (32 KB / depth-16 / required-field checks)
  │   ├─ DeduplicationCache   bounded (10k) + TTL (10 min), keyed by deduplicationKey
  │   └─ ack: observed_by_agent
  ▼
Aggregator ── priority routing per (aggregationKey, priority) bucket, max 100 buckets
  │   ├─ critical    → render immediately
  │   ├─ important   → 2 s batch window
  │   ├─ normal      → 10 s batch window
  │   └─ replaceable → keep only the latest event per key
  ▼
ToastContentFactory ── single/batch → ToastRequest (title ≤ 120, message ≤ 500 grapheme clusters)
  │
  ▼
IToastRenderer ──► ack: submitted_to_windows ──► NATS notify.ack.desktop
```

### Projects

| Project | Target | Role |
|---|---|---|
| `src/NotificationAgent.Core` | `net10.0` | The entire pipeline: parsing, dedup, aggregation, toast content, telemetry, hosting. No Windows dependencies. |
| `src/NotificationAgent.ConsoleHost` | `net10.0` | Dev head for Linux: renders "toasts" to the console. |
| `src/NotificationAgent.Windows` | `net10.0-windows10.0.19041.0` | Production head: Windows Community Toolkit toasts, MSAL/WAM identity, single-instance mutex. **Not in the solution file** — compiled separately; execution is Windows-only. |
| `tools/TestPublisher` | `net10.0` | Publishes test events and prints acks; stands in for the backend. |
| `tests/NotificationAgent.Core.Tests` | `net10.0` | xUnit suite (uses `FakeTimeProvider` for all timing) plus a NATS integration test that skips itself when no server is on `localhost:4222`. |

### Identity

The Windows account name is never used as identity. `IIdentityProvider` resolves the application user ID and device ID:

- **Windows (production):** `MsalIdentityProvider` — WAM-brokered silent MSAL sign-in; user ID is the Entra object ID (`u_{oid}`), device ID is a stable per-install GUID under `%LOCALAPPDATA%\DesktopNotificationAgent`.
- **Dev/Linux:** `EnvironmentIdentityProvider` — reads `NOTIFY_USER_ID` (required) and `NOTIFY_DEVICE_ID` (defaults to `d-{machinename}`).

### Wire contracts

Inbound events must match the design §7 JSON shape (`schemaVersion`, `eventId`, `notificationType`, `target.userId`, `content.{title,message,secondaryText,image.url}`, `action.{label,url}`, `classification.{priority,aggregationKey,deduplicationKey,replaceable}`, `timestamps.{...}`). `content.image.url` is optional and must be `https`; it renders as a circular avatar via `AppLogoOverride`. Acks are camelCase JSON: `eventId`, `deviceId`, `agentReceivedAt`, `toastSubmittedAt` (omitted when null), `status`.

## Setup

### Prerequisites

- **.NET 10 SDK** — if not installed:
  ```bash
  curl -fsSL https://dot.net/v1/dotnet-install.sh -o /tmp/dotnet-install.sh
  bash /tmp/dotnet-install.sh --channel 10.0
  export PATH="$HOME/.dotnet:$PATH"   # add to your shell profile
  ```
- **NATS server** — easiest via Docker:
  ```bash
  docker run -d --name nats-test -p 4222:4222 nats:2.10-alpine
  ```

### Build and test

```bash
dotnet build
dotnet test
dotnet format NotificationAgent.sln --verify-no-changes --no-restore
```

Builds enforce the repository's Roslyn and StyleCop analyzer rules as errors. The
shared policy lives in `Directory.Build.props` and `.editorconfig`, so the command
line and compatible IDEs use the same C# conventions.

The solution (`NotificationAgent.sln`) contains Core, Core.Tests, ConsoleHost, and TestPublisher, so build/test stay green on Linux. The integration tests in `NatsIntegrationTests.cs` run only when a NATS server is reachable on `localhost:4222` and skip politely otherwise.

The Windows head and its notification-content tests are intentionally excluded from the cross-platform solution. They can still be compiled and tested with the standalone .NET 10 SDK on Linux or WSL:

```bash
dotnet build src/NotificationAgent.Windows
dotnet test tests/NotificationAgent.Windows.Tests
```

### Configuration (environment variables)

| Variable | Default | Used by |
|---|---|---|
| `NOTIFY_NATS_URL` | `nats://127.0.0.1:4222` | all hosts + TestPublisher |
| `NOTIFY_SUBJECT_TEMPLATE` | `notify.user.{0}.desktop` | agent hosts |
| `NOTIFY_ACK_SUBJECT` | `notify.ack.desktop` | agent hosts + TestPublisher |
| `NOTIFY_USER_ID` | *(required in dev)* | `EnvironmentIdentityProvider` |
| `NOTIFY_DEVICE_ID` | `d-{machinename}` | `EnvironmentIdentityProvider` |
| `NOTIFY_AAD_CLIENT_ID` | *(unset → env identity)* | Windows head: enables MSAL/WAM |
| `NOTIFY_AAD_TENANT_ID` | `organizations` | Windows head (with client ID) |

## How to use

### End-to-end smoke on Linux (console host)

Terminal 1 — run the agent:

```bash
export NOTIFY_USER_ID=u_demo
dotnet run --project src/NotificationAgent.ConsoleHost
# Agent subscribed to notify.user.u_demo.desktop on nats://127.0.0.1:4222. Ctrl+C to exit.
```

Terminal 2 — publish test events and watch acks:

```bash
# Usage: dotnet run --project tools/TestPublisher -- <userId> [title] [message] [priority] [count] [imageUrl]
dotnet run --project tools/TestPublisher -- u_demo "Invoice ready" "INV-8492 is ready." normal 3
```

The agent prints an `observed_by_agent` ack per event immediately, batches the three `normal` events into one console toast after the 10-second window, then emits `submitted_to_windows` acks — all visible in the TestPublisher output. Try `critical` as the priority to see immediate rendering.

### Windows head

`NotificationAgent.Windows` is deliberately excluded from the solution. It can be compiled on Linux, WSL, or Windows with the standalone .NET 10 SDK, but it can only run and display notifications on Windows 10/11:

```powershell
dotnet build src/NotificationAgent.Windows
$env:NOTIFY_NATS_URL = "nats://your-nats-host:4222"
# Either dev identity...
$env:NOTIFY_USER_ID = "u_demo"
# ...or real Entra identity via WAM:
# $env:NOTIFY_AAD_CLIENT_ID = "<app registration client id>"
# $env:NOTIFY_AAD_TENANT_ID = "<tenant id>"    # optional, defaults to "organizations"
dotnet run --project src/NotificationAgent.Windows
```

It runs unpackaged (no MSIX), enforces one instance per session via a `Local\` mutex, submits native toasts through `Microsoft.Toolkit.Uwp.Notifications`, and uses Windows protocol activation to open a validated HTTPS action URL in the default browser. It has no additional notification runtime dependency.

`Microsoft.Toolkit.Uwp.Notifications` 7.1.3 is retained as an explicit legacy compatibility dependency because it supports unpackaged desktop notifications without introducing the Windows App SDK runtime. Revisit this choice when a maintained alternative provides the same standalone deployment model.

### Verify the avatar image renders correctly (Windows)

With the Windows head running (above) and pointed at a NATS server reachable from wherever you run `TestPublisher` (the same machine, or any box that can reach `NOTIFY_NATS_URL`), publish one event with an image URL to reproduce a Teams-presence-style toast (circular avatar + two-line text):

```bash
dotnet run --project tools/TestPublisher -- u_demo "Tony Redmond" "is now available" critical 1 "https://i.pravatar.cc/300"
```

(`critical` renders immediately instead of waiting for a batch window; `https://i.pravatar.cc/300` is a public placeholder-avatar service — swap in any reachable https image URL.)

**Expected toast, on the Windows machine:**
- A circular avatar image at the left, cropped from the URL — confirms `AddAppLogoOverride(uri, ToastGenericAppLogoCrop.Circle)`.
- "Tony Redmond" as the first text line, "is now available" as the second.
- "TestPublisher" as small attribution text (from `TestPublisher`'s hardcoded `secondaryText`).
- A "View" button (from `TestPublisher`'s hardcoded `action`); clicking it opens `https://app.example.com/invoices/8492` in the default browser.

**Negative case** — confirm a bad image URL degrades to text-only instead of failing the toast:

```bash
dotnet run --project tools/TestPublisher -- u_demo "Tony Redmond" "is now available" critical 1 "http://not-https.example.com/x.jpg"
```

On the **Windows head**, expect the same toast *without* an avatar (the `http://` URL fails `HttpsUrlPolicy` and is silently dropped) — title, message, attribution, and button still render normally. This case can only be verified there: the **console dev host** prints whatever `ImageUrl` it's given without validating it first (`ConsoleToastRenderer` isn't `HttpsUrlPolicy`-gated — the same is already true of its `[ActionLabel] -> ActionUrl` line, so this isn't a new gap), so it will show `[image] http://not-https.example.com/x.jpg` regardless of scheme.

## Development

- **TDD workflow:** every Core component was built test-first; keep it that way. All time-dependent code takes a `TimeProvider` so tests use `FakeTimeProvider` — no sleeps or polling.
- **Cross-platform rule:** `NotificationAgent.Core` must stay free of Windows dependencies. Anything OS-specific goes in a head behind `IToastRenderer` / `IIdentityProvider`.
- **Design invariants** (do not change casually): channel capacity 500, 2 workers; max payload 32 KB, max JSON depth 16; max 100 aggregate buckets; title/message limits 120/500 grapheme clusters; ack status strings exactly `observed_by_agent` and `submitted_to_windows`.
- **Run one test class:** `dotnet test --filter "FullyQualifiedName~AggregatorTests"`.
- **Integration tests:** start the NATS container (above) before `dotnet test` to include them.
- The full implementation plan, including per-task rationale, lives at `docs/superpowers/plans/2026-07-15-windows-desktop-notification-agent.md`.

### Known gaps (Phase 2 / pending)

- Windows head has not yet been verified on a real Windows 11 machine.
- Persistent dedup state and memory guardrails are Phase-2 items.
- Shutdown does not drain the bounded channel; in-flight events may be dropped on exit.
