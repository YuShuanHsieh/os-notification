# Rust Notification Agent Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Rust port of the desktop notification agent per `docs/superpowers/specs/2026-07-20-rust-notification-agent-design.md`: identical NATS wire API to the C# agent, device-code OIDC identity, the three shutdown/ordering fixes baked in, and a Windows exe cross-compiled from Linux.

**Architecture:** Cargo workspace under `rust/` with a cross-platform core library (`notify-agent-core`: parser, dedup, aggregator, pipeline, ack telemetry, identity, NATS host — all behind `ToastRenderer`/`IdentityProvider`/`TelemetryPublisher` traits) and two thin binary heads: a Linux console head and a Windows head (windows-rs WinRT toasts, target-gated so Linux builds stay green).

**Tech Stack:** Rust 1.96.1 (pinned), tokio + tokio-util, async-nats, serde/serde_json, chrono, unicode-segmentation, async-trait, thiserror/anyhow, tracing, reqwest (rustls), base64, gethostname, futures; windows + winreg (Windows target only). Existing C# `tools/TestPublisher` for e2e.

## Global Constraints

Copied from the spec; every task implicitly includes these.

- Wire parity with C#: subscribe `notify.user.{userId}.desktop` (Core NATS, no JetStream); acks to `notify.ack.desktop`; ack JSON camelCase `eventId`, `deviceId`, `agentReceivedAt`, `toastSubmittedAt` (omitted when null), `status`; statuses exactly `observed_by_agent` / `submitted_to_windows`, never `published`/`unobserved`.
- Env vars identical to C#: `NOTIFY_NATS_URL`, `NOTIFY_SUBJECT_TEMPLATE` (placeholder literal `{0}`), `NOTIFY_ACK_SUBJECT`, `NOTIFY_USER_ID`, `NOTIFY_DEVICE_ID`, `NOTIFY_AAD_CLIENT_ID`, `NOTIFY_AAD_TENANT_ID`.
- Parse defaults: `deduplicationKey`→`eventId`; `aggregationKey`→`notificationType`→`"unknown"`; unknown priority→normal. Required fields (named in error): `eventId`, `target.userId`, `content.title`, `content.message`.
- Limits: payload ≤ 32768 bytes; JSON depth ≤ 16 (string-aware pre-parse byte scan; depth 16 allowed, 17 rejected); title 120 / message 500 extended grapheme clusters, `…` counted inside the limit.
- Pipeline constants: queue 500, 2 workers, 100 buckets max, important window 2s, normal window 10s, dedup 10 000 entries / 10 min TTL, drain timeout 5s.
- Best-effort doctrine: invalid/duplicate events drop silently before any ack; render failures logged and swallowed; overflow drops counted (`dropped_queue_full`, `dropped_bucket_overflow`); never unbounded buffering.
- Spec §5 improvements are requirements: monotonic `seq` stamped by the subscribe loop; replaceable keeps highest-seq; batch "latest" = highest seq; shutdown = close intake → drain workers → flush buckets → await in-flight renders with 5s timeout → close NATS last; subscribe-loop death logs and exits 1.
- Rust **1.96.1** pinned via `rust/rust-toolchain.toml`. Crate versions below are known-good minimums — if a listed version no longer resolves or its API drifted, use the current release and adapt minimally, noting it in your report.

## Environment facts (verified 2026-07-21)

- Work in the worktree `/home/cjamhe01385/os-notification/.worktrees/rust-agent` (branch `rust-agent` off `main`) — the main checkout is occupied by another branch. All paths below are relative to the worktree root.
- No Rust toolchain installed → Task 1 installs rustup into `~/.cargo`. **Every task assumes** `export PATH="$HOME/.cargo/bin:$PATH"`.
- `sudo apt-get` works without password; `mingw-w64` NOT yet installed → Task 9 installs it.
- A NATS server is live on `localhost:4222`, owned by a pre-existing container that must NOT be stopped/removed. Just use it.
- .NET SDK at `~/.dotnet` (`export PATH="$HOME/.dotnet:$PATH"`) for the C# TestPublisher in Task 8.
- `cargo` commands run from `rust/` unless stated otherwise.

## File Structure

```
rust/
├── rust-toolchain.toml
├── Cargo.toml                          # workspace
├── .cargo/config.toml                  # mingw linker for x86_64-pc-windows-gnu (Task 9)
├── notify-agent-core/
│   ├── Cargo.toml
│   ├── src/lib.rs                      # pub mod declarations
│   ├── src/model.rs                    # Task 1
│   ├── src/parser.rs                   # Task 1
│   ├── src/grapheme.rs                 # Task 2
│   ├── src/toast.rs                    # Task 2
│   ├── src/dedup.rs                    # Task 3
│   ├── src/aggregator.rs               # Task 4
│   ├── src/ack.rs                      # Task 5
│   ├── src/pipeline.rs                 # Task 6
│   ├── src/identity.rs                 # Task 7
│   ├── src/host.rs                     # Task 7
│   └── tests/nats_integration.rs       # Task 7
├── notify-agent-console/               # Task 8
│   ├── Cargo.toml
│   └── src/main.rs
└── notify-agent-windows/               # Task 9
    ├── Cargo.toml
    └── src/main.rs                     # cfg-gated; stub on non-Windows
```

Unit tests live in `#[cfg(test)] mod tests` at the bottom of each module (Rust convention). Interfaces flow: Task 1 → model+parser; Task 2 → toast contract; Tasks 3–5 leaves; Task 6 wires 1–5; Task 7 adds identity+NATS host; Tasks 8–9 are heads.

---

### Task 1: Toolchain, workspace, model, and parser

**Files:**
- Create: `rust/rust-toolchain.toml`, `rust/Cargo.toml`, `rust/notify-agent-core/Cargo.toml`, `rust/notify-agent-core/src/lib.rs`
- Create: `rust/notify-agent-core/src/model.rs`, `rust/notify-agent-core/src/parser.rs`

**Interfaces:**
- Produces: `enum Priority { Normal, Important, Critical }` (derives `Debug, Clone, Copy, PartialEq, Eq, Hash`); `struct InboundNotification` (fields below, derives `Debug, Clone, PartialEq`); `parser::parse_event(payload: &[u8], received_at: DateTime<Utc>, seq: u64) -> Result<InboundNotification, ParseError>`; consts `parser::MAX_PAYLOAD_BYTES = 32768`, `parser::MAX_JSON_DEPTH = 16`.

- [ ] **Step 1: Install Rust 1.96.1 and scaffold the workspace**

```bash
cd /home/cjamhe01385/os-notification/.worktrees/rust-agent
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs -o /tmp/rustup-init.sh
sh /tmp/rustup-init.sh -y --default-toolchain 1.96.1 --profile minimal
export PATH="$HOME/.cargo/bin:$PATH"
rustc --version    # Expected: rustc 1.96.1 (...)
mkdir -p rust/notify-agent-core/src
```

Create `rust/rust-toolchain.toml`:

```toml
[toolchain]
channel = "1.96.1"
```

Create `rust/Cargo.toml`:

```toml
[workspace]
resolver = "2"
members = ["notify-agent-core"]
```

Create `rust/notify-agent-core/Cargo.toml`:

```toml
[package]
name = "notify-agent-core"
version = "0.1.0"
edition = "2021"

[dependencies]
anyhow = "1"
async-nats = "0.38"
async-trait = "0.1"
base64 = "0.22"
chrono = { version = "0.4", features = ["serde"] }
futures = "0.3"
gethostname = "1"
reqwest = { version = "0.12", default-features = false, features = ["rustls-tls", "json"] }
serde = { version = "1", features = ["derive"] }
serde_json = "1"
thiserror = "2"
tokio = { version = "1", features = ["full"] }
tokio-util = { version = "0.7", features = ["rt"] }
tracing = "0.1"
unicode-segmentation = "1"

[dev-dependencies]
tokio = { version = "1", features = ["full", "test-util"] }
```

Create `rust/notify-agent-core/src/lib.rs`:

```rust
pub mod model;
pub mod parser;
```

```bash
cd rust && cargo build 2>&1 | tail -2   # Expected: error: file not found for module `model` (next step creates it) — or run after Step 3
```

- [ ] **Step 2: Write the failing parser tests**

Create `rust/notify-agent-core/src/model.rs`:

```rust
use chrono::{DateTime, Utc};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum Priority {
    Normal,
    Important,
    Critical,
}

/// Normalized, validated notification event as consumed by the pipeline.
/// `seq` is a monotonic arrival stamp assigned by the (single-threaded)
/// subscribe loop; all ordering decisions use it (spec §5.1).
#[derive(Debug, Clone, PartialEq)]
pub struct InboundNotification {
    pub seq: u64,
    pub event_id: String,
    pub user_id: String,
    pub title: String,
    pub message: String,
    pub secondary_text: Option<String>,
    pub action_label: Option<String>,
    pub action_url: Option<String>,
    pub priority: Priority,
    pub aggregation_key: String,
    pub deduplication_key: String,
    pub replaceable: bool,
    pub producer_created_at: Option<DateTime<Utc>>,
    pub server_published_at: Option<DateTime<Utc>>,
    pub received_at: DateTime<Utc>,
}
```

Create `rust/notify-agent-core/src/parser.rs` containing ONLY the test module for now (so the red step is a missing-symbol compile failure):

```rust
#[cfg(test)]
mod tests {
    use super::*;
    use crate::model::Priority;
    use chrono::{DateTime, Utc};

    fn received_at() -> DateTime<Utc> {
        "2026-07-15T08:30:00.190Z".parse().unwrap()
    }

    // Exact example payload from design doc §7 (same fixture as the C# test).
    const DOC_EXAMPLE: &str = r#"{
      "schemaVersion": "1.0",
      "eventId": "evt-12345",
      "notificationType": "billing.invoice.ready",
      "target": { "userId": "u_7f92a845" },
      "content": {
        "title": "Invoice ready",
        "message": "Invoice INV-8492 is ready for review.",
        "secondaryText": "Contoso Billing"
      },
      "action": { "label": "View invoice", "url": "https://app.example.com/invoices/8492" },
      "classification": {
        "priority": "normal",
        "aggregationKey": "billing.invoice.ready",
        "deduplicationKey": "invoice.ready:8492",
        "replaceable": false
      },
      "timestamps": {
        "producerCreatedAt": "2026-07-15T08:30:00.100Z",
        "serverPublishedAt": "2026-07-15T08:30:00.150Z"
      }
    }"#;

    #[test]
    fn parses_doc_example_payload() {
        let n = parse_event(DOC_EXAMPLE.as_bytes(), received_at(), 7).unwrap();
        assert_eq!(n.seq, 7);
        assert_eq!(n.event_id, "evt-12345");
        assert_eq!(n.user_id, "u_7f92a845");
        assert_eq!(n.title, "Invoice ready");
        assert_eq!(n.message, "Invoice INV-8492 is ready for review.");
        assert_eq!(n.secondary_text.as_deref(), Some("Contoso Billing"));
        assert_eq!(n.action_label.as_deref(), Some("View invoice"));
        assert_eq!(n.action_url.as_deref(), Some("https://app.example.com/invoices/8492"));
        assert_eq!(n.priority, Priority::Normal);
        assert_eq!(n.aggregation_key, "billing.invoice.ready");
        assert_eq!(n.deduplication_key, "invoice.ready:8492");
        assert!(!n.replaceable);
        assert_eq!(n.producer_created_at, Some("2026-07-15T08:30:00.100Z".parse().unwrap()));
        assert_eq!(n.server_published_at, Some("2026-07-15T08:30:00.150Z".parse().unwrap()));
        assert_eq!(n.received_at, received_at());
    }

    #[test]
    fn maps_priority_strings() {
        for (s, expected) in [
            ("critical", Priority::Critical),
            ("important", Priority::Important),
            ("normal", Priority::Normal),
            ("garbage", Priority::Normal), // unknown degrades to normal
        ] {
            let json = format!(
                r#"{{"eventId":"e1","target":{{"userId":"u1"}},
                     "content":{{"title":"t","message":"m"}},
                     "classification":{{"priority":"{s}"}}}}"#
            );
            let n = parse_event(json.as_bytes(), received_at(), 1).unwrap();
            assert_eq!(n.priority, expected, "priority string {s:?}");
        }
    }

    #[test]
    fn applies_defaults_for_missing_optional_fields() {
        let json = br#"{"eventId":"e1","notificationType":"a.b",
                        "target":{"userId":"u1"},"content":{"title":"t","message":"m"}}"#;
        let n = parse_event(json, received_at(), 1).unwrap();
        assert_eq!(n.deduplication_key, "e1"); // defaults to eventId
        assert_eq!(n.aggregation_key, "a.b");  // defaults to notificationType
        assert_eq!(n.priority, Priority::Normal);
        assert!(!n.replaceable);
        assert_eq!(n.action_label, None);
        assert_eq!(n.producer_created_at, None);
    }

    #[test]
    fn aggregation_key_falls_back_to_unknown() {
        let json = br#"{"eventId":"e1","target":{"userId":"u1"},"content":{"title":"t","message":"m"}}"#;
        let n = parse_event(json, received_at(), 1).unwrap();
        assert_eq!(n.aggregation_key, "unknown");
    }

    #[test]
    fn rejects_missing_required_fields() {
        for (json, field) in [
            (r#"{"target":{"userId":"u1"},"content":{"title":"t","message":"m"}}"#, "eventId"),
            (r#"{"eventId":"e1","content":{"title":"t","message":"m"}}"#, "target.userId"),
            (r#"{"eventId":"e1","target":{"userId":"u1"},"content":{"message":"m"}}"#, "content.title"),
            (r#"{"eventId":"e1","target":{"userId":"u1"},"content":{"title":"t"}}"#, "content.message"),
        ] {
            let err = parse_event(json.as_bytes(), received_at(), 1).unwrap_err();
            assert!(err.to_string().contains(field), "error {err} should name {field}");
        }
    }

    #[test]
    fn rejects_payload_over_32kb() {
        let big = vec![b' '; MAX_PAYLOAD_BYTES + 1];
        let err = parse_event(&big, received_at(), 1).unwrap_err();
        assert!(err.to_string().contains("exceeds"));
    }

    #[test]
    fn depth_boundary_16_allowed_17_rejected() {
        // depth 16: {"a":{"a":...1...}} with 16 opening braces. It passes the
        // depth gate, then deterministically fails the eventId requirement —
        // proving the gate did NOT fire at exactly 16.
        let d16 = format!("{}1{}", r#"{"a":"#.repeat(16), "}".repeat(16));
        let err = parse_event(d16.as_bytes(), received_at(), 1).unwrap_err();
        assert!(err.to_string().contains("missing eventId"), "depth 16 must pass the depth gate, got: {err}");
        let d17 = format!("{}1{}", r#"{"a":"#.repeat(17), "}".repeat(17));
        let err = parse_event(d17.as_bytes(), received_at(), 1).unwrap_err();
        assert!(err.to_string().to_lowercase().contains("depth"), "got: {err}");
    }

    #[test]
    fn rejects_malformed_empty_and_null_payloads() {
        assert!(parse_event(b"not json", received_at(), 1).is_err());
        assert!(parse_event(b"", received_at(), 1).is_err());
        assert!(parse_event(b"null", received_at(), 1).is_err());
    }
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd rust && cargo test -p notify-agent-core 2>&1 | tail -5`
Expected: compile FAILURE — `cannot find function parse_event` (this is the red).

