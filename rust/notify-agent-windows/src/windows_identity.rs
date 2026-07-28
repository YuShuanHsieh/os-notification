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
/// - Sanitizes via an *allowlist* (`[a-z0-9_-]`, everything else mapped to
///   `_`) rather than rejecting a denylist of characters: this id is
///   substituted directly into the `notify.user.{0}.desktop` subject
///   template, which flows straight into the NATS wire protocol's
///   whitespace-tokenized `SUB <subject> [queue-group] <sid>` line. A
///   denylist that only blocks `.`/`*`/`>` still lets an interior space
///   through (Windows account names may legitimately contain spaces, e.g.
///   "John Doe", and `trim()` only strips the ends) — the NATS server then
///   parses everything after the space as a queue-group token, silently
///   truncating the subject and misrouting the subscription with no error
///   logged anywhere. An allowlist closes this class of bug entirely rather
///   than chasing individual unsafe characters one at a time, and as a
///   bonus stops rejecting extremely common `first.last`-style Windows/AD
///   usernames outright (they're sanitized to `first_last` instead) — this
///   identity path is now the *only* one available when no AAD client id is
///   configured, so a hard rejection would leave those accounts with no way
///   to run the agent at all.
pub fn user_id_from_username(raw: &str) -> anyhow::Result<String> {
    let stripped = strip_domain_prefix(raw.trim());
    let lower = stripped.to_lowercase();
    let sanitized: String = lower
        .chars()
        .map(|c| {
            if c.is_ascii_alphanumeric() || c == '_' || c == '-' {
                c
            } else {
                '_'
            }
        })
        .collect();
    if sanitized.is_empty() || sanitized.chars().all(|c| c == '_') {
        anyhow::bail!(
            "windows username {raw:?} has no usable characters for identity after sanitization"
        );
    }
    Ok(format!("u_{sanitized}"))
}

/// Strips a `DOMAIN\` prefix if present (see `user_id_from_username`'s doc
/// comment).
fn strip_domain_prefix(raw: &str) -> &str {
    raw.rsplit('\\').next().unwrap_or(raw)
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
        use windows::core::PWSTR;
        use windows::Win32::System::WindowsProgramming::GetUserNameW;

        // UNLEN (256) + 1 (for the trailing NUL `GetUserNameW` writes) is the
        // documented maximum buffer size a UNLEN-length username needs; a
        // 256-element buffer was one short of that and would fail a
        // maximum-length username with ERROR_INSUFFICIENT_BUFFER.
        let mut buf = [0u16; 257];
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
            Ok(AgentIdentity {
                user_id,
                device_id: self.device_id.clone(),
            })
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
    fn sanitizes_interior_space() {
        // The confirmed-exploitable case: an unsanitized interior space would
        // let the id split a NATS `SUB <subject> [queue-group] <sid>` line
        // into subject + queue-group. Must come out as one safe token.
        assert_eq!(user_id_from_username("John Doe").unwrap(), "u_john_doe");
    }

    #[test]
    fn sanitizes_dot() {
        assert_eq!(user_id_from_username("john.doe").unwrap(), "u_john_doe");
    }

    #[test]
    fn sanitizes_star() {
        assert_eq!(user_id_from_username("user*name").unwrap(), "u_user_name");
    }

    #[test]
    fn sanitizes_gt() {
        assert_eq!(user_id_from_username("user>name").unwrap(), "u_user_name");
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
    fn rejects_username_that_sanitizes_to_all_underscores() {
        // Nothing but unsafe characters survives sanitization to a usable id.
        assert!(user_id_from_username("***").is_err());
        assert!(user_id_from_username("...").is_err());
    }

    #[test]
    fn accepts_common_username_shapes() {
        assert_eq!(user_id_from_username("jdoe").unwrap(), "u_jdoe");
        assert_eq!(
            user_id_from_username("j.doe-test_1").unwrap(),
            "u_j_doe-test_1"
        );
        assert_eq!(
            user_id_from_username("MACHINE\\svc_account").unwrap(),
            "u_svc_account"
        );
    }
}
