use std::sync::Arc;

use anyhow::{anyhow, Context};
use async_trait::async_trait;
use base64::Engine;

use crate::toast::{ToastRenderer, ToastRequest};

#[derive(Debug, Clone)]
pub struct AgentIdentity {
    pub user_id: String,
    pub device_id: String,
}

/// Resolves the immutable application user ID and device ID (design §8).
/// The OS account name is never used as identity.
#[async_trait]
pub trait IdentityProvider: Send + Sync {
    async fn identity(&self) -> anyhow::Result<AgentIdentity>;
}

/// Development identity from environment variables (parity with C#).
pub struct EnvIdentity;

pub fn default_device_id() -> String {
    format!("d-{}", gethostname::gethostname().to_string_lossy().to_lowercase())
}

#[async_trait]
impl IdentityProvider for EnvIdentity {
    async fn identity(&self) -> anyhow::Result<AgentIdentity> {
        let user_id = std::env::var("NOTIFY_USER_ID")
            .ok()
            .filter(|s| !s.trim().is_empty())
            .context("NOTIFY_USER_ID is not set")?;
        let device_id = std::env::var("NOTIFY_DEVICE_ID")
            .ok()
            .filter(|s| !s.trim().is_empty())
            .unwrap_or_else(default_device_id);
        Ok(AgentIdentity { user_id, device_id })
    }
}

#[derive(Debug, PartialEq)]
pub enum TokenPoll {
    Pending,
    Success { id_token: String },
    Failed(String),
}

pub fn parse_token_poll(body: &serde_json::Value) -> TokenPoll {
    if let Some(id_token) = body.get("id_token").and_then(|v| v.as_str()) {
        return TokenPoll::Success { id_token: id_token.to_string() };
    }
    match body.get("error").and_then(|v| v.as_str()) {
        Some("authorization_pending") => TokenPoll::Pending,
        Some(err) => {
            let desc = body.get("error_description").and_then(|v| v.as_str()).unwrap_or("");
            TokenPoll::Failed(format!("{err}: {desc}"))
        }
        None => TokenPoll::Failed("malformed token response".to_string()),
    }
}

/// Extract the Entra object id from an id_token. POC trade-off (spec §7):
/// no signature validation — the token arrives directly from Entra over TLS
/// and is used only to derive the local user id.
pub fn oid_from_id_token(id_token: &str) -> anyhow::Result<String> {
    let payload_b64 = id_token.split('.').nth(1).ok_or_else(|| anyhow!("not a JWT"))?;
    let bytes = base64::engine::general_purpose::URL_SAFE_NO_PAD
        .decode(payload_b64)
        .context("id_token payload is not base64url")?;
    let claims: serde_json::Value = serde_json::from_slice(&bytes).context("id_token payload is not JSON")?;
    claims
        .get("oid")
        .and_then(|v| v.as_str())
        .map(str::to_string)
        .ok_or_else(|| anyhow!("id_token has no oid claim"))
}

/// OIDC device-code sign-in (spec §7): the WAM broker replacement.
pub struct DeviceCodeIdentity {
    pub client_id: String,
    pub tenant: String,
    pub device_id: String,
    pub renderer: Arc<dyn ToastRenderer>,
}

#[derive(serde::Deserialize)]
struct DeviceCodeResponse {
    device_code: String,
    user_code: String,
    verification_uri: String,
    interval: u64,
    expires_in: u64,
}

