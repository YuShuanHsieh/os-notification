use serde_json::Value;

use crate::spec::PublishSpec;

pub fn build_payload(spec: &PublishSpec, message: &str, event_id: &str) -> Value {
    let mut content = serde_json::Map::new();
    content.insert("title".to_string(), Value::String(spec.title.clone()));
    content.insert("message".to_string(), Value::String(message.to_string()));
    if let Some(secondary) = &spec.secondary {
        content.insert("secondaryText".to_string(), Value::String(secondary.clone()));
    }
    if let Some(image_url) = &spec.image_url {
        content.insert(
            "image".to_string(),
            serde_json::json!({ "url": image_url, "shape": spec.image_shape }),
        );
    }

    let mut payload = serde_json::Map::new();
    payload.insert(
        "schemaVersion".to_string(),
        Value::String(if spec.image_url.is_some() { "1.1" } else { "1.0" }.to_string()),
    );
    payload.insert("eventId".to_string(), Value::String(event_id.to_string()));
    payload.insert("notificationType".to_string(), Value::String(spec.notification_type.clone()));
    payload.insert("target".to_string(), serde_json::json!({ "userId": spec.user_id }));
    payload.insert("content".to_string(), Value::Object(content));

    if let (Some(label), Some(url)) = (&spec.action_label, &spec.action_url) {
        payload.insert("action".to_string(), serde_json::json!({ "label": label, "url": url }));
    }

    let aggregation_key = spec.agg_key.clone().unwrap_or_else(|| spec.notification_type.clone());
    let deduplication_key = spec.dedup_key.clone().unwrap_or_else(|| event_id.to_string());
    payload.insert(
        "classification".to_string(),
        serde_json::json!({
            "priority": spec.priority,
            "aggregationKey": aggregation_key,
            "deduplicationKey": deduplication_key,
            "replaceable": spec.replaceable,
        }),
    );

    let producer_created_at = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Millis, true);
    let server_published_at = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Millis, true);
    payload.insert(
        "timestamps".to_string(),
        serde_json::json!({
            "producerCreatedAt": producer_created_at,
            "serverPublishedAt": server_published_at,
        }),
    );

    Value::Object(payload)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn schema_version_is_10_without_an_image() {
        let mut spec = PublishSpec::defaults("u1");
        spec.image_url = None;
        let payload = build_payload(&spec, "hello", "evt-1");
        assert_eq!(payload["schemaVersion"], "1.0");
        assert!(payload["content"].get("image").is_none());
    }

    #[test]
    fn schema_version_is_11_with_an_image() {
        let mut spec = PublishSpec::defaults("u1");
        spec.image_url = Some("https://img".to_string());
        spec.image_shape = "square".to_string();
        let payload = build_payload(&spec, "hello", "evt-1");
        assert_eq!(payload["schemaVersion"], "1.1");
        assert_eq!(payload["content"]["image"]["url"], "https://img");
        assert_eq!(payload["content"]["image"]["shape"], "square");
    }

    #[test]
    fn content_omits_secondary_text_when_absent() {
        let mut spec = PublishSpec::defaults("u1");
        spec.secondary = None;
        let payload = build_payload(&spec, "hello", "evt-1");
        assert!(payload["content"].get("secondaryText").is_none());
    }

    #[test]
    fn content_includes_secondary_text_when_present() {
        let spec = PublishSpec::defaults("u1"); // secondary = Some("TestPublisher")
        let payload = build_payload(&spec, "hello", "evt-1");
        assert_eq!(payload["content"]["secondaryText"], "TestPublisher");
        assert_eq!(payload["content"]["title"], spec.title);
        assert_eq!(payload["content"]["message"], "hello");
    }

    #[test]
    fn action_included_only_when_both_label_and_url_set() {
        let mut spec = PublishSpec::defaults("u1");
        spec.action_url = None; // label still Some("View") from defaults
        let payload = build_payload(&spec, "hello", "evt-1");
        assert!(payload.get("action").is_none());

        spec.action_url = Some("https://x".to_string());
        let payload = build_payload(&spec, "hello", "evt-1");
        assert_eq!(payload["action"]["label"], "View");
        assert_eq!(payload["action"]["url"], "https://x");
    }

    #[test]
    fn classification_defaults_agg_key_to_type_and_dedup_key_to_event_id() {
        let spec = PublishSpec::defaults("u1"); // agg_key = None, dedup_key = None
        let payload = build_payload(&spec, "hello", "evt-1");
        assert_eq!(payload["classification"]["aggregationKey"], spec.notification_type);
        assert_eq!(payload["classification"]["deduplicationKey"], "evt-1");
        assert_eq!(payload["classification"]["priority"], spec.priority);
        assert_eq!(payload["classification"]["replaceable"], false);
    }

    #[test]
    fn classification_uses_explicit_agg_and_dedup_keys_when_set() {
        let mut spec = PublishSpec::defaults("u1");
        spec.agg_key = Some("custom-agg".to_string());
        spec.dedup_key = Some("custom-dedup".to_string());
        let payload = build_payload(&spec, "hello", "evt-1");
        assert_eq!(payload["classification"]["aggregationKey"], "custom-agg");
        assert_eq!(payload["classification"]["deduplicationKey"], "custom-dedup");
    }

    #[test]
    fn timestamps_are_present_as_strings() {
        let spec = PublishSpec::defaults("u1");
        let payload = build_payload(&spec, "hello", "evt-1");
        assert!(payload["timestamps"]["producerCreatedAt"].is_string());
        assert!(payload["timestamps"]["serverPublishedAt"].is_string());
    }

    #[test]
    fn target_and_event_id_and_notification_type_are_set() {
        let spec = PublishSpec::defaults("u1");
        let payload = build_payload(&spec, "hello", "evt-1");
        assert_eq!(payload["target"]["userId"], "u1");
        assert_eq!(payload["eventId"], "evt-1");
        assert_eq!(payload["notificationType"], spec.notification_type);
    }
}
