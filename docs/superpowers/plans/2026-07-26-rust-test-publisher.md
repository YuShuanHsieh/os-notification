# Rust Test Publisher Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a new Rust binary crate `rust/test-publisher` that is a full-feature-parity port of `tools/TestPublisher/Program.cs` (the C# NATS test-event publisher), so the Rust workspace has its own dev-side event producer.

**Architecture:** Three small modules (`spec`, `args`, `payload`) hold pure, unit-testable logic; `main.rs` wires them to `async-nats` for the actual publish/ack-watch I/O loop, which is verified manually against a live NATS server (matching how the C# tool has always been verified — it has no automated tests either).

**Tech Stack:** Rust 2021 edition (pinned by `../rust-toolchain.toml`), `async-nats` 0.38 (`websockets` feature), `tokio` (`full`), `serde_json`, `anyhow`, `chrono` (`serde` feature), `futures`, `rand` 0.8 — all already resolved in `rust/Cargo.lock` via other workspace crates, so no new dependency versions enter the lockfile.

## Global Constraints

- Full feature parity with `tools/TestPublisher/Program.cs`: same env vars, same NATS subject pattern, same 5 scenarios (`presence`, `invoice`, `progress`, `batch`, `dedup`) with identical field values and `Expect` strings, same flags, same legacy positional mode, same JSON payload shape, same `[PUB]`/`[ACK]` console output, same usage-error behavior (exit code 2).
- `tools/TestPublisher` (C#) is **not** modified or removed — it stays as-is.
- No new dependencies beyond what's already in `rust/Cargo.lock`: `async-nats` (`websockets`), `tokio` (`full`), `serde_json`, `anyhow`, `chrono` (`serde`), `futures`, `rand` (`0.8`, matching the version already pulled by `async-nats`/`nkeys`/`nuid` — confirmed via `cargo tree -i rand@0.8.7`).
- CLI parsing is hand-rolled (`std::env::args`), matching `notify-agent-console`/`notify-agent-windows` — no `clap`.
- No NATS integration test for this crate (the C# tool has none either — it's a dev tool verified live).

---

### Task 1: Crate scaffold + `PublishSpec` + scenario presets

**Files:**
- Modify: `rust/Cargo.toml`
- Create: `rust/test-publisher/Cargo.toml`
- Create: `rust/test-publisher/src/main.rs` (placeholder `fn main() {}` for now — Task 4 fills this in)
- Create: `rust/test-publisher/src/spec.rs`

**Interfaces:**
- Consumes: nothing (first task).
- Produces (in `src/spec.rs`, `mod spec;` declared in `main.rs`):
  - `pub struct PublishSpec { pub user_id: String, pub title: String, pub message: String, pub secondary: Option<String>, pub notification_type: String, pub priority: String, pub count: u32, pub image_url: Option<String>, pub image_shape: String, pub action_label: Option<String>, pub action_url: Option<String>, pub agg_key: Option<String>, pub dedup_key: Option<String>, pub replaceable: bool, pub delay_ms: u64, pub messages: Option<Vec<String>>, pub expect: Option<String> }` — derives `Debug, Clone, PartialEq`.
  - `impl PublishSpec { pub fn defaults(user_id: impl Into<String>) -> Self }`
  - `pub fn apply_scenario(spec: &mut PublishSpec, name: &str) -> bool` — mutates `spec` in place, returns `false` for an unknown scenario name.

- [ ] **Step 1: Register the new workspace member**

Edit `rust/Cargo.toml`:

```toml
[workspace]
resolver = "2"
members = ["notify-agent-core", "notify-agent-console", "notify-agent-windows", "test-publisher"]
```

- [ ] **Step 2: Create the crate manifest**

Create `rust/test-publisher/Cargo.toml`:

```toml
[package]
name = "test-publisher"
version = "0.1.0"
edition = "2021"

[dependencies]
anyhow = "1"
async-nats = { version = "0.38", features = ["websockets"] }
chrono = { version = "0.4", features = ["serde"] }
futures = "0.3"
rand = "0.8"
serde_json = "1"
tokio = { version = "1", features = ["full"] }
```

- [ ] **Step 3: Create a placeholder binary entry point**

Create `rust/test-publisher/src/main.rs`:

```rust
mod spec;

fn main() {}
```

- [ ] **Step 4: Write the failing tests for `PublishSpec` defaults and scenarios**

Create `rust/test-publisher/src/spec.rs`:

```rust
#[derive(Debug, Clone, PartialEq)]
pub struct PublishSpec {
    pub user_id: String,
    pub title: String,
    pub message: String,
    pub secondary: Option<String>,
    pub notification_type: String,
    pub priority: String,
    pub count: u32,
    pub image_url: Option<String>,
    pub image_shape: String,
    pub action_label: Option<String>,
    pub action_url: Option<String>,
    pub agg_key: Option<String>,
    pub dedup_key: Option<String>,
    pub replaceable: bool,
    pub delay_ms: u64,
    pub messages: Option<Vec<String>>,
    pub expect: Option<String>,
}

impl PublishSpec {
    pub fn defaults(user_id: impl Into<String>) -> Self {
        todo!()
    }
}

pub fn apply_scenario(spec: &mut PublishSpec, name: &str) -> bool {
    todo!()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn defaults_match_the_legacy_baseline() {
        let spec = PublishSpec::defaults("u1");
        assert_eq!(spec.user_id, "u1");
        assert_eq!(spec.title, "Invoice ready");
        assert_eq!(spec.message, "Invoice INV-8492 is ready for review.");
        assert_eq!(spec.secondary.as_deref(), Some("TestPublisher"));
        assert_eq!(spec.notification_type, "billing.invoice.ready");
        assert_eq!(spec.priority, "normal");
        assert_eq!(spec.count, 1);
        assert_eq!(spec.image_url, None);
        assert_eq!(spec.image_shape, "circle");
        assert_eq!(spec.action_label.as_deref(), Some("View"));
        assert_eq!(spec.action_url.as_deref(), Some("https://app.example.com/invoices/8492"));
        assert_eq!(spec.agg_key, None);
        assert_eq!(spec.dedup_key, None);
        assert!(!spec.replaceable);
        assert_eq!(spec.delay_ms, 0);
        assert_eq!(spec.messages, None);
        assert_eq!(spec.expect, None);
    }

    #[test]
    fn unknown_scenario_returns_false_and_leaves_spec_untouched() {
        let mut spec = PublishSpec::defaults("u1");
        let before = spec.clone();
        assert!(!apply_scenario(&mut spec, "not-a-scenario"));
        assert_eq!(spec, before);
    }

    #[test]
    fn presence_scenario() {
        let mut spec = PublishSpec::defaults("u1");
        assert!(apply_scenario(&mut spec, "presence"));
        assert_eq!(spec.title, "Tony Redmond");
        assert_eq!(spec.message, "is now available");
        assert_eq!(spec.secondary.as_deref(), Some("Microsoft Teams"));
        assert_eq!(spec.notification_type, "presence.available");
        assert_eq!(spec.priority, "critical");
        assert_eq!(spec.image_url.as_deref(), Some("https://i.pravatar.cc/96?u=tony"));
        assert_eq!(spec.image_shape, "circle");
        assert_eq!(spec.action_label.as_deref(), Some("Open chat"));
        assert_eq!(spec.action_url.as_deref(), Some("https://teams.example.com/chat/tony"));
        assert_eq!(spec.expect.as_deref(), Some("1 avatar toast, 2 acks"));
    }

    #[test]
    fn invoice_scenario() {
        let mut spec = PublishSpec::defaults("u1");
        assert!(apply_scenario(&mut spec, "invoice"));
        assert_eq!(spec.title, "Invoice ready");
        assert_eq!(spec.message, "Invoice INV-8492 is ready for review.");
        assert_eq!(spec.secondary.as_deref(), Some("Contoso Billing"));
        assert_eq!(spec.notification_type, "billing.invoice.ready");
        assert_eq!(spec.priority, "normal");
        assert_eq!(spec.action_label.as_deref(), Some("View invoice"));
        assert_eq!(spec.action_url.as_deref(), Some("https://app.example.com/invoices/8492"));
        assert_eq!(spec.expect.as_deref(), Some("1 toast after ~10s, 2 acks"));
    }

    #[test]
    fn progress_scenario_leaves_untouched_fields_at_their_defaults() {
        let mut spec = PublishSpec::defaults("u1");
        assert!(apply_scenario(&mut spec, "progress"));
        assert_eq!(spec.title, "Export job");
        assert_eq!(spec.notification_type, "job.progress");
        assert_eq!(spec.agg_key.as_deref(), Some("job.progress"));
        assert_eq!(spec.priority, "normal");
        assert!(spec.replaceable);
        assert_eq!(spec.delay_ms, 100);
        assert_eq!(spec.messages, Some(vec!["10%".to_string(), "60%".to_string(), "90%".to_string()]));
        assert_eq!(spec.expect.as_deref(), Some("after ~10s ONE toast showing 90%"));
        // progress doesn't touch these — they stay at PublishSpec::defaults() values.
        assert_eq!(spec.action_label.as_deref(), Some("View"));
        assert_eq!(spec.action_url.as_deref(), Some("https://app.example.com/invoices/8492"));
        assert_eq!(spec.secondary.as_deref(), Some("TestPublisher"));
    }

    #[test]
    fn batch_scenario_leaves_untouched_fields_at_their_defaults() {
        let mut spec = PublishSpec::defaults("u1");
        assert!(apply_scenario(&mut spec, "batch"));
        assert_eq!(spec.title, "Batch demo");
        assert_eq!(spec.agg_key.as_deref(), Some("demo.batch"));
        assert_eq!(spec.priority, "normal");
        assert_eq!(spec.delay_ms, 100);
        assert_eq!(spec.messages, Some(vec!["first".to_string(), "second".to_string(), "third".to_string()]));
        assert_eq!(spec.expect.as_deref(), Some("ONE '3 notifications — demo.batch' toast, 6 acks sharing one toastSubmittedAt"));
        // batch doesn't touch these — they stay at PublishSpec::defaults() values.
        assert_eq!(spec.notification_type, "billing.invoice.ready");
        assert_eq!(spec.action_label.as_deref(), Some("View"));
    }

    #[test]
    fn dedup_scenario_leaves_untouched_fields_at_their_defaults() {
        let mut spec = PublishSpec::defaults("u1");
        assert!(apply_scenario(&mut spec, "dedup"));
        assert_eq!(spec.priority, "critical");
        assert_eq!(spec.dedup_key.as_deref(), Some("dedup-demo"));
        assert_eq!(spec.count, 3);
        assert_eq!(spec.expect.as_deref(), Some("ONE toast, exactly 2 acks (duplicates dropped)"));
        // dedup doesn't touch these — they stay at PublishSpec::defaults() values.
        assert_eq!(spec.title, "Invoice ready");
        assert_eq!(spec.notification_type, "billing.invoice.ready");
    }
}
```

- [ ] **Step 5: Run the tests to verify they fail**

Run: `cargo test -p test-publisher`
Expected: the crate compiles cleanly, then every test panics with `not yet implemented` (from the `todo!()` bodies) — `cargo test` reports `FAILED` for all 7 tests. If it fails to compile instead, fix the struct/signature mismatch before continuing.

- [ ] **Step 6: Implement `PublishSpec::defaults` and `apply_scenario`**

Replace the two `todo!()` bodies in `rust/test-publisher/src/spec.rs`:

```rust
impl PublishSpec {
    pub fn defaults(user_id: impl Into<String>) -> Self {
        Self {
            user_id: user_id.into(),
            title: "Invoice ready".to_string(),
            message: "Invoice INV-8492 is ready for review.".to_string(),
            secondary: Some("TestPublisher".to_string()),
            notification_type: "billing.invoice.ready".to_string(),
            priority: "normal".to_string(),
            count: 1,
            image_url: None,
            image_shape: "circle".to_string(),
            action_label: Some("View".to_string()),
            action_url: Some("https://app.example.com/invoices/8492".to_string()),
            agg_key: None,
            dedup_key: None,
            replaceable: false,
            delay_ms: 0,
            messages: None,
            expect: None,
        }
    }
}

pub fn apply_scenario(spec: &mut PublishSpec, name: &str) -> bool {
    match name {
        "presence" => {
            spec.title = "Tony Redmond".to_string();
            spec.message = "is now available".to_string();
            spec.secondary = Some("Microsoft Teams".to_string());
            spec.notification_type = "presence.available".to_string();
            spec.priority = "critical".to_string();
            spec.image_url = Some("https://i.pravatar.cc/96?u=tony".to_string());
            spec.image_shape = "circle".to_string();
            spec.action_label = Some("Open chat".to_string());
            spec.action_url = Some("https://teams.example.com/chat/tony".to_string());
            spec.expect = Some("1 avatar toast, 2 acks".to_string());
            true
        }
        "invoice" => {
            spec.title = "Invoice ready".to_string();
            spec.message = "Invoice INV-8492 is ready for review.".to_string();
            spec.secondary = Some("Contoso Billing".to_string());
            spec.notification_type = "billing.invoice.ready".to_string();
            spec.priority = "normal".to_string();
            spec.action_label = Some("View invoice".to_string());
            spec.action_url = Some("https://app.example.com/invoices/8492".to_string());
            spec.expect = Some("1 toast after ~10s, 2 acks".to_string());
            true
        }
        "progress" => {
            spec.title = "Export job".to_string();
            spec.notification_type = "job.progress".to_string();
            spec.agg_key = Some("job.progress".to_string());
            spec.priority = "normal".to_string();
            spec.replaceable = true;
            spec.delay_ms = 100;
            spec.messages = Some(vec!["10%".to_string(), "60%".to_string(), "90%".to_string()]);
            spec.expect = Some("after ~10s ONE toast showing 90%".to_string());
            true
        }
        "batch" => {
            spec.title = "Batch demo".to_string();
            spec.agg_key = Some("demo.batch".to_string());
            spec.priority = "normal".to_string();
            spec.delay_ms = 100;
            spec.messages = Some(vec!["first".to_string(), "second".to_string(), "third".to_string()]);
            spec.expect = Some("ONE '3 notifications — demo.batch' toast, 6 acks sharing one toastSubmittedAt".to_string());
            true
        }
        "dedup" => {
            spec.priority = "critical".to_string();
            spec.dedup_key = Some("dedup-demo".to_string());
            spec.count = 3;
            spec.expect = Some("ONE toast, exactly 2 acks (duplicates dropped)".to_string());
            true
        }
        _ => false,
    }
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `cargo test -p test-publisher`
Expected: `test result: ok. 7 passed; 0 failed`

- [ ] **Step 8: Commit**

```bash
git add rust/Cargo.toml rust/test-publisher
git commit -m "feat(rust): scaffold test-publisher crate with PublishSpec and scenarios"
```

---

### Task 2: Argument parsing

**Files:**
- Create: `rust/test-publisher/src/args.rs`
- Modify: `rust/test-publisher/src/main.rs` (add `mod args;`)

**Interfaces:**
- Consumes: `PublishSpec` and `apply_scenario` from `crate::spec` (Task 1).
- Produces: `pub fn parse(args: &[String]) -> Result<PublishSpec, String>` in `src/args.rs` — `args` excludes the program name (i.e. the raw CLI arguments after argv[0]); `Err` carries just the error message (no "error:" prefix, no usage text — the caller in Task 4 prints those).

- [ ] **Step 1: Add the module declaration**

In `rust/test-publisher/src/main.rs`, add above `mod spec;`:

```rust
mod args;
```

- [ ] **Step 2: Write the failing tests for `parse`**

Create `rust/test-publisher/src/args.rs`:

```rust
use crate::spec::{apply_scenario, PublishSpec};

pub fn parse(args: &[String]) -> Result<PublishSpec, String> {
    todo!()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn args(items: &[&str]) -> Vec<String> {
        items.iter().map(|s| s.to_string()).collect()
    }

    #[test]
    fn empty_args_is_an_error() {
        let err = parse(&args(&[])).unwrap_err();
        assert_eq!(err, "first argument must be <userId>");
    }

    #[test]
    fn flag_as_first_arg_is_an_error() {
        let err = parse(&args(&["--title", "x"])).unwrap_err();
        assert_eq!(err, "first argument must be <userId>");
    }

    #[test]
    fn bare_user_id_uses_defaults() {
        let spec = parse(&args(&["u1"])).unwrap();
        assert_eq!(spec, PublishSpec::defaults("u1"));
    }

    #[test]
    fn legacy_positionals_fill_title_message_priority_count_image_url() {
        let spec = parse(&args(&["u1", "T", "M", "critical", "3", "https://img"])).unwrap();
        assert_eq!(spec.title, "T");
        assert_eq!(spec.message, "M");
        assert_eq!(spec.priority, "critical");
        assert_eq!(spec.count, 3);
        assert_eq!(spec.image_url.as_deref(), Some("https://img"));
    }

    #[test]
    fn too_many_legacy_positionals_is_an_error() {
        let err = parse(&args(&["u1", "T", "M", "critical", "3", "https://img", "extra"])).unwrap_err();
        assert_eq!(err, "too many legacy positional arguments");
    }

    #[test]
    fn invalid_legacy_count_is_an_error() {
        let err = parse(&args(&["u1", "T", "M", "critical", "zero"])).unwrap_err();
        assert_eq!(err, "count must be a positive integer");
        let err = parse(&args(&["u1", "T", "M", "critical", "0"])).unwrap_err();
        assert_eq!(err, "count must be a positive integer");
    }

    #[test]
    fn scenario_applies_preset_fields() {
        let spec = parse(&args(&["u1", "--scenario", "invoice"])).unwrap();
        let mut expected = PublishSpec::defaults("u1");
        apply_scenario(&mut expected, "invoice");
        assert_eq!(spec, expected);
    }

    #[test]
    fn unknown_scenario_is_an_error() {
        let err = parse(&args(&["u1", "--scenario", "nope"])).unwrap_err();
        assert_eq!(err, "unknown scenario 'nope' (presence|invoice|progress|batch|dedup)");
    }

    #[test]
    fn scenario_combined_with_legacy_positional_is_an_error() {
        let err = parse(&args(&["u1", "T", "--scenario", "invoice"])).unwrap_err();
        assert_eq!(err, "--scenario cannot be combined with legacy positional arguments");
    }

    #[test]
    fn flags_override_scenario_fields() {
        let spec = parse(&args(&["u1", "--scenario", "presence", "--priority", "normal"])).unwrap();
        assert_eq!(spec.priority, "normal");
        assert_eq!(spec.title, "Tony Redmond"); // untouched preset field survives
    }

    #[test]
    fn message_flag_clears_messages_list() {
        let spec = parse(&args(&["u1", "--scenario", "progress", "--message", "custom"])).unwrap();
        assert_eq!(spec.message, "custom");
        assert_eq!(spec.messages, None);
    }

    #[test]
    fn count_flag_clears_messages_list() {
        let spec = parse(&args(&["u1", "--scenario", "batch", "--count", "2"])).unwrap();
        assert_eq!(spec.count, 2);
        assert_eq!(spec.messages, None);
    }

    #[test]
    fn invalid_count_flag_is_an_error() {
        let err = parse(&args(&["u1", "--count", "0"])).unwrap_err();
        assert_eq!(err, "--count must be a positive integer");
    }

    #[test]
    fn image_shape_flag_validates_value() {
        let err = parse(&args(&["u1", "--image-shape", "hexagon"])).unwrap_err();
        assert_eq!(err, "--image-shape must be circle or square");
        let spec = parse(&args(&["u1", "--image-shape", "square"])).unwrap();
        assert_eq!(spec.image_shape, "square");
    }

    #[test]
    fn replaceable_flag_needs_no_value() {
        let spec = parse(&args(&["u1", "--replaceable"])).unwrap();
        assert!(spec.replaceable);
    }

    #[test]
    fn delay_ms_flag_validates_value() {
        let err = parse(&args(&["u1", "--delay-ms", "-1"])).unwrap_err();
        assert_eq!(err, "--delay-ms must be a non-negative integer");
        let spec = parse(&args(&["u1", "--delay-ms", "250"])).unwrap();
        assert_eq!(spec.delay_ms, 250);
    }

    #[test]
    fn unknown_flag_is_an_error() {
        let err = parse(&args(&["u1", "--nope"])).unwrap_err();
        assert_eq!(err, "unknown flag '--nope'");
    }

    #[test]
    fn flag_missing_value_is_an_error() {
        let err = parse(&args(&["u1", "--title"])).unwrap_err();
        assert_eq!(err, "--title needs a value");
    }
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cargo test -p test-publisher`
Expected: the crate compiles cleanly, then every test calling `parse` panics with `not yet implemented` — `cargo test` reports `FAILED` for all of this module's tests (the Task 1 `spec` tests still pass). If it fails to compile instead, fix the type mismatch before continuing.

- [ ] **Step 4: Implement `parse`**

Replace the `todo!()` body in `rust/test-publisher/src/args.rs`:

```rust
fn next_value(flags: &[String], i: &mut usize, flag: &str) -> Result<String, String> {
    *i += 1;
    flags.get(*i).cloned().ok_or_else(|| format!("{flag} needs a value"))
}

pub fn parse(args: &[String]) -> Result<PublishSpec, String> {
    if args.is_empty() || args[0].starts_with("--") {
        return Err("first argument must be <userId>".to_string());
    }

    let mut spec = PublishSpec::defaults(args[0].clone());
    let rest = &args[1..];
    let split_at = rest.iter().position(|a| a.starts_with("--")).unwrap_or(rest.len());
    let positionals = &rest[..split_at];
    let flags = &rest[split_at..];

    let mut scenario: Option<&str> = None;
    for i in 0..flags.len() {
        if flags[i] == "--scenario" {
            scenario = Some(flags.get(i + 1).ok_or_else(|| "--scenario needs a value".to_string())?.as_str());
        }
    }

    if scenario.is_some() && !positionals.is_empty() {
        return Err("--scenario cannot be combined with legacy positional arguments".to_string());
    }

    if let Some(name) = scenario {
        if !apply_scenario(&mut spec, name) {
            return Err(format!("unknown scenario '{name}' (presence|invoice|progress|batch|dedup)"));
        }
    }

    if positionals.len() > 5 {
        return Err("too many legacy positional arguments".to_string());
    }
    if let Some(v) = positionals.first() {
        spec.title = v.clone();
    }
    if let Some(v) = positionals.get(1) {
        spec.message = v.clone();
    }
    if let Some(v) = positionals.get(2) {
        spec.priority = v.clone();
    }
    if let Some(v) = positionals.get(3) {
        let c: i64 = v.parse().map_err(|_| "count must be a positive integer".to_string())?;
        if c < 1 {
            return Err("count must be a positive integer".to_string());
        }
        spec.count = c as u32;
    }
    if let Some(v) = positionals.get(4) {
        spec.image_url = Some(v.clone());
    }

    let mut i = 0;
    while i < flags.len() {
        let flag = flags[i].clone();
        match flag.as_str() {
            "--scenario" => i += 1, // already applied above
            "--title" => spec.title = next_value(flags, &mut i, &flag)?,
            "--message" => {
                spec.message = next_value(flags, &mut i, &flag)?;
                spec.messages = None;
            }
            "--secondary" => spec.secondary = Some(next_value(flags, &mut i, &flag)?),
            "--type" => spec.notification_type = next_value(flags, &mut i, &flag)?,
            "--priority" => spec.priority = next_value(flags, &mut i, &flag)?,
            "--count" => {
                let v = next_value(flags, &mut i, &flag)?;
                let c: i64 = v.parse().map_err(|_| "--count must be a positive integer".to_string())?;
                if c < 1 {
                    return Err("--count must be a positive integer".to_string());
                }
                spec.count = c as u32;
                spec.messages = None;
            }
            "--image-url" => spec.image_url = Some(next_value(flags, &mut i, &flag)?),
            "--image-shape" => {
                let shape = next_value(flags, &mut i, &flag)?;
                if shape != "circle" && shape != "square" {
                    return Err("--image-shape must be circle or square".to_string());
                }
                spec.image_shape = shape;
            }
            "--action-label" => spec.action_label = Some(next_value(flags, &mut i, &flag)?),
            "--action-url" => spec.action_url = Some(next_value(flags, &mut i, &flag)?),
            "--agg-key" => spec.agg_key = Some(next_value(flags, &mut i, &flag)?),
            "--dedup-key" => spec.dedup_key = Some(next_value(flags, &mut i, &flag)?),
            "--replaceable" => spec.replaceable = true,
            "--delay-ms" => {
                let v = next_value(flags, &mut i, &flag)?;
                let d: i64 = v.parse().map_err(|_| "--delay-ms must be a non-negative integer".to_string())?;
                if d < 0 {
                    return Err("--delay-ms must be a non-negative integer".to_string());
                }
                spec.delay_ms = d as u64;
            }
            other => return Err(format!("unknown flag '{other}'")),
        }
        i += 1;
    }

    Ok(spec)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cargo test -p test-publisher`
Expected: `test result: ok. 24 passed; 0 failed` (7 from Task 1's `spec` module + 17 from this task's `args` module).

- [ ] **Step 6: Commit**

```bash
git add rust/test-publisher/src/args.rs rust/test-publisher/src/main.rs
git commit -m "feat(rust): add test-publisher CLI argument parsing"
```

---

### Task 3: JSON payload construction

**Files:**
- Create: `rust/test-publisher/src/payload.rs`
- Modify: `rust/test-publisher/src/main.rs` (add `mod payload;`)

**Interfaces:**
- Consumes: `PublishSpec` from `crate::spec` (Task 1).
- Produces: `pub fn build_payload(spec: &PublishSpec, message: &str, event_id: &str) -> serde_json::Value` in `src/payload.rs`.

- [ ] **Step 1: Add the module declaration**

In `rust/test-publisher/src/main.rs`, add:

```rust
mod payload;
```

- [ ] **Step 2: Write the failing tests for `build_payload`**

Create `rust/test-publisher/src/payload.rs`:

```rust
use serde_json::Value;

use crate::spec::PublishSpec;

pub fn build_payload(spec: &PublishSpec, message: &str, event_id: &str) -> Value {
    todo!()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn schema_version_is_10_without_an_image() {
        let mut spec = PublishSpec::defaults("u1");
        spec.image_url = None;
        let payload = build_payload(&spec, "hello", "evt-1");
        assert_eq!(payload["schemaVersion"], "1.0");
        assert!(payload["content"].get("image").is_none());
    }

    #[test]
    fn schema_version_is_11_with_an_image() {
        let mut spec = PublishSpec::defaults("u1");
        spec.image_url = Some("https://img".to_string());
        spec.image_shape = "square".to_string();
        let payload = build_payload(&spec, "hello", "evt-1");
        assert_eq!(payload["schemaVersion"], "1.1");
        assert_eq!(payload["content"]["image"]["url"], "https://img");
        assert_eq!(payload["content"]["image"]["shape"], "square");
    }

    #[test]
    fn content_omits_secondary_text_when_absent() {
        let mut spec = PublishSpec::defaults("u1");
        spec.secondary = None;
        let payload = build_payload(&spec, "hello", "evt-1");
        assert!(payload["content"].get("secondaryText").is_none());
    }

    #[test]
    fn content_includes_secondary_text_when_present() {
        let spec = PublishSpec::defaults("u1"); // secondary = Some("TestPublisher")
        let payload = build_payload(&spec, "hello", "evt-1");
        assert_eq!(payload["content"]["secondaryText"], "TestPublisher");
        assert_eq!(payload["content"]["title"], spec.title);
        assert_eq!(payload["content"]["message"], "hello");
    }

    #[test]
    fn action_included_only_when_both_label_and_url_set() {
        let mut spec = PublishSpec::defaults("u1");
        spec.action_url = None; // label still Some("View") from defaults
        let payload = build_payload(&spec, "hello", "evt-1");
        assert!(payload.get("action").is_none());

        spec.action_url = Some("https://x".to_string());
        let payload = build_payload(&spec, "hello", "evt-1");
        assert_eq!(payload["action"]["label"], "View");
        assert_eq!(payload["action"]["url"], "https://x");
    }

    #[test]
    fn classification_defaults_agg_key_to_type_and_dedup_key_to_event_id() {
        let spec = PublishSpec::defaults("u1"); // agg_key = None, dedup_key = None
        let payload = build_payload(&spec, "hello", "evt-1");
        assert_eq!(payload["classification"]["aggregationKey"], spec.notification_type);
        assert_eq!(payload["classification"]["deduplicationKey"], "evt-1");
        assert_eq!(payload["classification"]["priority"], spec.priority);
        assert_eq!(payload["classification"]["replaceable"], false);
    }

    #[test]
    fn classification_uses_explicit_agg_and_dedup_keys_when_set() {
        let mut spec = PublishSpec::defaults("u1");
        spec.agg_key = Some("custom-agg".to_string());
        spec.dedup_key = Some("custom-dedup".to_string());
        let payload = build_payload(&spec, "hello", "evt-1");
        assert_eq!(payload["classification"]["aggregationKey"], "custom-agg");
        assert_eq!(payload["classification"]["deduplicationKey"], "custom-dedup");
    }

    #[test]
    fn timestamps_are_present_as_strings() {
        let spec = PublishSpec::defaults("u1");
        let payload = build_payload(&spec, "hello", "evt-1");
        assert!(payload["timestamps"]["producerCreatedAt"].is_string());
        assert!(payload["timestamps"]["serverPublishedAt"].is_string());
    }

    #[test]
    fn target_and_event_id_and_notification_type_are_set() {
        let spec = PublishSpec::defaults("u1");
        let payload = build_payload(&spec, "hello", "evt-1");
        assert_eq!(payload["target"]["userId"], "u1");
        assert_eq!(payload["eventId"], "evt-1");
        assert_eq!(payload["notificationType"], spec.notification_type);
    }
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cargo test -p test-publisher`
Expected: the crate compiles cleanly, then every test calling `build_payload` panics with `not yet implemented` — `cargo test` reports `FAILED` for all of this module's tests (the Task 1/2 tests still pass).

- [ ] **Step 4: Implement `build_payload`**

Replace the `todo!()` body in `rust/test-publisher/src/payload.rs`:

```rust
pub fn build_payload(spec: &PublishSpec, message: &str, event_id: &str) -> Value {
    let mut content = serde_json::Map::new();
    content.insert("title".to_string(), Value::String(spec.title.clone()));
    content.insert("message".to_string(), Value::String(message.to_string()));
    if let Some(secondary) = &spec.secondary {
        content.insert("secondaryText".to_string(), Value::String(secondary.clone()));
    }
    if let Some(image_url) = &spec.image_url {
        content.insert(
            "image".to_string(),
            serde_json::json!({ "url": image_url, "shape": spec.image_shape }),
        );
    }

    let mut payload = serde_json::Map::new();
    payload.insert(
        "schemaVersion".to_string(),
        Value::String(if spec.image_url.is_some() { "1.1" } else { "1.0" }.to_string()),
    );
    payload.insert("eventId".to_string(), Value::String(event_id.to_string()));
    payload.insert("notificationType".to_string(), Value::String(spec.notification_type.clone()));
    payload.insert("target".to_string(), serde_json::json!({ "userId": spec.user_id }));
    payload.insert("content".to_string(), Value::Object(content));

    if let (Some(label), Some(url)) = (&spec.action_label, &spec.action_url) {
        payload.insert("action".to_string(), serde_json::json!({ "label": label, "url": url }));
    }

    let aggregation_key = spec.agg_key.clone().unwrap_or_else(|| spec.notification_type.clone());
    let deduplication_key = spec.dedup_key.clone().unwrap_or_else(|| event_id.to_string());
    payload.insert(
        "classification".to_string(),
        serde_json::json!({
            "priority": spec.priority,
            "aggregationKey": aggregation_key,
            "deduplicationKey": deduplication_key,
            "replaceable": spec.replaceable,
        }),
    );

    let producer_created_at = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Millis, true);
    let server_published_at = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Millis, true);
    payload.insert(
        "timestamps".to_string(),
        serde_json::json!({
            "producerCreatedAt": producer_created_at,
            "serverPublishedAt": server_published_at,
        }),
    );

    Value::Object(payload)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cargo test -p test-publisher`
Expected: `test result: ok. 33 passed; 0 failed` (24 from Tasks 1–2 + 9 from this task).

- [ ] **Step 6: Commit**

```bash
git add rust/test-publisher/src/payload.rs rust/test-publisher/src/main.rs
git commit -m "feat(rust): add test-publisher JSON payload construction"
```

---

### Task 4: Wire up `main.rs` — NATS publish/ack loop

**Files:**
- Modify: `rust/test-publisher/src/main.rs`

**Interfaces:**
- Consumes: `args::parse(&[String]) -> Result<PublishSpec, String>` (Task 2), `payload::build_payload(&PublishSpec, &str, &str) -> serde_json::Value` (Task 3).
- Produces: the `test-publisher` binary. No downstream Rust consumers — this is the final integration point, verified by running the binary (Task 6), not by unit test.

- [ ] **Step 1: Replace the placeholder `main.rs`**

Replace the full contents of `rust/test-publisher/src/main.rs`:

```rust
mod args;
mod payload;
mod spec;

use std::time::Duration;

use anyhow::Result;
use futures::StreamExt;
use rand::RngCore;

fn generate_event_id() -> String {
    let mut bytes = [0u8; 16];
    rand::thread_rng().fill_bytes(&mut bytes);
    let hex: String = bytes.iter().map(|b| format!("{b:02x}")).collect();
    format!("evt-{hex}")
}

fn print_usage(error: &str) {
    eprintln!("error: {error}");
    eprintln!("usage: test-publisher <userId> [title] [message] [priority] [count] [imageUrl]");
    eprintln!("       test-publisher <userId> --scenario presence|invoice|progress|batch|dedup [--flags]");
    eprintln!("       test-publisher <userId> [--flags]");
    eprintln!("flags: --title --message --secondary --type --priority --count --image-url");
    eprintln!("       --image-shape circle|square --action-label --action-url --agg-key");
    eprintln!("       --dedup-key --replaceable --delay-ms");
}

#[tokio::main]
async fn main() -> Result<()> {
    let cli_args: Vec<String> = std::env::args().skip(1).collect();
    let spec = match args::parse(&cli_args) {
        Ok(spec) => spec,
        Err(msg) => {
            print_usage(&msg);
            std::process::exit(2);
        }
    };

    let nats_url = std::env::var("NOTIFY_NATS_URL").unwrap_or_else(|_| "nats://127.0.0.1:4222".to_string());
    let ack_subject = std::env::var("NOTIFY_ACK_SUBJECT").unwrap_or_else(|_| "notify.ack.desktop".to_string());
    let subject = format!("notify.user.{}.desktop", spec.user_id);

    if let Some(expect) = &spec.expect {
        println!("EXPECT: {expect}");
    }

    let client = async_nats::ConnectOptions::new().connect(&nats_url).await?;

    let mut ack_sub = client.subscribe(ack_subject).await?;
    let ack_watcher = tokio::spawn(async move {
        while let Some(msg) = ack_sub.next().await {
            println!("[ACK] {}", String::from_utf8_lossy(&msg.payload));
        }
    });
    tokio::time::sleep(Duration::from_millis(300)).await;

    let messages: Vec<String> = spec
        .messages
        .clone()
        .unwrap_or_else(|| std::iter::repeat(spec.message.clone()).take(spec.count as usize).collect());

    for (i, message) in messages.iter().enumerate() {
        let event_id = generate_event_id();
        let payload = payload::build_payload(&spec, message, &event_id);
        let bytes = serde_json::to_vec(&payload)?;
        client.publish(subject.clone(), bytes.into()).await?;
        println!("[PUB] {event_id} -> {subject} (priority={})", spec.priority);
        if spec.delay_ms > 0 && i + 1 < messages.len() {
            tokio::time::sleep(Duration::from_millis(spec.delay_ms)).await;
        }
    }

    tokio::time::sleep(Duration::from_secs(12)).await;
    ack_watcher.abort();
    let _ = ack_watcher.await;

    Ok(())
}
```

- [ ] **Step 2: Build the workspace**

Run: `cargo build`
Expected: builds cleanly, no warnings.

- [ ] **Step 3: Run the full workspace test suite**

Run: `cargo test`
Expected: all `test-publisher` unit tests still pass (33), plus every pre-existing test in `notify-agent-core`/`notify-agent-console`/`notify-agent-windows` (the Task 1 baseline: 104 unit + 1 live NATS integration test) is unaffected.

- [ ] **Step 4: Manual smoke test — usage error path**

Run: `cargo run -p test-publisher --`
Expected: stderr prints `error: first argument must be <userId>` followed by the usage block; exit code `2` (`echo $?` confirms).

- [ ] **Step 5: Manual smoke test — live publish (requires a NATS server on 127.0.0.1:4222)**

Run: `cargo run -p test-publisher -- u_demo --scenario invoice`
Expected: prints `EXPECT: 1 toast after ~10s, 2 acks`, then one `[PUB] evt-... -> notify.user.u_demo.desktop (priority=normal)` line, then exits after ~12s (any `[ACK] ...` lines depend on an agent head being connected — see Task 6 for the full live matrix).

- [ ] **Step 6: Commit**

```bash
git add rust/test-publisher/src/main.rs
git commit -m "feat(rust): wire test-publisher main to async-nats publish/ack loop"
```

---

### Task 5: README updates

**Files:**
- Modify: `rust/README.md`

**Interfaces:** None (documentation only).

- [ ] **Step 1: Add `test-publisher` to the crate table**

In `rust/README.md`, in the `## Layout` table, add a row after `notify-agent-windows`:

```markdown
| `test-publisher` | Dev tool: publishes test notification events to NATS (Rust port of `../tools/TestPublisher`, full feature parity — scenarios, flags, legacy positional mode). |
```

- [ ] **Step 2: Show the Rust publisher as the primary publish command**

In `rust/README.md`, in the `## Run the console head` section, replace:

```markdown
Publish a test event from another shell (needs the .NET SDK):

```bash
export PATH="$HOME/.dotnet:$PATH"
dotnet run --project ../tools/TestPublisher -- u_demo --scenario presence
```

`TestPublisher --scenario <name>` drives every schema use case end-to-end (avatar image, action button, priority batching, deduplication, replaceable progress updates) — see `../tools/TestPublisher/Program.cs` or run it with `--help`-style bad input to print the usage text, which lists all scenarios and flags.
```

with:

```markdown
Publish a test event from another shell:

```bash
cargo run -p test-publisher -- u_demo --scenario presence
```

`test-publisher --scenario <name>` drives every schema use case end-to-end (avatar image, action button, priority batching, deduplication, replaceable progress updates) — run it with no args to print the usage text, which lists all scenarios and flags. It's a Rust port of the C# `../tools/TestPublisher` (`dotnet run --project ../tools/TestPublisher -- u_demo --scenario presence`, needs the .NET SDK), kept for parity — either works.
```

- [ ] **Step 3: Commit**

```bash
git add rust/README.md
git commit -m "docs(rust): document the test-publisher crate"
```

---

### Task 6: Full workspace verification + live scenario matrix

**Files:** None (verification only).

**Interfaces:** None.

- [ ] **Step 1: Clean workspace build and test**

Run: `cargo build && cargo test`
Expected: builds clean; all tests pass, including the live NATS integration test in `notify-agent-core` if a server is reachable on `127.0.0.1:4222`.

- [ ] **Step 2: Start a NATS server if one isn't already running**

Run: `docker run -d --name nats-dev -p 4222:4222 nats:2.10-alpine` (skip if already running — check with `docker ps`).

- [ ] **Step 3: Start the console head in the background**

Run (background): `NOTIFY_USER_ID=u_demo cargo run -p notify-agent-console`
Expected: prints `Agent subscribed to notify.user.u_demo.desktop on nats://127.0.0.1:4222. Ctrl+C to exit.`

- [ ] **Step 4: Run all 5 scenarios and confirm each `EXPECT` outcome**

For each scenario, run and compare the console head's `[TOAST]` output and the publisher's ack count against the `Expect` string it printed:

```bash
cargo run -p test-publisher -- u_demo --scenario presence
cargo run -p test-publisher -- u_demo --scenario invoice
cargo run -p test-publisher -- u_demo --scenario progress
cargo run -p test-publisher -- u_demo --scenario batch
cargo run -p test-publisher -- u_demo --scenario dedup
```

Expected, matching each scenario's `Expect` line:
- `presence`: exactly one `[TOAST]` block with the avatar image line and "Open chat" action; 2 `[ACK]` lines.
- `invoice`: one `[TOAST]` block after ~10s; 2 `[ACK]` lines.
- `progress`: one `[TOAST]` block after ~10s showing `90%` only.
- `batch`: one `[TOAST]` block titled "3 notifications — demo.batch"; 6 `[ACK]` lines.
- `dedup`: one `[TOAST]` block; exactly 2 `[ACK]` lines (not 6 — duplicates dropped).

- [ ] **Step 5: Prove legacy and mixed-mode parity**

```bash
cargo run -p test-publisher -- u_demo "Legacy title" "Legacy message" normal 1
```
Expected: publishes one event with `title = "Legacy title"`, `message = "Legacy message"`.

```bash
cargo run -p test-publisher -- u_demo Legacy --scenario invoice; echo "exit=$?"
```
Expected: `error: --scenario cannot be combined with legacy positional arguments` on stderr, `exit=2`.

- [ ] **Step 6: Stop the console head**

Bring the background `notify-agent-console` process to the foreground (or find/kill its PID) and send Ctrl+C / SIGINT; confirm it prints `Shutting down.` and exits 0.

- [ ] **Step 7: Record the outcome**

No commit for this task — it's verification only. If any scenario's live behavior doesn't match its `Expect` string, stop and fix the responsible task (`spec.rs` for wrong field values, `payload.rs` for wrong JSON shape, `main.rs` for wrong publish/ack timing) before considering the plan complete.