- [ ] **Step 4: Implement the parser**

Prepend to `rust/notify-agent-core/src/parser.rs` (above the test module):

```rust
use chrono::{DateTime, Utc};
use serde::Deserialize;

use crate::model::{InboundNotification, Priority};

pub const MAX_PAYLOAD_BYTES: usize = 32 * 1024;
pub const MAX_JSON_DEPTH: usize = 16;

#[derive(Debug, thiserror::Error)]
pub enum ParseError {
    #[error("empty payload")]
    Empty,
    #[error("payload {0} bytes exceeds {MAX_PAYLOAD_BYTES}")]
    TooLarge(usize),
    #[error("json depth exceeds {MAX_JSON_DEPTH}")]
    TooDeep,
    #[error("invalid json: {0}")]
    Json(String),
    #[error("missing {0}")]
    MissingField(&'static str),
}

pub fn parse_event(
    payload: &[u8],
    received_at: DateTime<Utc>,
    seq: u64,
) -> Result<InboundNotification, ParseError> {
    if payload.is_empty() {
        return Err(ParseError::Empty);
    }
    if payload.len() > MAX_PAYLOAD_BYTES {
        return Err(ParseError::TooLarge(payload.len()));
    }
    if depth_exceeds(payload, MAX_JSON_DEPTH) {
        return Err(ParseError::TooDeep);
    }

    let wire: WireEvent = serde_json::from_slice(payload)
        .map_err(|e| ParseError::Json(e.to_string()))?;

    let event_id = require(wire.event_id, "eventId")?;
    let user_id = require(wire.target.and_then(|t| t.user_id), "target.userId")?;
    let content = wire.content.unwrap_or_default();
    let title = require(content.title, "content.title")?;
    let message = require(content.message, "content.message")?;

    let notification_type = non_blank(wire.notification_type).unwrap_or_else(|| "unknown".into());
    let classification = wire.classification.unwrap_or_default();
    let priority = match classification.priority.as_deref().map(str::to_lowercase).as_deref() {
        Some("critical") => Priority::Critical,
        Some("important") => Priority::Important,
        _ => Priority::Normal,
    };
    let action = wire.action.unwrap_or_default();
    let timestamps = wire.timestamps.unwrap_or_default();

    Ok(InboundNotification {
        seq,
        aggregation_key: non_blank(classification.aggregation_key).unwrap_or(notification_type),
        deduplication_key: non_blank(classification.deduplication_key).unwrap_or_else(|| event_id.clone()),
        event_id,
        user_id,
        title,
        message,
        secondary_text: non_blank(content.secondary_text),
        action_label: non_blank(action.label),
        action_url: non_blank(action.url),
        priority,
        replaceable: classification.replaceable.unwrap_or(false),
        producer_created_at: timestamps.producer_created_at,
        server_published_at: timestamps.server_published_at,
        received_at,
    })
}

fn non_blank(v: Option<String>) -> Option<String> {
    v.filter(|s| !s.trim().is_empty())
}

fn require(v: Option<String>, field: &'static str) -> Result<String, ParseError> {
    non_blank(v).ok_or(ParseError::MissingField(field))
}

/// String-aware structural depth scan; enforces the depth limit before the
/// full parse (serde_json's own recursion limit is 128, far above ours).
fn depth_exceeds(payload: &[u8], max_depth: usize) -> bool {
    let (mut depth, mut in_string, mut escaped) = (0usize, false, false);
    for &b in payload {
        if in_string {
            if escaped {
                escaped = false;
            } else if b == b'\\' {
                escaped = true;
            } else if b == b'"' {
                in_string = false;
            }
            continue;
        }
        match b {
            b'"' => in_string = true,
            b'{' | b'[' => {
                depth += 1;
                if depth > max_depth {
                    return true;
                }
            }
            b'}' | b']' => depth = depth.saturating_sub(1),
            _ => {}
        }
    }
    false
}

#[derive(Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
struct WireEvent {
    #[allow(dead_code)]
    schema_version: Option<String>,
    event_id: Option<String>,
    notification_type: Option<String>,
    target: Option<WireTarget>,
    content: Option<WireContent>,
    action: Option<WireAction>,
    classification: Option<WireClassification>,
    timestamps: Option<WireTimestamps>,
}

#[derive(Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
struct WireTarget {
    user_id: Option<String>,
}

#[derive(Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
struct WireContent {
    title: Option<String>,
    message: Option<String>,
    secondary_text: Option<String>,
}

#[derive(Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
struct WireAction {
    label: Option<String>,
    url: Option<String>,
}

#[derive(Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
struct WireClassification {
    priority: Option<String>,
    aggregation_key: Option<String>,
    deduplication_key: Option<String>,
    replaceable: Option<bool>,
}

#[derive(Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
struct WireTimestamps {
    producer_created_at: Option<DateTime<Utc>>,
    server_published_at: Option<DateTime<Utc>>,
}
```

Note: `parse_event(b"null", ...)` fails because serde maps JSON `null` to a `WireEvent` deserialize error for a non-Option root — if it instead produces all-None, the `eventId` require check rejects it either way; the test only asserts `is_err()`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd rust && cargo test -p notify-agent-core 2>&1 | tail -5`
Expected: `test result: ok. 8 passed; 0 failed` — and zero warnings in the build output. Fix any warning before committing.

- [ ] **Step 6: Commit**

```bash
cd /home/cjamhe01385/os-notification/.worktrees/rust-agent
git add rust/
git commit -m "feat(rust): workspace scaffold, event model, and parser with 32KB/depth-16 validation"
```

---

### Task 2: Grapheme truncation and toast content factory

**Files:**
- Create: `rust/notify-agent-core/src/grapheme.rs`, `rust/notify-agent-core/src/toast.rs`
- Modify: `rust/notify-agent-core/src/lib.rs` (add `pub mod grapheme; pub mod toast;`)

**Interfaces:**
- Consumes: `InboundNotification` (Task 1).
- Produces: `grapheme::truncate(value: &str, max_graphemes: usize) -> String`; `struct ToastRequest { title: String, message: String, attribution: Option<String>, action_label: Option<String>, action_url: Option<String>, sources: Vec<InboundNotification> }` (derives `Debug, Clone`); `#[async_trait] trait ToastRenderer: Send + Sync { async fn show(&self, toast: &ToastRequest) -> anyhow::Result<DateTime<Utc>>; }` (returns submission timestamp); `toast::from_single(n: &InboundNotification) -> ToastRequest`; `toast::from_batch(batch: &[InboundNotification]) -> ToastRequest` (panics on empty; "latest" = highest `seq`); consts `toast::MAX_TITLE_GRAPHEMES = 120`, `toast::MAX_MESSAGE_GRAPHEMES = 500`.

- [ ] **Step 1: Write the failing tests**

Create `rust/notify-agent-core/src/grapheme.rs` with only:

```rust
#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn returns_short_strings_unchanged() {
        assert_eq!(truncate("hello", 5), "hello");
        assert_eq!(truncate("", 5), "");
    }

    #[test]
    fn truncates_to_limit_with_ellipsis() {
        // 6 chars, limit 5 → 4 kept + "…" = 5 grapheme clusters total
        assert_eq!(truncate("abcdef", 5), "abcd…");
    }

    #[test]
    fn counts_grapheme_clusters_not_chars() {
        // Family emoji: 1 grapheme cluster, 7 chars / 11 UTF-16 units
        let family = "\u{1F469}\u{200D}\u{1F469}\u{200D}\u{1F467}\u{200D}\u{1F466}";
        assert_eq!(truncate(family, 1), family);
        let two = format!("{family}{family}");
        assert_eq!(truncate(&two, 2), two);
        let three = format!("{family}{family}{family}");
        assert_eq!(truncate(&three, 2), format!("{family}…"));
    }
}
```

Create `rust/notify-agent-core/src/toast.rs` with only:

```rust
#[cfg(test)]
mod tests {
    use super::*;
    use crate::model::{InboundNotification, Priority};
    use unicode_segmentation::UnicodeSegmentation;

    /// Shared test-event builder (also used by later tasks' tests).
    pub(crate) fn event(seq: u64, id: &str, message: &str) -> InboundNotification {
        InboundNotification {
            seq,
            event_id: id.into(),
            user_id: "u1".into(),
            title: "Title".into(),
            message: message.into(),
            secondary_text: Some("App".into()),
            action_label: Some("Open".into()),
            action_url: Some("https://example.com/x".into()),
            priority: Priority::Normal,
            aggregation_key: "agg.key".into(),
            deduplication_key: id.into(),
            replaceable: false,
            producer_created_at: None,
            server_published_at: None,
            received_at: "2026-07-15T08:30:00.190Z".parse().unwrap(),
        }
    }

    #[test]
    fn single_event_maps_fields_directly() {
        let n = event(1, "e1", "Message");
        let t = from_single(&n);
        assert_eq!(t.title, "Title");
        assert_eq!(t.message, "Message");
        assert_eq!(t.attribution.as_deref(), Some("App"));
        assert_eq!(t.action_label.as_deref(), Some("Open"));
        assert_eq!(t.action_url.as_deref(), Some("https://example.com/x"));
        assert_eq!(t.sources, vec![n]);
    }

    #[test]
    fn single_event_truncates_title_120_message_500() {
        let mut n = event(1, "e1", &"M".repeat(600));
        n.title = "T".repeat(200);
        let t = from_single(&n);
        assert_eq!(t.title.graphemes(true).count(), 120);
        assert!(t.title.ends_with('…'));
        assert_eq!(t.message.graphemes(true).count(), 500);
        assert!(t.message.ends_with('…'));
    }

    #[test]
    fn batch_of_one_behaves_like_single() {
        let n = event(1, "e1", "Message");
        let t = from_batch(std::slice::from_ref(&n));
        assert_eq!(t.title, "Title");
        assert_eq!(t.message, "Message");
        assert_eq!(t.sources, vec![n]);
    }

    #[test]
    fn batch_summarizes_count_and_latest_by_seq() {
        // deliberately out of positional order: seq decides "latest", not position
        let batch = vec![event(1, "e1", "first"), event(3, "e3", "third"), event(2, "e2", "second")];
        let t = from_batch(&batch);
        assert_eq!(t.title, "3 notifications — agg.key");
        assert_eq!(t.message, "Latest: third");
        assert_eq!(t.sources.len(), 3);
        assert_eq!(t.action_label.as_deref(), Some("Open"));
    }

    #[test]
    #[should_panic(expected = "batch must not be empty")]
    fn empty_batch_panics() {
        from_batch(&[]);
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd rust && cargo test -p notify-agent-core 2>&1 | tail -5`
Expected: compile FAILURE — `cannot find function truncate` / `from_single`. (First add `pub mod grapheme; pub mod toast;` to `lib.rs`.)

- [ ] **Step 3: Implement**

Prepend to `rust/notify-agent-core/src/grapheme.rs`:

```rust
use unicode_segmentation::UnicodeSegmentation;

/// Truncate to at most `max_graphemes` extended grapheme clusters (the design
/// doc's "product limit" unit), ellipsis included in the limit.
pub fn truncate(value: &str, max_graphemes: usize) -> String {
    assert!(max_graphemes >= 1, "max_graphemes must be >= 1");
    if value.graphemes(true).count() <= max_graphemes {
        return value.to_string();
    }
    let kept: String = value.graphemes(true).take(max_graphemes - 1).collect();
    format!("{kept}…")
}
```

Prepend to `rust/notify-agent-core/src/toast.rs`:

