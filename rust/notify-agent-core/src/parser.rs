use chrono::{DateTime, Utc};
use serde::Deserialize;

use crate::model::{InboundNotification, Priority};

pub const MAX_PAYLOAD_BYTES: usize = 32 * 1024;
pub const MAX_JSON_DEPTH: usize = 16;
pub const MAX_IMAGE_URL_BYTES: usize = 2048;

#[derive(Debug, thiserror::Error)]
pub enum ParseError {
    #[error("empty payload")]
    Empty,
    #[error("payload {0} bytes exceeds {MAX_PAYLOAD_BYTES}")]
    TooLarge(usize),
    #[error("json depth exceeds {MAX_JSON_DEPTH}")]
    TooDeep,
    #[error("invalid json: {0}")]
    Json(String),
    #[error("missing {0}")]
    MissingField(&'static str),
}

pub fn parse_event(
    payload: &[u8],
    received_at: DateTime<Utc>,
    seq: u64,
) -> Result<InboundNotification, ParseError> {
    if payload.is_empty() {
        return Err(ParseError::Empty);
    }
    if payload.len() > MAX_PAYLOAD_BYTES {
        return Err(ParseError::TooLarge(payload.len()));
    }
    if depth_exceeds(payload, MAX_JSON_DEPTH) {
        return Err(ParseError::TooDeep);
    }

    let wire: WireEvent = serde_json::from_slice(payload)
        .map_err(|e| ParseError::Json(e.to_string()))?;

    let event_id = require(wire.event_id, "eventId")?;
    let user_id = require(wire.target.and_then(|t| t.user_id), "target.userId")?;
    let mut content = wire.content.unwrap_or_default();
    let image = parse_image(content.image.take());
    let title = require(content.title, "content.title")?;
    let message = require(content.message, "content.message")?;

    let notification_type = non_blank(wire.notification_type).unwrap_or_else(|| "unknown".into());
    let classification = wire.classification.unwrap_or_default();
    let priority = match classification.priority.as_deref().map(str::to_lowercase).as_deref() {
        Some("critical") => Priority::Critical,
        Some("important") => Priority::Important,
        _ => Priority::Normal,
    };
    let action = wire.action.unwrap_or_default();
    let timestamps = wire.timestamps.unwrap_or_default();

    Ok(InboundNotification {
        seq,
        aggregation_key: non_blank(classification.aggregation_key).unwrap_or(notification_type),
        deduplication_key: non_blank(classification.deduplication_key).unwrap_or_else(|| event_id.clone()),
        event_id,
        user_id,
        title,
        message,
        secondary_text: non_blank(content.secondary_text),
        action_label: non_blank(action.label),
        action_url: non_blank(action.url),
        image,
        priority,
        replaceable: classification.replaceable.unwrap_or(false),
        producer_created_at: timestamps.producer_created_at,
        server_published_at: timestamps.server_published_at,
        received_at,
    })
}

fn non_blank(v: Option<String>) -> Option<String> {
    v.filter(|s| !s.trim().is_empty())
}

fn require(v: Option<String>, field: &'static str) -> Result<String, ParseError> {
    non_blank(v).ok_or(ParseError::MissingField(field))
}

/// Best-effort: any invalid image spec yields None (the event is unaffected).
fn parse_image(wire: Option<WireImage>) -> Option<crate::model::ImageRef> {
    let wire = wire?;
    let url = wire.url.filter(|u| !u.trim().is_empty())?;
    if !url.starts_with("https://") || url.len() > MAX_IMAGE_URL_BYTES {
        tracing::debug!("dropping invalid image url");
        return None;
    }
    let shape = match wire.shape.as_deref().map(str::to_lowercase).as_deref() {
        Some("square") => crate::model::ImageShape::Square,
        _ => crate::model::ImageShape::Circle,
    };
    Some(crate::model::ImageRef { url, shape })
}

/// String-aware structural depth scan; enforces the depth limit before the
/// full parse (serde_json's own recursion limit is 128, far above ours).
fn depth_exceeds(payload: &[u8], max_depth: usize) -> bool {
    let (mut depth, mut in_string, mut escaped) = (0usize, false, false);
    for &b in payload {
        if in_string {
            if escaped {
                escaped = false;
            } else if b == b'\\' {
                escaped = true;
            } else if b == b'"' {
                in_string = false;
            }
            continue;
        }
        match b {
            b'"' => in_string = true,
            b'{' | b'[' => {
                depth += 1;
                if depth > max_depth {
                    return true;
                }
            }
            b'}' | b']' => depth = depth.saturating_sub(1),
            _ => {}
        }
    }
    false
}

#[derive(Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
struct WireEvent {
    #[allow(dead_code)]
    schema_version: Option<String>,
    event_id: Option<String>,
    notification_type: Option<String>,
    target: Option<WireTarget>,
    content: Option<WireContent>,
    action: Option<WireAction>,
    classification: Option<WireClassification>,
    timestamps: Option<WireTimestamps>,
}

#[derive(Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
struct WireTarget {
    user_id: Option<String>,
}

#[derive(Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
struct WireContent {
    title: Option<String>,
    message: Option<String>,
    secondary_text: Option<String>,
    image: Option<WireImage>,
}

#[derive(Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
struct WireImage {
    url: Option<String>,
    shape: Option<String>,
}

#[derive(Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
struct WireAction {
    label: Option<String>,
    url: Option<String>,
}

#[derive(Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
struct WireClassification {
    priority: Option<String>,
    aggregation_key: Option<String>,
    deduplication_key: Option<String>,
    replaceable: Option<bool>,
}

#[derive(Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
struct WireTimestamps {
    producer_created_at: Option<DateTime<Utc>>,
    server_published_at: Option<DateTime<Utc>>,
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::model::Priority;
    use chrono::{DateTime, Utc};

