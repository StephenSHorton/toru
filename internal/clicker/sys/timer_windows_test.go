//go:build windows

package sys

import (
	"testing"
	"time"
)

func TestQPCToNs(t *testing.T) {
	const f = 10_000_000
	if got := qpcToNs(f*3+5, f); got != 3_000_000_500 {
		t.Errorf("qpcToNs = %d", got)
	}
	// A year of uptime at 10 MHz must not overflow.
	year := int64(f) * 365 * 24 * 3600
	if got := qpcToNs(year, f); got != year*100 {
		t.Errorf("qpcToNs(year) = %d, want %d", got, year*100)
	}
}

// TestClockInterrupt checks the interrupt semantics on the real clock; it
// waits on a timer but injects nothing.
func TestClockInterrupt(t *testing.T) {
	clk, closeFn, err := NewClock()
	if err != nil {
		t.Skipf("no high-resolution timer: %v", err)
	}
	defer closeFn()
	go func() {
		time.Sleep(30 * time.Millisecond)
		clk.Interrupt()
	}()
	start := clk.Now()
	if clk.WaitUntil(start + int64(5*time.Second)) {
		t.Fatal("WaitUntil returned true; want false on Interrupt")
	}
	if waited := time.Duration(clk.Now() - start); waited > time.Second {
		t.Fatalf("interrupt took %v", waited)
	}
	if clk.WaitUntil(clk.Now() + int64(time.Second)) {
		t.Fatal("WaitUntil after Interrupt should return false at once")
	}
	clk.ClearInterrupt()
	d := clk.Now() + int64(5*time.Millisecond)
	if !clk.WaitUntil(d) || clk.Now() < d {
		t.Fatal("WaitUntil after ClearInterrupt should reach the deadline")
	}
}

func TestFallbackClock(t *testing.T) {
	clk, closeFn := NewFallbackClock()
	defer closeFn()
	d := clk.Now() + int64(3*time.Millisecond)
	if !clk.WaitUntil(d) || clk.Now() < d {
		t.Fatal("fallback WaitUntil did not reach the deadline")
	}
	g := newGoClock()
	g.Interrupt()
	if g.WaitUntil(g.Now() + int64(time.Second)) {
		t.Fatal("goClock ignored Interrupt")
	}
	g.ClearInterrupt()
	if !g.WaitUntil(g.Now() + int64(time.Millisecond)) {
		t.Fatal("goClock did not wait after ClearInterrupt")
	}
}
