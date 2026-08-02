package pipeline_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YuShuanHsieh/os-notification/golang/internal/clock"
	"github.com/YuShuanHsieh/os-notification/golang/internal/dedup"
	"github.com/YuShuanHsieh/os-notification/golang/internal/model"
	"github.com/YuShuanHsieh/os-notification/golang/internal/pipeline"
)

// logCapture replaces slog's package-level default logger (restored via
// t.Cleanup) with one that writes into an in-memory buffer, so a test can
// count how many times a particular message was logged without real I/O or
// depending on test output ordering.
type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *logCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *logCapture) count(substr string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Count(c.buf.String(), substr)
}

func captureLogs(t *testing.T) *logCapture {
	t.Helper()
	prev := slog.Default()
	lc := &logCapture{}
	slog.SetDefault(slog.New(slog.NewTextHandler(lc, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return lc
}

func criticalPayload(id string) []byte {
	return []byte(fmt.Sprintf(
		`{"eventId":"%s","target":{"userId":"u1"},`+
			`"content":{"title":"T","message":"M"},`+
			`"classification":{"priority":"critical","deduplicationKey":"%s"}}`,
		id, id))
}

func newDedupCache() *dedup.Cache {
	clk := clock.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	return dedup.NewCache(100, 10*time.Minute, clk)
}

// recorder collects observed events behind a mutex and lets tests wait for a
// specific count without relying on time.Sleep races.
type recorder struct {
	mu     sync.Mutex
	events []*model.InboundNotification
}

func (r *recorder) onObserved(evt *model.InboundNotification) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, evt)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func (r *recorder) snapshot() []*model.InboundNotification {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*model.InboundNotification, len(r.events))
	copy(out, r.events)
	return out
}

// waitUntil polls cond until it returns true or the timeout elapses, failing
// the test on timeout. Avoids fixed time.Sleep races.
func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not reached within %s", timeout)
	}
}

// runAndWaitStop starts p.Run(ctx) in a goroutine and returns a function that
// cancels ctx and blocks (with a timeout) until Run has returned, failing the
// test if Run hangs -- this is how every test proves no goroutine leak.
func runAndWaitStop(t *testing.T, p *pipeline.Pipeline) (ctx context.Context, stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.Run(ctx)
		close(done)
	}()
	return ctx, func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("pipeline.Run did not return after context cancellation (goroutine leak?)")
		}
	}
}

func TestValidPayloadTriggersOnObservedOnceWithParsedEvent(t *testing.T) {
	rec := &recorder{}
	p := pipeline.New(pipeline.Options{QueueCapacity: 500, WorkerCount: 2}, newDedupCache(), rec.onObserved, clock.RealClock{}, nil)

	_, stop := runAndWaitStop(t, p)
	defer stop()

	if !p.TryEnqueue(criticalPayload("evt-1")) {
		t.Fatal("TryEnqueue returned false, want true (queue not full)")
	}

	waitUntil(t, 2*time.Second, func() bool { return rec.count() == 1 })

	events := rec.snapshot()
	if len(events) != 1 {
		t.Fatalf("want exactly 1 observed event, got %d", len(events))
	}
	got := events[0]
	if got.EventID != "evt-1" {
		t.Errorf("EventID = %q, want %q", got.EventID, "evt-1")
	}
	if got.UserID != "u1" {
		t.Errorf("UserID = %q, want %q", got.UserID, "u1")
	}
	if got.Title != "T" || got.Message != "M" {
		t.Errorf("Title/Message = %q/%q, want %q/%q", got.Title, got.Message, "T", "M")
	}
	if got.Classification.Priority != model.PriorityCritical {
		t.Errorf("Priority = %v, want PriorityCritical", got.Classification.Priority)
	}
	if got.Classification.DeduplicationKey != "evt-1" {
		t.Errorf("DeduplicationKey = %q, want %q", got.Classification.DeduplicationKey, "evt-1")
	}

	// Give any stray extra callback a chance to arrive before final assert.
	time.Sleep(50 * time.Millisecond)
	if c := rec.count(); c != 1 {
		t.Fatalf("onObserved fired %d times, want exactly 1", c)
	}
}