    fn received_at() -> DateTime<Utc> {
        "2026-07-15T08:30:00.190Z".parse().unwrap()
    }

    // Exact example payload from design doc §7 (same fixture as the C# test).
    const DOC_EXAMPLE: &str = r#"{
      "schemaVersion": "1.0",
      "eventId": "evt-12345",
      "notificationType": "billing.invoice.ready",
      "target": { "userId": "u_7f92a845" },
      "content": {
        "title": "Invoice ready",
        "message": "Invoice INV-8492 is ready for review.",
        "secondaryText": "Contoso Billing"
      },
      "action": { "label": "View invoice", "url": "https://app.example.com/invoices/8492" },
      "classification": {
        "priority": "normal",
        "aggregationKey": "billing.invoice.ready",
        "deduplicationKey": "invoice.ready:8492",
        "replaceable": false
      },
      "timestamps": {
        "producerCreatedAt": "2026-07-15T08:30:00.100Z",
        "serverPublishedAt": "2026-07-15T08:30:00.150Z"
      }
    }"#;

    #[test]
    fn parses_doc_example_payload() {
        let n = parse_event(DOC_EXAMPLE.as_bytes(), received_at(), 7).unwrap();
        assert_eq!(n.seq, 7);
        assert_eq!(n.event_id, "evt-12345");
        assert_eq!(n.user_id, "u_7f92a845");
        assert_eq!(n.title, "Invoice ready");
        assert_eq!(n.message, "Invoice INV-8492 is ready for review.");
        assert_eq!(n.secondary_text.as_deref(), Some("Contoso Billing"));
        assert_eq!(n.action_label.as_deref(), Some("View invoice"));
        assert_eq!(n.action_url.as_deref(), Some("https://app.example.com/invoices/8492"));
        assert_eq!(n.priority, Priority::Normal);
        assert_eq!(n.aggregation_key, "billing.invoice.ready");
        assert_eq!(n.deduplication_key, "invoice.ready:8492");
        assert!(!n.replaceable);
        assert_eq!(n.producer_created_at, Some("2026-07-15T08:30:00.100Z".parse().unwrap()));
        assert_eq!(n.server_published_at, Some("2026-07-15T08:30:00.150Z".parse().unwrap()));
        assert_eq!(n.received_at, received_at());
    }

