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
| `internal/identity` | Application identity resolution. `EnvIdentity` (environment variables) is the shared, cross-platform provider used by the console head — no AAD/MSAL/device-code sign-in here. The Windows head has its own `WindowsUsernameIdentity` provider (`cmd/notify-agent-windows/identity_windows.go`) instead; see "Identity" below. |
| `internal/loglevel` | Parses `NOTIFY_LOG_LEVEL`/the settings file's `logLevel` string into a `log/slog.Level`, shared by both cmd heads. |
| `internal/natsauth` | Pluggable NATS auth. Currently only `CredsFileAuth` (`.creds` file) — no external-auth-service provider yet. |
| `internal/aggregator` | Priority routing, batching, and replaceable collapsing, keyed by `(aggregationKey, priority)`. |
| `internal/toast` | Builds renderer-neutral toast content from one or more notifications. |
| `internal/pipeline` | Bounded intake queue + worker pool at the front of the agent. |
| `internal/host` | The `AgentHost` composition root: resolves identity, connects to NATS, wires dedup/pipeline/aggregator/renderer/ack-publishing/metrics together (`Start`/`Shutdown`). |
| `internal/metrics` | `AgentMetrics` interface (events-received/dropped/render-duration) and its `NullAgentMetrics` no-op default -- deliberately free of any `go.opentelemetry.io/otel*` import, following the same interface-in-`internal/*`, real-implementation-in-the-head-that-needs-it pattern as `identity.Provider`/`toast.Renderer`/`natsauth.Provider`. See "Metrics (OpenTelemetry, Windows head only)" below. |
| `internal/imagecache` | Bounded, HTTPS-only, best-effort local disk cache for remote avatar images. |
| `internal/windowstoast` | Builds the Windows toast notification XML as a pure, cross-platform-testable string builder. |
| `cmd/notify-agent-console` | Linux dev head — subscribes for real, prints `[TOAST]` blocks to stdout instead of showing a native toast. |
| `cmd/notify-agent-windows` | Windows head — tray application; submits real toasts via PowerShell (see "Build the Windows head" below). Builds to a stub `main` on non-Windows targets so the module stays buildable here. |
| `cmd/test-publisher` | Publishes test events and prints acks — the dev-side event producer, ported line-for-line from the Rust/C# `TestPublisher`. |

## Prerequisites

- **Go 1.26** (this module targets `go 1.26.4`; any current Go 1.26.x toolchain works):
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
| `NOTIFY_USER_ID` | — | **Console head only, required.** Not read by the Windows head at all (see Identity below). |
| `NOTIFY_DEVICE_ID` | `d-{lowercase hostname}` | Optional override for the device identifier in acks. Both heads honor it; the Windows head also accepts the settings file's `deviceId` (env wins when both are set). |
| `NOTIFY_NATS_CREDS_FILE` | — | Path to a standard NATS `.creds` file (JWT + NKey seed). If set, both heads authenticate to NATS with it. The Windows head also accepts the settings file's `natsCredsFile` (env wins when both are set). |
| `NOTIFY_LOG_LEVEL` | `info` | Minimum `log/slog` level for both heads: `debug`, `info`, `warn`, or `error` (case-insensitive). The Windows head also accepts the settings file's `logLevel` (env wins when both are set); an unset/blank/unparseable value at either tier falls through to the next, defaulting to `info`. |

### Identity

