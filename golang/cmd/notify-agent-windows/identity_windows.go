//go:build windows

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/YuShuanHsieh/os-notification/golang/internal/identity"
)

// golang.org/x/sys/windows wraps GetUserNameEx (secur32.dll, for
// domain-qualified name formats) directly, so the primary path below needs no
// raw-syscall plumbing of its own -- unlike the plain Win32 GetUserNameW
// (advapi32.dll), which x/sys/windows does not wrap and which is kept here
// only as a fallback (see getWindowsUserNameFallback). windows.NameSamCompatible
// selects the SAM-compatible "DOMAIN\username" format (or
// "MACHINENAME\username" on a machine that isn't domain-joined), so unlike
// GetUserNameW -- which never returns a domain prefix at all -- two
// identically-named accounts in different domains (e.g. CORP\jdoe and
// CONTOSO\jdoe) now resolve to different raw values here, and therefore
// different derived identities once fed through userIDFromWindowsUsername
// (identity.go), which deliberately no longer strips the domain.
var (
	modadvapi32     = windows.NewLazySystemDLL("advapi32.dll")
	procGetUserName = modadvapi32.NewProc("GetUserNameW")
)

// getWindowsUserName retrieves the SAM-compatible, domain-qualified name of
// the user associated with the calling thread via the wrapped GetUserNameEx
// (secur32.dll), falling back to the plain Win32 GetUserNameW (advapi32.dll,
// getWindowsUserNameFallback below) if that fails for any reason. The
// fallback loses any domain qualification GetUserNameEx would have supplied,
// but a bare username is still preferable to turning "domain info
// unavailable" into a hard startup failure this provider didn't have before
// this change.
func getWindowsUserName() (string, error) {
	name, err := getWindowsSamCompatibleName()
	if err == nil {
		return name, nil
	}

	fallbackName, fallbackErr := getWindowsUserNameFallback()
	if fallbackErr != nil {
		return "", fmt.Errorf("GetUserNameEx failed (%v) and GetUserNameW fallback also failed: %w", err, fallbackErr)
	}
	slog.Warn("windows identity: GetUserNameEx failed, falling back to GetUserNameW (any domain qualification is lost)", "error", err)
	return fallbackName, nil
}

// getWindowsSamCompatibleName calls the wrapped GetUserNameEx function
// (secur32.dll) with windows.NameSamCompatible to retrieve the
// "DOMAIN\username" (or "MACHINENAME\username" when not domain-joined) name
// of the user associated with the calling thread. GetUserNameEx's calling
// convention requires two calls: the first, with an undersized buffer,
// fails with ERROR_MORE_DATA (not ERROR_INSUFFICIENT_BUFFER -- that's
// GetUserNameW's error code, GetUserNameEx's is different per its own MSDN
// docs) and reports the required buffer size (in UTF-16 code units,
// including the terminating null) through the in/out size parameter; the
// second, with a correctly sized buffer, succeeds and returns the name.
func getWindowsSamCompatibleName() (string, error) {
	var size uint32 = 1
	buf := make([]uint16, size)

	err := windows.GetUserNameEx(windows.NameSamCompatible, &buf[0], &size)
	if err != nil {
		if err != windows.ERROR_MORE_DATA {
			return "", fmt.Errorf("GetUserNameEx: get required buffer size: %w", err)
		}
	}
	if size <= 1 {
		return "", fmt.Errorf("GetUserNameEx: reported an implausible buffer size %d", size)
	}

	buf = make([]uint16, size)
	if err := windows.GetUserNameEx(windows.NameSamCompatible, &buf[0], &size); err != nil {
		return "", fmt.Errorf("GetUserNameEx: %w", err)
	}

	return windows.UTF16ToString(buf), nil
}

// getWindowsUserNameFallback calls the plain Win32 GetUserNameW function
// directly (advapi32.dll, via NewLazySystemDLL + NewProc -- the same
// raw-syscall pattern aumid.go already uses for shell32.dll's
// SetCurrentProcessExplicitAppUserModelID, since x/sys/windows doesn't wrap
// this one). It never returns a domain prefix, even on a domain-joined
// machine -- documented Win32 behavior, not a bug -- which is exactly why
// getWindowsSamCompatibleName/GetUserNameEx above is the primary path; this
// exists solely as a fallback for the rare case that call fails.
func getWindowsUserNameFallback() (string, error) {
	var size uint32 = 1
	buf := make([]uint16, size)

	r, _, callErr := procGetUserName.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if r == 0 {
		if callErr != windows.ERROR_INSUFFICIENT_BUFFER {
			return "", fmt.Errorf("GetUserNameW: get required buffer size: %w", callErr)
		}
	}
	if size <= 1 {
		return "", fmt.Errorf("GetUserNameW: reported an implausible buffer size %d", size)
	}

	buf = make([]uint16, size)
	r, _, callErr = procGetUserName.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if r == 0 {
		return "", fmt.Errorf("GetUserNameW: %w", callErr)
	}

	return windows.UTF16ToString(buf), nil
}

// WindowsUsernameIdentity implements identity.Provider by resolving the
// current Windows account's SAM-compatible, domain-qualified name (via
// GetUserNameEx, falling back to GetUserNameW) into this product's "u_{...}"
// identity shape -- see identity.go's package doc for why this is a
// deliberate, documented exception to "the OS account name is never used as
// identity", scoped to the Windows heads.
//
// Getenv is injected (production passes os.Getenv) so NOTIFY_DEVICE_ID
// resolution is deterministic in tests; Settings carries the Feature-2
// settings file's deviceId override. Both feed ResolveDeviceID's
// env-then-file precedence (settings.go), falling back to
// defaultWindowsDeviceID (hostname-derived) when neither is set.
type WindowsUsernameIdentity struct {
	Getenv   func(string) string
	Settings Settings
}

// Resolve implements identity.Provider.
func (w WindowsUsernameIdentity) Resolve(ctx context.Context) (identity.Identity, error) {
	getenv := w.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	raw, err := getWindowsUserName()
	if err != nil {
		return identity.Identity{}, fmt.Errorf("windows identity: get username: %w", err)
	}

	userID, err := userIDFromWindowsUsername(raw)
	if err != nil {
		return identity.Identity{}, fmt.Errorf("windows identity: %w", err)
	}

	deviceID, err := resolveWindowsDeviceID(getenv, w.Settings)
	if err != nil {
		return identity.Identity{}, fmt.Errorf("windows identity: %w", err)
	}

	slog.Debug("windows identity resolved", "mode", "windows-username", "deviceId", deviceID)
	return identity.Identity{UserID: userID, DeviceID: deviceID}, nil
}