```rust
use async_trait::async_trait;
use chrono::{DateTime, Utc};

use crate::grapheme;
use crate::model::InboundNotification;

pub const MAX_TITLE_GRAPHEMES: usize = 120;
pub const MAX_MESSAGE_GRAPHEMES: usize = 500;

/// Renderer-ready toast. `sources` lists every event this toast represents,
/// so the render sink can ack each of them as submitted_to_windows.
#[derive(Debug, Clone)]
pub struct ToastRequest {
    pub title: String,
    pub message: String,
    pub attribution: Option<String>,
    pub action_label: Option<String>,
    pub action_url: Option<String>,
    pub sources: Vec<InboundNotification>,
}

#[async_trait]
pub trait ToastRenderer: Send + Sync {
    /// Submit the toast; returns the submission timestamp (toastSubmittedAt).
    async fn show(&self, toast: &ToastRequest) -> anyhow::Result<DateTime<Utc>>;
}

pub fn from_single(n: &InboundNotification) -> ToastRequest {
    ToastRequest {
        title: grapheme::truncate(&n.title, MAX_TITLE_GRAPHEMES),
        message: grapheme::truncate(&n.message, MAX_MESSAGE_GRAPHEMES),
        attribution: n.secondary_text.clone(),
        action_label: n.action_label.clone(),
        action_url: n.action_url.clone(),
        sources: vec![n.clone()],
    }
}

/// Builds one summary toast from a bucket of events. "Latest" is decided by
/// `seq` (the subscribe-loop arrival stamp), never by slice position — this is
/// the spec §5.1 ordering fix over the C# implementation.
pub fn from_batch(batch: &[InboundNotification]) -> ToastRequest {
    assert!(!batch.is_empty(), "batch must not be empty");
    if batch.len() == 1 {
        return from_single(&batch[0]);
    }
    let latest = batch.iter().max_by_key(|n| n.seq).expect("non-empty");
    ToastRequest {
        title: grapheme::truncate(
            &format!("{} notifications — {}", batch.len(), latest.aggregation_key),
            MAX_TITLE_GRAPHEMES,
        ),
        message: grapheme::truncate(&format!("Latest: {}", latest.message), MAX_MESSAGE_GRAPHEMES),
        attribution: latest.secondary_text.clone(),
        action_label: latest.action_label.clone(),
        action_url: latest.action_url.clone(),
        sources: batch.to_vec(),
    }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd rust && cargo test -p notify-agent-core 2>&1 | tail -5`
Expected: `test result: ok. 16 passed; 0 failed` (8 from Task 1 + 8 new), zero warnings.

- [ ] **Step 5: Commit**

```bash
git add rust/notify-agent-core
git commit -m "feat(rust): grapheme-limit toast content factory with seq-based latest selection"
```

---

### Task 3: Bounded TTL deduplication cache

**Files:**
- Create: `rust/notify-agent-core/src/dedup.rs`
- Modify: `rust/notify-agent-core/src/lib.rs` (add `pub mod dedup;`)

**Interfaces:**
- Consumes: `tokio::time::Instant` (respects the paused test clock).
- Produces: `struct DedupCache` with `DedupCache::new(capacity: usize, ttl: Duration) -> Self` (panics if `capacity == 0`), `try_add(&self, key: &str) -> bool` (true = first sighting → process; false = duplicate → drop), `len(&self) -> usize`. `Send + Sync` (interior `std::sync::Mutex`).

- [ ] **Step 1: Write the failing tests**

Create `rust/notify-agent-core/src/dedup.rs` with only:

```rust
#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicU32, Ordering};
    use std::sync::Arc;
    use tokio::time::{advance, Duration};

    #[tokio::test(start_paused = true)]
    async fn first_add_true_second_false() {
        let cache = DedupCache::new(10, Duration::from_secs(600));
        assert!(cache.try_add("k1"));
        assert!(!cache.try_add("k1"));
        assert!(cache.try_add("k2"));
    }

    #[tokio::test(start_paused = true)]
    async fn key_expires_after_ttl() {
        let cache = DedupCache::new(10, Duration::from_secs(600));
        assert!(cache.try_add("k1"));
        advance(Duration::from_secs(540)).await; // 9 min: still within TTL
        assert!(!cache.try_add("k1"));
        advance(Duration::from_secs(120)).await; // 11 min since insert
        assert!(cache.try_add("k1"));
    }

    #[tokio::test(start_paused = true)]
    async fn evicts_oldest_when_over_capacity() {
        let cache = DedupCache::new(2, Duration::from_secs(3600));
        assert!(cache.try_add("a"));
        assert!(cache.try_add("b"));
        assert!(cache.try_add("c")); // evicts "a"
        assert!(cache.try_add("a")); // "a" was forgotten
        assert!(cache.len() <= 2);
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 4)]
    async fn concurrent_adds_have_exactly_one_winner_per_key() {
        let cache = Arc::new(DedupCache::new(10_000, Duration::from_secs(600)));
        let wins = Arc::new(AtomicU32::new(0));
        let mut handles = Vec::new();
        for i in 0..1000 {
            let cache = cache.clone();
            let wins = wins.clone();
            handles.push(tokio::spawn(async move {
                if cache.try_add("same-key") {
                    wins.fetch_add(1, Ordering::Relaxed);
                }
                cache.try_add(&format!("key-{i}"));
            }));
        }
        for h in handles {
            h.await.unwrap();
        }
        assert_eq!(wins.load(Ordering::Relaxed), 1);
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd rust && cargo test -p notify-agent-core 2>&1 | tail -5`
Expected: compile FAILURE — `cannot find struct DedupCache`. (First add `pub mod dedup;` to `lib.rs`.)

- [ ] **Step 3: Implement**

Prepend to `rust/notify-agent-core/src/dedup.rs`:

```rust
use std::collections::{HashMap, VecDeque};
use std::sync::Mutex;
use tokio::time::{Duration, Instant};

/// In-memory duplicate suppression, bounded by entry count and TTL (design §5
/// "Local state"). Uses tokio::time::Instant so tests control the clock.
/// Not persistent — POC scope.
pub struct DedupCache {
    inner: Mutex<Inner>,
    capacity: usize,
    ttl: Duration,
}

struct Inner {
    expiry_by_key: HashMap<String, Instant>,
    insertion_order: VecDeque<(String, Instant)>,
}

impl DedupCache {
    pub fn new(capacity: usize, ttl: Duration) -> Self {
        assert!(capacity >= 1, "capacity must be >= 1");
        Self {
            inner: Mutex::new(Inner {
                expiry_by_key: HashMap::new(),
                insertion_order: VecDeque::new(),
            }),
            capacity,
            ttl,
        }
    }

    pub fn try_add(&self, key: &str) -> bool {
        let now = Instant::now();
        let mut g = self.inner.lock().unwrap();

        // Purge expired from the front: fixed TTL + monotonic clock means the
        // queue is expiry-ordered.
        while g.insertion_order.front().is_some_and(|(_, exp)| *exp <= now) {
            Self::dequeue_one(&mut g);
        }

        if g.expiry_by_key.get(key).is_some_and(|exp| *exp > now) {
            return false;
        }

        while g.expiry_by_key.len() >= self.capacity && !g.insertion_order.is_empty() {
            Self::dequeue_one(&mut g);
        }

        let expires = now + self.ttl;
        g.expiry_by_key.insert(key.to_string(), expires);
        g.insertion_order.push_back((key.to_string(), expires));
        true
    }

    pub fn len(&self) -> usize {
        self.inner.lock().unwrap().expiry_by_key.len()
    }

    pub fn is_empty(&self) -> bool {
        self.len() == 0
    }

    fn dequeue_one(g: &mut Inner) {
        if let Some((key, expires_at)) = g.insertion_order.pop_front() {
            // A re-added key leaves a stale queue entry behind; only remove on
            // exact expiry match.
            if g.expiry_by_key.get(&key) == Some(&expires_at) {
                g.expiry_by_key.remove(&key);
            }
        }
    }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd rust && cargo test -p notify-agent-core 2>&1 | tail -5`
Expected: `test result: ok. 20 passed; 0 failed`, zero warnings.

- [ ] **Step 5: Commit**

```bash
git add rust/notify-agent-core
git commit -m "feat(rust): bounded TTL deduplication cache on the tokio test clock"
```

---

### Task 4: Priority-aware aggregator with drain-safe shutdown

**Files:**
- Create: `rust/notify-agent-core/src/aggregator.rs`
- Modify: `rust/notify-agent-core/src/lib.rs` (add `pub mod aggregator;`)

**Interfaces:**
- Consumes: `InboundNotification`, `Priority` (Task 1); `toast::{from_single, from_batch, ToastRequest}` (Task 2); tokio timers; `tokio_util::task::TaskTracker`.
- Produces: `#[async_trait] trait RenderSink: Send + Sync + 'static { async fn render(&self, toast: ToastRequest); }`; `struct AggregatorConfig { max_buckets: usize, important_window: Duration, normal_window: Duration, drain_timeout: Duration }` (`Default` = 100 / 2s / 10s / 5s, derives `Clone`); `struct Aggregator` (derives `Clone`) with `Aggregator::new(config: AggregatorConfig, sink: Arc<dyn RenderSink>) -> Self`, `add(&self, n: InboundNotification)` (sync — spawns renders on an internal tracker), `dropped_bucket_overflow(&self) -> u64`, `async shutdown(&self)` (flush all buckets, then await in-flight renders bounded by `drain_timeout`).

Behavior (design §6.3 + ADR-007 + spec §5):
- `critical` → render immediately (spawned on the tracker), bypassing buckets.
- `important`/`normal` → bucket keyed `(aggregation_key, priority)`; the bucket flushes once, `important_window`/`normal_window` after its first event, as one `ToastRequest` via `from_batch` with events sorted by `seq`.
- `replaceable == true` → the bucket keeps only the highest-`seq` event; a stale (lower-seq) replaceable arrival is discarded even if it arrives later in wall time.
- More than `max_buckets` concurrent buckets → new events for new keys are dropped and counted.
- `shutdown()` never hangs: `drain_timeout` bounds the wait for in-flight renders.

- [ ] **Step 1: Write the failing tests**

Create `rust/notify-agent-core/src/aggregator.rs` with only:

