use chrono::{DateTime, Utc};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum Priority {
    Normal,
    Important,
    Critical,
}

/// Normalized, validated notification event as consumed by the pipeline.
/// `seq` is a monotonic arrival stamp assigned by the (single-threaded)
/// subscribe loop; all ordering decisions use it (spec §5.1).
#[derive(Debug, Clone, PartialEq)]
pub struct InboundNotification {
    pub seq: u64,
    pub event_id: String,
    pub user_id: String,
    pub title: String,
    pub message: String,
    pub secondary_text: Option<String>,
    pub action_label: Option<String>,
    pub action_url: Option<String>,
    pub priority: Priority,
    pub aggregation_key: String,
    pub deduplication_key: String,
    pub replaceable: bool,
    pub producer_created_at: Option<DateTime<Utc>>,
    pub server_published_at: Option<DateTime<Utc>>,
    pub received_at: DateTime<Utc>,
}
