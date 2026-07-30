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
/// - Keeps a `DOMAIN\` prefix if present rather than stripping it: the input
///   is the SAM-compatible qualified name (`DOMAIN\username`, or
///   `MACHINENAME\username` on a non-domain-joined machine), and the domain
///   is deliberately retained so two different domains' identically-named
///   accounts (`CORP\jdoe` vs `CONTOSO\jdoe`) derive different identities
///   instead of colliding. See `current_windows_username` below for how the
///   qualified name is obtained.
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
/// - The allowlist sanitization above is inherently lossy: distinct
///   usernames can sanitize to the identical string (`"user.name"` and
///   `"user_name"` both become `user_name`), which would collide two
///   different users onto one identity/NATS subject. To stay injective, an
///   8-hex-char suffix of `SHA-256(normalized username)` — hashed *before*
///   sanitization, so inputs that sanitize identically still diverge — is
///   appended to the sanitized prefix. The hash suffix also means the final
///   id is always non-empty/usable regardless of how degenerate the
///   sanitized prefix is (e.g. an all-punctuation username sanitizes to all
///   underscores), so the only remaining error case is an empty username
///   after trimming. Same fix applied identically in the sibling C#/Go
///   Windows-head implementations.
pub fn user_id_from_username(raw: &str) -> anyhow::Result<String> {
    let normalized = raw.trim().to_lowercase();
    if normalized.is_empty() {
        anyhow::bail!("windows username {raw:?} is empty after trimming");
    }
    let sanitized: String = normalized
        .chars()
        .map(|c| {
            if c.is_ascii_alphanumeric() || c == '_' || c == '-' {
                c
            } else {
                '_'
            }
        })
        .collect();
    let hash8 = hash_prefix8(&normalized);
    Ok(format!("u_{sanitized}_{hash8}"))
}

