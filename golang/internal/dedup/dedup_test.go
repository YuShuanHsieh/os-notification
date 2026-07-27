package dedup

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YuShuanHsieh/os-notification/golang/internal/clock"
)

func TestSeenOrAdd_FirstSeenThenDuplicate(t *testing.T) {
	clk := clock.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	c := NewCache(10, 10*time.Minute, clk)

	if got := c.SeenOrAdd("k1"); got {
		t.Fatalf("first call for k1: got seen=%v, want false (not seen)", got)
	}
	if got := c.SeenOrAdd("k1"); !got {
		t.Fatalf("second call for k1: got seen=%v, want true (duplicate)", got)
	}
	// A different key is unaffected.
	if got := c.SeenOrAdd("k2"); got {
		t.Fatalf("first call for k2: got seen=%v, want false (not seen)", got)
	}
}

func TestSeenOrAdd_ExpiresAfterTTL(t *testing.T) {
	clk := clock.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	c := NewCache(10, 10*time.Minute, clk)

	if got := c.SeenOrAdd("k1"); got {
		t.Fatalf("first call for k1: got seen=%v, want false", got)
	}
	clk.Advance(9 * time.Minute)
	if got := c.SeenOrAdd("k1"); !got {
		t.Fatalf("call within TTL: got seen=%v, want true (still duplicate)", got)
	}
	clk.Advance(2 * time.Minute) // total 11 minutes since original insert
	if got := c.SeenOrAdd("k1"); got {
		t.Fatalf("call after TTL expiry: got seen=%v, want false (expired, first-seen again)", got)
	}
}

func TestSeenOrAdd_CapacityEviction(t *testing.T) {
	const capacity = 3
	clk := clock.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	c := NewCache(capacity, time.Hour, clk)

	keys := []string{"a", "b", "c", "d", "e"} // capacity+2 distinct keys
	for _, k := range keys {
		if got := c.SeenOrAdd(k); got {
			t.Fatalf("first insert of key %q: got seen=%v, want false", k, got)
		}
	}

	if size := c.size(); size > capacity {
		t.Fatalf("cache size = %d, want <= capacity (%d)", size, capacity)
	}

	// The oldest keys ("a", "b") should have been evicted, so re-adding them
	// is treated as first-seen (not a duplicate) rather than being suppressed.
	if got := c.SeenOrAdd("a"); got {
		t.Fatalf("re-add of evicted key %q: got seen=%v, want false (evicted, first-seen again)", "a", got)
	}
}

func TestSeenOrAdd_ConcurrentSameKey_ExactlyOneWinner(t *testing.T) {
	clk := clock.RealClock{}
	c := NewCache(10_000, 10*time.Minute, clk)

	const n = 1000
	var wins int64
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			if !c.SeenOrAdd("same-key") {
				atomic.AddInt64(&wins, 1)
			}
			// Also add distinct keys concurrently to exercise capacity/eviction paths.
			c.SeenOrAdd("key-" + string(rune('a'+(i%26))) + string(rune('0'+(i/26)%10)))
		}(i)
	}
	wg.Wait()

	if wins != 1 {
		t.Fatalf("wins = %d, want exactly 1 (exactly-once first-seen semantics)", wins)
	}
}