```rust
#[cfg(test)]
mod tests {
    use super::*;
    use crate::model::Priority;
    use crate::toast::{tests::event, ToastRequest};
    use async_trait::async_trait;
    use std::sync::{Arc, Mutex};
    use tokio::time::{advance, Duration};

    #[derive(Default)]
    struct RecordingSink {
        rendered: Mutex<Vec<ToastRequest>>,
    }

    #[async_trait]
    impl RenderSink for RecordingSink {
        async fn render(&self, toast: ToastRequest) {
            self.rendered.lock().unwrap().push(toast);
        }
    }

    /// Sink that takes `delay` of (paused) time before recording.
    struct SlowSink {
        delay: Duration,
        rendered: Mutex<Vec<ToastRequest>>,
    }

    #[async_trait]
    impl RenderSink for SlowSink {
        async fn render(&self, toast: ToastRequest) {
            tokio::time::sleep(self.delay).await;
            self.rendered.lock().unwrap().push(toast);
        }
    }

    fn prioritized(seq: u64, id: &str, priority: Priority, agg_key: &str, replaceable: bool, message: &str)
        -> crate::model::InboundNotification
    {
        let mut n = event(seq, id, message);
        n.priority = priority;
        n.aggregation_key = agg_key.into();
        n.replaceable = replaceable;
        n
    }

    /// Let spawned tasks (timers, renders) run to quiescence on the paused clock.
    async fn settle() {
        for _ in 0..20 {
            tokio::task::yield_now().await;
        }
    }

    #[tokio::test(start_paused = true)]
    async fn critical_renders_immediately() {
        let sink = Arc::new(RecordingSink::default());
        let agg = Aggregator::new(AggregatorConfig::default(), sink.clone());
        agg.add(prioritized(1, "e1", Priority::Critical, "agg.key", false, "m"));
        settle().await;
        let rendered = sink.rendered.lock().unwrap();
        assert_eq!(rendered.len(), 1);
        assert_eq!(rendered[0].sources[0].event_id, "e1");
    }

    #[tokio::test(start_paused = true)]
    async fn normal_events_batch_and_flush_after_10s() {
        let sink = Arc::new(RecordingSink::default());
        let agg = Aggregator::new(AggregatorConfig::default(), sink.clone());
        for (seq, id) in [(1, "e1"), (2, "e2"), (3, "e3")] {
            agg.add(prioritized(seq, id, Priority::Normal, "agg.key", false, "m"));
        }
        settle().await;
        assert!(sink.rendered.lock().unwrap().is_empty(), "window still open");
        advance(Duration::from_secs(10)).await;
        settle().await;
        let rendered = sink.rendered.lock().unwrap();
        assert_eq!(rendered.len(), 1);
        assert_eq!(rendered[0].sources.len(), 3);
        assert!(rendered[0].title.starts_with("3 notifications"));
    }

    #[tokio::test(start_paused = true)]
    async fn important_flushes_after_2s_normal_does_not() {
        let sink = Arc::new(RecordingSink::default());
        let agg = Aggregator::new(AggregatorConfig::default(), sink.clone());
        agg.add(prioritized(1, "i1", Priority::Important, "imp", false, "m"));
        agg.add(prioritized(2, "n1", Priority::Normal, "norm", false, "m"));
        advance(Duration::from_secs(2)).await;
        settle().await;
        {
            let rendered = sink.rendered.lock().unwrap();
            assert_eq!(rendered.len(), 1);
            assert_eq!(rendered[0].sources[0].event_id, "i1");
        }
        advance(Duration::from_secs(8)).await;
        settle().await;
        assert_eq!(sink.rendered.lock().unwrap().len(), 2);
    }

    #[tokio::test(start_paused = true)]
    async fn replaceable_keeps_highest_seq_even_when_stale_arrives_late() {
        let sink = Arc::new(RecordingSink::default());
        let agg = Aggregator::new(AggregatorConfig::default(), sink.clone());
        agg.add(prioritized(1, "p1", Priority::Normal, "prog", true, "10%"));
        agg.add(prioritized(3, "p3", Priority::Normal, "prog", true, "90%"));
        agg.add(prioritized(2, "p2", Priority::Normal, "prog", true, "60%")); // stale: lower seq arrives later
        advance(Duration::from_secs(10)).await;
        settle().await;
        let rendered = sink.rendered.lock().unwrap();
        assert_eq!(rendered.len(), 1);
        assert_eq!(rendered[0].sources.len(), 1, "replaced, not batched");
        assert_eq!(rendered[0].sources[0].event_id, "p3");
        assert_eq!(rendered[0].message, "90%");
    }

    #[tokio::test(start_paused = true)]
    async fn separate_aggregation_keys_produce_separate_toasts() {
        let sink = Arc::new(RecordingSink::default());
        let agg = Aggregator::new(AggregatorConfig::default(), sink.clone());
        agg.add(prioritized(1, "a1", Priority::Normal, "a", false, "m"));
        agg.add(prioritized(2, "b1", Priority::Normal, "b", false, "m"));
        advance(Duration::from_secs(10)).await;
        settle().await;
        assert_eq!(sink.rendered.lock().unwrap().len(), 2);
    }

    #[tokio::test(start_paused = true)]
    async fn drops_events_beyond_max_buckets() {
        let sink = Arc::new(RecordingSink::default());
        let config = AggregatorConfig { max_buckets: 2, ..Default::default() };
        let agg = Aggregator::new(config, sink.clone());
        agg.add(prioritized(1, "a1", Priority::Normal, "a", false, "m"));
        agg.add(prioritized(2, "b1", Priority::Normal, "b", false, "m"));
        agg.add(prioritized(3, "c1", Priority::Normal, "c", false, "m")); // over cap → dropped
        assert_eq!(agg.dropped_bucket_overflow(), 1);
        advance(Duration::from_secs(10)).await;
        settle().await;
        assert_eq!(sink.rendered.lock().unwrap().len(), 2);
    }

    #[tokio::test(start_paused = true)]
    async fn shutdown_flushes_pending_buckets() {
        let sink = Arc::new(RecordingSink::default());
        let agg = Aggregator::new(AggregatorConfig::default(), sink.clone());
        agg.add(prioritized(1, "e1", Priority::Normal, "agg.key", false, "m"));
        agg.shutdown().await;
        assert_eq!(sink.rendered.lock().unwrap().len(), 1);
    }

    #[tokio::test(start_paused = true)]
    async fn shutdown_waits_for_in_flight_renders() {
        // The C# agent loses this render (fire-and-forget); spec §5.2 fixes it.
        let sink = Arc::new(SlowSink { delay: Duration::from_secs(1), rendered: Mutex::new(Vec::new()) });
        let agg = Aggregator::new(AggregatorConfig::default(), sink.clone());
        agg.add(prioritized(1, "e1", Priority::Critical, "agg.key", false, "m"));
        settle().await;
        agg.shutdown().await; // paused clock auto-advances through the 1s render
        assert_eq!(sink.rendered.lock().unwrap().len(), 1);
    }

    #[tokio::test(start_paused = true)]
    async fn hung_renderer_forfeits_after_drain_timeout() {
        struct HungSink;
        #[async_trait]
        impl RenderSink for HungSink {
            async fn render(&self, _toast: ToastRequest) {
                std::future::pending::<()>().await;
            }
        }
        let agg = Aggregator::new(AggregatorConfig::default(), Arc::new(HungSink));
        agg.add(prioritized(1, "e1", Priority::Critical, "agg.key", false, "m"));
        settle().await;
        // Must complete (auto-advance covers the 5s drain timeout) instead of hanging.
        tokio::time::timeout(Duration::from_secs(60), agg.shutdown())
            .await
            .expect("shutdown must not hang on a stuck renderer");
    }
}
```

Also change the visibility of the test-event builder in `rust/notify-agent-core/src/toast.rs`: the `tests` module there must be `#[cfg(test)] pub(crate) mod tests { ... }` so `crate::toast::tests::event` resolves from this file.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd rust && cargo test -p notify-agent-core 2>&1 | tail -5`
Expected: compile FAILURE — `cannot find struct Aggregator`. (First add `pub mod aggregator;` to `lib.rs`.)

- [ ] **Step 3: Implement**

Prepend to `rust/notify-agent-core/src/aggregator.rs`:

```rust
use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};

use async_trait::async_trait;
use tokio::task::JoinHandle;
use tokio::time::Duration;
use tokio_util::task::TaskTracker;

use crate::model::{InboundNotification, Priority};
use crate::toast::{self, ToastRequest};

/// Downstream of the aggregator: renders a toast and publishes its acks.
#[async_trait]
pub trait RenderSink: Send + Sync + 'static {
    async fn render(&self, toast: ToastRequest);
}

#[derive(Clone)]
pub struct AggregatorConfig {
    pub max_buckets: usize,
    pub important_window: Duration,
    pub normal_window: Duration,
    pub drain_timeout: Duration,
}

impl Default for AggregatorConfig {
    fn default() -> Self {
        Self {
            max_buckets: 100,                          // design §9
            important_window: Duration::from_secs(2),  // design §6.3
            normal_window: Duration::from_secs(10),    // design §6.3
            drain_timeout: Duration::from_secs(5),     // spec §5.2/5.3
        }
    }
}

type Key = (String, Priority);

struct Bucket {
    events: Vec<InboundNotification>,
    timer: JoinHandle<()>,
}

struct Inner {
    config: AggregatorConfig,
    buckets: Mutex<HashMap<Key, Bucket>>,
    sink: Arc<dyn RenderSink>,
    renders: TaskTracker,
    dropped_bucket_overflow: AtomicU64,
}

/// Owns priority handling, batching, and latest-state replacement (ADR-007).
/// Best-effort in steady state, but shutdown drains in-flight renders bounded
/// by `drain_timeout` (spec §5.2/5.3) instead of fire-and-forget.
#[derive(Clone)]
pub struct Aggregator {
    inner: Arc<Inner>,
}

impl Aggregator {
    pub fn new(config: AggregatorConfig, sink: Arc<dyn RenderSink>) -> Self {
        Self {
            inner: Arc::new(Inner {
                config,
                buckets: Mutex::new(HashMap::new()),
                sink,
                renders: TaskTracker::new(),
                dropped_bucket_overflow: AtomicU64::new(0),
            }),
        }
    }

    pub fn dropped_bucket_overflow(&self) -> u64 {
        self.inner.dropped_bucket_overflow.load(Ordering::Relaxed)
    }

    pub fn add(&self, n: InboundNotification) {
        if n.priority == Priority::Critical {
            let sink = self.inner.sink.clone();
            let toast = toast::from_single(&n);
            self.inner.renders.spawn(async move { sink.render(toast).await });
            return;
        }

        let key: Key = (n.aggregation_key.clone(), n.priority);
        let mut buckets = self.inner.buckets.lock().unwrap();

        if let Some(bucket) = buckets.get_mut(&key) {
            apply_to_bucket(&mut bucket.events, n);
            return;
        }
        if buckets.len() >= self.inner.config.max_buckets {
            self.inner.dropped_bucket_overflow.fetch_add(1, Ordering::Relaxed);
            return;
        }

        let window = match n.priority {
            Priority::Important => self.inner.config.important_window,
            _ => self.inner.config.normal_window,
        };
        let inner = self.inner.clone();
        let timer_key = key.clone();
        let timer = tokio::spawn(async move {
            tokio::time::sleep(window).await;
            Inner::flush(&inner, &timer_key);
        });
        buckets.insert(key, Bucket { events: vec![n], timer });
    }

    /// Flush every open bucket, then wait (bounded) for all in-flight renders.
    pub async fn shutdown(&self) {
        let keys: Vec<Key> = self.inner.buckets.lock().unwrap().keys().cloned().collect();
        for key in &keys {
            Inner::flush(&self.inner, key);
        }
        self.inner.renders.close();
        if tokio::time::timeout(self.inner.config.drain_timeout, self.inner.renders.wait())
            .await
            .is_err()
        {
            tracing::warn!("shutdown drain timed out; abandoning in-flight renders");
        }
    }
}

/// Replaceable events keep only the highest-seq value (spec §5.1): a stale
/// lower-seq arrival is dropped even if it arrives later in wall time.
fn apply_to_bucket(events: &mut Vec<InboundNotification>, n: InboundNotification) {
    if n.replaceable {
        let newest = events.iter().map(|e| e.seq).max().unwrap_or(0);
        if n.seq >= newest {
            events.clear();
            events.push(n);
        }
    } else {
        events.push(n);
    }
}

