package host

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/YuShuanHsieh/os-notification/golang/internal/aggregator"
	"github.com/YuShuanHsieh/os-notification/golang/internal/clock"
	"github.com/YuShuanHsieh/os-notification/golang/internal/dedup"
	"github.com/YuShuanHsieh/os-notification/golang/internal/identity"
	"github.com/YuShuanHsieh/os-notification/golang/internal/model"
	"github.com/YuShuanHsieh/os-notification/golang/internal/pipeline"
	"github.com/YuShuanHsieh/os-notification/golang/internal/toast"
)

const natsTestURL = "nats://127.0.0.1:4222"

// requireLiveNATS skips the test (does not fail the suite) when no NATS
// server is reachable at 127.0.0.1:4222 -- matching the existing pattern in
// this repository (see e.g. cmd/test-publisher's development-runtime
// assumptions) rather than requiring a server for `go test ./...` to pass.
func requireLiveNATS(t *testing.T) {
	t.Helper()
	nc, err := nats.Connect(natsTestURL, nats.Timeout(500*time.Millisecond))
	if err != nil {
		t.Skipf("no NATS server reachable on %s: %v", natsTestURL, err)
	}
	nc.Close()
}

// fixedIdentity is a trivial identity.Provider stand-in for tests.
type fixedIdentity struct {
	id identity.Identity
}

func (f fixedIdentity) Resolve(ctx context.Context) (identity.Identity, error) {
	return f.id, nil
}

// recordingRenderer is an in-test toast.Renderer that records every Show call
// and returns a fixed (or per-call, if set) submission timestamp.
type recordingRenderer struct {
	mu    sync.Mutex
	calls []toast.ToastRequest

	// submittedAt is returned from every Show call when non-zero; otherwise
	// time.Now() is used.
	submittedAt time.Time
	err         error
}

func (r *recordingRenderer) Show(ctx context.Context, req toast.ToastRequest) (time.Time, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, req)
	if r.err != nil {
		return time.Time{}, r.err
	}
	if !r.submittedAt.IsZero() {
		return r.submittedAt, nil
	}
	return time.Now(), nil
}

func (r *recordingRenderer) Calls() []toast.ToastRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]toast.ToastRequest, len(r.calls))
	copy(out, r.calls)
	return out
}

// uniqueID returns a short random hex string so concurrent/successive test
// runs don't collide on subjects or user IDs against a shared live server.
func uniqueID(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return hex.EncodeToString(buf)
}

