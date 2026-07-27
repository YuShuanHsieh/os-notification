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
| `NOTIFY_USER_ID` | Required for environment identity | ConsoleHost; Windows fallback identity (Go: the only identity source) |
| `NOTIFY_DEVICE_ID` | `d-{lowercase machine name}` | Environment identity |
| `NOTIFY_AAD_CLIENT_ID` | Unset | C#/Rust Windows only; when set, selects MSAL/WAM (C#) or device-code (Rust) identity |
| `NOTIFY_AAD_TENANT_ID` | `organizations` | C#/Rust Windows MSAL/device-code identity |

`AgentOptions.FromEnvironment` owns transport configuration.
`EnvironmentIdentityProvider` owns development identity configuration. The Windows
entry point owns selection between environment and MSAL identity. The Rust Windows
entry point uses environment identity unless `NOTIFY_AAD_CLIENT_ID` selects its
device-code identity flow.

The Go port (`golang/internal/host.OptionsFromEnv`, `golang/internal/identity.EnvIdentity`)
currently has a narrower scope than the other two implementations, and this is
accepted/documented rather than a gap to close incidentally: identity is
environment-only (`NOTIFY_USER_ID`/`NOTIFY_DEVICE_ID`, no AAD/MSAL/device-code
sign-in), and NATS auth is creds-file-only (`golang/internal/natsauth.CredsFileAuth`,
no external-auth-service provider). `NOTIFY_AAD_CLIENT_ID`,
`NOTIFY_AAD_TENANT_ID`, `NOTIFY_NATS_AUTH_SERVICE_URL`, and
`NOTIFY_NATS_AUTH_SERVICE_SCOPE` are not consumed by the Go agent.

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
  falls back to interactive acquisition when UI is required.
- The device ID file is created beneath
  `%LOCALAPPDATA%\DesktopNotificationAgent\device-id`.
- The Rust Windows head is a tray application rather than a headless process. It
  shows a placeholder icon immediately, displays the running version and Close
  in its context menu, and marks the tooltip when agent startup fails. Close has
  a bounded graceful-shutdown attempt followed by forced process termination.
- Rust Windows builds can be cross-compiled from Linux. The checked-in Docker
  workflow vendors dependencies so the release build can run with network access
  disabled; see `rust/docker/windows-cross.Dockerfile` and
  `rust/scripts/build-windows-docker.sh`.
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

## Operational caveats

- No NATS server means host startup cannot connect.
- No Core NATS replay means subscribers must be ready before a publisher sends an
  event in integration tests and smoke tests.
- Windows has not been fully validated by cross-platform unit tests; XML content
  tests prove construction, not notification-center behavior.
- Deduplication is not persistent, and shutdown is best-effort.
