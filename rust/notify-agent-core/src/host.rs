use std::sync::Arc;

use async_trait::async_trait;
use chrono::Utc;
use futures::StreamExt;
use tokio::task::JoinHandle;
use tokio_util::sync::CancellationToken;

use crate::ack::{self, AckPayload, TelemetryPublisher};
use crate::aggregator::{Aggregator, AggregatorConfig};
use crate::dedup::DedupCache;
use crate::identity::IdentityProvider;
use crate::metrics::{AgentMetrics, NullAgentMetrics};
use crate::nats_auth::NatsAuthProvider;
use crate::pipeline::{build_agent, Pipeline, PipelineConfig, ReceivedEvent};
use crate::toast::ToastRenderer;

pub struct AgentConfig {
    pub nats_url: String,
    pub subject_template: String, // literal "{0}" placeholder — env parity with C#
    pub ack_subject: String,
}

impl AgentConfig {
    pub fn from_env() -> Self {
        let var = |k: &str, d: &str| {
            std::env::var(k)
                .ok()
                .filter(|s| !s.is_empty())
                .unwrap_or_else(|| d.into())
        };
        Self {
            nats_url: var("NOTIFY_NATS_URL", "nats://127.0.0.1:4222"),
            subject_template: var("NOTIFY_SUBJECT_TEMPLATE", "notify.user.{0}.desktop"), // design §4
            ack_subject: var("NOTIFY_ACK_SUBJECT", "notify.ack.desktop"),
        }
    }
}

pub struct NatsTelemetry {
    pub client: async_nats::Client,
    pub subject: String,
}

#[async_trait]
impl TelemetryPublisher for NatsTelemetry {
    async fn publish_ack(&self, ack_payload: &AckPayload) -> anyhow::Result<()> {
        self.client
            .publish(self.subject.clone(), ack::serialize(ack_payload).into())
            .await?;
        Ok(())
    }
}

/// Composition root: identity → NATS connection → pipeline → live subscription.
/// Plain Core NATS subscription: reconnects resume with future events only
/// (design §6.2). Shutdown order (spec §5.2): cancel subscribe loop → drain
/// pipeline → drain aggregator (bounded) → flush NATS last.
pub struct AgentHost {
    subject: String,
    client: async_nats::Client,
    pipeline: Pipeline,
    aggregator: Aggregator,
    cancel: CancellationToken,
    subscriber: JoinHandle<()>,
}

impl AgentHost {
    pub async fn start(
        config: AgentConfig,
        identity: Arc<dyn IdentityProvider>,
        renderer: Arc<dyn ToastRenderer>,
        auth_provider: Option<Arc<dyn NatsAuthProvider>>,
        metrics: Option<Arc<dyn AgentMetrics>>,
    ) -> anyhow::Result<AgentHost> {
        let metrics = metrics.unwrap_or_else(|| Arc::new(NullAgentMetrics));
        let id = identity.identity().await?;
        tracing::debug!(user_id = %id.user_id, device_id = %id.device_id, "agent: identity resolved");
        tracing::debug!(url = %config.nats_url, authenticated = auth_provider.is_some(), "nats: connecting");
        let opts = match &auth_provider {
            Some(provider) => provider.connect_options().await?,
            None => async_nats::ConnectOptions::new(),
        };
        let client = opts.connect(&config.nats_url).await?;
        tracing::debug!("nats: connected");
        let telemetry: Arc<dyn TelemetryPublisher> = Arc::new(NatsTelemetry {
            client: client.clone(),
            subject: config.ack_subject.clone(),
        });
        let dedup = Arc::new(DedupCache::new(10_000, std::time::Duration::from_secs(600)));
        let (pipeline_half, aggregator) = build_agent(
            PipelineConfig::default(),
            AggregatorConfig::default(),
            dedup,
            renderer,
            telemetry,
            id.device_id.clone(),
            metrics,
        );

        let subject = config.subject_template.replace("{0}", &id.user_id);
        let mut subscription = client.subscribe(subject.clone()).await?;
        let cancel = CancellationToken::new();

        let loop_cancel = cancel.clone();
        let intake = pipeline_half.intake_handle();
        let subscriber = tokio::spawn(async move {
            let mut seq: u64 = 0;
            loop {
                tokio::select! {
                    _ = loop_cancel.cancelled() => break,
                    msg = subscription.next() => {
                        match msg {
                            Some(msg) if !msg.payload.is_empty() => {
                                seq += 1;
                                intake.try_enqueue(ReceivedEvent {
                                    payload: msg.payload.to_vec(),
                                    received_at: Utc::now(),
                                    seq,
                                });
                            }
                            Some(_) => {} // empty payload: ignore
                            None => {
                                // Spec §5.4: a dead subscription must not leave
                                // a silent zombie agent.
                                tracing::error!("NATS subscription ended unexpectedly; exiting");
                                std::process::exit(1);
                            }
                        }
                    }
                }
            }
        });

        Ok(AgentHost {
            subject,
            client,
            pipeline: pipeline_half,
            aggregator,
            cancel,
            subscriber,
        })
    }

    pub fn subject(&self) -> &str {
        &self.subject
    }

    /// Delegates to the pipeline's own counter. Parity with the Go
    /// implementation's `Host.DroppedQueueFull()`.
    pub fn dropped_queue_full(&self) -> u64 {
        self.pipeline.dropped_queue_full()
    }

    /// Delegates to the aggregator's own counter. Parity with the Go
    /// implementation's `Host.DroppedBucketOverflow()`.
    pub fn dropped_bucket_overflow(&self) -> u64 {
        self.aggregator.dropped_bucket_overflow()
    }

    pub async fn shutdown(self) -> anyhow::Result<()> {
        self.cancel.cancel();
        let _ = self.subscriber.await;
        self.pipeline.shutdown().await;
        self.aggregator.shutdown().await;
        self.client.flush().await?; // push any buffered acks before drop
        Ok(())
    }
}
