// Package clock provides an injectable time source so that batching and
// TTL-dependent code (aggregator, dedup cache) can be tested deterministically
// without real sleeps, mirroring the C# TimeProvider / Rust FakeClock pattern
// used elsewhere in this repository.
package clock

import (
	"sync"
	"time"
)

// Clock is the injectable time source.
type Clock interface {
	Now() time.Time
}

// RealClock is the production Clock backed by the wall clock.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

// FakeClock is a manually-advanced Clock for deterministic tests.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewFakeClock returns a FakeClock starting at start.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start}
}

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
