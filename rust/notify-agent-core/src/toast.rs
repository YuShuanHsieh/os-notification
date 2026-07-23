use async_trait::async_trait;
use chrono::{DateTime, Utc};

use crate::grapheme;
use crate::model::{InboundNotification, ImageRef};

pub const MAX_TITLE_GRAPHEMES: usize = 120;
pub const MAX_MESSAGE_GRAPHEMES: usize = 500;

/// Renderer-ready toast. `sources` lists every event this toast represents,
/// so the render sink can ack each of them as submitted_to_windows.
#[derive(Debug, Clone)]
pub struct ToastRequest {
    pub title: String,
    pub message: String,
    pub attribution: Option<String>,
    pub action_label: Option<String>,
    pub action_url: Option<String>,
    pub image: Option<ImageRef>,
    pub sources: Vec<InboundNotification>,
}

#[async_trait]
pub trait ToastRenderer: Send + Sync {
    /// Submit the toast; returns the submission timestamp (toastSubmittedAt).
    async fn show(&self, toast: &ToastRequest) -> anyhow::Result<DateTime<Utc>>;
}

pub fn from_single(n: &InboundNotification) -> ToastRequest {
    ToastRequest {
        title: grapheme::truncate(&n.title, MAX_TITLE_GRAPHEMES),
        message: grapheme::truncate(&n.message, MAX_MESSAGE_GRAPHEMES),
        attribution: n.secondary_text.clone(),
        action_label: n.action_label.clone(),
        action_url: n.action_url.clone(),
        image: n.image.clone(),
        sources: vec![n.clone()],
    }
}

/// Builds one summary toast from a bucket of events. "Latest" is decided by
/// `seq` (the subscribe-loop arrival stamp), never by slice position — this is
/// the spec §5.1 ordering fix over the C# implementation.
pub fn from_batch(batch: &[InboundNotification]) -> ToastRequest {
    assert!(!batch.is_empty(), "batch must not be empty");
    if batch.len() == 1 {
        return from_single(&batch[0]);
    }
    let latest = batch.iter().max_by_key(|n| n.seq).expect("non-empty");
    ToastRequest {
        title: grapheme::truncate(
            &format!("{} notifications — {}", batch.len(), latest.aggregation_key),
            MAX_TITLE_GRAPHEMES,
        ),
        message: grapheme::truncate(&format!("Latest: {}", latest.message), MAX_MESSAGE_GRAPHEMES),
        attribution: latest.secondary_text.clone(),
        action_label: latest.action_label.clone(),
        action_url: latest.action_url.clone(),
        image: latest.image.clone(),
        sources: batch.to_vec(),
    }
}

#[cfg(test)]
pub(crate) mod tests {
    use super::*;
    use crate::model::{InboundNotification, Priority};
    use unicode_segmentation::UnicodeSegmentation;

    /// Shared test-event builder (also used by later tasks' tests).
    pub(crate) fn event(seq: u64, id: &str, message: &str) -> InboundNotification {
        InboundNotification {
            seq,
            event_id: id.into(),
            user_id: "u1".into(),
            title: "Title".into(),
            message: message.into(),
            secondary_text: Some("App".into()),
            action_label: Some("Open".into()),
            action_url: Some("https://example.com/x".into()),
            image: None,
            priority: Priority::Normal,
            aggregation_key: "agg.key".into(),
            deduplication_key: id.into(),
            replaceable: false,
            producer_created_at: None,
            server_published_at: None,
            received_at: "2026-07-15T08:30:00.190Z".parse().unwrap(),
        }
    }

    #[test]
    fn single_event_maps_fields_directly() {
        let n = event(1, "e1", "Message");
        let t = from_single(&n);
        assert_eq!(t.title, "Title");
        assert_eq!(t.message, "Message");
        assert_eq!(t.attribution.as_deref(), Some("App"));
        assert_eq!(t.action_label.as_deref(), Some("Open"));
        assert_eq!(t.action_url.as_deref(), Some("https://example.com/x"));
        assert_eq!(t.sources, vec![n]);
    }

    #[test]
    fn single_event_truncates_title_120_message_500() {
        let mut n = event(1, "e1", &"M".repeat(600));
        n.title = "T".repeat(200);
        let t = from_single(&n);
        assert_eq!(t.title.graphemes(true).count(), 120);
        assert!(t.title.ends_with('…'));
        assert_eq!(t.message.graphemes(true).count(), 500);
        assert!(t.message.ends_with('…'));
    }

    #[test]
    fn batch_of_one_behaves_like_single() {
        let n = event(1, "e1", "Message");
        let t = from_batch(std::slice::from_ref(&n));
        assert_eq!(t.title, "Title");
        assert_eq!(t.message, "Message");
        assert_eq!(t.sources, vec![n]);
    }

    #[test]
    fn batch_summarizes_count_and_latest_by_seq() {
        // deliberately out of positional order: seq decides "latest", not position
        let batch = vec![event(1, "e1", "first"), event(3, "e3", "third"), event(2, "e2", "second")];
        let t = from_batch(&batch);
        assert_eq!(t.title, "3 notifications — agg.key");
        assert_eq!(t.message, "Latest: third");
        assert_eq!(t.sources.len(), 3);
        assert_eq!(t.action_label.as_deref(), Some("Open"));
    }

    #[test]
    #[should_panic(expected = "batch must not be empty")]
    fn empty_batch_panics() {
        from_batch(&[]);
    }

    #[test]
    fn single_event_threads_image() {
        let mut n = event(1, "e1", "m");
        n.image = Some(crate::model::ImageRef {
            url: "https://x.example/a.jpg".into(),
            shape: crate::model::ImageShape::Circle,
        });
        assert_eq!(from_single(&n).image, n.image);
    }

    #[test]
    fn batch_takes_latest_events_image_strictly() {
        let mut older = event(1, "e1", "first");
        older.image = Some(crate::model::ImageRef {
            url: "https://x.example/old.jpg".into(),
            shape: crate::model::ImageShape::Square,
        });
        let mut latest = event(2, "e2", "second");

        // latest has no image → toast has none (no scavenging)
        assert_eq!(from_batch(&[older.clone(), latest.clone()]).image, None);

        // latest has one → toast carries exactly it
        latest.image = Some(crate::model::ImageRef {
            url: "https://x.example/new.jpg".into(),
            shape: crate::model::ImageShape::Circle,
        });
        assert_eq!(from_batch(&[older, latest.clone()]).image, latest.image);
    }
}
