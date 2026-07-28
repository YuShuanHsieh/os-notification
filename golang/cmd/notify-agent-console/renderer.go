// Package main implements notify-agent-console, the Linux/dev entry point for
// the notification agent: a stand-in for the Windows toast renderer that
// prints "toasts" to stdout instead of showing OS notifications. It mirrors
// src/NotificationAgent.ConsoleHost (C#) and rust/notify-agent-console
// (Rust) exactly, including the console output format.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/YuShuanHsieh/os-notification/golang/internal/toast"
)

// ConsoleRenderer is a dev stand-in for the Windows renderer: it prints
// "toasts" to stdout (format mirrors the C# ConsoleToastRenderer and the
// Rust ConsoleToastRenderer). It intentionally applies no HTTPS URL policy —
// it is a visibility aid only and prints whatever URL it is given verbatim
// (context/configuration-and-runtime.md).
type ConsoleRenderer struct{}

// Show implements toast.Renderer.
func (ConsoleRenderer) Show(ctx context.Context, req toast.ToastRequest) (time.Time, error) {
	fmt.Printf("[TOAST] %s\n", req.Title)
	fmt.Printf("        %s\n", req.Message)
	if req.Attribution != "" {
		fmt.Printf("        — %s\n", req.Attribution)
	}
	if req.Image != nil {
		fmt.Printf("        [image] %s (%s)\n", req.Image.URL, req.Image.Shape.String())
	}
	if req.ActionLabel != "" && req.ActionURL != "" {
		fmt.Printf("        [%s] -> %s\n", req.ActionLabel, req.ActionURL)
	}
	return time.Now(), nil
}
