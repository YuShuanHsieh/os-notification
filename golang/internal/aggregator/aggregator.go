// Package aggregator owns priority routing, batching, and replaceable
// collapsing for inbound notifications (ADR-007 in the C#/Rust references).
// It ports src/NotificationAgent.Core/Aggregation/Aggregator.cs and
// rust/notify-agent-core/src/aggregator.rs faithfully: `critical` bypasses
// batching entirely; `important`/`normal` events are grouped into buckets
// keyed by (aggregationKey, priority) and flushed after a priority-specific
// window; `replaceable` events (independent of priority) discard whatever was
// previously in their bucket the instant a new one arrives, so only the
// latest survives to render time.
package aggregator

import (
	"log/slog"
	"sync"
	"time"

	"github.com/YuShuanHsieh/os-notification/golang/internal/clock"
	"github.com/YuShuanHsieh/os-notification/golang/internal/model"
)

// Options configures bucket capacity and batch windows. Zero-value fields
// fall back to the same defaults as the C#/Rust references (100 buckets, a
// 2s important window, a 10s normal window) — see New.
type Options struct {
	MaxBuckets      int           // default 100
	ImportantWindow time.Duration // default 2 * time.Second
	NormalWindow    time.Duration // default 10 * time.Second
}

// RenderFunc is invoked with the batch of source events that should render as
// one toast. len(batch) >= 1 always.
type RenderFunc func(batch []*model.InboundNotification)

// bucketKey groups events for batching. Two events only ever share a bucket
// (and thus a render call) when both their aggregation key and their
// priority match.
type bucketKey struct {
	aggregationKey string
	priority       model.Priority
}

// bucket accumulates events for one bucketKey until its window timer fires.
type bucket struct {
	events []*model.InboundNotification
	timer  clock.Timer
}

// bucketOverflowWarnInterval bounds how often Add logs its bucket-overflow
// warning under sustained overload -- see pipeline.queueFullWarnInterval's
// doc comment for the identical rationale (sustained overload is exactly the
// scenario that would otherwise flood the log). The cumulative
// droppedBucketOverflow counter still reflects every drop regardless of
// whether a given drop's warning gets logged.
const bucketOverflowWarnInterval = 10 * time.Second

// Aggregator owns priority handling, batching, and latest-state replacement.
// Best-effort in steady state: bucket overflow silently drops the incoming
// event (see context/contracts-and-invariants.md: "bounded resources, drop
// under overload") rather than growing unboundedly or blocking.
type Aggregator struct {
	mu      sync.Mutex
	opts    Options
	clk     clock.Clock
	render  RenderFunc
	buckets map[bucketKey]*bucket

	// droppedBucketOverflow counts events dropped because adding them would
	// have required creating a bucket beyond MaxBuckets. Exposed via
	// DroppedBucketOverflow below so a caller/telemetry hook can observe it.
	droppedBucketOverflow uint64

	// bucketOverflowLastWarnAt records when the bucket-overflow warning was
	// last logged, guarded by mu (already held at the one call site that
	// reads/writes it), so at most one warning is logged per
	// bucketOverflowWarnInterval regardless of drop volume.
	bucketOverflowLastWarnAt time.Time
}

// DroppedBucketOverflow returns the running count of events dropped because
// creating a new bucket for them would have exceeded MaxBuckets.
func (a *Aggregator) DroppedBucketOverflow() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.droppedBucketOverflow
}

// New constructs an Aggregator. render is called synchronously from whatever
// goroutine triggers it (either the caller's goroutine for a `critical`
// event, or the clock's AfterFunc callback goroutine when a batch window
// fires) — callers needing their own concurrency must handle that inside
// render itself.
func New(opts Options, clk clock.Clock, render RenderFunc) *Aggregator {
	if opts.MaxBuckets <= 0 {
		opts.MaxBuckets = 100
	}
	if opts.ImportantWindow <= 0 {
		opts.ImportantWindow = 2 * time.Second
	}
	if opts.NormalWindow <= 0 {
		opts.NormalWindow = 10 * time.Second
	}
	return &Aggregator{
		opts:    opts,
		clk:     clk,
		render:  render,
		buckets: make(map[bucketKey]*bucket),
	}
}

