package capture

import (
	"reflect"
	"testing"
)

func TestAppMixRoots(t *testing.T) {
	// explorer(10) -> toru(20) -> webview(21) -> webview audio(22)
	// explorer(10) -> steam(30) -> game(31)
	// explorer(10) -> chrome(40) -> chrome audio(41)
	// gg(50) -> sonar(51)
	// discord(60) -> discord voice(61)
	parent := map[uint32]uint32{
		20: 10, 21: 20, 22: 21,
		30: 10, 31: 30,
		40: 10, 41: 40,
		51: 50,
		61: 60,
	}
	sessions := []uint32{10, 22, 30, 31, 41, 50, 51, 61, 0}
	got := appMixRoots(sessions, parent, []uint32{20, 51})
	// 10 (explorer holds Toru) and 50 (gg holds Sonar) are dropped; 22 is in
	// Toru's tree, 51 is Sonar; 31 is covered by steam's tree.
	want := []uint32{30, 41, 61}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("appMixRoots = %v, want %v", got, want)
	}
}

func TestAppMixRootsParentCycle(t *testing.T) {
	// A stale parent pid can point back into the chain; it must not loop.
	parent := map[uint32]uint32{1: 2, 2: 1, 3: 1}
	if got := appMixRoots([]uint32{3}, parent, []uint32{9}); !reflect.DeepEqual(got, []uint32{3}) {
		t.Fatalf("got %v", got)
	}
}
