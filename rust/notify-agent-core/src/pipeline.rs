use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;

use async_trait::async_trait;
use chrono::{DateTime, Utc};
use tokio::sync::mpsc;
use tokio::task::JoinHandle;

use crate::ack::{AckPayload, TelemetryPublisher, OBSERVED_BY_AGENT, SUBMITTED_TO_WINDOWS};
use crate::aggregator::{Aggregator, AggregatorConfig, RenderSink};
use crate::dedup::DedupCache;
use crate::parser;
use crate::toast::{ToastRenderer, ToastRequest};

#[derive(Debug)]
pub struct ReceivedEvent {
    pub payload: Vec<u8>,
    pub received_at: DateTime<Utc>,
    /// Monotonic arrival stamp from the (single-threaded) subscribe loop.
    pub seq: u64,
}

#[derive(Clone)]
pub struct PipelineConfig {
    pub queue_capacity: usize, // design §9 baseline: 500
    pub workers: usize,        // design §9 baseline: 2
}

impl Default for PipelineConfig {
    fn default() -> Self {
        Self { queue_capacity: 500, workers: 2 }
    }
}

/// Bounded intake queue with a fixed worker pool (design §9). Overload drops
/// at the queue boundary via try_send — memory stays bounded, delivery stays
/// best-effort, and every drop is counted.
pub struct Pipeline {
    tx: mpsc::Sender<ReceivedEvent>,
    dropped_queue_full: Arc<AtomicU64>,
    _rx: Arc<tokio::sync::Mutex<mpsc::Receiver<ReceivedEvent>>>,
    workers: Vec<JoinHandle<()>>,
}

impl Pipeline {
    pub fn start(
        config: PipelineConfig,
        dedup: Arc<DedupCache>,
        aggregator: Aggregator,
        telemetry: Arc<dyn TelemetryPublisher>,
        device_id: String,
    ) -> Self {
        let (tx, rx) = mpsc::channel::<ReceivedEvent>(config.queue_capacity);
        let rx = Arc::new(tokio::sync::Mutex::new(rx));
        let workers = (0..config.workers)
            .map(|_| {
                let rx = rx.clone();
                let dedup = dedup.clone();
                let aggregator = aggregator.clone();
                let telemetry = telemetry.clone();
                let device_id = device_id.clone();
                tokio::spawn(async move {
                    loop {
                        // Lock only to receive, never while processing, so the
                        // two workers process concurrently.
                        let evt = { rx.lock().await.recv().await };
                        let Some(evt) = evt else { break }; // closed + drained
                        process(evt, &dedup, &aggregator, telemetry.as_ref(), &device_id).await;
                    }
                })
            })
            .collect();
        Self { tx, dropped_queue_full: Arc::new(AtomicU64::new(0)), _rx: rx, workers }
    }

    pub fn try_enqueue(&self, evt: ReceivedEvent) -> bool {
        match self.tx.try_send(evt) {
            Ok(()) => true,
            Err(_) => {
                self.dropped_queue_full.fetch_add(1, Ordering::Relaxed);
                false
            }
        }
    }

    pub fn dropped_queue_full(&self) -> u64 {
        self.dropped_queue_full.load(Ordering::Relaxed)
    }

    /// Close the intake and let workers drain every queued event (spec §5.2:
    /// bounded work — at most queue_capacity events — so no timeout needed here;
    /// the render drain timeout lives in Aggregator::shutdown).
    pub async fn shutdown(self) {
        drop(self.tx);
        for w in self.workers {
            let _ = w.await;
        }
    }
}

async fn process(
    evt: ReceivedEvent,
    dedup: &DedupCache,
    aggregator: &Aggregator,
    telemetry: &dyn TelemetryPublisher,
    device_id: &str,
) {
    let n = match parser::parse_event(&evt.payload, evt.received_at, evt.seq) {
        Ok(n) => n,
        Err(e) => {
            tracing::debug!(error = %e, "dropping invalid event");
            return;
        }
    };
    if !dedup.try_add(&n.deduplication_key) {
        return;
    }
    let ack = AckPayload {
        event_id: n.event_id.clone(),
        device_id: device_id.to_string(),
        agent_received_at: n.received_at,
        toast_submitted_at: None,
        status: OBSERVED_BY_AGENT.into(),
    };
    if let Err(e) = telemetry.publish_ack(&ack).await {
        tracing::warn!(error = %e, "observed ack publish failed");
    }
    aggregator.add(n);
}

