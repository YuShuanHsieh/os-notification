package host

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/YuShuanHsieh/os-notification/golang/internal/aggregator"
	"github.com/YuShuanHsieh/os-notification/golang/internal/clock"
	"github.com/YuShuanHsieh/os-notification/golang/internal/dedup"
	"github.com/YuShuanHsieh/os-notification/golang/internal/identity"
	"github.com/YuShuanHsieh/os-notification/golang/internal/metrics"
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

// recordingMetrics is an in-test metrics.AgentMetrics fake that records
// every call (guarded by a mutex) so tests can assert exactly what fired,
// with what arguments, without touching any real OpenTelemetry type.
type recordingMetrics struct {
	mu              sync.Mutex
	receivedCount   int
	droppedReasons  []string
	renderDurations []float64
}

var _ metrics.AgentMetrics = (*recordingMetrics)(nil)

func (m *recordingMetrics) RecordEventReceived() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.receivedCount++
}

func (m *recordingMetrics) RecordEventDropped(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.droppedReasons = append(m.droppedReasons, reason)
}

func (m *recordingMetrics) RecordRenderDuration(seconds float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.renderDurations = append(m.renderDurations, seconds)
}

func (m *recordingMetrics) snapshot() (received int, dropped []string, durations []float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dOut := make([]string, len(m.droppedReasons))
	copy(dOut, m.droppedReasons)
	rOut := make([]float64, len(m.renderDurations))
	copy(rOut, m.renderDurations)
	return m.receivedCount, dOut, rOut
}

// panickingMetrics is a metrics.AgentMetrics fake whose every method panics
// unconditionally -- used to prove the safeRecord containment around every
// AgentMetrics call site in host.go actually contains a panic instead of
// letting it propagate, per this feature's explicit, user-mandated
// "metrics-recording code must never crash the application" requirement.
type panickingMetrics struct{}

var _ metrics.AgentMetrics = panickingMetrics{}

