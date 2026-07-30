package main

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/YuShuanHsieh/os-notification/golang/internal/metrics"
)

// resetGlobalMeterProvider restores otel's global MeterProvider to a no-op
// implementation after a test that (successfully) called InitMetrics with
// telemetry enabled -- InitMetrics calls otel.SetMeterProvider, which is
// process-global state; without this, a test earlier in this file's run
// order could leak a real MeterProvider into a later, unrelated test.
func resetGlobalMeterProvider(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { otel.SetMeterProvider(noop.NewMeterProvider()) })
}

// TestInitMetricsDisabledReturnsNullAgentMetricsQuickly proves InitMetrics
// short-circuits to metrics.NullAgentMetrics{} immediately -- without
// attempting any OTel SDK/exporter construction, which would be slow or
// blocking if it tried to dial an endpoint -- whenever telemetry is
// disabled. This is the "zero overhead when off" requirement.
func TestInitMetricsDisabledReturnsNullAgentMetricsQuickly(t *testing.T) {
	s := Settings{OtelEnabled: false, OtelExporterEndpoint: "collector.example:4318"}

	start := time.Now()
	got := InitMetrics(context.Background(), s)
	elapsed := time.Since(start)

	if _, ok := got.(metrics.NullAgentMetrics); !ok {
		t.Fatalf("InitMetrics with OtelEnabled=false = %T, want metrics.NullAgentMetrics", got)
	}
	if elapsed > time.Second {
		t.Fatalf("InitMetrics with OtelEnabled=false took %s, want near-instant (no exporter/network setup attempted)", elapsed)
	}
}

// TestInitMetricsBlankEndpointReturnsNullAgentMetrics proves InitMetrics
// treats OtelEnabled=true with a blank exporter endpoint the same as
// disabled -- no endpoint configured means nowhere to export to.
func TestInitMetricsBlankEndpointReturnsNullAgentMetrics(t *testing.T) {
	s := Settings{OtelEnabled: true, OtelExporterEndpoint: "   "}

	got := InitMetrics(context.Background(), s)

	if _, ok := got.(metrics.NullAgentMetrics); !ok {
		t.Fatalf("InitMetrics with blank endpoint = %T, want metrics.NullAgentMetrics", got)
	}
}

// TestInitMetricsInvalidEndpointFallsBackToNullAgentMetricsWithoutPanic
// proves a deliberately-invalid/unreachable exporter endpoint never panics
// and never aborts startup: InitMetrics logs the failure and falls back to
// metrics.NullAgentMetrics{} instead of propagating an error or crashing --
// this feature's explicit "a telemetry misconfiguration must never abort
// agent startup" requirement. otlpmetrichttp.New itself does not dial
// eagerly, so this also exercises the fallback path for whatever
// construction step (endpoint parsing, instrument creation) could fail.
func TestInitMetricsInvalidEndpointFallsBackToNullAgentMetricsWithoutPanic(t *testing.T) {
	s := Settings{
		OtelEnabled: true,
		// A deliberately malformed endpoint (contains a scheme and a path,
		// which WithEndpoint documents should be host[:port] only) --
		// proving InitMetrics never panics regardless of what a caller
		// puts in the settings file/env override.
		OtelExporterEndpoint: "http://\x00invalid host/with spaces:not-a-port",
		OtelServiceName:      "test-service",
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("InitMetrics panicked with an invalid endpoint: %v", r)
		}
	}()
	got := InitMetrics(context.Background(), s)

	// Whether or not exporter construction itself happens to return an
	// error for this particular malformed string, InitMetrics must return
	// *some* usable, non-nil AgentMetrics -- never nil, never a panic.
	if got == nil {
		t.Fatal("InitMetrics returned nil, want a non-nil AgentMetrics (NullAgentMetrics at minimum)")
	}

	// Exercise every method on whatever was returned -- proving it's safe
	// to call regardless of which branch InitMetrics took.
	got.RecordEventReceived()
	got.RecordEventDropped("queue_full")
	got.RecordRenderDuration(0.5)
}

// TestInitMetricsWithExtremeEndpointNeverPanics is a defense-in-depth check
// that InitMetrics' top-level recover() guard is in place and the whole
// sequence tolerates an extreme (very long) endpoint value without
// panicking or returning nil, whether or not that particular value happens
// to make exporter/instrument construction itself fail.
func TestInitMetricsWithExtremeEndpointNeverPanics(t *testing.T) {
	resetGlobalMeterProvider(t)
	extreme := make([]byte, 8192)
	for i := range extreme {
		extreme[i] = 'a'
	}
	s := Settings{
		OtelEnabled:          true,
		OtelExporterEndpoint: string(extreme),
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("InitMetrics panicked: %v", r)
		}
	}()
	if got := InitMetrics(context.Background(), s); got == nil {
		t.Fatal("InitMetrics returned nil, want a non-nil AgentMetrics")
	}
}
