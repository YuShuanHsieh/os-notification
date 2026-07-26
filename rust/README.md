# Rust Notification Agent

A Rust port of the desktop notification agent, wire-compatible with the C# agent in `../src/NotificationAgent.*`: same NATS subjects, same `content` JSON schema, same acknowledgement contract. See `../docs/superpowers/specs/2026-07-20-rust-notification-agent-design.md` for the design and `../docs/superpowers/specs/2026-07-22-image-toasts-design.md` for the avatar-image extension.

## Layout

| Crate | What it is |
|---|---|
| `notify-agent-core` | Cross-platform library: event parsing, dedup, aggregation/batching, toast content, ack telemetry, identity, NATS host. Runs and tests on Linux. |
| `notify-agent-console` | Linux dev head — subscribes for real, prints `[TOAST]` blocks to stdout instead of showing a native toast. |
| `notify-agent-windows` | Windows head — real WinRT toast notifications. Compiles to a stub on non-Windows targets so the workspace stays buildable here; only runs on Windows. |

## Prerequisites

- **Rust 1.96.1**, pinned by `rust-toolchain.toml` — `rustup` will fetch it automatically the first time you run `cargo` in this directory.
  ```bash
  curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain none
  export PATH="$HOME/.cargo/bin:$PATH"   # add to your shell profile
  ```
- **A NATS server** reachable at `nats://127.0.0.1:4222` (default) or wherever `NOTIFY_NATS_URL` points. For local dev:
  ```bash
  docker run -d --name nats-dev -p 4222:4222 nats:2.10-alpine
  ```
- **The C# `TestPublisher` tool** (`../tools/TestPublisher`) if you want to publish test events — it's the dev-side event producer for both agents. Needs the .NET 8 SDK (`export PATH="$HOME/.dotnet:$PATH"` once installed).

## Build and test (Linux/macOS)

```bash
cd rust
cargo build
cargo test
```

`notify-agent-windows` compiles as a no-op stub off Windows, so the whole workspace builds and tests clean here. The test suite includes a live NATS integration test (`notify-agent-core/tests/nats_integration.rs`) that politely no-ops with a `SKIPPED` message if no server is reachable on `localhost:4222` — start the NATS container above to exercise it for real.

## Run the console head

```bash
export NOTIFY_USER_ID=u_demo          # required unless NOTIFY_AAD_CLIENT_ID is set (see Identity below)
cargo run -p notify-agent-console
```

It subscribes to `notify.user.u_demo.desktop`, prints a `[TOAST]` block for each rendered notification, and shuts down gracefully on Ctrl+C.

Publish a test event from another shell (needs the .NET SDK):

```bash
export PATH="$HOME/.dotnet:$PATH"
dotnet run --project ../tools/TestPublisher -- u_demo --scenario presence
```

`TestPublisher --scenario <name>` drives every schema use case end-to-end (avatar image, action button, priority batching, deduplication, replaceable progress updates) — see `../tools/TestPublisher/Program.cs` or run it with `--help`-style bad input to print the usage text, which lists all scenarios and flags.

## Configuration (environment variables)