impl Inner {
    fn flush(inner: &Arc<Inner>, key: &Key) {
        let bucket = inner.buckets.lock().unwrap().remove(key);
        if let Some(bucket) = bucket {
            // Called either from the timer task itself (abort is then a no-op
            // that takes effect after this synchronous flush completes) or
            // from shutdown (kills a still-pending timer).
            bucket.timer.abort();
            let mut events = bucket.events;
            if events.is_empty() {
                return;
            }
            events.sort_by_key(|e| e.seq);
            let toast = toast::from_batch(&events);
            let sink = inner.sink.clone();
            inner.renders.spawn(async move { sink.render(toast).await });
        }
    }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd rust && cargo test -p notify-agent-core 2>&1 | tail -5`
Expected: `test result: ok. 29 passed; 0 failed`, zero warnings. (Paused-clock note: `advance()` fires timers; `settle()` yields so spawned tasks run; the hung-renderer test relies on tokio auto-advance to cover the 5s timeout instantly.)

- [ ] **Step 5: Commit**

```bash
git add rust/notify-agent-core
git commit -m "feat(rust): seq-ordered aggregator with bounded drain-safe shutdown"
```

---

### Task 5: Acknowledgement telemetry contract

**Files:**
- Create: `rust/notify-agent-core/src/ack.rs`
- Modify: `rust/notify-agent-core/src/lib.rs` (add `pub mod ack;`)

**Interfaces:**
- Produces: `ack::OBSERVED_BY_AGENT = "observed_by_agent"`, `ack::SUBMITTED_TO_WINDOWS = "submitted_to_windows"` (`&'static str` consts); `struct AckPayload { event_id: String, device_id: String, agent_received_at: DateTime<Utc>, toast_submitted_at: Option<DateTime<Utc>>, status: String }` (derives `Debug, Clone, Serialize`, camelCase, None omitted); `ack::serialize(ack: &AckPayload) -> Vec<u8>`; `#[async_trait] trait TelemetryPublisher: Send + Sync { async fn publish_ack(&self, ack: &AckPayload) -> anyhow::Result<()>; }`.

- [ ] **Step 1: Write the failing tests**

Create `rust/notify-agent-core/src/ack.rs` with only:

```rust
#[cfg(test)]
mod tests {
    use super::*;
    use chrono::{DateTime, Utc};

    fn ts(s: &str) -> DateTime<Utc> {
        s.parse().unwrap()
    }

    #[test]
    fn serializes_submitted_ack_in_design_doc_shape() {
        let ack = AckPayload {
            event_id: "evt-12345".into(),
            device_id: "d-456".into(),
            agent_received_at: ts("2026-07-15T08:30:00.190Z"),
            toast_submitted_at: Some(ts("2026-07-15T08:30:00.205Z")),
            status: SUBMITTED_TO_WINDOWS.into(),
        };
        let v: serde_json::Value = serde_json::from_slice(&serialize(&ack)).unwrap();
        assert_eq!(v["eventId"], "evt-12345");
        assert_eq!(v["deviceId"], "d-456");
        assert_eq!(v["agentReceivedAt"].as_str().unwrap().parse::<DateTime<Utc>>().unwrap(),
                   ts("2026-07-15T08:30:00.190Z"));
        assert_eq!(v["toastSubmittedAt"].as_str().unwrap().parse::<DateTime<Utc>>().unwrap(),
                   ts("2026-07-15T08:30:00.205Z"));
        assert_eq!(v["status"], "submitted_to_windows");
        assert_eq!(v.as_object().unwrap().len(), 5, "exactly the five contract fields");
    }

    #[test]
    fn observed_ack_omits_null_toast_submitted_at() {
        let ack = AckPayload {
            event_id: "evt-1".into(),
            device_id: "d-1".into(),
            agent_received_at: ts("2026-07-15T08:30:00.190Z"),
            toast_submitted_at: None,
            status: OBSERVED_BY_AGENT.into(),
        };
        let v: serde_json::Value = serde_json::from_slice(&serialize(&ack)).unwrap();
        assert_eq!(v["status"], "observed_by_agent");
        assert!(v.as_object().unwrap().get("toastSubmittedAt").is_none());
        assert_eq!(v.as_object().unwrap().len(), 4);
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd rust && cargo test -p notify-agent-core 2>&1 | tail -5`
Expected: compile FAILURE — `cannot find struct AckPayload`. (First add `pub mod ack;` to `lib.rs`.)

- [ ] **Step 3: Implement**

Prepend to `rust/notify-agent-core/src/ack.rs`:

```rust
use async_trait::async_trait;
use chrono::{DateTime, Utc};
use serde::Serialize;

/// Exact status strings from design §10. The agent never emits
/// "published" or "unobserved" — those are backend-side classifications.
pub const OBSERVED_BY_AGENT: &str = "observed_by_agent";
pub const SUBMITTED_TO_WINDOWS: &str = "submitted_to_windows";

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct AckPayload {
    pub event_id: String,
    pub device_id: String,
    pub agent_received_at: DateTime<Utc>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub toast_submitted_at: Option<DateTime<Utc>>,
    pub status: String,
}

#[async_trait]
pub trait TelemetryPublisher: Send + Sync {
    async fn publish_ack(&self, ack: &AckPayload) -> anyhow::Result<()>;
}

pub fn serialize(ack: &AckPayload) -> Vec<u8> {
    serde_json::to_vec(ack).expect("ack serialization is infallible")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd rust && cargo test -p notify-agent-core 2>&1 | tail -5`
Expected: `test result: ok. 31 passed; 0 failed`, zero warnings.

- [ ] **Step 5: Commit**

```bash
git add rust/notify-agent-core
git commit -m "feat(rust): acknowledgement payload contract and serializer"
```

---

### Task 6: Bounded-channel event pipeline

**Files:**
- Create: `rust/notify-agent-core/src/pipeline.rs`
- Modify: `rust/notify-agent-core/src/lib.rs` (add `pub mod pipeline;`)

**Interfaces:**
- Consumes: `parser::parse_event` (Task 1), `DedupCache` (Task 3), `Aggregator`/`AggregatorConfig`/`RenderSink` (Task 4), `ack::*` (Task 5), `ToastRenderer`/`ToastRequest` (Task 2).
- Produces: `struct ReceivedEvent { payload: Vec<u8>, received_at: DateTime<Utc>, seq: u64 }` (derives `Debug`); `struct PipelineConfig { queue_capacity: usize, workers: usize }` (`Default` = 500 / 2, derives `Clone`); `struct Pipeline` with `Pipeline::start(config, dedup: Arc<DedupCache>, aggregator: Aggregator, telemetry: Arc<dyn TelemetryPublisher>, device_id: String) -> Pipeline`, `try_enqueue(&self, evt: ReceivedEvent) -> bool`, `dropped_queue_full(&self) -> u64`, `async shutdown(self)` (close intake, drain workers to completion); `struct AckingRenderSink { renderer: Arc<dyn ToastRenderer>, telemetry: Arc<dyn TelemetryPublisher>, device_id: String }` implementing `RenderSink`; `pipeline::build_agent(pipeline_config, aggregator_config, dedup, renderer, telemetry, device_id) -> (Pipeline, Aggregator)`.

Worker behavior per event: parse (drop invalid, `tracing::debug`) → dedup (drop duplicate) → publish `observed_by_agent` ack (failure logged, processing continues) → `aggregator.add`. `AckingRenderSink::render`: `renderer.show(&toast)` → on success one `submitted_to_windows` ack per `toast.sources` entry carrying that source's own `received_at` and the shared submission timestamp; on show error log and skip acks.

- [ ] **Step 1: Write the failing tests**

Create `rust/notify-agent-core/src/pipeline.rs` with only:

```rust
#[cfg(test)]
mod tests {
    use super::*;
    use crate::ack::{AckPayload, TelemetryPublisher, OBSERVED_BY_AGENT, SUBMITTED_TO_WINDOWS};
    use crate::aggregator::AggregatorConfig;
    use crate::dedup::DedupCache;
    use crate::toast::{ToastRenderer, ToastRequest};
    use async_trait::async_trait;
    use chrono::{DateTime, Utc};
    use std::sync::{Arc, Mutex};
    use tokio::time::Duration;

    #[derive(Default)]
    struct RecordingTelemetry {
        acks: Mutex<Vec<AckPayload>>,
    }

    #[async_trait]
    impl TelemetryPublisher for RecordingTelemetry {
        async fn publish_ack(&self, ack: &AckPayload) -> anyhow::Result<()> {
            self.acks.lock().unwrap().push(ack.clone());
            Ok(())
        }
    }

    struct RecordingRenderer {
        shown: Mutex<Vec<ToastRequest>>,
        submit_at: DateTime<Utc>,
    }

    impl Default for RecordingRenderer {
        fn default() -> Self {
            Self {
                shown: Mutex::new(Vec::new()),
                submit_at: "2026-07-15T08:30:00.205Z".parse().unwrap(),
            }
        }
    }

    #[async_trait]
    impl ToastRenderer for RecordingRenderer {
        async fn show(&self, toast: &ToastRequest) -> anyhow::Result<DateTime<Utc>> {
            self.shown.lock().unwrap().push(toast.clone());
            Ok(self.submit_at)
        }
    }

    fn received_at() -> DateTime<Utc> {
        "2026-07-15T08:30:00.190Z".parse().unwrap()
    }

    fn critical_event(seq: u64, id: &str) -> ReceivedEvent {
        let payload = format!(
            r#"{{"eventId":"{id}","target":{{"userId":"u1"}},
                 "content":{{"title":"T","message":"M"}},
                 "classification":{{"priority":"critical","deduplicationKey":"{id}"}}}}"#
        );
        ReceivedEvent { payload: payload.into_bytes(), received_at: received_at(), seq }
    }

    fn harness(queue_capacity: usize, workers: usize)
        -> (Pipeline, crate::aggregator::Aggregator, Arc<RecordingTelemetry>, Arc<RecordingRenderer>)
    {
        let telemetry = Arc::new(RecordingTelemetry::default());
        let renderer = Arc::new(RecordingRenderer::default());
        let (pipeline, aggregator) = build_agent(
            PipelineConfig { queue_capacity, workers },
            AggregatorConfig::default(),
            Arc::new(DedupCache::new(100, Duration::from_secs(600))),
            renderer.clone(),
            telemetry.clone(),
            "d-456".to_string(),
        );
        (pipeline, aggregator, telemetry, renderer)
    }

    async fn wait_until(mut cond: impl FnMut() -> bool) {
        for _ in 0..500 {
            if cond() {
                return;
            }
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
        panic!("condition not reached within 5s");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn valid_critical_event_flows_to_renderer_with_both_acks() {
        let (pipeline, aggregator, telemetry, renderer) = harness(500, 2);
        assert!(pipeline.try_enqueue(critical_event(1, "evt-1")));
        wait_until(|| telemetry.acks.lock().unwrap().len() == 2).await;

        assert_eq!(renderer.shown.lock().unwrap().len(), 1);
        let acks = telemetry.acks.lock().unwrap().clone();
        let observed = acks.iter().find(|a| a.status == OBSERVED_BY_AGENT).unwrap();
        let submitted = acks.iter().find(|a| a.status == SUBMITTED_TO_WINDOWS).unwrap();
        assert_eq!(observed.event_id, "evt-1");
        assert_eq!(observed.device_id, "d-456");
        assert_eq!(observed.agent_received_at, received_at());
        assert_eq!(observed.toast_submitted_at, None);
        assert_eq!(submitted.toast_submitted_at, Some(renderer.submit_at));
        assert_eq!(submitted.agent_received_at, received_at());

        pipeline.shutdown().await;
        aggregator.shutdown().await;
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn duplicate_events_are_processed_once() {
        let (pipeline, aggregator, telemetry, renderer) = harness(500, 2);
        for seq in 1..=3 {
            pipeline.try_enqueue(critical_event(seq, "evt-dup"));
        }
        wait_until(|| telemetry.acks.lock().unwrap().len() >= 2).await;
        tokio::time::sleep(Duration::from_millis(100)).await; // grace: no further acks

        assert_eq!(renderer.shown.lock().unwrap().len(), 1);
        assert_eq!(telemetry.acks.lock().unwrap().len(), 2); // one observed + one submitted
        pipeline.shutdown().await;
        aggregator.shutdown().await;
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn invalid_payloads_are_dropped_silently() {
        let (pipeline, aggregator, telemetry, renderer) = harness(500, 2);
        pipeline.try_enqueue(ReceivedEvent {
            payload: b"garbage".to_vec(),
            received_at: received_at(),
            seq: 1,
        });
        pipeline.try_enqueue(critical_event(2, "evt-ok")); // proves the worker survived
        wait_until(|| telemetry.acks.lock().unwrap().len() == 2).await;

        assert_eq!(renderer.shown.lock().unwrap().len(), 1);
        assert!(telemetry.acks.lock().unwrap().iter().all(|a| a.event_id == "evt-ok"));
        pipeline.shutdown().await;
        aggregator.shutdown().await;
    }

    #[tokio::test]
    async fn try_enqueue_reports_drop_when_queue_full() {
        // workers: 0 → nothing drains the channel.
        let (pipeline, _aggregator, _telemetry, _renderer) = harness(2, 0);
        assert!(pipeline.try_enqueue(critical_event(1, "e1")));
        assert!(pipeline.try_enqueue(critical_event(2, "e2")));
        assert!(!pipeline.try_enqueue(critical_event(3, "e3")));
        assert_eq!(pipeline.dropped_queue_full(), 1);
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd rust && cargo test -p notify-agent-core 2>&1 | tail -5`
Expected: compile FAILURE — `cannot find function build_agent`. (First add `pub mod pipeline;` to `lib.rs`.)

- [ ] **Step 3: Implement**

Prepend to `rust/notify-agent-core/src/pipeline.rs`:

```rust
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;

use async_trait::async_trait;
use chrono::{DateTime, Utc};
use tokio::sync::mpsc;
use tokio::task::JoinHandle;

use crate::ack::{AckPayload, TelemetryPublisher, OBSERVED_BY_AGENT, SUBMITTED_TO_WINDOWS};
use crate::aggregator::{Aggregator, AggregatorConfig, RenderSink};
use crate::dedup::DedupCache;
use crate::parser;
use crate::toast::{ToastRenderer, ToastRequest};

#[derive(Debug)]
pub struct ReceivedEvent {
    pub payload: Vec<u8>,
    pub received_at: DateTime<Utc>,
    /// Monotonic arrival stamp from the (single-threaded) subscribe loop.
    pub seq: u64,
}

#[derive(Clone)]
pub struct PipelineConfig {
    pub queue_capacity: usize, // design §9 baseline: 500
    pub workers: usize,        // design §9 baseline: 2
}

impl Default for PipelineConfig {
    fn default() -> Self {
        Self { queue_capacity: 500, workers: 2 }
    }
}

/// Bounded intake queue with a fixed worker pool (design §9). Overload drops
/// at the queue boundary via try_send — memory stays bounded, delivery stays
/// best-effort, and every drop is counted.
pub struct Pipeline {
    tx: mpsc::Sender<ReceivedEvent>,
    dropped_queue_full: Arc<AtomicU64>,
    workers: Vec<JoinHandle<()>>,
}

impl Pipeline {
    pub fn start(
        config: PipelineConfig,
        dedup: Arc<DedupCache>,
        aggregator: Aggregator,
        telemetry: Arc<dyn TelemetryPublisher>,
        device_id: String,
    ) -> Self {
        let (tx, rx) = mpsc::channel::<ReceivedEvent>(config.queue_capacity);
        let rx = Arc::new(tokio::sync::Mutex::new(rx));
        let workers = (0..config.workers)
            .map(|_| {
                let rx = rx.clone();
                let dedup = dedup.clone();
                let aggregator = aggregator.clone();
                let telemetry = telemetry.clone();
                let device_id = device_id.clone();
                tokio::spawn(async move {
                    loop {
                        // Lock only to receive, never while processing, so the
                        // two workers process concurrently.
                        let evt = { rx.lock().await.recv().await };
                        let Some(evt) = evt else { break }; // closed + drained
                        process(evt, &dedup, &aggregator, telemetry.as_ref(), &device_id).await;
                    }
                })
            })
            .collect();
        Self { tx, dropped_queue_full: Arc::new(AtomicU64::new(0)), workers }
    }

    pub fn try_enqueue(&self, evt: ReceivedEvent) -> bool {
        match self.tx.try_send(evt) {
            Ok(()) => true,
            Err(_) => {
                self.dropped_queue_full.fetch_add(1, Ordering::Relaxed);
                false
            }
        }
    }

    pub fn dropped_queue_full(&self) -> u64 {
        self.dropped_queue_full.load(Ordering::Relaxed)
    }

    /// Close the intake and let workers drain every queued event (spec §5.2:
    /// bounded work — at most queue_capacity events — so no timeout needed here;
    /// the render drain timeout lives in Aggregator::shutdown).
    pub async fn shutdown(self) {
        drop(self.tx);
        for w in self.workers {
            let _ = w.await;
        }
    }
}

async fn process(
    evt: ReceivedEvent,
    dedup: &DedupCache,
    aggregator: &Aggregator,
    telemetry: &dyn TelemetryPublisher,
    device_id: &str,
) {
    let n = match parser::parse_event(&evt.payload, evt.received_at, evt.seq) {
        Ok(n) => n,
        Err(e) => {
            tracing::debug!(error = %e, "dropping invalid event");
            return;
        }
    };
    if !dedup.try_add(&n.deduplication_key) {
        return;
    }
    let ack = AckPayload {
        event_id: n.event_id.clone(),
        device_id: device_id.to_string(),
        agent_received_at: n.received_at,
        toast_submitted_at: None,
        status: OBSERVED_BY_AGENT.into(),
    };
    if let Err(e) = telemetry.publish_ack(&ack).await {
        tracing::warn!(error = %e, "observed ack publish failed");
    }
    aggregator.add(n);
}

/// Renders a toast then acks every source event as submitted_to_windows,
/// each with its own received_at and the shared submission timestamp.
pub struct AckingRenderSink {
    pub renderer: Arc<dyn ToastRenderer>,
    pub telemetry: Arc<dyn TelemetryPublisher>,
    pub device_id: String,
}

#[async_trait]
impl RenderSink for AckingRenderSink {
    async fn render(&self, toast: ToastRequest) {
        let submitted_at = match self.renderer.show(&toast).await {
            Ok(t) => t,
            Err(e) => {
                tracing::warn!(error = %e, "toast render failed");
                return;
            }
        };
        for source in &toast.sources {
            let ack = AckPayload {
                event_id: source.event_id.clone(),
                device_id: self.device_id.clone(),
                agent_received_at: source.received_at,
                toast_submitted_at: Some(submitted_at),
                status: SUBMITTED_TO_WINDOWS.into(),
            };
            if let Err(e) = self.telemetry.publish_ack(&ack).await {
                tracing::warn!(error = %e, event_id = %source.event_id, "submitted ack publish failed");
            }
        }
    }
}

/// Composition helper: wires renderer + telemetry into the aggregator's render
/// path and returns both halves. Shutdown order: pipeline first (drain intake
/// into the aggregator), then aggregator (flush + bounded render drain).
pub fn build_agent(
    pipeline_config: PipelineConfig,
    aggregator_config: AggregatorConfig,
    dedup: Arc<DedupCache>,
    renderer: Arc<dyn ToastRenderer>,
    telemetry: Arc<dyn TelemetryPublisher>,
    device_id: String,
) -> (Pipeline, Aggregator) {
    let sink = Arc::new(AckingRenderSink {
        renderer,
        telemetry: telemetry.clone(),
        device_id: device_id.clone(),
    });
    let aggregator = Aggregator::new(aggregator_config, sink);
    let pipeline = Pipeline::start(pipeline_config, dedup, aggregator.clone(), telemetry, device_id);
    (pipeline, aggregator)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd rust && cargo test -p notify-agent-core 2>&1 | tail -5`
Expected: `test result: ok. 35 passed; 0 failed`, zero warnings.

- [ ] **Step 5: Commit**

```bash
git add rust/notify-agent-core
git commit -m "feat(rust): bounded-channel pipeline (500 cap, 2 workers) with ack wiring"
```

---

### Task 7: Identity providers, NATS host, and integration test

**Files:**
- Create: `rust/notify-agent-core/src/identity.rs`, `rust/notify-agent-core/src/host.rs`
- Create: `rust/notify-agent-core/tests/nats_integration.rs`
- Modify: `rust/notify-agent-core/src/lib.rs` (add `pub mod identity; pub mod host;`)

**Interfaces:**
- Consumes: everything from Tasks 1–6; `async-nats`, `reqwest`, `base64`, `gethostname`, `futures::StreamExt`, `tokio_util::sync::CancellationToken`.
- Produces: `struct AgentIdentity { user_id: String, device_id: String }` (derives `Debug, Clone`); `#[async_trait] trait IdentityProvider: Send + Sync { async fn identity(&self) -> anyhow::Result<AgentIdentity>; }`; `struct EnvIdentity` (reads `NOTIFY_USER_ID` required / `NOTIFY_DEVICE_ID` optional, default `d-{hostname lowercase}`); `struct DeviceCodeIdentity { client_id: String, tenant: String, device_id: String, renderer: Arc<dyn ToastRenderer> }` with `identity()` running the device-code flow; helpers `identity::parse_token_poll(body: &serde_json::Value) -> TokenPoll` (`enum TokenPoll { Pending, Success { id_token: String }, Failed(String) }`, derives `Debug, PartialEq`) and `identity::oid_from_id_token(id_token: &str) -> anyhow::Result<String>`; `struct AgentConfig { nats_url: String, subject_template: String, ack_subject: String }` with `AgentConfig::from_env()` (defaults `nats://127.0.0.1:4222` / `notify.user.{0}.desktop` / `notify.ack.desktop`); `struct NatsTelemetry { client: async_nats::Client, subject: String }` implementing `TelemetryPublisher`; `struct AgentHost` with `AgentHost::start(config: AgentConfig, identity: Arc<dyn IdentityProvider>, renderer: Arc<dyn ToastRenderer>) -> anyhow::Result<AgentHost>`, `subject(&self) -> &str`, `async shutdown(self) -> anyhow::Result<()>`.

- [ ] **Step 1: Write the failing identity unit tests**

Create `rust/notify-agent-core/src/identity.rs` with only:

```rust
#[cfg(test)]
mod tests {
    use super::*;
    use base64::Engine;

    fn fake_jwt(claims: &serde_json::Value) -> String {
        let b64 = |v: &serde_json::Value| {
            base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(v.to_string())
        };
        format!("{}.{}.sig", b64(&serde_json::json!({"alg":"none"})), b64(claims))
    }

    #[test]
    fn extracts_oid_from_id_token() {
        let token = fake_jwt(&serde_json::json!({"oid": "7f92a845-0000-0000-0000-000000000001", "name": "x"}));
        assert_eq!(oid_from_id_token(&token).unwrap(), "7f92a845-0000-0000-0000-000000000001");
    }

    #[test]
    fn rejects_token_without_oid() {
        let token = fake_jwt(&serde_json::json!({"name": "x"}));
        assert!(oid_from_id_token(&token).is_err());
        assert!(oid_from_id_token("not-a-jwt").is_err());
    }

    #[test]
    fn classifies_token_poll_responses() {
        assert_eq!(
            parse_token_poll(&serde_json::json!({"error": "authorization_pending"})),
            TokenPoll::Pending
        );
        assert_eq!(
            parse_token_poll(&serde_json::json!({"id_token": "abc", "access_token": "def"})),
            TokenPoll::Success { id_token: "abc".into() }
        );
        assert_eq!(
            parse_token_poll(&serde_json::json!({"error": "expired_token", "error_description": "gone"})),
            TokenPoll::Failed("expired_token: gone".into())
        );
        assert_eq!(
            parse_token_poll(&serde_json::json!({})),
            TokenPoll::Failed("malformed token response".into())
        );
    }

    #[tokio::test]
    async fn env_identity_requires_user_id() {
        // Serialized via env-var uniqueness: this test owns these two vars.
        std::env::remove_var("NOTIFY_USER_ID");
        std::env::set_var("NOTIFY_DEVICE_ID", "d-test");
        assert!(EnvIdentity.identity().await.is_err());
        std::env::set_var("NOTIFY_USER_ID", "u_demo");
        let id = EnvIdentity.identity().await.unwrap();
        assert_eq!(id.user_id, "u_demo");
        assert_eq!(id.device_id, "d-test");
        std::env::remove_var("NOTIFY_USER_ID");
        std::env::remove_var("NOTIFY_DEVICE_ID");
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd rust && cargo test -p notify-agent-core 2>&1 | tail -5`
Expected: compile FAILURE — `cannot find function oid_from_id_token`. (First add `pub mod identity;` to `lib.rs`.)

- [ ] **Step 3: Implement identity**

Prepend to `rust/notify-agent-core/src/identity.rs`:

```rust
use std::sync::Arc;

use anyhow::{anyhow, Context};
use async_trait::async_trait;
use base64::Engine;

use crate::toast::{ToastRenderer, ToastRequest};

#[derive(Debug, Clone)]
pub struct AgentIdentity {
    pub user_id: String,
    pub device_id: String,
}

/// Resolves the immutable application user ID and device ID (design §8).
/// The OS account name is never used as identity.
#[async_trait]
pub trait IdentityProvider: Send + Sync {
    async fn identity(&self) -> anyhow::Result<AgentIdentity>;
}

/// Development identity from environment variables (parity with C#).
pub struct EnvIdentity;

pub fn default_device_id() -> String {
    format!("d-{}", gethostname::gethostname().to_string_lossy().to_lowercase())
}

#[async_trait]
impl IdentityProvider for EnvIdentity {
    async fn identity(&self) -> anyhow::Result<AgentIdentity> {
        let user_id = std::env::var("NOTIFY_USER_ID")
            .ok()
            .filter(|s| !s.trim().is_empty())
            .context("NOTIFY_USER_ID is not set")?;
        let device_id = std::env::var("NOTIFY_DEVICE_ID")
            .ok()
            .filter(|s| !s.trim().is_empty())
            .unwrap_or_else(default_device_id);
        Ok(AgentIdentity { user_id, device_id })
    }
}

#[derive(Debug, PartialEq)]
pub enum TokenPoll {
    Pending,
    Success { id_token: String },
    Failed(String),
}

pub fn parse_token_poll(body: &serde_json::Value) -> TokenPoll {
    if let Some(id_token) = body.get("id_token").and_then(|v| v.as_str()) {
        return TokenPoll::Success { id_token: id_token.to_string() };
    }
    match body.get("error").and_then(|v| v.as_str()) {
        Some("authorization_pending") => TokenPoll::Pending,
        Some(err) => {
            let desc = body.get("error_description").and_then(|v| v.as_str()).unwrap_or("");
            TokenPoll::Failed(format!("{err}: {desc}"))
        }
        None => TokenPoll::Failed("malformed token response".to_string()),
    }
}

/// Extract the Entra object id from an id_token. POC trade-off (spec §7):
/// no signature validation — the token arrives directly from Entra over TLS
/// and is used only to derive the local user id.
pub fn oid_from_id_token(id_token: &str) -> anyhow::Result<String> {
    let payload_b64 = id_token.split('.').nth(1).ok_or_else(|| anyhow!("not a JWT"))?;
    let bytes = base64::engine::general_purpose::URL_SAFE_NO_PAD
        .decode(payload_b64)
        .context("id_token payload is not base64url")?;
    let claims: serde_json::Value = serde_json::from_slice(&bytes).context("id_token payload is not JSON")?;
    claims
        .get("oid")
        .and_then(|v| v.as_str())
        .map(str::to_string)
        .ok_or_else(|| anyhow!("id_token has no oid claim"))
}

/// OIDC device-code sign-in (spec §7): the WAM broker replacement.
pub struct DeviceCodeIdentity {
    pub client_id: String,
    pub tenant: String,
    pub device_id: String,
    pub renderer: Arc<dyn ToastRenderer>,
}

#[derive(serde::Deserialize)]
struct DeviceCodeResponse {
    device_code: String,
    user_code: String,
    verification_uri: String,
    interval: u64,
    expires_in: u64,
}

#[async_trait]
impl IdentityProvider for DeviceCodeIdentity {
    async fn identity(&self) -> anyhow::Result<AgentIdentity> {
        let http = reqwest::Client::new();
        let base = format!("https://login.microsoftonline.com/{}/oauth2/v2.0", self.tenant);

        let dc: DeviceCodeResponse = http
            .post(format!("{base}/devicecode"))
            .form(&[("client_id", self.client_id.as_str()), ("scope", "openid profile")])
            .send()
            .await?
            .error_for_status()?
            .json()
            .await?;

        let prompt = format!("Go to {} and enter code {}", dc.verification_uri, dc.user_code);
        println!("[SIGN-IN] {prompt}");
        // Best-effort toast with the code, so the Windows head surfaces it too.
        let _ = self
            .renderer
            .show(&ToastRequest {
                title: "Sign in required".into(),
                message: prompt,
                attribution: Some("Desktop Notification Agent".into()),
                action_label: Some("Open sign-in page".into()),
                action_url: Some(dc.verification_uri.clone()),
                sources: Vec::new(),
            })
            .await;

        let deadline = tokio::time::Instant::now() + std::time::Duration::from_secs(dc.expires_in);
        loop {
            if tokio::time::Instant::now() >= deadline {
                anyhow::bail!("device-code sign-in timed out");
            }
            tokio::time::sleep(std::time::Duration::from_secs(dc.interval.max(1))).await;
            let body: serde_json::Value = http
                .post(format!("{base}/token"))
                .form(&[
                    ("grant_type", "urn:ietf:params:oauth:grant-type:device_code"),
                    ("client_id", self.client_id.as_str()),
                    ("device_code", dc.device_code.as_str()),
                ])
                .send()
                .await?
                .json()
                .await?;
            match parse_token_poll(&body) {
                TokenPoll::Pending => continue,
                TokenPoll::Success { id_token } => {
                    let oid = oid_from_id_token(&id_token)?;
                    return Ok(AgentIdentity {
                        user_id: format!("u_{oid}"),
                        device_id: self.device_id.clone(),
                    });
                }
                TokenPoll::Failed(reason) => anyhow::bail!("device-code sign-in failed: {reason}"),
            }
        }
    }
}
```

Run: `cd rust && cargo test -p notify-agent-core 2>&1 | tail -5`
Expected: `test result: ok. 39 passed; 0 failed` (4 new), zero warnings.

- [ ] **Step 4: Implement the host**

Create `rust/notify-agent-core/src/host.rs` (add `pub mod host;` to `lib.rs`):

```rust
use std::sync::Arc;

use async_trait::async_trait;
use chrono::Utc;
use futures::StreamExt;
use tokio::task::JoinHandle;
use tokio_util::sync::CancellationToken;

use crate::ack::{self, AckPayload, TelemetryPublisher};
use crate::aggregator::{Aggregator, AggregatorConfig};
use crate::dedup::DedupCache;
use crate::identity::IdentityProvider;
use crate::pipeline::{build_agent, Pipeline, PipelineConfig, ReceivedEvent};
use crate::toast::ToastRenderer;

pub struct AgentConfig {
    pub nats_url: String,
    pub subject_template: String, // literal "{0}" placeholder — env parity with C#
    pub ack_subject: String,
}

impl AgentConfig {
    pub fn from_env() -> Self {
        let var = |k: &str, d: &str| std::env::var(k).ok().filter(|s| !s.is_empty()).unwrap_or_else(|| d.into());
        Self {
            nats_url: var("NOTIFY_NATS_URL", "nats://127.0.0.1:4222"),
            subject_template: var("NOTIFY_SUBJECT_TEMPLATE", "notify.user.{0}.desktop"), // design §4
            ack_subject: var("NOTIFY_ACK_SUBJECT", "notify.ack.desktop"),
        }
    }
}

pub struct NatsTelemetry {
    pub client: async_nats::Client,
    pub subject: String,
}

#[async_trait]
impl TelemetryPublisher for NatsTelemetry {
    async fn publish_ack(&self, ack_payload: &AckPayload) -> anyhow::Result<()> {
        self.client
            .publish(self.subject.clone(), ack::serialize(ack_payload).into())
            .await?;
        Ok(())
    }
}

/// Composition root: identity → NATS connection → pipeline → live subscription.
/// Plain Core NATS subscription: reconnects resume with future events only
/// (design §6.2). Shutdown order (spec §5.2): cancel subscribe loop → drain
/// pipeline → drain aggregator (bounded) → flush NATS last.
pub struct AgentHost {
    subject: String,
    client: async_nats::Client,
    pipeline: Pipeline,
    aggregator: Aggregator,
    cancel: CancellationToken,
    subscriber: JoinHandle<()>,
}

impl AgentHost {
    pub async fn start(
        config: AgentConfig,
        identity: Arc<dyn IdentityProvider>,
        renderer: Arc<dyn ToastRenderer>,
    ) -> anyhow::Result<AgentHost> {
        let id = identity.identity().await?;
        let client = async_nats::connect(&config.nats_url).await?;
        let telemetry: Arc<dyn TelemetryPublisher> = Arc::new(NatsTelemetry {
            client: client.clone(),
            subject: config.ack_subject.clone(),
        });
        let dedup = Arc::new(DedupCache::new(10_000, std::time::Duration::from_secs(600)));
        let (pipeline_half, aggregator) = build_agent(
            PipelineConfig::default(),
            AggregatorConfig::default(),
            dedup,
            renderer,
            telemetry,
            id.device_id.clone(),
        );

        let subject = config.subject_template.replace("{0}", &id.user_id);
        let mut subscription = client.subscribe(subject.clone()).await?;
        let cancel = CancellationToken::new();

        let loop_cancel = cancel.clone();
        let intake = pipeline_half.intake_handle();
        let subscriber = tokio::spawn(async move {
            let mut seq: u64 = 0;
            loop {
                tokio::select! {
                    _ = loop_cancel.cancelled() => break,
                    msg = subscription.next() => {
                        match msg {
                            Some(msg) if !msg.payload.is_empty() => {
                                seq += 1;
                                intake.try_enqueue(ReceivedEvent {
                                    payload: msg.payload.to_vec(),
                                    received_at: Utc::now(),
                                    seq,
                                });
                            }
                            Some(_) => {} // empty payload: ignore
                            None => {
                                // Spec §5.4: a dead subscription must not leave
                                // a silent zombie agent.
                                tracing::error!("NATS subscription ended unexpectedly; exiting");
                                std::process::exit(1);
                            }
                        }
                    }
                }
            }
        });

        Ok(AgentHost { subject, client, pipeline: pipeline_half, aggregator, cancel, subscriber })
    }

    pub fn subject(&self) -> &str {
        &self.subject
    }

    pub async fn shutdown(self) -> anyhow::Result<()> {
        self.cancel.cancel();
        let _ = self.subscriber.await;
        self.pipeline.shutdown().await;
        self.aggregator.shutdown().await;
        self.client.flush().await?; // push any buffered acks before drop
        Ok(())
    }
}
```

This requires one addition to `rust/notify-agent-core/src/pipeline.rs` — a cheap cloneable intake handle so the subscribe task can enqueue without owning the `Pipeline`:

```rust
/// Cloneable enqueue-only handle for producer tasks.
#[derive(Clone)]
pub struct IntakeHandle {
    tx: mpsc::Sender<ReceivedEvent>,
    dropped_queue_full: Arc<AtomicU64>,
}

impl IntakeHandle {
    pub fn try_enqueue(&self, evt: ReceivedEvent) -> bool {
        match self.tx.try_send(evt) {
            Ok(()) => true,
            Err(_) => {
                self.dropped_queue_full.fetch_add(1, Ordering::Relaxed);
                false
            }
        }
    }
}

impl Pipeline {
    pub fn intake_handle(&self) -> IntakeHandle {
        IntakeHandle { tx: self.tx.clone(), dropped_queue_full: self.dropped_queue_full.clone() }
    }
}
```

(Note: `Pipeline::shutdown` drops the `Pipeline`'s own `tx` AND any `IntakeHandle` clones must be dropped for the channel to close — the host guarantees this by awaiting the subscriber task, which owns the only clone, before calling `pipeline.shutdown()`.)

Run: `cd rust && cargo build -p notify-agent-core 2>&1 | tail -3`
Expected: clean build, zero warnings. All 39 unit tests still pass (`cargo test -p notify-agent-core --lib`).

- [ ] **Step 5: Write the integration test**

Create `rust/notify-agent-core/tests/nats_integration.rs`:

```rust
//! End-to-end against a real NATS server on localhost:4222 (provided by a
//! pre-existing container on this machine — do NOT manage that container).
//! The test no-ops politely when the port is closed, same as the C# suite.

use std::sync::{Arc, Mutex};
use std::time::Duration;

use async_trait::async_trait;
use chrono::{DateTime, Utc};
use futures::StreamExt;
use notify_agent_core::host::{AgentConfig, AgentHost};
use notify_agent_core::identity::{AgentIdentity, IdentityProvider};
use notify_agent_core::toast::{ToastRenderer, ToastRequest};

fn nats_available() -> bool {
    std::net::TcpStream::connect_timeout(&"127.0.0.1:4222".parse().unwrap(), Duration::from_secs(1)).is_ok()
}

struct StubIdentity;

#[async_trait]
impl IdentityProvider for StubIdentity {
    async fn identity(&self) -> anyhow::Result<AgentIdentity> {
        Ok(AgentIdentity { user_id: "itest-rust".into(), device_id: "d-itest-rust".into() })
    }
}

#[derive(Default)]
struct RecordingRenderer {
    shown: Mutex<Vec<ToastRequest>>,
}

#[async_trait]
impl ToastRenderer for RecordingRenderer {
    async fn show(&self, toast: &ToastRequest) -> anyhow::Result<DateTime<Utc>> {
        self.shown.lock().unwrap().push(toast.clone());
        Ok(Utc::now())
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn published_event_is_rendered_and_acked_end_to_end() {
    if !nats_available() {
        eprintln!("SKIPPED: no NATS server on localhost:4222");
        return;
    }

    let renderer = Arc::new(RecordingRenderer::default());
    let host = AgentHost::start(
        AgentConfig {
            nats_url: "nats://127.0.0.1:4222".into(),
            subject_template: "notify.user.{0}.desktop".into(),
            ack_subject: "notify.ack.desktop".into(),
        },
        Arc::new(StubIdentity),
        renderer.clone(),
    )
    .await
    .expect("host start");
    assert_eq!(host.subject(), "notify.user.itest-rust.desktop");

    let probe = async_nats::connect("nats://127.0.0.1:4222").await.unwrap();
    let mut acks = probe.subscribe("notify.ack.desktop").await.unwrap();
    tokio::time::sleep(Duration::from_millis(500)).await; // let subscriptions settle (no replay in Core NATS)

    let event_id = format!("evt-{}", uuid_like());
    let payload = format!(
        r#"{{"eventId":"{event_id}","target":{{"userId":"itest-rust"}},
             "content":{{"title":"Integration","message":"Hello"}},
             "classification":{{"priority":"critical"}}}}"#
    );
    probe.publish("notify.user.itest-rust.desktop", payload.into_bytes().into()).await.unwrap();
    probe.flush().await.unwrap();

    let mut received: Vec<String> = Vec::new();
    let deadline = tokio::time::Instant::now() + Duration::from_secs(10);
    while received.len() < 2 && tokio::time::Instant::now() < deadline {
        match tokio::time::timeout_at(deadline, acks.next()).await {
            Ok(Some(msg)) => received.push(String::from_utf8_lossy(&msg.payload).to_string()),
            _ => break,
        }
    }

    assert_eq!(received.len(), 2, "expected 2 acks within 10s, got: {received:?}");
    assert!(received.iter().any(|a| a.contains("observed_by_agent") && a.contains(&event_id)));
    assert!(received.iter().any(|a| a.contains("submitted_to_windows") && a.contains(&event_id)));
    assert!(received.iter().all(|a| a.contains("d-itest-rust")));
    assert_eq!(renderer.shown.lock().unwrap().len(), 1);

    host.shutdown().await.expect("clean shutdown");
}

/// Unique-enough id without a uuid dependency.
fn uuid_like() -> String {
    format!("{:x}", std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap().as_nanos())
}
```

- [ ] **Step 6: Run the full suite (NATS live) and verify the skip path reasoning**

```bash
cd rust && cargo test -p notify-agent-core 2>&1 | tail -6
```
Expected: 39 unit tests + 1 integration test, all passing, integration test genuinely exercising NATS (runtime ~1s, no `SKIPPED` line in output). Do NOT stop the NATS container to drill the skip path — it is not ours; the skip branch is a 3-line TCP check verified by inspection.

- [ ] **Step 7: Commit**

```bash
git add rust/notify-agent-core
git commit -m "feat(rust): identity providers (env + device-code OIDC), NATS AgentHost, e2e integration test"
```

---

### Task 8: Console head and cross-language e2e smoke

**Files:**
- Create: `rust/notify-agent-console/Cargo.toml`, `rust/notify-agent-console/src/main.rs`
- Modify: `rust/Cargo.toml` (add member `"notify-agent-console"`)

**Interfaces:**
- Consumes: `AgentHost::start`, `AgentConfig::from_env`, `EnvIdentity`, `DeviceCodeIdentity`, `default_device_id`, `ToastRenderer`/`ToastRequest` (Task 7/2).
- Produces: runnable `notify-agent-console` binary; no new library APIs.

- [ ] **Step 1: Create the crate**

Create `rust/notify-agent-console/Cargo.toml`:

```toml
[package]
name = "notify-agent-console"
version = "0.1.0"
edition = "2021"

[dependencies]
anyhow = "1"
async-trait = "0.1"
chrono = "0.4"
notify-agent-core = { path = "../notify-agent-core" }
tokio = { version = "1", features = ["full"] }
tracing-subscriber = { version = "0.3", features = ["env-filter"] }
```

Add `"notify-agent-console"` to `members` in `rust/Cargo.toml`.

- [ ] **Step 2: Implement the console head**

Create `rust/notify-agent-console/src/main.rs`:

```rust
use std::sync::Arc;

use async_trait::async_trait;
use chrono::{DateTime, Utc};
use notify_agent_core::host::{AgentConfig, AgentHost};
use notify_agent_core::identity::{default_device_id, DeviceCodeIdentity, EnvIdentity, IdentityProvider};
use notify_agent_core::toast::{ToastRenderer, ToastRequest};

/// Dev stand-in for the Windows renderer: prints "toasts" to stdout
/// (format mirrors the C# ConsoleToastRenderer).
struct ConsoleToastRenderer;

#[async_trait]
impl ToastRenderer for ConsoleToastRenderer {
    async fn show(&self, toast: &ToastRequest) -> anyhow::Result<DateTime<Utc>> {
        println!("[TOAST] {}", toast.title);
        println!("        {}", toast.message);
        if let Some(attribution) = &toast.attribution {
            println!("        — {attribution}");
        }
        if let (Some(label), Some(url)) = (&toast.action_label, &toast.action_url) {
            println!("        [{label}] -> {url}");
        }
        Ok(Utc::now())
    }
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .init();

    let config = AgentConfig::from_env();
    let nats_url = config.nats_url.clone();
    let renderer: Arc<dyn ToastRenderer> = Arc::new(ConsoleToastRenderer);

    let identity: Arc<dyn IdentityProvider> = match std::env::var("NOTIFY_AAD_CLIENT_ID") {
        Ok(client_id) if !client_id.trim().is_empty() => Arc::new(DeviceCodeIdentity {
            client_id,
            tenant: std::env::var("NOTIFY_AAD_TENANT_ID").unwrap_or_else(|_| "organizations".into()),
            device_id: std::env::var("NOTIFY_DEVICE_ID").ok().filter(|s| !s.is_empty()).unwrap_or_else(default_device_id),
            renderer: renderer.clone(),
        }),
        _ => Arc::new(EnvIdentity),
    };

    let host = AgentHost::start(config, identity, renderer).await?;
    println!("Agent subscribed to {} on {}. Ctrl+C to exit.", host.subject(), nats_url);

    tokio::signal::ctrl_c().await?;
    println!("Shutting down.");
    host.shutdown().await?;
    Ok(())
}
```

- [ ] **Step 3: Build everything**

Run: `cd rust && cargo build 2>&1 | tail -3`
Expected: clean build, zero warnings.

- [ ] **Step 4: Cross-language e2e smoke (Rust agent + C# TestPublisher)**

```bash
cd /home/cjamhe01385/os-notification/.worktrees/rust-agent/rust
SMOKELOG=$(mktemp /tmp/rust-agent-smoke.XXXX.log)
NOTIFY_USER_ID=u_rustdemo cargo run -p notify-agent-console > "$SMOKELOG" 2>&1 &
AGENT_PID=$!
sleep 3
export PATH="$HOME/.dotnet:$PATH"
# TestPublisher lives in the MAIN checkout (built there by the C# plan):
dotnet run --project /home/cjamhe01385/os-notification/tools/TestPublisher -- u_rustdemo "Invoice ready" "Cross-language smoke" critical
kill -INT $AGENT_PID; sleep 2
cat "$SMOKELOG"
```
Expected in publisher output: one `[PUB] evt-...` line, then two `[ACK]` lines — one containing `"status":"observed_by_agent"` and one `"status":"submitted_to_windows"`, both containing the Rust device id. Expected in `$SMOKELOG`: `Agent subscribed to notify.user.u_rustdemo.desktop ...`, then `[TOAST] Invoice ready` / `Cross-language smoke`, then `Shutting down.` (the SIGINT graceful path — the C# smoke never exercised this).

Also verify batching: rerun the agent, then `dotnet run --project /home/cjamhe01385/os-notification/tools/TestPublisher -- u_rustdemo "Job" "step done" normal 3` → after ~10s the agent log shows a single `[TOAST] 3 notifications — billing.invoice.ready`. Kill the agent (`kill -INT`) after.

- [ ] **Step 5: Commit**

```bash
git add rust/
git commit -m "feat(rust): console dev head; cross-language e2e smoke vs C# TestPublisher verified"
```

---

### Task 9: Windows head and Linux cross-compilation

**Files:**
- Create: `rust/notify-agent-windows/Cargo.toml`, `rust/notify-agent-windows/src/main.rs`
- Create: `rust/.cargo/config.toml`
- Modify: `rust/Cargo.toml` (add member `"notify-agent-windows"`)

**Interfaces:**
- Consumes: `AgentHost::start`, `AgentConfig::from_env`, identity providers (Task 7), `ToastRenderer`/`ToastRequest` (Task 2).
- Produces: `notify-agent-windows.exe` for Windows 11. No downstream consumers.

> **Platform note:** the crate must build as a stub on Linux (`cargo build` in the workspace stays green) and cross-compile fully via `--target x86_64-pc-windows-gnu`. Run-verification happens on a Windows 11 machine (Step 6 checklist). If the `windows`/`winreg` crate versions below have moved, use current ones and adapt names minimally.

- [ ] **Step 1: Create the crate**

Create `rust/notify-agent-windows/Cargo.toml`:

```toml
[package]
name = "notify-agent-windows"
version = "0.1.0"
edition = "2021"

[dependencies]
anyhow = "1"
async-trait = "0.1"
chrono = "0.4"
notify-agent-core = { path = "../notify-agent-core" }
tokio = { version = "1", features = ["full"] }
tracing = "0.1"
tracing-subscriber = { version = "0.3", features = ["env-filter"] }

[target.'cfg(windows)'.dependencies]
windows = { version = "0.58", features = [
    "Data_Xml_Dom",
    "Foundation",
    "UI_Notifications",
    "Win32_Foundation",
    "Win32_System_Threading",
    "Win32_UI_Shell",
    "Win32_UI_WindowsAndMessaging",
] }
winreg = "0.52"
```

Add `"notify-agent-windows"` to `members` in `rust/Cargo.toml`.

- [ ] **Step 2: Implement (cfg-gated)**

Create `rust/notify-agent-windows/src/main.rs`:

```rust
#[cfg(not(windows))]
fn main() {
    eprintln!("notify-agent-windows only runs on Windows. Build with --target x86_64-pc-windows-gnu.");
    std::process::exit(2);
}

#[cfg(windows)]
mod win {
    use std::sync::Arc;

    use async_trait::async_trait;
    use chrono::{DateTime, Utc};
    use notify_agent_core::host::{AgentConfig, AgentHost};
    use notify_agent_core::identity::{DeviceCodeIdentity, EnvIdentity, IdentityProvider};
    use notify_agent_core::toast::{ToastRenderer, ToastRequest};
    use windows::core::{HSTRING, w};
    use windows::Data::Xml::Dom::XmlDocument;
    use windows::Foundation::TypedEventHandler;
    use windows::UI::Notifications::{
        ToastActivatedEventArgs, ToastNotification, ToastNotificationManager,
    };
    use windows::Win32::Foundation::{ERROR_ALREADY_EXISTS, GetLastError, HWND};
    use windows::Win32::System::Threading::CreateMutexW;
    use windows::Win32::UI::Shell::ShellExecuteW;
    use windows::Win32::UI::WindowsAndMessaging::SW_SHOWNORMAL;

    /// Unpackaged-app AppUserModelID; registered per-user in HKCU on first
    /// run (the WinAppSDK Register() substitute — design §6 of the Rust spec).
    const AUMID: &str = "NotifyAgent.Rust";

    fn register_aumid() -> anyhow::Result<()> {
        let hkcu = winreg::RegKey::predef(winreg::enums::HKEY_CURRENT_USER);
        let (key, _) = hkcu.create_subkey(format!(r"Software\Classes\AppUserModelId\{AUMID}"))?;
        key.set_value("DisplayName", &"Desktop Notification Agent (Rust)")?;
        Ok(())
    }

    fn xml_escape(s: &str) -> String {
        s.replace('&', "&amp;").replace('<', "&lt;").replace('>', "&gt;")
            .replace('"', "&quot;").replace('\'', "&apos;")
    }

    pub struct WindowsToastRenderer;

    #[async_trait]
    impl ToastRenderer for WindowsToastRenderer {
        async fn show(&self, toast: &ToastRequest) -> anyhow::Result<DateTime<Utc>> {
            // Budget (design §7): ≤3 text elements, 1 button. Title/message are
            // already grapheme-truncated by the content factory.
            let attribution = toast.attribution.as_deref().map(xml_escape).map(|a| {
                format!(r#"<text placement="attribution">{a}</text>"#)
            }).unwrap_or_default();
            let actions = match (&toast.action_label, &toast.action_url) {
                (Some(label), Some(url)) => format!(
                    r#"<actions><action content="{}" arguments="{}" activationType="foreground"/></actions>"#,
                    xml_escape(label), xml_escape(url)
                ),
                _ => String::new(),
            };
            let xml = format!(
                r#"<toast><visual><binding template="ToastGeneric"><text>{}</text><text>{}</text>{attribution}</binding></visual>{actions}</toast>"#,
                xml_escape(&toast.title), xml_escape(&toast.message)
            );

            let doc = XmlDocument::new()?;
            doc.LoadXml(&HSTRING::from(xml))?;
            let notification = ToastNotification::CreateToastNotification(&doc)?;

            // Button clicks while the agent runs: validate and open http(s) only.
            notification.Activated(&TypedEventHandler::new(|_, args: &Option<windows::core::IInspectable>| {
                if let Some(args) = args {
                    if let Ok(activated) = args.cast::<ToastActivatedEventArgs>() {
                        if let Ok(arguments) = activated.Arguments() {
                            let url = arguments.to_string();
                            if url.starts_with("https://") || url.starts_with("http://") {
                                unsafe {
                                    ShellExecuteW(HWND::default(), w!("open"),
                                        &HSTRING::from(url), None, None, SW_SHOWNORMAL);
                                }
                            }
                        }
                    }
                }
                Ok(())
            }))?;

            ToastNotificationManager::CreateToastNotifierWithId(&HSTRING::from(AUMID))?
                .Show(&notification)?;
            Ok(Utc::now())
        }
    }

    /// Stable per-install device id, SHARED with the C# head (same file), so
    /// acks correlate to one device regardless of which agent runs.
    fn device_id() -> anyhow::Result<String> {
        let dir = std::path::PathBuf::from(std::env::var("LOCALAPPDATA")?)
            .join("DesktopNotificationAgent");
        std::fs::create_dir_all(&dir)?;
        let path = dir.join("device-id");
        if let Ok(existing) = std::fs::read_to_string(&path) {
            let existing = existing.trim().to_string();
            if !existing.is_empty() {
                return Ok(existing);
            }
        }
        let id = format!("d-{:x}", std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)?.as_nanos());
        std::fs::write(&path, &id)?;
        Ok(id)
    }

    pub fn run() -> anyhow::Result<()> {
        // One instance per interactive session: "Local\" mutexes are
        // session-scoped. Deliberately distinct from the C# mutex name so the
        // two heads can be compared side by side (Rust spec §6).
        unsafe {
            let _mutex = CreateMutexW(None, true, w!("Local\\NotifyAgentRust"))?;
            if GetLastError() == ERROR_ALREADY_EXISTS {
                return Ok(());
            }
        }
        register_aumid()?;

        tokio::runtime::Runtime::new()?.block_on(async {
            tracing_subscriber::fmt()
                .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
                .init();
            let config = AgentConfig::from_env();
            let renderer: Arc<dyn ToastRenderer> = Arc::new(WindowsToastRenderer);
            let identity: Arc<dyn IdentityProvider> = match std::env::var("NOTIFY_AAD_CLIENT_ID") {
                Ok(client_id) if !client_id.trim().is_empty() => Arc::new(DeviceCodeIdentity {
                    client_id,
                    tenant: std::env::var("NOTIFY_AAD_TENANT_ID").unwrap_or_else(|_| "organizations".into()),
                    device_id: device_id()?,
                    renderer: renderer.clone(),
                }),
                _ => Arc::new(EnvIdentity),
            };
            let host = AgentHost::start(config, identity, renderer).await?;
            tracing::info!(subject = host.subject(), "agent running");
            tokio::signal::ctrl_c().await?;
            host.shutdown().await
        })
    }
}

#[cfg(windows)]
fn main() -> anyhow::Result<()> {
    win::run()
}
```

- [ ] **Step 3: Verify the Linux workspace stays green**

Run: `cd rust && cargo build && cargo test 2>&1 | tail -4`
Expected: clean build (windows crate compiles as the stub), all tests passing, zero warnings.

- [ ] **Step 4: Install the cross toolchain and cross-compile**

Create `rust/.cargo/config.toml`:

```toml
[target.x86_64-pc-windows-gnu]
linker = "x86_64-w64-mingw32-gcc"
```

```bash
sudo apt-get install -y mingw-w64
rustup target add x86_64-pc-windows-gnu
cd /home/cjamhe01385/os-notification/.worktrees/rust-agent/rust
cargo build --release --target x86_64-pc-windows-gnu -p notify-agent-windows 2>&1 | tail -3
ls -la target/x86_64-pc-windows-gnu/release/notify-agent-windows.exe
```
Expected: `Finished` line and a `notify-agent-windows.exe` (a real, runnable Windows binary — the capability the C# toolchain lacked). If the `windows` crate fails to link a specific import library under mingw, record the exact error in your report and fall back to `cargo check --target x86_64-pc-windows-gnu -p notify-agent-windows` (full type-check) — do not fight the linker for more than two attempts.

- [ ] **Step 5: Commit**

```bash
git add rust/
git commit -m "feat(rust): Windows head with WinRT toasts, AUMID self-registration, mingw cross-build"
```

- [ ] **Step 6: Windows 11 run-verification checklist (manual, later, on a Windows 11 machine)**

```powershell
# Copy target/x86_64-pc-windows-gnu/release/notify-agent-windows.exe to the machine, then:
$env:NOTIFY_NATS_URL = "nats://<linux-host>:4222"
$env:NOTIFY_USER_ID  = "u_demo"     # env identity until an Entra app registration exists
.\notify-agent-windows.exe
# From the Linux box: dotnet run --project tools/TestPublisher -- u_demo "Hi" "From Rust" critical
```
Expected: native toast "Hi / From Rust" with a button that opens the browser; both acks in the publisher output; a second `notify-agent-windows.exe` in the same session exits immediately (mutex); with `NOTIFY_AAD_CLIENT_ID` set, a sign-in toast + console prompt appears (device-code flow). Record results in the PR description.

---

## Verification sweep (after all tasks)

```bash
export PATH="$HOME/.cargo/bin:$PATH"
cd /home/cjamhe01385/os-notification/.worktrees/rust-agent/rust
cargo build && cargo test 2>&1 | tail -5          # all green, NATS integration live
cargo build --release --target x86_64-pc-windows-gnu -p notify-agent-windows 2>&1 | tail -2
```

## Spec coverage map (self-review record)

| Spec section | Where covered |
|---|---|
| §1 goal/decisions | Workspace (T1), heads (T8/T9), C# TestPublisher reuse (T8) |
| §2 wire contract | Parser+limits (T1), grapheme (T2), constants (T4/T6), ack shape (T5), subjects/env (T7) |
| §3 layout/traits | T1 scaffold; ToastRenderer (T2), TelemetryPublisher (T5), RenderSink (T4), IdentityProvider (T7) |
| §4 stack | T1 Cargo.toml (+ tokio-util TaskTracker fulfilling the JoinSet drain role) |
| §5 improvements | seq ordering (T1/T2/T4), bounded drain (T4/T6), no-hang (T4), loop-death exit (T7) |
| §6 Windows head | T9 (AUMID, toast XML, activation, mutex, shared device-id) |
| §7 identity | T7 (EnvIdentity, device-code flow, oid extraction, memory-only tokens) |
| §8 error doctrine | T6 (silent drops before ack, logged render failures, counters) |
| §9 testing | Unit tests T1–T7 (39), integration (T7), cross-language smoke (T8) |
| §10 build | rust-toolchain.toml 1.96.1 (T1), mingw cross-build (T9) |
