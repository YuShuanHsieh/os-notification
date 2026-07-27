package telemetry

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("failed to parse time %q: %v", s, err)
	}
	return ts
}

func TestToJSON_ObservedByAgent_OmitsToastSubmittedAtEntirely(t *testing.T) {
	ack := Ack{
		EventID:          "evt-1",
		DeviceID:         "d-1",
		AgentReceivedAt:  mustParseTime(t, "2026-07-15T08:30:00.190Z"),
		ToastSubmittedAt: nil,
		Status:           StatusObservedByAgent,
	}

	data, err := ack.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON() error: %v", err)
	}

	if strings.Contains(string(data), `"toastSubmittedAt"`) {
		t.Errorf("expected no toastSubmittedAt key at all, got: %s", data)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if _, ok := m["toastSubmittedAt"]; ok {
		t.Errorf("expected toastSubmittedAt to be absent from the decoded map, got: %v", m)
	}
	if m["status"] != string(StatusObservedByAgent) {
		t.Errorf("status = %v, want %q", m["status"], StatusObservedByAgent)
	}
	if m["eventId"] != "evt-1" {
		t.Errorf("eventId = %v, want %q", m["eventId"], "evt-1")
	}
	if m["deviceId"] != "d-1" {
		t.Errorf("deviceId = %v, want %q", m["deviceId"], "d-1")
	}
	if _, ok := m["agentReceivedAt"]; !ok {
		t.Errorf("expected agentReceivedAt key present, got: %v", m)
	}

	wantKeys := map[string]bool{"eventId": true, "deviceId": true, "agentReceivedAt": true, "status": true}
	if len(m) != len(wantKeys) {
		t.Errorf("expected exactly %d keys %v, got %d keys: %v", len(wantKeys), wantKeys, len(m), m)
	}
	for k := range m {
		if !wantKeys[k] {
			t.Errorf("unexpected key %q in observed ack JSON", k)
		}
	}
}

func TestToJSON_SubmittedToWindows_IncludesToastSubmittedAtAsRFC3339(t *testing.T) {
	submittedAt := mustParseTime(t, "2026-07-15T08:30:00.205Z")
	ack := Ack{
		EventID:          "evt-12345",
		DeviceID:         "d-456",
		AgentReceivedAt:  mustParseTime(t, "2026-07-15T08:30:00.190Z"),
		ToastSubmittedAt: &submittedAt,
		Status:           StatusSubmittedToWindows,
	}

	data, err := ack.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON() error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if m["eventId"] != "evt-12345" {
		t.Errorf("eventId = %v, want %q", m["eventId"], "evt-12345")
	}
	if m["deviceId"] != "d-456" {
		t.Errorf("deviceId = %v, want %q", m["deviceId"], "d-456")
	}
	if m["status"] != string(StatusSubmittedToWindows) {
		t.Errorf("status = %v, want %q", m["status"], StatusSubmittedToWindows)
	}

	toastRaw, ok := m["toastSubmittedAt"].(string)
	if !ok {
		t.Fatalf("expected toastSubmittedAt to be a string, got: %v (%T)", m["toastSubmittedAt"], m["toastSubmittedAt"])
	}
	gotTime, err := time.Parse(time.RFC3339Nano, toastRaw)
	if err != nil {
		t.Fatalf("toastSubmittedAt %q did not parse as RFC3339: %v", toastRaw, err)
	}
	if !gotTime.Equal(submittedAt) {
		t.Errorf("toastSubmittedAt = %v, want %v", gotTime, submittedAt)
	}

	agentRaw, ok := m["agentReceivedAt"].(string)
	if !ok {
		t.Fatalf("expected agentReceivedAt to be a string, got: %v", m["agentReceivedAt"])
	}
	if _, err := time.Parse(time.RFC3339Nano, agentRaw); err != nil {
		t.Errorf("agentReceivedAt %q did not parse as RFC3339: %v", agentRaw, err)
	}

	wantKeys := map[string]bool{
		"eventId": true, "deviceId": true, "agentReceivedAt": true,
		"toastSubmittedAt": true, "status": true,
	}
	if len(m) != len(wantKeys) {
		t.Errorf("expected exactly %d keys %v, got %d keys: %v", len(wantKeys), wantKeys, len(m), m)
	}
}

func TestStatus_ExactWireValues(t *testing.T) {
	if StatusObservedByAgent != "observed_by_agent" {
		t.Errorf("StatusObservedByAgent = %q, want %q", StatusObservedByAgent, "observed_by_agent")
	}
	if StatusSubmittedToWindows != "submitted_to_windows" {
		t.Errorf("StatusSubmittedToWindows = %q, want %q", StatusSubmittedToWindows, "submitted_to_windows")
	}
}