    #[test]
    fn maps_priority_strings() {
        for (s, expected) in [
            ("critical", Priority::Critical),
            ("important", Priority::Important),
            ("normal", Priority::Normal),
            ("garbage", Priority::Normal), // unknown degrades to normal
        ] {
            let json = format!(
                r#"{{"eventId":"e1","target":{{"userId":"u1"}},
                     "content":{{"title":"t","message":"m"}},
                     "classification":{{"priority":"{s}"}}}}"#
            );
            let n = parse_event(json.as_bytes(), received_at(), 1).unwrap();
            assert_eq!(n.priority, expected, "priority string {s:?}");
        }
    }

    #[test]
    fn applies_defaults_for_missing_optional_fields() {
        let json = br#"{"eventId":"e1","notificationType":"a.b",
                        "target":{"userId":"u1"},"content":{"title":"t","message":"m"}}"#;
        let n = parse_event(json, received_at(), 1).unwrap();
        assert_eq!(n.deduplication_key, "e1"); // defaults to eventId
        assert_eq!(n.aggregation_key, "a.b");  // defaults to notificationType
        assert_eq!(n.priority, Priority::Normal);
        assert!(!n.replaceable);
        assert_eq!(n.action_label, None);
        assert_eq!(n.producer_created_at, None);
    }

    #[test]
    fn aggregation_key_falls_back_to_unknown() {
        let json = br#"{"eventId":"e1","target":{"userId":"u1"},"content":{"title":"t","message":"m"}}"#;
        let n = parse_event(json, received_at(), 1).unwrap();
        assert_eq!(n.aggregation_key, "unknown");
    }

    #[test]
    fn rejects_missing_required_fields() {
        for (json, field) in [
            (r#"{"target":{"userId":"u1"},"content":{"title":"t","message":"m"}}"#, "eventId"),
            (r#"{"eventId":"e1","content":{"title":"t","message":"m"}}"#, "target.userId"),
            (r#"{"eventId":"e1","target":{"userId":"u1"},"content":{"message":"m"}}"#, "content.title"),
            (r#"{"eventId":"e1","target":{"userId":"u1"},"content":{"title":"t"}}"#, "content.message"),
        ] {
            let err = parse_event(json.as_bytes(), received_at(), 1).unwrap_err();
            assert!(err.to_string().contains(field), "error {err} should name {field}");
        }
    }

    #[test]
    fn rejects_payload_over_32kb() {
        let big = vec![b' '; MAX_PAYLOAD_BYTES + 1];
        let err = parse_event(&big, received_at(), 1).unwrap_err();
        assert!(err.to_string().contains("exceeds"));
    }

    #[test]
    fn depth_boundary_16_allowed_17_rejected() {
        // depth 16: {"a":{"a":...1...}} with 16 opening braces. It passes the
        // depth gate, then deterministically fails the eventId requirement —
        // proving the gate did NOT fire at exactly 16.
        let d16 = format!("{}1{}", r#"{"a":"#.repeat(16), "}".repeat(16));
        let err = parse_event(d16.as_bytes(), received_at(), 1).unwrap_err();
        assert!(err.to_string().contains("missing eventId"), "depth 16 must pass the depth gate, got: {err}");
        let d17 = format!("{}1{}", r#"{"a":"#.repeat(17), "}".repeat(17));
        let err = parse_event(d17.as_bytes(), received_at(), 1).unwrap_err();
        assert!(err.to_string().to_lowercase().contains("depth"), "got: {err}");
    }

    #[test]
    fn rejects_malformed_empty_and_null_payloads() {
        assert!(parse_event(b"not json", received_at(), 1).is_err());
        assert!(parse_event(b"", received_at(), 1).is_err());
        assert!(parse_event(b"null", received_at(), 1).is_err());
    }

    #[test]
    fn parses_image_with_default_circle_shape() {
        let json = br#"{"eventId":"e1","target":{"userId":"u1"},
            "content":{"title":"t","message":"m",
                       "image":{"url":"https://cdn.example.com/a.jpg"}}}"#;
        let n = parse_event(json, received_at(), 1).unwrap();
        let img = n.image.expect("image present");
        assert_eq!(img.url, "https://cdn.example.com/a.jpg");
        assert_eq!(img.shape, crate::model::ImageShape::Circle);
    }

    #[test]
    fn parses_square_shape_and_defaults_unknown_to_circle() {
        for (shape_json, expected) in [
            ("square", crate::model::ImageShape::Square),
            ("SQUARE", crate::model::ImageShape::Square),
            ("hexagon", crate::model::ImageShape::Circle),
        ] {
            let json = format!(
                r#"{{"eventId":"e1","target":{{"userId":"u1"}},
                     "content":{{"title":"t","message":"m",
                                 "image":{{"url":"https://x.example/a.png","shape":"{shape_json}"}}}}}}"#
            );
            assert_eq!(parse_event(json.as_bytes(), received_at(), 1).unwrap().image.unwrap().shape, expected);
        }
    }

    #[test]
    fn absent_image_is_none_and_schema_10_unchanged() {
        let n = parse_event(DOC_EXAMPLE.as_bytes(), received_at(), 1).unwrap();
        assert_eq!(n.image, None);
    }

    #[test]
    fn invalid_image_drops_image_not_event() {
        for bad in [
            r#"{"url":"http://insecure.example/a.jpg"}"#,          // wrong scheme
            r#"{"url":""}"#,                                        // blank
            r#"{"shape":"circle"}"#,                                // no url
        ] {
            let json = format!(
                r#"{{"eventId":"e1","target":{{"userId":"u1"}},
                     "content":{{"title":"t","message":"m","image":{bad}}}}}"#
            );
            let n = parse_event(json.as_bytes(), received_at(), 1).unwrap(); // event OK
            assert_eq!(n.image, None, "case: {bad}");
        }
    }

    #[test]
    fn oversize_image_url_drops_image_not_event() {
        let url = format!("https://x.example/{}", "a".repeat(MAX_IMAGE_URL_BYTES));
        let json = format!(
            r#"{{"eventId":"e1","target":{{"userId":"u1"}},
                 "content":{{"title":"t","message":"m","image":{{"url":"{url}"}}}}}}"#
        );
        let n = parse_event(json.as_bytes(), received_at(), 1).unwrap();
        assert_eq!(n.image, None);
    }
}
