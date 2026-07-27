// Package model holds the normalized, wire-format-independent notification
// event shape shared across the parser, pipeline, aggregator, and renderers.
package model

import "time"

// Priority is the classification priority used for aggregation routing.
// It has exactly three levels; "replaceable" is an orthogonal boolean on
// Classification, not a fourth priority level.
type Priority int

const (
	PriorityNormal Priority = iota
	PriorityImportant
	PriorityCritical
)

// ParsePriority maps a wire priority string to a Priority, defaulting
// unknown or missing values to PriorityNormal per the wire contract.
func ParsePriority(s string) Priority {
	switch s {
	case "critical":
		return PriorityCritical
	case "important":
		return PriorityImportant
	default:
		return PriorityNormal
	}
}

func (p Priority) String() string {
	switch p {
	case PriorityCritical:
		return "critical"
	case PriorityImportant:
		return "important"
	default:
		return "normal"
	}
}

// ImageShape is the crop shape applied to an avatar image.
type ImageShape int

const (
	ImageShapeCircle ImageShape = iota
	ImageShapeSquare
)

func ParseImageShape(s string) ImageShape {
	if s == "square" {
		return ImageShapeSquare
	}
	return ImageShapeCircle
}

func (s ImageShape) String() string {
	if s == ImageShapeSquare {
		return "square"
	}
	return "circle"
}

// Image is an optional avatar image attached to a notification's content.
type Image struct {
	URL   string
	Shape ImageShape
}

// Action is an optional single action button attached to a notification.
type Action struct {
	Label string
	URL   string
}

// Classification carries the aggregation/dedup/priority routing fields.
type Classification struct {
	Priority         Priority
	AggregationKey   string
	DeduplicationKey string
	Replaceable      bool
}

// InboundNotification is the normalized, parsed representation of an
// inbound wire event — the shape every downstream stage (dedup, aggregator,
// toast content) operates on.
type InboundNotification struct {
	EventID           string
	NotificationType  string
	UserID            string
	Title             string
	Message           string
	SecondaryText     string  // "" means absent
	Image             *Image  // nil means absent
	Action            *Action // nil means absent
	Classification    Classification
	ProducerCreatedAt time.Time
	ServerPublishedAt time.Time
}