func TestInvalidPayloadNeverTriggersOnObservedAndWorkerSurvives(t *testing.T) {
	rec := &recorder{}
	p := pipeline.New(pipeline.Options{QueueCapacity: 500, WorkerCount: 2}, newDedupCache(), rec.onObserved, clock.RealClock{}, nil)

	_, stop := runAndWaitStop(t, p)
	defer stop()

	if !p.TryEnqueue([]byte("garbage")) {
		t.Fatal("TryEnqueue(garbage) returned false, want true (queue not full)")
	}
	// Prove the worker survived the malformed payload by enqueuing a valid
	// one afterwards and waiting for it to be observed.
	if !p.TryEnqueue(criticalPayload("evt-ok")) {
		t.Fatal("TryEnqueue(evt-ok) returned false, want true (queue not full)")
	}

	waitUntil(t, 2*time.Second, func() bool { return rec.count() == 1 })

	events := rec.snapshot()
	if len(events) != 1 || events[0].EventID != "evt-ok" {
		t.Fatalf("want exactly 1 observed event with id evt-ok, got %+v", events)
	}
}

func TestDuplicateDeduplicationKeyTriggersOnObservedOnce(t *testing.T) {
	rec := &recorder{}
	p := pipeline.New(pipeline.Options{QueueCapacity: 500, WorkerCount: 2}, newDedupCache(), rec.onObserved, clock.RealClock{}, nil)

	_, stop := runAndWaitStop(t, p)
	defer stop()

	p.TryEnqueue(criticalPayload("evt-dup"))
	p.TryEnqueue(criticalPayload("evt-dup"))
	p.TryEnqueue(criticalPayload("evt-dup"))

	waitUntil(t, 2*time.Second, func() bool { return rec.count() >= 1 })
	// Grace period: no further onObserved calls should arrive for the
	// duplicate payloads.
	time.Sleep(100 * time.Millisecond)

	if c := rec.count(); c != 1 {
		t.Fatalf("onObserved fired %d times, want exactly 1 (duplicates should be suppressed)", c)
	}
}

func TestTryEnqueueRejectsWhenQueueFullWithoutRun(t *testing.T) {
	rec := &recorder{}
	p := pipeline.New(pipeline.Options{QueueCapacity: 2, WorkerCount: 2}, newDedupCache(), rec.onObserved, clock.RealClock{}, nil)

	// Run is never started, so nothing drains the intake queue.
	if !p.TryEnqueue(criticalPayload("e1")) {
		t.Fatal("1st TryEnqueue: got false, want true (within capacity)")
	}
	if !p.TryEnqueue(criticalPayload("e2")) {
		t.Fatal("2nd TryEnqueue: got false, want true (within capacity)")
	}
	if p.TryEnqueue(criticalPayload("e3")) {
		t.Fatal("3rd TryEnqueue: got true, want false (queue full)")
	}

	if got := p.DroppedQueueFull(); got != 1 {
		t.Fatalf("DroppedQueueFull() = %d, want 1", got)
	}

	// A second rejection increments further.
	if p.TryEnqueue(criticalPayload("e4")) {
		t.Fatal("4th TryEnqueue: got true, want false (queue still full)")
	}
	if got := p.DroppedQueueFull(); got != 2 {
		t.Fatalf("DroppedQueueFull() = %d, want 2", got)
	}
}

// TestQueueFullWarningIsRateLimited proves the fix for the log-flood
// concern: sustained overload is exactly the scenario the queue-full warning
// exists to surface, but logging it on every single drop would flood the log
// with an identical line under that same sustained overload. Many rapid
// drops (no time elapsed on the pipeline's own clock) must produce only one
// warning line, while DroppedQueueFull still counts every drop; advancing
// the clock past the rate-limit window allows exactly one more.
func TestQueueFullWarningIsRateLimited(t *testing.T) {
	clk := clock.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	rec := &recorder{}
	p := pipeline.New(pipeline.Options{QueueCapacity: 1, WorkerCount: 1}, newDedupCache(), rec.onObserved, clk, nil)

	logs := captureLogs(t)

	if !p.TryEnqueue(criticalPayload("e0")) {
		t.Fatal("1st TryEnqueue: got false, want true (within capacity)")
	}

	const dropAttempts = 50
	for i := 0; i < dropAttempts; i++ {
		if p.TryEnqueue(criticalPayload(fmt.Sprintf("drop-%d", i))) {
			t.Fatalf("TryEnqueue #%d unexpectedly succeeded, want queue full", i)
		}
	}

	if got := p.DroppedQueueFull(); got != dropAttempts {
		t.Fatalf("DroppedQueueFull() = %d, want %d (every drop must still be counted)", got, dropAttempts)
	}
	if n := logs.count("intake queue full"); n != 1 {
		t.Fatalf("queue-full warning logged %d times for %d rapid drops, want exactly 1", n, dropAttempts)
	}

	// Advancing the clock past the rate-limit window allows exactly one
	// more logged occurrence for a subsequent drop.
	clk.Advance(11 * time.Second)
	if p.TryEnqueue(criticalPayload("drop-after-advance")) {
		t.Fatal("TryEnqueue after advance unexpectedly succeeded, want queue still full")
	}
	if got := p.DroppedQueueFull(); got != dropAttempts+1 {
		t.Fatalf("DroppedQueueFull() = %d, want %d", got, dropAttempts+1)
	}
	if n := logs.count("intake queue full"); n != 2 {
		t.Fatalf("queue-full warning logged %d times after advancing past the rate-limit window, want exactly 2", n)
	}
}

