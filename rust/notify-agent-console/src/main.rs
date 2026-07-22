use std::sync::Arc;

use async_trait::async_trait;
use chrono::{DateTime, Utc};
use notify_agent_core::host::{AgentConfig, AgentHost};
use notify_agent_core::identity::{default_device_id, DeviceCodeIdentity, EnvIdentity, IdentityProvider};
use notify_agent_core::toast::{ToastRenderer, ToastRequest};

/// Dev stand-in for the Windows renderer: prints "toasts" to stdout
/// (format mirrors the C# ConsoleToastRenderer).
struct ConsoleToastRenderer;

#[async_trait]
impl ToastRenderer for ConsoleToastRenderer {
    async fn show(&self, toast: &ToastRequest) -> anyhow::Result<DateTime<Utc>> {
        println!("[TOAST] {}", toast.title);
        println!("        {}", toast.message);
        if let Some(attribution) = &toast.attribution {
            println!("        — {attribution}");
        }
        if let Some(image) = &toast.image {
            let shape = match image.shape {
                notify_agent_core::model::ImageShape::Circle => "circle",
                notify_agent_core::model::ImageShape::Square => "square",
            };
            println!("        [image] {} ({shape})", image.url);
        }
        if let (Some(label), Some(url)) = (&toast.action_label, &toast.action_url) {
            println!("        [{label}] -> {url}");
        }
        Ok(Utc::now())
    }
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .init();

    let config = AgentConfig::from_env();
    let nats_url = config.nats_url.clone();
    let renderer: Arc<dyn ToastRenderer> = Arc::new(ConsoleToastRenderer);

    let identity: Arc<dyn IdentityProvider> = match std::env::var("NOTIFY_AAD_CLIENT_ID") {
        Ok(client_id) if !client_id.trim().is_empty() => Arc::new(DeviceCodeIdentity {
            client_id,
            tenant: std::env::var("NOTIFY_AAD_TENANT_ID").unwrap_or_else(|_| "organizations".into()),
            device_id: std::env::var("NOTIFY_DEVICE_ID").ok().filter(|s| !s.is_empty()).unwrap_or_else(default_device_id),
            renderer: renderer.clone(),
        }),
        _ => Arc::new(EnvIdentity),
    };

    let host = AgentHost::start(config, identity, renderer).await?;
    println!("Agent subscribed to {} on {}. Ctrl+C to exit.", host.subject(), nats_url);

    tokio::signal::ctrl_c().await?;
    println!("Shutting down.");
    host.shutdown().await?;
    Ok(())
}
