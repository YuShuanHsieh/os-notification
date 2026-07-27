//go:build windows

package main

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/YuShuanHsieh/os-notification/golang/internal/imagecache"
	"github.com/YuShuanHsieh/os-notification/golang/internal/toast"
	"github.com/YuShuanHsieh/os-notification/golang/internal/windowstoast"
)

// powershellTimeout bounds a single toast-submission PowerShell invocation.
// The WinRT toast APIs it calls are local and fast; this guards against a
// hung/misbehaving powershell.exe blocking the render path indefinitely.
const powershellTimeout = 10 * time.Second

// WindowsRenderer submits toast XML to the OS by shelling out to
// powershell.exe, which loads the WinRT ToastNotificationManager type via
// PowerShell's WindowsRuntime ContentType accelerator and calls .Show() —
// the same mechanism the BurntToast PowerShell module uses. Go has no
// actively-maintained pure-Go WinRT projection equivalent to the Rust
// `windows` crate, so this is the pragmatic native-toast path for an
// unpackaged Go app (documented architecture choice, not a shortcut).
type WindowsRenderer struct {
	cache *imagecache.Cache
}

// NewWindowsRenderer returns a WindowsRenderer whose image cache is rooted
// at cacheDir (the caller resolves this to
// %LOCALAPPDATA%\DesktopNotificationAgent\image-cache in production).
func NewWindowsRenderer(cacheDir string) *WindowsRenderer {
	return &WindowsRenderer{cache: imagecache.New(cacheDir)}
}

// Show implements toast.Renderer.
func (r *WindowsRenderer) Show(ctx context.Context, req toast.ToastRequest) (time.Time, error) {
	imagePath := ""
	if req.Image != nil {
		// Best-effort: any image-cache failure (bad scheme, oversize,
		// timeout, non-image content type, ...) simply renders imageless
		// rather than failing the toast.
		if path, ok := r.cache.Fetch(ctx, req.Image.URL); ok {
			imagePath = path
		}
	}

	xml := windowstoast.BuildToastXML(req, imagePath)

	if err := showToastViaPowerShell(ctx, xml); err != nil {
		return time.Time{}, fmt.Errorf("windows renderer: %w", err)
	}
	return time.Now(), nil
}

// showToastViaPowerShell submits xml by running a short PowerShell script
// through -EncodedCommand (base64 UTF-16LE). Using -EncodedCommand instead
// of interpolating xml into a -Command string sidesteps PowerShell argv/
// quoting escaping entirely — the toast XML (including any text the network
// producer supplied) never has to be shell-escaped, only embedded as a
// single-quoted PowerShell string literal inside the encoded script, which
// only requires doubling literal single quotes (defense in depth: the XML
// builder already XML-escapes all variable content, including replacing '
// with &apos;, so no unescaped ' should ever reach here).
func showToastViaPowerShell(ctx context.Context, xml string) error {
	runCtx, cancel := context.WithTimeout(ctx, powershellTimeout)
	defer cancel()

	script := buildPowerShellScript(xml)
	encoded := encodeCommand(script)

	cmd := exec.CommandContext(runCtx, "powershell.exe",
		"-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden",
		"-EncodedCommand", encoded)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("powershell toast submission failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// buildPowerShellScript builds the script that loads the WinRT toast types,
// parses xml into an XmlDocument, and shows it under this agent's aumid —
// the same identity registered in the registry and set via
// SetCurrentProcessExplicitAppUserModelID (aumid.go), so the toast displays
// with this agent's name/icon instead of "Windows PowerShell".
func buildPowerShellScript(xml string) string {
	escapedXML := strings.ReplaceAll(xml, "'", "''")
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom, ContentType = WindowsRuntime] > $null
$xmlDoc = New-Object Windows.Data.Xml.Dom.XmlDocument
$xmlDoc.LoadXml('%s')
$toast = New-Object Windows.UI.Notifications.ToastNotification $xmlDoc
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('%s').Show($toast)
`, escapedXML, aumid)
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

// defaultImageCacheDir resolves the production image cache directory,
// %LOCALAPPDATA%\DesktopNotificationAgent\image-cache, falling back to the
// OS temp directory if LOCALAPPDATA is somehow unset.
func defaultImageCacheDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "DesktopNotificationAgent", "image-cache")
}
