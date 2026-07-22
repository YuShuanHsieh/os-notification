#[cfg(not(windows))]
fn main() {
    eprintln!("notify-agent-windows only runs on Windows. Build with --target x86_64-pc-windows-gnu.");
    std::process::exit(2);
}

#[cfg(windows)]
mod win {
    use std::sync::Arc;

    use async_trait::async_trait;
    use chrono::{DateTime, Utc};
    use notify_agent_core::host::{AgentConfig, AgentHost};
    use notify_agent_core::identity::{DeviceCodeIdentity, EnvIdentity, IdentityProvider};
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

        tokio::runtime::Runtime::new()?.block_on(async {
            tracing_subscriber::fmt()
                .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
                .init();
            let config = AgentConfig::from_env();
            let renderer: Arc<dyn ToastRenderer> = Arc::new(WindowsToastRenderer::new()?);
            let identity: Arc<dyn IdentityProvider> = match std::env::var("NOTIFY_AAD_CLIENT_ID") {
                Ok(client_id) if !client_id.trim().is_empty() => Arc::new(DeviceCodeIdentity {
                    client_id,
                    tenant: std::env::var("NOTIFY_AAD_TENANT_ID").unwrap_or_else(|_| "organizations".into()),
                    device_id: device_id()?,
                    renderer: renderer.clone(),
                }),
                _ => Arc::new(EnvIdentity),
            };
            let host = AgentHost::start(config, identity, renderer).await?;
            tracing::info!(subject = host.subject(), "agent running");
            tokio::signal::ctrl_c().await?;
            host.shutdown().await
        })
    }
}

#[cfg(windows)]
fn main() -> anyhow::Result<()> {
    win::run()
}
