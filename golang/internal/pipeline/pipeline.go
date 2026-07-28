// Package pipeline implements the bounded intake queue + worker pool that
// sits at the front of the agent: raw inbound payloads are enqueued via
// TryEnqueue, and a fixed pool of workers parses, dedups, and hands off each
// valid first-seen event via a caller-supplied callback (design §9).
//
// This is a faithful port of the queue/worker-pool shape in
// src/NotificationAgent.Core/Pipeline/EventPipeline.cs and
// rust/notify-agent-core/src/pipeline.rs, with one deliberate architectural
// difference: those reference implementations wire the aggregator and
// telemetry publisher directly into the pipeline. This Go version stays
// decoupled from both via the OnObserved callback -- a later AgentHost type
// owns wiring the pipeline to dedup, the aggregator, and telemetry.
package pipeline

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/YuShuanHsieh/os-notification/golang/internal/clock"
	"github.com/YuShuanHsieh/os-notification/golang/internal/dedup"
	"github.com/YuShuanHsieh/os-notification/golang/internal/model"
	"github.com/YuShuanHsieh/os-notification/golang/internal/parser"
)

// Options configures the pipeline's bounded intake queue and worker pool.
type Options struct {
	// QueueCapacity bounds the number of raw payloads buffered between
	// TryEnqueue and a worker picking them up. Design §9 baseline: 500.
	QueueCapacity int
	// WorkerCount is the number of worker goroutines started by Run.
	// Design §9 baseline: 2.
	WorkerCount int
}

// OnObserved is invoked once per valid, first-seen (non-duplicate) event,
// from one of the pipeline's worker goroutines. The caller (AgentHost) uses
// this to emit an observed_by_agent acknowledgement and forward the event
// into the Aggregator. OnObserved may be called concurrently from different
// workers -- implementations must be safe for concurrent use, or do their own
// synchronization.
type OnObserved func(event *model.InboundNotification)

// queueItem pairs a raw payload with the timestamp it was accepted at --
// captured in TryEnqueue, at the moment the raw payload is accepted from
// NATS, so that it reflects true receipt time rather than whenever a worker
// eventually gets around to processing it. This is an internal
// implementation detail; it does not appear in any public signature.
type queueItem struct {
	payload    []byte
	receivedAt time.Time
}

// Pipeline is a bounded intake queue with a fixed worker pool. Overload drops
// payloads at the queue boundary -- memory stays bounded, delivery stays
// best-effort, and every drop is counted via DroppedQueueFull.
type Pipeline struct {
	opts       Options
	queue      chan queueItem
	dedupCache *dedup.Cache
	onObserved OnObserved
	clk        clock.Clock

	droppedQueueFull atomic.Uint64
}

// New constructs a Pipeline. dedupCache suppresses duplicates by
// deduplicationKey before OnObserved fires. clk is the time source used to
// stamp each payload's AgentReceivedAt at TryEnqueue time.
func New(opts Options, dedupCache *dedup.Cache, onObserved OnObserved, clk clock.Clock) *Pipeline {
	return &Pipeline{
		opts:       opts,
		queue:      make(chan queueItem, opts.QueueCapacity),
		dedupCache: dedupCache,
		onObserved: onObserved,
		clk:        clk,
	}
}

// TryEnqueue accepts one raw inbound payload (as received from NATS) into the
// bounded intake queue, stamping it with the current time (per this
// Pipeline's clock) as its AgentReceivedAt -- captured here, at intake,
// rather than later when a worker happens to dequeue it. Returns false if
// the queue is currently full -- the payload is dropped without being
// parsed, and the caller should count this as a dropped-queue-full event
// (exposed via DroppedQueueFull() below). Returns true if it was accepted
// into the queue (not a guarantee it will pass parsing/dedup -- that happens
// asynchronously in a worker).
func (p *Pipeline) TryEnqueue(payload []byte) bool {
	item := queueItem{payload: payload, receivedAt: p.clk.Now()}
	select {
	case p.queue <- item:
		return true
	default:
		p.droppedQueueFull.Add(1)
		return false
	}
}

// DroppedQueueFull returns the running count of payloads rejected because
// the intake queue was full.
func (p *Pipeline) DroppedQueueFull() uint64 {
	return p.droppedQueueFull.Load()
}

// Run starts the configured number of worker goroutines, each pulling
// payloads off the intake queue, parsing them (dropping silently on parse
// error -- no callback, no crash), checking dedup (dropping silently on
// duplicate), and calling onObserved for every valid first-seen event. Run
// blocks until ctx is canceled; on cancellation, workers should stop
// accepting new work but this is a best-effort shutdown (in-flight work may
// or may not finish -- mirrors the documented "shutdown is best-effort, not a
// durable drain guarantee" product contract in context/architecture.md).
// Run returns once all worker goroutines have exited.
func (p *Pipeline) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(p.opts.WorkerCount)
	for i := 0; i < p.opts.WorkerCount; i++ {
		go func() {
			defer wg.Done()
			p.workerLoop(ctx)
		}()
	}
	wg.Wait()
}

func (p *Pipeline) workerLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-p.queue:
			p.process(item)
		}
	}
}

// process parses and dedups a single payload, invoking onObserved for every
// valid first-seen event. Parse errors and duplicates are dropped silently
// -- one poison payload must never crash a worker. A panic anywhere in this
// path (parsing, dedup, or the onObserved callback) is also contained here,
// so a single poisoned payload can only ever cost itself, never the worker
// goroutine or any subsequent payload (context/architecture.md: "a single
// bad event cannot terminate the agent").
func (p *Pipeline) process(item queueItem) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("pipeline: recovered from panic while processing event: %v", r)
		}
	}()

	evt, err := parser.Parse(item.payload)
	if err != nil {
		return
	}
	if p.dedupCache.SeenOrAdd(evt.Classification.DeduplicationKey) {
		return
	}
	evt.AgentReceivedAt = item.receivedAt
	p.onObserved(evt)
}
