package aggregator_test

import (
	"strings"
	"testing"
	"time"

	"github.com/YuShuanHsieh/os-notification/golang/internal/aggregator"
	"github.com/YuShuanHsieh/os-notification/golang/internal/clock"
	"github.com/YuShuanHsieh/os-notification/golang/internal/model"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func event(id string, priority model.Priority, aggKey string, replaceable bool, message string) *model.InboundNotification {
	return &model.InboundNotification{
		EventID: id,
		Message: message,
		Classification: model.Classification{
			Priority:       priority,
			AggregationKey: aggKey,
			Replaceable:    replaceable,
		},
	}
}

// recorder captures each render call as its own batch (slice of event IDs),
// in call order, so tests can assert both the number of renders and their
// contents without dealing with concurrency (New's contract says render is
// invoked synchronously on the caller's/timer goroutine, and none of these
// tests call Add/Flush concurrently, so a plain slice is safe).
type recorder struct {
	batches [][]*model.InboundNotification
}

func (r *recorder) render(batch []*model.InboundNotification) {
	r.batches = append(r.batches, batch)
}

func ids(batch []*model.InboundNotification) []string {
	out := make([]string, len(batch))
	for i, e := range batch {
		out[i] = e.EventID
	}
	return out
}

func defaultOpts() aggregator.Options {
	return aggregator.Options{
		MaxBuckets:      100,
		ImportantWindow: 2 * time.Second,
		NormalWindow:    10 * time.Second,
	}
}

func TestCriticalRendersImmediatelyWithoutClockAdvance(t *testing.T) {
	clk := clock.NewFakeClock(epoch)
	rec := &recorder{}
	agg := aggregator.New(defaultOpts(), clk, rec.render)

	agg.Add(event("e1", model.PriorityCritical, "agg.key", false, "m"))

	if len(rec.batches) != 1 {
		t.Fatalf("want 1 render, got %d", len(rec.batches))
	}
	if got := ids(rec.batches[0]); len(got) != 1 || got[0] != "e1" {
		t.Fatalf("want batch [e1], got %v", got)
	}
}

func TestLoneNormalEventRendersOnlyAfter10sWindow(t *testing.T) {
	clk := clock.NewFakeClock(epoch)
	rec := &recorder{}
	agg := aggregator.New(defaultOpts(), clk, rec.render)

	agg.Add(event("e1", model.PriorityNormal, "agg.key", false, "m"))

	clk.Advance(9 * time.Second)
	if len(rec.batches) != 0 {
		t.Fatalf("want no render before window elapses, got %d", len(rec.batches))
	}

	clk.Advance(1 * time.Second) // total 10s
	if len(rec.batches) != 1 {
		t.Fatalf("want 1 render once 10s window elapses, got %d", len(rec.batches))
	}
	if got := ids(rec.batches[0]); len(got) != 1 || got[0] != "e1" {
		t.Fatalf("want batch [e1], got %v", got)
	}
}

func TestLoneImportantEventRendersOnlyAfter2sWindow(t *testing.T) {
	clk := clock.NewFakeClock(epoch)
	rec := &recorder{}
	agg := aggregator.New(defaultOpts(), clk, rec.render)

	agg.Add(event("i1", model.PriorityImportant, "imp", false, "m"))

	clk.Advance(1999 * time.Millisecond)
	if len(rec.batches) != 0 {
		t.Fatalf("want no render before 2s window elapses, got %d", len(rec.batches))
	}

	clk.Advance(1 * time.Millisecond) // total 2s
	if len(rec.batches) != 1 {
		t.Fatalf("want 1 render once 2s window elapses, got %d", len(rec.batches))
	}
}

func TestNonReplaceableEventsInSameBucketAllBatchIntoOneRender(t *testing.T) {
	clk := clock.NewFakeClock(epoch)
	rec := &recorder{}
	agg := aggregator.New(defaultOpts(), clk, rec.render)

	agg.Add(event("e1", model.PriorityNormal, "agg.key", false, "m"))
	agg.Add(event("e2", model.PriorityNormal, "agg.key", false, "m"))
	agg.Add(event("e3", model.PriorityNormal, "agg.key", false, "m"))

	if len(rec.batches) != 0 {
		t.Fatalf("want no render while window is open, got %d", len(rec.batches))
	}

	clk.Advance(10 * time.Second)

	if len(rec.batches) != 1 {
		t.Fatalf("want exactly 1 render call for the whole batch, got %d", len(rec.batches))
	}
	got := ids(rec.batches[0])
	want := []string{"e1", "e2", "e3"}
	if len(got) != len(want) {
		t.Fatalf("want batch of %d events (all 3 sources retained), got %v", len(want), got)
	}
	for i, id := range want {
		if got[i] != id {
			t.Fatalf("want batch order %v, got %v", want, got)
		}
	}
}

func TestReplaceableBucketCollapsesToOnlyTheLatestEvent(t *testing.T) {
	clk := clock.NewFakeClock(epoch)
	rec := &recorder{}
	agg := aggregator.New(defaultOpts(), clk, rec.render)

	// The "progress" pattern: 10% -> 60% -> 90%, all replaceable, same key.
	agg.Add(event("p1", model.PriorityNormal, "prog", true, "10%"))
	agg.Add(event("p2", model.PriorityNormal, "prog", true, "60%"))
	agg.Add(event("p3", model.PriorityNormal, "prog", true, "90%"))

	clk.Advance(10 * time.Second)

	if len(rec.batches) != 1 {
		t.Fatalf("want exactly 1 render call, got %d", len(rec.batches))
	}
	batch := rec.batches[0]
	if len(batch) != 1 {
		t.Fatalf("want batch of length 1 (earlier events discarded, not carried forward), got %d: %v", len(batch), ids(batch))
	}
	if batch[0].EventID != "p3" || batch[0].Message != "90%" {
		t.Fatalf("want survivor p3/90%%, got %s/%s", batch[0].EventID, batch[0].Message)
	}
}

func TestDifferentAggregationKeysDoNotShareABucket(t *testing.T) {
	clk := clock.NewFakeClock(epoch)
	rec := &recorder{}
	agg := aggregator.New(defaultOpts(), clk, rec.render)

	agg.Add(event("a1", model.PriorityNormal, "a", false, "m"))
	agg.Add(event("b1", model.PriorityNormal, "b", false, "m"))

	clk.Advance(10 * time.Second)

	if len(rec.batches) != 2 {
		t.Fatalf("want 2 separate renders for 2 aggregation keys, got %d", len(rec.batches))
	}
	for _, b := range rec.batches {
		if len(b) != 1 {
			t.Fatalf("want each bucket to contain only its own event, got %v", ids(b))
		}
	}
}

func TestDifferentPrioritiesDoNotShareABucketEvenWithSameKey(t *testing.T) {
	clk := clock.NewFakeClock(epoch)
	rec := &recorder{}
	agg := aggregator.New(defaultOpts(), clk, rec.render)

	agg.Add(event("i1", model.PriorityImportant, "shared", false, "m"))
	agg.Add(event("n1", model.PriorityNormal, "shared", false, "m"))

	clk.Advance(2 * time.Second)
	if len(rec.batches) != 1 {
		t.Fatalf("want only the important bucket to fire at 2s, got %d renders", len(rec.batches))
	}
	if got := ids(rec.batches[0]); len(got) != 1 || got[0] != "i1" {
		t.Fatalf("want the important event i1 to fire first, got %v", got)
	}

	clk.Advance(8 * time.Second) // total 10s
	if len(rec.batches) != 2 {
		t.Fatalf("want the normal bucket to fire once total elapsed reaches 10s, got %d renders", len(rec.batches))
	}
	if got := ids(rec.batches[1]); len(got) != 1 || got[0] != "n1" {
		t.Fatalf("want the normal event n1 in the second render, got %v", got)
	}
}

func TestExceedingMaxBucketsDropsTheOverflowEventInsteadOfPanickingOrGrowing(t *testing.T) {
	clk := clock.NewFakeClock(epoch)
	rec := &recorder{}
	opts := defaultOpts()
	opts.MaxBuckets = 2
	agg := aggregator.New(opts, clk, rec.render)

	agg.Add(event("a1", model.PriorityNormal, "a", false, "m"))
	agg.Add(event("b1", model.PriorityNormal, "b", false, "m"))
	// A 3rd distinct aggregation key would be a 3rd bucket - over cap, so it
	// must be dropped silently (not panic, not evict an existing bucket).
	agg.Add(event("c1", model.PriorityNormal, "c", false, "m"))

	clk.Advance(10 * time.Second)

	if len(rec.batches) != 2 {
		t.Fatalf("want only the 2 buckets under the cap to render (overflow dropped), got %d renders", len(rec.batches))
	}
	for _, b := range rec.batches {
		for _, e := range b {
			if e.EventID == "c1" {
				t.Fatalf("overflow event c1 must be dropped, not rendered")
			}
		}
	}
}

func TestFlushRendersPendingBucketsImmediatelyWithoutClockAdvance(t *testing.T) {
	clk := clock.NewFakeClock(epoch)
	rec := &recorder{}
	agg := aggregator.New(defaultOpts(), clk, rec.render)

	agg.Add(event("e1", model.PriorityNormal, "agg.key", false, "m"))
	agg.Add(event("i1", model.PriorityImportant, "imp.key", false, "m"))

	agg.Flush()

	if len(rec.batches) != 2 {
		t.Fatalf("want Flush to render both pending buckets immediately, got %d renders", len(rec.batches))
	}
}

func TestFlushClearsBucketsSoTheOriginalWindowLaterDoesNotDoubleRender(t *testing.T) {
	clk := clock.NewFakeClock(epoch)
	rec := &recorder{}
	agg := aggregator.New(defaultOpts(), clk, rec.render)

	agg.Add(event("e1", model.PriorityNormal, "agg.key", false, "m"))
	agg.Flush()
	if len(rec.batches) != 1 {
		t.Fatalf("want 1 render from Flush, got %d", len(rec.batches))
	}

	// Reaching the original 10s deadline must not fire again: Flush cleared
	// the bucket (and should have canceled/orphaned its timer).
	clk.Advance(10 * time.Second)
	if len(rec.batches) != 1 {
		t.Fatalf("want no double-render after the original window elapses post-Flush, got %d renders", len(rec.batches))
	}
}

// TestPanickingRenderDoesNotCrashAndSubsequentBatchesStillRender proves the
// panic-containment fix: a render callback that panics for one
// critical/lone/flushed batch must not crash the calling goroutine (proven
// simply by this test function returning normally instead of the test
// process crashing), and unrelated later batches must still render
// correctly afterwards -- a poisoned batch's panic must not corrupt the
// Aggregator's internal state for anything that follows.
func TestPanickingRenderDoesNotCrashAndSubsequentBatchesStillRender(t *testing.T) {
	clk := clock.NewFakeClock(epoch)
	rec := &recorder{}
	render := func(batch []*model.InboundNotification) {
		if len(batch) == 1 && strings.HasPrefix(batch[0].EventID, "poison") {
			panic("boom: simulated render panic")
		}
		rec.render(batch)
	}
	agg := aggregator.New(defaultOpts(), clk, render)

	// Critical events render synchronously on the caller's (this test's)
	// goroutine -- the most direct way to prove Add() itself survives a
	// panicking render.
	agg.Add(event("poison", model.PriorityCritical, "agg.key", false, "m"))
	agg.Add(event("e-ok", model.PriorityCritical, "agg.key", false, "m"))

	if len(rec.batches) != 1 {
		t.Fatalf("want 1 successful render (poison batch contained, not recorded), got %d", len(rec.batches))
	}
	if got := ids(rec.batches[0]); len(got) != 1 || got[0] != "e-ok" {
		t.Fatalf("want batch [e-ok] to render normally after the poisoned batch, got %v", got)
	}

	// Also prove a poisoned window-fired batch doesn't wedge that bucket's
	// key or the aggregator's internal map: add another poison event to a
	// fresh bucket, let its window fire (still via the timer goroutine),
	// then prove a subsequent event for the *same* aggregation key still
	// batches and renders normally afterwards.
	agg.Add(event("poison2", model.PriorityNormal, "timer.key", false, "m"))
	clk.Advance(10 * time.Second) // fires the window timer synchronously (FakeClock)

	agg.Add(event("after-poison", model.PriorityNormal, "timer.key", false, "m"))
	clk.Advance(10 * time.Second)

	if len(rec.batches) != 2 {
		t.Fatalf("want 2 successful renders total after the timer-fired poison batch, got %d", len(rec.batches))
	}
	if got := ids(rec.batches[1]); len(got) != 1 || got[0] != "after-poison" {
		t.Fatalf("want batch [after-poison] to render normally in a fresh bucket, got %v", got)
	}
}

func TestFlushOnEmptyAggregatorRendersNothing(t *testing.T) {
	clk := clock.NewFakeClock(epoch)
	rec := &recorder{}
	agg := aggregator.New(defaultOpts(), clk, rec.render)

	agg.Flush()

	if len(rec.batches) != 0 {
		t.Fatalf("want no renders when there is nothing pending, got %d", len(rec.batches))
	}
}
