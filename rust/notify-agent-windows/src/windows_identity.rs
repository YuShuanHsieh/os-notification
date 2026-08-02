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
/// - Drops a `DOMAIN\`/`MACHINENAME\` prefix if present: the input may be
///   the SAM-compatible qualified name (`DOMAIN\username`, or
///   `MACHINENAME\username` on a non-domain-joined machine) from
///   `current_windows_username` below, or the bare name from its fallback.
///   Either way, only the portion after the last `\` becomes the identity —
///   deployments using this Windows head are expected to guarantee
///   account-name uniqueness themselves, so no domain disambiguation is
///   attempted here.
/// - Lowercases.
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
///   to run the agent at all. Same transformation applied identically in
///   the sibling C#/Go Windows-head implementations.
pub fn user_id_from_username(raw: &str) -> anyhow::Result<String> {
    let normalized = raw.trim().to_lowercase();
    if normalized.is_empty() {
        anyhow::bail!("windows username {raw:?} is empty after trimming");
    }
    let account_name = match normalized.rsplit_once('\\') {
        Some((_domain, account)) => account,
        None => &normalized,
    };
    if account_name.is_empty() {
        anyhow::bail!("windows username {raw:?} has no account name after the domain separator");
    }
    let sanitized: String = account_name
        .chars()
        .map(|c| {
            if c.is_ascii_alphanumeric() || c == '_' || c == '-' {
                c
            } else {
                '_'
            }
        })
        .collect();
    Ok(sanitized)
}

#[cfg(windows)]
mod win {
    use super::*;
    use async_trait::async_trait;
    use notify_agent_core::identity::{AgentIdentity, IdentityProvider};

    /// Direct Win32 call (`secur32.dll`'s `GetUserNameExW`, requested with
    /// `NameSamCompatible`) rather than `std::env::var("USERNAME")`: it asks
    /// the OS for the actual signed-in account name instead of trusting a
    /// process environment variable (which, in principle, could be absent,
    /// stale, or overridden by the process's own environment), and unlike
    /// the plain `GetUserNameW` API, `NameSamCompatible` returns the
    /// domain-qualified `DOMAIN\username` form (or `MACHINENAME\username` on
    /// a non-domain-joined machine) — see `user_id_from_username`'s doc
    /// comment for why the domain qualifier matters.
    ///
    /// Uses the same two-call buffer-size-discovery pattern as the
    /// `GetUserNameW` call it replaces: call once to learn the required
    /// buffer size, then call again with a correctly sized buffer. Unlike
    /// `GetUserNameW`, `GetUserNameExW`'s size-on-success value does *not*
    /// include the trailing NUL, so the two calls' returned lengths are not
    /// interchangeable — each is handled according to its own documented
    /// convention below.
    fn current_windows_username() -> anyhow::Result<String> {
        use windows::core::PWSTR;
        use windows::Win32::Security::Authentication::Identity::{
            GetUserNameExW, NameSamCompatible,
        };

        // First call: an intentionally empty buffer always fails, but
        // populates `len` with the required buffer size (in wide chars).
        // GetUserNameExW does not document whether that required size
        // includes room for the trailing NUL, so the second call below pads
        // by one element to be safe.
        let mut len: u32 = 0;
        unsafe {
            let _ = GetUserNameExW(NameSamCompatible, PWSTR::null(), &mut len);
        }
        if len == 0 {
            anyhow::bail!("GetUserNameExW did not report a required buffer size");
        }
        let mut buf = vec![0u16; (len as usize) + 1];
        let mut cap = buf.len() as u32;
        let ok = unsafe { GetUserNameExW(NameSamCompatible, PWSTR(buf.as_mut_ptr()), &mut cap) };
        if ok.0 == 0 {
            let err = unsafe { windows::Win32::Foundation::GetLastError() };
            anyhow::bail!("GetUserNameExW failed: {err:?}");
        }
        // On success `cap` is the number of characters written, *not*
        // including the terminating NUL (the opposite convention from
        // `GetUserNameW`'s success case, which does include it).
        let end = (cap as usize).min(buf.len());
        Ok(String::from_utf16_lossy(&buf[..end]))
    }

