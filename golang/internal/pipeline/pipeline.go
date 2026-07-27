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
	"sync"
	"sync/atomic"

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

// Pipeline is a bounded intake queue with a fixed worker pool. Overload drops
// payloads at the queue boundary -- memory stays bounded, delivery stays
// best-effort, and every drop is counted via DroppedQueueFull.
type Pipeline struct {
	opts       Options
	queue      chan []byte
	dedupCache *dedup.Cache
	onObserved OnObserved

	droppedQueueFull atomic.Uint64
}

// New constructs a Pipeline. dedupCache suppresses duplicates by
// deduplicationKey before OnObserved fires.
func New(opts Options, dedupCache *dedup.Cache, onObserved OnObserved) *Pipeline {
	return &Pipeline{
		opts:       opts,
		queue:      make(chan []byte, opts.QueueCapacity),
		dedupCache: dedupCache,
		onObserved: onObserved,
	}
}

// TryEnqueue accepts one raw inbound payload (as received from NATS) into the
// bounded intake queue. Returns false if the queue is currently full -- the
// payload is dropped without being parsed, and the caller should count this
// as a dropped-queue-full event (exposed via DroppedQueueFull() below).
// Returns true if it was accepted into the queue (not a guarantee it will
// pass parsing/dedup -- that happens asynchronously in a worker).
func (p *Pipeline) TryEnqueue(payload []byte) bool {
	select {
	case p.queue <- payload:
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
		case payload := <-p.queue:
			p.process(payload)
		}
	}
}

// process parses and dedups a single payload, invoking onObserved for every
// valid first-seen event. Parse errors and duplicates are dropped silently
// -- one poison payload must never crash a worker.
func (p *Pipeline) process(payload []byte) {
	evt, err := parser.Parse(payload)
	if err != nil {
		return
	}
	if p.dedupCache.SeenOrAdd(evt.Classification.DeduplicationKey) {
		return
	}
	p.onObserved(evt)
}
