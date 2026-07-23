# JSON-Driven Avatar/Image Toasts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `docs/superpowers/specs/2026-07-22-image-toasts-design.md`: an optional `content.image` in the JSON event (schema 1.1) rendered as a circular/square avatar (`appLogoOverride`) by the Rust Windows head via a best-effort local download cache, echoed by the console head, exercised end-to-end via an extended C# TestPublisher.

**Architecture:** Additive changes along the existing pipeline: parser gains `ImageRef` validation (invalid image drops the image, never the event) → `InboundNotification.image` → toast factory threads latest-wins image → new `ImageCache` module (https-only, 3MB/3s caps, SHA-256-named files, 50-file eviction) → new pure `toast_xml` module (Linux-testable) consumed by the Windows head.

**Tech Stack:** existing Rust workspace (1.96.1); new deps: `sha2`, reqwest `stream` feature. C# TestPublisher gains one optional argument.

## Global Constraints

- Best-effort doctrine extends to images: any image failure (bad scheme, oversize URL, fetch timeout/cap/content-type, IO) drops the **image only** — the event still parses, aggregates, renders, and acks exactly as today. Debug-level logs only.
- Image validation: scheme must be `https`, URL ≤ **2048 bytes**. Shape `"circle"` (default, also for unknown values) | `"square"`.
- Cache: **3 MB** per image hard cap (abort mid-stream), **3 s** total fetch timeout, `Content-Type` must start with `image/`, files named hex SHA-256 of the URL, max **50** files (evict oldest mtime). Cache hit → no network.
- Wire compatibility: schema-1.0 events byte-identical behavior; ack payloads/statuses/subjects unchanged; §7 text/button budget unchanged (appLogoOverride is a separate slot). All existing tests must pass — the only permitted edits to existing test code are adding `image: None` to `InboundNotification`/`ToastRequest` struct literals and new test cases.
- Batched toasts: image = **latest (highest-seq) event's image, strictly** — if the latest event has no image the toast has none.
- Work on branch `rust-agent` in worktree `/home/cjamhe01385/os-notification/.worktrees/rust-agent`. `export PATH="$HOME/.cargo/bin:$PATH"` every shell; cargo runs from `rust/`. NATS lives on localhost:4222 (not ours to manage). .NET SDK: `export PATH="$HOME/.dotnet:$PATH"`.

## File Structure

```
rust/notify-agent-core/src/model.rs        # Task 1: ImageShape, ImageRef, InboundNotification.image
rust/notify-agent-core/src/parser.rs       # Task 1: WireImage + parse_image validation
rust/notify-agent-core/src/toast.rs        # Task 2: ToastRequest.image + factory threading
rust/notify-agent-core/src/identity.rs     # Task 2: sign-in ToastRequest literal gains image: None
rust/notify-agent-console/src/main.rs      # Task 2: [image] echo line
rust/notify-agent-core/src/image_cache.rs  # Task 3: ImageCache (new)
rust/notify-agent-core/src/toast_xml.rs    # Task 4: pure XML builder (new, Linux-testable)
rust/notify-agent-windows/src/main.rs      # Task 4: use toast_xml + ImageCache
rust/notify-agent-core/Cargo.toml          # Task 3: sha2, reqwest "stream"
tools/TestPublisher/Program.cs             # Task 5: optional [imageUrl] arg (C# dev tool)
```

---

### Task 1: ImageRef model and parser validation

**Files:**
- Modify: `rust/notify-agent-core/src/model.rs`, `rust/notify-agent-core/src/parser.rs`
- Modify (mechanical): `rust/notify-agent-core/src/toast.rs` — the `tests::event` helper's `InboundNotification` literal gains `image: None`

**Interfaces:**
- Produces: `enum ImageShape { Circle, Square }` (derives `Debug, Clone, Copy, PartialEq, Eq`); `struct ImageRef { pub url: String, pub shape: ImageShape }` (derives `Debug, Clone, PartialEq`); `InboundNotification` gains `pub image: Option<ImageRef>` (insert after `action_url`); `parser::MAX_IMAGE_URL_BYTES = 2048`.

- [ ] **Step 1: Write the failing tests** — append inside `parser.rs`'s existing `mod tests`:

```rust
    #[test]
    fn parses_image_with_default_circle_shape() {
        let json = br#"{"eventId":"e1","target":{"userId":"u1"},
            "content":{"title":"t","message":"m",
                       "image":{"url":"https://cdn.example.com/a.jpg"}}}"#;
        let n = parse_event(json, received_at(), 1).unwrap();
        let img = n.image.expect("image present");
        assert_eq!(img.url, "https://cdn.example.com/a.jpg");
        assert_eq!(img.shape, crate::model::ImageShape::Circle);
    }

    #[test]
    fn parses_square_shape_and_defaults_unknown_to_circle() {
        for (shape_json, expected) in [
            ("square", crate::model::ImageShape::Square),
            ("SQUARE", crate::model::ImageShape::Square),
            ("hexagon", crate::model::ImageShape::Circle),
        ] {
            let json = format!(
                r#"{{"eventId":"e1","target":{{"userId":"u1"}},
                     "content":{{"title":"t","message":"m",
                                 "image":{{"url":"https://x.example/a.png","shape":"{shape_json}"}}}}}}"#
            );
            assert_eq!(parse_event(json.as_bytes(), received_at(), 1).unwrap().image.unwrap().shape, expected);
        }
    }

    #[test]
    fn absent_image_is_none_and_schema_10_unchanged() {
        let n = parse_event(DOC_EXAMPLE.as_bytes(), received_at(), 1).unwrap();
        assert_eq!(n.image, None);
    }

    #[test]
    fn invalid_image_drops_image_not_event() {
        for bad in [
            r#"{"url":"http://insecure.example/a.jpg"}"#,          // wrong scheme
            r#"{"url":""}"#,                                        // blank
            r#"{"shape":"circle"}"#,                                // no url
        ] {
            let json = format!(
                r#"{{"eventId":"e1","target":{{"userId":"u1"}},
                     "content":{{"title":"t","message":"m","image":{bad}}}}}"#
            );
            let n = parse_event(json.as_bytes(), received_at(), 1).unwrap(); // event OK
            assert_eq!(n.image, None, "case: {bad}");
        }
    }

    #[test]
    fn oversize_image_url_drops_image_not_event() {
        let url = format!("https://x.example/{}", "a".repeat(MAX_IMAGE_URL_BYTES));
        let json = format!(
            r#"{{"eventId":"e1","target":{{"userId":"u1"}},
                 "content":{{"title":"t","message":"m","image":{{"url":"{url}"}}}}}}"#
        );
        let n = parse_event(json.as_bytes(), received_at(), 1).unwrap();
        assert_eq!(n.image, None);
    }
```

- [ ] **Step 2: Run to verify red** — `cd rust && cargo test -p notify-agent-core --lib 2>&1 | tail -5` → compile FAILURE (`image` field / `ImageShape` unknown).

- [ ] **Step 3: Implement.** In `model.rs`, add above `InboundNotification`:

```rust
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ImageShape {
    Circle,
    Square,
}

/// Optional toast image (design 2026-07-22): rendered in the appLogoOverride
/// slot by the Windows head, echoed by the console head.
#[derive(Debug, Clone, PartialEq)]
pub struct ImageRef {
    pub url: String,
    pub shape: ImageShape,
}
```

and insert `pub image: Option<ImageRef>,` into `InboundNotification` directly after `pub action_url: Option<String>,`.

In `parser.rs`: add `pub const MAX_IMAGE_URL_BYTES: usize = 2048;` next to the other consts; extend `WireContent` with `image: Option<WireImage>` and add:

```rust
#[derive(Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
struct WireImage {
    url: Option<String>,
    shape: Option<String>,
}

/// Best-effort: any invalid image spec yields None (the event is unaffected).
fn parse_image(wire: Option<WireImage>) -> Option<crate::model::ImageRef> {
    let wire = wire?;
    let url = wire.url.filter(|u| !u.trim().is_empty())?;
    let Some(url) = crate::action_url::validate(&url) else {
        tracing::debug!("dropping invalid image url");
        return None;
    }
    let shape = match wire.shape.as_deref().map(str::to_lowercase).as_deref() {
        Some("square") => crate::model::ImageShape::Square,
        _ => crate::model::ImageShape::Circle,
    };
    Some(crate::model::ImageRef { url: url.into(), shape })
}
```

In `parse_event`, capture the image before `content` fields are consumed (order matters — take `content.image` out first):

