//go:build windows

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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

// showToastViaPowerShell submits xml by running a short, fixed-size
// PowerShell script through -EncodedCommand (base64 UTF-16LE), passing xml
// itself to the subprocess over stdin rather than embedding it in the
// script text.
//
// This split matters because -EncodedCommand's payload is still subject to
// Windows' CreateProcess command-line length ceiling (~32,767 UTF-16
// characters for lpCommandLine); base64-encoding UTF-16LE text expands it
// ~2.67x. The old approach interpolated xml into the script before
// encoding it, so a large producer-controlled value (e.g. content.
// secondaryText / Attribution, embedded verbatim and un-truncated, which
// this product's payload budget allows to reach a few KB) could push the
// encoded command past that ceiling — well within the documented 32 KiB
// total payload limit — causing powershell.exe to silently fail to launch
// or truncate the command, with no submitted_to_windows ack ever sent.
// Piping xml via stdin instead means the script's own -EncodedCommand size
// no longer scales with producer content length at all: buildPowerShellScript
// takes no xml parameter, so its output (and therefore the encoded command)
// is constant-size regardless of how long title/message/attribution/URLs
// are. It also means xml needs no shell-level escaping (only the XML-level
// escaping windowstoast.XMLEscape already does): it never becomes part of a
// quoted string literal, just raw bytes on a pipe.
func showToastViaPowerShell(ctx context.Context, xml string) error {
	runCtx, cancel := context.WithTimeout(ctx, powershellTimeout)
	defer cancel()

	script := buildPowerShellScript(aumid)
	encoded := encodeCommand(script)

	cmd := exec.CommandContext(runCtx, "powershell.exe",
		"-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden",
		"-EncodedCommand", encoded)
	cmd.Stdin = bytes.NewReader(buildStdinPayload(xml))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("powershell toast submission failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
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
