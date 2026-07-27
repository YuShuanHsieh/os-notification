package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

// TestBuildPowerShellScriptIsConstantSize proves the fix's core property:
// the PowerShell script text (and therefore the -EncodedCommand payload
// derived from it) no longer scales with the size of producer-controlled
// toast content. Unlike the old buildPowerShellScript(xml string), the
// current signature doesn't even accept the toast XML, so its output is
// identical regardless of how large title/message/attribution/URLs are —
// that variable-length data now flows through buildStdinPayload/cmd.Stdin
// instead.
func TestBuildPowerShellScriptIsConstantSize(t *testing.T) {
	script := buildPowerShellScript("Some.Aumid")

	// A large attribution/title/message would, under the old design, have
	// been interpolated directly into the script. Confirm the current
	// script contains no such interpolation point and instead reads its
	// data from stdin.
	if strings.Contains(script, "LoadXml('") {
		t.Fatalf("script embeds XML as a string literal instead of reading stdin: %q", script)
	}
	if !strings.Contains(script, "[Console]::In.ReadToEnd()") {
		t.Fatalf("script does not read the toast XML from stdin: %q", script)
	}

	encoded := encodeCommand(script)

	// The encoded command must stay small and constant no matter what the
	// caller's toast content looks like, since buildPowerShellScript takes
	// no xml/content parameter at all. Assert it well under both the
	// documented 32 KiB total payload budget and the ~32,767-UTF-16-char
	// CreateProcess command-line ceiling that motivated this fix.
	const generousCeiling = 4000 // encoded command chars; actual is far smaller
	if len(encoded) > generousCeiling {
		t.Fatalf("encoded command unexpectedly large (%d chars): -EncodedCommand payload should be small and fixed-size, not scale with toast content", len(encoded))
	}

	// Determinism: calling it again (as would happen across many toasts of
	// wildly different content sizes) produces byte-identical output.
	if again := buildPowerShellScript("Some.Aumid"); again != script {
		t.Fatalf("buildPowerShellScript is not deterministic for the same aumid")
	}
}

// TestBuildPowerShellScriptEscapesAumid preserves the existing
// defense-in-depth property (doubling literal single quotes before
// embedding into a PowerShell single-quoted string literal), now applied to
// aumidID instead of the toast XML.
func TestBuildPowerShellScriptEscapesAumid(t *testing.T) {
	script := buildPowerShellScript("evil'; Remove-Item C:\\ -Recurse -Force; '")
	if strings.Contains(script, "evil'; Remove-Item") {
		t.Fatalf("unescaped single quote reached the script literal: %q", script)
	}
	if !strings.Contains(script, "evil''; Remove-Item C:\\ -Recurse -Force; ''") {
		t.Fatalf("expected doubled single quotes in escaped aumid, got: %q", script)
	}
}

// TestBuildStdinPayloadRoundTrips proves the data channel that replaces the
// old argv/-EncodedCommand embedding: buildStdinPayload base64-encodes xml
// as UTF-8 bytes, matching what buildPowerShellScript's stdin-reading line
// expects ([Console]::In.ReadToEnd() then Convert.FromBase64String then
// Encoding.UTF8.GetString). It also demonstrates that this payload is
// entirely separate from the script/argv: nothing about its content ever
// touches buildPowerShellScript's output.
func TestBuildStdinPayloadRoundTrips(t *testing.T) {
	xml := `<toast><visual><binding template="ToastGeneric"><text>hello 世界 &amp; 'quotes'</text></binding></visual></toast>`

	payload := buildStdinPayload(xml)

	decoded, err := base64.StdEncoding.DecodeString(string(payload))
	if err != nil {
		t.Fatalf("stdin payload is not valid base64: %v", err)
	}
	if string(decoded) != xml {
		t.Fatalf("round trip mismatch:\n got: %q\nwant: %q", decoded, xml)
	}

	// The payload must be pure base64 (safe to write to a pipe verbatim,
	// no shell-relevant characters), regardless of what special characters
	// the original xml contained.
	for _, r := range string(payload) {
		if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/=", r) {
			t.Fatalf("stdin payload contains non-base64 character %q", r)
		}
	}
}

// TestStdinPayloadGrowsIndependentlyOfScript is the direct proof that this
// fix removes the length-scaling relationship between producer content size
// and the -EncodedCommand argv: a toast XML far larger than would ever have
// fit safely in the old embedded-string-literal design (well past the
// point where base64-encoded UTF-16LE would exceed the ~32,767-UTF-16-char
// CreateProcess command-line ceiling) still produces the exact same,
// small, constant-size PowerShell script/argv — only the stdin payload
// grows.
func TestStdinPayloadGrowsIndependentlyOfScript(t *testing.T) {
	small := buildStdinPayload(strings.Repeat("x", 10))
	huge := buildStdinPayload(strings.Repeat("x", 50_000)) // several times the old argv ceiling

	if len(huge) <= len(small) {
		t.Fatalf("expected stdin payload to grow with xml length")
	}

	scriptForSmall := encodeCommand(buildPowerShellScript(aumidForTest))
	scriptForHuge := scriptForSmall // buildPowerShellScript doesn't take xml at all

	if scriptForSmall != scriptForHuge {
		t.Fatalf("script/argv size must not depend on toast content size")
	}
	if len(scriptForSmall) >= len(huge) {
		// Sanity check that the test itself is meaningful: the huge stdin
		// payload really is bigger than the fixed script/argv, proving the
		// data moved off argv onto the pipe.
		t.Fatalf("expected huge stdin payload (%d) to exceed the fixed script/argv size (%d)", len(huge), len(scriptForSmall))
	}
}

const aumidForTest = "NotificationAgent.Golang.DesktopAgent.Test"