func TestRunReturnsPromptlyOnContextCancelWithoutLeakingGoroutines(t *testing.T) {
	rec := &recorder{}
	p := pipeline.New(pipeline.Options{QueueCapacity: 500, WorkerCount: 2}, newDedupCache(), rec.onObserved, clock.RealClock{}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pipeline.Run did not return after context cancellation (goroutine leak?)")
	}
}

// TestTryEnqueueStampsAgentReceivedAtAtIntakeNotAtWorkerProcessingTime proves
// the fix for the AgentReceivedAt-timing bug: the timestamp on the parsed
// event must reflect when TryEnqueue accepted the payload, not whenever a
// worker later happens to dequeue and process it. A single-worker pipeline
// with a FakeClock (advanced between two TryEnqueue calls, before Run ever
// starts draining the queue) makes this observable deterministically.
func TestTryEnqueueStampsAgentReceivedAtAtIntakeNotAtWorkerProcessingTime(t *testing.T) {
	clk := clock.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	rec := &recorder{}
	p := pipeline.New(pipeline.Options{QueueCapacity: 500, WorkerCount: 1}, newDedupCache(), rec.onObserved, clk, nil)

	t0 := clk.Now()
	if !p.TryEnqueue(criticalPayload("evt-early")) {
		t.Fatal("TryEnqueue(evt-early) returned false, want true")
	}
	clk.Advance(5 * time.Second)
	t1 := clk.Now()
	if !p.TryEnqueue(criticalPayload("evt-late")) {
		t.Fatal("TryEnqueue(evt-late) returned false, want true")
	}

	// Only now does anything start draining the queue -- if AgentReceivedAt
	// were captured at worker-processing time instead of at TryEnqueue time,
	// both events would incorrectly share this later timestamp.
	_, stop := runAndWaitStop(t, p)
	defer stop()

	waitUntil(t, 2*time.Second, func() bool { return rec.count() == 2 })

	byID := make(map[string]*model.InboundNotification)
	for _, e := range rec.snapshot() {
		byID[e.EventID] = e
	}
	early, ok := byID["evt-early"]
	if !ok {
		t.Fatal("evt-early was never observed")
	}
	late, ok := byID["evt-late"]
	if !ok {
		t.Fatal("evt-late was never observed")
	}
	if !early.AgentReceivedAt.Equal(t0) {
		t.Errorf("evt-early.AgentReceivedAt = %v, want %v (time of its TryEnqueue call)", early.AgentReceivedAt, t0)
	}
	if !late.AgentReceivedAt.Equal(t1) {
		t.Errorf("evt-late.AgentReceivedAt = %v, want %v (time of its TryEnqueue call)", late.AgentReceivedAt, t1)
	}
}

// TestPanickingOnObservedDoesNotCrashWorkerAndSubsequentEventsStillProcess
// proves the panic-containment fix: a poisoned callback that panics on one
// event must not crash the worker goroutine, and a following, unrelated
// event must still be processed normally afterwards.
// TestQueueFullInvokesOnDroppedCallbackOncePerDrop proves TryEnqueue calls
// the optional OnDropped callback exactly once per queue-full drop -- the
// hook internal/host wires to its metrics recorder (RecordEventDropped
// ("queue_full")) -- alongside (not instead of) the existing
// DroppedQueueFull counter.
func TestQueueFullInvokesOnDroppedCallbackOncePerDrop(t *testing.T) {
	var mu sync.Mutex
	var droppedCalls int
	onDropped := func() {
		mu.Lock()
		defer mu.Unlock()
		droppedCalls++
	}

	rec := &recorder{}
	p := pipeline.New(pipeline.Options{QueueCapacity: 1, WorkerCount: 2}, newDedupCache(), rec.onObserved, clock.RealClock{}, onDropped)

	// Run is never started, so nothing drains the intake queue -- both
	// TryEnqueue calls after the first are guaranteed to hit the full path.
	if !p.TryEnqueue(criticalPayload("e1")) {
		t.Fatal("1st TryEnqueue: got false, want true (within capacity)")
	}
	if p.TryEnqueue(criticalPayload("e2")) {
		t.Fatal("2nd TryEnqueue: got true, want false (queue full)")
	}
	if p.TryEnqueue(criticalPayload("e3")) {
		t.Fatal("3rd TryEnqueue: got true, want false (queue full)")
	}

	mu.Lock()
	got := droppedCalls
	mu.Unlock()
	if got != 2 {
		t.Fatalf("OnDropped invoked %d times, want 2 (one per queue-full drop)", got)
	}
	if want := p.DroppedQueueFull(); uint64(got) != want {
		t.Fatalf("OnDropped call count = %d, want to match DroppedQueueFull() = %d", got, want)
	}
}

