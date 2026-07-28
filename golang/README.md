# Go Notification Agent

A Go port of the desktop notification agent, wire-compatible with the C# agent in `../src/NotificationAgent.*` and the Rust port in `../rust`: same NATS subjects, same `content` JSON schema, same acknowledgement contract. See `../docs/superpowers/specs/2026-07-20-rust-notification-agent-design.md` for the design this port also follows (no separate Go design doc exists). This is a behavioral, wire-compatible port, not a line-for-line one — in particular, the Windows head's architecture differs from both references (toast submission via a PowerShell-invoked WinRT script instead of native bindings, and a `systray`-based tray icon instead of WinForms/raw Win32 calls); see "Known gaps" below.

## Layout

| Package | What it is |
|---|---|
| `internal/model` | Normalized, wire-format-independent notification event shape shared by the parser, pipeline, aggregator, and renderers. |
| `internal/clock` | Injectable time source so batching/TTL code is testable without real sleeps. |
| `internal/parser` | Turns raw inbound wire JSON bytes into a normalized `model.InboundNotification`; same size/depth limits, required fields, and defaulting rules as the C#/Rust parsers. |
| `internal/dedup` | In-memory duplicate suppression, bounded by entry count and TTL. Not persistent. |
| `internal/graphemetext` | Unicode-safe truncation by extended grapheme cluster (so a multi-codepoint emoji is never split). |
| `internal/httpsurl` | Shared HTTPS URL safety policy for image/action URLs before Windows toast rendering. |
| `internal/telemetry` | Acknowledgement JSON payload (`observed_by_agent` / `submitted_to_windows`). |
| `internal/identity` | Application identity resolution. Currently only `EnvIdentity` (environment variables) — no AAD/MSAL/device-code sign-in yet. |
| `internal/natsauth` | Pluggable NATS auth. Currently only `CredsFileAuth` (`.creds` file) — no external-auth-service provider yet. |
| `internal/aggregator` | Priority routing, batching, and replaceable collapsing, keyed by `(aggregationKey, priority)`. |
| `internal/toast` | Builds renderer-neutral toast content from one or more notifications. |
| `internal/pipeline` | Bounded intake queue + worker pool at the front of the agent. |
| `internal/host` | The `AgentHost` composition root: resolves identity, connects to NATS, wires dedup/pipeline/aggregator/renderer/ack-publishing together (`Start`/`Shutdown`). |
| `internal/imagecache` | Bounded, HTTPS-only, best-effort local disk cache for remote avatar images. |
| `internal/windowstoast` | Builds the Windows toast notification XML as a pure, cross-platform-testable string builder. |
| `cmd/notify-agent-console` | Linux dev head — subscribes for real, prints `[TOAST]` blocks to stdout instead of showing a native toast. |
| `cmd/notify-agent-windows` | Windows head — tray application; submits real toasts via PowerShell (see "Build the Windows head" below). Builds to a stub `main` on non-Windows targets so the module stays buildable here. |
| `cmd/test-publisher` | Publishes test events and prints acks — the dev-side event producer, ported line-for-line from the Rust/C# `TestPublisher`. |

## Prerequisites

- **Go 1.25** (this module targets `go 1.25.10`; any current Go 1.25.x toolchain works):
  ```bash
  # if not already installed, see https://go.dev/doc/install
  go version
  ```
- **A NATS server** reachable at `nats://127.0.0.1:4222` (default) or wherever `NOTIFY_NATS_URL` points. For local dev:
  ```bash
  docker run -d --name nats-dev -p 4222:4222 nats:2.10-alpine
  ```

## Build and test

```bash
cd golang
test -z "$(gofmt -l .)"
go vet ./...
go build ./...
go test ./...
go test -race ./...
```

`cmd/notify-agent-windows` builds as a stub off Windows (a `//go:build !windows` file that just prints an error and exits), so the whole module builds and tests clean here. `internal/host`'s test suite includes a live NATS integration test that skips cleanly (no failure) if no server is reachable on `127.0.0.1:4222` — start the NATS container above to exercise it for real.

## Run the console head

```bash
export NOTIFY_USER_ID=u_demo
go run ./cmd/notify-agent-console
```

It subscribes to `notify.user.u_demo.desktop`, prints a `[TOAST]` block for each rendered notification, and shuts down gracefully on Ctrl+C.

Publish a test event from another shell:

```bash
go run ./cmd/test-publisher -- u_demo --scenario presence
```

`test-publisher --scenario <name>` drives every schema use case end-to-end (avatar image, action button, priority batching, deduplication, replaceable progress updates) — presets are `presence`, `invoice`, `progress`, `batch`, and `dedup`. Run it with no arguments to print the usage text, which also lists the named flags (`--title`, `--message`, `--priority`, `--count`, `--image-url`, `--action-label`, `--agg-key`, `--dedup-key`, `--replaceable`, `--delay-ms`, ...).

## Configuration (environment variables)

