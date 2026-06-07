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
		{10_000, 7_068_000},  // 10s  → 7.2 Mbps minus the 132k audio share
		{60_000, 1_068_000},  // 1m   → 1.2 Mbps minus audio
		{300_000, 108_000},   // 5m   → thin; downscale path kicks in
		{900_000, 100_000},   // 15m  → video floor holds
		{0, minDiscordBps},   // degenerate duration → floor, never divide by zero
		{-50, minDiscordBps}, // negative guard
	}
	for _, c := range cases {
		got := discordBitrateBps(c.durMs)
		if got != c.want {
			t.Errorf("discordBitrateBps(%d) = %d, want %d", c.durMs, got, c.want)
		}
		// video + audio together must stay within the byte target (the floor
		// cases intentionally trade this for watchability on absurd lengths).
		if c.durMs > 0 && got > 100_000 {
			if bytes := (got + discordAudioBps) / 8 * int64(c.durMs) / 1000; bytes > discordTargetBytes {
				t.Errorf("budget overshoot: %d+%d bps over %dms = %d bytes > target", got, discordAudioBps, c.durMs, bytes)
			}
		}
	}
}
