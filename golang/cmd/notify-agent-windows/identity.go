// Windows-username-derived identity for the Windows head: the pure,
// platform-independent half of Feature 3 (deriving a default identity from
// the OS username so NOTIFY_USER_ID is no longer required here).
//
// This file has no `//go:build windows` constraint: the GetUserNameEx call
// (SAM-compatible, domain-qualified name) and the identity.Provider
// implementation that uses it live in identity_windows.go, but the
// username-to-identity transformation and its NATS-subject-safety
// validation are pure string logic with no Windows-specific dependency, so
// they stay here, testable on any platform -- same split as
// toastscript.go/settings.go.
//
// Using the Windows account name as identity is a deliberate, documented,
// Windows-heads-only exception to this product's general "OS account name
// is never used as identity" rule (see internal/identity's package doc and
// context/contracts-and-invariants.md): this Go port has no AAD/device-code
// identity path at all, so the username-derived ID is used unconditionally
// here, not merely as an AAD fallback.
package main

import (
	"fmt"
	"os"
	"strings"
)

// userIDFromWindowsUsername normalizes a raw Windows username -- whether
// bare ("username", GetUserNameW's format) or domain-qualified
// ("DOMAIN\username"/"MACHINENAME\username", GetUserNameEx's SAM-compatible
// format) -- into this product's identity shape: just the plain,
// lowercased, sanitized account name, with any domain/machine qualifier
// dropped. Deployments using this Windows head are expected to guarantee
// account-name uniqueness themselves (e.g. a single domain, or names that
// don't collide across domains/machines); this function does not attempt to
// disambiguate on their behalf.
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
// replacing every character outside [a-z0-9_-] with '_'. This exact
// algorithm (strip domain, sanitize) is shared verbatim across this
// product's C# and Rust Windows-head implementations; keep them in sync if
// this changes.
func userIDFromWindowsUsername(raw string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return "", fmt.Errorf("windows username %q is empty", raw)
	}

	if idx := strings.LastIndex(normalized, `\`); idx != -1 {
		normalized = normalized[idx+1:]
	}
	if normalized == "" {
		return "", fmt.Errorf("windows username %q has no account name after the domain separator", raw)
	}

	return sanitizeForIdentity(normalized), nil
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

// resolveWindowsDeviceID applies ResolveDeviceID's env-then-file precedence
// and falls back to defaultWindowsDeviceID's hostname-derived default when
// neither tier supplies one. This is the exact device-ID resolution
// WindowsUsernameIdentity.Resolve (identity_windows.go) performs -- factored
// out here, in this file with no `//go:build windows` constraint, so the
// full "settings file -> env -> hostname fallback" chain (including the
// whitespace-only-settings-value case ResolveDeviceID now handles) is
// testable on any platform without needing the real GetUserNameW syscall
// identity_windows.go's Resolve otherwise depends on.
func resolveWindowsDeviceID(getenv func(string) string, s Settings) (string, error) {
	if deviceID := ResolveDeviceID(getenv, s); deviceID != "" {
		return deviceID, nil
	}
	return defaultWindowsDeviceID()
}