| Variable | Default | Purpose |
|---|---|---|
| `NOTIFY_NATS_URL` | `nats://127.0.0.1:4222` | NATS server to connect to. |
| `NOTIFY_SUBJECT_TEMPLATE` | `notify.user.%s.desktop` | Subscribe subject; `%s` is replaced with the user ID (Go's `fmt.Sprintf` placeholder, same default subject as the C#/Rust `{0}` template). |
| `NOTIFY_ACK_SUBJECT` | `notify.ack.desktop` | Subject the agent publishes acks to. |
| `NOTIFY_USER_ID` | — | Required: the only identity source in this port (see Identity below). |
| `NOTIFY_DEVICE_ID` | `d-{lowercase hostname}` | Optional override for the device identifier in acks. |
| `NOTIFY_NATS_CREDS_FILE` | — | Path to a standard NATS `.creds` file (JWT + NKey seed). If set, both heads authenticate to NATS with it. |

### Identity

This port currently implements **environment identity only**: set `NOTIFY_USER_ID` (and optionally `NOTIFY_DEVICE_ID`). There is no AAD/MSAL sign-in (C#) and no device-code OIDC flow (Rust) here yet — `internal/identity.EnvIdentity` is the only `identity.Provider` implementation. `NOTIFY_AAD_CLIENT_ID` and `NOTIFY_AAD_TENANT_ID` are not read by this agent.

### NATS authentication

By default the agent connects to NATS unauthenticated. Set `NOTIFY_NATS_CREDS_FILE` to authenticate with a standard `.creds` file (`internal/natsauth.CredsFileAuth`, works with both heads). There is no external-auth-service provider in this port (the Windows-only AAD-reused-token flow that C#/Rust support) — `NOTIFY_NATS_AUTH_SERVICE_URL` and `NOTIFY_NATS_AUTH_SERVICE_SCOPE` are not read by this agent.

## Build the Windows head

The Windows binary can be **cross-compiled from Linux** into a real, runnable `.exe` — no Windows machine needed to produce the artifact (only to run and visually verify it), and unlike the Rust head, **no mingw or other cross-toolchain is required** since this Windows target has no cgo dependency:

```bash
cd golang
GOOS=windows GOARCH=amd64 go build -o notify-agent-windows.exe ./cmd/notify-agent-windows
```

Copy the `.exe` to a Windows 10/11 machine and run it (same environment variables as the console head apply). It registers a per-user AppUserModelID (`cmd/notify-agent-windows/aumid.go`), enforces a single running instance per session via a `Local\NotifyAgentGolang` mutex, and runs as a system tray app (no console window) using `github.com/getlantern/systray`: the icon (`cmd/notify-agent-windows/assets/app.ico`) appears in the tray immediately on launch, before the agent finishes connecting to NATS, and right-clicking it shows the running version plus a Close item that shuts the agent down.

Toast rendering has no mature pure-Go WinRT projection to build on (unlike Rust's `windows` crate), so this head builds the same toast XML the OS expects (`internal/windowstoast`) and submits it by shelling out to `powershell.exe -NoProfile -NonInteractive -EncodedCommand ...`, which loads `Windows.UI.Notifications.ToastNotificationManager` via PowerShell's WindowsRuntime `ContentType` accelerator — the same mechanism the BurntToast PowerShell module uses for unpackaged-app toast notifications. This is a documented architecture choice, not a shortcut: it renders avatar images (downloaded and cached under `%LOCALAPPDATA%\DesktopNotificationAgent\image-cache`, best-effort — a bad/oversized/slow image falls back to text-only rather than failing the toast) the same way the C#/Rust heads do.

**Manual smoke-test checklist** (the tray icon and PowerShell toast submission have no automated coverage — they need a live Windows desktop session; a successful cross-compile only proves the code compiles, not that it behaves correctly at runtime):
- Launch the `.exe` — the tray icon appears immediately, before the agent has finished connecting to NATS.
- Right-click the icon — the menu shows "Version 0.1.0" (disabled, not clickable) and "Close".
- Click Close — the icon disappears immediately and the process exits (check Task Manager) within ~5 seconds.
- Point `NOTIFY_NATS_URL` at an unreachable host, relaunch — the tray icon still appears, the tooltip flags the failure ("... (agent failed to start)"), and Close still terminates the process immediately.
- Publish an event with an image URL (`go run ./cmd/test-publisher -- u_demo "Tony Redmond" "is now available" critical 1 "https://i.pravatar.cc/300"`) and confirm a real toast renders with the circular avatar, title, message, and (if configured) action button.

## Troubleshooting

- **`go test ./...` hangs or the integration test fails oddly:** confirm nothing else is bound to port 4222, and that the NATS container is actually running (`docker ps`).
- **Console head exits immediately with an identity error:** you need `NOTIFY_USER_ID` set.
- **Windows cross-compile fails:** confirm your Go toolchain supports the target (`go tool dist list | grep windows`) — no additional linker or C toolchain should be needed for this module.
- **No image appears in a Windows toast:** the agent downloads images best-effort and silently falls back to a text-only toast on any failure (bad scheme, oversize body, slow server, non-image content type); check `internal/imagecache` behavior if you need to debug further.
- **Toast never appears / PowerShell errors:** confirm `powershell.exe` is on `PATH` and that Windows notifications are enabled for the current user session; `cmd/notify-agent-windows/renderer.go`'s PowerShell invocation surfaces `CombinedOutput()` in its returned error.

## Known gaps

- No AAD/MSAL sign-in and no device-code identity flow — environment identity (`NOTIFY_USER_ID`) is the only supported identity source.
- No external-auth-service NATS authentication — only `.creds`-file auth is implemented.
- Windows toast rendering goes through a `powershell.exe`-invoked WinRT script rather than native bindings. This is an intentional architecture choice (no mature pure-Go WinRT projection exists), not a defect, but it does mean toast submission depends on `powershell.exe` being present and functional.
- The tray icon uses `github.com/getlantern/systray` rather than raw Win32 `Shell_NotifyIconW` calls.
- Same as the C# and Rust implementations: the Windows head's live/visual behavior (tray icon, toast rendering, Close lifecycle) has not been verified on a real Windows desktop from this environment — only cross-compilation has been confirmed.
