package httpsurl

import (
	"strings"
	"testing"
)

func TestValid_AcceptsPlainHttpsUrlWithHost(t *testing.T) {
	if !Valid("https://example.com") {
		t.Error("expected https://example.com to be valid")
	}
	if !Valid("https://example.com/path?one=1&two=2") {
		t.Error("expected https URL with path and query to be valid")
	}
	if !Valid("https://localhost:8443/path") {
		t.Error("expected https URL with port to be valid")
	}
}

func TestValid_RejectsHttpScheme(t *testing.T) {
	if Valid("http://example.com") {
		t.Error("expected http:// URL to be rejected")
	}
}

func TestValid_RejectsEmbeddedUserInfo(t *testing.T) {
	if Valid("https://user:password@example.com") {
		t.Error("expected URL with user:pass@host to be rejected")
	}
	if Valid("https://user@example.com") {
		t.Error("expected URL with username-only userinfo to be rejected")
	}
}

func TestValid_RejectsRelativeUrl(t *testing.T) {
	if Valid("foo/bar") {
		t.Error("expected relative URL to be rejected")
	}
	if Valid("/path/only") {
		t.Error("expected path-only URL to be rejected")
	}
}

func TestValid_RejectsOversizedUrl(t *testing.T) {
	oversized := "https://example.com/" + strings.Repeat("a", MaxURLLength)
	if Valid(oversized) {
		t.Error("expected URL longer than MaxURLLength to be rejected")
	}
}

// TestValid_MeasuresLengthInRunesNotBytes proves the fix for the
// bytes-vs-characters length bug: a URL containing multi-byte runes must be
// judged by its *character* count against MaxURLLength, not its byte count
// -- otherwise a URL well within the 2048-character contract limit could be
// rejected purely because non-ASCII characters take more than one byte each
// in UTF-8.
func TestValid_MeasuresLengthInRunesNotBytes(t *testing.T) {
	const prefix = "https://example.com/"
	// 'é' encodes as 2 bytes in UTF-8, so this URL's byte length is well over
	// 2048 even though its rune (character) count lands exactly at the
	// MaxURLLength boundary.
	runeCount := MaxURLLength - len([]rune(prefix))
	atBoundary := prefix + strings.Repeat("é", runeCount)
	if got := len([]rune(atBoundary)); got != MaxURLLength {
		t.Fatalf("test setup: rune count = %d, want exactly %d", got, MaxURLLength)
	}
	if got := len(atBoundary); got <= MaxURLLength {
		t.Fatalf("test setup: expected byte length (%d) to exceed MaxURLLength (%d) for this to be a meaningful test", got, MaxURLLength)
	}
	if !Valid(atBoundary) {
		t.Errorf("expected a URL with exactly %d runes to be valid despite its larger byte length", MaxURLLength)
	}

	overBoundary := prefix + strings.Repeat("é", runeCount+1)
	if Valid(overBoundary) {
		t.Errorf("expected a URL with %d runes (over MaxURLLength) to be rejected", MaxURLLength+1)
	}
}

func TestValid_RejectsEmptyString(t *testing.T) {
	if Valid("") {
		t.Error("expected empty string to be rejected")
	}
}

func TestValid_RejectsUrlWithNoHost(t *testing.T) {
	if Valid("https:///path") {
		t.Error("expected https:///path (no host) to be rejected")
	}
	if Valid("https://") {
		t.Error("expected https:// (no host) to be rejected")
	}
}

func TestValid_RejectsMalformedOrUnsafeSchemes(t *testing.T) {
	cases := []string{
		"not-a-url",
		"file:///C:/Windows/System32/cmd.exe",
		"javascript:alert(1)",
	}
	for _, c := range cases {
		if Valid(c) {
			t.Errorf("expected %q to be rejected", c)
		}
	}
}
