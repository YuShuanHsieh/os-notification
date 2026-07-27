package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/YuShuanHsieh/os-notification/golang/internal/model"
	"github.com/YuShuanHsieh/os-notification/golang/internal/toast"
)

// captureStdout redirects os.Stdout to a pipe for the duration of fn and
// returns everything written to it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	os.Stdout = orig

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return buf.String()
}

func TestConsoleRenderer_PlainTitleAndMessage(t *testing.T) {
	req := toast.ToastRequest{Title: "Invoice ready", Message: "Invoice INV-8492 is ready for review."}

	out := captureStdout(t, func() {
		if _, err := (ConsoleRenderer{}).Show(context.Background(), req); err != nil {
			t.Fatalf("Show: %v", err)
		}
	})

	want := "[TOAST] Invoice ready\n        Invoice INV-8492 is ready for review.\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestConsoleRenderer_WithAttribution(t *testing.T) {
	req := toast.ToastRequest{Title: "Title", Message: "Message", Attribution: "From Alice"}

	out := captureStdout(t, func() {
		_, _ = (ConsoleRenderer{}).Show(context.Background(), req)
	})

	want := "[TOAST] Title\n        Message\n        — From Alice\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestConsoleRenderer_WithImage(t *testing.T) {
	req := toast.ToastRequest{
		Title:   "Title",
		Message: "Message",
		Image:   &model.Image{URL: "https://example.com/avatar.png", Shape: model.ImageShapeCircle},
	}

	out := captureStdout(t, func() {
		_, _ = (ConsoleRenderer{}).Show(context.Background(), req)
	})

	want := "[TOAST] Title\n        Message\n        [image] https://example.com/avatar.png (circle)\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestConsoleRenderer_WithSquareImage(t *testing.T) {
	req := toast.ToastRequest{
		Title:   "Title",
		Message: "Message",
		Image:   &model.Image{URL: "https://example.com/avatar.png", Shape: model.ImageShapeSquare},
	}

	out := captureStdout(t, func() {
		_, _ = (ConsoleRenderer{}).Show(context.Background(), req)
	})

	want := "[TOAST] Title\n        Message\n        [image] https://example.com/avatar.png (square)\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestConsoleRenderer_WithLabelAndURL(t *testing.T) {
	req := toast.ToastRequest{
		Title:       "Title",
		Message:     "Message",
		ActionLabel: "Open",
		ActionURL:   "https://example.com/open",
	}

	out := captureStdout(t, func() {
		_, _ = (ConsoleRenderer{}).Show(context.Background(), req)
	})

	want := "[TOAST] Title\n        Message\n        [Open] -> https://example.com/open\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestConsoleRenderer_LabelOnly_NoActionLine(t *testing.T) {
	req := toast.ToastRequest{Title: "Title", Message: "Message", ActionLabel: "Open"}

	out := captureStdout(t, func() {
		_, _ = (ConsoleRenderer{}).Show(context.Background(), req)
	})

	want := "[TOAST] Title\n        Message\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestConsoleRenderer_URLOnly_NoActionLine(t *testing.T) {
	req := toast.ToastRequest{Title: "Title", Message: "Message", ActionURL: "https://example.com/open"}

	out := captureStdout(t, func() {
		_, _ = (ConsoleRenderer{}).Show(context.Background(), req)
	})

	want := "[TOAST] Title\n        Message\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestConsoleRenderer_ReturnsRecentTimestamp(t *testing.T) {
	req := toast.ToastRequest{Title: "Title", Message: "Message"}

	before := time.Now()
	var ts time.Time
	var err error
	_ = captureStdout(t, func() {
		ts, err = (ConsoleRenderer{}).Show(context.Background(), req)
	})
	after := time.Now()

	if err != nil {
		t.Fatalf("Show returned error: %v", err)
	}
	if ts.Before(before) || ts.After(after) {
		t.Errorf("Show returned timestamp %v outside [%v, %v]", ts, before, after)
	}
}
