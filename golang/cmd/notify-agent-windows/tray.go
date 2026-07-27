//go:build windows

package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/getlantern/systray"

	"github.com/YuShuanHsieh/os-notification/golang/internal/host"
	"github.com/YuShuanHsieh/os-notification/golang/internal/identity"
	"github.com/YuShuanHsieh/os-notification/golang/internal/natsauth"
)

// version is the running agent's version string, shown (disabled/
// non-clickable) in the tray context menu as "Version 0.1.0" — matching the
// C# TrayApplicationContext's "Version {VersionInfo.Current}" and the Rust
// tray's "Version {CARGO_PKG_VERSION}", both currently "0.1.0".
const version = "0.1.0"

const baseTooltip = "Desktop Notification Agent"

// closeTimeout bounds how long the Close handler waits for a graceful
// AgentHost.Shutdown before force-exiting the process regardless — the same
// 5-second bound used by tray.rs's CLOSE_TIMEOUT and
// TrayApplicationContext.CloseTimeout.
const closeTimeout = 5 * time.Second

//go:embed assets/app.ico
var appIcon []byte

// trayApp owns the tray icon/menu and the AgentHost's lifecycle.
//
// Lifecycle: systray.Run(onReady, onExit) synchronously registers the
// window and adds the tray icon (NIM_ADD, with a default placeholder icon)
// before onReady ever runs, and onReady itself always runs on its own
// goroutine, separate from the native message-pump thread. So the icon is
// visible immediately at launch, and startAgent's NATS/host.Start work
// (which may block or fail) never blocks the tray icon or its message pump
// — matching the C# TrayApplicationContext / Rust tray.rs design of
// "icon appears without waiting on NATS connect".
type trayApp struct {
	h atomic.Pointer[host.Host]
}

func (a *trayApp) onReady() {
	systray.SetIcon(appIcon)
	systray.SetTooltip(baseTooltip)

	versionItem := systray.AddMenuItem(fmt.Sprintf("Version %s", version), "")
	versionItem.Disable()
	systray.AddSeparator()
	closeItem := systray.AddMenuItem("Close", "Shut down the agent and exit")

	go a.watchClose(closeItem)
	go a.startAgent()
}

// onExit is systray's exit callback, invoked from systray.Quit() (called by
// watchClose). There is nothing left to do here: watchClose already owns
// the bounded graceful shutdown and the process's forced exit.
func (a *trayApp) onExit() {}

// startAgent starts the AgentHost. On failure, the tray icon and its Close
// item remain present and usable — Close still works with no host to shut
// down (mirrors the C# design's failure path) — but the tooltip is updated
// to flag the failure, per the "(agent failed to start)" requirement.
func (a *trayApp) startAgent() {
	opts := host.OptionsFromEnv()

	var authProvider natsauth.Provider
	if credsPath := os.Getenv("NOTIFY_NATS_CREDS_FILE"); strings.TrimSpace(credsPath) != "" {
		authProvider = natsauth.CredsFileAuth{Path: credsPath}
	}

	renderer := NewWindowsRenderer(defaultImageCacheDir())

	// identity.EnvIdentity is the only identity provider built so far in
	// this Go port (no AAD/MSAL/device-code sign-in) — a known, accepted
	// gap versus the C#/Rust Windows heads, not something this task adds.
	h, err := host.Start(context.Background(), opts, identity.EnvIdentity{}, renderer, authProvider)
	if err != nil {
		systray.SetTooltip(fmt.Sprintf("%s (agent failed to start)", baseTooltip))
		return
	}
	a.h.Store(h)
}

// watchClose waits for the Close menu item to be clicked, then hides the
// icon, attempts a bounded graceful AgentHost.Shutdown, and force-exits the
// process once that finishes or closeTimeout elapses, whichever is first —
// mirroring tray.rs's WM_COMMAND/ID_MENU_CLOSE handler and
// TrayApplicationContext.OnCloseClickedAsync.
func (a *trayApp) watchClose(closeItem *systray.MenuItem) {
	<-closeItem.ClickedCh
	systray.Quit() // hides the icon and tears down the message loop

	done := make(chan struct{})
	go func() {
		defer close(done)
		if h := a.h.Load(); h != nil {
			ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
			defer cancel()
			_ = h.Shutdown(ctx)
		}
	}()

	select {
	case <-done:
	case <-time.After(closeTimeout):
	}
	os.Exit(0)
}
