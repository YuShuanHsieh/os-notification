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
- `TrayApplicationContext.cs` owns the tray icon, version/Close menu, and
  startup-failure tooltip; its icon is `Assets/app.ico`, embedded via
  `<ApplicationIcon>` in `NotificationAgent.Windows.csproj`.

## Rust ownership

- `rust/notify-agent-core/src/host.rs` is the Rust composition root and accepts
  an optional `NatsAuthProvider`.
- `rust/notify-agent-core/src/parser.rs`, `pipeline.rs`, `aggregator.rs`, and
  `dedup.rs` own the bounded wire-processing pipeline; `toast.rs` and
  `toast_xml.rs` own renderer-neutral and Windows XML toast shaping.
- `rust/notify-agent-core/src/nats_auth.rs` owns credentials-file and external
  auth-service provider contracts. `identity.rs` owns environment and device-code
  identity plus AAD token refresh.
- `rust/notify-agent-windows/src/main.rs` owns Windows startup, identity/auth
  selection, and the async runtime. `tray.rs` owns the tray icon, version/Close
  menu, startup-failure tooltip, and message loop; its icon is `assets/app.ico`,
  embedded via `icon.rc`/`build.rs`.
- `rust/notify-agent-core/src/image_cache.rs` performs bounded, HTTPS-only
  best-effort image caching for Rust Windows toasts.

## Historical material

Dated specifications and plans under `docs/superpowers/` explain why major changes
were made. Consult them when intent is unclear, but do not assume unfinished plan
steps describe current behavior.
