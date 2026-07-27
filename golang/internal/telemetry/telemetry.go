// Package telemetry implements the acknowledgement JSON payload published to
// NATS after an event has been observed and, later, rendered, mirroring
// Acks.cs (C#) and ack.rs (Rust) elsewhere in this repository.
package telemetry

import (
	"encoding/json"
	"time"
)

// Status is one of the exact wire status strings the agent emits. The agent
// never emits any other value (e.g. backend-side classifications such as
// "published" or "unobserved") on this field.
type Status string

const (
	// StatusObservedByAgent is emitted once an event has been parsed and
	// deduplicated, before any renderer submission has happened.
	StatusObservedByAgent Status = "observed_by_agent"
	// StatusSubmittedToWindows is emitted once the toast has been handed to
	// the Windows renderer.
	StatusSubmittedToWindows Status = "submitted_to_windows"
)

// Ack is the acknowledgement telemetry payload published to NATS.
type Ack struct {
	EventID  string
	DeviceID string

	AgentReceivedAt time.Time
	// ToastSubmittedAt is nil until the toast has been submitted to Windows,
	// at which point ToJSON omits the field entirely rather than emitting it
	// as JSON null.
	ToastSubmittedAt *time.Time

	Status Status
}

// wireAck is the camelCase JSON shape of Ack. ToastSubmittedAt uses
// `omitempty` on a pointer, which is the idiomatic way in Go to omit a field
// entirely (rather than emit null) when it has no value.
type wireAck struct {
	EventID          string     `json:"eventId"`
	DeviceID         string     `json:"deviceId"`
	AgentReceivedAt  time.Time  `json:"agentReceivedAt"`
	ToastSubmittedAt *time.Time `json:"toastSubmittedAt,omitempty"`
	Status           Status     `json:"status"`
}

// ToJSON serializes the ack per the wire contract: camelCase keys, RFC3339
// timestamps, and ToastSubmittedAt omitted entirely (never emitted as null)
// when nil.
func (a Ack) ToJSON() ([]byte, error) {
	return json.Marshal(wireAck{
		EventID:          a.EventID,
		DeviceID:         a.DeviceID,
		AgentReceivedAt:  a.AgentReceivedAt,
		ToastSubmittedAt: a.ToastSubmittedAt,
		Status:           a.Status,
	})
}
