//! End-to-end against a real NATS server on localhost:4222 (provided by a
//! pre-existing container on this machine — do NOT manage that container).
//! The test no-ops politely when the port is closed, same as the C# suite.

use std::sync::{Arc, Mutex};
use std::time::Duration;

use async_trait::async_trait;
use chrono::{DateTime, Utc};
use futures::StreamExt;
use notify_agent_core::host::{AgentConfig, AgentHost};
use notify_agent_core::identity::{AgentIdentity, IdentityProvider};
use notify_agent_core::metrics::AgentMetrics;
use notify_agent_core::toast::{ToastRenderer, ToastRequest};

fn nats_available() -> bool {
    std::net::TcpStream::connect_timeout(&"127.0.0.1:4222".parse().unwrap(), Duration::from_secs(1))
        .is_ok()
}

struct StubIdentity;

#[async_trait]
impl IdentityProvider for StubIdentity {
    async fn identity(&self) -> anyhow::Result<AgentIdentity> {
        Ok(AgentIdentity {
            user_id: "itest-rust".into(),
            device_id: "d-itest-rust".into(),
        })
    }
}

#[derive(Default)]
struct RecordingRenderer {
    shown: Mutex<Vec<ToastRequest>>,
}

#[async_trait]
impl ToastRenderer for RecordingRenderer {
    async fn show(&self, toast: &ToastRequest) -> anyhow::Result<DateTime<Utc>> {
        self.shown.lock().unwrap().push(toast.clone());
        Ok(Utc::now())
    }
}

/// Records every `AgentMetrics` call, proving the end-to-end host wiring
/// (`AgentHost::start`'s optional metrics param -> `build_agent` ->
/// pipeline/aggregator) actually delivers calls, not just the in-process
/// unit tests in pipeline.rs/aggregator.rs.
#[derive(Default)]
struct RecordingMetrics {
    calls: Mutex<Vec<String>>,
}

impl AgentMetrics for RecordingMetrics {
    fn record_event_received(&self) {
        self.calls
            .lock()
            .unwrap()
            .push("event_received".to_string());
    }
    fn record_event_dropped(&self, reason: &str) {
        self.calls
            .lock()
            .unwrap()
            .push(format!("event_dropped:{reason}"));
    }
    fn record_render_duration(&self, seconds: f64) {
        self.calls
            .lock()
            .unwrap()
            .push(format!("render_duration:{seconds}"));
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn published_event_is_rendered_and_acked_end_to_end() {
    if !nats_available() {
        eprintln!("SKIPPED: no NATS server on localhost:4222");
        return;
    }

    let renderer = Arc::new(RecordingRenderer::default());
    let metrics = Arc::new(RecordingMetrics::default());
    let host = AgentHost::start(
        AgentConfig {
            nats_url: "nats://127.0.0.1:4222".into(),
            subject_template: "notify.user.{0}.desktop".into(),
            ack_subject: "notify.ack.desktop".into(),
        },
        Arc::new(StubIdentity),
        renderer.clone(),
        None,
        Some(metrics.clone() as Arc<dyn AgentMetrics>),
    )
    .await
    .expect("host start");
    assert_eq!(host.subject(), "notify.user.itest-rust.desktop");

    let probe = async_nats::connect("nats://127.0.0.1:4222").await.unwrap();
    let mut acks = probe.subscribe("notify.ack.desktop").await.unwrap();
    tokio::time::sleep(Duration::from_millis(500)).await; // let subscriptions settle (no replay in Core NATS)

    let event_id = format!("evt-{}", uuid_like());
    let payload = format!(
        r#"{{"eventId":"{event_id}","target":{{"userId":"itest-rust"}},
             "content":{{"title":"Integration","message":"Hello"}},
             "classification":{{"priority":"critical"}}}}"#
    );
    probe
        .publish(
            "notify.user.itest-rust.desktop",
            payload.into_bytes().into(),
        )
        .await
        .unwrap();
    probe.flush().await.unwrap();

    let mut received: Vec<String> = Vec::new();
    let deadline = tokio::time::Instant::now() + Duration::from_secs(10);
    while received.len() < 2 && tokio::time::Instant::now() < deadline {
        match tokio::time::timeout_at(deadline, acks.next()).await {
            Ok(Some(msg)) => received.push(String::from_utf8_lossy(&msg.payload).to_string()),
            _ => break,
        }
    }

    assert_eq!(
        received.len(),
        2,
        "expected 2 acks within 10s, got: {received:?}"
    );
    assert!(received
        .iter()
        .any(|a| a.contains("observed_by_agent") && a.contains(&event_id)));
    assert!(received
        .iter()
        .any(|a| a.contains("submitted_to_windows") && a.contains(&event_id)));
    assert!(received.iter().all(|a| a.contains("d-itest-rust")));
    assert_eq!(renderer.shown.lock().unwrap().len(), 1);

    // Metrics wiring: one event_received (matching the one observed_by_agent
    // ack above) and one render_duration sample (matching the one render).
    let calls = metrics.calls.lock().unwrap().clone();
    assert_eq!(calls.iter().filter(|c| *c == "event_received").count(), 1);
    assert_eq!(
        calls
            .iter()
            .filter(|c| c.starts_with("render_duration:"))
            .count(),
        1
    );
    assert_eq!(host.dropped_queue_full(), 0);
    assert_eq!(host.dropped_bucket_overflow(), 0);

    host.shutdown().await.expect("clean shutdown");
}

/// Unique-enough id without a uuid dependency.
fn uuid_like() -> String {
    format!(
        "{:x}",
        std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap()
            .as_nanos()
    )
}
