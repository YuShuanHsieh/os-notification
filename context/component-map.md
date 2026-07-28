# Component Map

## Projects

| Path | Responsibility | Key dependencies |
|---|---|---|
| `src/NotificationAgent.Core` | Cross-platform domain and runtime pipeline | `NATS.Net`; no Windows APIs |
| `src/NotificationAgent.ConsoleHost` | Linux/development entry point and console renderer | Core |
| `src/NotificationAgent.Windows` | Windows entry point, native toast adapter, MSAL/WAM identity | Core, Windows notification toolkit, MSAL |
| `tests/NotificationAgent.Core.Tests` | Cross-platform unit and optional NATS integration tests | Core, xUnit, fake time |
| `tests/NotificationAgent.Windows.Tests` | Windows toast XML/content tests | Windows project, xUnit |
| `tools/TestPublisher` | Publishes sample events and observes acknowledgements | `NATS.Net` |
| `rust/notify-agent-core` | Cross-platform Rust pipeline, identity, NATS host, auth, and toast contracts | `async-nats`, Tokio |
| `rust/notify-agent-console` | Rust Linux/development head and console renderer | Rust Core |
| `rust/notify-agent-windows` | Rust Windows head, WinRT toast renderer, image cache, and system tray | Rust Core, Windows APIs |
| `golang/internal/*` | Cross-platform Go pipeline, identity, NATS host, auth, image cache, and toast contracts | `nats.go` |
| `golang/cmd/notify-agent-console` | Go Linux/development head and console renderer | Go Core packages |
| `golang/cmd/notify-agent-windows` | Go Windows head: PowerShell-submitted WinRT toast XML, AUMID registration, and `systray`-based tray icon | Go Core packages, `golang.org/x/sys/windows`, `getlantern/systray`, `powershell.exe` |
| `golang/cmd/test-publisher` | Publishes sample events and observes acknowledgements (Go port) | `nats.go` |

The solution file includes Core, Core.Tests, ConsoleHost, and TestPublisher. The
Windows application and Windows tests are intentionally built separately so the
default solution remains cross-platform.

## Core ownership

| Concern | Source | Primary tests |
|---|---|---|
| Normalized event model and priority enum | `Models/InboundNotification.cs` | `EventParserTests.cs` |
| Payload limits, JSON parsing, defaults, validation | `Serialization/EventParser.cs` | `EventParserTests.cs` |
| Intake queue, worker pool, processing order | `Pipeline/EventPipeline.cs` | `EventPipelineTests.cs` |
| TTL/capacity duplicate suppression | `Dedup/DeduplicationCache.cs` | `DeduplicationCacheTests.cs` |
| Priority routing, batching, replacement, bucket limit | `Aggregation/Aggregator.cs` | `AggregatorTests.cs` |
| Single/batch renderer-neutral content | `Rendering/ToastContentFactory.cs` | `ToastContentFactoryTests.cs` |
| Unicode-safe truncation | `Rendering/GraphemeText.cs` | `GraphemeTextTests.cs` |
| HTTPS action/image validation | `Rendering/HttpsUrlPolicy.cs` | `HttpsUrlPolicyTests.cs`, Windows content tests |
| Renderer contract and toast DTO | `Rendering/ToastRequest.cs` | Pipeline and content tests |
| Ack schema/status and NATS telemetry adapter | `Telemetry/Acks.cs`, `Hosting/AgentHost.cs` | `AckJsonTests.cs`, `EventPipelineTests.cs` |
| Environment identity contract | `Identity/IIdentityProvider.cs` | Covered indirectly; add focused tests for new behavior |
| NATS composition/subscription/lifecycle | `Hosting/AgentHost.cs` | `NatsIntegrationTests.cs` |

## Host ownership

- `NotificationAgent.ConsoleHost/Program.cs` wires environment identity and the
  console renderer. `ConsoleToastRenderer.cs` is diagnostic output, not a security
  or fidelity equivalent of Windows rendering.
- `NotificationAgent.Windows/Program.cs` owns single-instance startup and selects
  MSAL identity when `NOTIFY_AAD_CLIENT_ID` is present.
- `MsalIdentityProvider.cs` resolves the Entra object ID and stores a stable device
  ID under `%LOCALAPPDATA%\DesktopNotificationAgent\device-id`.
- `WindowsToastContentFactory.cs` translates a `ToastRequest` to Windows toast XML
  and applies HTTPS URL policy.
- `WindowsToastRenderer.cs` submits native notifications.

## Rust ownership

- `rust/notify-agent-core/src/host.rs` is the Rust composition root and accepts
  an optional `NatsAuthProvider`.
- `rust/notify-agent-core/src/parser.rs`, `pipeline.rs`, `aggregator.rs`, and
  `dedup.rs` own the bounded wire-processing pipeline; `toast.rs` and
  `toast_xml.rs` own renderer-neutral and Windows XML toast shaping.
- `rust/notify-agent-core/src/nats_auth.rs` owns credentials-file and external
  auth-service provider contracts. `identity.rs` owns environment and device-code
  identity plus AAD token refresh (`EnvIdentity`/`NOTIFY_USER_ID` here is shared
  with the console head and unaffected by the Windows head's own default
  identity below).
- `rust/notify-agent-windows/src/main.rs` owns Windows startup, identity/auth
  selection, and the async runtime. `tray.rs` owns the tray icon, version/Close
  menu, startup-failure tooltip, and message loop. `settings.rs` owns the
  optional `%LOCALAPPDATA%\DesktopNotificationAgent\settings.json` file
  (parsing, env-over-file-over-default precedence, and the `logLevel` ->
  `tracing_subscriber::EnvFilter` mapping). `windows_identity.rs` owns the
  default Windows-username-derived identity (`WindowsUsernameIdentity`) used
  when `NOTIFY_AAD_CLIENT_ID` isn't set — a deliberate, documented exception to
  Core's "OS account name is never identity" principle, scoped to this head;
  see `contracts-and-invariants.md`.
- `rust/notify-agent-core/src/image_cache.rs` performs bounded, HTTPS-only
  best-effort image caching for Rust Windows toasts.

## Go ownership

- `golang/internal/host/host.go` is the Go composition root (`Start`/`Shutdown`)
  and accepts an optional `natsauth.Provider`.
- `golang/internal/parser`, `pipeline`, `aggregator`, and `dedup` own the
  bounded wire-processing pipeline; `toast` and `windowstoast` own
  renderer-neutral and Windows XML toast shaping.
- `golang/internal/natsauth` owns the credentials-file auth provider contract
  (no external-auth-service provider yet). `golang/internal/identity` owns
  environment-only identity (no AAD/MSAL/device-code flow yet).
- `golang/cmd/notify-agent-windows/main.go` and `tray.go` own Windows startup,
  single-instance enforcement, and the `systray`-based tray icon/menu/close
  lifecycle. `renderer.go` submits toast XML by shelling out to
  `powershell.exe` (no native WinRT bindings). `aumid.go` owns AUMID
  registration and process identity.
- `golang/internal/imagecache` performs bounded, HTTPS-only best-effort image
  caching for Go Windows toasts.

## Historical material

Dated specifications and plans under `docs/superpowers/` explain why major changes
were made. Consult them when intent is unclear, but do not assume unfinished plan
steps describe current behavior.
