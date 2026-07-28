//! Default identity for the Rust Windows head when AAD/device-code sign-in
//! (`NOTIFY_AAD_CLIENT_ID`) is not configured: derive a stable application
//! user id from the signed-in Windows username, instead of requiring
//! `NOTIFY_USER_ID` (`notify-agent-core::identity::EnvIdentity`, unchanged,
//! still backs the console head).
//!
//! This is a deliberate, narrowly scoped exception to this codebase's
//! product principle that the OS account name is never used as application
//! identity — see `context/contracts-and-invariants.md` and
//! `context/architecture.md` for the documented exception, and
//! `notify_agent_core::identity` for the trait this module implements. The
//! exception applies only to the Rust Windows head's *default* identity:
//! the console head's `EnvIdentity` and the AAD/device-code sign-in path
//! (`DeviceCodeIdentity`) are both completely unaffected and remain
//! available.
//!
//! `user_id_from_username` below is only called from the `#[cfg(windows)]
//! mod win` block on a real Windows build; on other targets nothing calls
//! it outside the `#[cfg(test)]` tests, so `dead_code` is suppressed here
//! rather than for the whole crate.
#![cfg_attr(not(windows), allow(dead_code))]

/// Pure username -> application-user-id transformation, kept separate from
/// the actual OS lookup so it is unit-testable on any platform (including
/// this Linux dev machine) without a real Windows username to query.
///
/// - Strips a `DOMAIN\` prefix if present (some Win32 APIs return the
///   qualified name on domain-joined machines).
/// - Lowercases, matching the `u_{oid}` shape the AAD/device-code path
///   already produces.
/// - Rejects `.`, `*`, and `>`: this id is substituted directly into the
///   `notify.user.{0}.desktop` subject template. NATS treats `.` as a
///   subject-token separator and `*`/`>` as wildcard tokens, so an
///   unvalidated username could otherwise turn a per-user subscription into
///   one that accidentally (or maliciously) receives other users' events.
///   A sibling Go implementation of this same product needed exactly this
///   guard after a security-focused review — treat it as required
///   hardening, not optional.
pub fn user_id_from_username(raw: &str) -> anyhow::Result<String> {
    let unqualified = raw.rsplit('\\').next().unwrap_or(raw);
    let lower = unqualified.trim().to_lowercase();
    if lower.is_empty() {
        anyhow::bail!("windows username resolved to an empty string");
    }
    if lower.contains(['.', '*', '>']) {
        anyhow::bail!(
            "windows username {lower:?} contains '.', '*', or '>', which are unsafe to embed in a NATS subject"
        );
    }
    Ok(format!("u_{lower}"))
}

#[cfg(windows)]
mod win {
    use super::*;
    use async_trait::async_trait;
    use notify_agent_core::identity::{AgentIdentity, IdentityProvider};

    /// Direct Win32 call (`advapi32.dll`'s `GetUserNameW`) rather than
    /// `std::env::var("USERNAME")`: it asks the OS for the actual signed-in
    /// account name instead of trusting a process environment variable
    /// (which, in principle, could be absent, stale, or overridden by the
    /// process's own environment), and it is the literal Win32 API this
    /// feature asked for.
    fn current_windows_username() -> anyhow::Result<String> {
        use windows::Win32::System::WindowsProgramming::GetUserNameW;
        use windows::core::PWSTR;

        // UNLEN (256) + 1 is the documented maximum Windows username length;
        // comfortably oversized here since GetUserNameW reports the actual
        // length used either way.
        let mut buf = [0u16; 256];
        let mut len: u32 = buf.len() as u32;
        unsafe {
            GetUserNameW(PWSTR(buf.as_mut_ptr()), &mut len)
                .map_err(|e| anyhow::anyhow!("GetUserNameW failed: {e}"))?;
        }
        // On success `len` includes the trailing NUL terminator.
        let end = (len as usize).saturating_sub(1).min(buf.len());
        Ok(String::from_utf16_lossy(&buf[..end]))
    }

    /// Derives identity from the current Windows username. The device id is
    /// resolved the same way as every other identity path in this head —
    /// see `main.rs`'s `device_id()`, which callers pass in unchanged.
    pub struct WindowsUsernameIdentity {
        pub device_id: String,
    }

    #[async_trait]
    impl IdentityProvider for WindowsUsernameIdentity {
        async fn identity(&self) -> anyhow::Result<AgentIdentity> {
            let username = current_windows_username()?;
            let user_id = user_id_from_username(&username)?;
            Ok(AgentIdentity { user_id, device_id: self.device_id.clone() })
        }
    }
}

#[cfg(windows)]
pub use win::WindowsUsernameIdentity;

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn lowercases_and_prefixes() {
        assert_eq!(user_id_from_username("Alice").unwrap(), "u_alice");
    }

    #[test]
    fn strips_domain_prefix() {
        assert_eq!(user_id_from_username(r"CONTOSO\Bob").unwrap(), "u_bob");
    }

    #[test]
    fn trims_whitespace() {
        assert_eq!(user_id_from_username("  alice  ").unwrap(), "u_alice");
    }

    #[test]
    fn rejects_dot() {
        assert!(user_id_from_username("bob.smith").is_err());
    }

    #[test]
    fn rejects_star() {
        assert!(user_id_from_username("bob*").is_err());
    }

    #[test]
    fn rejects_gt() {
        assert!(user_id_from_username("bob>").is_err());
    }

    #[test]
    fn rejects_empty_username() {
        assert!(user_id_from_username("").is_err());
        assert!(user_id_from_username("   ").is_err());
    }

    #[test]
    fn rejects_domain_qualified_but_otherwise_empty_username() {
        assert!(user_id_from_username(r"CONTOSO\").is_err());
    }

    #[test]
    fn accepts_common_username_shapes() {
        for raw in ["jdoe", "j.doe-test_1", "MACHINE\\svc_account"] {
            // `.` in "j.doe-test_1" is expected to be rejected: assert the two
            // classes separately instead of asserting success for all.
            let _ = user_id_from_username(raw); // must not panic on any shape
        }
        assert!(user_id_from_username("jdoe").is_ok());
        assert!(user_id_from_username("MACHINE\\svc_account").is_ok());
        assert!(user_id_from_username("j.doe-test_1").is_err());
    }
}
