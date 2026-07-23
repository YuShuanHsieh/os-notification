use std::sync::Arc;

use anyhow::{anyhow, Context};
use async_trait::async_trait;
use base64::Engine;

use crate::nats_auth::AccessTokenProvider;
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
    Success { id_token: String, refresh_token: Option<String> },
    Failed(String),
}

pub fn parse_token_poll(body: &serde_json::Value) -> TokenPoll {
    if let Some(id_token) = body.get("id_token").and_then(|v| v.as_str()) {
        let refresh_token = body.get("refresh_token").and_then(|v| v.as_str()).map(str::to_string);
        return TokenPoll::Success { id_token: id_token.to_string(), refresh_token };
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

/// Scope string for the device-code sign-in request: always includes offline_access (so a
/// refresh token comes back, letting AadTokenProvider mint later scoped tokens silently),
/// plus any caller-supplied extra scopes such as the NATS external-auth-service scope
/// (design: NATS WebSocket + pluggable auth §4).
fn device_code_scope(extra_scopes: &[String]) -> String {
    let mut scopes = vec!["openid".to_string(), "profile".to_string(), "offline_access".to_string()];
    scopes.extend(extra_scopes.iter().cloned());
    scopes.join(" ")
}

/// OIDC device-code sign-in (spec §7): the WAM broker replacement.
pub struct DeviceCodeIdentity {
    pub client_id: String,
    pub tenant: String,
    pub device_id: String,
    pub renderer: Arc<dyn ToastRenderer>,
    /// Additional OAuth scopes to request during sign-in, beyond `openid profile
    /// offline_access` — e.g. the NATS external-auth-service scope, so a single sign-in
    /// covers both identity and later silent NATS-auth token acquisition.
    pub extra_scopes: Vec<String>,
    /// Populated with the refresh token after a successful sign-in, so AadTokenProvider can
    /// mint additional-scope access tokens later without a second interactive sign-in.
    pub refresh_token_sink: Option<Arc<tokio::sync::Mutex<Option<String>>>>,
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

        let scope = device_code_scope(&self.extra_scopes);
        let dc: DeviceCodeResponse = http
            .post(format!("{base}/devicecode"))
            .form(&[("client_id", self.client_id.as_str()), ("scope", scope.as_str())])
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
                TokenPoll::Success { id_token, refresh_token } => {
                    let oid = oid_from_id_token(&id_token)?;
                    if let (Some(sink), Some(rt)) = (&self.refresh_token_sink, refresh_token) {
                        *sink.lock().await = Some(rt);
                    }
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

/// Silently mints AAD access tokens for an additional scope by exchanging the refresh token
/// DeviceCodeIdentity captured during sign-in — this crate's stand-in for MSAL's
/// AcquireTokenSilent cache, which C#'s MsalIdentityProvider.GetAccessTokenAsync relies on
/// (design: NATS WebSocket + pluggable auth §4).
pub struct AadTokenProvider {
    pub client_id: String,
    pub tenant: String,
    pub scope: String,
    pub refresh_token: Arc<tokio::sync::Mutex<Option<String>>>,
}

#[derive(serde::Deserialize)]
struct RefreshTokenResponse {
    access_token: String,
    refresh_token: Option<String>,
}

#[async_trait]
impl AccessTokenProvider for AadTokenProvider {
    async fn access_token(&self) -> anyhow::Result<String> {
        tracing::debug!(scope = %self.scope, "aad: requesting access token via refresh_token grant");
        let refresh_token = self
            .refresh_token
            .lock()
            .await
            .clone()
            .context("no AAD refresh token available; device-code sign-in must complete before the NATS auth callback runs")?;

        let http = reqwest::Client::new();
        let base = format!("https://login.microsoftonline.com/{}/oauth2/v2.0", self.tenant);
        let response: RefreshTokenResponse = http
            .post(format!("{base}/token"))
            .form(&[
                ("grant_type", "refresh_token"),
                ("client_id", self.client_id.as_str()),
                ("refresh_token", refresh_token.as_str()),
                ("scope", self.scope.as_str()),
            ])
            .send()
            .await?
            .error_for_status()?
            .json()
            .await?;

        if let Some(rotated) = response.refresh_token {
            tracing::debug!("aad: refresh token rotated");
            *self.refresh_token.lock().await = Some(rotated);
        }
        tracing::debug!("aad: access token acquired");
        Ok(response.access_token)
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
            TokenPoll::Success { id_token: "abc".into(), refresh_token: None }
        );
        assert_eq!(
            parse_token_poll(&serde_json::json!({"id_token": "abc", "refresh_token": "rt-1"})),
            TokenPoll::Success { id_token: "abc".into(), refresh_token: Some("rt-1".into()) }
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

    #[test]
    fn device_code_scope_includes_offline_access_and_extra_scopes() {
        assert_eq!(device_code_scope(&[]), "openid profile offline_access");
        assert_eq!(
            device_code_scope(&["api://x/Nats.Connect".to_string()]),
            "openid profile offline_access api://x/Nats.Connect"
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

    #[tokio::test]
    async fn aad_token_provider_errors_without_a_refresh_token_yet() {
        let provider = AadTokenProvider {
            client_id: "c".into(),
            tenant: "organizations".into(),
            scope: "api://x/Nats.Connect".into(),
            refresh_token: Arc::new(tokio::sync::Mutex::new(None)),
        };

        assert!(provider.access_token().await.is_err());
    }
}
