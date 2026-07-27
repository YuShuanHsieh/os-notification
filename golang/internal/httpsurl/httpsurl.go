// Package httpsurl implements the shared HTTPS URL safety policy used to
// validate image and action URLs before they are handed to Windows toast
// rendering, mirroring HttpsUrlPolicy.cs (C#) and action_url.rs (Rust)
// elsewhere in this repository. A URL is safe only if it is an absolute,
// well-formed https:// URL with a real host and no embedded credentials.
package httpsurl

import "net/url"

// MaxURLLength is the maximum accepted length, in characters, of a raw URL
// string per the shared contract (context/contracts-and-invariants.md).
const MaxURLLength = 2048

// Valid reports whether rawURL is an absolute, well-formed https:// URL with
// a non-empty host, no embedded user-info (no "user:pass@host" form), and
// length <= MaxURLLength.
func Valid(rawURL string) bool {
	if rawURL == "" || len(rawURL) > MaxURLLength {
		return false
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	if !u.IsAbs() || u.Scheme != "https" {
		return false
	}

	if u.Hostname() == "" {
		return false
	}

	if u.User != nil {
		return false
	}

	return true
}