// Add enqueues an event for aggregation/batching.
//
//   - `critical` events bypass bucketing entirely and render immediately,
//     synchronously, as a batch of one.
//   - Otherwise the event joins the bucket for (aggregationKey, priority),
//     creating it (and starting its window timer) on first arrival.
//   - If the event is `replaceable`, it first clears out everything
//     currently in that bucket — so a replaceable event never accumulates
//     with earlier events, it replaces them outright — then the event is
//     added. Non-replaceable events are simply appended, so a batch of
//     several accumulates all of them for one shared render call.
//   - If creating a *new* bucket would exceed MaxBuckets, the event is
//     dropped (matching Aggregator.cs / aggregator.rs: bucket overflow is a
//     silent drop, not a panic or an unbounded map).
func (a *Aggregator) Add(event *model.InboundNotification) {
	if event.Classification.Priority == model.PriorityCritical {
		a.safeRender([]*model.InboundNotification{event})
		return
	}

	key := bucketKey{
		aggregationKey: event.Classification.AggregationKey,
		priority:       event.Classification.Priority,
	}

	a.mu.Lock()
	b, ok := a.buckets[key]
	if !ok {
		if len(a.buckets) >= a.opts.MaxBuckets {
			a.droppedBucketOverflow++
			total := a.droppedBucketOverflow
			maxBuckets := a.opts.MaxBuckets

			now := a.clk.Now()
			shouldLog := now.Sub(a.bucketOverflowLastWarnAt) >= bucketOverflowWarnInterval
			if shouldLog {
				a.bucketOverflowLastWarnAt = now
			}
			a.mu.Unlock()

			if shouldLog {
				slog.Warn("aggregator: dropped event, bucket overflow", "aggregationKey", key.aggregationKey, "priority", key.priority, "maxBuckets", maxBuckets, "totalDropped", total)
			}
			return
		}
		window := a.opts.NormalWindow
		if key.priority == model.PriorityImportant {
			window = a.opts.ImportantWindow
		}
		b = &bucket{}
		a.buckets[key] = b
		b.timer = a.clk.AfterFunc(window, func() { a.fire(key) })
	}

	if event.Classification.Replaceable {
		b.events = b.events[:0]
	}
	b.events = append(b.events, event)
	a.mu.Unlock()
}

// fire is the window-timer callback: it removes the bucket (so a concurrent
// Add for the same key starts a fresh bucket rather than reviving this one)
// and renders whatever it had accumulated.
func (a *Aggregator) fire(key bucketKey) {
	a.mu.Lock()
	b, ok := a.buckets[key]
	if ok {
		delete(a.buckets, key)
	}
	a.mu.Unlock()

	if !ok || len(b.events) == 0 {
		return
	}
	a.safeRender(b.events)
}

// Flush immediately renders every bucket that currently has pending content
// and clears all buckets/timers, so nothing pending is silently lost (used at
// shutdown). Each bucket's timer is stopped; if a timer had already fired
// concurrently before Flush could stop it, that fire() sees the bucket
// already removed from the map and becomes a harmless no-op, so no bucket is
// ever rendered twice.
func (a *Aggregator) Flush() {
	a.mu.Lock()
	pending := a.buckets
	a.buckets = make(map[bucketKey]*bucket)
	a.mu.Unlock()

	for _, b := range pending {
		if b.timer != nil {
			b.timer.Stop()
		}
		if len(b.events) > 0 {
			a.safeRender(b.events)
		}
	}
}

// safeRender invokes the injected RenderFunc with a recover() guard, so a
// panic inside content-building or rendering for one batch can't kill the
// caller's goroutine (the caller of Add, for a critical event) or the
// aggregator's own timer goroutine (for a window-fired or flushed batch) --
// matching the documented "a single bad event cannot terminate the agent"
// contract (context/architecture.md).
func (a *Aggregator) safeRender(batch []*model.InboundNotification) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("aggregator: recovered from panic while rendering batch", "panic", r)
		}
	}()
	a.render(batch)
}
