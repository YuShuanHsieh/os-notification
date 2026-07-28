// Package imagecache implements a best-effort, bounded local disk cache for
// remote avatar images used by toast rendering, mirroring
// rust/notify-agent-core/src/image_cache.rs. Every failure path (bad scheme,
// oversize body, slow server, network error, non-image content type) returns
// a miss rather than an error: a single bad avatar URL must never fail the
// toast it is attached to (design 2026-07-22, ported from the Rust head).
//
// The cache directory is a constructor parameter rather than a hard-coded
// path, so this package is fully testable on any platform with t.TempDir()
// and httptest — the choice of the real Windows cache directory
// (%LOCALAPPDATA%\DesktopNotificationAgent\image-cache) lives in the
// Windows-specific main package, not here.
package imagecache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/YuShuanHsieh/os-notification/golang/internal/httpsurl"
)

// Options configures an ImageCache's limits. Zero-value Options is not
// valid for production use — call DefaultOptions and override selectively.
type Options struct {
	// MaxBytes is the hard cap on a downloaded image's body size. The
	// download is aborted (and treated as a failure) the instant the
	// cumulative body would exceed this, so the cache never buffers an
	// oversized body in memory or on disk.
	MaxBytes int64
	// Timeout bounds the whole fetch (cache-hit check + download), matching
	// the Rust reference's tokio::time::timeout wrapping.
	Timeout time.Duration
	// MaxFiles is the maximum number of files kept in the cache directory;
	// a successful download evicts the oldest-by-mtime files beyond this
	// bound. Best-effort: eviction failures are swallowed.
	MaxFiles int
	// HTTPClient, if non-nil, is used instead of the default client. Tests
	// use this to inject an httptest.Server's preconfigured TLS client.
	HTTPClient *http.Client
}

// DefaultOptions returns the production limits: 3 MB max body size, 3 second
// timeout, 50 files max — the same bounds as the Rust reference's
// ImageCacheOptions::default().
func DefaultOptions() Options {
	return Options{
		MaxBytes: 3 * 1024 * 1024,
		Timeout:  3 * time.Second,
		MaxFiles: 50,
	}
}

// Cache is a bounded, disk-backed cache of downloaded images keyed by the
// sha256 hex digest of their source URL.
type Cache struct {
	dir     string
	options Options
	client  *http.Client

	// group serializes concurrent Fetch calls for the same URL so two
	// goroutines racing on the same avatar can't both miss the cache-file
	// check and download/write the same path at once. Entries are removed
	// once the in-flight call completes, so this never grows unboundedly
	// the way a plain per-key mutex map would.
	group singleflight.Group
}

// New returns a Cache rooted at dir using DefaultOptions.
func New(dir string) *Cache {
	return NewWithOptions(dir, DefaultOptions())
}

// NewWithOptions returns a Cache rooted at dir with the given Options.
func NewWithOptions(dir string, options Options) *Cache {
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{
			// No redirects: an https URL that 3xx's to something else is
			// treated as a failure, matching the Rust client's
			// redirect::Policy::none().
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &Cache{dir: dir, options: options, client: client}
}

// Fetch resolves rawURL to a local file path, downloading and caching it if
// necessary. It returns ("", false) on ANY failure — invalid/non-https URL,
// oversize body, timeout, network error, non-2xx status, or a non-image
// content type — never an error, since an image is always optional on a
// toast.
func (c *Cache) Fetch(ctx context.Context, rawURL string) (string, bool) {
	if !httpsurl.Valid(rawURL) {
		return "", false
	}

	key := hexSHA256(rawURL)
	path := filepath.Join(c.dir, key)

	if _, err := os.Stat(path); err == nil {
		return path, true
	}

	// Concurrent Fetch calls for the same URL share one download: the
	// singleflight group ensures only the first caller's function runs
	// (and thus its ctx/timeout governs the shared download); every caller
	// waiting on the same key gets that call's result rather than each
	// racing to download/write the same path independently.
	v, err, _ := c.group.Do(key, func() (any, error) {
		// Re-check: another goroutine may have completed this exact fetch
		// while we were waiting to become the leader for this key.
		if _, statErr := os.Stat(path); statErr == nil {
			return path, nil
		}

		fetchCtx, cancel := context.WithTimeout(ctx, c.timeout())
		defer cancel()

		if dlErr := c.download(fetchCtx, rawURL, path); dlErr != nil {
			return "", dlErr
		}
		c.evictBeyondCap()
		return path, nil
	})
	if err != nil {
		return "", false
	}
	return v.(string), true
}

func (c *Cache) timeout() time.Duration {
	if c.options.Timeout <= 0 {
		return DefaultOptions().Timeout
	}
	return c.options.Timeout
}

func (c *Cache) maxBytes() int64 {
	if c.options.MaxBytes <= 0 {
		return DefaultOptions().MaxBytes
	}
	return c.options.MaxBytes
}

func (c *Cache) maxFiles() int {
	if c.options.MaxFiles <= 0 {
		return DefaultOptions().MaxFiles
	}
	return c.options.MaxFiles
}

// download fetches url and atomically writes it to path, never buffering
// more than maxBytes()+1 in memory: the body is streamed through a
// size-limited reader so an oversized image is rejected without ever
// completing the read.
func (c *Cache) download(ctx context.Context, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("imagecache: unexpected status %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if !isImageContentType(contentType) {
		return fmt.Errorf("imagecache: not an image: %q", contentType)
	}

	limit := c.maxBytes()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > limit {
		return fmt.Errorf("imagecache: image exceeds %d bytes", limit)
	}

	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(c.dir, ".imagecache-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

func isImageContentType(contentType string) bool {
	// Accept "image/png", "image/jpeg; charset=..." etc — any media type
	// beginning with "image/", matching the Rust reference's check.
	for i := 0; i < len(contentType); i++ {
		if contentType[i] == ';' {
			contentType = contentType[:i]
			break
		}
	}
	return len(contentType) > len("image/") && contentType[:len("image/")] == "image/"
}

// evictBeyondCap keeps at most maxFiles() entries in the cache directory,
// deleting the oldest-by-mtime files beyond that bound. Best-effort: any
// error reading the directory or removing a file is swallowed.
func (c *Cache) evictBeyondCap() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}

	type fileInfo struct {
		path    string
		modTime time.Time
	}
	var files []fileInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{
			path:    filepath.Join(c.dir, entry.Name()),
			modTime: info.ModTime(),
		})
	}

	max := c.maxFiles()
	if len(files) <= max {
		return
	}

	sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })
	for _, f := range files[:len(files)-max] {
		_ = os.Remove(f.path)
	}
}

func hexSHA256(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}
