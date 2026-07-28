package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/YuShuanHsieh/os-notification/golang/internal/host"
)

func TestUserIDFromWindowsUsername(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{"plain username", "jdoe", "u_jdoe", false},
		{"mixed case lowercased", "JDoe", "u_jdoe", false},
		{"domain-qualified strips domain", `CONTOSO\jdoe`, "u_jdoe", false},
		{"domain-qualified mixed case", `Contoso\JDoe`, "u_jdoe", false},
		{"surrounding whitespace trimmed", "  jdoe  ", "u_jdoe", false},
		{"blank after stripping domain prefix errors", `CONTOSO\`, "", true},
		{"empty raw errors", "", "", true},
		{"whitespace only errors", "   ", "", true},
		{"space sanitized (common Windows account name shape)", "John Doe", "u_john_doe", false},
		{"dot sanitized", "john.doe", "u_john_doe", false},
		{"asterisk sanitized", "user*name", "u_user_name", false},
		{"greater-than sanitized", "user>name", "u_user_name", false},
		{"domain-qualified username with dot sanitized", `CONTOSO\j.doe`, "u_j_doe", false},
		{"all-unusable characters after sanitization errors", "***", "", true},
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
			if got != tt.want {
				t.Fatalf("userIDFromWindowsUsername(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
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
	if want := "u_john_doe"; userID != want {
		t.Fatalf("userIDFromWindowsUsername(%q) = %q, want %q", "John Doe", userID, want)
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