// TestNilOnDroppedIsSafeToOmit proves passing nil for onDropped (matching
// every existing call site in this test file, and the production default
// when no metrics recorder is configured) does not panic on a queue-full
// drop.
func TestNilOnDroppedIsSafeToOmit(t *testing.T) {
	rec := &recorder{}
	p := pipeline.New(pipeline.Options{QueueCapacity: 1, WorkerCount: 2}, newDedupCache(), rec.onObserved, clock.RealClock{}, nil)

	if !p.TryEnqueue(criticalPayload("e1")) {
		t.Fatal("1st TryEnqueue: got false, want true (within capacity)")
	}
	if p.TryEnqueue(criticalPayload("e2")) {
		t.Fatal("2nd TryEnqueue: got true, want false (queue full)")
	}
}

// TestPanickingOnDroppedCallbackDoesNotCrashTryEnqueue proves the
// panic-containment fix applies to OnDropped too: a poisoned callback that
// panics must not propagate out of TryEnqueue (in production, TryEnqueue is
// called directly from the NATS message-dispatch goroutine, with no
// recover of its own at that call site) -- this is the explicit,
// user-mandated "metrics-recording code must never crash the application"
// requirement, exercised at the pipeline layer.
func TestPanickingOnDroppedCallbackDoesNotCrashTryEnqueue(t *testing.T) {
	onDropped := func() { panic("boom: simulated onDropped panic") }

	rec := &recorder{}
	p := pipeline.New(pipeline.Options{QueueCapacity: 1, WorkerCount: 2}, newDedupCache(), rec.onObserved, clock.RealClock{}, onDropped)

	if !p.TryEnqueue(criticalPayload("e1")) {
		t.Fatal("1st TryEnqueue: got false, want true (within capacity)")
	}
	// This call's onDropped panics -- proving it doesn't crash the test
	// process/goroutine is the entire point; the return value and counter
	// must still be correct despite the contained panic.
	if p.TryEnqueue(criticalPayload("e2")) {
		t.Fatal("2nd TryEnqueue: got true, want false (queue full)")
	}
	if got := p.DroppedQueueFull(); got != 1 {
		t.Fatalf("DroppedQueueFull() = %d, want 1 (still counted despite the panicking callback)", got)
	}

	// A subsequent, unrelated call must also survive and behave normally.
	if p.TryEnqueue(criticalPayload("e3")) {
		t.Fatal("3rd TryEnqueue: got true, want false (queue still full)")
	}
	if got := p.DroppedQueueFull(); got != 2 {
		t.Fatalf("DroppedQueueFull() = %d, want 2", got)
	}
}

func TestPanickingOnObservedDoesNotCrashWorkerAndSubsequentEventsStillProcess(t *testing.T) {
	rec := &recorder{}
	onObserved := func(evt *model.InboundNotification) {
		if evt.EventID == "evt-poison" {
			panic("boom: simulated onObserved panic")
		}
		rec.onObserved(evt)
	}
	p := pipeline.New(pipeline.Options{QueueCapacity: 500, WorkerCount: 2}, newDedupCache(), onObserved, clock.RealClock{}, nil)

	_, stop := runAndWaitStop(t, p)
	defer stop()

	if !p.TryEnqueue(criticalPayload("evt-poison")) {
		t.Fatal("TryEnqueue(evt-poison) returned false, want true")
	}
	if !p.TryEnqueue(criticalPayload("evt-ok")) {
		t.Fatal("TryEnqueue(evt-ok) returned false, want true")
	}

	waitUntil(t, 2*time.Second, func() bool { return rec.count() == 1 })

	events := rec.snapshot()
	if len(events) != 1 || events[0].EventID != "evt-ok" {
		t.Fatalf("want exactly evt-ok observed (poison event contained, not observed), got %+v", events)
	}
}
