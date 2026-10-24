package capture

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeSeg(t *testing.T, dir string, run, idx int, size int, age time.Duration) {
	t.Helper()
	p := filepath.Join(dir, fmt.Sprintf("r%04d-%08d.ts", run, idx))
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	_ = os.Chtimes(p, when, when)
}

func TestPrunePersistentAgeSizeAndProtect(t *testing.T) {
	dir := t.TempDir()
	// run 100: protected (manual recording), very old
	fakeSeg(t, dir, 100, 0, 10, 10*time.Hour)
	writeRunInfo(dir, RunInfo{Run: 100, StartMs: 1, Tag: "manual"})
	// run 200: old background footage
	fakeSeg(t, dir, 200, 0, 10, 3*time.Hour)
	fakeSeg(t, dir, 200, 1, 10, 3*time.Hour)
	writeRunInfo(dir, RunInfo{Run: 200, StartMs: 2})
	// run 300: recent
	for i := 0; i < 5; i++ {
		fakeSeg(t, dir, 300, i, 10, time.Duration(5-i)*time.Minute)
	}
	writeRunInfo(dir, RunInfo{Run: 300, StartMs: 3})

	b := NewReplayBuffer()
	b.run = 300
	b.SetProtect(func(run int) bool { return run == 100 })
	b.prunePersistent(ReplayConfig{Dir: dir, Persist: true, KeepSeconds: 3600})

	segs := ListRecSegments(dir)
	if len(segs) != 6 || segs[0].Run != 100 || segs[1].Run != 300 {
		t.Fatalf("after age prune: %+v", segs)
	}
	if _, ok := ReadRunInfo(dir, 200); ok {
		t.Error("sidecar of a fully pruned run should be removed")
	}
	if _, ok := ReadRunInfo(dir, 100); !ok {
		t.Error("protected run's sidecar must stay")
	}

	// Size cap: 60 bytes on disk, cap 35 -> oldest unprotected go, newest stays.
	b.prunePersistent(ReplayConfig{Dir: dir, Persist: true, MaxBytes: 35})
	segs = ListRecSegments(dir)
	if len(segs) != 3 || segs[0].Run != 100 || segs[2].Index != 4 {
		t.Fatalf("after size prune: %+v", segs)
	}
	if st := b.Status(); st.Error != "" {
		t.Errorf("under the cap, no error expected: %q", st.Error)
	}
	b.prunePersistent(ReplayConfig{Dir: dir, Persist: true, MaxBytes: 5})
	if st := b.Status(); st.Error == "" {
		t.Error("protected footage over the cap should report storage full")
	}
	if n := len(ListRecSegments(dir)); n != 2 {
		t.Errorf("only protected + in-progress segments should remain, got %d", n)
	}
}

func TestSegmentTimesAndDeleteRun(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "r0042.csv"), []byte(`C:\x\r0042-00000000.ts,0.000000,2.000000`+"\nr0042-00000001.ts,2.000000,4.016667\nbad line\n"), 0o644)
	got := SegmentTimes(dir, 42)
	if got["r0042-00000000.ts"] != [2]float64{0, 2} || got["r0042-00000001.ts"][1] != 4.016667 || len(got) != 2 {
		t.Errorf("SegmentTimes = %v", got)
	}
	fakeSeg(t, dir, 42, 0, 1, 0)
	fakeSeg(t, dir, 43, 0, 1, 0)
	writeRunInfo(dir, RunInfo{Run: 42})
	if err := DeleteRun(dir, 42); err != nil {
		t.Fatal(err)
	}
	if segs := ListRecSegments(dir); len(segs) != 1 || segs[0].Run != 43 {
		t.Errorf("DeleteRun left %+v", segs)
	}
	if _, err := os.Stat(filepath.Join(dir, "r0042.csv")); !os.IsNotExist(err) {
		t.Error("csv should be deleted with its run")
	}
}

