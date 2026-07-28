//go:build windows

// Package main implements notify-agent-windows: the Windows entry point for
// the notification agent. It is a tray application (not a headless
// process), matching rust/notify-agent-windows's design rather than the
// original C# WinForms head's process model in name only — the tray
// behavior (immediate icon, version/Close menu, startup-failure tooltip,
// bounded-graceful-then-forced Close) is a faithful port either way.
//
// Toast rendering has no mature pure-Go WinRT projection to build on (unlike
// Rust's `windows` crate), so this head builds the same toast XML the OS
// expects (internal/windowstoast) and submits it by shelling out to
// powershell.exe, which loads the WinRT ToastNotificationManager type via
// PowerShell's WindowsRuntime ContentType accelerator — the same mechanism
// the BurntToast PowerShell module uses for unpackaged-app toast
// notifications. See renderer.go.
package main

import (
	"fmt"
	"os"

	"github.com/getlantern/systray"
	"golang.org/x/sys/windows"
)

// mutexName scopes one running agent process per interactive logon session
// ("Local\" mutexes are session-scoped), matching the C#/Rust Windows
// heads' single-instance behavior. Deliberately distinct from their mutex
// names (Local\DesktopNotificationAgent in C#, Local\NotifyAgentRust in
// Rust) so all three heads can run side by side during development.
const mutexName = `Local\NotifyAgentGolang`

func main() {
	os.Exit(run())
}

func run() int {
	alreadyRunning, err := acquireSingleInstance()
	if err != nil {
		// Fail closed: an inconclusive single-instance check must not be
		// treated as "not already running." Silently falling through here
		// would defeat the entire purpose of the mutex guard, unlike the
		// AUMID path below (which is explicitly cosmetic-only on failure).
		fmt.Fprintf(os.Stderr, "notify-agent-windows: single-instance check failed: %v\n", err)
		return 1
	}
	if alreadyRunning {
		return 0
	}

	// Best-effort: even if AUMID registration fails, still start the tray —
	// toasts would just fall back to showing "Windows PowerShell" as the
	// sender rather than this agent's identity, which is a cosmetic
	// degradation, not a reason to refuse to run.
	if err := registerAUMID(); err != nil {
		fmt.Fprintf(os.Stderr, "notify-agent-windows: warning: %v\n", err)
	}
	if err := setCurrentProcessAUMID(aumid); err != nil {
		fmt.Fprintf(os.Stderr, "notify-agent-windows: warning: %v\n", err)
	}

	app := &trayApp{}
	systray.Run(app.onReady, app.onExit)
	return 0
}

// acquireSingleInstance reports whether an instance of this agent is
// already running in the current interactive session, in which case the
// caller should exit immediately and leave the existing instance's tray
// icon in place.
func acquireSingleInstance() (alreadyRunning bool, err error) {
	namePtr, err := windows.UTF16PtrFromString(mutexName)
	if err != nil {
		return false, err
	}
	_, err = windows.CreateMutex(nil, true, namePtr)
	if err == windows.ERROR_ALREADY_EXISTS {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}
