use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};

use async_trait::async_trait;
use tokio::task::JoinHandle;
use tokio::time::Duration;
use tokio_util::task::TaskTracker;

use crate::metrics::AgentMetrics;
use crate::model::{InboundNotification, Priority};
use crate::toast::{self, ToastRequest};

/// Downstream of the aggregator: renders a toast and publishes its acks.
#[async_trait]
pub trait RenderSink: Send + Sync + 'static {
    async fn render(&self, toast: ToastRequest);
}

#[derive(Clone)]
pub struct AggregatorConfig {
    pub max_buckets: usize,
    pub important_window: Duration,
    pub normal_window: Duration,
    pub drain_timeout: Duration,
}

impl Default for AggregatorConfig {
    fn default() -> Self {
        Self {
            max_buckets: 100,                         // design §9
            important_window: Duration::from_secs(2), // design §6.3
            normal_window: Duration::from_secs(10),   // design §6.3
            drain_timeout: Duration::from_secs(5),    // spec §5.2/5.3
        }
    }
}

type Key = (String, Priority);

struct Bucket {
    events: Vec<InboundNotification>,
    timer: JoinHandle<()>,
}

struct Inner {
    config: AggregatorConfig,
    buckets: Mutex<HashMap<Key, Bucket>>,
    sink: Arc<dyn RenderSink>,
    renders: TaskTracker,
    dropped_bucket_overflow: AtomicU64,
    metrics: Arc<dyn AgentMetrics>,
}

/// Owns priority handling, batching, and latest-state replacement (ADR-007).
/// Best-effort in steady state, but shutdown drains in-flight renders bounded
/// by `drain_timeout` (spec §5.2/5.3) instead of fire-and-forget.
#[derive(Clone)]
pub struct Aggregator {
    inner: Arc<Inner>,
}

impl Aggregator {
    pub fn new(
        config: AggregatorConfig,
        sink: Arc<dyn RenderSink>,
        metrics: Arc<dyn AgentMetrics>,
    ) -> Self {
        Self {
            inner: Arc::new(Inner {
                config,
                buckets: Mutex::new(HashMap::new()),
                sink,
                renders: TaskTracker::new(),
                dropped_bucket_overflow: AtomicU64::new(0),
                metrics,
            }),
        }
    }

    pub fn dropped_bucket_overflow(&self) -> u64 {
        self.inner.dropped_bucket_overflow.load(Ordering::Relaxed)
    }

    pub fn add(&self, n: InboundNotification) {
        if n.priority == Priority::Critical {
            let sink = self.inner.sink.clone();
            let toast = toast::from_single(&n);
            self.inner
                .renders
                .spawn(async move { sink.render(toast).await });
            return;
        }

        let key: Key = (n.aggregation_key.clone(), n.priority);
        let mut buckets = self.inner.buckets.lock().unwrap();

        if let Some(bucket) = buckets.get_mut(&key) {
            apply_to_bucket(&mut bucket.events, n);
            return;
        }
        if buckets.len() >= self.inner.config.max_buckets {
            let dropped = self
                .inner
                .dropped_bucket_overflow
                .fetch_add(1, Ordering::Relaxed)
                + 1;
            // Release the lock before calling into a pluggable, external
            // implementation: holding it here would poison the mutex (and
            // wedge every future add()/shutdown() call) if that
            // implementation ever panics, violating its own contract.
            drop(buckets);
            tracing::warn!(
                dropped_bucket_overflow = dropped,
                "dropping event because the aggregation bucket limit is reached"
            );
            self.inner.metrics.record_event_dropped("bucket_overflow");
            return;
        }

        let window = match n.priority {
            Priority::Important => self.inner.config.important_window,
            _ => self.inner.config.normal_window,
        };
        let inner = self.inner.clone();
        let timer_key = key.clone();
        let timer = tokio::spawn(async move {
            tokio::time::sleep(window).await;
            Inner::flush(&inner, &timer_key);
        });
        buckets.insert(
            key,
            Bucket {
                events: vec![n],
                timer,
            },
        );
    }

    /// Flush every open bucket, then wait (bounded) for all in-flight renders.
    pub async fn shutdown(&self) {
        let keys: Vec<Key> = self.inner.buckets.lock().unwrap().keys().cloned().collect();
        for key in &keys {
            Inner::flush(&self.inner, key);
        }
        self.inner.renders.close();
        if tokio::time::timeout(self.inner.config.drain_timeout, self.inner.renders.wait())
            .await
            .is_err()
        {
            tracing::warn!("shutdown drain timed out; abandoning in-flight renders");
        }
    }
}

/// Replaceable events keep only the highest-seq value (spec §5.1): a stale
/// lower-seq arrival is dropped even if it arrives later in wall time.
fn apply_to_bucket(events: &mut Vec<InboundNotification>, n: InboundNotification) {
    if n.replaceable {
        let newest = events.iter().map(|e| e.seq).max().unwrap_or(0);
        if n.seq >= newest {
            events.clear();
            events.push(n);
        }
    } else {
        events.push(n);
    }
}