    /// Fallback used only if `GetUserNameExW` fails (e.g. `secur32.dll`'s
    /// domain lookup is unavailable for some reason): the plain
    /// `advapi32.dll` `GetUserNameW` API, which never returns a domain
    /// prefix. Turning "domain info unavailable" into a hard startup
    /// failure would be a regression from today's behavior (which has no
    /// domain-lookup step to fail at all), so this falls back to a
    /// bare-username identity rather than erroring out. Deliberately does
    /// *not* attempt to reconstruct or strip a domain prefix here — this
    /// path either has no domain qualifier to begin with (the whole reason
    /// it's a fallback) or, on the off chance it did, the intent of this
    /// change is domain-inclusive identity everywhere, so there is no
    /// domain-stripping step anywhere in this module anymore.
    fn current_windows_username_fallback() -> anyhow::Result<String> {
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
            // Prefer the domain-qualified `GetUserNameExW` lookup; fall back
            // to the plain `GetUserNameW` API (see
            // `current_windows_username_fallback`'s doc comment) rather than
            // failing outright if it errors or comes back empty, since that
            // path never had a domain-lookup step to fail at before this
            // change.
            let username = current_windows_username()
                .and_then(|name| {
                    if name.trim().is_empty() {
                        anyhow::bail!("GetUserNameExW returned an empty username");
                    }
                    Ok(name)
                })
                .or_else(|primary_err| {
                    current_windows_username_fallback().map_err(|fallback_err| {
                        anyhow::anyhow!(
                            "GetUserNameExW failed ({primary_err}) and GetUserNameW \
                             fallback also failed ({fallback_err})"
                        )
                    })
                })?;
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

    /// Asserts `id` only contains allowlisted characters (`[a-z0-9_-]`) and
    /// is non-empty.
    fn assert_matches_id_shape(id: &str) {
        assert!(!id.is_empty(), "id must not be empty");
        assert!(
            id.chars()
                .all(|c| c.is_ascii_alphanumeric() || c == '_' || c == '-'),
            "{id:?} contains a character outside [a-z0-9_-]"
        );
    }

    #[test]
    fn lowercases() {
        let id = user_id_from_username("Alice").unwrap();
        assert_eq!(id, "alice");
    }

    #[test]
    fn drops_domain_prefix() {
        // Deployments using this Windows head are expected to guarantee
        // account-name uniqueness themselves, so the domain/machine
        // qualifier is discarded rather than folded into the id.
        let id = user_id_from_username(r"CONTOSO\Bob").unwrap();
        assert_eq!(id, "bob");
    }

    #[test]
    fn trims_whitespace() {
        let id = user_id_from_username("  alice  ").unwrap();
        assert_eq!(id, "alice");
    }

    #[test]
    fn sanitizes_interior_space() {
        // The confirmed-exploitable case: an unsanitized interior space would
        // let the id split a NATS `SUB <subject> [queue-group] <sid>` line
        // into subject + queue-group. Must come out as one safe token.
        let id = user_id_from_username("John Doe").unwrap();
        assert_eq!(id, "john_doe");
    }

    #[test]
    fn sanitizes_dot() {
        let id = user_id_from_username("john.doe").unwrap();
        assert_eq!(id, "john_doe");
    }

    #[test]
    fn sanitizes_star() {
        let id = user_id_from_username("user*name").unwrap();
        assert_eq!(id, "user_name");
    }

    #[test]
    fn sanitizes_gt() {
        let id = user_id_from_username("user>name").unwrap();
        assert_eq!(id, "user_name");
    }

    #[test]
    fn rejects_empty_username() {
        assert!(user_id_from_username("").is_err());
        assert!(user_id_from_username("   ").is_err());
    }

    #[test]
    fn rejects_domain_with_no_account_name() {
        assert!(user_id_from_username(r"CONTOSO\").is_err());
    }

    #[test]
    fn domain_qualified_and_bare_usernames_collapse_to_the_same_id() {
        // Documents the deliberate behavior: with the domain qualifier
        // dropped, identically-named accounts in different domains (and
        // their bare equivalent) all resolve to the same id.
        let corp = user_id_from_username(r"CORP\jdoe").unwrap();
        let contoso = user_id_from_username(r"CONTOSO\jdoe").unwrap();
        let no_domain = user_id_from_username("jdoe").unwrap();
        assert_eq!(corp, "jdoe");
        assert_eq!(contoso, "jdoe");
        assert_eq!(no_domain, "jdoe");
    }

    #[test]
    fn accepts_common_username_shapes() {
        for raw in ["jdoe", "j.doe-test_1", "MACHINE\\svc_account"] {
            let id = user_id_from_username(raw).unwrap();
            assert_matches_id_shape(&id);
        }
    }

    #[test]
    fn is_deterministic() {
        // Same input, same output every time — required for a stable
        // per-user identity.
        assert_eq!(
            user_id_from_username("Alice").unwrap(),
            user_id_from_username("Alice").unwrap()
        );
        assert_eq!(
            user_id_from_username(r"CONTOSO\svc.deploy").unwrap(),
            user_id_from_username(r"CONTOSO\svc.deploy").unwrap()
        );
    }

    #[test]
    fn degenerate_all_punctuation_username_still_succeeds() {
        // sanitize replaces rather than strips characters, so an
        // all-punctuation username still produces a non-empty, usable id.
        let id = user_id_from_username("***").unwrap();
        assert_eq!(id, "___");
    }
}
