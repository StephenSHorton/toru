package capture

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSweepTempRemovesOnlyStaleEntries(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := now.Add(-48 * time.Hour)

	write := func(name string, mod time.Time) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("12345"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
		return p
	}
	mkdir := func(name string, mod time.Time) string {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Join(p, "inner"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
		return p
	}

	staleRec := write("toru-rec-20260101-000000-1.webm", old)
	freshRec := write("toru-rec-20260101-000000-2.webm", now.Add(-time.Hour))
	staleTrim := write("toru-rec-20260101-000000-1-trimmed.webm", old)
	staleDiscord := write("toru-rec-20260101-000000-1-cropped-discord.webm", old)
	staleShare := mkdir("share-123", old)
	foreignDir := mkdir("something-else", old)

	removed, freed := SweepTemp(dir, TempMaxAge, now)
	if removed != 2 || freed != 5 {
		t.Fatalf("removed=%d freed=%d, want 2 and 5", removed, freed)
	}
	for _, gone := range []string{staleRec, staleShare} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s should be removed", filepath.Base(gone))
		}
	}
	for _, kept := range []string{freshRec, foreignDir, staleTrim, staleDiscord} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s should be kept: %v", filepath.Base(kept), err)
		}
	}
}

func TestSweepTempMissingDir(t *testing.T) {
	if n, _ := SweepTemp(filepath.Join(t.TempDir(), "nope"), TempMaxAge, time.Now()); n != 0 {
		t.Fatalf("removed %d from a missing dir", n)
	}
}
