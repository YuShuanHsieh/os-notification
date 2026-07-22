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
        Self { dir, options, http: reqwest::Client::new() }
    }

    pub async fn fetch(&self, url: &str) -> Option<PathBuf> {
        if self.options.require_https && !url.starts_with("https://") {
            tracing::debug!(url, "image url rejected: https required");
            return None;
        }
        let path = self.dir.join(hex_sha256(url));
        if path.exists() {
            return Some(path);
        }
        match tokio::time::timeout(self.options.timeout, self.download(url, &path)).await {
            Ok(Ok(())) => {
                self.evict_beyond_cap();
                Some(path)
            }
            Ok(Err(e)) => {
                tracing::debug!(url, error = %e, "image fetch failed");
                None
            }
            Err(_) => {
                tracing::debug!(url, "image fetch timed out");
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
