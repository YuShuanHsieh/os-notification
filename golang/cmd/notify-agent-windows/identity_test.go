package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/YuShuanHsieh/os-notification/golang/internal/host"
)

// userIDPattern matches userIDFromWindowsUsername's output shape:
// "u_{sanitized}_{8 lowercase hex hash chars}". The sanitized portion may
// itself be empty (e.g. an all-unusable-characters username still produces
// a valid, non-empty, unique userID thanks to the hash suffix -- see
// TestUserIDFromWindowsUsername_AllUnusableCharactersStillProducesValidID).
var userIDPattern = regexp.MustCompile(`^u_[a-z0-9_-]*_[0-9a-f]{8}$`)

func TestUserIDFromWindowsUsername(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		wantPrefix string // expected "u_{sanitized}_" prefix, sans hash suffix
		wantErr    bool
	}{
		{"plain username", "jdoe", "u_jdoe_", false},
		{"mixed case lowercased", "JDoe", "u_jdoe_", false},
		{"domain-qualified strips domain", `CONTOSO\jdoe`, "u_jdoe_", false},
		{"domain-qualified mixed case", `Contoso\JDoe`, "u_jdoe_", false},
		{"surrounding whitespace trimmed", "  jdoe  ", "u_jdoe_", false},
		{"blank after stripping domain prefix errors", `CONTOSO\`, "", true},
		{"empty raw errors", "", "", true},
		{"whitespace only errors", "   ", "", true},
		{"space sanitized (common Windows account name shape)", "John Doe", "u_john_doe_", false},
		{"dot sanitized", "john.doe", "u_john_doe_", false},
		{"asterisk sanitized", "user*name", "u_user_name_", false},
		{"greater-than sanitized", "user>name", "u_user_name_", false},
		{"domain-qualified username with dot sanitized", `CONTOSO\j.doe`, "u_j_doe_", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := userIDFromWindowsUsername(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("userIDFromWindowsUsername(%q) = %q, nil; want an error", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("userIDFromWindowsUsername(%q) unexpected error: %v", tt.raw, err)
			}
			if !userIDPattern.MatchString(got) {
				t.Fatalf("userIDFromWindowsUsername(%q) = %q, does not match pattern %s", tt.raw, got, userIDPattern.String())
			}
			if !strings.HasPrefix(got, tt.wantPrefix) {
				t.Fatalf("userIDFromWindowsUsername(%q) = %q, want prefix %q", tt.raw, got, tt.wantPrefix)
			}
		})
	}
}

// TestUserIDFromWindowsUsername_Deterministic verifies that calling the
// function twice with the same input always produces the same output --
// required since the same Windows account must always resolve to the same
// identity/NATS subject across agent restarts.
func TestUserIDFromWindowsUsername_Deterministic(t *testing.T) {
	for _, raw := range []string{"jdoe", "John Doe", `CONTOSO\j.doe`, "***"} {
		first, err := userIDFromWindowsUsername(raw)
		if err != nil {
			t.Fatalf("userIDFromWindowsUsername(%q) unexpected error: %v", raw, err)
		}
		second, err := userIDFromWindowsUsername(raw)
		if err != nil {
			t.Fatalf("userIDFromWindowsUsername(%q) unexpected error on second call: %v", raw, err)
		}
		if first != second {
			t.Fatalf("userIDFromWindowsUsername(%q) is not deterministic: %q != %q", raw, first, second)
		}
	}
}

// TestUserIDFromWindowsUsername_CollidingSanitizedPrefixesStayDistinct is the
// key regression test for the bug this fix addresses: "user.name" and
// "user_name" both sanitize to the identical prefix "user_name" (a '.' and a
// pre-existing '_' both survive/collapse to '_'), which under the old,
// hash-less sanitize-only scheme produced the exact same final user ID for
// two genuinely different usernames -- silently colluding two different
// users onto one identity/NATS subject. The hash-of-normalized-string suffix
// must keep them distinct.
func TestUserIDFromWindowsUsername_CollidingSanitizedPrefixesStayDistinct(t *testing.T) {
	a, err := userIDFromWindowsUsername("user.name")
	if err != nil {
		t.Fatalf("userIDFromWindowsUsername(%q) unexpected error: %v", "user.name", err)
	}
	b, err := userIDFromWindowsUsername("user_name")
	if err != nil {
		t.Fatalf("userIDFromWindowsUsername(%q) unexpected error: %v", "user_name", err)
	}
	if a == b {
		t.Fatalf("userIDFromWindowsUsername(%q) = %q and userIDFromWindowsUsername(%q) = %q, want distinct user IDs for distinct usernames", "user.name", a, "user_name", b)
	}
}

// TestUserIDFromWindowsUsername_AllUnusableCharactersStillProducesValidID
// covers the case the previous review round treated as an error (a username
// that sanitizes to nothing usable, e.g. all-asterisks): the hash suffix
// guarantees a non-empty, valid, unique-enough user ID regardless of what
// the sanitized prefix looks like, so this is no longer an error case.
func TestUserIDFromWindowsUsername_AllUnusableCharactersStillProducesValidID(t *testing.T) {
	got, err := userIDFromWindowsUsername("***")
	if err != nil {
		t.Fatalf("userIDFromWindowsUsername(%q) unexpected error: %v", "***", err)
	}
	if !userIDPattern.MatchString(got) {
		t.Fatalf("userIDFromWindowsUsername(%q) = %q, does not match pattern %s", "***", got, userIDPattern.String())
	}
	if err := host.ValidateUserIDForSubject(got); err != nil {
		t.Fatalf("host.ValidateUserIDForSubject(%q) = %v, want nil", got, err)
	}
}

// TestUserIDFromWindowsUsernameProducesSubscribableSubject is a regression
// test for the reviewer's headline finding: a Windows account name
// containing a space (e.g. "John Doe", an entirely ordinary account name --
// Windows only forbids `" / \ [ ] : ; | = , + * ? < >`, not spaces) used to
// pass the old reject-on-denylist check (which only looked for '.', '*',
// '>') and then fail much later, confusingly, when nc.Subscribe rejected the
// resulting subject outright because NATS subjects cannot contain whitespace.
// Sanitization must close that gap: the resolved user ID must contain no
// whitespace, must pass internal/host.ValidateUserIDForSubject (the same
// final safety net Host.start applies to every identity provider), and the
// fully-formatted subject must actually be free of the characters that make
// nc.Subscribe fail.
func TestUserIDFromWindowsUsernameProducesSubscribableSubject(t *testing.T) {
	userID, err := userIDFromWindowsUsername("John Doe")
	if err != nil {
		t.Fatalf("userIDFromWindowsUsername(%q) unexpected error: %v", "John Doe", err)
	}
	if want := "u_john_doe_"; !strings.HasPrefix(userID, want) {
		t.Fatalf("userIDFromWindowsUsername(%q) = %q, want prefix %q", "John Doe", userID, want)
	}
	if strings.ContainsAny(userID, " \t\r\n") {
		t.Fatalf("userIDFromWindowsUsername(%q) = %q, must not contain whitespace (NATS subjects can't carry it)", "John Doe", userID)
	}
	if err := host.ValidateUserIDForSubject(userID); err != nil {
		t.Fatalf("host.ValidateUserIDForSubject(%q) = %v, want nil", userID, err)
	}

	subject := fmt.Sprintf("notify.user.%s.desktop", userID)
	if strings.ContainsAny(subject, " \t\r\n") {
		t.Fatalf("formatted subject %q contains whitespace, would fail nc.Subscribe with ErrBadSubject", subject)
	}
}

// TestResolveWindowsDeviceID_WhitespaceOnlySettingsValueFallsThroughToHostname
// is the end-to-end regression test for Fix 3: a whitespace-only
// Settings.DeviceID (e.g. a stray `"deviceId": "   "` in settings.json)
// flowing through resolveWindowsDeviceID -- the exact device-ID resolution
// WindowsUsernameIdentity.Resolve (identity_windows.go) performs -- must
// fall all the way through to defaultWindowsDeviceID's hostname-derived
// value, not "win" as a literal whitespace device ID. This exercises the
// real call path (resolveWindowsDeviceID), not just ResolveDeviceID in
// isolation (see settings_test.go's
// TestResolveDeviceID_WhitespaceOnlyFileValueFallsThrough for that).
func TestResolveWindowsDeviceID_WhitespaceOnlySettingsValueFallsThroughToHostname(t *testing.T) {
	want, err := defaultWindowsDeviceID()
	if err != nil {
		t.Fatalf("defaultWindowsDeviceID: %v", err)
	}

	got, err := resolveWindowsDeviceID(noEnv, Settings{DeviceID: "   "})
	if err != nil {
		t.Fatalf("resolveWindowsDeviceID: %v", err)
	}
	if got != want {
		t.Fatalf("resolveWindowsDeviceID with whitespace-only settings deviceId = %q, want hostname-derived fallback %q", got, want)
	}
}

// TestResolveWindowsDeviceID_FilePrecedence covers the non-degenerate cases
// (env wins, file used when env unset) through the same real call path.
func TestResolveWindowsDeviceID_FilePrecedence(t *testing.T) {
	got, err := resolveWindowsDeviceID(noEnv, Settings{DeviceID: "d-file"})
	if err != nil {
		t.Fatalf("resolveWindowsDeviceID: %v", err)
	}
	if got != "d-file" {
		t.Fatalf("resolveWindowsDeviceID = %q, want file value %q", got, "d-file")
	}

	getenv := envMap(map[string]string{"NOTIFY_DEVICE_ID": "d-env"})
	got, err = resolveWindowsDeviceID(getenv, Settings{DeviceID: "d-file"})
	if err != nil {
		t.Fatalf("resolveWindowsDeviceID: %v", err)
	}
	if got != "d-env" {
		t.Fatalf("resolveWindowsDeviceID = %q, want env value %q to win", got, "d-env")
	}
}

func TestDefaultWindowsDeviceID(t *testing.T) {
	got, err := defaultWindowsDeviceID()
	if err != nil {
		t.Fatalf("defaultWindowsDeviceID: %v", err)
	}
	if got == "" {
		t.Fatal("defaultWindowsDeviceID returned an empty string")
	}
	if got[:2] != "d-" {
		t.Fatalf("defaultWindowsDeviceID = %q, want d- prefix", got)
	}
}
