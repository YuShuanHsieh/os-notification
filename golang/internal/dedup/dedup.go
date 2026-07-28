// Package dedup provides in-memory duplicate suppression, bounded by entry
// count and TTL (design §5 "Local state": bounded deduplication state). Not
// persistent — POC scope. Mirrors NotificationAgent.Core.Dedup.DeduplicationCache
// (C#) and notify_agent_core::dedup::DedupCache (Rust).
package dedup

import (
	"sync"
	"time"

	"github.com/YuShuanHsieh/os-notification/golang/internal/clock"
)

// entry pairs a key with the expiry time it was given at insertion. Kept in
// insertionOrder so that, combined with a fixed TTL and a monotonic clock,
// the queue stays expiry-ordered from front to back.
type entry struct {
	key    string
	expiry time.Time
}

// Cache is a bounded, TTL-based deduplication cache. It is safe for
// concurrent use by multiple goroutines.
type Cache struct {
	mu             sync.Mutex
	expiryByKey    map[string]time.Time
	insertionOrder []entry
	capacity       int
	ttl            time.Duration
	clk            clock.Clock
}

// NewCache returns a bounded, TTL-based dedup cache. capacity bounds the
// number of distinct keys retained (oldest/expired evicted first); ttl is how
// long a key continues to suppress duplicates after being added; clk is the
// time source.
func NewCache(capacity int, ttl time.Duration, clk clock.Clock) *Cache {
	if capacity < 1 {
		panic("dedup: capacity must be >= 1")
	}
	return &Cache{
		expiryByKey: make(map[string]time.Time, capacity),
		capacity:    capacity,
		ttl:         ttl,
		clk:         clk,
	}
}

// SeenOrAdd reports whether key was already present and not yet expired (a
// duplicate — the caller should drop the event without processing it
// further), or false if key is newly recorded (first-seen — caller proceeds
// normally). Safe for concurrent use from multiple goroutines with
// exactly-once "first seen" semantics per key even under a concurrent race.
func (c *Cache) SeenOrAdd(key string) bool {
	now := c.clk.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Purge expired entries from the front: fixed TTL + monotonic-ish clock
	// means the queue is expiry-ordered.
	for len(c.insertionOrder) > 0 && !c.insertionOrder[0].expiry.After(now) {
		c.dequeueOneLocked()
	}

	if existing, ok := c.expiryByKey[key]; ok && existing.After(now) {
		return true
	}

	for len(c.expiryByKey) >= c.capacity && len(c.insertionOrder) > 0 {
		c.dequeueOneLocked()
	}

	expires := now.Add(c.ttl)
	c.expiryByKey[key] = expires
	c.insertionOrder = append(c.insertionOrder, entry{key: key, expiry: expires})
	return false
}

// dequeueOneLocked removes the oldest entry from insertionOrder and, if it is
// still the current expiry for its key (a re-added key leaves a stale queue
// entry behind), removes it from expiryByKey too. Caller must hold c.mu.
func (c *Cache) dequeueOneLocked() {
	e := c.insertionOrder[0]
	c.insertionOrder = c.insertionOrder[1:]
	if current, ok := c.expiryByKey[e.key]; ok && current.Equal(e.expiry) {
		delete(c.expiryByKey, e.key)
	}
}

// size reports the current number of distinct keys held. Test-only helper.
func (c *Cache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.expiryByKey)
}