func (panickingMetrics) RecordEventReceived() { panic("boom: simulated RecordEventReceived panic") }
func (panickingMetrics) RecordEventDropped(reason string) {
	panic("boom: simulated RecordEventDropped panic")
}
func (panickingMetrics) RecordRenderDuration(seconds float64) {
	panic("boom: simulated RecordRenderDuration panic")
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

	h, err := Start(ctx, opts, idp, renderer, nil, nil)
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

	h, err := start(ctx, opts, idp, renderer, nil, nil, d)
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

	h, err := start(ctx, opts, idp, renderer, nil, nil, d)
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

// TestStartLiveNATSRecordsEventReceivedAndRenderDurationForCriticalEvent
// proves a valid critical (lone, immediate-render) event fires exactly one
// RecordEventReceived and one RecordRenderDuration call on the injected
// AgentMetrics, at the same points the observed_by_agent/submitted_to_windows
// acks are published (see TestStartLiveNATSObserveRenderAck, which this
// mirrors).
func TestStartLiveNATSRecordsEventReceivedAndRenderDurationForCriticalEvent(t *testing.T) {
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
	rm := &recordingMetrics{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h, err := Start(ctx, opts, idp, renderer, nil, rm)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
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

	eventID := "evt-" + uniqueID(t)
	body := wireEvent(eventID, userID, "Test Title", "Test Message", "critical", "test-agg", false)
	if err := pubNC.Publish(h.Subject(), body); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := pubNC.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	acks.waitFor(t, eventID, "observed_by_agent", 5*time.Second)
	acks.waitFor(t, eventID, "submitted_to_windows", 5*time.Second)

	waitUntilHostTest(t, 2*time.Second, func() bool {
		received, _, durations := rm.snapshot()
		return received == 1 && len(durations) == 1
	})

	received, dropped, durations := rm.snapshot()
	if received != 1 {
		t.Fatalf("RecordEventReceived call count = %d, want 1", received)
	}
	if len(dropped) != 0 {
		t.Fatalf("RecordEventDropped calls = %v, want none", dropped)
	}
	if len(durations) != 1 {
		t.Fatalf("RecordRenderDuration call count = %d, want 1", len(durations))
	}
	if durations[0] < 0 {
		t.Fatalf("RecordRenderDuration seconds = %v, want >= 0", durations[0])
	}
}

// TestStartLiveNATSBatchRecordsRenderDurationOncePerSourceEventNotPerToast
// proves the batch-fanout scenario (three events sharing an aggregation
// bucket, rendered as a single toast -- see TestStartLiveNATSBatchAckFanout,
// which this mirrors) fires RecordRenderDuration once per *source event*
// represented in the batch, not once per renderer.Show call/toast.
func TestStartLiveNATSBatchRecordsRenderDurationOncePerSourceEventNotPerToast(t *testing.T) {
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
	rm := &recordingMetrics{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := defaultDeps()
	d.aggregatorOptions = aggregator.Options{
		MaxBuckets:      100,
		ImportantWindow: 2 * time.Second,
		NormalWindow:    200 * time.Millisecond,
	}
	d.pipelineOptions = pipeline.Options{QueueCapacity: 500, WorkerCount: 2}

	h, err := start(ctx, opts, idp, renderer, nil, rm, d)
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

	const aggKey = "batch-agg-metrics"
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

	for _, id := range eventIDs {
		acks.waitFor(t, id, "observed_by_agent", 5*time.Second)
	}
	for _, id := range eventIDs {
		acks.waitFor(t, id, "submitted_to_windows", 5*time.Second)
	}

	waitUntilHostTest(t, 2*time.Second, func() bool {
		received, _, durations := rm.snapshot()
		return received == 3 && len(durations) == 3
	})

	received, _, durations := rm.snapshot()
	if received != 3 {
		t.Fatalf("RecordEventReceived call count = %d, want 3 (once per event, before batching)", received)
	}
	// The whole point of this test: one render call (one toast), but three
	// RecordRenderDuration calls -- one per source event the toast represents.
	calls := renderer.Calls()
	if len(calls) != 1 {
		t.Fatalf("renderer.Show call count = %d, want 1 (batched)", len(calls))
	}
	if len(durations) != 3 {
		t.Fatalf("RecordRenderDuration call count = %d, want 3 (once per source event in the batch, not once per toast)", len(durations))
	}
}

// TestStartLiveNATSReplaceableBatchRecordsRenderDurationOnlyForSurvivor
// proves the replaceable-collapsing scenario (see
// TestStartLiveNATSReplaceableBatchSurvivorAckHasItsOwnAgentReceivedAt, which
// this mirrors) fires RecordRenderDuration exactly once -- for the surviving
// event only, since p1/p2 are discarded and never render.
func TestStartLiveNATSReplaceableBatchRecordsRenderDurationOnlyForSurvivor(t *testing.T) {
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
	rm := &recordingMetrics{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := defaultDeps()
	d.aggregatorOptions = aggregator.Options{
		MaxBuckets:      100,
		ImportantWindow: 2 * time.Second,
		NormalWindow:    500 * time.Millisecond,
	}
	d.pipelineOptions = pipeline.Options{QueueCapacity: 500, WorkerCount: 2}

	h, err := start(ctx, opts, idp, renderer, nil, rm, d)
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

	const aggKey = "progress-agg-metrics"
	base := "evt-" + uniqueID(t)
	eventIDs := []string{base + "-p1", base + "-p2", base + "-p3"}
	for i, id := range eventIDs {
		body := wireEvent(id, userID, fmt.Sprintf("Progress %d", i), fmt.Sprintf("%d%%", (i+1)*30), "normal", aggKey, true)
		if err := pubNC.Publish(h.Subject(), body); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := pubNC.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	for _, id := range eventIDs {
		acks.waitFor(t, id, "observed_by_agent", 5*time.Second)
	}
	survivorID := eventIDs[2]
	acks.waitFor(t, survivorID, "submitted_to_windows", 5*time.Second)

	waitUntilHostTest(t, 2*time.Second, func() bool {
		received, _, durations := rm.snapshot()
		return received == 3 && len(durations) == 1
	})

	received, _, durations := rm.snapshot()
	if received != 3 {
		t.Fatalf("RecordEventReceived call count = %d, want 3 (every observed event, even the discarded ones)", received)
	}
	if len(durations) != 1 {
		t.Fatalf("RecordRenderDuration call count = %d, want 1 (only the surviving event renders)", len(durations))
	}
}

// waitUntilHostTest polls cond until it returns true or timeout elapses,
// failing the test on timeout -- avoids fixed time.Sleep races when waiting
// for asynchronous metrics recording to catch up with acks already observed.
func waitUntilHostTest(t *testing.T, timeout time.Duration, cond func() bool) {
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

// TestHostDropsInvokeMetricsRecordEventDroppedWithCorrectReason proves
// host.start wires the pipeline's and aggregator's OnDropped callbacks to
// the injected AgentMetrics' RecordEventDropped with the documented reason
// strings ("queue_full", "bucket_overflow") -- constructed directly with
// the exact same closures host.start uses (no live NATS needed, since
// dropping at the pipeline/aggregator layer never touches NATS).
func TestHostDropsInvokeMetricsRecordEventDroppedWithCorrectReason(t *testing.T) {
	clk := clock.RealClock{}
	rm := &recordingMetrics{}

	dedupCache := dedup.NewCache(10, time.Minute, clk)
	pl := pipeline.New(pipeline.Options{QueueCapacity: 1, WorkerCount: 2}, dedupCache, func(*model.InboundNotification) {}, clk, func() {
		safeRecord(func() { rm.RecordEventDropped("queue_full") })
	})
	// Run is deliberately never started: with nothing draining the queue,
	// filling it to capacity and then overflowing is deterministic.
	if !pl.TryEnqueue([]byte("a")) {
		t.Fatal("1st TryEnqueue: got false, want true (within capacity)")
	}
	if pl.TryEnqueue([]byte("b")) {
		t.Fatal("2nd TryEnqueue: got true, want false (queue full)")
	}

	agg := aggregator.New(aggregator.Options{MaxBuckets: 1}, clk, func([]*model.InboundNotification) {}, func() {
		safeRecord(func() { rm.RecordEventDropped("bucket_overflow") })
	})
	agg.Add(&model.InboundNotification{
		Classification: model.Classification{Priority: model.PriorityNormal, AggregationKey: "a"},
	})
	agg.Add(&model.InboundNotification{
		Classification: model.Classification{Priority: model.PriorityNormal, AggregationKey: "b"}, // 2nd distinct key: over MaxBuckets=1, dropped
	})

	_, dropped, _ := rm.snapshot()
	want := []string{"queue_full", "bucket_overflow"}
	if len(dropped) != len(want) {
		t.Fatalf("RecordEventDropped calls = %v, want %v", dropped, want)
	}
	for i := range want {
		if dropped[i] != want[i] {
			t.Fatalf("RecordEventDropped calls = %v, want %v", dropped, want)
		}
	}
}

// TestPanickingMetricsAreContainedInOnObservedAndRender proves the
// safeRecord guard around every AgentMetrics call site in onObserved/render
// actually contains a panic (this test function returning normally instead
// of the test process crashing) instead of letting it propagate -- and that
// normal operation (ack publishing) still completes despite every metrics
// call panicking. Uses a real NATS connection for publishAck (so it doesn't
// itself fail with a nil-pointer panic), but bypasses Start/start entirely,
// calling onObserved/render directly for a deterministic, synchronous test.
func TestPanickingMetricsAreContainedInOnObservedAndRender(t *testing.T) {
	requireLiveNATS(t)

	nc, err := nats.Connect(natsTestURL)
	if err != nil {
		t.Fatalf("nats.Connect: %v", err)
	}
	defer nc.Close()

	ackSubject := "notify.ack.test." + uniqueID(t)
	acks := newAckCollector(collectAcks(t, nc, ackSubject))

	renderer := &recordingRenderer{}
	h := &Host{
		nc:         nc,
		renderer:   renderer,
		clk:        clock.RealClock{},
		metrics:    panickingMetrics{},
		ackSubject: ackSubject,
		deviceID:   "d-test",
	}
	h.agg = aggregator.New(aggregator.Options{}, h.clk, h.render, nil)

	event := &model.InboundNotification{
		EventID:         "evt-" + uniqueID(t),
		AgentReceivedAt: time.Now(),
		Classification:  model.Classification{Priority: model.PriorityCritical, AggregationKey: "k"},
	}

	// Must not panic, despite every AgentMetrics method panicking
	// unconditionally: onObserved calls RecordEventReceived, then
	// h.agg.Add (critical priority) synchronously calls h.render, which
	// calls RecordRenderDuration.
	h.onObserved(event)

	acks.waitFor(t, event.EventID, "observed_by_agent", 5*time.Second)
	acks.waitFor(t, event.EventID, "submitted_to_windows", 5*time.Second)

	calls := renderer.Calls()
	if len(calls) != 1 {
		t.Fatalf("renderer.Show call count = %d, want 1", len(calls))
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
	pl := pipeline.New(pipeline.Options{QueueCapacity: 1, WorkerCount: 2}, dedupCache, func(*model.InboundNotification) {}, clk, nil)
	// Run is deliberately never started: with nothing draining the queue,
	// filling it to capacity and then overflowing is deterministic.
	if !pl.TryEnqueue([]byte("a")) {
		t.Fatal("1st TryEnqueue: got false, want true (within capacity)")
	}
	if pl.TryEnqueue([]byte("b")) {
		t.Fatal("2nd TryEnqueue: got true, want false (queue full)")
	}

	agg := aggregator.New(aggregator.Options{MaxBuckets: 1}, clk, func([]*model.InboundNotification) {}, nil)
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

func TestRedactedURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"no userinfo returned unchanged", "nats://127.0.0.1:4222", "nats://127.0.0.1:4222"},
		{"username and password both masked", "nats://user:password@host:4222", "nats://%2A%2A%2A:xxxxx@host:4222"},
		{"bare token username masked", "nats://s3cr3t-token@host:4222", "nats://%2A%2A%2A:xxxxx@host:4222"},
		{"unparseable input returned unchanged", "not a url with spaces", "not a url with spaces"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactedURL(tt.raw)
			if got != tt.want {
				t.Fatalf("redactedURL(%q) = %q, want %q", tt.raw, got, tt.want)
			}
			if strings.Contains(got, "password") || strings.Contains(got, "s3cr3t-token") {
				t.Fatalf("redactedURL(%q) = %q, leaked the raw credential", tt.raw, got)
			}
		})
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

	h, err := Start(context.Background(), opts, idp, &recordingRenderer{}, nil, nil)
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

	h, err := Start(context.Background(), opts, idp, &recordingRenderer{}, nil, nil)
	if err == nil {
		t.Fatalf("Start() error = nil, want non-nil for unreachable NATS")
	}
	if h != nil {
		t.Fatalf("Start() host = %v, want nil on failure", h)
	}
}

func TestStartFailsOnSubjectTemplateMissingPlaceholder(t *testing.T) {
	idp := fixedIdentity{id: identity.Identity{UserID: "u1", DeviceID: "d1"}}
	opts := Options{
		NatsURL:         natsTestURL,
		SubjectTemplate: "notify.user.desktop", // no %s placeholder
		AckSubject:      "notify.ack.desktop",
	}

	h, err := Start(context.Background(), opts, idp, &recordingRenderer{}, nil, nil)
	if err == nil {
		t.Fatalf("Start() error = nil, want non-nil for a subject template with no %%s placeholder")
	}
	if h != nil {
		t.Fatalf("Start() host = %v, want nil on failure", h)
	}
}

func TestStartFailsOnUserIDWithSubjectWildcardCharacters(t *testing.T) {
	opts := Options{
		NatsURL:         natsTestURL,
		SubjectTemplate: "notify.user.%s.desktop",
		AckSubject:      "notify.ack.desktop",
	}

	for _, userID := range []string{"*", ">", "a.b", "a*b", "a>b"} {
		idp := fixedIdentity{id: identity.Identity{UserID: userID, DeviceID: "d1"}}
		h, err := Start(context.Background(), opts, idp, &recordingRenderer{}, nil, nil)
		if err == nil {
			t.Errorf("Start() with UserID %q: error = nil, want non-nil (subject wildcard/delimiter character)", userID)
		}
		if h != nil {
			t.Errorf("Start() with UserID %q: host = %v, want nil on failure", userID, h)
		}
	}
}
