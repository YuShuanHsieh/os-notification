#!/usr/bin/env bash
# Cross-compile notify-agent-windows inside Docker.
#
# The rust/ workspace is bind-mounted into the container at /workspace, so
# cargo's target/ directory is written straight to the host filesystem —
# the .exe is still there once the container exits, no copy-out step needed.
set -euo pipefail

TARGET="${1:-x86_64-pc-windows-gnu}"
IMAGE_TAG="notify-agent-windows-builder"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUST_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

docker build -t "$IMAGE_TAG" -f "$RUST_DIR/docker/windows-cross.Dockerfile" "$RUST_DIR/docker"

docker run --rm \
  -v "$RUST_DIR:/workspace" \
  -v notify-agent-cargo-registry:/usr/local/cargo/registry \
  -v notify-agent-rustup:/usr/local/rustup \
  -w /workspace \
  "$IMAGE_TAG" \
  bash -c '
    set -euo pipefail
    rustup target add "$1"
    cargo build --release --target "$1" -p notify-agent-windows
    chown -R "$2":"$3" target
  ' _ "$TARGET" "$(id -u)" "$(id -g)"

echo "Built: $RUST_DIR/target/$TARGET/release/notify-agent-windows.exe"
