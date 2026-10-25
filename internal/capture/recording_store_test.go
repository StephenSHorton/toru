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

type segTime struct {
	name       string
	start, end float64
}

// writeSegTimes writes run's segment list (r<run>.csv) in ffmpeg's
// "<file>,<start>,<end>" format.
func writeSegTimes(t *testing.T, dir string, run int, rows []segTime) {
	t.Helper()
	var sb strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&sb, "%s,%f,%f\n", r.name, r.start, r.end)
	}
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("r%04d.csv", run)), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// footageRun writes a background run of n segments of segSec seconds each, the
// first `listed` of them in the run's csv; the rest look still in progress /
// not yet listed. Segment i ends ageOfLast + (n-1-i)*segSec ago.
func footageRun(t *testing.T, dir string, run, n, listed, size int, segSec float64, ageOfLast time.Duration) {
	t.Helper()
	writeRunInfo(dir, RunInfo{Run: run, StartMs: int64(run) * 1000, Tag: "background|Game"})
	var rows []segTime
	for i := 0; i < n; i++ {
		age := ageOfLast + time.Duration(float64(n-1-i)*segSec*float64(time.Second))
		fakeSeg(t, dir, run, i, size, age)
		if i < listed {
			rows = append(rows, segTime{fmt.Sprintf("r%04d-%08d.ts", run, i), float64(i) * segSec, float64(i+1) * segSec})
		}
	}
	if listed > 0 {
		writeSegTimes(t, dir, run, rows)
	}
}

func runCounts(dir string) map[int][]int {
	out := map[int][]int{}
	for _, s := range ListRecSegments(dir) {
		out[s.Run] = append(out[s.Run], s.Index)
	}
	return out
}

