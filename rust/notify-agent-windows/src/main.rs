#![windows_subsystem = "windows"]

#[cfg(not(windows))]
fn main() {
    eprintln!("notify-agent-windows only runs on Windows. Build with --target x86_64-pc-windows-gnu.");
    std::process::exit(2);
}

#[cfg(windows)]
mod tray;

#[cfg(windows)]
mod win {
    use std::sync::Arc;

    use async_trait::async_trait;
    use chrono::{DateTime, Utc};
    use notify_agent_core::host::{AgentConfig, AgentHost};
    use notify_agent_core::identity::{AadTokenProvider, DeviceCodeIdentity, EnvIdentity, IdentityProvider};
    use notify_agent_core::nats_auth::{
        CredsFileAuth, ExternalAuthServiceAuth, NatsAuthConfig, NatsAuthProvider, validate_auth_service_config,
    };
    use notify_agent_core::toast::{ToastRenderer, ToastRequest};
    use windows::core::{HSTRING, w};
    use windows::Data::Xml::Dom::XmlDocument;
    use windows::UI::Notifications::{ToastNotification, ToastNotificationManager};
    use windows::Win32::Foundation::{ERROR_ALREADY_EXISTS, GetLastError};
    use windows::Win32::System::Threading::CreateMutexW;

    /// Unpackaged-app AppUserModelID; registered per-user in HKCU on first
    /// run (the WinAppSDK Register() substitute — design §6 of the Rust spec).
    const AUMID: &str = "NotifyAgent.Rust";

    fn register_aumid() -> anyhow::Result<()> {
        let hkcu = winreg::RegKey::predef(winreg::enums::HKEY_CURRENT_USER);
        let (key, _) = hkcu.create_subkey(format!(r"Software\Classes\AppUserModelId\{AUMID}"))?;
        key.set_value("DisplayName", &"Desktop Notification Agent (Rust)")?;
        Ok(())
    }

    pub struct WindowsToastRenderer {
        cache: notify_agent_core::image_cache::ImageCache,
    }

    impl WindowsToastRenderer {
        pub fn new() -> anyhow::Result<Self> {
            let dir = std::path::PathBuf::from(std::env::var("LOCALAPPDATA")?)
                .join("DesktopNotificationAgent")
                .join("image-cache");
            Ok(Self { cache: notify_agent_core::image_cache::ImageCache::new(dir) })
        }
    }

    #[async_trait]
    impl ToastRenderer for WindowsToastRenderer {
        async fn show(&self, toast: &ToastRequest) -> anyhow::Result<DateTime<Utc>> {
            let image_path = match &toast.image {
                Some(image_ref) => self.cache.fetch(&image_ref.url).await, // ≤3s, best-effort
                None => None,
            };
            let xml = notify_agent_core::toast_xml::build_toast_xml(toast, image_path.as_deref());

            let doc = XmlDocument::new()?;
            doc.LoadXml(&HSTRING::from(xml))?;
            let notification = ToastNotification::CreateToastNotification(&doc)?;

            // The action button (if any) uses activationType="protocol": the OS
            // launches the pre-validated https URL directly on click via
            // protocol activation. No app code runs, so there is no
            // NotificationInvoked handler / ShellExecuteW here to launch a URL
            // ourselves (CWE-78 fix, ported from commit 2dc820d).
            ToastNotificationManager::CreateToastNotifierWithId(&HSTRING::from(AUMID))?
                .Show(&notification)?;
            Ok(Utc::now())
        }
    }

