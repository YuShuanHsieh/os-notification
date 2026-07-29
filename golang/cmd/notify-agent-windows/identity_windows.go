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

// golang.org/x/sys/windows exposes GetUserNameEx (secur32.dll, for
// domain-qualified name formats) but not the plain Win32 GetUserNameW
// (advapi32.dll) this provider wants, so it is called directly via
// NewLazySystemDLL + NewProc -- the same raw-syscall pattern aumid.go
// already uses for shell32.dll's SetCurrentProcessExplicitAppUserModelID.
var (
	modadvapi32     = windows.NewLazySystemDLL("advapi32.dll")
	procGetUserName = modadvapi32.NewProc("GetUserNameW")
)

// getWindowsUserName calls the Win32 GetUserNameW function to retrieve the
// name of the user associated with the calling thread. GetUserNameW's
// calling convention requires two calls: the first, with an undersized
// buffer, fails and reports the required buffer size (in UTF-16 code units,
// including the terminating null) through the in/out size parameter; the
// second, with a correctly sized buffer, succeeds and returns the name.
func getWindowsUserName() (string, error) {
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
// current Windows account name (via GetUserNameW) into this product's
// "u_{...}" identity shape -- see identity.go's package doc for why this is
// a deliberate, documented exception to "the OS account name is never used
// as identity", scoped to the Windows heads.
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
