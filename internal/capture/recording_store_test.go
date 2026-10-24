package capture

import (
	"fmt"
	"os"
	"path/filepath"
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
