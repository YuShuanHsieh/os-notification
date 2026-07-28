// Package parser turns raw inbound wire JSON bytes into a normalized
// model.InboundNotification. It is a faithful Go port of the C#
// EventParser (src/NotificationAgent.Core/Serialization/EventParser.cs)
// and the Rust parser (rust/notify-agent-core/src/parser.rs): same size
// and depth limits, same required fields, same defaulting rules for
// priority/aggregation key/deduplication key/replaceable, and the same
// "drop just the image, not the event" behavior for invalid image URLs.
package parser

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/YuShuanHsieh/os-notification/golang/internal/httpsurl"
	"github.com/YuShuanHsieh/os-notification/golang/internal/model"
)

// MaxPayloadBytes is the largest inbound wire payload accepted.
const MaxPayloadBytes = 32 * 1024

// MaxJSONDepth is the deepest nesting of JSON objects/arrays accepted.
const MaxJSONDepth = 16

// maxImageURLLength mirrors the shared HTTPS URL policy's MaxURLLength
// (internal/httpsurl), kept as an alias so existing tests referencing this
// package-local name keep working. Action URLs are not validated here --
// that happens downstream at render time in both reference implementations.
const maxImageURLLength = httpsurl.MaxURLLength

// wire* types mirror the inbound JSON shape (camelCase field names). Every
// field is optional at the JSON level; required-ness is enforced by Parse
// after decoding, not by the struct shape, so a missing key and an explicit
// blank string are treated identically (matching the C#/Rust behavior of
// treating null/whitespace-only strings as absent).
type wireEvent struct {
	SchemaVersion    string              `json:"schemaVersion"`
	EventID          string              `json:"eventId"`
	NotificationType string              `json:"notificationType"`
	Target           *wireTarget         `json:"target"`
	Content          *wireContent        `json:"content"`
	Action           *wireAction         `json:"action"`
	Classification   *wireClassification `json:"classification"`
	Timestamps       *wireTimestamps     `json:"timestamps"`
}

type wireTarget struct {
	UserID string `json:"userId"`
}

type wireContent struct {
	Title         string     `json:"title"`
	Message       string     `json:"message"`
	SecondaryText string     `json:"secondaryText"`
	Image         *wireImage `json:"image"`
}

type wireImage struct {
	URL   string `json:"url"`
	Shape string `json:"shape"`
}

type wireAction struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type wireClassification struct {
	Priority         string `json:"priority"`
	AggregationKey   string `json:"aggregationKey"`
	DeduplicationKey string `json:"deduplicationKey"`
	Replaceable      bool   `json:"replaceable"`
}

type wireTimestamps struct {
	ProducerCreatedAt *time.Time `json:"producerCreatedAt"`
	ServerPublishedAt *time.Time `json:"serverPublishedAt"`
}

