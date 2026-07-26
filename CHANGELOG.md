# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Rust notification-agent workspace with cross-platform core, Linux console,
  and Windows heads, wire-compatible with the existing C# agent.
- Rust event parsing, bounded deduplication and aggregation, toast content,
  acknowledgement telemetry, identity providers, and NATS integration.
- Windows system-tray integration for both C# and Rust heads, including version
  display, single-instance behavior, startup-failure status, and graceful close
  with timeout handling.
- Schema 1.1 avatar-image support with HTTPS-only validation, bounded caching,
  and best-effort fallback to text-only toasts.
- NATS WebSocket (`ws://`/`wss://`) transport support.
- Pluggable NATS authentication with `.creds` files and the Windows external
  authentication service, including AAD token refresh.
- TestPublisher scenario presets and named flags for exercising notification
  flows end to end.
- A Docker-based, offline-capable Windows cross-build environment for the Rust
  Windows head.

### Changed

- Rust toolchain and Windows cross-compilation support are now documented and
  configured for the Rust agent.
- Rust and C# notification tooling now share the documented image, scenario,
  and acknowledgement payload behavior.

### Fixed

- Enforced HTTPS action URLs and safe toast image handling, including URI
  encoding, download limits, and unique temporary files.
- Restored legacy TestPublisher payload key ordering for compatibility.
- Added bounded HTTP timeouts and integration coverage for external NATS
  authentication.

### Documentation

- Added Rust agent setup, configuration, build, test, authentication, and
  troubleshooting documentation.
- Added Windows tray verification guidance and Rust Windows cross-build
  instructions.

<!-- PR #14: feat(rust): NATS WebSocket + pluggable auth (merged 2026-07-23) -->
