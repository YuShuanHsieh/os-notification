# Configuration and Runtime

## Environment variables

| Variable | Default | Consumer |
|---|---|---|
| `NOTIFY_NATS_URL` | `nats://127.0.0.1:4222` | All three agents and TestPublisher; Rust also accepts `ws://`/`wss://` |
| `NOTIFY_SUBJECT_TEMPLATE` | `notify.user.{0}.desktop` | Agent hosts (Go uses Go's `%s` placeholder instead of `{0}`, same default subject) |
| `NOTIFY_ACK_SUBJECT` | `notify.ack.desktop` | Agent hosts and TestPublisher |
| `NOTIFY_NATS_CREDS_FILE` | *(unset → no auth)* | All hosts: path to a NATS `.creds` file |
| `NOTIFY_NATS_AUTH_SERVICE_URL` | *(unset → falls back to `NOTIFY_NATS_CREDS_FILE`, then no auth)* | C#/Rust Windows only: HTTPS endpoint that mints a NATS JWT for the agent's AAD identity |
| `NOTIFY_NATS_AUTH_SERVICE_SCOPE` | *(required with `NOTIFY_NATS_AUTH_SERVICE_URL`)* | C#/Rust Windows only: AAD scope requested when calling the auth service |
| `NOTIFY_USER_ID` | Required for environment identity | C# ConsoleHost (required); Rust console head (required); Go ConsoleHost (required). **Not read by the C#, Rust, or Go Windows heads** — see below |
| `NOTIFY_DEVICE_ID` | `d-{lowercase machine name}` | Environment identity; C#, Rust, and Go Windows heads also accept it (or their respective settings file's `deviceId`) as an override for the default device id |
| `NOTIFY_AAD_CLIENT_ID` | Unset | C#/Rust Windows only; when set, selects MSAL/WAM (C#) or device-code (Rust) identity |
| `NOTIFY_AAD_TENANT_ID` | `organizations` | C#/Rust Windows MSAL/device-code identity |
| `NOTIFY_LOG_LEVEL` | `Information` (C#) / `info` (Go) | C# Windows only: minimum `Microsoft.Extensions.Logging.LogLevel` for the console logger. Go: both heads, minimum `log/slog` level (`debug`/`info`/`warn`/`error`); Go Windows also accepts the settings file's `logLevel` (env wins when both are set) |
| `RUST_LOG` | `info` | Rust only, both heads: `tracing_subscriber::EnvFilter` directive string (not just a bare level — supports per-module filters). Rust Windows also accepts the settings file's `logLevel` as a plain-level fallback when `RUST_LOG` is unset/blank (`RUST_LOG` wins when both are set) |
| `NOTIFY_OTEL_ENABLED` | `false` | Go Windows head only: enables OpenTelemetry metrics export (`strconv.ParseBool`); also accepts the settings file's `otelEnabled` (env wins when both are set) |
| `NOTIFY_OTEL_EXPORTER_ENDPOINT` | Unset → metrics stay off | Go Windows head only: OTLP/HTTP metrics exporter endpoint; also accepts the settings file's `otelExporterEndpoint` (env wins when both are set) |
| `NOTIFY_OTEL_SERVICE_NAME` | `notify-agent-windows-golang` | Go Windows head only: `service.name` resource attribute for exported metrics; also accepts the settings file's `otelServiceName` (env wins when both are set) |
| `NOTIFY_OTEL_ENABLED` | `false` | C# Windows only: enables OpenTelemetry OTLP metrics export (`true`/`false`, also accepts `1`/`0`); settings file's `otelEnabled` used when unset/blank |
| `NOTIFY_OTEL_EXPORTER_ENDPOINT` | *(unset)* | C# Windows only: OTLP exporter endpoint URL; settings file's `otelExporterEndpoint` used when unset/blank. Metrics stay a no-op unless both this and `NOTIFY_OTEL_ENABLED`/`otelEnabled` resolve truthy |
| `NOTIFY_OTEL_SERVICE_NAME` | `notify-agent-windows-csharp` | C# Windows only: OTel resource service name attached to exported metrics; settings file's `otelServiceName` used when unset/blank |
| `NOTIFY_OTEL_ENABLED` | `false` | Rust Windows only: enables OpenTelemetry metrics export. Only `true`/`1` (case-insensitive) overrides; any other value (including unset or an explicit `false`) falls through to the settings file's `otelEnabled`, then the built-in default (`settings::resolved_bool`) |
| `NOTIFY_OTEL_EXPORTER_ENDPOINT` | *(unset)* | Rust Windows only: OTLP/HTTP metrics exporter endpoint; settings file's `otelExporterEndpoint` used when unset/blank. Metrics stay a no-op unless both this and `NOTIFY_OTEL_ENABLED`/`otelEnabled` resolve truthy |
| `NOTIFY_OTEL_SERVICE_NAME` | `notify-agent-windows-rust` | Rust Windows only: `service.name` resource attribute attached to exported metrics; settings file's `otelServiceName` used when unset/blank |

`AgentOptions.FromEnvironment` owns transport configuration.
`EnvironmentIdentityProvider` owns development identity configuration; it is used
unchanged by `NotificationAgent.ConsoleHost` and requires `NOTIFY_USER_ID`. The C#
Windows entry point owns selection between AAD/MSAL and a Windows-username-derived
default identity (see "C# Windows settings file" below) — it no longer uses
`EnvironmentIdentityProvider` or reads `NOTIFY_USER_ID` at all. The Rust Windows
entry point (`rust/notify-agent-windows/src/main.rs`'s `start_host`) similarly owns
selection between AAD/device-code and a Windows-username-derived default identity
(`WindowsUsernameIdentity`, see "Rust Windows settings file" below) — it no longer
uses `notify_agent_core::identity::EnvIdentity`/reads `NOTIFY_USER_ID` by default.
The Rust console head is unaffected and still requires `NOTIFY_USER_ID` via the
same, unchanged `EnvIdentity`. The Go Windows entry point (`cmd/notify-agent-windows`)
similarly no longer uses `golang/internal/identity.EnvIdentity` or reads
`NOTIFY_USER_ID` at all — it has no AAD/device-code path to fall back from, so
`WindowsUsernameIdentity` (see "Go Windows settings file" below) is always used.
The Go console head is unaffected and still requires `NOTIFY_USER_ID` via
`EnvIdentity`.

## C# Windows settings file

The C# Windows head also reads an optional JSON settings file at
`%LOCALAPPDATA%\DesktopNotificationAgent\settings.json`
(`NotificationAgent.Windows/WindowsSettings.cs`), so an operator can configure a
deployed agent without setting environment variables. All fields are optional and
mirror the environment variables above: `natsUrl`, `subjectTemplate`, `ackSubject`,
`natsCredsFile`, `natsAuthServiceUrl`, `natsAuthServiceScope`, `aadClientId`,
`aadTenantId`, `deviceId`, `logLevel`, `otelEnabled`, `otelExporterEndpoint`,
`otelServiceName`. Precedence per field is environment variable
(non-blank) > settings file value (non-blank) > built-in default. A missing file is
normal and is never created or required; a malformed file logs a warning and is
treated as all-defaults rather than failing startup. This file is specific to the C#
Windows head; the console host and the Rust/Go Windows heads are unaffected.

`otelEnabled`/`otelExporterEndpoint`/`otelServiceName` (defaults `false`/unset/
`notify-agent-windows-csharp`) configure an OpenTelemetry OTLP metrics exporter
built by `NotificationAgent.Windows/OpenTelemetryAgentMetrics.cs`, wired into
`IAgentMetrics` (`NotificationAgent.Core/Telemetry/IAgentMetrics.cs`) — the same
interface/no-op-default split already used for `IToastRenderer`/
`IIdentityProvider`/`INatsAuthProvider`, so Core and the console host stay free
of the `OpenTelemetry` NuGet package. Metrics recording is fully crash-safe: every
OTel SDK call (provider/exporter/instrument construction, and every `.Add()`/
`.Record()` call from Core) is wrapped in try/catch, so a metrics failure —
misconfigured endpoint, SDK init failure, or anything else — is swallowed and
logged (`Log.OtelSetupFailed`/`Log.OtelEnabledWithoutEndpoint`) rather than ever
interrupting event processing, dedup, aggregation, or ack publishing. When
disabled (the default), no `Meter`/`MeterProvider` is constructed at all, so
there is zero OTel overhead.

## Rust Windows settings file

The Rust Windows head also reads an optional JSON settings file at
`%LOCALAPPDATA%\DesktopNotificationAgent\settings.json`
(`rust/notify-agent-windows/src/settings.rs`), the same directory and filename
the C#/Go Windows heads use, so an operator can configure a deployed agent
without setting environment variables. Fields mirror the environment variables
above: `natsUrl`, `subjectTemplate`, `ackSubject`, `natsCredsFile`,
`natsAuthServiceUrl`, `natsAuthServiceScope`, `aadClientId`, `aadTenantId`,
`deviceId`, `logLevel`, plus three metrics-only fields — `otelEnabled` (bool,
default `false`), `otelExporterEndpoint` (string, default blank),
`otelServiceName` (string, default `notify-agent-windows-rust`) — see "Rust
Windows metrics (OpenTelemetry)" below. Precedence per field is environment
variable (non-blank) > settings file value (non-blank) > built-in default —
`settings::agent_config` and `settings::resolved_str`/`resolved_opt`/
`resolved_bool`/`resolved_otel_settings` layer the parsed `Settings` under
each `std::env::var(...)` call, never by changing
`notify_agent_core::host::AgentConfig::from_env` itself (shared with the console
head, which stays pure-env). `otelEnabled` uses `resolved_bool` rather than
`resolved_str`/`resolved_opt`: since it is a boolean, only an env value of
`true`/`1` (case-insensitive) overrides — any other env value, including an
explicit `false`, falls through to the file value instead of forcing it off
(see that function's doc comment for the rationale). A missing file is normal
and is never created or required; a malformed file logs a `tracing::warn!`
and is treated as all-defaults rather than failing startup. `logLevel` feeds
`tracing_subscriber`'s `EnvFilter` (see the `RUST_LOG` row above and the
Windows-runtime logging bullet below); every other field's parsing/precedence
logic is plain structs and runs under `cargo test` on any platform. This file
is specific to the Rust Windows head; the console host and the C#/Go Windows
heads are unaffected.

## Rust Windows metrics (OpenTelemetry)

The Rust Windows head can optionally export three OpenTelemetry metrics over
OTLP/HTTP (`rust/notify-agent-windows/src/otel_metrics.rs`): a
`notify_agent_events_received_total` counter (once per valid, first-seen
event accepted into the pipeline — the same point the `observed_by_agent`
ack is published), a `notify_agent_events_dropped_total` counter tagged with
a `reason` attribute (`"queue_full"` or `"bucket_overflow"`), and a
`notify_agent_render_duration_seconds` histogram (once per source event
represented in a rendered toast, using that event's own
`agent_received_at`/`toast_submitted_at` — a batched toast covering 3 events
records 3 observations, not 1). It is disabled by default;
`otelEnabled`/`NOTIFY_OTEL_ENABLED` (only `true`/`1` overrides — see "Rust
Windows settings file" above) and a non-blank
`otelExporterEndpoint`/`NOTIFY_OTEL_EXPORTER_ENDPOINT` together turn it on;
`otelServiceName`/`NOTIFY_OTEL_SERVICE_NAME` sets the `service.name`
resource attribute.

`notify_agent_core::metrics::AgentMetrics` is the trait this goes through —
deliberately free of any `opentelemetry`/`opentelemetry-otlp` dependency,
mirroring the `IdentityProvider`/`ToastRenderer`/`NatsAuthProvider` pattern
of "a small trait in Core, a no-op default, a real implementation supplied
only by the head that needs it" (`NullAgentMetrics`, also in
`notify_agent_core::metrics`). Only `notify-agent-windows` depends on the
actual OTel SDK/exporter crates; `notify-agent-core`'s pipeline/aggregator
only ever see the trait object (`Arc<dyn AgentMetrics>`, defaulting to
`NullAgentMetrics` when `AgentHost::start`'s optional `metrics` parameter is
`None`), and `notify-agent-console` never depends on any `opentelemetry*`
crate at all (verified via `cargo tree -p notify-agent-console`).

Metrics-recording code can never crash the agent, achieved by construction
rather than by wrapping call sites in `std::panic::catch_unwind` (reserved
here for FFI/thread boundaries, not routine same-thread calls — see
`otel_metrics.rs`'s module doc comment for the full reasoning): the trait's
three methods return `()`, not `Result`, so there is no fallible signature to
begin with; the OTel-backed implementation has no `.unwrap()`/`.expect()`
anywhere and no fallible internal step once its instruments are built,
since the OTel metrics API's `Counter::add`/`Histogram::record` calls are
themselves infallible/non-blocking by design (an unreachable collector is
handled inside the SDK's own background `PeriodicReader` thread, never
surfaced to the caller). `otel_metrics::init` (called from `main.rs`'s
`start_host` after the tracing subscriber is installed, so failures are
actually visible) never panics and never fails startup: any setup error — a
malformed endpoint, an OTLP exporter build failure — is logged via
`tracing::warn!` and falls back to `NullAgentMetrics` instead. When disabled
or unconfigured (the default), `init` returns `NullAgentMetrics` immediately
without constructing any OTel SDK type at all, so there is zero overhead.

## Go Windows settings file

The Go Windows head also reads an optional JSON settings file at
`%LOCALAPPDATA%\DesktopNotificationAgent\settings.json`
(`golang/cmd/notify-agent-windows/settings.go`), the same directory and
filename the C# Windows head uses, so an operator can configure a deployed
agent without setting environment variables. Its schema is intentionally
narrower than C#'s (see below for why): `natsUrl`, `subjectTemplate`,
`ackSubject`, `natsCredsFile`, `deviceId`, `logLevel`, plus three
metrics-only fields — `otelEnabled` (bool, default `false`),
`otelExporterEndpoint` (string, default blank), `otelServiceName` (string,
default `notify-agent-windows-golang`) — see "Go Windows metrics
(OpenTelemetry)" below. Precedence per field is environment variable
(non-blank) > settings file value (non-blank) > built-in default —
implemented by `ResolveHostOptions`/`ResolveCredsFile`/`ResolveDeviceID`/
`ResolveLogLevel`/`ResolveOtelEnabled`/`ResolveOtelExporterEndpoint`/
`ResolveOtelServiceName` layering the parsed `Settings` under
`host.OptionsFromEnv()`'s result, never by modifying `host.Options` or
`host.OptionsFromEnv` itself (those stay shared, cross-platform types). A
missing file is normal and is never created or required; a malformed file logs
a warning (`log/slog`) and is treated as all-defaults rather than failing
startup. This file is specific to the Go Windows head; the Go console host and
the C#/Rust Windows heads are unaffected.

## Go Windows metrics (OpenTelemetry)

The Go Windows head can optionally export three OpenTelemetry metrics over
OTLP/HTTP (`golang/cmd/notify-agent-windows/otelmetrics.go`): an
`agent.events.received` counter (once per valid, first-seen event accepted
into the pipeline), an `agent.events.dropped` counter tagged with a `reason`
attribute (`"queue_full"` or `"bucket_overflow"`), and an
`agent.render.duration` histogram in seconds (once per source event
represented in a rendered toast — a batched toast covering 3 events records
3 observations, not 1). It is disabled by default; `otelEnabled`/
`NOTIFY_OTEL_ENABLED` (env wins), a non-blank `otelExporterEndpoint`/
`NOTIFY_OTEL_EXPORTER_ENDPOINT` (env wins), and `otelServiceName`/
`NOTIFY_OTEL_SERVICE_NAME` (env wins, `service.name` resource attribute)
turn it on.

`golang/internal/metrics.AgentMetrics` is the interface this goes through —
deliberately free of any `go.opentelemetry.io/otel*` import, mirroring the
`identity.Provider`/`toast.Renderer`/`natsauth.Provider` pattern of "a small
interface in `internal/*`, a real implementation supplied only by the head
that needs it." Only `cmd/notify-agent-windows` imports the actual OTel
SDK/exporter packages; `internal/host`, `internal/pipeline`, and
`internal/aggregator` only ever see the interface (defaulting to
`internal/metrics.NullAgentMetrics{}`, a no-op, when nothing is supplied),
and the Go console head (`cmd/notify-agent-console`) never imports any
`go.opentelemetry.io/otel*` package at all.

Metrics-recording code can never crash the agent, by explicit design: every
`AgentMetrics` call from `internal/host` is wrapped in a `safeRecord` helper
(`defer recover()`), and the concrete OpenTelemetry implementation's own
methods each carry their own `defer recover()` too — belt and suspenders,
even though the stable OTel Go metric API's `Add`/`Record` calls don't
themselves return errors. `InitMetrics` (the constructor called from
`main.go` after the logger is configured) is wrapped the same way: any
failure building the exporter, provider, or instruments is logged via
`slog.Error` and falls back to `NullAgentMetrics{}` rather than aborting
startup or being treated as fatal. `internal/pipeline`/`internal/aggregator`
stay decoupled from `AgentMetrics` itself, matching their existing
pipeline/aggregator-don't-know-about-telemetry architecture: each instead
takes a small optional `func()` callback (`onDropped`, added to
`pipeline.New`/`aggregator.New`) invoked at the exact point a drop is
counted, which `internal/host` wires to a `safeRecord`-guarded
`RecordEventDropped` call with the appropriate reason string.

The Go port (`golang/internal/host.OptionsFromEnv`, `golang/internal/identity.EnvIdentity`)
still has a narrower scope than the other two implementations for NATS auth, and
this is accepted/documented rather than a gap to close incidentally: NATS auth is
creds-file-only (`golang/internal/natsauth.CredsFileAuth`, no
external-auth-service provider) on both Go heads. `NOTIFY_AAD_CLIENT_ID`,
`NOTIFY_AAD_TENANT_ID`, `NOTIFY_NATS_AUTH_SERVICE_URL`, and
`NOTIFY_NATS_AUTH_SERVICE_SCOPE` are not consumed by the Go agent, which is also
why the Go Windows settings file above has no `natsAuthServiceUrl`/
`natsAuthServiceScope`/`aadClientId`/`aadTenantId` fields — it only exposes
configuration the Go Windows head actually acts on. Identity itself is no
longer environment-only on the Go Windows head specifically (see the
Windows-username-derived identity fallback above and in
[`contracts-and-invariants.md`](contracts-and-invariants.md)); the Go console
head remains environment-only (`NOTIFY_USER_ID`/`NOTIFY_DEVICE_ID`, via
`EnvIdentity`, no AAD/MSAL/device-code sign-in).

One detail to preserve: `TestPublisher` currently constructs
`notify.user.{userId}.desktop` directly; it does not consume
`NOTIFY_SUBJECT_TEMPLATE`. If subject configurability changes, update the tool as
well as the host or explicitly document the asymmetry.

## Development runtime

The default local path needs a NATS server on `127.0.0.1:4222`, a console host with
`NOTIFY_USER_ID` set, and `tools/TestPublisher`. See the root `README.md` for exact
commands. The publisher waits long enough to observe the normal ten-second batch
window and prints ack payloads.

The console renderer prints URLs without applying `HttpsUrlPolicy`; it is a
visibility aid only. Windows content construction is the authoritative URL safety
boundary for displayed images and launched actions.

## NATS Authentication

`AgentHost.StartAsync`'s optional `INatsAuthProvider` (from `NotificationAgent.Core.Nats`)
owns auth configuration: `CredsFileNatsAuthProvider` (Core, both hosts) wraps a `.creds`
file; `ExternalAuthServiceNatsAuthProvider` (Windows only) calls an external HTTPS auth
service, using `MsalIdentityProvider` to silently acquire a separate token for a second,
independently configured scope (reusing the same signed-in WAM account, not the identity
token itself). `NotificationAgent.Windows/NatsAuthSelection.cs` owns the presence-based
selection between them at startup.

## Windows runtime

- Target: `net10.0-windows10.0.19041.0`, with `win-x64` and `win-arm64` runtime IDs.
- Assembly output name: `DesktopAgent`.
- It is unpackaged and uses `Microsoft.Toolkit.Uwp.Notifications` 7.1.3 for native
  toast submission without a Windows App SDK runtime dependency.
- `EnableWindowsTargeting` allows compilation on non-Windows machines, but actual
  notification and WAM behavior require Windows verification.
- A session-scoped `Local\DesktopNotificationAgent` mutex prevents duplicate agent
  processes for the same interactive session.
- With an AAD client ID, `MsalIdentityProvider` tries silent WAM acquisition and
  falls back to interactive acquisition when UI is required. Without one,
  `WindowsUsernameIdentityProvider` derives a default identity from the Windows
  username, resolved via the SAM-compatible, domain-qualified name format
  (`DOMAIN\username`, or `MACHINENAME\username` when not domain-joined) so
  that two identically-named accounts in different domains resolve to
  different identities: normalized (lowercased, trimmed), sanitized to
  `[a-z0-9_-]` (every other character, including `.`/`*`/`>`/whitespace/the
  `\` separator, becomes `_`), then suffixed with an 8-hex-character
  `SHA-256` digest of the pre-sanitization normalized username so that two
  usernames sanitizing to the same string (e.g. `"user.name"` and
  `"user_name"`) still resolve to different identities —
  `u_{sanitized}_{hash8}`.
- The device ID file is created beneath
  `%LOCALAPPDATA%\DesktopNotificationAgent\device-id`, unless overridden by
  `NOTIFY_DEVICE_ID`/the settings file's `deviceId`.
- The compiled `DesktopAgent.exe` and the tray `NotifyIcon` both use
  `src/NotificationAgent.Windows/app.ico` (copied verbatim from the repo-root
  canonical `assets/app.ico`; do not regenerate — see commit 9f58508 on why a
  re-exported `.ico` can silently fail to render as a Win32 resource).
- The Windows head logs via `Microsoft.Extensions.Logging` (console, single-line),
  covering identity mode selection, NATS auth mode selection, resolved
  configuration, and startup success/failure. It does not thread logging through
  `NotificationAgent.Core`. Any NATS URL logged (the connection URL or the
  external-auth-service URL) is credential-redacted first (`NatsUrlRedactor`) —
  userinfo embedded in the URL is never written to the log, and an unparseable
  URL is logged as a fixed `<invalid URL>` marker rather than passed through.
- The Rust Windows head is a tray application rather than a headless process. It
  shows a placeholder icon immediately, displays the running version and Close
  in its context menu, and marks the tooltip when agent startup fails. Close has
  a bounded graceful-shutdown attempt followed by forced process termination.
- Rust Windows builds can be cross-compiled from Linux. The checked-in Docker
  workflow vendors dependencies so the release build can run with network access
  disabled; see `rust/docker/windows-cross.Dockerfile` and
  `rust/scripts/build-windows-docker.sh`.
- The Rust device ID file is created beneath
  `%LOCALAPPDATA%\DesktopNotificationAgent\device-id`, unless overridden by
  `NOTIFY_DEVICE_ID`/the settings file's `deviceId` (`main.rs`'s `device_id()`).
- With an AAD client id, `DeviceCodeIdentity` runs the OIDC device-code sign-in
  flow (see "Identity" in `rust/README.md`). Without one,
  `rust/notify-agent-windows/src/windows_identity.rs`'s `WindowsUsernameIdentity`
  derives a default identity from the Windows username, calling
  `GetUserNameExW` (`secur32.dll`, via the `windows` crate's
  `Win32::Security::Authentication::Identity` module) with
  `NameSamCompatible` to resolve the SAM-compatible, domain-qualified name
  format (`DOMAIN\username`, or `MACHINENAME\username` when not
  domain-joined) so that two identically-named accounts in different domains
  resolve to different identities, falling back to the plain Win32
  `GetUserNameW` (`advapi32.dll`) if `GetUserNameExW` fails or returns an
  empty string. The resolved name is then lowercased and sanitized (replacing
  `.`, `*`, `>`, whitespace, the `\` separator, and any other character
  outside `[a-z0-9_-]` with `_`, then appending a hash suffix of the
  pre-sanitization value) before it reaches subject construction — the same
  validated-username identity exception as the C#/Go Windows heads; see
  `contracts-and-invariants.md`.
- `rust/notify-agent-windows/assets/app.ico` (embedded via `icon.rc`/`build.rs`)
  is a copy of the repo-root canonical `assets/app.ico`, matching the C#/Go
  Windows heads; do not regenerate it independently — see commit 9f58508 on why
  a re-exported `.ico` can silently fail to render as a Win32 resource.
- The Rust Windows head logs via `tracing` (`tracing_subscriber::fmt`, single
  line per event), covering identity mode selection, NATS auth mode selection,
  resolved configuration, startup success/failure, dropped/duplicate events
  (queue-full, aggregation-bucket-overflow, dedup-key-seen, parse failures), and
  render/toast-submission failures. Its filter comes from `RUST_LOG` if set,
  else the settings file's `logLevel`, else `info` (`settings::resolve_log_filter`).
  The Rust console head shares the same `tracing`/`RUST_LOG` convention but has
  no settings file.
- The Go Windows head (`golang/cmd/notify-agent-windows`) is also a tray
  application rather than a headless process, using
  `github.com/getlantern/systray` for the icon/version/Close menu with the same
  immediate-icon and bounded-graceful-then-forced-close lifecycle as the C# and
  Rust heads. It has no native WinRT toast bindings: `renderer.go` builds toast
  XML (`golang/internal/windowstoast`) and submits it by invoking
  `powershell.exe -EncodedCommand`, which loads
  `Windows.UI.Notifications.ToastNotificationManager` via PowerShell's
  WindowsRuntime `ContentType` accelerator.
- Go Windows builds cross-compile from Linux with a plain
  `GOOS=windows GOARCH=amd64 go build`; unlike the Rust head, there is no cgo
  dependency here, so no mingw or other cross-toolchain is required.
- The compiled `notify-agent-windows.exe`'s icon resource and the tray icon
  both come from `golang/cmd/notify-agent-windows/assets/app.ico` (copied
  verbatim from the repo-root canonical `assets/app.ico`; do not regenerate —
  see commit 9f58508 on why a re-exported `.ico` can silently fail to render
  as a Win32 resource). Unlike C#'s `<ApplicationIcon>` MSBuild property or
  Rust's `icon.rc`/`build.rs`, Go has no built-in exe-icon mechanism: a
  `resource.syso` (generated via `github.com/josephspurrier/goversioninfo`
  from `versioninfo.json`, both committed alongside it in
  `cmd/notify-agent-windows/`) is linked automatically by `go build`/
  `GOOS=windows go build` whenever it is present in the package directory —
  no `go generate` step or C toolchain is needed at build time. Regenerate it
  after changing the icon with (run from `cmd/notify-agent-windows/`):
  `goversioninfo -icon=assets/app.ico -o=resource.syso versioninfo.json`.
- The Go Windows head resolves identity via `WindowsUsernameIdentity`
  (`identity_windows.go`), calling the wrapped `GetUserNameEx` function
  (`secur32.dll`, via `golang.org/x/sys/windows`) with
  `windows.NameSamCompatible` to retrieve the domain-qualified
  `DOMAIN\username` (or `MACHINENAME\username`) form, falling back to the
  plain Win32 `GetUserNameW` (`advapi32.dll`, via the same raw
  `NewLazySystemDLL`/`NewProc` pattern `aumid.go` uses for `shell32.dll`) if
  `GetUserNameEx` fails — rather than an environment variable — see the
  identity bullet above and `contracts-and-invariants.md`.
- The Go Windows head logs via the standard library `log/slog` (text handler,
  stderr), covering identity resolution, NATS connect/subscribe, render
  failures, queue-full/bucket-overflow drops, and tray lifecycle events
  (icon shown, Close clicked, agent-start failure). Its minimum level is
  `NOTIFY_LOG_LEVEL` or the settings file's `logLevel` (env wins), default
  `info`. The Go console head uses the same `log/slog` convention,
  environment-only (no settings file), same default.
- The Go Windows head can optionally export OpenTelemetry metrics (events
  received/dropped, render duration) over OTLP/HTTP — off by default, see
  "Go Windows metrics (OpenTelemetry)" above. This is the only place in the
  Go port that imports any `go.opentelemetry.io/otel*` package; the Go
  console head is unaffected and never imports it. Every metrics-recording
  call site is `defer recover()`-guarded (in both `internal/host` and the
  concrete OTel implementation), so a metrics failure — or a deliberate
  telemetry misconfiguration at startup — can never crash the agent.

## Operational caveats

- No NATS server means host startup cannot connect.
- No Core NATS replay means subscribers must be ready before a publisher sends an
  event in integration tests and smoke tests.
- Windows has not been fully validated by cross-platform unit tests; XML content
  tests prove construction, not notification-center behavior.
- Deduplication is not persistent, and shutdown is best-effort.
