// Package clock provides an injectable time source so that batching and
// TTL-dependent code (aggregator, dedup cache) can be tested deterministically
// without real sleeps, mirroring the C# TimeProvider / Rust FakeClock pattern
// used elsewhere in this repository.
package clock

import (
	"sort"
	"sync"
	"time"
)

// Clock is the injectable time source.
//
// AfterFunc schedules f to run once, after d has elapsed on this Clock's own
// notion of time. For RealClock that's wall-clock time (backed by a real
// time.Timer); for FakeClock, f only runs when a test calls Advance() far
// enough to cross the deadline — no real timer, no real sleep. This is what
// lets timer-driven production code (e.g. the aggregator's batch windows) be
// exercised deterministically in tests: inject a FakeClock and drive it by
// calling Advance(), instead of relying on wall-clock sleeps.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is a handle to a scheduled Clock callback that can be canceled.
// *time.Timer already satisfies this interface.
type Timer interface {
	// Stop cancels the timer. It returns true if the cancellation stopped a
	// pending callback, false if the callback already fired (or was already
	// stopped).
	Stop() bool
}

// RealClock is the production Clock backed by the wall clock.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) AfterFunc(d time.Duration, f func()) Timer {
	return time.AfterFunc(d, f)
}

// FakeClock is a manually-advanced Clock for deterministic tests.
type FakeClock struct {
	mu      sync.Mutex
	now     time.Time
	nextID  uint64
	pending map[uint64]*fakeTimer
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

// Advance moves the clock forward by d, then synchronously runs (in
// scheduling order) every pending AfterFunc callback whose deadline is now at
// or before the new time. Callbacks run without FakeClock's internal lock
// held, so they may safely call back into the clock (e.g. Now() or another
// AfterFunc) without deadlocking.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now

	var due []*fakeTimer
	for id, t := range c.pending {
		if !t.deadline.After(now) {
			due = append(due, t)
			delete(c.pending, id)
		}
	}
	c.mu.Unlock()

	sort.Slice(due, func(i, j int) bool {
		if due[i].deadline.Equal(due[j].deadline) {
			return due[i].id < due[j].id
		}
		return due[i].deadline.Before(due[j].deadline)
	})
	for _, t := range due {
		t.f()
	}
}

// AfterFunc schedules f to run the next time Advance() moves the clock's
// time to or past now+d. It never spawns a goroutine or blocks; the callback
// is only ever invoked synchronously from inside a later Advance() call.
func (c *FakeClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil {
		c.pending = make(map[uint64]*fakeTimer)
	}
	c.nextID++
	id := c.nextID
	c.pending[id] = &fakeTimer{id: id, deadline: c.now.Add(d), f: f}
	return &fakeClockTimer{clock: c, id: id}
}

func (c *FakeClock) stop(id uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.pending[id]; ok {
		delete(c.pending, id)
		return true
	}
	return false
}

type fakeTimer struct {
	id       uint64
	deadline time.Time
	f        func()
}

type fakeClockTimer struct {
	clock *FakeClock
	id    uint64
}

func (t *fakeClockTimer) Stop() bool {
	return t.clock.stop(t.id)
}
