//go:build !windows

// Package main implements notify-agent-windows, the Windows tray/toast entry
// point for the notification agent. This file is the non-Windows stub: it
// lets `go build ./...` succeed on any dev machine without pulling in any
// Windows-only package (systray, golang.org/x/sys/windows, PowerShell
// invocation). The real implementation lives in the //go:build windows
// files in this package.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "notify-agent-windows only runs on Windows.")
	os.Exit(1)
}
