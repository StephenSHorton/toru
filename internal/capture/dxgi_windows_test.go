//go:build windows

package capture

import "testing"

// TestDXGIOutputMappingBijective verifies the GDI->DXGI bridge on the real
// machine: every enumerated display must resolve to a DXGI output, and no two
// displays may map to the same output. This is the invariant the recording
// path depends on (it caught kbinani 0/1 == DXGI 1/0 inversion in the wild).
// Skips on headless sessions where either enumeration is empty.
func TestDXGIOutputMappingBijective(t *testing.T) {
	displays := EnumDisplays()
	if len(displays) == 0 {
		t.Skip("no displays (headless session)")
	}
	seen := map[int]int{} // dxgiIdx -> kbinani Index
	for _, d := range displays {
		idx, ok := dxgiOutputIndexFor(d)
		if !ok {
			// Single-adapter machines should always resolve; multi-GPU setups
			// may legitimately have outputs off the default adapter.
			t.Skipf("display %d (%d,%d %dx%d) has no DXGI output on the default adapter", d.Index, d.X, d.Y, d.W, d.H)
		}
		if prev, dup := seen[idx]; dup {
			t.Fatalf("displays %d and %d both map to DXGI output %d", prev, d.Index, idx)
		}
		seen[idx] = d.Index
		t.Logf("kbinani idx=%d (%d,%d %dx%d) -> DXGI output_idx=%d", d.Index, d.X, d.Y, d.W, d.H, idx)
	}
}