// wireEvent builds one raw inbound wire JSON payload matching the contract
// documented in context/contracts-and-invariants.md.
func wireEvent(eventID, userID, title, message, priority, aggregationKey string, replaceable bool) []byte {
	payload := map[string]any{
		"schemaVersion":    "1.0",
		"eventId":          eventID,
		"notificationType": "test",
		"target": map[string]any{
			"userId": userID,
		},
		"content": map[string]any{
			"title":   title,
			"message": message,
		},
		"classification": map[string]any{
			"priority":         priority,
			"aggregationKey":   aggregationKey,
			"deduplicationKey": eventID,
			"replaceable":      replaceable,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return body
}

// collectAcks subscribes to subject on nc and returns a channel that
// receives every ack payload decoded as ackWire, plus a cleanup func.
type ackWire struct {
	EventID          string     `json:"eventId"`
	DeviceID         string     `json:"deviceId"`
	AgentReceivedAt  time.Time  `json:"agentReceivedAt"`
	ToastSubmittedAt *time.Time `json:"toastSubmittedAt"`
	Status           string     `json:"status"`
}

func collectAcks(t *testing.T, nc *nats.Conn, subject string) chan ackWire {
	t.Helper()
	ch := make(chan ackWire, 64)
	sub, err := nc.Subscribe(subject, func(msg *nats.Msg) {
		var a ackWire
		if err := json.Unmarshal(msg.Data, &a); err != nil {
			return
		}
		ch <- a
	})
	if err != nil {
		t.Fatalf("subscribe acks: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	// Ensure the SUB has actually reached the server (and settle briefly)
	// before the caller publishes -- otherwise a publish on a separate
	// connection can race the subscription's registration, matching the
	// same settle concern cmd/test-publisher documents around its own
	// pre-publish sleep.
	if err := nc.Flush(); err != nil {
		t.Fatalf("flush after subscribe: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	return ch
}

// ackCollector buffers every ack read off a channel, keyed by (eventID,
// status), so that waiting for one specific ack never silently discards a
// different event's ack that happened to arrive first. This matters here
// because the pipeline runs a worker pool: acks for concurrently-processed
// events are not guaranteed to arrive in publish order.
type ackCollector struct {
	ch   chan ackWire
	seen map[[2]string]ackWire
}

func newAckCollector(ch chan ackWire) *ackCollector {
	return &ackCollector{ch: ch, seen: make(map[[2]string]ackWire)}
}

// waitFor returns the ack matching eventID+status, reading (and buffering)
// further acks off the channel until it's seen, or failing the test after
// timeout.
func (c *ackCollector) waitFor(t *testing.T, eventID, status string, timeout time.Duration) ackWire {
	t.Helper()
	key := [2]string{eventID, status}
	if a, ok := c.seen[key]; ok {
		return a
	}
	deadline := time.After(timeout)
	for {
		select {
		case a := <-c.ch:
			c.seen[[2]string{a.EventID, a.Status}] = a
			if a.EventID == eventID && a.Status == status {
				return a
			}
		case <-deadline:
			t.Fatalf("timed out waiting for ack eventId=%s status=%s", eventID, status)
			return ackWire{}
		}
	}
}

func TestStartLiveNATSObserveRenderAck(t *testing.T) {
	requireLiveNATS(t)

	userID := "u-" + uniqueID(t)
	ackSubject := "notify.ack.test." + uniqueID(t)
	opts := Options{
		NatsURL:         natsTestURL,
		SubjectTemplate: "notify.user.%s.desktop",
		AckSubject:      ackSubject,
	}
	idp := fixedIdentity{id: identity.Identity{UserID: userID, DeviceID: "d-test"}}
	renderer := &recordingRenderer{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h, err := Start(ctx, opts, idp, renderer, nil)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = h.Shutdown(shutdownCtx)
	}()

	if got, want := h.Subject(), fmt.Sprintf("notify.user.%s.desktop", userID); got != want {
		t.Fatalf("Subject() = %q, want %q", got, want)
	}
	// Ensure the host's own subject subscription has reached the server
	// before anything publishes to it (see collectAcks for the same
	// settle concern).
	if err := h.nc.Flush(); err != nil {
		t.Fatalf("flush host subscription: %v", err)
	}

	// Observe acks with a raw connection, independent of the Host.
	obsNC, err := nats.Connect(natsTestURL)
	if err != nil {
		t.Fatalf("nats.Connect (observer): %v", err)
	}
	defer obsNC.Close()
	acks := newAckCollector(collectAcks(t, obsNC, ackSubject))

	// Publisher: a separate raw connection, matching the test-publisher
	// pattern of publishing independently of the host under test.
	pubNC, err := nats.Connect(natsTestURL)
	if err != nil {
		t.Fatalf("nats.Connect (publisher): %v", err)
	}
	defer pubNC.Close()

	eventID := "evt-" + uniqueID(t)
	// priority=critical bypasses aggregation batching entirely and renders
	// immediately, keeping this test fast and deterministic without needing
	// to wait out a real aggregation window.
	body := wireEvent(eventID, userID, "Test Title", "Test Message", "critical", "test-agg", false)
	if err := pubNC.Publish(h.Subject(), body); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := pubNC.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	observedAck := acks.waitFor(t, eventID, "observed_by_agent", 5*time.Second)
	if observedAck.ToastSubmittedAt != nil {
		t.Fatalf("observed_by_agent ack: ToastSubmittedAt = %v, want nil", observedAck.ToastSubmittedAt)
	}
	if observedAck.DeviceID != "d-test" {
		t.Fatalf("observed_by_agent ack: DeviceID = %q, want %q", observedAck.DeviceID, "d-test")
	}

	submittedAck := acks.waitFor(t, eventID, "submitted_to_windows", 5*time.Second)
	if submittedAck.ToastSubmittedAt == nil {
		t.Fatalf("submitted_to_windows ack: ToastSubmittedAt = nil, want non-nil")
	}
	if !submittedAck.AgentReceivedAt.Equal(observedAck.AgentReceivedAt) {
		t.Fatalf("submitted_to_windows ack: AgentReceivedAt = %v, want %v (from observed ack)",
			submittedAck.AgentReceivedAt, observedAck.AgentReceivedAt)
	}

	calls := renderer.Calls()
	if len(calls) != 1 {
		t.Fatalf("renderer.Show call count = %d, want 1", len(calls))
	}
	if calls[0].Title != "Test Title" || calls[0].Message != "Test Message" {
		t.Fatalf("renderer.Show req = %+v, want Title=%q Message=%q", calls[0], "Test Title", "Test Message")
	}
}

func TestStartLiveNATSBatchAckFanout(t *testing.T) {
	requireLiveNATS(t)

	userID := "u-" + uniqueID(t)
	ackSubject := "notify.ack.test." + uniqueID(t)
	opts := Options{
		NatsURL:         natsTestURL,
		SubjectTemplate: "notify.user.%s.desktop",
		AckSubject:      ackSubject,
	}
	idp := fixedIdentity{id: identity.Identity{UserID: userID, DeviceID: "d-test"}}
	renderer := &recordingRenderer{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Shortened aggregation window: waiting out the real 10s normal-priority
	// window in every test run is impractical, so this scenario uses the
	// internal test-only constructor path (start, not Start) with a much
	// shorter window. Production callers only ever reach Start, which pins
	// the documented defaults (see defaultDeps).
	d := defaultDeps()
	d.aggregatorOptions = aggregator.Options{
		MaxBuckets:      100,
		ImportantWindow: 2 * time.Second,
		NormalWindow:    200 * time.Millisecond,
	}
	d.pipelineOptions = pipeline.Options{QueueCapacity: 500, WorkerCount: 2}

	h, err := start(ctx, opts, idp, renderer, nil, d)
	if err != nil {
		t.Fatalf("start() error = %v", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = h.Shutdown(shutdownCtx)
	}()
	// Ensure the host's own subject subscription has reached the server
	// before anything publishes to it (see collectAcks for the same
	// settle concern).
	if err := h.nc.Flush(); err != nil {
		t.Fatalf("flush host subscription: %v", err)
	}

	obsNC, err := nats.Connect(natsTestURL)
	if err != nil {
		t.Fatalf("nats.Connect (observer): %v", err)
	}
	defer obsNC.Close()
	acks := newAckCollector(collectAcks(t, obsNC, ackSubject))

	pubNC, err := nats.Connect(natsTestURL)
	if err != nil {
		t.Fatalf("nats.Connect (publisher): %v", err)
	}
	defer pubNC.Close()

	const aggKey = "batch-agg"
	eventIDs := make([]string, 3)
	for i := range eventIDs {
		eventIDs[i] = fmt.Sprintf("evt-%s-%d", uniqueID(t), i)
		body := wireEvent(eventIDs[i], userID, fmt.Sprintf("Title %d", i), fmt.Sprintf("Message %d", i), "normal", aggKey, false)
		if err := pubNC.Publish(h.Subject(), body); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
	if err := pubNC.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	// Every event should get its own observed_by_agent ack first.
	for _, id := range eventIDs {
		acks.waitFor(t, id, "observed_by_agent", 5*time.Second)
	}

	// After the (shortened) aggregation window elapses, all three should be
	// submitted together as one batch, sharing one toastSubmittedAt.
	submitted := make(map[string]ackWire, 3)
	for _, id := range eventIDs {
		submitted[id] = acks.waitFor(t, id, "submitted_to_windows", 5*time.Second)
	}

	var sharedTS time.Time
	for id, a := range submitted {
		if a.ToastSubmittedAt == nil {
			t.Fatalf("submitted_to_windows ack for %s: ToastSubmittedAt = nil, want non-nil", id)
		}
		if sharedTS.IsZero() {
			sharedTS = *a.ToastSubmittedAt
		} else if !a.ToastSubmittedAt.Equal(sharedTS) {
			t.Fatalf("submitted_to_windows ack for %s: ToastSubmittedAt = %v, want shared value %v", id, *a.ToastSubmittedAt, sharedTS)
		}
	}

	calls := renderer.Calls()
	if len(calls) != 1 {
		t.Fatalf("renderer.Show call count = %d, want 1 (batched)", len(calls))
	}
	if len(calls[0].Sources) != 3 {
		t.Fatalf("renderer.Show req.Sources length = %d, want 3", len(calls[0].Sources))
	}
}

// TestStartLiveNATSReplaceableBatchSurvivorAckHasItsOwnAgentReceivedAt is a
// regression test for the fixed AgentReceivedAt-tracking leak: it exercises
// the steady-state "replaceable/progress" pattern (three replaceable events
// in the same bucket, each superseding the last) that used to orphan a
// receivedAt map entry per discarded event, and proves the surviving event's
// submitted_to_windows ack carries its own AgentReceivedAt -- not some
// earlier discarded event's -- now that the timestamp travels on the event
// itself instead of through a side map keyed by EventID.
func TestStartLiveNATSReplaceableBatchSurvivorAckHasItsOwnAgentReceivedAt(t *testing.T) {
	requireLiveNATS(t)

	userID := "u-" + uniqueID(t)
	ackSubject := "notify.ack.test." + uniqueID(t)
	opts := Options{
		NatsURL:         natsTestURL,
		SubjectTemplate: "notify.user.%s.desktop",
		AckSubject:      ackSubject,
	}
	idp := fixedIdentity{id: identity.Identity{UserID: userID, DeviceID: "d-test"}}
	renderer := &recordingRenderer{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := defaultDeps()
	d.aggregatorOptions = aggregator.Options{
		MaxBuckets:      100,
		ImportantWindow: 2 * time.Second,
		NormalWindow:    500 * time.Millisecond,
	}
	d.pipelineOptions = pipeline.Options{QueueCapacity: 500, WorkerCount: 2}

	h, err := start(ctx, opts, idp, renderer, nil, d)
	if err != nil {
		t.Fatalf("start() error = %v", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = h.Shutdown(shutdownCtx)
	}()
	if err := h.nc.Flush(); err != nil {
		t.Fatalf("flush host subscription: %v", err)
	}

	obsNC, err := nats.Connect(natsTestURL)
	if err != nil {
		t.Fatalf("nats.Connect (observer): %v", err)
	}
	defer obsNC.Close()
	acks := newAckCollector(collectAcks(t, obsNC, ackSubject))

	pubNC, err := nats.Connect(natsTestURL)
	if err != nil {
		t.Fatalf("nats.Connect (publisher): %v", err)
	}
	defer pubNC.Close()

	const aggKey = "progress-agg"
	base := "evt-" + uniqueID(t)
	eventIDs := []string{base + "-p1", base + "-p2", base + "-p3"}
	for i, id := range eventIDs {
		body := wireEvent(id, userID, fmt.Sprintf("Progress %d", i), fmt.Sprintf("%d%%", (i+1)*30), "normal", aggKey, true)
		if err := pubNC.Publish(h.Subject(), body); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
		// Small, deliberate gap so each event gets a distinguishably later
		// AgentReceivedAt -- without this, all three could land in the same
		// timestamp tick and the assertion below would pass vacuously.
		time.Sleep(20 * time.Millisecond)
	}
	if err := pubNC.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	// Every event is observed individually, before any bucketing/collapsing.
	observedTS := make(map[string]time.Time, len(eventIDs))
	for _, id := range eventIDs {
		a := acks.waitFor(t, id, "observed_by_agent", 5*time.Second)
		observedTS[id] = a.AgentReceivedAt
	}
	// Distinct timestamps for each event, proving they aren't sharing state.
	if observedTS[eventIDs[0]].Equal(observedTS[eventIDs[2]]) {
		t.Fatalf("expected p1 and p3 to have distinct AgentReceivedAt timestamps, both were %v", observedTS[eventIDs[0]])
	}

	survivorID := eventIDs[2] // p3: replaceable collapsing keeps only the latest
	submittedAck := acks.waitFor(t, survivorID, "submitted_to_windows", 5*time.Second)

	if !submittedAck.AgentReceivedAt.Equal(observedTS[survivorID]) {
		t.Fatalf("survivor submitted_to_windows ack: AgentReceivedAt = %v, want %v (its own observed_by_agent timestamp, not an earlier discarded event's)",
			submittedAck.AgentReceivedAt, observedTS[survivorID])
	}

	// The whole point of replaceable collapsing: exactly one render call, for
	// exactly the survivor -- p1/p2 never render (and so never get a
	// submitted_to_windows ack at all).
	calls := renderer.Calls()
	if len(calls) != 1 {
		t.Fatalf("renderer.Show call count = %d, want 1 (replaceable collapse)", len(calls))
	}
	if len(calls[0].Sources) != 1 || calls[0].Sources[0].EventID != survivorID {
		t.Fatalf("renderer.Show req.Sources = %+v, want exactly [%s]", calls[0].Sources, survivorID)
	}
}

// TestHostDelegatesDropCountersToPipelineAndAggregator proves Host.
// DroppedQueueFull/DroppedBucketOverflow correctly delegate to the
// underlying pipeline/aggregator rather than tracking their own (possibly
// stale) copy. Constructed directly (no live NATS needed) since this only
// exercises the delegation, not the full Start/subscribe wiring.
func TestHostDelegatesDropCountersToPipelineAndAggregator(t *testing.T) {
	clk := clock.RealClock{}
	dedupCache := dedup.NewCache(10, time.Minute, clk)
	pl := pipeline.New(pipeline.Options{QueueCapacity: 1, WorkerCount: 2}, dedupCache, func(*model.InboundNotification) {}, clk)
	// Run is deliberately never started: with nothing draining the queue,
	// filling it to capacity and then overflowing is deterministic.
	if !pl.TryEnqueue([]byte("a")) {
		t.Fatal("1st TryEnqueue: got false, want true (within capacity)")
	}
	if pl.TryEnqueue([]byte("b")) {
		t.Fatal("2nd TryEnqueue: got true, want false (queue full)")
	}

	agg := aggregator.New(aggregator.Options{MaxBuckets: 1}, clk, func([]*model.InboundNotification) {})
	agg.Add(&model.InboundNotification{
		Classification: model.Classification{Priority: model.PriorityNormal, AggregationKey: "a"},
	})
	agg.Add(&model.InboundNotification{
		Classification: model.Classification{Priority: model.PriorityNormal, AggregationKey: "b"}, // 2nd distinct key: over MaxBuckets=1, dropped
	})

	h := &Host{pipeline: pl, agg: agg}

	if got := h.DroppedQueueFull(); got != 1 {
		t.Errorf("Host.DroppedQueueFull() = %d, want 1", got)
	}
	if got := h.DroppedBucketOverflow(); got != 1 {
		t.Errorf("Host.DroppedBucketOverflow() = %d, want 1", got)
	}
}

func TestOptionsFromEnvDefaults(t *testing.T) {
	t.Setenv("NOTIFY_NATS_URL", "")
	t.Setenv("NOTIFY_SUBJECT_TEMPLATE", "")
	t.Setenv("NOTIFY_ACK_SUBJECT", "")

	opts := OptionsFromEnv()

	if opts.NatsURL != "nats://127.0.0.1:4222" {
		t.Errorf("NatsURL = %q, want default", opts.NatsURL)
	}
	if opts.SubjectTemplate != "notify.user.%s.desktop" {
		t.Errorf("SubjectTemplate = %q, want default", opts.SubjectTemplate)
	}
	if opts.AckSubject != "notify.ack.desktop" {
		t.Errorf("AckSubject = %q, want default", opts.AckSubject)
	}
}

func TestOptionsFromEnvOverrides(t *testing.T) {
	t.Setenv("NOTIFY_NATS_URL", "nats://example.invalid:4222")
	t.Setenv("NOTIFY_SUBJECT_TEMPLATE", "custom.%s.subject")
	t.Setenv("NOTIFY_ACK_SUBJECT", "custom.ack.subject")

	opts := OptionsFromEnv()

	if opts.NatsURL != "nats://example.invalid:4222" {
		t.Errorf("NatsURL = %q, want override", opts.NatsURL)
	}
	if opts.SubjectTemplate != "custom.%s.subject" {
		t.Errorf("SubjectTemplate = %q, want override", opts.SubjectTemplate)
	}
	if opts.AckSubject != "custom.ack.subject" {
		t.Errorf("AckSubject = %q, want override", opts.AckSubject)
	}
}

func TestStartFailsOnBadIdentity(t *testing.T) {
	idp := failingIdentity{}
	opts := Options{NatsURL: natsTestURL, SubjectTemplate: "notify.user.%s.desktop", AckSubject: "notify.ack.desktop"}

	h, err := Start(context.Background(), opts, idp, &recordingRenderer{}, nil)
	if err == nil {
		t.Fatalf("Start() error = nil, want non-nil when identity resolution fails")
	}
	if h != nil {
		t.Fatalf("Start() host = %v, want nil on failure", h)
	}
}

type failingIdentity struct{}

func (failingIdentity) Resolve(ctx context.Context) (identity.Identity, error) {
	return identity.Identity{}, fmt.Errorf("boom")
}

func TestStartFailsOnUnreachableNATS(t *testing.T) {
	idp := fixedIdentity{id: identity.Identity{UserID: "u1", DeviceID: "d1"}}
	opts := Options{
		NatsURL:         "nats://127.0.0.1:1", // nothing listens here
		SubjectTemplate: "notify.user.%s.desktop",
		AckSubject:      "notify.ack.desktop",
	}

	h, err := Start(context.Background(), opts, idp, &recordingRenderer{}, nil)
	if err == nil {
		t.Fatalf("Start() error = nil, want non-nil for unreachable NATS")
	}
	if h != nil {
		t.Fatalf("Start() host = %v, want nil on failure", h)
	}
}