func TestPrunePersistentKeepsFreeSpaceAndPauses(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 4; i++ {
		fakeSeg(t, dir, 500, i, 100, time.Duration(4-i)*time.Minute)
	}
	fakeSeg(t, dir, 400, 0, 100, time.Hour) // protected
	free := int64(1000)
	diskFreeBytes = func(string) (int64, bool) { return free, true }
	defer func() { diskFreeBytes = diskFreeBytesOS }()

	b := NewReplayBuffer()
	b.run = 500
	b.SetProtect(func(run int) bool { return run == 400 })
	cfg := ReplayConfig{Dir: dir, Persist: true, MinFreeBytes: 1150}

	// 150 bytes short: two unprotected segments go, recording continues.
	b.prunePersistent(cfg)
	if n := len(ListRecSegments(dir)); n != 3 || b.isDiskFull() {
		t.Fatalf("after shortfall prune: %d segments, full=%v", n, b.isDiskFull())
	}

	// Far short with only protected + in-progress left: pause, with a reason.
	free = 0
	b.prunePersistent(cfg)
	if !b.isDiskFull() || !strings.Contains(b.Status().Error, "almost full") {
		t.Fatalf("should pause on a full drive: full=%v err=%q", b.isDiskFull(), b.Status().Error)
	}
	if segs := ListRecSegments(dir); len(segs) != 2 || segs[0].Run != 400 {
		t.Errorf("protected footage must survive: %+v", segs)
	}

	// Space comes back: resume.
	free = 1 << 40
	b.prunePersistent(cfg)
	if b.isDiskFull() || b.Status().Error != "" {
		t.Errorf("should resume when space frees: full=%v err=%q", b.isDiskFull(), b.Status().Error)
	}
}

func TestPruneWholeSessions(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-5 * time.Hour)
	// Three sessions: A (two runs 5s apart = one session), B (kept), C (live).
	mk := func(run int, start time.Time, n int) {
		writeRunInfo(dir, RunInfo{Run: run, StartMs: start.UnixMilli(), Tag: "background|Game"})
		for i := 0; i < n; i++ {
			fakeSeg(t, dir, run, i, 100, time.Since(start.Add(time.Duration(i+1)*2*time.Second)))
		}
	}
	mk(1000, base, 3)                     // A, part 1: ends base+6s
	mk(1011, base.Add(11*time.Second), 2) // A, part 2: 5s gap -> same session
	mk(2000, base.Add(time.Hour), 3)      // B (protected)
	mk(3000, base.Add(2*time.Hour), 4)    // C (live)

	b := NewReplayBuffer()
	b.run = 3000
	b.SetProtect(func(run int) bool { return run == 2000 })

	// 1200 bytes on disk, cap 1100: the oldest whole session (A, 500 bytes) goes.
	b.prunePersistent(ReplayConfig{Dir: dir, Persist: true, WholeSessions: true, MaxBytes: 1100})
	runs := map[int]int{}
	for _, s := range ListRecSegments(dir) {
		runs[s.Run]++
	}
	if runs[1000] != 0 || runs[1011] != 0 || runs[2000] != 3 || runs[3000] != 4 {
		t.Fatalf("whole oldest session should go, kept + live stay: %v", runs)
	}
	if _, ok := ReadRunInfo(dir, 1011); ok {
		t.Error("pruned session's sidecars should be removed")
	}

	// Cap below protected + live: only the live session's oldest footage is trimmed
	// (never its segment in progress); the kept session is untouched.
	b.prunePersistent(ReplayConfig{Dir: dir, Persist: true, WholeSessions: true, MaxBytes: 450})
	runs = map[int]int{}
	for _, s := range ListRecSegments(dir) {
		runs[s.Run]++
	}
	if runs[2000] != 3 || runs[3000] != 1 {
		t.Errorf("live session trimmed to its in-progress segment, kept intact: %v", runs)
	}
}
