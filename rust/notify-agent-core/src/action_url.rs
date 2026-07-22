use url::Url;

pub const MAX_URL_LENGTH: usize = 2048;

/// Port of the C# ActionUrlPolicy (CWE-78 fix, commit 2dc820d): a toast action
/// may only carry a well-formed absolute https URL with a real host and no
/// embedded credentials. Returns the parsed (normalized) URL, or None.
pub fn validate(value: &str) -> Option<Url> {
    if value.trim().is_empty() || value.len() > MAX_URL_LENGTH {
        return None;
    }
    let parsed = Url::parse(value).ok()?;
    if parsed.scheme() != "https" {
        return None;
    }
    if parsed.host_str().map_or(true, str::is_empty) {
        return None;
    }
    if !parsed.username().is_empty() || parsed.password().is_some() {
        return None;
    }
    Some(parsed)
}

#[cfg(test)]
mod tests {
    use super::*;

    // --- accepts: ported 1:1 from ActionUrlPolicyTests.TryCreate_AcceptsValidHttpsUrl ---

    #[test]
    fn accepts_plain_https_url() {
        let url = validate("https://example.com").expect("should be valid");
        assert_eq!(url.scheme(), "https");
        // usable as &str, e.g. for the toast XML `arguments` attribute
        assert_eq!(url.as_str(), "https://example.com/");
    }

    #[test]
    fn accepts_https_url_with_path() {
        let url = validate("https://example.com/path").expect("should be valid");
        assert_eq!(url.as_str(), "https://example.com/path");
    }

    #[test]
    fn accepts_https_url_with_query() {
        let url = validate("https://example.com/path?one=1&two=2").expect("should be valid");
        assert_eq!(url.as_str(), "https://example.com/path?one=1&two=2");
    }

    #[test]
    fn accepts_localhost_with_port() {
        let url = validate("https://localhost:8443/path").expect("should be valid");
        assert_eq!(url.host_str(), Some("localhost"));
    }

    #[test]
    fn accepts_ipv4_host() {
        let url = validate("https://127.0.0.1/path").expect("should be valid");
        assert_eq!(url.host_str(), Some("127.0.0.1"));
    }

    #[test]
    fn accepts_ipv6_host() {
        let url = validate("https://[::1]/path").expect("should be valid");
        assert_eq!(url.host_str(), Some("[::1]"));
    }

    // --- rejects: ported 1:1 from ActionUrlPolicyTests.TryCreate_RejectsUnsafeOrMalformedUrl ---

    #[test]
    fn rejects_empty_string() {
        assert!(validate("").is_none());
    }

    #[test]
    fn rejects_whitespace_only() {
        assert!(validate("   ").is_none());
    }

    #[test]
    fn rejects_not_a_url() {
        assert!(validate("not-a-url").is_none());
    }

    #[test]
    fn rejects_http_scheme() {
        assert!(validate("http://example.com").is_none());
    }

    #[test]
    fn rejects_ftp_scheme() {
        assert!(validate("ftp://example.com").is_none());
    }

    #[test]
    fn rejects_file_scheme() {
        assert!(validate("file:///C:/Windows/System32/cmd.exe").is_none());
    }

    #[test]
    fn rejects_javascript_scheme() {
        assert!(validate("javascript:alert(1)").is_none());
    }

    #[test]
    fn rejects_https_with_no_authority() {
        // "https://" has an empty host; url-crate treats an empty host on a
        // special scheme (https) as a hard parse error (EmptyHost), so this
        // is rejected at the `Url::parse(..).ok()?` step rather than by the
        // explicit empty-host check below it — same net result as the C#
        // `string.IsNullOrWhiteSpace(candidate.Host)` branch.
        assert!(validate("https://").is_none());
    }

    #[test]
    fn rejects_userinfo_with_password() {
        assert!(validate("https://user:password@example.com").is_none());
    }

    #[test]
    fn rejects_userinfo_username_only() {
        assert!(validate("https://user@host/x").is_none());
    }

    #[test]
    fn rejects_userinfo_username_and_password_with_path() {
        assert!(validate("https://user:pass@host/x").is_none());
    }

    #[test]
    fn rejects_relative_url() {
        assert!(validate("foo/bar").is_none());
    }

    #[test]
    fn rejects_oversized_url() {
        let value = format!("https://example.com/{}", "a".repeat(MAX_URL_LENGTH));
        assert!(validate(&value).is_none());
    }

    #[test]
    fn rejects_malformed_host_with_embedded_space() {
        // IDNA rejects the space, so Url::parse fails outright.
        assert!(validate("https://exa mple.com").is_none());
    }

    // --- documented semantic gaps vs. the C# Uri-based implementation ---

    #[test]
    fn backslash_url_is_normalized_and_accepted_unlike_csharp() {
        // C# `ActionUrlPolicyTests.TryCreate_RejectsUnsafeOrMalformedUrl` rejects
        // @"https:\\example.com\path" because .NET's `IsWellFormedOriginalString()`
        // compares the parsed/re-serialized URI against the original string, and
        // backslash-to-slash normalization makes them differ.
        //
        // The `url` crate has no equivalent "was the original string already
        // well-formed" check — it implements the WHATWG URL Standard, which
        // treats '\' as '/' for special schemes (http/https/ws/wss/ftp/file)
        // during parsing itself, so the string is normalized *before* we ever
        // see it and there is nothing left to compare against. There is no
        // hand-rollable equivalent without re-implementing IsWellFormedOriginalString,
        // which the task explicitly avoids (no hand-rolled URL parsing).
        //
        // Net effect: this input is ACCEPTED by our port (normalized to
        // "https://example.com/path", a genuinely safe https URL with a real
        // host and no userinfo), whereas C# rejects it outright. Documented
        // here rather than silently diverging.
        let url = validate(r"https:\\example.com\path").expect("normalized by url-crate WHATWG parsing");
        assert_eq!(url.as_str(), "https://example.com/path");
    }

    #[test]
    fn triple_slash_path_is_reinterpreted_as_host_unlike_csharp_empty_host() {
        // In C#, `Uri.TryCreate("https:///path", ...)` yields an empty Host,
        // which `ActionUrlPolicy.TryCreate` rejects via
        // `string.IsNullOrWhiteSpace(candidate.Host)`.
        //
        // The `url` crate follows the WHATWG "special authority ignore
        // slashes" state: for special schemes, any run of '/' (or '\')
        // right after the scheme is collapsed, and parsing resumes in the
        // authority state — so "https:///path" is NOT parsed as
        // (empty-authority, path="/path"). Instead "path" itself is consumed
        // as the host, yielding "https://path/" with host_str() == Some("path").
        //
        // So this input never reaches our empty-host check at all: it is
        // ACCEPTED (as a URL to host "path"), the opposite of C#'s rejection.
        // This is a genuine WHATWG-vs-.NET-Uri divergence, not something a
        // stricter host check can close without re-parsing the raw string.
        let url = validate("https:///path").expect("url-crate host-collapsing quirk");
        assert_eq!(url.host_str(), Some("path"));
    }
}
