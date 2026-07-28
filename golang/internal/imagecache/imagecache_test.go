package imagecache

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// httpsServer starts an httptest TLS server (so its URL passes the
// package's https-only policy) and returns it along with a client
// preconfigured to trust its self-signed certificate.
func httpsServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *http.Client) {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	client := srv.Client()
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return srv, client
}

func TestFetch_DownloadsThenReusesCacheWithoutNetwork(t *testing.T) {
	var hits int
	srv, client := httpsServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("PNGDATA"))
	})

	dir := t.TempDir()
	cache := NewWithOptions(dir, Options{HTTPClient: client})

	first, ok := cache.Fetch(context.Background(), srv.URL+"/img.png")
	if !ok {
		t.Fatal("expected first fetch to succeed")
	}
	data, err := os.ReadFile(first)
	if err != nil || string(data) != "PNGDATA" {
		t.Fatalf("unexpected cached content: %q, err=%v", data, err)
	}

	second, ok := cache.Fetch(context.Background(), srv.URL+"/img.png")
	if !ok {
		t.Fatal("expected cache-hit fetch to succeed")
	}
	if first != second {
		t.Fatalf("expected same cached path, got %q vs %q", first, second)
	}
	if hits != 1 {
		t.Fatalf("expected exactly one network hit, got %d", hits)
	}
}

func TestFetch_RejectsHTTPScheme(t *testing.T) {
	dir := t.TempDir()
	cache := New(dir)

	if _, ok := cache.Fetch(context.Background(), "http://127.0.0.1:1/x.png"); ok {
		t.Fatal("expected non-https URL to be rejected")
	}
}

func TestFetch_RejectsUserinfoInURL(t *testing.T) {
	dir := t.TempDir()
	cache := New(dir)

	if _, ok := cache.Fetch(context.Background(), "https://user:pass@example.com/x.png"); ok {
		t.Fatal("expected userinfo URL to be rejected")
	}
}

func TestFetch_AbortsWhenBodyExceedsCap(t *testing.T) {
	srv, client := httpsServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(make([]byte, 64))
	})

	dir := t.TempDir()
	cache := NewWithOptions(dir, Options{MaxBytes: 16, HTTPClient: client})

	if _, ok := cache.Fetch(context.Background(), srv.URL+"/img.png"); ok {
		t.Fatal("expected oversize body to be rejected")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no partial file left, got %v", entries)
	}
}

func TestFetch_TimesOutOnSlowServer(t *testing.T) {
	srv, client := httpsServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("late"))
	})

	dir := t.TempDir()
	cache := NewWithOptions(dir, Options{Timeout: 100 * time.Millisecond, HTTPClient: client})

	if _, ok := cache.Fetch(context.Background(), srv.URL+"/img.png"); ok {
		t.Fatal("expected slow server to time out")
	}
}

func TestFetch_RejectsNonImageContentType(t *testing.T) {
	srv, client := httpsServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>"))
	})

	dir := t.TempDir()
	cache := NewWithOptions(dir, Options{HTTPClient: client})

	if _, ok := cache.Fetch(context.Background(), srv.URL+"/img.png"); ok {
		t.Fatal("expected non-image content type to be rejected")
	}
}

func TestFetch_RejectsNon2xxStatus(t *testing.T) {
	srv, client := httpsServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("nope"))
	})

	dir := t.TempDir()
	cache := NewWithOptions(dir, Options{HTTPClient: client})

	if _, ok := cache.Fetch(context.Background(), srv.URL+"/img.png"); ok {
		t.Fatal("expected 404 status to be rejected")
	}
}

func TestFetch_EvictsOldestBeyondMaxFiles(t *testing.T) {
	dir := t.TempDir()
	// Backdate the seed files well into the past (and in increasing order)
	// so the freshly downloaded file — timestamped with the real wall clock
	// at download time — is unambiguously the newest, and old0 is
	// unambiguously the oldest.
	base := time.Now().Add(-1 * time.Hour)
	for i := 0; i < 3; i++ {
		p := filepath.Join(dir, fmt.Sprintf("old%d", i))
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed file: %v", err)
		}
		mtime := base.Add(time.Duration(i) * time.Second)
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	srv, client := httpsServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("JPG"))
	})

	cache := NewWithOptions(dir, Options{MaxFiles: 3, HTTPClient: client})

	if _, ok := cache.Fetch(context.Background(), srv.URL+"/img.jpg"); !ok {
		t.Fatal("expected fetch to succeed")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected eviction down to max_files=3, got %d entries", len(entries))
	}
	if _, err := os.Stat(filepath.Join(dir, "old0")); !os.IsNotExist(err) {
		t.Fatalf("expected oldest file old0 to be evicted, stat err=%v", err)
	}
}

func TestFetch_RejectsInvalidURL(t *testing.T) {
	dir := t.TempDir()
	cache := New(dir)

	if _, ok := cache.Fetch(context.Background(), "not a url"); ok {
		t.Fatal("expected malformed URL to be rejected")
	}
	if _, ok := cache.Fetch(context.Background(), ""); ok {
		t.Fatal("expected empty URL to be rejected")
	}
}

// TestFetch_ConcurrentFetchesForSameURLDownloadOnce proves the singleflight
// fix: without per-key serialization, N goroutines racing on the same URL
// could all miss the os.Stat cache check and each independently download and
// write to the same path. This asserts the server is hit exactly once and
// every caller still gets a valid, identical result.
func TestFetch_ConcurrentFetchesForSameURLDownloadOnce(t *testing.T) {
	var hits int64
	srv, client := httpsServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		// Give concurrent callers a real window to race in.
		time.Sleep(20 * time.Millisecond)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("PNGDATA"))
	})

	dir := t.TempDir()
	cache := NewWithOptions(dir, Options{HTTPClient: client})

	const concurrency = 20
	var wg sync.WaitGroup
	results := make([]string, concurrency)
	oks := make([]bool, concurrency)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], oks[i] = cache.Fetch(context.Background(), srv.URL+"/img.png")
		}(i)
	}
	wg.Wait()

	for i := range oks {
		if !oks[i] {
			t.Fatalf("goroutine %d: Fetch failed", i)
		}
		if results[i] != results[0] {
			t.Fatalf("goroutine %d: path = %q, want same path as goroutine 0 (%q)", i, results[i], results[0])
		}
	}

	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("server hit %d times, want exactly 1 (concurrent fetches for the same URL must not each download independently)", got)
	}

	data, err := os.ReadFile(results[0])
	if err != nil || string(data) != "PNGDATA" {
		t.Fatalf("unexpected cached content: %q, err=%v", data, err)
	}
}