#[async_trait]
impl IdentityProvider for DeviceCodeIdentity {
    async fn identity(&self) -> anyhow::Result<AgentIdentity> {
        let http = reqwest::Client::new();
        let base = format!("https://login.microsoftonline.com/{}/oauth2/v2.0", self.tenant);

        let dc: DeviceCodeResponse = http
            .post(format!("{base}/devicecode"))
            .form(&[("client_id", self.client_id.as_str()), ("scope", "openid profile")])
            .send()
            .await?
            .error_for_status()?
            .json()
            .await?;

        let prompt = format!("Go to {} and enter code {}", dc.verification_uri, dc.user_code);
        println!("[SIGN-IN] {prompt}");
        // Best-effort toast with the code, so the Windows head surfaces it too.
        let _ = self
            .renderer
            .show(&ToastRequest {
                title: "Sign in required".into(),
                message: prompt,
                attribution: Some("Desktop Notification Agent".into()),
                action_label: Some("Open sign-in page".into()),
                action_url: Some(dc.verification_uri.clone()),
                image: None,
                sources: Vec::new(),
            })
            .await;

        let deadline = tokio::time::Instant::now() + std::time::Duration::from_secs(dc.expires_in);
        loop {
            if tokio::time::Instant::now() >= deadline {
                anyhow::bail!("device-code sign-in timed out");
            }
            tokio::time::sleep(std::time::Duration::from_secs(dc.interval.max(1))).await;
            let body: serde_json::Value = http
                .post(format!("{base}/token"))
                .form(&[
                    ("grant_type", "urn:ietf:params:oauth:grant-type:device_code"),
                    ("client_id", self.client_id.as_str()),
                    ("device_code", dc.device_code.as_str()),
                ])
                .send()
                .await?
                .json()
                .await?;
            match parse_token_poll(&body) {
                TokenPoll::Pending => continue,
                TokenPoll::Success { id_token } => {
                    let oid = oid_from_id_token(&id_token)?;
                    return Ok(AgentIdentity {
                        user_id: format!("u_{oid}"),
                        device_id: self.device_id.clone(),
                    });
                }
                TokenPoll::Failed(reason) => anyhow::bail!("device-code sign-in failed: {reason}"),
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use base64::Engine;

    fn fake_jwt(claims: &serde_json::Value) -> String {
        let b64 = |v: &serde_json::Value| {
            base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(v.to_string())
        };
        format!("{}.{}.sig", b64(&serde_json::json!({"alg":"none"})), b64(claims))
    }

    #[test]
    fn extracts_oid_from_id_token() {
        let token = fake_jwt(&serde_json::json!({"oid": "7f92a845-0000-0000-0000-000000000001", "name": "x"}));
        assert_eq!(oid_from_id_token(&token).unwrap(), "7f92a845-0000-0000-0000-000000000001");
    }

    #[test]
    fn rejects_token_without_oid() {
        let token = fake_jwt(&serde_json::json!({"name": "x"}));
        assert!(oid_from_id_token(&token).is_err());
        assert!(oid_from_id_token("not-a-jwt").is_err());
    }

    #[test]
    fn classifies_token_poll_responses() {
        assert_eq!(
            parse_token_poll(&serde_json::json!({"error": "authorization_pending"})),
            TokenPoll::Pending
        );
        assert_eq!(
            parse_token_poll(&serde_json::json!({"id_token": "abc", "access_token": "def"})),
            TokenPoll::Success { id_token: "abc".into() }
        );
        assert_eq!(
            parse_token_poll(&serde_json::json!({"error": "expired_token", "error_description": "gone"})),
            TokenPoll::Failed("expired_token: gone".into())
        );
        assert_eq!(
            parse_token_poll(&serde_json::json!({})),
            TokenPoll::Failed("malformed token response".into())
        );
    }

    #[tokio::test]
    async fn env_identity_requires_user_id() {
        // Serialized via env-var uniqueness: this test owns these two vars.
        std::env::remove_var("NOTIFY_USER_ID");
        std::env::set_var("NOTIFY_DEVICE_ID", "d-test");
        assert!(EnvIdentity.identity().await.is_err());
        std::env::set_var("NOTIFY_USER_ID", "u_demo");
        let id = EnvIdentity.identity().await.unwrap();
        assert_eq!(id.user_id, "u_demo");
        assert_eq!(id.device_id, "d-test");
        std::env::remove_var("NOTIFY_USER_ID");
        std::env::remove_var("NOTIFY_DEVICE_ID");
    }
}
