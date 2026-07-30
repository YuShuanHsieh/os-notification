//go:build windows

package main

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/getlantern/systray"

	"github.com/YuShuanHsieh/os-notification/golang/internal/host"
	"github.com/YuShuanHsieh/os-notification/golang/internal/metrics"
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

	// settings is the Feature-2 settings-file value loaded once at process
	// startup (main.go), before systray.Run -- it never changes for the
	// life of the process, so no synchronization is needed to read it from
	// startAgent's goroutine.
	settings Settings

	// metrics is the AgentMetrics constructed once at process startup
	// (main.go's InitMetrics call), before systray.Run -- like settings, it
	// never changes for the life of the process, so no synchronization is
	// needed to read it from startAgent's goroutine. Nil-safe: a zero-value
	// trayApp (as tests might construct) has a nil metrics field, but
	// host.Start treats a nil AgentMetrics the same as
	// metrics.NullAgentMetrics{}.
	metrics metrics.AgentMetrics
}

func (a *trayApp) onReady() {
	systray.SetIcon(appIcon)
	systray.SetTooltip(baseTooltip)
	slog.Info("tray icon shown", "version", version)

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
	opts := ResolveHostOptions(os.Getenv, host.OptionsFromEnv(), a.settings)

	var authProvider natsauth.Provider
	if credsPath := ResolveCredsFile(os.Getenv, a.settings); strings.TrimSpace(credsPath) != "" {
		authProvider = natsauth.CredsFileAuth{Path: credsPath}
	}

	renderer := NewWindowsRenderer(defaultImageCacheDir())

	// WindowsUsernameIdentity (identity_windows.go) derives identity from
	// the current Windows account name via GetUserNameW -- a deliberate,
	// documented exception to "the OS account name is never used as
	// identity" scoped to the Windows heads (see identity.go's package
	// doc). This Go port has no AAD/MSAL/device-code sign-in at all, so it
	// is used unconditionally rather than only as an AAD fallback.
	idp := WindowsUsernameIdentity{Getenv: os.Getenv, Settings: a.settings}
	h, err := host.Start(context.Background(), opts, idp, renderer, authProvider, a.metrics)
	if err != nil {
		// slog.Error alone is sufficient here: main.go installs a text
		// handler that writes to os.Stderr, so a separate raw
		// fmt.Fprintf(os.Stderr, ...) would just double-report this same
		// failure, once structured and once raw.
		slog.Error("agent failed to start", "error", err)
		systray.SetTooltip(fmt.Sprintf("%s (agent failed to start)", baseTooltip))
		return
	}
	a.h.Store(h)
}

// watchClose waits for the Close menu item to be clicked, then attempts a
// bounded graceful AgentHost.Shutdown (up to closeTimeout), then hides the
// icon and force-exits the process — mirroring tray.rs's
// WM_COMMAND/ID_MENU_CLOSE handler and TrayApplicationContext.OnCloseClickedAsync.
func (a *trayApp) watchClose(closeItem *systray.MenuItem) {
	<-closeItem.ClickedCh
	slog.Info("close clicked, shutting down")

	// Run the bounded graceful shutdown to completion (or timeout) BEFORE
	// calling systray.Quit(). systray.Quit() causes the blocking
	// systray.Run() call in main()'s run() to return, which immediately
	// hits os.Exit(0) there — if Quit() fired first, that race could exit
	// the process before Shutdown ever got a chance to run, defeating the
	// whole point of a graceful, bounded Close.
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

	systray.Quit() // hides the icon and tears down the message loop
	os.Exit(0)
}
