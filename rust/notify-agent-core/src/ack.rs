use async_trait::async_trait;
use chrono::{DateTime, Utc};
use serde::Serialize;

/// Exact status strings from design §10. The agent never emits
/// "published" or "unobserved" — those are backend-side classifications.
pub const OBSERVED_BY_AGENT: &str = "observed_by_agent";
pub const SUBMITTED_TO_WINDOWS: &str = "submitted_to_windows";

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct AckPayload {
    pub event_id: String,
    pub device_id: String,
    pub agent_received_at: DateTime<Utc>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub toast_submitted_at: Option<DateTime<Utc>>,
    pub status: String,
}

#[async_trait]
pub trait TelemetryPublisher: Send + Sync {
    async fn publish_ack(&self, ack: &AckPayload) -> anyhow::Result<()>;
}

pub fn serialize(ack: &AckPayload) -> Vec<u8> {
    serde_json::to_vec(ack).expect("ack serialization is infallible")
}

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::{DateTime, Utc};

    fn ts(s: &str) -> DateTime<Utc> {
        s.parse().unwrap()
    }

    #[test]
    fn serializes_submitted_ack_in_design_doc_shape() {
        let ack = AckPayload {
            event_id: "evt-12345".into(),
            device_id: "d-456".into(),
            agent_received_at: ts("2026-07-15T08:30:00.190Z"),
            toast_submitted_at: Some(ts("2026-07-15T08:30:00.205Z")),
            status: SUBMITTED_TO_WINDOWS.into(),
        };
        let v: serde_json::Value = serde_json::from_slice(&serialize(&ack)).unwrap();
        assert_eq!(v["eventId"], "evt-12345");
        assert_eq!(v["deviceId"], "d-456");
        assert_eq!(v["agentReceivedAt"].as_str().unwrap().parse::<DateTime<Utc>>().unwrap(),
                   ts("2026-07-15T08:30:00.190Z"));
        assert_eq!(v["toastSubmittedAt"].as_str().unwrap().parse::<DateTime<Utc>>().unwrap(),
                   ts("2026-07-15T08:30:00.205Z"));
        assert_eq!(v["status"], "submitted_to_windows");
        assert_eq!(v.as_object().unwrap().len(), 5, "exactly the five contract fields");
    }

    #[test]
    fn observed_ack_omits_null_toast_submitted_at() {
        let ack = AckPayload {
            event_id: "evt-1".into(),
            device_id: "d-1".into(),
            agent_received_at: ts("2026-07-15T08:30:00.190Z"),
            toast_submitted_at: None,
            status: OBSERVED_BY_AGENT.into(),
        };
        let v: serde_json::Value = serde_json::from_slice(&serialize(&ack)).unwrap();
        assert_eq!(v["status"], "observed_by_agent");
        assert!(v.as_object().unwrap().get("toastSubmittedAt").is_none());
        assert_eq!(v.as_object().unwrap().len(), 4);
    }
}
