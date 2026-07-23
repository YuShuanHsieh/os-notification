# Configuration and Runtime

## Environment variables

| Variable | Default | Consumer |
|---|---|---|
| `NOTIFY_NATS_URL` | `nats://127.0.0.1:4222` | Both hosts and TestPublisher |
| `NOTIFY_SUBJECT_TEMPLATE` | `notify.user.{0}.desktop` | Agent hosts |
| `NOTIFY_ACK_SUBJECT` | `notify.ack.desktop` | Agent hosts and TestPublisher |
| `NOTIFY_NATS_CREDS_FILE` | *(unset → no auth)* | Both hosts: path to a NATS `.creds` file |
| `NOTIFY_NATS_AUTH_SERVICE_URL` | *(unset → falls back to `NOTIFY_NATS_CREDS_FILE`, then no auth)* | Windows: HTTPS endpoint that mints a NATS JWT for the agent's AAD identity |
| `NOTIFY_NATS_AUTH_SERVICE_SCOPE` | *(required with `NOTIFY_NATS_AUTH_SERVICE_URL`)* | Windows: AAD scope requested when calling the auth service |
| `NOTIFY_USER_ID` | Required for environment identity | ConsoleHost; Windows fallback identity |
| `NOTIFY_DEVICE_ID` | `d-{lowercase machine name}` | Environment identity |
| `NOTIFY_AAD_CLIENT_ID` | Unset | Windows; when set, selects MSAL/WAM identity |
| `NOTIFY_AAD_TENANT_ID` | `organizations` | Windows MSAL identity |

`AgentOptions.FromEnvironment` owns transport configuration.
`EnvironmentIdentityProvider` owns development identity configuration. The Windows
entry point owns selection between environment and MSAL identity.

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

## Operational caveats

- No NATS server means host startup cannot connect.
- No Core NATS replay means subscribers must be ready before a publisher sends an
  event in integration tests and smoke tests.
- Windows has not been fully validated by cross-platform unit tests; XML content
  tests prove construction, not notification-center behavior.
- Deduplication is not persistent, and shutdown is best-effort.
