package vid

import "testing"

// The byte budget must spread across the REAL duration: a 10s clip gets a fat
// bitrate, a 5-minute clip gets a thin one — and the result must always
// multiply back out to ≤ the 9MB target.
func TestDiscordBitrateBudget(t *testing.T) {
	cases := []struct {
		durMs int
		want  int64 // bps
	}{
		{10_000, 7_200_000},  // 10s  → 7.2 Mbps
		{60_000, 1_200_000},  // 1m   → 1.2 Mbps
		{300_000, 240_000},   // 5m   → 240 kbps (downscale path kicks in)
		{0, minDiscordBps},   // degenerate duration → floor, never divide by zero
		{-50, minDiscordBps}, // negative guard
	}
	for _, c := range cases {
		got := discordBitrateBps(c.durMs)
		if got != c.want {
			t.Errorf("discordBitrateBps(%d) = %d, want %d", c.durMs, got, c.want)
		}
		if c.durMs > 0 {
			if bytes := got / 8 * int64(c.durMs) / 1000; bytes > discordTargetBytes {
				t.Errorf("budget overshoot: %d bps over %dms = %d bytes > target", got, c.durMs, bytes)
			}
		}
	}
}
