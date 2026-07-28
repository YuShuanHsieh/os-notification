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
	"fmt"
	"os"
	"strings"
)

// userIDFromWindowsUsername normalizes a raw Windows username (as returned
// by GetUserNameW, which may be qualified as "DOMAIN\username") into this
// product's "u_{...}" identity shape -- lowercased, matching the shape
// other identity sources in this product use (e.g. "u_{oid}" for AAD) -- and
// validates the result is safe to embed in a NATS subject.
//
// The validation replicates internal/host's unexported
// validateUserIDForSubject check (added there after a security review to
// reject '.', '*', '>' before a resolved user ID is substituted into a
// subject template like "notify.user.%s.desktop"): that function isn't
// exported for reuse across packages, so the same check is duplicated here
// rather than importing internal/host purely for it.
func userIDFromWindowsUsername(raw string) (string, error) {
	name := raw
	if idx := strings.LastIndexByte(name, '\\'); idx >= 0 {
		name = name[idx+1:]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("windows username %q is empty after stripping any domain prefix", raw)
	}

	userID := "u_" + strings.ToLower(name)
	if strings.ContainsAny(userID, ".*>") {
		return "", fmt.Errorf("resolved user ID %q must not contain NATS subject wildcard/delimiter characters ('.', '*', '>')", userID)
	}
	return userID, nil
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
