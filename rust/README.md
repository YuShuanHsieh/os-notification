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
| `NOTIFY_NATS_URL` | `nats://127.0.0.1:4222` | NATS server to connect to |
| `NOTIFY_SUBJECT_TEMPLATE` | `notify.user.{0}.desktop` | Subscribe subject; `{0}` is replaced with the user ID |
| `NOTIFY_ACK_SUBJECT` | `notify.ack.desktop` | Subject the agent publishes acks to |
| `NOTIFY_USER_ID` | — | Required identity when not using OIDC sign-in (below) |
| `NOTIFY_DEVICE_ID` | hostname-derived | Optional override for the device identifier in acks |
| `NOTIFY_AAD_CLIENT_ID` | — | If set, switches identity to the OIDC device-code flow instead of `NOTIFY_USER_ID` |
| `NOTIFY_AAD_TENANT_ID` | `organizations` | Entra tenant for the device-code flow |

### Identity

- **Env identity (default, used above):** set `NOTIFY_USER_ID`. Simplest path for local dev.
- **Device-code OIDC sign-in:** set `NOTIFY_AAD_CLIENT_ID` (and optionally `NOTIFY_AAD_TENANT_ID`). On startup the agent prints a URL and a code — sign in with any browser, on any device — then resolves the signed-in user's Entra object ID as the identity. This is the Rust agent's replacement for the C# agent's Windows-broker (WAM) sign-in, since no WAM equivalent exists outside .NET.

## Build the Windows head

The Windows binary can be **cross-compiled from Linux** into a real, runnable `.exe` — no Windows machine needed to produce the artifact (only to run and visually verify it):

```bash
sudo apt-get install -y mingw-w64        # one-time; provides the linker
rustup target add x86_64-pc-windows-gnu  # one-time
cargo build --release --target x86_64-pc-windows-gnu -p notify-agent-windows
```

The linked binary lands at `target/x86_64-pc-windows-gnu/release/notify-agent-windows.exe`. Copy it to a Windows 11 machine and run it (same environment variables as the console head apply). It registers a per-user AppUserModelID, enforces a single running instance per session, and renders native toast notifications including avatar images (downloaded and cached under `%LOCALAPPDATA%\DesktopNotificationAgent\`).

`.cargo/config.toml` in this directory configures the mingw linker for the cross-compile target automatically — no manual linker flags needed.

## Troubleshooting

- **`cargo test` hangs or the integration test fails oddly:** confirm nothing else is bound to port 4222, and that the NATS container is actually running (`docker ps`).
- **Console head exits immediately with an identity error:** you need either `NOTIFY_USER_ID` or `NOTIFY_AAD_CLIENT_ID` set.
- **Windows cross-compile fails at the link step:** re-check `mingw-w64` is installed and `x86_64-pc-windows-gnu` is added (`rustup target list --installed`).
- **No image appears in a Windows toast:** the agent downloads images best-effort (3 MB / 3 s limits, `https://` only) and silently falls back to a text-only toast on any failure — check the agent's log output (`RUST_LOG=debug`) for the dropped-image reason.