```rust
    let mut content = wire.content.unwrap_or_default();
    let image = parse_image(content.image.take());
```

(change `let content` to `let mut content`; requires `WireContent.image` to be a plain field so `.take()` works) and add `image,` to the `InboundNotification` literal after `action_url`. Add `image: None,` to the `tests::event` helper literal in `toast.rs`.

- [ ] **Step 4: Run to verify green** — `cargo test -p notify-agent-core --lib 2>&1 | tail -3` → `44 passed`, zero warnings.

- [ ] **Step 5: Commit** — `git add rust/ && git commit -m "feat(rust): schema-1.1 content.image parsing with drop-image-not-event validation"`

---

### Task 2: Toast factory threading and console echo

**Files:**
- Modify: `rust/notify-agent-core/src/toast.rs` (ToastRequest field + factory + tests), `rust/notify-agent-core/src/identity.rs` (sign-in toast literal), `rust/notify-agent-console/src/main.rs` (echo line)

**Interfaces:**
- Consumes: `ImageRef`/`ImageShape` (Task 1).
- Produces: `ToastRequest` gains `pub image: Option<ImageRef>` (insert after `action_url`); `from_single`/`from_batch` thread it (batch: strictly the latest-by-seq event's image).

- [ ] **Step 1: Write the failing tests** — append inside `toast.rs`'s `tests`:

```rust
    #[test]
    fn single_event_threads_image() {
        let mut n = event(1, "e1", "m");
        n.image = Some(crate::model::ImageRef {
            url: "https://x.example/a.jpg".into(),
            shape: crate::model::ImageShape::Circle,
        });
        assert_eq!(from_single(&n).image, n.image);
    }

    #[test]
    fn batch_takes_latest_events_image_strictly() {
        let mut older = event(1, "e1", "first");
        older.image = Some(crate::model::ImageRef {
            url: "https://x.example/old.jpg".into(),
            shape: crate::model::ImageShape::Square,
        });
        let mut latest = event(2, "e2", "second");

        // latest has no image → toast has none (no scavenging)
        assert_eq!(from_batch(&[older.clone(), latest.clone()]).image, None);

        // latest has one → toast carries exactly it
        latest.image = Some(crate::model::ImageRef {
            url: "https://x.example/new.jpg".into(),
            shape: crate::model::ImageShape::Circle,
        });
        assert_eq!(from_batch(&[older, latest.clone()]).image, latest.image);
    }
```

- [ ] **Step 2: Run to verify red** — compile FAILURE (`image` field unknown on `ToastRequest`).

- [ ] **Step 3: Implement.** Add `pub image: Option<ImageRef>,` to `ToastRequest` after `action_url` (import `ImageRef` in `toast.rs`'s use of `crate::model`). In `from_single`: `image: n.image.clone(),`. In `from_batch`: `image: latest.image.clone(),`. In `identity.rs`'s sign-in `ToastRequest` literal: add `image: None,`. In `notify-agent-console/src/main.rs`, after the attribution line print:

```rust
        if let Some(image) = &toast.image {
            let shape = match image.shape {
                notify_agent_core::model::ImageShape::Circle => "circle",
                notify_agent_core::model::ImageShape::Square => "square",
            };
            println!("        [image] {} ({shape})", image.url);
        }
```

- [ ] **Step 4: Run to verify green** — `cargo build && cargo test 2>&1 | tail -3` from `rust/` → workspace builds, `46 passed` lib tests (+1 integration if NATS up), zero warnings.

- [ ] **Step 5: Commit** — `git add rust/ && git commit -m "feat(rust): thread toast image through factory and console echo (latest-wins)"`

---

### Task 3: ImageCache

**Files:**
- Create: `rust/notify-agent-core/src/image_cache.rs`
- Modify: `rust/notify-agent-core/src/lib.rs` (`pub mod image_cache;`), `rust/notify-agent-core/Cargo.toml` (add `sha2 = "0.10"`; add `"stream"` to reqwest features)

**Interfaces:**
- Produces: `struct ImageCacheOptions { pub require_https: bool, pub max_bytes: u64, pub timeout: Duration, pub max_files: usize }` (`Default` = true / 3 * 1024 * 1024 / 3s / 50, derives `Clone`); `ImageCache::new(dir: PathBuf) -> Self` (default options), `ImageCache::with_options(dir: PathBuf, options: ImageCacheOptions) -> Self`, `async fn fetch(&self, url: &str) -> Option<PathBuf>`.

- [ ] **Step 1: Write the failing tests** — create `image_cache.rs` with only the test module. The loopback helper serves ONE canned HTTP response then closes; tests construct the cache with `require_https: false` (production default stays true — one test asserts the https gate):

```rust
#[cfg(test)]
mod tests {
    use super::*;
    use std::time::Duration;
    use tokio::io::{AsyncReadExt, AsyncWriteExt};

    /// Minimal one-shot HTTP server: accepts one connection, reads the request,
    /// writes `response` (after `delay`), closes. Returns the URL to hit.
    async fn serve_once(response: Vec<u8>, delay: Duration) -> String {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        tokio::spawn(async move {
            if let Ok((mut sock, _)) = listener.accept().await {
                let mut buf = [0u8; 1024];
                let _ = sock.read(&mut buf).await;
                tokio::time::sleep(delay).await;
                let _ = sock.write_all(&response).await;
            }
        });
        format!("http://{addr}/img.png")
    }

    fn http_response(content_type: &str, body: &[u8]) -> Vec<u8> {
        let mut r = format!(
            "HTTP/1.1 200 OK\r\ncontent-type: {content_type}\r\ncontent-length: {}\r\nconnection: close\r\n\r\n",
            body.len()
        )
        .into_bytes();
        r.extend_from_slice(body);
        r
    }

    fn test_cache(dir: &std::path::Path) -> ImageCache {
        ImageCache::with_options(
            dir.to_path_buf(),
            ImageCacheOptions { require_https: false, ..Default::default() },
        )
    }

    #[tokio::test]
    async fn downloads_then_reuses_cache_without_network() {
        let dir = tempdir();
        let cache = test_cache(&dir);
        let url = serve_once(http_response("image/png", b"PNGDATA"), Duration::ZERO).await;

        let first = cache.fetch(&url).await.expect("first fetch succeeds");
        assert_eq!(std::fs::read(&first).unwrap(), b"PNGDATA");
        // Server is gone (one-shot); a second fetch must still succeed from cache.
        let second = cache.fetch(&url).await.expect("cache hit needs no network");
        assert_eq!(first, second);
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[tokio::test]
    async fn rejects_http_scheme_when_required() {
        let dir = tempdir();
        let cache = ImageCache::new(dir.clone()); // production defaults: require_https = true
        assert_eq!(cache.fetch("http://127.0.0.1:1/x.png").await, None);
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[tokio::test]
    async fn aborts_when_body_exceeds_cap() {
        let dir = tempdir();
        let cache = ImageCache::with_options(
            dir.clone(),
            ImageCacheOptions { require_https: false, max_bytes: 16, ..Default::default() },
        );
        let url = serve_once(http_response("image/png", &[0u8; 64]), Duration::ZERO).await;
        assert_eq!(cache.fetch(&url).await, None);
        assert_eq!(std::fs::read_dir(&dir).unwrap().count(), 0, "no partial file left");
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[tokio::test]
    async fn times_out_on_slow_server() {
        let dir = tempdir();
        let cache = ImageCache::with_options(
            dir.clone(),
            ImageCacheOptions { require_https: false, timeout: Duration::from_millis(200), ..Default::default() },
        );
        let url = serve_once(http_response("image/png", b"late"), Duration::from_secs(5)).await;
        assert_eq!(cache.fetch(&url).await, None);
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[tokio::test]
    async fn rejects_non_image_content_type() {
        let dir = tempdir();
        let cache = test_cache(&dir);
        let url = serve_once(http_response("text/html", b"<html>"), Duration::ZERO).await;
        assert_eq!(cache.fetch(&url).await, None);
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[tokio::test]
    async fn evicts_oldest_beyond_max_files() {
        let dir = tempdir();
        std::fs::create_dir_all(&dir).unwrap();
        for i in 0..3 {
            let p = dir.join(format!("old{i}"));
            std::fs::write(&p, b"x").unwrap();
        }
        let cache = ImageCache::with_options(
            dir.clone(),
            ImageCacheOptions { require_https: false, max_files: 3, ..Default::default() },
        );
        let url = serve_once(http_response("image/jpeg", b"JPG"), Duration::ZERO).await;
        cache.fetch(&url).await.expect("fetch succeeds");
        assert_eq!(std::fs::read_dir(&dir).unwrap().count(), 3, "evicted down to max_files");
        let _ = std::fs::remove_dir_all(&dir);
    }

    fn tempdir() -> std::path::PathBuf {
        let dir = std::env::temp_dir().join(format!(
            "img-cache-test-{}-{:x}",
            std::process::id(),
            std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap().as_nanos()
        ));
        std::fs::create_dir_all(&dir).unwrap();
        dir
    }
}
```

- [ ] **Step 2: Run to verify red** — compile FAILURE (`ImageCache` unknown). First add `pub mod image_cache;` to `lib.rs` and the deps: in `Cargo.toml`, `sha2 = "0.10"` and reqwest features `["rustls-tls", "json", "stream"]`.

- [ ] **Step 3: Implement** — prepend to `image_cache.rs`:

```rust
use std::path::{Path, PathBuf};
use std::time::Duration;

use futures::StreamExt;
use sha2::{Digest, Sha256};

/// Best-effort local image cache for toast rendering (design 2026-07-22).
/// Every failure path returns None: the toast renders without the image.
#[derive(Clone)]
pub struct ImageCacheOptions {
    pub require_https: bool,
    pub max_bytes: u64,
    pub timeout: Duration,
    pub max_files: usize,
}

impl Default for ImageCacheOptions {
    fn default() -> Self {
        Self {
            require_https: true,
            max_bytes: 3 * 1024 * 1024, // spec: 3 MB hard cap
            timeout: Duration::from_secs(3),
            max_files: 50,
        }
    }
}

pub struct ImageCache {
    dir: PathBuf,
    options: ImageCacheOptions,
    http: reqwest::Client,
}

impl ImageCache {
    pub fn new(dir: PathBuf) -> Self {
        Self::with_options(dir, ImageCacheOptions::default())
    }

    pub fn with_options(dir: PathBuf, options: ImageCacheOptions) -> Self {
        Self {
            dir,
            options,
            http: reqwest::Client::builder()
                .redirect(reqwest::redirect::Policy::none())
                .build()
                .expect("image cache HTTP client configuration is valid"),
        }
    }

    pub async fn fetch(&self, url: &str) -> Option<PathBuf> {
        if self.options.require_https && crate::action_url::validate(url).is_none() {
            tracing::debug!("image url rejected: https required");
            return None;
        }
        let path = self.dir.join(hex_sha256(url));
        match tokio::time::timeout(self.options.timeout, async {
            if tokio::fs::try_exists(&path).await? { return Ok(path.clone()); }
            self.download(url, &path).await?;
            self.evict_beyond_cap().await;
            Ok(path.clone())
        }).await {
            Ok(Ok(path)) => Some(path),
            Ok(Err(e)) => {
                tracing::debug!(host = ?url::Url::parse(url).ok().and_then(|u| u.host_str().map(str::to_owned)), error = %e, "image fetch failed");
                None
            }
            Err(_) => {
                tracing::debug!(host = ?url::Url::parse(url).ok().and_then(|u| u.host_str().map(str::to_owned)), "image fetch timed out");
                None
            }
        }
    }

    async fn download(&self, url: &str, path: &Path) -> anyhow::Result<()> {
        let resp = self.http.get(url).send().await?.error_for_status()?;
        let content_type = resp
            .headers()
            .get(reqwest::header::CONTENT_TYPE)
            .and_then(|v| v.to_str().ok())
            .unwrap_or("");
        anyhow::ensure!(content_type.starts_with("image/"), "not an image: {content_type}");

        let mut body: Vec<u8> = Vec::new();
        let mut stream = resp.bytes_stream();
        while let Some(chunk) = stream.next().await {
            let chunk = chunk?;
            anyhow::ensure!(
                (body.len() + chunk.len()) as u64 <= self.options.max_bytes,
                "image exceeds {} bytes",
                self.options.max_bytes
            );
            body.extend_from_slice(&chunk);
        }

        std::fs::create_dir_all(&self.dir)?;
        let tmp = path.with_extension("part");
        std::fs::write(&tmp, &body)?;
        std::fs::rename(&tmp, path)?;
        Ok(())
    }

    /// Keep at most max_files entries; delete oldest by mtime. Best-effort.
    fn evict_beyond_cap(&self) {
        let Ok(entries) = std::fs::read_dir(&self.dir) else { return };
        let mut files: Vec<(std::time::SystemTime, PathBuf)> = entries
            .flatten()
            .filter_map(|e| {
                let meta = e.metadata().ok()?;
                meta.is_file().then(|| (meta.modified().ok().unwrap_or(std::time::UNIX_EPOCH), e.path()))
            })
            .collect();
        if files.len() <= self.options.max_files {
            return;
        }
        files.sort_by_key(|(mtime, _)| *mtime);
        for (_, path) in files.iter().take(files.len() - self.options.max_files) {
            let _ = std::fs::remove_file(path);
        }
    }
}

fn hex_sha256(input: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(input.as_bytes());
    format!("{:x}", hasher.finalize())
}
```

- [ ] **Step 4: Run to verify green** — `cargo test -p notify-agent-core --lib 2>&1 | tail -3` → `52 passed`, zero warnings.

- [ ] **Step 5: Commit** — `git add rust/ && git commit -m "feat(rust): best-effort image cache (https-only, 3MB/3s caps, sha256-keyed, 50-file eviction)"`

---

### Task 4: Pure toast-XML builder and Windows head wiring

**Files:**
- Create: `rust/notify-agent-core/src/toast_xml.rs`
- Modify: `rust/notify-agent-core/src/lib.rs` (`pub mod toast_xml;`), `rust/notify-agent-windows/src/main.rs` (use the shared builder + ImageCache; delete its private `xml_escape` and inline XML)

**Interfaces:**
- Produces: `toast_xml::build_toast_xml(toast: &ToastRequest, image_path: Option<&Path>) -> String`; `toast_xml::xml_escape(s: &str) -> String` (moved from the Windows head so it's Linux-tested).

- [ ] **Step 1: Write the failing tests** — create `toast_xml.rs` with only:

```rust
#[cfg(test)]
mod tests {
    use super::*;
    use crate::model::{ImageRef, ImageShape};
    use crate::toast::ToastRequest;

    fn toast(image: Option<ImageRef>) -> ToastRequest {
        ToastRequest {
            title: "Tony Redmond".into(),
            message: "is now available".into(),
            attribution: Some("Microsoft Teams".into()),
            action_label: Some("Open <chat>".into()),
            action_url: Some("https://teams.example/chat?a=1&b=2".into()),
            sources: Vec::new(),
            image,
        }
    }

    #[test]
    fn no_image_builds_todays_xml() {
        let xml = build_toast_xml(&toast(None), None);
        assert!(xml.starts_with("<toast><visual><binding template=\"ToastGeneric\">"));
        assert!(xml.contains("<text>Tony Redmond</text>"));
        assert!(!xml.contains("<image"));
        // escaping in action attributes
        assert!(xml.contains("Open &lt;chat&gt;"));
        assert!(xml.contains("a=1&amp;b=2"));
    }

    #[test]
    fn circle_image_gets_applogo_with_crop() {
        let image = ImageRef { url: "https://x/a.jpg".into(), shape: ImageShape::Circle };
        let xml = build_toast_xml(&toast(Some(image)), Some(std::path::Path::new("/tmp/cache/abc123")));
        assert!(xml.contains(r#"<image placement="appLogoOverride" hint-crop="circle" src="file:///tmp/cache/abc123"/>"#));
    }

    #[test]
    fn square_image_omits_crop_attribute() {
        let image = ImageRef { url: "https://x/a.jpg".into(), shape: ImageShape::Square };
        let xml = build_toast_xml(&toast(Some(image)), Some(std::path::Path::new("/tmp/cache/abc123")));
        assert!(xml.contains(r#"<image placement="appLogoOverride" src="file:///tmp/cache/abc123"/>"#));
        assert!(!xml.contains("hint-crop"));
    }

    #[test]
    fn image_ref_without_local_path_renders_imageless() {
        let image = ImageRef { url: "https://x/a.jpg".into(), shape: ImageShape::Circle };
        let xml = build_toast_xml(&toast(Some(image)), None); // fetch failed
        assert!(!xml.contains("<image"));
    }

    #[test]
    fn windows_backslash_paths_become_forward_slash_file_uris() {
        let image = ImageRef { url: "https://x/a.jpg".into(), shape: ImageShape::Square };
        let xml = build_toast_xml(
            &toast(Some(image)),
            Some(std::path::Path::new(r"C:\Users\u\AppData\Local\DesktopNotificationAgent\image-cache\abc")),
        );
        assert!(xml.contains(r#"src="file:///C:/Users/u/AppData/Local/DesktopNotificationAgent/image-cache/abc"/>"#));
    }
}
```

- [ ] **Step 2: Run to verify red** — compile FAILURE (`build_toast_xml` unknown; add `pub mod toast_xml;` to lib.rs first).

- [ ] **Step 3: Implement** — prepend to `toast_xml.rs`:

```rust
use std::path::Path;

use crate::model::ImageShape;
use crate::toast::ToastRequest;

pub fn xml_escape(s: &str) -> String {
    s.replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
        .replace('\'', "&apos;")
}

/// file:/// URI with forward slashes; a leading '/' (unix paths) is not doubled.
fn file_uri(path: &Path) -> String {
    let p = path.display().to_string().replace('\\', "/");
    let p = p.strip_prefix('/').unwrap_or(&p);
    format!("file:///{p}")
}

/// Toast XML per design §7 budget: ≤3 texts, 1 button, plus the separate
/// appLogoOverride image slot (design 2026-07-22). image_path is the LOCAL
/// cached file — the OS ignores remote URLs for unpackaged apps, so a failed
/// fetch (None) renders imageless.
pub fn build_toast_xml(toast: &ToastRequest, image_path: Option<&Path>) -> String {
    let image = match (image_path, &toast.image) {
        (Some(path), Some(image_ref)) => {
            let crop = match image_ref.shape {
                ImageShape::Circle => r#" hint-crop="circle""#,
                ImageShape::Square => "",
            };
            format!(
                r#"<image placement="appLogoOverride"{crop} src="{}"/>"#,
                xml_escape(&file_uri(path))
            )
        }
        _ => String::new(),
    };
    let attribution = toast
        .attribution
        .as_deref()
        .map(|a| format!(r#"<text placement="attribution">{}</text>"#, xml_escape(a)))
        .unwrap_or_default();
    let actions = match (&toast.action_label, &toast.action_url) {
        (Some(label), Some(url)) => format!(
            r#"<actions><action content="{}" arguments="{}" activationType="foreground"/></actions>"#,
            xml_escape(label),
            xml_escape(url)
        ),
        _ => String::new(),
    };
    format!(
        r#"<toast><visual><binding template="ToastGeneric">{image}<text>{}</text><text>{}</text>{attribution}</binding></visual>{actions}</toast>"#,
        xml_escape(&toast.title),
        xml_escape(&toast.message)
    )
}
```

Then rewire `rust/notify-agent-windows/src/main.rs` (Windows side only; the stub main is untouched): delete the private `xml_escape` and the inline XML construction in `WindowsToastRenderer::show`; the renderer becomes:

```rust
    pub struct WindowsToastRenderer {
        cache: notify_agent_core::image_cache::ImageCache,
    }

    impl WindowsToastRenderer {
        pub fn new() -> anyhow::Result<Self> {
            let dir = std::path::PathBuf::from(std::env::var("LOCALAPPDATA")?)
                .join("DesktopNotificationAgent")
                .join("image-cache");
            Ok(Self { cache: notify_agent_core::image_cache::ImageCache::new(dir) })
        }
    }

    #[async_trait]
    impl ToastRenderer for WindowsToastRenderer {
        async fn show(&self, toast: &ToastRequest) -> anyhow::Result<DateTime<Utc>> {
            let image_path = match &toast.image {
                Some(image_ref) => self.cache.fetch(&image_ref.url).await, // ≤3s, best-effort
                None => None,
            };
            let xml = notify_agent_core::toast_xml::build_toast_xml(toast, image_path.as_deref());

            let doc = XmlDocument::new()?;
            doc.LoadXml(&HSTRING::from(xml))?;
            // ... existing ToastNotification creation, Activated handler, and Show(...) unchanged ...
        }
    }
```

and the construction site changes from `Arc::new(WindowsToastRenderer)` to `Arc::new(WindowsToastRenderer::new()?)`. Everything else in the file (mutex, AUMID, identity selection, Activated handler) stays as-is.

- [ ] **Step 4: Verify** — `cargo build && cargo test 2>&1 | tail -3` (Linux green, `57 passed` lib), then `cargo build --release --target x86_64-pc-windows-gnu -p notify-agent-windows 2>&1 | tail -2` (cross-compile still links).

- [ ] **Step 5: Commit** — `git add rust/ && git commit -m "feat(rust): shared toast XML builder with appLogoOverride image; Windows head uses ImageCache"`

---

### Task 5: TestPublisher imageUrl argument and e2e smoke

**Files:**
- Modify: `tools/TestPublisher/Program.cs` (optional 6th arg; conditional `content.image`; schemaVersion stamp)

**Interfaces:**
- Consumes: existing TestPublisher CLI (`<userId> [title] [message] [priority] [count]`).
- Produces: extended CLI `<userId> [title] [message] [priority] [count] [imageUrl]`; events carry `content.image = { url, shape: "circle" }` and `schemaVersion: "1.1"` only when the arg is given.

- [ ] **Step 1: Extend TestPublisher.** In `tools/TestPublisher/Program.cs`, after the `count` arg parsing add `var imageUrl = args.Length > 5 ? args[5] : null;`, update the usage comment to `-- <userId> [title] [message] [priority] [count] [imageUrl]`, and replace the anonymous `content` member so the payload builds with a conditional image (Dictionary keeps the JSON shape additive):

```csharp
    var content = new Dictionary<string, object>
    {
        ["title"] = title,
        ["message"] = message,
        ["secondaryText"] = "TestPublisher",
    };
    if (imageUrl is not null)
        content["image"] = new { url = imageUrl, shape = "circle" };

    var payload = new
    {
        schemaVersion = imageUrl is not null ? "1.1" : "1.0",
        eventId,
        notificationType = "billing.invoice.ready",
        target = new { userId },
        content,
        // ... action / classification / timestamps unchanged ...
    };
```

Build check: `export PATH="$HOME/.dotnet:$PATH" && dotnet build tools/TestPublisher 2>&1 | tail -3` (run from the worktree root; expect 0 warnings/0 errors). Note: this edits the C# dev tool on the `rust-agent` branch — the C# agent itself is untouched.

- [ ] **Step 2: E2E smoke with image (console head).**

```bash
cd /home/cjamhe01385/os-notification/.worktrees/rust-agent/rust
SMOKELOG=$(mktemp /tmp/image-smoke.XXXX.log)
NOTIFY_USER_ID=u_imgdemo cargo run -p notify-agent-console > "$SMOKELOG" 2>&1 &
AGENT_PID=$!
sleep 3
export PATH="$HOME/.dotnet:$PATH"
dotnet run --project ../tools/TestPublisher -- u_imgdemo "Tony Redmond" "is now available" critical 1 "https://example.com/avatars/tony.jpg"
kill -INT $AGENT_PID; sleep 2
cat "$SMOKELOG"
```

Expected in the log: the `[TOAST] Tony Redmond` block including `        [image] https://example.com/avatars/tony.jpg (circle)`, plus both acks in the publisher output. Then re-run WITHOUT the imageUrl arg and confirm the output has no `[image]` line (schema-1.0 behavior unchanged).

- [ ] **Step 3: Full verification sweep.**

```bash
cd rust && cargo build && cargo test 2>&1 | tail -3            # 57 lib + 1 integration, zero warnings
cargo build --release --target x86_64-pc-windows-gnu -p notify-agent-windows 2>&1 | tail -2
```

- [ ] **Step 4: Commit** — `git add tools/TestPublisher && git commit -m "feat(tools): TestPublisher optional imageUrl argument (schema 1.1)"`

---

## Spec coverage map (self-review record)

| Spec section | Where |
|---|---|
| Schema 1.1 `content.image` + validation + drop-image-not-event | Task 1 |
| Data flow (model → factory latest-wins → ToastRequest) | Tasks 1–2 |
| ImageCache (https/3MB/3s/content-type/sha256/50-file eviction, testable options) | Task 3 |
| Rendering (appLogoOverride + crop, file URI, imageless fallback; console echo) | Tasks 4, 2 |
| TestPublisher arg + e2e smoke + 1.0 regression | Task 5 |
| Out of scope (C# agent, multi-button, hero images, auth URLs, TTL) | untouched |
