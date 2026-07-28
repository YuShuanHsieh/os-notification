package clock

import (
	"testing"
	"time"
)

func TestFakeClockAdvance(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewFakeClock(start)

	if got := c.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %v, want %v", got, start)
	}

	c.Advance(10 * time.Second)

	want := start.Add(10 * time.Second)
	if got := c.Now(); !got.Equal(want) {
		t.Fatalf("Now() after Advance = %v, want %v", got, want)
	}
}

func TestRealClockReturnsCurrentTime(t *testing.T) {
	before := time.Now()
	got := RealClock{}.Now()
	after := time.Now()

	if got.Before(before) || got.After(after) {
		t.Fatalf("RealClock.Now() = %v, want between %v and %v", got, before, after)
	}
}