**The console head** (`cmd/notify-agent-console`) implements environment identity only: set `NOTIFY_USER_ID` (and optionally `NOTIFY_DEVICE_ID`) — `internal/identity.EnvIdentity` is its only `identity.Provider`. There is no AAD/MSAL sign-in (C#) and no device-code OIDC flow (Rust) here. `NOTIFY_AAD_CLIENT_ID` and `NOTIFY_AAD_TENANT_ID` are not read by this agent.

**The Windows head** (`cmd/notify-agent-windows`) no longer requires (or reads) `NOTIFY_USER_ID` at all: it derives a default identity from the current Windows account instead, via `WindowsUsernameIdentity` (`cmd/notify-agent-windows/identity_windows.go`). This calls the wrapped `GetUserNameEx` function (`secur32.dll`, via `golang.org/x/sys/windows`) with `windows.NameSamCompatible` to retrieve the SAM-compatible, domain-qualified name (`DOMAIN\username`, or `MACHINENAME\username` on a machine that isn't domain-joined) — falling back to the plain Win32 `GetUserNameW` (`advapi32.dll`, raw `syscall`/`NewLazySystemDLL` — the same pattern `aumid.go` already uses for `shell32.dll`) if `GetUserNameEx` fails. Either way, only the account-name portion after the domain/machine separator is used — the qualifier itself is discarded, since deployments using this Windows head are expected to guarantee account-name uniqueness themselves. The account name is lowercased and sanitized (every character outside `[a-z0-9_-]` becomes `_`), giving the plain sanitized username as the user ID (e.g. `jdoe`) — see `userIDFromWindowsUsername` in `identity.go`. The result is validated against the same NATS-subject-safety rule `internal/host` enforces (rejecting `.`, `*`, `>`) before it's used.

This is a **deliberate, documented, Windows-heads-only exception** to this product's general rule that the OS account name is never used as identity (see `internal/identity`'s package doc and `../context/contracts-and-invariants.md`) — the C#/Rust Windows heads take the same fallback only when AAD isn't configured; the Go Windows head takes it unconditionally, since it has no AAD/device-code identity path at all. The device ID still defaults to `d-{lowercase hostname}` (`identity.go`'s `defaultWindowsDeviceID`), overridable via `NOTIFY_DEVICE_ID` or the settings file's `deviceId`. This provider cannot be exercised outside a real Windows session; its pure username transformation and validation logic (`identity.go`'s `userIDFromWindowsUsername`) is covered by `identity_test.go` and runs on any platform.

### NATS authentication

By default the agent connects to NATS unauthenticated. Set `NOTIFY_NATS_CREDS_FILE` to authenticate with a standard `.creds` file (`internal/natsauth.CredsFileAuth`, works with both heads). There is no external-auth-service provider in this port (the Windows-only AAD-reused-token flow that C#/Rust support) — `NOTIFY_NATS_AUTH_SERVICE_URL` and `NOTIFY_NATS_AUTH_SERVICE_SCOPE` are not read by this agent.

## Settings file (Windows head only)

The Windows head reads an optional JSON settings file at `%LOCALAPPDATA%\DesktopNotificationAgent\settings.json` (the same base directory the image cache uses) at startup, so an operator can configure a deployed agent without setting environment variables. All fields are optional:

```json
{
  "natsUrl": "nats://127.0.0.1:4222",
  "subjectTemplate": "notify.user.%s.desktop",
  "ackSubject": "notify.ack.desktop",
  "natsCredsFile": "",
  "deviceId": "",
  "logLevel": "info",
  "otelEnabled": false,
  "otelExporterEndpoint": "",
  "otelServiceName": "notify-agent-windows-golang"
}
```

This schema intentionally covers only what the Go Windows head actually acts on — it has no AAD/device-code identity and no external-auth-service NATS auth mode, so unlike C#'s broader settings file, there is no field for either here.

**Precedence per field: environment variable (if set and non-blank) > settings file value (if present and non-blank) > built-in default.** This is implemented in `cmd/notify-agent-windows/settings.go` (`ResolveHostOptions`, `ResolveCredsFile`, `ResolveDeviceID`, `ResolveLogLevel`, `ResolveOtelEnabled`, `ResolveOtelExporterEndpoint`, `ResolveOtelServiceName`), which layers the parsed `Settings` under an already environment-resolved `host.Options` — `host.OptionsFromEnv` and `host.Options` themselves are untouched, since those are shared, cross-platform types. A missing file is normal (never created or required); a malformed file logs a warning and falls back to defaults, never a startup failure. The console head is unaffected — it has no settings file, environment variables only, and never configures metrics at all.

`otelEnabled`/`otelExporterEndpoint`/`otelServiceName` are also overridable via `NOTIFY_OTEL_ENABLED` (parsed with `strconv.ParseBool`), `NOTIFY_OTEL_EXPORTER_ENDPOINT`, and `NOTIFY_OTEL_SERVICE_NAME` respectively — same env-wins-over-file-wins-over-default precedence as every other field. See "Metrics (OpenTelemetry, Windows head only)" below.

## Logging

Both heads use the standard library's `log/slog` (a text handler writing to stderr) as the logging convention across `internal/*` and both `cmd/` heads, covering identity resolution, NATS connect/subscribe, render failures, intake-queue-full and aggregation-bucket-overflow drops, and (Windows only) tray lifecycle events (icon shown, Close clicked, agent-start failure). Notification content itself (titles/messages/URLs) is never logged — only identifiers, counts, and error reasons. The minimum level is `NOTIFY_LOG_LEVEL` (both heads) or, on the Windows head, the settings file's `logLevel` (env wins when both are set); default `info`.

## Metrics (OpenTelemetry, Windows head only)

The Windows head can optionally export three OpenTelemetry metrics over OTLP/HTTP: a counter of events accepted into the pipeline (`agent.events.received`), a counter of dropped events tagged with a `reason` attribute (`agent.events.dropped`, `"queue_full"` or `"bucket_overflow"`), and a histogram of end-to-end per-event processing latency in seconds (`agent.render.duration`, once per source event represented in a rendered toast — a batched toast covering 3 events records 3 observations, not 1). It is **off by default** — set `otelEnabled: true` and a non-blank `otelExporterEndpoint` (settings file) or `NOTIFY_OTEL_ENABLED=true`/`NOTIFY_OTEL_EXPORTER_ENDPOINT=...` (env, wins over the file) to turn it on. `otelServiceName`/`NOTIFY_OTEL_SERVICE_NAME` sets the `service.name` resource attribute (default `notify-agent-windows-golang`).

`internal/metrics.AgentMetrics` is the interface these three calls go through, defined without any dependency on `go.opentelemetry.io/otel*` — following the same "small interface in `internal/*`, real implementation supplied by the head that needs it" pattern as `identity.Provider`/`toast.Renderer`/`natsauth.Provider`. Only `cmd/notify-agent-windows/otelmetrics.go` imports the actual OTel SDK/exporter packages and builds the real implementation (`InitMetrics`, called from `main.go` after the logger is configured); `internal/host`/`internal/pipeline`/`internal/aggregator` only ever see the interface, defaulting to `internal/metrics.NullAgentMetrics{}` (a no-op) when nothing is supplied. **`cmd/notify-agent-console` never imports any `go.opentelemetry.io/otel*` package at all** — verify with `go list -deps ./cmd/notify-agent-console/... | grep opentelemetry` (expect no output).

**Metrics-recording code can never crash the agent**, by explicit design: every call into `AgentMetrics` from `internal/host` is wrapped in a `safeRecord` helper (`defer recover()`), and the concrete OpenTelemetry implementation's own methods each carry their own `defer recover()` too — belt and suspenders, even though the stable OTel Go metric API's `Add`/`Record` calls don't themselves return errors. `InitMetrics` itself is wrapped the same way: any failure constructing the exporter, provider, or instruments (bad endpoint, unreachable collector, etc.) is logged via `slog.Error` and falls back to `NullAgentMetrics{}` rather than aborting startup or being treated as fatal — a telemetry misconfiguration degrades gracefully, it never takes down the agent. `internal/pipeline`/`internal/aggregator` stay decoupled from `AgentMetrics` itself (matching their existing "pipeline/aggregator don't know about telemetry, host wires them together" architecture): each instead takes a small optional `func()` callback (`pipeline.New`'s/`aggregator.New`'s `onDropped` parameter) invoked at the exact point a drop is counted, which `internal/host` wires to a `safeRecord`-guarded `RecordEventDropped` call with the appropriate reason string.

## Build the Windows head

The Windows binary can be **cross-compiled from Linux** into a real, runnable `.exe` — no Windows machine needed to produce the artifact (only to run and visually verify it), and unlike the Rust head, **no mingw or other cross-toolchain is required** since this Windows target has no cgo dependency:

```bash
cd golang
GOOS=windows GOARCH=amd64 go build -o notify-agent-windows.exe ./cmd/notify-agent-windows
```

Copy the `.exe` to a Windows 10/11 machine and run it (same environment variables as the console head apply). It registers a per-user AppUserModelID (`cmd/notify-agent-windows/aumid.go`), enforces a single running instance per session via a `Local\NotifyAgentGolang` mutex, and runs as a system tray app (no console window) using `github.com/getlantern/systray`: the icon (`cmd/notify-agent-windows/assets/app.ico`) appears in the tray immediately on launch, before the agent finishes connecting to NATS, and right-clicking it shows the running version plus a Close item that shuts the agent down.

The compiled `.exe` itself also carries this same icon as its Win32 resource (Explorer/taskbar, not just the tray). Go has no built-in equivalent of C#'s `<ApplicationIcon>` MSBuild property or Rust's `icon.rc`/`build.rs` build script, so this is done via a committed `cmd/notify-agent-windows/resource_windows_amd64.syso`, generated by the pure-Go [`goversioninfo`](https://github.com/josephspurrier/goversioninfo) tool (no `windres`/mingw/cross-toolchain needed, keeping the "no cross-toolchain needed" cross-compile story intact) from `cmd/notify-agent-windows/versioninfo.json`. Go's build system links any `.syso` file present in a package directory automatically — no `go generate` step is needed at build time. The `_windows_amd64` suffix matters: it's Go's standard OS/ARCH build-constraint filename convention, so this object is only linked into Windows/amd64 builds and is invisible to (and doesn't bloat or break) the native Linux build used for `go build ./...`/`go test ./...` here. Regenerate it after changing the icon:

```bash
cd golang/cmd/notify-agent-windows
go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.7.0
goversioninfo -icon=assets/app.ico -o=resource_windows_amd64.syso versioninfo.json
```

Toast rendering has no mature pure-Go WinRT projection to build on (unlike Rust's `windows` crate), so this head builds the same toast XML the OS expects (`internal/windowstoast`) and submits it by shelling out to `powershell.exe -NoProfile -NonInteractive -EncodedCommand ...`, which loads `Windows.UI.Notifications.ToastNotificationManager` via PowerShell's WindowsRuntime `ContentType` accelerator — the same mechanism the BurntToast PowerShell module uses for unpackaged-app toast notifications. This is a documented architecture choice, not a shortcut: it renders avatar images (downloaded and cached under `%LOCALAPPDATA%\DesktopNotificationAgent\image-cache`, best-effort — a bad/oversized/slow image falls back to text-only rather than failing the toast) the same way the C#/Rust heads do.

**Manual smoke-test checklist** (the tray icon and PowerShell toast submission have no automated coverage — they need a live Windows desktop session; a successful cross-compile only proves the code compiles, not that it behaves correctly at runtime):
- Launch the `.exe` — the tray icon appears immediately, before the agent has finished connecting to NATS.
- Right-click the icon — the menu shows "Version 0.1.0" (disabled, not clickable) and "Close".
- Click Close — the icon disappears immediately and the process exits (check Task Manager) within ~5 seconds.
- Point `NOTIFY_NATS_URL` at an unreachable host, relaunch — the tray icon still appears, the tooltip flags the failure ("... (agent failed to start)"), and Close still terminates the process immediately.
- Publish an event with an image URL (`go run ./cmd/test-publisher -- u_demo "Tony Redmond" "is now available" critical 1 "https://i.pravatar.cc/300"`) and confirm a real toast renders with the circular avatar, title, message, and (if configured) action button.

## Troubleshooting

- **`go test ./...` hangs or the integration test fails oddly:** confirm nothing else is bound to port 4222, and that the NATS container is actually running (`docker ps`).
- **Console head exits immediately with an identity error:** you need `NOTIFY_USER_ID` set. (The Windows head does not need or read this variable — see "Identity" above.)
- **Windows cross-compile fails:** confirm your Go toolchain supports the target (`go tool dist list | grep windows`) — no additional linker or C toolchain should be needed for this module.
- **No image appears in a Windows toast:** the agent downloads images best-effort and silently falls back to a text-only toast on any failure (bad scheme, oversize body, slow server, non-image content type); check `internal/imagecache` behavior if you need to debug further.
- **Toast never appears / PowerShell errors:** confirm `powershell.exe` is on `PATH` and that Windows notifications are enabled for the current user session; `cmd/notify-agent-windows/renderer.go`'s PowerShell invocation surfaces `CombinedOutput()` in its returned error.

## Known gaps

- No AAD/MSAL sign-in and no device-code identity flow on either head. The console head's only identity source is environment identity (`NOTIFY_USER_ID`); the Windows head has no AAD fallback to select between — it always uses the Windows-username-derived identity described above, unconditionally rather than as an AAD fallback (unlike C#/Rust).
- No external-auth-service NATS authentication — only `.creds`-file auth is implemented.
- Windows toast rendering goes through a `powershell.exe`-invoked WinRT script rather than native bindings. This is an intentional architecture choice (no mature pure-Go WinRT projection exists), not a defect, but it does mean toast submission depends on `powershell.exe` being present and functional.
- The tray icon uses `github.com/getlantern/systray` rather than raw Win32 `Shell_NotifyIconW` calls.
- Same as the C# and Rust implementations: the Windows head's live/visual behavior (tray icon, toast rendering, Close lifecycle, the settings file actually being read, the `GetUserNameEx`-based identity resolution (including its `GetUserNameW` fallback), and the compiled `.exe`'s Explorer/taskbar icon) has not been verified on a real Windows desktop from this environment — only cross-compilation has been confirmed, plus (for the icon) that the `.rsrc` PE section is present in the cross-compiled binary.
