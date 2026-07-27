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
