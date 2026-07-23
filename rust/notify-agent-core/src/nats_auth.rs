use async_trait::async_trait;

/// Resolves NATS authentication for the agent's connection (design: NATS WebSocket +
/// pluggable auth §2). Mirrors the IdentityProvider pattern: a simple default lives here in
/// Core; richer composition (e.g. reusing an AAD identity) happens in a host's main.rs.
#[async_trait]
pub trait NatsAuthProvider: Send + Sync {
    async fn connect_options(&self) -> anyhow::Result<async_nats::ConnectOptions>;
}

/// Authenticates using a standard NATS .creds file (user JWT + NKey seed) (design §3).
pub struct CredsFileAuth {
    pub path: String,
}

#[async_trait]
impl NatsAuthProvider for CredsFileAuth {
    async fn connect_options(&self) -> anyhow::Result<async_nats::ConnectOptions> {
        tracing::debug!(path = %self.path, "nats auth [creds-file]: loading");
        let opts = async_nats::ConnectOptions::new().credentials_file(&self.path).await?;
        tracing::debug!("nats auth [creds-file]: loaded ok");
        Ok(opts)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn write_temp_creds(seed: &str) -> std::path::PathBuf {
        let path = std::env::temp_dir().join(format!(
            "notify-agent-test-{}.creds",
            std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap().as_nanos()
        ));
        let contents = format!(
            "-----BEGIN NATS USER JWT-----\nfake.jwt.value\n------END NATS USER JWT------\n\n\
             -----BEGIN USER NKEY SEED-----\n{seed}\n------END USER NKEY SEED------\n"
        );
        std::fs::write(&path, contents).unwrap();
        path
    }

    #[tokio::test]
    async fn creds_file_auth_builds_connect_options_from_a_valid_file() {
        let seed = nkeys::KeyPair::new(nkeys::KeyPairType::User).seed().unwrap();
        let path = write_temp_creds(&seed);

        let result = CredsFileAuth { path: path.to_string_lossy().into_owned() }.connect_options().await;

        std::fs::remove_file(&path).ok();
        assert!(result.is_ok(), "expected a valid creds file to parse: {result:?}");
    }

    #[tokio::test]
    async fn creds_file_auth_fails_on_missing_file() {
        let provider = CredsFileAuth { path: "/nonexistent/path.creds".into() };
        assert!(provider.connect_options().await.is_err());
    }
}