func TestPruneFootageTrimsAcrossSessions(t *testing.T) {
	dir := t.TempDir()
	footageRun(t, dir, 200, 3, 3, 10, 2, 2*time.Hour) // finished session: 6 s
	footageRun(t, dir, 300, 5, 4, 10, 2, 0)           // live: 4 finished (8 s) + in progress

	b := NewReplayBuffer()
	b.run = 300
	cfg := ReplayConfig{Dir: dir, Persist: true, WholeSessions: true, MaxFootageSec: 6}
	b.prunePersistent(cfg)

	got := runCounts(dir)
	if len(got[200]) != 0 {
		t.Errorf("run 200 should be gone: %v", got)
	}
	if want := []int{1, 2, 3, 4}; fmt.Sprint(got[300]) != fmt.Sprint(want) {
		t.Errorf("run 300 should keep its newest 3 finished + in-progress, got %v", got[300])
	}
	if _, ok := ReadRunInfo(dir, 200); ok {
		t.Error("sidecar of a fully pruned run should be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "r0200.csv")); !os.IsNotExist(err) {
		t.Error("csv of a fully pruned run should be removed")
	}
	if _, ok := ReadRunInfo(dir, 300); !ok {
		t.Error("live run's sidecar must stay")
	}
	if b.Status().Error != "" {
		t.Errorf("footage trimming is not an error: %q", b.Status().Error)
	}

	cfg.MaxFootageSec = 100
	b.prunePersistent(cfg)
	if got := runCounts(dir); fmt.Sprint(got[300]) != fmt.Sprint([]int{1, 2, 3, 4}) {
		t.Errorf("a roomy budget deletes nothing: %v", got)
	}
}

func TestPruneFootageSkipsProtected(t *testing.T) {
	dir := t.TempDir()
	footageRun(t, dir, 100, 4, 4, 10, 2, 5*time.Hour) // manual, protected: 8 s
	footageRun(t, dir, 300, 5, 4, 10, 2, 0)           // live

	b := NewReplayBuffer()
	b.run = 300
	b.SetProtect(func(run int) bool { return run == 100 })
	b.prunePersistent(ReplayConfig{Dir: dir, Persist: true, WholeSessions: true, MaxFootageSec: 6})

	got := runCounts(dir)
	if len(got[100]) != 4 {
		t.Errorf("protected run must be untouched: %v", got)
	}
	// If the protected 8 s had counted, run 300 would keep no finished footage.
	if fmt.Sprint(got[300]) != fmt.Sprint([]int{1, 2, 3, 4}) {
		t.Errorf("protected footage must not eat the budget: %v", got[300])
	}
	if _, ok := ReadRunInfo(dir, 100); !ok {
		t.Error("protected run's sidecar must stay")
	}
}

func TestPruneFootageWithoutCsvFallsBackToSegmentLength(t *testing.T) {
	dir := t.TempDir()
	footageRun(t, dir, 200, 3, 0, 10, 2, 2*time.Hour) // no csv
	footageRun(t, dir, 300, 5, 0, 10, 2, 0)           // no csv

	b := NewReplayBuffer()
	b.run = 300
	b.prunePersistent(ReplayConfig{Dir: dir, Persist: true, WholeSessions: true, MaxFootageSec: 6})

	got := runCounts(dir)
	if len(got[200]) != 0 || fmt.Sprint(got[300]) != fmt.Sprint([]int{1, 2, 3, 4}) {
		t.Errorf("without a csv each segment counts as %d s: %v", replaySegmentSec, got)
	}
}

func TestPruneFootageThenSizeCap(t *testing.T) {
	dir := t.TempDir()
	footageRun(t, dir, 200, 3, 3, 5, 2, 2*time.Hour)
	footageRun(t, dir, 300, 5, 4, 5, 2, 0)

	b := NewReplayBuffer()
	b.run = 300
	// Footage leaves run 300's 4 segments = 20 bytes; the size cap then trims
	// the live session's oldest footage down to 15 (whole-session rules).
	b.prunePersistent(ReplayConfig{Dir: dir, Persist: true, WholeSessions: true, MaxFootageSec: 6, MaxBytes: 15})

	got := runCounts(dir)
	if len(got[200]) != 0 || fmt.Sprint(got[300]) != fmt.Sprint([]int{2, 3, 4}) {
		t.Errorf("footage then size cap: %v", got)
	}
	b.mu.Lock()
	used := b.diskUsed
	b.mu.Unlock()
	if err := b.Status().Error; used != 15 || err != "" {
		t.Errorf("after prune: diskUsed=%d err=%q", used, err)
	}
}

func TestSetMaxFootageSecLive(t *testing.T) {
	dir := t.TempDir()
	footageRun(t, dir, 300, 5, 4, 10, 2, 0)

	b := NewReplayBuffer()
	b.run = 300
	cfg := ReplayConfig{Dir: dir, Persist: true, WholeSessions: true, MaxFootageSec: 6}

	// The live override is tighter than the running config.
	b.SetMaxFootageSec(2)
	b.prunePersistent(cfg)
	if got := runCounts(dir); fmt.Sprint(got[300]) != fmt.Sprint([]int{3, 4}) {
		t.Fatalf("SetMaxFootageSec(2) should keep one finished segment: %v", got)
	}

	// Setting it back to 0 turns the limit off even though cfg still says 6.
	b.SetMaxFootageSec(0)
	footageRun(t, dir, 400, 6, 5, 10, 2, 0)
	b.run = 400
	b.prunePersistent(cfg)
	got := runCounts(dir)
	if len(got[300]) != 2 || len(got[400]) != 6 {
		t.Errorf("SetMaxFootageSec(0) should disable the budget: %v", got)
	}
}

func TestPruneFootageTrimsInsideFinishedSession(t *testing.T) {
	dir := t.TempDir()
	footageRun(t, dir, 200, 3, 3, 10, 2, 2*time.Hour) // finished: 6 s
	footageRun(t, dir, 300, 5, 4, 10, 2, 0)           // live: 4 finished (8 s) + in progress

	b := NewReplayBuffer()
	b.run = 300
	b.prunePersistent(ReplayConfig{Dir: dir, Persist: true, WholeSessions: true, MaxFootageSec: 10})

	// 10 s: all of run 300's finished footage (8 s) plus run 200's newest
	// segment (2 s); the finished run is trimmed from the front, not deleted.
	got := runCounts(dir)
	if fmt.Sprint(got[200]) != fmt.Sprint([]int{2}) || fmt.Sprint(got[300]) != fmt.Sprint([]int{0, 1, 2, 3, 4}) {
		t.Fatalf("finished run should keep only its newest segment: %v", got)
	}
	if _, ok := ReadRunInfo(dir, 200); !ok {
		t.Error("a trimmed run that still has footage keeps its sidecar")
	}
	if _, err := os.Stat(filepath.Join(dir, "r0200.csv")); err != nil {
		t.Error("a trimmed run that still has footage keeps its segment list")
	}
	b.mu.Lock()
	used := b.diskUsed
	b.mu.Unlock()
	if used != 6*10 {
		t.Errorf("diskUsed = %d, want the 6 survivors' bytes", used)
	}
	if b.Status().DiskBytes != 6*10 {
		t.Errorf("status after a prune should carry the bytes on disk: %+v", b.Status())
	}
}
