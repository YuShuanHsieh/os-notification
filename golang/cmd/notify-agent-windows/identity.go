// Windows-username-derived identity for the Windows head: the pure,
// platform-independent half of Feature 3 (deriving a default identity from
// the OS username so NOTIFY_USER_ID is no longer required here).
//
// This file has no `//go:build windows` constraint: the raw
// GetUserNameW syscall wrapper and the identity.Provider implementation
// that calls it live in identity_windows.go, but the username -> "u_{...}"
// transformation and its NATS-subject-safety validation are pure string
// logic with no Windows-specific dependency, so they stay here, testable on
// any platform -- same split as toastscript.go/settings.go.
//
// Using the Windows account name as identity is a deliberate, documented,
// Windows-heads-only exception to this product's general "OS account name
// is never used as identity" rule (see internal/identity's package doc and
// context/contracts-and-invariants.md): this Go port has no AAD/device-code
// identity path at all, so the username-derived ID is used unconditionally
// here, not merely as an AAD fallback.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// userIDFromWindowsUsername normalizes a raw Windows username (as returned
// by GetUserNameW, which may be qualified as "DOMAIN\username") into this
// product's "u_{...}" identity shape -- lowercased, matching the shape
// other identity sources in this product use (e.g. "u_{oid}" for AAD) --
// sanitized to be safe to embed in a NATS subject, and suffixed with a short
// hash of the normalized (pre-sanitization) username so the mapping stays
// injective (collision-resistant).
//
// Windows account names are not under this product's control and commonly
// contain characters a NATS subject can't safely carry -- a space ("John
// Doe") or a dot ("john.doe", an extremely common shape in Windows/AD
// environments) are both completely ordinary account names, not edge cases.
// Since this head has no alternative identity path (no AAD, no
// NOTIFY_USER_ID), rejecting such a username outright would leave that user
// with no way to run the agent at all. So, unlike a resolved user ID from an
// arbitrary source (which internal/host.ValidateUserIDForSubject rejects
// outright as a final safety net), a raw OS username is sanitized instead --
// replacing every character outside [a-z0-9_-] with '_'.
//
// Sanitization alone would be lossy: two genuinely different usernames can
// sanitize to the identical string (e.g. "user.name" and "user_name" both
// sanitize to "user_name"), which would collide two different users onto one
// identity/NATS subject. To stay injective, the first 8 hex characters of
// SHA-256(normalized username) are appended as a suffix -- computed from the
// normalized string *before* sanitization, so two inputs that sanitize
// identically still get different hashes and therefore different final user
// IDs. This exact algorithm (sanitize + hash-of-normalized suffix) is shared
// verbatim across this product's C# and Rust Windows-head implementations,
// which have the identical bug/fix; keep them in sync if this changes.
func userIDFromWindowsUsername(raw string) (string, error) {
	name := raw
	if idx := strings.LastIndexByte(name, '\\'); idx >= 0 {
		name = name[idx+1:]
	}
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		return "", fmt.Errorf("windows username %q is empty after stripping any domain prefix", raw)
	}

	sanitized := sanitizeForIdentity(normalized)
	sum := sha256.Sum256([]byte(normalized))
	hash8 := hex.EncodeToString(sum[:4])
	return fmt.Sprintf("u_%s_%s", sanitized, hash8), nil
}

// sanitizeForIdentity replaces every rune outside the lowercase-alphanumeric
// plus '_'/'-' allowlist with '_'. It exists for identity sources whose raw
// value isn't under this product's control and commonly contains characters
// NATS subjects can't safely carry -- e.g. a Windows account name. This is
// only ever used as one ingredient (alongside the hash suffix above) of
// userIDFromWindowsUsername's injective encoding, not a generic
// subject-safety check on its own -- that's internal/host.ValidateUserIDForSubject,
// which every identity provider's final resolved user ID (including this
// one's) still passes through as a defense-in-depth check inside
// host.start().
func sanitizeForIdentity(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// defaultWindowsDeviceID returns "d-{lowercase hostname}", the same default
// device ID derivation identity.EnvIdentity uses, so a Windows-username
// identity that doesn't have a settings-file/env override still gets a
// stable per-install device ID.
func defaultWindowsDeviceID() (string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("resolve device id: %w", err)
	}
	return "d-" + strings.ToLower(hostname), nil
}
