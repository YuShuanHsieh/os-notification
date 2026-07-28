package main

import "testing"

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
		{"dot rejected", "j.doe", "", true},
		{"asterisk rejected", "j*doe", "", true},
		{"greater-than rejected", "j>doe", "", true},
		{"backslash-only username segment with dot rejected", `CONTOSO\j.doe`, "", true},
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
