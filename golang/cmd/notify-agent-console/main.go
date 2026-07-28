package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/YuShuanHsieh/os-notification/golang/internal/host"
	"github.com/YuShuanHsieh/os-notification/golang/internal/identity"
	"github.com/YuShuanHsieh/os-notification/golang/internal/loglevel"
	"github.com/YuShuanHsieh/os-notification/golang/internal/natsauth"
)

// shutdownTimeout bounds how long Shutdown is allowed to wait for background
// work to drain before giving up (best-effort, not a durable drain
// guarantee -- see host.Shutdown's doc comment).
const shutdownTimeout = 5 * time.Second

func main() {
	os.Exit(run())
}

func run() int {
	// Set up the default slog logger before anything else, matching
	// notify-agent-windows's convention: NOTIFY_LOG_LEVEL (default "info").
	// This head has no settings file (Windows-only, see Feature 2), so
	// this is env-only, one tier simpler than the Windows head's
	// env-then-file-then-default precedence.
	level := slog.LevelInfo
	if lvl, ok := loglevel.Parse(os.Getenv("NOTIFY_LOG_LEVEL")); ok {
		level = lvl
	}
	levelVar := new(slog.LevelVar)
	levelVar.Set(level)
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: levelVar})))

	opts := host.OptionsFromEnv()

	var authProvider natsauth.Provider
	if credsPath := os.Getenv("NOTIFY_NATS_CREDS_FILE"); strings.TrimSpace(credsPath) != "" {
		authProvider = natsauth.CredsFileAuth{Path: credsPath}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	h, err := host.Start(ctx, opts, identity.EnvIdentity{}, ConsoleRenderer{}, authProvider)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	fmt.Printf("Agent subscribed to %s on %s. Ctrl+C to exit.\n", h.Subject(), opts.NatsURL)

	<-ctx.Done()
	fmt.Println("Shutting down.")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := h.Shutdown(shutdownCtx); err != nil {
		fmt.Fprintf(os.Stderr, "error during shutdown: %v\n", err)
	}

	return 0
}
