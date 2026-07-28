package main

import "time"

// rfc3339Millis matches chrono's to_rfc3339_opts(SecondsFormat::Millis, true):
// millisecond precision, "Z" suffix for UTC rather than "+00:00".
const rfc3339Millis = "2006-01-02T15:04:05.000Z07:00"

// buildPayload builds the wire JSON payload for one notification event,
// mirroring rust/test-publisher/src/payload.rs's build_payload. Optional
// fields are omitted entirely (never emitted as null) when unset. Using
// map[string]interface{} here is deliberate: encoding/json sorts map keys
// alphabetically on marshal, matching serde_json's default (non-
// preserve_order) Map, which is backed by a BTreeMap — so key order in the
// emitted JSON matches the Rust reference's output.
func buildPayload(spec *PublishSpec, message string, eventID string) map[string]interface{} {
	content := map[string]interface{}{
		"title":   spec.Title,
		"message": message,
	}
	if spec.Secondary != nil {
		content["secondaryText"] = *spec.Secondary
	}
	if spec.ImageURL != nil {
		content["image"] = map[string]interface{}{
			"url":   *spec.ImageURL,
			"shape": spec.ImageShape,
		}
	}

	schemaVersion := "1.0"
	if spec.ImageURL != nil {
		schemaVersion = "1.1"
	}

	payload := map[string]interface{}{
		"schemaVersion":    schemaVersion,
		"eventId":          eventID,
		"notificationType": spec.NotificationType,
		"target": map[string]interface{}{
			"userId": spec.UserID,
		},
		"content": content,
	}

	if spec.ActionLabel != nil && spec.ActionURL != nil {
		payload["action"] = map[string]interface{}{
			"label": *spec.ActionLabel,
			"url":   *spec.ActionURL,
		}
	}

	aggregationKey := spec.NotificationType
	if spec.AggKey != nil {
		aggregationKey = *spec.AggKey
	}
	deduplicationKey := eventID
	if spec.DedupKey != nil {
		deduplicationKey = *spec.DedupKey
	}
	payload["classification"] = map[string]interface{}{
		"priority":         spec.Priority,
		"aggregationKey":   aggregationKey,
		"deduplicationKey": deduplicationKey,
		"replaceable":      spec.Replaceable,
	}

	producerCreatedAt := time.Now().UTC().Format(rfc3339Millis)
	serverPublishedAt := time.Now().UTC().Format(rfc3339Millis)
	payload["timestamps"] = map[string]interface{}{
		"producerCreatedAt": producerCreatedAt,
		"serverPublishedAt": serverPublishedAt,
	}

	return payload
}