// Parse parses raw inbound wire JSON bytes into a model.InboundNotification.
// It returns a non-nil error for an oversize payload, JSON nested deeper
// than MaxJSONDepth, malformed/null JSON, or any missing/blank required
// field (eventId, target.userId, content.title, content.message). The
// caller is expected to drop the event silently on error; Parse never
// panics on untrusted input.
func Parse(payload []byte) (*model.InboundNotification, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("empty payload")
	}
	if len(payload) > MaxPayloadBytes {
		return nil, fmt.Errorf("payload %d bytes exceeds %d", len(payload), MaxPayloadBytes)
	}
	if depthExceeds(payload, MaxJSONDepth) {
		return nil, fmt.Errorf("json depth exceeds %d", MaxJSONDepth)
	}

	var wire wireEvent
	if err := json.Unmarshal(payload, &wire); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}

	eventID, err := requireField(wire.EventID, "eventId")
	if err != nil {
		return nil, err
	}

	var userID string
	if wire.Target != nil {
		userID = wire.Target.UserID
	}
	userID, err = requireField(userID, "target.userId")
	if err != nil {
		return nil, err
	}

	var content wireContent
	if wire.Content != nil {
		content = *wire.Content
	}

	title, err := requireField(content.Title, "content.title")
	if err != nil {
		return nil, err
	}
	message, err := requireField(content.Message, "content.message")
	if err != nil {
		return nil, err
	}

	notificationType := nonBlank(wire.NotificationType)

	var classification wireClassification
	if wire.Classification != nil {
		classification = *wire.Classification
	}
	priority := model.ParsePriority(strings.ToLower(classification.Priority))

	aggregationKey := nonBlank(classification.AggregationKey)
	if aggregationKey == "" {
		aggregationKey = notificationType
	}
	if aggregationKey == "" {
		aggregationKey = "unknown"
	}

	deduplicationKey := nonBlank(classification.DeduplicationKey)
	if deduplicationKey == "" {
		deduplicationKey = eventID
	}

	var action *model.Action
	if wire.Action != nil {
		label := nonBlank(wire.Action.Label)
		actionURL := nonBlank(wire.Action.URL)
		// Both label and URL are required to construct the bundled
		// model.Action -- see the parser package doc comment / EventParser
		// port notes for why this differs from the reference
		// implementations' independent optional fields.
		if label != "" && actionURL != "" {
			action = &model.Action{Label: label, URL: actionURL}
		}
	}

	image := parseImage(content.Image)

	var producerCreatedAt, serverPublishedAt time.Time
	if wire.Timestamps != nil {
		if wire.Timestamps.ProducerCreatedAt != nil {
			producerCreatedAt = *wire.Timestamps.ProducerCreatedAt
		}
		if wire.Timestamps.ServerPublishedAt != nil {
			serverPublishedAt = *wire.Timestamps.ServerPublishedAt
		}
	}

	return &model.InboundNotification{
		EventID:          eventID,
		NotificationType: notificationType,
		UserID:           userID,
		Title:            title,
		Message:          message,
		SecondaryText:    nonBlank(content.SecondaryText),
		Image:            image,
		Action:           action,
		Classification: model.Classification{
			Priority:         priority,
			AggregationKey:   aggregationKey,
			DeduplicationKey: deduplicationKey,
			Replaceable:      classification.Replaceable,
		},
		ProducerCreatedAt: producerCreatedAt,
		ServerPublishedAt: serverPublishedAt,
	}, nil
}

// nonBlank returns s unchanged if it contains any non-whitespace character,
// or "" if s is empty or whitespace-only. This mirrors the C#
// string.IsNullOrWhiteSpace / Rust non_blank helper: a field's absence and
// an explicit blank string are treated identically.
func nonBlank(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return s
}

func requireField(s, field string) (string, error) {
	v := nonBlank(s)
	if v == "" {
		return "", fmt.Errorf("missing %s", field)
	}
	return v, nil
}

// parseImage is best-effort: any invalid image spec yields nil (the rest of
// the event is unaffected), matching the Rust/C# "drop just the image"
// behavior for structurally invalid or oversized image URLs.
func parseImage(wire *wireImage) *model.Image {
	if wire == nil {
		return nil
	}
	imgURL := nonBlank(wire.URL)
	if imgURL == "" {
		return nil
	}
	if !httpsurl.Valid(imgURL) {
		return nil
	}
	shape := model.ParseImageShape(strings.ToLower(wire.Shape))
	return &model.Image{URL: imgURL, Shape: shape}
}

// depthExceeds is a string-aware structural scan of the raw JSON bytes that
// enforces the depth limit before the full parse (Go's encoding/json has no
// configurable depth limit). Ported directly from the Rust parser's
// depth_exceeds.
func depthExceeds(payload []byte, maxDepth int) bool {
	depth := 0
	inString := false
	escaped := false
	for _, b := range payload {
		if inString {
			switch {
			case escaped:
				escaped = false
			case b == '\\':
				escaped = true
			case b == '"':
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString = true
		case '{', '[':
			depth++
			if depth > maxDepth {
				return true
			}
		case '}', ']':
			if depth > 0 {
				depth--
			}
		}
	}
	return false
}