/// First 8 lowercase hex characters (4 bytes) of `SHA-256(input)`, used as a
/// collision-resistant suffix so two usernames that sanitize identically
/// still produce different ids (see `user_id_from_username`'s doc comment).
/// Hand-rolled hex formatting rather than pulling in a `hex` crate for 4
/// bytes.
fn hash_prefix8(input: &str) -> String {
    use sha2::{Digest, Sha256};
    let digest = Sha256::digest(input.as_bytes());
    digest[..4].iter().map(|b| format!("{b:02x}")).collect()
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

    /// Checks the shape `u_<sanitized>_<8 lowercase hex chars>` without
    /// hardcoding the hash (which depends on the actual SHA-256 output and
    /// can't be hand-computed in a test). Also asserts the sanitized middle
    /// section only contains allowlisted characters.
    fn assert_matches_id_shape(id: &str) {
        let rest = id
            .strip_prefix("u_")
            .unwrap_or_else(|| panic!("{id:?} does not start with \"u_\""));
        let (sanitized, hash8) = rest
            .rsplit_once('_')
            .unwrap_or_else(|| panic!("{id:?} has no '_' separating sanitized prefix from hash"));
        assert_eq!(
            hash8.len(),
            8,
            "hash suffix of {id:?} should be exactly 8 hex chars, got {hash8:?}"
        );
        assert!(
            hash8
                .chars()
                .all(|c| c.is_ascii_hexdigit() && !c.is_ascii_uppercase()),
            "hash suffix of {id:?} should be lowercase hex, got {hash8:?}"
        );
        assert!(
            sanitized
                .chars()
                .all(|c| c.is_ascii_alphanumeric() || c == '_' || c == '-'),
            "sanitized portion of {id:?} contains a character outside [a-z0-9_-]: {sanitized:?}"
        );
    }

    #[test]
    fn lowercases_and_prefixes() {
        let id = user_id_from_username("Alice").unwrap();
        assert_matches_id_shape(&id);
        assert!(id.starts_with("u_alice_"), "got {id:?}");
    }

    #[test]
    fn retains_domain_prefix() {
        // The point of this change: the domain qualifier is kept (sanitized
        // to `_` like any other disallowed character) rather than stripped,
        // so it contributes to both the sanitized prefix and the hash.
        let id = user_id_from_username(r"CONTOSO\Bob").unwrap();
        assert_matches_id_shape(&id);
        assert!(id.starts_with("u_contoso_bob_"), "got {id:?}");
    }

    #[test]
    fn trims_whitespace() {
        let id = user_id_from_username("  alice  ").unwrap();
        assert_matches_id_shape(&id);
        assert!(id.starts_with("u_alice_"), "got {id:?}");
    }

    #[test]
    fn sanitizes_interior_space() {
        // The confirmed-exploitable case: an unsanitized interior space would
        // let the id split a NATS `SUB <subject> [queue-group] <sid>` line
        // into subject + queue-group. Must come out as one safe token.
        let id = user_id_from_username("John Doe").unwrap();
        assert_matches_id_shape(&id);
        assert!(id.starts_with("u_john_doe_"), "got {id:?}");
    }

    #[test]
    fn sanitizes_dot() {
        let id = user_id_from_username("john.doe").unwrap();
        assert_matches_id_shape(&id);
        assert!(id.starts_with("u_john_doe_"), "got {id:?}");
    }

    #[test]
    fn sanitizes_star() {
        let id = user_id_from_username("user*name").unwrap();
        assert_matches_id_shape(&id);
        assert!(id.starts_with("u_user_name_"), "got {id:?}");
    }

    #[test]
    fn sanitizes_gt() {
        let id = user_id_from_username("user>name").unwrap();
        assert_matches_id_shape(&id);
        assert!(id.starts_with("u_user_name_"), "got {id:?}");
    }

    #[test]
    fn rejects_empty_username() {
        assert!(user_id_from_username("").is_err());
        assert!(user_id_from_username("   ").is_err());
    }

    #[test]
    fn distinguishes_domains_with_identical_usernames() {
        // The actual regression this change fixes: `GetUserNameW` never
        // returns a domain, so on a domain-joined machine two different
        // domains' identically-named accounts used to derive the exact same
        // identity. With the domain retained, they must now differ.
        let corp = user_id_from_username(r"CORP\jdoe").unwrap();
        let contoso = user_id_from_username(r"CONTOSO\jdoe").unwrap();
        let no_domain = user_id_from_username("jdoe").unwrap();
        assert_matches_id_shape(&corp);
        assert_matches_id_shape(&contoso);
        assert_matches_id_shape(&no_domain);
        assert_ne!(
            corp, contoso,
            r"CORP\jdoe and CONTOSO\jdoe must not collide onto the same id, got {corp:?} == {contoso:?}"
        );
        assert_ne!(
            corp, no_domain,
            r"CORP\jdoe and bare jdoe must not collide onto the same id, got {corp:?} == {no_domain:?}"
        );
        assert_ne!(
            contoso, no_domain,
            r"CONTOSO\jdoe and bare jdoe must not collide onto the same id, got {contoso:?} == {no_domain:?}"
        );
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
        // Same input, same output every time (no randomness/timestamps in the
        // derivation) — required for a stable per-user identity.
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
    fn distinguishes_usernames_that_sanitize_identically() {
        // The actual bug being fixed: "user.name" and "user_name" both
        // sanitize (allowlist-mapped) to the same "user_name" prefix, so
        // without a hash suffix derived from the *pre-sanitization* string
        // they'd collide onto the identical final user id.
        let a = user_id_from_username("user.name").unwrap();
        let b = user_id_from_username("user_name").unwrap();
        assert_matches_id_shape(&a);
        assert_matches_id_shape(&b);
        assert_ne!(
            a, b,
            "\"user.name\" and \"user_name\" must not collide onto the same id, got {a:?} == {b:?}"
        );

        // Same class of collision via a different sanitized character (*).
        let c = user_id_from_username("user*name").unwrap();
        let d = user_id_from_username("user_name").unwrap();
        assert_ne!(c, d, "\"user*name\" and \"user_name\" must not collide");
    }

    #[test]
    fn degenerate_all_punctuation_username_still_succeeds() {
        // No longer an error case: the hash suffix guarantees a non-empty,
        // usable id no matter how degenerate the sanitized prefix is. Two
        // different all-punctuation usernames must still land on different
        // ids (they normalize/hash differently even though both sanitize to
        // all underscores).
        let a = user_id_from_username("***").unwrap();
        let b = user_id_from_username("...").unwrap();
        assert_matches_id_shape(&a);
        assert_matches_id_shape(&b);
        assert_ne!(a, b);
    }
}