impl Inner {
    fn flush(inner: &Arc<Inner>, key: &Key) {
        let bucket = inner.buckets.lock().unwrap().remove(key);
        if let Some(bucket) = bucket {
            // Called either from the timer task itself (abort is then a no-op
            // that takes effect after this synchronous flush completes) or
            // from shutdown (kills a still-pending timer).
            bucket.timer.abort();
            let mut events = bucket.events;
            if events.is_empty() {
                return;
            }
            events.sort_by_key(|e| e.seq);
            let toast = toast::from_batch(&events);
            let sink = inner.sink.clone();
            inner.renders.spawn(async move { sink.render(toast).await });
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::metrics::NullAgentMetrics;
    use crate::model::Priority;
    use crate::toast::{tests::event, ToastRequest};
    use async_trait::async_trait;
    use std::sync::{Arc, Mutex};
    use tokio::time::{advance, Duration};

    /// Records every `AgentMetrics` call, mirroring pipeline.rs's test fake of
    /// the same name — kept local to each module's test suite rather than
    /// shared, matching this file's existing preference for self-contained
    /// test fixtures (e.g. `RecordingSink`/`SlowSink` above).
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

    #[derive(Default)]
    struct RecordingSink {
        rendered: Mutex<Vec<ToastRequest>>,
    }

    #[async_trait]
    impl RenderSink for RecordingSink {
        async fn render(&self, toast: ToastRequest) {
            self.rendered.lock().unwrap().push(toast);
        }
    }

    /// Sink that takes `delay` of (paused) time before recording.
    struct SlowSink {
        delay: Duration,
        rendered: Mutex<Vec<ToastRequest>>,
    }

    #[async_trait]
    impl RenderSink for SlowSink {
        async fn render(&self, toast: ToastRequest) {
            tokio::time::sleep(self.delay).await;
            self.rendered.lock().unwrap().push(toast);
        }
    }

    fn prioritized(
        seq: u64,
        id: &str,
        priority: Priority,
        agg_key: &str,
        replaceable: bool,
        message: &str,
    ) -> crate::model::InboundNotification {
        let mut n = event(seq, id, message);
        n.priority = priority;
        n.aggregation_key = agg_key.into();
        n.replaceable = replaceable;
        n
    }

    /// Let spawned tasks (timers, renders) run to quiescence on the paused clock.
    async fn settle() {
        for _ in 0..20 {
            tokio::task::yield_now().await;
        }
    }

    #[tokio::test(start_paused = true)]
    async fn critical_renders_immediately() {
        let sink = Arc::new(RecordingSink::default());
        let agg = Aggregator::new(
            AggregatorConfig::default(),
            sink.clone(),
            Arc::new(NullAgentMetrics),
        );
        agg.add(prioritized(
            1,
            "e1",
            Priority::Critical,
            "agg.key",
            false,
            "m",
        ));
        settle().await;
        let rendered = sink.rendered.lock().unwrap();
        assert_eq!(rendered.len(), 1);
        assert_eq!(rendered[0].sources[0].event_id, "e1");
    }

    #[tokio::test(start_paused = true)]
    async fn normal_events_batch_and_flush_after_10s() {
        let sink = Arc::new(RecordingSink::default());
        let agg = Aggregator::new(
            AggregatorConfig::default(),
            sink.clone(),
            Arc::new(NullAgentMetrics),
        );
        for (seq, id) in [(1, "e1"), (2, "e2"), (3, "e3")] {
            agg.add(prioritized(
                seq,
                id,
                Priority::Normal,
                "agg.key",
                false,
                "m",
            ));
        }
        settle().await;
        assert!(
            sink.rendered.lock().unwrap().is_empty(),
            "window still open"
        );
        advance(Duration::from_secs(10)).await;
        settle().await;
        let rendered = sink.rendered.lock().unwrap();
        assert_eq!(rendered.len(), 1);
        assert_eq!(rendered[0].sources.len(), 3);
        assert!(rendered[0].title.starts_with("3 notifications"));
    }

    #[tokio::test(start_paused = true)]
    async fn important_flushes_after_2s_normal_does_not() {
        let sink = Arc::new(RecordingSink::default());
        let agg = Aggregator::new(
            AggregatorConfig::default(),
            sink.clone(),
            Arc::new(NullAgentMetrics),
        );
        agg.add(prioritized(1, "i1", Priority::Important, "imp", false, "m"));
        agg.add(prioritized(2, "n1", Priority::Normal, "norm", false, "m"));
        settle().await;
        advance(Duration::from_secs(2)).await;
        settle().await;
        {
            let rendered = sink.rendered.lock().unwrap();
            assert_eq!(rendered.len(), 1);
            assert_eq!(rendered[0].sources[0].event_id, "i1");
        }
        advance(Duration::from_secs(8)).await;
        settle().await;
        assert_eq!(sink.rendered.lock().unwrap().len(), 2);
    }

    #[tokio::test(start_paused = true)]
    async fn replaceable_keeps_highest_seq_even_when_stale_arrives_late() {
        let sink = Arc::new(RecordingSink::default());
        let agg = Aggregator::new(
            AggregatorConfig::default(),
            sink.clone(),
            Arc::new(NullAgentMetrics),
        );
        agg.add(prioritized(1, "p1", Priority::Normal, "prog", true, "10%"));
        agg.add(prioritized(3, "p3", Priority::Normal, "prog", true, "90%"));
        agg.add(prioritized(2, "p2", Priority::Normal, "prog", true, "60%")); // stale: lower seq arrives later
        settle().await;
        advance(Duration::from_secs(10)).await;
        settle().await;
        let rendered = sink.rendered.lock().unwrap();
        assert_eq!(rendered.len(), 1);
        assert_eq!(rendered[0].sources.len(), 1, "replaced, not batched");
        assert_eq!(rendered[0].sources[0].event_id, "p3");
        assert_eq!(rendered[0].message, "90%");
    }

    #[tokio::test(start_paused = true)]
    async fn separate_aggregation_keys_produce_separate_toasts() {
        let sink = Arc::new(RecordingSink::default());
        let agg = Aggregator::new(
            AggregatorConfig::default(),
            sink.clone(),
            Arc::new(NullAgentMetrics),
        );
        agg.add(prioritized(1, "a1", Priority::Normal, "a", false, "m"));
        agg.add(prioritized(2, "b1", Priority::Normal, "b", false, "m"));
        settle().await;
        advance(Duration::from_secs(10)).await;
        settle().await;
        assert_eq!(sink.rendered.lock().unwrap().len(), 2);
    }

    #[tokio::test(start_paused = true)]
    async fn drops_events_beyond_max_buckets() {
        let sink = Arc::new(RecordingSink::default());
        let config = AggregatorConfig {
            max_buckets: 2,
            ..Default::default()
        };
        let metrics = Arc::new(RecordingMetrics::default());
        let agg = Aggregator::new(config, sink.clone(), metrics.clone());
        agg.add(prioritized(1, "a1", Priority::Normal, "a", false, "m"));
        agg.add(prioritized(2, "b1", Priority::Normal, "b", false, "m"));
        agg.add(prioritized(3, "c1", Priority::Normal, "c", false, "m")); // over cap → dropped
        assert_eq!(agg.dropped_bucket_overflow(), 1);
        assert_eq!(
            metrics.calls.lock().unwrap().as_slice(),
            &["event_dropped:bucket_overflow".to_string()]
        );
        settle().await;
        advance(Duration::from_secs(10)).await;
        settle().await;
        assert_eq!(sink.rendered.lock().unwrap().len(), 2);
    }

    #[tokio::test(start_paused = true)]
    async fn shutdown_flushes_pending_buckets() {
        let sink = Arc::new(RecordingSink::default());
        let agg = Aggregator::new(
            AggregatorConfig::default(),
            sink.clone(),
            Arc::new(NullAgentMetrics),
        );
        agg.add(prioritized(
            1,
            "e1",
            Priority::Normal,
            "agg.key",
            false,
            "m",
        ));
        agg.shutdown().await;
        assert_eq!(sink.rendered.lock().unwrap().len(), 1);
    }

    #[tokio::test(start_paused = true)]
    async fn shutdown_waits_for_in_flight_renders() {
        // The C# agent loses this render (fire-and-forget); spec §5.2 fixes it.
        let sink = Arc::new(SlowSink {
            delay: Duration::from_secs(1),
            rendered: Mutex::new(Vec::new()),
        });
        let agg = Aggregator::new(
            AggregatorConfig::default(),
            sink.clone(),
            Arc::new(NullAgentMetrics),
        );
        agg.add(prioritized(
            1,
            "e1",
            Priority::Critical,
            "agg.key",
            false,
            "m",
        ));
        settle().await;
        agg.shutdown().await; // paused clock auto-advances through the 1s render
        assert_eq!(sink.rendered.lock().unwrap().len(), 1);
    }

    #[tokio::test(start_paused = true)]
    async fn hung_renderer_forfeits_after_drain_timeout() {
        struct HungSink;
        #[async_trait]
        impl RenderSink for HungSink {
            async fn render(&self, _toast: ToastRequest) {
                std::future::pending::<()>().await;
            }
        }
        let agg = Aggregator::new(
            AggregatorConfig::default(),
            Arc::new(HungSink),
            Arc::new(NullAgentMetrics),
        );
        agg.add(prioritized(
            1,
            "e1",
            Priority::Critical,
            "agg.key",
            false,
            "m",
        ));
        settle().await;
        // Must complete (auto-advance covers the 5s drain timeout) instead of hanging.
        tokio::time::timeout(Duration::from_secs(60), agg.shutdown())
            .await
            .expect("shutdown must not hang on a stuck renderer");
    }
}
