# JSON-Driven Avatar/Image Toasts — Design

**Date:** 2026-07-22
**Status:** Approved (brainstorming session with user)
**Builds on:** the Rust agent (`rust/`, branch `rust-agent`, spec `2026-07-20-rust-notification-agent-design.md`). Reference example: a Teams-style toast — app header (OS chrome from AUMID), circular avatar photo at left (`appLogoOverride`, `hint-crop="circle"`), bold title line, status line.

## Goal

Let producers request richer Windows toasts through the JSON event alone: an optional image (avatar) rendered in the toast's app-logo slot. Modeled on the user's example image (person photo + "Tony Redmond / is now available").

User decisions:
1. **Scope: Rust heads only.** The Rust Windows head renders the image; the Rust console head echoes it. The C# agent stays on schema 1.0 and ignores the new field (System.Text.Json skips unknown members) — no C# agent changes. The C# `TestPublisher` dev tool gains an optional `[imageUrl]` argument (tooling, not agent).
2. **Image sourcing: download + cache, best-effort.** Unpackaged Win32 apps cannot render remote `http(s)` image URLs in toast XML — the OS silently drops them — so the agent downloads to a local cache and references `file:///` URIs. Failures degrade to an imageless toast; never delay past the fetch timeout, never drop an event over an image.

Approaches considered: (A) additive optional field + schemaVersion bump — chosen; (B) template registry keyed by notificationType — deferred, overkill for one image; (C) raw toast-XML passthrough — rejected (injection surface, bypasses §7 budget).

## Schema (additive, `schemaVersion: "1.1"`)

```json
"content": {
  "title": "Tony Redmond",
  "message": "is now available",
  "secondaryText": "Microsoft Teams",
  "image": { "url": "https://cdn.example.com/avatars/tony.jpg", "shape": "circle" }
}
```

- `content.image` optional object: `url` (required within the object), `shape` = `"circle"` (default) | `"square"` (unknown values → circle).
- Validation at parse: scheme must be `https`, URL length ≤ 2048. **An invalid image spec drops the image, not the event** (debug log). Absent field ≡ schema 1.0 event, byte-for-byte compatible.
- `schemaVersion` remains informational (the parser doesn't gate on it); producers using `image` should stamp `"1.1"`.
- No changes to: ack payload/statuses, subjects, priorities, dedup/aggregation keys, action contract (still 1 button), limits (32KB/depth-16 — an image object fits trivially).

## Data flow

`parser` → `InboundNotification.image: Option<ImageRef>` where `ImageRef { url: String, shape: ImageShape }`, `enum ImageShape { Circle, Square }` → aggregator untouched (buckets carry events) → `toast::from_single` copies the event's image; `toast::from_batch` takes the **latest** (highest-seq) event's image, consistent with existing latest-message/attribution/action semantics → `ToastRequest.image: Option<ImageRef>` → renderers.

## ImageCache (new module in `notify-agent-core`, Linux-testable)

`ImageCache::new(cache_dir: PathBuf) -> Self`; `async fn fetch(&self, url: &str) -> Option<PathBuf>`.

- https only (re-checked here — defense in depth with the parser).
- Streaming download with a **3 MB hard cap** (abort mid-stream when exceeded), **3 s total timeout**, response `Content-Type` must start with `image/`.
- Cache file name = hex SHA-256 of the URL (no extension needed for toast rendering; no path-traversal surface). Existing file → returned immediately, no network.
- Bound: after a successful write, if the directory holds > 50 files, delete oldest-by-mtime beyond 50. Best-effort eviction (errors ignored).
- Every failure path (scheme, timeout, cap, content-type, IO) returns `None` with a debug log.
- Windows head cache dir: `%LOCALAPPDATA%\DesktopNotificationAgent\image-cache`. The console head never instantiates the cache.
- Dependency note: sha2 crate added; reqwest already present.

## Rendering

- **Windows head:** if `ToastRequest.image` is set and `fetch` returns a path, prepend to the binding: `<image placement="appLogoOverride" hint-crop="circle" src="file:///{path-with-forward-slashes}"/>` (omit `hint-crop` for `Square`). On `None`: today's XML exactly. The appLogoOverride slot is separate from the ≤3-text/1-button §7 budget. The fetch happens inside `show()` before XML build (bounded by the 3s timeout — an acceptable, capped delay).
- **Console head:** prints `        [image] {url} ({shape})` after the attribution line. No download on Linux.

## Testing

- Parser: image present (both shapes + default), absent (1.0 event unchanged — existing doc-example test must still pass untouched), bad scheme / oversize URL → event parses with `image: None`.
- Toast factory: image threading in `from_single`; `from_batch` picks latest-by-seq image; batch where only an older event has an image (latest has none → toast has none — latest-wins is strict, no fallback scavenging).
- ImageCache unit tests against a local loopback HTTP listener spun up in-test (tokio TcpListener serving canned responses): success + reuse (second fetch hits no listener), size-cap abort, timeout, wrong content-type, http-scheme rejection, eviction beyond 50.
- Windows XML builder: extract the XML-building into a pure `fn build_toast_xml(toast, image_path: Option<&Path>) -> String` testable on Linux (asserts the image element, crop attr, escaping, and file URI form).
- E2E smoke: extended TestPublisher sends an event with an image URL → Rust console head prints the `[image]` line; schema-1.0 smoke unchanged.

## Out of scope

C# agent rendering; multiple buttons; hero/inline images (only the appLogoOverride slot); auth-protected image URLs; image caching TTL/refresh (cache is content-addressed by URL only — a changed avatar behind the same URL shows stale until eviction); action-center COM activation.

## Success criteria

1. `cargo test` green with the new parser/factory/cache/XML tests; all existing tests pass unmodified (except the factory test file gaining cases).
2. Live smoke: TestPublisher with an image URL → console head `[image]` line; without → output identical to today.
3. Windows 11 manual check (added to the existing deferred checklist): toast shows the circular avatar like the reference example; a dead image URL still shows a normal toast.
