// Package host is the AgentHost composition root: it resolves identity,
// connects to NATS, and wires the dedup cache, pipeline, aggregator, toast
// renderer, and telemetry acknowledgements together into one running agent.
// It is a faithful port of src/NotificationAgent.Core/Hosting/AgentHost.cs
// (C#) and rust/notify-agent-core/src/host.rs (Rust), with the same
// deliberate decoupling the pipeline/aggregator packages already document:
// this package is the only place that wires those already-decoupled pieces
// together via callbacks (see context/architecture.md "Processing stages").
package host

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/YuShuanHsieh/os-notification/golang/internal/aggregator"
	"github.com/YuShuanHsieh/os-notification/golang/internal/clock"
	"github.com/YuShuanHsieh/os-notification/golang/internal/dedup"
	"github.com/YuShuanHsieh/os-notification/golang/internal/identity"
	"github.com/YuShuanHsieh/os-notification/golang/internal/model"
	"github.com/YuShuanHsieh/os-notification/golang/internal/natsauth"
	"github.com/YuShuanHsieh/os-notification/golang/internal/pipeline"
	"github.com/YuShuanHsieh/os-notification/golang/internal/telemetry"
	"github.com/YuShuanHsieh/os-notification/golang/internal/toast"
)

// Options configures transport/subject wiring for a Host. Zero-value fields
// are not defaulted here -- use OptionsFromEnv for the production defaults.
type Options struct {
	NatsURL         string
	SubjectTemplate string // e.g. "notify.user.%s.desktop", formatted with the resolved user ID
	AckSubject      string
}

// OptionsFromEnv reads NOTIFY_NATS_URL (default "nats://127.0.0.1:4222"),
// NOTIFY_SUBJECT_TEMPLATE (default "notify.user.%s.desktop"), and
// NOTIFY_ACK_SUBJECT (default "notify.ack.desktop"). This mirrors
// AgentOptions.FromEnvironment (C#) / the Rust equivalent, translated to Go's
// fmt.Sprintf-style "%s" placeholder instead of "{0}".
func OptionsFromEnv() Options {
	return Options{
		NatsURL:         getEnvOr("NOTIFY_NATS_URL", "nats://127.0.0.1:4222"),
		SubjectTemplate: getEnvOr("NOTIFY_SUBJECT_TEMPLATE", "notify.user.%s.desktop"),
		AckSubject:      getEnvOr("NOTIFY_ACK_SUBJECT", "notify.ack.desktop"),
	}
}

