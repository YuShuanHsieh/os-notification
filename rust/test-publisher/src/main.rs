mod args;
mod payload;
mod spec;

use std::time::Duration;

use anyhow::Result;
use futures::StreamExt;
use rand::RngCore;

fn generate_event_id() -> String {
    let mut bytes = [0u8; 16];
    rand::thread_rng().fill_bytes(&mut bytes);
    let hex: String = bytes.iter().map(|b| format!("{b:02x}")).collect();
    format!("evt-{hex}")
}

fn print_usage(error: &str) {
    eprintln!("error: {error}");
    eprintln!("usage: test-publisher <userId> [title] [message] [priority] [count] [imageUrl]");
    eprintln!("       test-publisher <userId> --scenario presence|invoice|progress|batch|dedup [--flags]");
    eprintln!("       test-publisher <userId> [--flags]");
    eprintln!("flags: --title --message --secondary --type --priority --count --image-url");
    eprintln!("       --image-shape circle|square --action-label --action-url --agg-key");
    eprintln!("       --dedup-key --replaceable --delay-ms");
}

#[tokio::main]
async fn main() -> Result<()> {
    let cli_args: Vec<String> = std::env::args().skip(1).collect();
    let spec = match args::parse(&cli_args) {
        Ok(spec) => spec,
        Err(msg) => {
            print_usage(&msg);
            std::process::exit(2);
        }
    };

    let nats_url = std::env::var("NOTIFY_NATS_URL").unwrap_or_else(|_| "nats://127.0.0.1:4222".to_string());
    let ack_subject = std::env::var("NOTIFY_ACK_SUBJECT").unwrap_or_else(|_| "notify.ack.desktop".to_string());
    let subject = format!("notify.user.{}.desktop", spec.user_id);

    if let Some(expect) = &spec.expect {
        println!("EXPECT: {expect}");
    }

    let client = async_nats::ConnectOptions::new().connect(&nats_url).await?;

    let mut ack_sub = client.subscribe(ack_subject).await?;
    let ack_watcher = tokio::spawn(async move {
        while let Some(msg) = ack_sub.next().await {
            println!("[ACK] {}", String::from_utf8_lossy(&msg.payload));
        }
    });
    tokio::time::sleep(Duration::from_millis(300)).await;

    let messages: Vec<String> = spec
        .messages
        .clone()
        .unwrap_or_else(|| std::iter::repeat(spec.message.clone()).take(spec.count as usize).collect());

    for (i, message) in messages.iter().enumerate() {
        let event_id = generate_event_id();
        let payload = payload::build_payload(&spec, message, &event_id);
        let bytes = serde_json::to_vec(&payload)?;
        client.publish(subject.clone(), bytes.into()).await?;
        println!("[PUB] {event_id} -> {subject} (priority={})", spec.priority);
        if spec.delay_ms > 0 && i + 1 < messages.len() {
            tokio::time::sleep(Duration::from_millis(spec.delay_ms)).await;
        }
    }

    tokio::time::sleep(Duration::from_secs(12)).await;
    ack_watcher.abort();
    let _ = ack_watcher.await;

    Ok(())
}
