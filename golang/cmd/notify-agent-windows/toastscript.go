// PowerShell script construction for toast submission. This file has no
// `//go:build windows` constraint, unlike renderer.go which actually shells
// out to powershell.exe: keeping the pure string-building logic here (no
// Windows-only imports) lets it compile and unit-test on any platform, while
// only the process-execution wiring lives behind the Windows build tag.
package main

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
	"unicode/utf16"
)

// buildPowerShellScript returns the fixed script used to submit a toast
// under aumidID. Its size does NOT depend on the toast XML: the XML is
// never interpolated into this script text (which is what would grow the
// -EncodedCommand payload past Windows' ~32K-UTF-16-character CreateProcess
// command-line ceiling once producer-controlled content — e.g. a multi-KB
// attribution, still within this product's documented payload budget — is
// included). Instead the script reads the XML from its own stdin
// (base64-encoded, to avoid any console-code-page/encoding ambiguity around
// non-ASCII title/message/attribution text); only this small constant
// string ever goes through -EncodedCommand, while the variable-length data
// goes through a pipe, which has no such ceiling.
//
// aumidID is this binary's own compile-time AppUserModelID constant (see
// aumid.go), not producer-controlled, but it is still defensively embedded
// as a doubled-single-quote PowerShell string literal (matching how the XML
// itself used to be embedded) rather than assumed safe.
func buildPowerShellScript(aumidID string) string {
	escapedAumid := strings.ReplaceAll(aumidID, "'", "''")
	return `$ErrorActionPreference = 'Stop'
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom, ContentType = WindowsRuntime] > $null
$b64 = [Console]::In.ReadToEnd()
$xmlText = [System.Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($b64))
$xmlDoc = New-Object Windows.Data.Xml.Dom.XmlDocument
$xmlDoc.LoadXml($xmlText)
$toast = New-Object Windows.UI.Notifications.ToastNotification $xmlDoc
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('` + escapedAumid + `').Show($toast)
`
}

// encodeCommand base64-encodes script as UTF-16LE, the encoding
// powershell.exe -EncodedCommand requires.
func encodeCommand(script string) string {
	u16 := utf16.Encode([]rune(script))
	buf := make([]byte, len(u16)*2)
	for i, v := range u16 {
		binary.LittleEndian.PutUint16(buf[i*2:], v)
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// buildStdinPayload encodes xml as base64 of its UTF-8 bytes, the form
// buildPowerShellScript's stdin-reading line expects. This is the data
// channel that replaces the old approach of interpolating xml into the
// PowerShell script text: xml's length has no bearing on the size of
// anything passed via argv/-EncodedCommand (see buildPowerShellScript), only
// on how much is written to the subprocess's stdin pipe, which is not
// subject to the CreateProcess command-line length ceiling.
func buildStdinPayload(xml string) []byte {
	return []byte(base64.StdEncoding.EncodeToString([]byte(xml)))
}