// getEnvOr returns the environment variable's value, or fallback if it is
// unset or blank (whitespace-only) -- matching identity.EnvIdentity's
// treatment of blank as absent.
func getEnvOr(key, fallback string) string {
	v := os.Getenv(key)
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// Production defaults for dedup/pipeline sizing (context/contracts-and-invariants.md).
const (
	dedupCapacity         = 10_000
	dedupTTL              = 10 * time.Minute
	pipelineQueueCapacity = 500
	pipelineWorkerCount   = 2
)

// deps bundles the pieces of Host construction that tests need to override
// (a faster clock, shorter aggregation windows) but production code always
// takes at their defaults. This is the "test-only constructor path" for the
// batch-ack-fanout scenario: production callers only ever reach Start, which
// pins these to the documented defaults.
type deps struct {
	clk               clock.Clock
	dedupCapacity     int
	dedupTTL          time.Duration
	pipelineOptions   pipeline.Options
	aggregatorOptions aggregator.Options
}

func defaultDeps() deps {
	return deps{
		clk:           clock.RealClock{},
		dedupCapacity: dedupCapacity,
		dedupTTL:      dedupTTL,
		pipelineOptions: pipeline.Options{
			QueueCapacity: pipelineQueueCapacity,
			WorkerCount:   pipelineWorkerCount,
		},
		aggregatorOptions: aggregator.Options{}, // aggregator.New applies its own defaults
	}
}

// Host is the running composition of NATS subscription, pipeline, aggregator,
// and toast rendering for one agent instance.
type Host struct {
	nc       *nats.Conn
	sub      *nats.Subscription
	pipeline *pipeline.Pipeline
	agg      *aggregator.Aggregator
	renderer toast.Renderer
	clk      clock.Clock

	ackSubject string
	subject    string
	deviceID   string

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Start resolves identity, connects to NATS (applying authProvider's options
// if non-nil), wires dedup+pipeline+aggregator+renderer+ack-publishing, and
// subscribes to the per-user subject, starting the pipeline's worker pool in
// the background. On any failure, no goroutines are left running and no NATS
// connection is left open.
func Start(ctx context.Context, opts Options, idp identity.Provider, renderer toast.Renderer, authProvider natsauth.Provider) (*Host, error) {
	return start(ctx, opts, idp, renderer, authProvider, defaultDeps())
}

func start(ctx context.Context, opts Options, idp identity.Provider, renderer toast.Renderer, authProvider natsauth.Provider, d deps) (*Host, error) {
	ident, err := idp.Resolve(ctx)
	if err != nil {
		return nil, fmt.Errorf("host: resolve identity: %w", err)
	}

	var natsOpts []nats.Option
	if authProvider != nil {
		natsOpts, err = authProvider.Options(ctx)
		if err != nil {
			return nil, fmt.Errorf("host: nats auth: %w", err)
		}
	}

	nc, err := nats.Connect(opts.NatsURL, natsOpts...)
	if err != nil {
		return nil, fmt.Errorf("host: connect nats: %w", err)
	}

	h := &Host{
		nc:         nc,
		renderer:   renderer,
		clk:        d.clk,
		ackSubject: opts.AckSubject,
		deviceID:   ident.DeviceID,
		subject:    fmt.Sprintf(opts.SubjectTemplate, ident.UserID),
	}

	dedupCache := dedup.NewCache(d.dedupCapacity, d.dedupTTL, d.clk)
	h.agg = aggregator.New(d.aggregatorOptions, d.clk, h.render)
	h.pipeline = pipeline.New(d.pipelineOptions, dedupCache, h.onObserved, d.clk)

	sub, err := nc.Subscribe(h.subject, func(msg *nats.Msg) {
		h.pipeline.TryEnqueue(msg.Data)
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("host: subscribe: %w", err)
	}
	h.sub = sub

	runCtx, cancel := context.WithCancel(ctx)
	h.cancel = cancel
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		h.pipeline.Run(runCtx)
	}()

	return h, nil
}

// Subject returns the fully-resolved subscribe subject (userID substituted).
func (h *Host) Subject() string {
	return h.subject
}

// DroppedQueueFull returns the running count of raw payloads rejected by the
// pipeline's intake queue because it was full, so an operator/future caller
// can observe overload without reaching into the pipeline directly.
func (h *Host) DroppedQueueFull() uint64 {
	return h.pipeline.DroppedQueueFull()
}

// DroppedBucketOverflow returns the running count of events the aggregator
// dropped because creating a new bucket for them would have exceeded
// MaxBuckets, so an operator/future caller can observe overload without
// reaching into the aggregator directly.
func (h *Host) DroppedBucketOverflow() uint64 {
	return h.agg.DroppedBucketOverflow()
}

// onObserved is the pipeline's OnObserved callback: fires once per valid,
// first-seen event. It publishes observed_by_agent using the event's own
// AgentReceivedAt (stamped by the pipeline at intake), then forwards the
// event into the aggregator.
func (h *Host) onObserved(event *model.InboundNotification) {
	h.publishAck(telemetry.Ack{
		EventID:         event.EventID,
		DeviceID:        h.deviceID,
		AgentReceivedAt: event.AgentReceivedAt,
		Status:          telemetry.StatusObservedByAgent,
	})
	h.agg.Add(event)
}

// render is the aggregator's RenderFunc: fires when a batch is ready
// (immediately for critical/lone events, or when a bucket's window
// elapses). It renders the batch as one toast and, only on success,
// acknowledges submitted_to_windows for every event the batch represents --
// each read straight off its own AgentReceivedAt (no side-tracking needed:
// the event carries it) paired with a shared toastSubmittedAt. A rendering
// failure acknowledges nothing and is otherwise swallowed: a single bad
// event/render must not crash the host (context/architecture.md).
func (h *Host) render(batch []*model.InboundNotification) {
	req := toast.FromBatch(batch)

	// Deliberately decoupled from the pipeline's cancelable run context: a
	// shutdown-triggered aggregator.Flush() must still be able to render
	// (and thus acknowledge) whatever was pending, per the "flush the
	// aggregator" shutdown step -- it must not be starved by a context
	// that Shutdown already canceled one step earlier.
	submittedAt, err := h.renderer.Show(context.Background(), req)
	if err != nil {
		return
	}

	for _, event := range batch {
		ts := submittedAt
		h.publishAck(telemetry.Ack{
			EventID:          event.EventID,
			DeviceID:         h.deviceID,
			AgentReceivedAt:  event.AgentReceivedAt,
			ToastSubmittedAt: &ts,
			Status:           telemetry.StatusSubmittedToWindows,
		})
	}
}

// publishAck serializes and publishes one acknowledgement, swallowing any
// error (a failed ack publish must not crash the host or block rendering).
func (h *Host) publishAck(ack telemetry.Ack) {
	data, err := ack.ToJSON()
	if err != nil {
		return
	}
	_ = h.nc.Publish(h.ackSubject, data)
}

// Shutdown cancels background pipeline work, waits (bounded by ctx) for its
// workers to exit, unsubscribes, flushes the aggregator so nothing pending is
// silently lost, and closes the NATS connection. Best-effort throughout: it
// must not hang or panic even if some component is mid-failure, matching the
// documented "shutdown is best-effort, not a durable drain guarantee"
// contract (context/architecture.md).
func (h *Host) Shutdown(ctx context.Context) error {
	if h.cancel != nil {
		h.cancel()
	}

	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}

	var firstErr error
	if h.sub != nil {
		if err := h.sub.Unsubscribe(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("host: unsubscribe: %w", err)
		}
	}

	if h.agg != nil {
		h.agg.Flush()
	}

	if h.nc != nil {
		h.nc.Close()
	}

	return firstErr
}