| Variable | Default | Purpose |
|---|---|---|
| `NOTIFY_NATS_URL` | `nats://127.0.0.1:4222` | NATS server to connect to. Also accepts `ws://`/`wss://` (NATS WebSocket, e.g. behind a load balancer that doesn't pass through raw TCP) — the scheme is detected automatically. |
| `NOTIFY_SUBJECT_TEMPLATE` | `notify.user.{0}.desktop` | Subscribe subject; `{0}` is replaced with the user ID |
| `NOTIFY_ACK_SUBJECT` | `notify.ack.desktop` | Subject the agent publishes acks to |
| `NOTIFY_USER_ID` | — | Required identity when not using OIDC sign-in (below) |
| `NOTIFY_DEVICE_ID` | hostname-derived | Optional override for the device identifier in acks |
| `NOTIFY_AAD_CLIENT_ID` | — | If set, switches identity to the OIDC device-code flow instead of `NOTIFY_USER_ID` |
| `NOTIFY_AAD_TENANT_ID` | `organizations` | Entra tenant for the device-code flow |
| `NOTIFY_NATS_CREDS_FILE` | — | Path to a standard NATS `.creds` file (JWT + NKey seed). If set, both heads authenticate to NATS with it. |
| `NOTIFY_NATS_AUTH_SERVICE_URL` | — | HTTPS endpoint of an external NATS auth service (Windows head only). If set, takes precedence over `NOTIFY_NATS_CREDS_FILE` and requires `NOTIFY_AAD_CLIENT_ID` + `NOTIFY_NATS_AUTH_SERVICE_SCOPE`. |
| `NOTIFY_NATS_AUTH_SERVICE_SCOPE` | — | AAD scope requested for the token used to call the external auth service. Required when `NOTIFY_NATS_AUTH_SERVICE_URL` is set. |

### Identity

- **Env identity (default, used above):** set `NOTIFY_USER_ID`. Simplest path for local dev.
- **Device-code OIDC sign-in:** set `NOTIFY_AAD_CLIENT_ID` (and optionally `NOTIFY_AAD_TENANT_ID`). On startup the agent prints a URL and a code — sign in with any browser, on any device — then resolves the signed-in user's Entra object ID as the identity. This is the Rust agent's replacement for the C# agent's Windows-broker (WAM) sign-in, since no WAM equivalent exists outside .NET.

### NATS authentication

By default the agent connects to NATS unauthenticated, same as before. Set `NOTIFY_NATS_CREDS_FILE` to authenticate with a standard `.creds` file (works with both heads). On Windows, set `NOTIFY_NATS_AUTH_SERVICE_URL` + `NOTIFY_NATS_AUTH_SERVICE_SCOPE` (alongside `NOTIFY_AAD_CLIENT_ID`) to instead authenticate via an external HTTPS auth service that reuses the same AAD sign-in — the NATS JWT is refreshed automatically on every connect and reconnect.

Run with `RUST_LOG=debug` to see each step of this flow as it happens: which auth mode was selected at startup, identity resolution, the NATS connect/connected transition, and — for the external-auth-service mode — the callback firing on each connect/reconnect attempt, the AAD token acquisition, and the JWT fetch, each as a separate `nats auth: ...` / `aad: ...` log line.

## Build the Windows head

The Windows binary can be **cross-compiled from Linux** into a real, runnable `.exe` — no Windows machine needed to produce the artifact (only to run and visually verify it):

```bash
sudo apt-get install -y mingw-w64        # one-time; provides the linker
rustup target add x86_64-pc-windows-gnu  # one-time
cargo build --release --target x86_64-pc-windows-gnu -p notify-agent-windows
```

The linked binary lands at `target/x86_64-pc-windows-gnu/release/notify-agent-windows.exe`. Copy it to a Windows 11 machine and run it (same environment variables as the console head apply). It registers a per-user AppUserModelID, enforces a single running instance per session, and renders native toast notifications including avatar images (downloaded and cached under `%LOCALAPPDATA%\DesktopNotificationAgent\`). It runs as a system tray app (no console window): the icon (`notify-agent-windows/assets/app.ico`, embedded via `icon.rc`/`build.rs`) appears in the tray immediately on launch, and right-clicking it shows the running version plus a Close item that shuts the agent down.

`.cargo/config.toml` in this directory configures the mingw linker for the cross-compile target automatically — no manual linker flags needed.

**Manual smoke-test checklist** (tray/`Shell_NotifyIconW` code has no automated coverage — it needs a live Windows desktop session):
- Launch the `.exe` — the tray icon appears immediately, before the agent has finished connecting to NATS.
- Right-click the icon — the menu shows "Version x.y.z" (disabled, not clickable) and "Close".
- Click Close — the icon disappears immediately and the process exits (check Task Manager) within ~5 seconds.
- Point `NOTIFY_NATS_URL` at an unreachable host, relaunch — the tray icon still appears, the tooltip flags the failure (hover to see "... (agent failed to start)"), and Close still terminates the process immediately (no `AgentHost` to wait on).

**Changing the tray icon:** replace `notify-agent-windows/assets/app.ico` and rebuild — no other file needs to change. **Use classic BMP/DIB-encoded frames, not PNG-compressed ones**, when regenerating the `.ico` (e.g. Pillow: `img.save(..., format="ICO", sizes=[...], bitmap_format="bmp")` — its default is PNG). GNU `windres` (the resource compiler `mingw-w64` provides, used by `build.rs` via the `embed-resource` crate) has a long-documented history of mishandling PNG-format icon directory entries: the resource bytes can look completely fine under inspection (right sizes, right resource IDs, `RT_ICON`/`RT_GROUP_ICON` all present) while the icon still fails to render at runtime. This bit us once already — an all-PNG `.ico` produced a binary that inspected as correct but showed no tray icon on real Windows.

```bash
./scripts/build-windows-docker.sh
```

This builds a small image (`docker/windows-cross.Dockerfile`, Rust + `mingw-w64`) and runs the same `cargo build --release --target x86_64-pc-windows-gnu -p notify-agent-windows` inside it. The `rust/` directory is bind-mounted into the container at `/workspace`, so `target/` is written straight to the host filesystem — the `.exe` lands at the same path as the host build (`target/x86_64-pc-windows-gnu/release/notify-agent-windows.exe`) and stays there after the container exits. The Cargo registry and rustup toolchain are cached in named Docker volumes so repeat builds don't re-download them.

## Troubleshooting

- **`cargo test` hangs or the integration test fails oddly:** confirm nothing else is bound to port 4222, and that the NATS container is actually running (`docker ps`).
- **Console head exits immediately with an identity error:** you need either `NOTIFY_USER_ID` or `NOTIFY_AAD_CLIENT_ID` set.
- **Windows cross-compile fails at the link step:** re-check `mingw-w64` is installed and `x86_64-pc-windows-gnu` is added (`rustup target list --installed`).
- **No image appears in a Windows toast:** the agent downloads images best-effort (3 MB / 3 s limits, `https://` only) and silently falls back to a text-only toast on any failure — check the agent's log output (`RUST_LOG=debug`) for the dropped-image reason.
- **NATS auth isn't behaving as expected (wrong mode selected, connect hangs, external-auth-service calls not firing):** run with `RUST_LOG=debug` and look for the `nats auth: mode = ...` line logged once at startup, then `nats auth [creds-file]: ...` / `nats auth [external-service]: ...` / `aad: ...` lines for each subsequent step. A missing `nats: connected` line after `nats: connecting` means the connect attempt itself is stuck or failing — check the NATS server is reachable at the configured `NOTIFY_NATS_URL`.
