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

	"github.com/YuShuanHsieh/os-notification/golang/internal/host"
)

// userIDFromWindowsUsername normalizes a raw Windows username (as returned
// by GetUserNameW, which may be qualified as "DOMAIN\username") into this
// product's "u_{...}" identity shape -- lowercased, matching the shape
// other identity sources in this product use (e.g. "u_{oid}" for AAD) --
// and sanitizes it to be safe to embed in a NATS subject.
//
// Windows account names are not under this product's control and commonly
// contain characters a NATS subject can't safely carry -- a space ("John
// Doe") or a dot ("john.doe", an extremely common shape in Windows/AD
// environments) are both completely ordinary account names, not edge cases.
// Since this head has no alternative identity path (no AAD, no
// NOTIFY_USER_ID), rejecting such a username outright would leave that user
// with no way to run the agent at all. So, unlike a resolved user ID from an
// arbitrary source (which internal/host.ValidateUserIDForSubject rejects
// outright as a final safety net), a raw OS username is sanitized via
// internal/host.SanitizeForSubject instead -- replacing every character
// outside [a-z0-9_-] with '_' -- rather than duplicating that character-class
// logic here.
func userIDFromWindowsUsername(raw string) (string, error) {
	name := raw
	if idx := strings.LastIndexByte(name, '\\'); idx >= 0 {
		name = name[idx+1:]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("windows username %q is empty after stripping any domain prefix", raw)
	}

	sanitized := host.SanitizeForSubject(strings.ToLower(name))
	if sanitized == "" || strings.Trim(sanitized, "_") == "" {
		return "", fmt.Errorf("windows username %q has no usable characters after sanitization", raw)
	}
	return "u_" + sanitized, nil
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
