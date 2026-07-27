//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

// aumid is this agent's AppUserModelID. It must be identical everywhere it
// is used — the HKCU registration below, SetCurrentProcessExplicitAppUserModelID,
// and the PowerShell ToastNotificationManager::CreateToastNotifier(aumid)
// call in renderer.go — or Windows shows the toast as coming from
// "Windows PowerShell" instead of this agent.
const (
	aumid            = "NotificationAgent.Golang.DesktopAgent"
	aumidDisplayName = "Desktop Notification Agent (Go)"
)

// registerAUMID registers this process's AppUserModelID under
// HKCU\Software\Classes\AppUserModelId\<aumid>, the unpackaged-app
// substitute for a WinAppSDK Register() call. Mirrors
// rust/notify-agent-windows/src/main.rs's register_aumid (via the winreg
// crate) and src/NotificationAgent.Windows's reliance on
// Microsoft.Toolkit.Uwp.Notifications to do the equivalent.
func registerAUMID() error {
	key, _, err := registry.CreateKey(
		registry.CURRENT_USER,
		`Software\Classes\AppUserModelId\`+aumid,
		registry.SET_VALUE,
	)
	if err != nil {
		return fmt.Errorf("registerAUMID: create key: %w", err)
	}
	defer key.Close()

	if err := key.SetStringValue("DisplayName", aumidDisplayName); err != nil {
		return fmt.Errorf("registerAUMID: set DisplayName: %w", err)
	}
	return nil
}

// golang.org/x/sys/windows does not expose
// SetCurrentProcessExplicitAppUserModelID, so it is called directly via
// shell32.dll, same as the raw Win32 approach the Rust head's windows crate
// wraps.
var (
	modshell32                                  = syscall.NewLazyDLL("shell32.dll")
	procSetCurrentProcessExplicitAppUserModelID = modshell32.NewProc("SetCurrentProcessExplicitAppUserModelID")
)

// setCurrentProcessAUMID sets the calling process's explicit AppUserModelID
// so that both the toast notifier and the PowerShell process it launches
// (which inherits no identity of its own — the AUMID passed to
// CreateToastNotifier is what matters there) resolve to the same registered
// identity.
func setCurrentProcessAUMID(id string) error {
	ptr, err := syscall.UTF16PtrFromString(id)
	if err != nil {
		return fmt.Errorf("setCurrentProcessAUMID: %w", err)
	}
	hr, _, _ := procSetCurrentProcessExplicitAppUserModelID.Call(uintptr(unsafe.Pointer(ptr)))
	// SetCurrentProcessExplicitAppUserModelID returns an HRESULT; S_OK is 0.
	if hr != 0 {
		return fmt.Errorf("setCurrentProcessAUMID: HRESULT 0x%x", hr)
	}
	return nil
}
