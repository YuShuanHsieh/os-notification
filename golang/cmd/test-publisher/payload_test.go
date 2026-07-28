package main

import (
	"testing"
	"time"
)

func TestSchemaVersionIs10WithoutAnImage(t *testing.T) {
	spec := Defaults("u1")
	spec.ImageURL = nil
	payload := buildPayload(&spec, "hello", "evt-1")
	if payload["schemaVersion"] != "1.0" {
		t.Errorf("schemaVersion: got %v", payload["schemaVersion"])
	}
	content := payload["content"].(map[string]interface{})
	if _, ok := content["image"]; ok {
		t.Error("content.image should be absent")
	}
}

func TestSchemaVersionIs11WithAnImage(t *testing.T) {
	spec := Defaults("u1")
	spec.ImageURL = strPtr("https://img")
	spec.ImageShape = "square"
	payload := buildPayload(&spec, "hello", "evt-1")
	if payload["schemaVersion"] != "1.1" {
		t.Errorf("schemaVersion: got %v", payload["schemaVersion"])
	}
	content := payload["content"].(map[string]interface{})
	image := content["image"].(map[string]interface{})
	if image["url"] != "https://img" {
		t.Errorf("image.url: got %v", image["url"])
	}
	if image["shape"] != "square" {
		t.Errorf("image.shape: got %v", image["shape"])
	}
}

func TestContentOmitsSecondaryTextWhenAbsent(t *testing.T) {
	spec := Defaults("u1")
	spec.Secondary = nil
	payload := buildPayload(&spec, "hello", "evt-1")
	content := payload["content"].(map[string]interface{})
	if _, ok := content["secondaryText"]; ok {
		t.Error("content.secondaryText should be absent")
	}
}

func TestContentIncludesSecondaryTextWhenPresent(t *testing.T) {
	spec := Defaults("u1") // secondary = Some("TestPublisher")
	payload := buildPayload(&spec, "hello", "evt-1")
	content := payload["content"].(map[string]interface{})
	if content["secondaryText"] != "TestPublisher" {
		t.Errorf("secondaryText: got %v", content["secondaryText"])
	}
	if content["title"] != spec.Title {
		t.Errorf("title: got %v", content["title"])
	}
	if content["message"] != "hello" {
		t.Errorf("message: got %v", content["message"])
	}
}

func TestActionIncludedOnlyWhenBothLabelAndURLSet(t *testing.T) {
	spec := Defaults("u1")
	spec.ActionURL = nil // label still Some("View") from defaults
	payload := buildPayload(&spec, "hello", "evt-1")
	if _, ok := payload["action"]; ok {
		t.Error("action should be absent")
	}

	spec.ActionURL = strPtr("https://x")
	payload = buildPayload(&spec, "hello", "evt-1")
	action := payload["action"].(map[string]interface{})
	if action["label"] != "View" {
		t.Errorf("label: got %v", action["label"])
	}
	if action["url"] != "https://x" {
		t.Errorf("url: got %v", action["url"])
	}
}

func TestClassificationDefaultsAggKeyToTypeAndDedupKeyToEventID(t *testing.T) {
	spec := Defaults("u1") // AggKey = nil, DedupKey = nil
	payload := buildPayload(&spec, "hello", "evt-1")
	classification := payload["classification"].(map[string]interface{})
	if classification["aggregationKey"] != spec.NotificationType {
		t.Errorf("aggregationKey: got %v", classification["aggregationKey"])
	}
	if classification["deduplicationKey"] != "evt-1" {
		t.Errorf("deduplicationKey: got %v", classification["deduplicationKey"])
	}
	if classification["priority"] != spec.Priority {
		t.Errorf("priority: got %v", classification["priority"])
	}
	if classification["replaceable"] != false {
		t.Errorf("replaceable: got %v", classification["replaceable"])
	}
}

func TestClassificationUsesExplicitAggAndDedupKeysWhenSet(t *testing.T) {
	spec := Defaults("u1")
	spec.AggKey = strPtr("custom-agg")
	spec.DedupKey = strPtr("custom-dedup")
	payload := buildPayload(&spec, "hello", "evt-1")
	classification := payload["classification"].(map[string]interface{})
	if classification["aggregationKey"] != "custom-agg" {
		t.Errorf("aggregationKey: got %v", classification["aggregationKey"])
	}
	if classification["deduplicationKey"] != "custom-dedup" {
		t.Errorf("deduplicationKey: got %v", classification["deduplicationKey"])
	}
}

func TestTimestampsAreValidRFC3339(t *testing.T) {
	spec := Defaults("u1")
	payload := buildPayload(&spec, "hello", "evt-1")
	timestamps := payload["timestamps"].(map[string]interface{})
	producer := timestamps["producerCreatedAt"].(string)
	server := timestamps["serverPublishedAt"].(string)
	if _, err := time.Parse(time.RFC3339, producer); err != nil {
		t.Errorf("producerCreatedAt %q did not parse as RFC3339: %v", producer, err)
	}
	if _, err := time.Parse(time.RFC3339, server); err != nil {
		t.Errorf("serverPublishedAt %q did not parse as RFC3339: %v", server, err)
	}
}

func TestTargetAndEventIDAndNotificationTypeAreSet(t *testing.T) {
	spec := Defaults("u1")
	payload := buildPayload(&spec, "hello", "evt-1")
	target := payload["target"].(map[string]interface{})
	if target["userId"] != "u1" {
		t.Errorf("userId: got %v", target["userId"])
	}
	if payload["eventId"] != "evt-1" {
		t.Errorf("eventId: got %v", payload["eventId"])
	}
	if payload["notificationType"] != spec.NotificationType {
		t.Errorf("notificationType: got %v", payload["notificationType"])
	}
}