/// Renders a toast then acks every source event as submitted_to_windows,
/// each with its own received_at and the shared submission timestamp.
pub struct AckingRenderSink {
    pub renderer: Arc<dyn ToastRenderer>,
    pub telemetry: Arc<dyn TelemetryPublisher>,
    pub device_id: String,
}

#[async_trait]
impl RenderSink for AckingRenderSink {
    async fn render(&self, toast: ToastRequest) {
        let submitted_at = match self.renderer.show(&toast).await {
            Ok(t) => t,
            Err(e) => {
                tracing::warn!(error = %e, "toast render failed");
                return;
            }
        };
        for source in &toast.sources {
            let ack = AckPayload {
                event_id: source.event_id.clone(),
                device_id: self.device_id.clone(),
                agent_received_at: source.received_at,
                toast_submitted_at: Some(submitted_at),
                status: SUBMITTED_TO_WINDOWS.into(),
            };
            if let Err(e) = self.telemetry.publish_ack(&ack).await {
                tracing::warn!(error = %e, event_id = %source.event_id, "submitted ack publish failed");
            }
        }
    }
}

/// Composition helper: wires renderer + telemetry into the aggregator's render
/// path and returns both halves. Shutdown order: pipeline first (drain intake
/// into the aggregator), then aggregator (flush + bounded render drain).
pub fn build_agent(
    pipeline_config: PipelineConfig,
    aggregator_config: AggregatorConfig,
    dedup: Arc<DedupCache>,
    renderer: Arc<dyn ToastRenderer>,
    telemetry: Arc<dyn TelemetryPublisher>,
    device_id: String,
) -> (Pipeline, Aggregator) {
    let sink = Arc::new(AckingRenderSink {
        renderer,
        telemetry: telemetry.clone(),
        device_id: device_id.clone(),
    });
    let aggregator = Aggregator::new(aggregator_config, sink);
    let pipeline = Pipeline::start(pipeline_config, dedup, aggregator.clone(), telemetry, device_id);
    (pipeline, aggregator)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::ack::{AckPayload, TelemetryPublisher, OBSERVED_BY_AGENT, SUBMITTED_TO_WINDOWS};
    use crate::aggregator::AggregatorConfig;
    use crate::dedup::DedupCache;
    use crate::toast::{ToastRenderer, ToastRequest};
    use async_trait::async_trait;
    use chrono::{DateTime, Utc};
    use std::sync::{Arc, Mutex};
    use tokio::time::Duration;

    #[derive(Default)]
    struct RecordingTelemetry {
        acks: Mutex<Vec<AckPayload>>,
    }

    #[async_trait]
    impl TelemetryPublisher for RecordingTelemetry {
        async fn publish_ack(&self, ack: &AckPayload) -> anyhow::Result<()> {
            self.acks.lock().unwrap().push(ack.clone());
            Ok(())
        }
    }

    struct RecordingRenderer {
        shown: Mutex<Vec<ToastRequest>>,
        submit_at: DateTime<Utc>,
    }

    impl Default for RecordingRenderer {
        fn default() -> Self {
            Self {
                shown: Mutex::new(Vec::new()),
                submit_at: "2026-07-15T08:30:00.205Z".parse().unwrap(),
            }
        }
    }

    #[async_trait]
    impl ToastRenderer for RecordingRenderer {
        async fn show(&self, toast: &ToastRequest) -> anyhow::Result<DateTime<Utc>> {
            self.shown.lock().unwrap().push(toast.clone());
            Ok(self.submit_at)
        }
    }

    fn received_at() -> DateTime<Utc> {
        "2026-07-15T08:30:00.190Z".parse().unwrap()
    }

    fn critical_event(seq: u64, id: &str) -> ReceivedEvent {
        let payload = format!(
            r#"{{"eventId":"{id}","target":{{"userId":"u1"}},
                 "content":{{"title":"T","message":"M"}},
                 "classification":{{"priority":"critical","deduplicationKey":"{id}"}}}}"#
        );
        ReceivedEvent { payload: payload.into_bytes(), received_at: received_at(), seq }
    }

    fn harness(queue_capacity: usize, workers: usize)
        -> (Pipeline, crate::aggregator::Aggregator, Arc<RecordingTelemetry>, Arc<RecordingRenderer>)
    {
        let telemetry = Arc::new(RecordingTelemetry::default());
        let renderer = Arc::new(RecordingRenderer::default());
        let (pipeline, aggregator) = build_agent(
            PipelineConfig { queue_capacity, workers },
            AggregatorConfig::default(),
            Arc::new(DedupCache::new(100, Duration::from_secs(600))),
            renderer.clone(),
            telemetry.clone(),
            "d-456".to_string(),
        );
        (pipeline, aggregator, telemetry, renderer)
    }

    async fn wait_until(mut cond: impl FnMut() -> bool) {
        for _ in 0..500 {
            if cond() {
                return;
            }
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
        panic!("condition not reached within 5s");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn valid_critical_event_flows_to_renderer_with_both_acks() {
        let (pipeline, aggregator, telemetry, renderer) = harness(500, 2);
        assert!(pipeline.try_enqueue(critical_event(1, "evt-1")));
        wait_until(|| telemetry.acks.lock().unwrap().len() == 2).await;

        assert_eq!(renderer.shown.lock().unwrap().len(), 1);
        let acks = telemetry.acks.lock().unwrap().clone();
        let observed = acks.iter().find(|a| a.status == OBSERVED_BY_AGENT).unwrap();
        let submitted = acks.iter().find(|a| a.status == SUBMITTED_TO_WINDOWS).unwrap();
        assert_eq!(observed.event_id, "evt-1");
        assert_eq!(observed.device_id, "d-456");
        assert_eq!(observed.agent_received_at, received_at());
        assert_eq!(observed.toast_submitted_at, None);
        assert_eq!(submitted.toast_submitted_at, Some(renderer.submit_at));
        assert_eq!(submitted.agent_received_at, received_at());

        pipeline.shutdown().await;
        aggregator.shutdown().await;
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn duplicate_events_are_processed_once() {
        let (pipeline, aggregator, telemetry, renderer) = harness(500, 2);
        for seq in 1..=3 {
            pipeline.try_enqueue(critical_event(seq, "evt-dup"));
        }
        wait_until(|| telemetry.acks.lock().unwrap().len() >= 2).await;
        tokio::time::sleep(Duration::from_millis(100)).await; // grace: no further acks

        assert_eq!(renderer.shown.lock().unwrap().len(), 1);
        assert_eq!(telemetry.acks.lock().unwrap().len(), 2); // one observed + one submitted
        pipeline.shutdown().await;
        aggregator.shutdown().await;
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn invalid_payloads_are_dropped_silently() {
        let (pipeline, aggregator, telemetry, renderer) = harness(500, 2);
        pipeline.try_enqueue(ReceivedEvent {
            payload: b"garbage".to_vec(),
            received_at: received_at(),
            seq: 1,
        });
        pipeline.try_enqueue(critical_event(2, "evt-ok")); // proves the worker survived
        wait_until(|| telemetry.acks.lock().unwrap().len() == 2).await;

        assert_eq!(renderer.shown.lock().unwrap().len(), 1);
        assert!(telemetry.acks.lock().unwrap().iter().all(|a| a.event_id == "evt-ok"));
        pipeline.shutdown().await;
        aggregator.shutdown().await;
    }

    #[tokio::test]
    async fn try_enqueue_reports_drop_when_queue_full() {
        // workers: 0 → nothing drains the channel.
        let (pipeline, _aggregator, _telemetry, _renderer) = harness(2, 0);
        assert!(pipeline.try_enqueue(critical_event(1, "e1")));
        assert!(pipeline.try_enqueue(critical_event(2, "e2")));
        assert!(!pipeline.try_enqueue(critical_event(3, "e3")));
        assert_eq!(pipeline.dropped_queue_full(), 1);
    }
}