    /// Stable per-install device id, SHARED with the C# head (same file), so
    /// acks correlate to one device regardless of which agent runs.
    fn device_id() -> anyhow::Result<String> {
        let dir = std::path::PathBuf::from(std::env::var("LOCALAPPDATA")?)
            .join("DesktopNotificationAgent");
        std::fs::create_dir_all(&dir)?;
        let path = dir.join("device-id");
        if let Ok(existing) = std::fs::read_to_string(&path) {
            let existing = existing.trim().to_string();
            if !existing.is_empty() {
                return Ok(existing);
            }
        }
        let id = format!("d-{:x}", std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)?.as_nanos());
        std::fs::write(&path, &id)?;
        Ok(id)
    }

    /// Tray icon + Win32 message loop run on the calling (main) thread; the agent's async
    /// lifetime runs on a dedicated thread with its own tokio runtime, since a blocking
    /// `GetMessageW` pump and a blocking `block_on` can't share one thread (design: system
    /// tray icon, ported from the C# TrayApplicationContext).
    pub fn run() -> anyhow::Result<()> {
        // One instance per interactive session: "Local\" mutexes are
        // session-scoped. Deliberately distinct from the C# mutex name so the
        // two heads can be compared side by side (Rust spec §6).
        unsafe {
            let _mutex = CreateMutexW(None, true, w!("Local\\NotifyAgentRust"))?;
            if GetLastError() == ERROR_ALREADY_EXISTS {
                return Ok(());
            }
        }
        register_aumid()?;

        tracing_subscriber::fmt()
            .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
            .init();

        let (close_tx, close_rx) = tokio::sync::mpsc::unbounded_channel::<()>();
        let (done_tx, done_rx) = std::sync::mpsc::channel::<()>();
        let tray = super::tray::create(close_tx, done_rx)?;

        std::thread::spawn(move || {
            let result = tokio::runtime::Runtime::new()
                .expect("failed to build tokio runtime")
                .block_on(run_agent(close_rx, tray));
            if let Err(e) = result {
                tracing::error!(error = %e, "agent failed to start or run");
            }
            let _ = done_tx.send(());
        });

        super::tray::run_message_loop();
        Ok(())
    }

    /// Starts the agent, then waits for either Ctrl+C or the tray's Close click before shutting
    /// down. On startup failure, flags the tray tooltip and returns — the tray/Close item stay
    /// usable with no `AgentHost` to dispose (matches the C# design's failure path).
    async fn run_agent(mut close_rx: tokio::sync::mpsc::UnboundedReceiver<()>, tray: super::tray::TrayHandle) -> anyhow::Result<()> {
        let host = match start_host().await {
            Ok(host) => host,
            Err(e) => {
                tray.set_start_failed();
                return Err(e);
            }
        };
        tracing::info!(subject = host.subject(), "agent running");
        tokio::select! {
            _ = tokio::signal::ctrl_c() => {}
            _ = close_rx.recv() => {}
        }
        host.shutdown().await
    }

    async fn start_host() -> anyhow::Result<AgentHost> {
        let config = AgentConfig::from_env();
        let renderer: Arc<dyn ToastRenderer> = Arc::new(WindowsToastRenderer::new()?);

        let client_id = std::env::var("NOTIFY_AAD_CLIENT_ID").ok().filter(|s| !s.trim().is_empty());
        let tenant = std::env::var("NOTIFY_AAD_TENANT_ID").unwrap_or_else(|_| "organizations".into());
        let auth_service_url = std::env::var("NOTIFY_NATS_AUTH_SERVICE_URL").ok().filter(|s| !s.trim().is_empty());
        let auth_service_scope = std::env::var("NOTIFY_NATS_AUTH_SERVICE_SCOPE").ok().filter(|s| !s.trim().is_empty());
        let creds_file = std::env::var("NOTIFY_NATS_CREDS_FILE").ok().filter(|s| !s.trim().is_empty());

        validate_auth_service_config(&NatsAuthConfig {
            auth_service_url: auth_service_url.clone(),
            auth_service_scope: auth_service_scope.clone(),
            has_aad_identity: client_id.is_some(),
        })?;

        tracing::debug!(
            aad_identity = client_id.is_some(),
            auth_service = auth_service_url.is_some(),
            creds_file = creds_file.is_some(),
            "nats auth: startup config resolved"
        );

        let refresh_token: Arc<tokio::sync::Mutex<Option<String>>> = Arc::new(tokio::sync::Mutex::new(None));
        let extra_scopes = match &auth_service_scope {
            Some(scope) if auth_service_url.is_some() => vec![scope.clone()],
            _ => Vec::new(),
        };

        let identity: Arc<dyn IdentityProvider> = match &client_id {
            Some(client_id) => {
                tracing::debug!(client_id = %client_id, "identity: mode = device-code (AAD)");
                Arc::new(DeviceCodeIdentity {
                    client_id: client_id.clone(),
                    tenant: tenant.clone(),
                    device_id: device_id()?,
                    renderer: renderer.clone(),
                    extra_scopes,
                    refresh_token_sink: Some(refresh_token.clone()),
                })
            }
            None => {
                tracing::debug!("identity: mode = env (NOTIFY_USER_ID)");
                Arc::new(EnvIdentity)
            }
        };

        let auth_provider: Option<Arc<dyn NatsAuthProvider>> = match auth_service_url {
            Some(url) => {
                tracing::debug!(url = %url, "nats auth: mode = external-auth-service");
                let token_provider = Arc::new(AadTokenProvider::new(
                    client_id.expect("validated above: auth service requires an AAD client id"),
                    tenant,
                    auth_service_scope.expect("validated above: auth service requires a scope"),
                    refresh_token,
                ));
                let provider = ExternalAuthServiceAuth::new(url, token_provider)?;
                Some(Arc::new(provider) as Arc<dyn NatsAuthProvider>)
            }
            None => match creds_file {
                Some(path) => {
                    tracing::debug!(path = %path, "nats auth: mode = creds-file");
                    Some(Arc::new(CredsFileAuth { path }) as Arc<dyn NatsAuthProvider>)
                }
                None => {
                    tracing::debug!("nats auth: mode = none (unauthenticated)");
                    None
                }
            },
        };

        let host = AgentHost::start(config, identity, renderer, auth_provider).await?;
        Ok(host)
    }
}

#[cfg(windows)]
fn main() -> anyhow::Result<()> {
    win::run()
}
