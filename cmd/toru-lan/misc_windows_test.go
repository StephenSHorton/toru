package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// Tests that need neither package lan nor elevation.

func TestServiceSDDL(t *testing.T) {
	sd, err := windows.SecurityDescriptorFromString(serviceSDDL)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatal(err)
	}
	// Interactive users: start (RP), stop (WP), query status (LC); no
	// config change, delete or DACL rights.
	iu := ""
	for _, ace := range strings.SplitAfter(serviceSDDL, ")") {
		if strings.HasSuffix(ace, ";;;IU)") {
			iu = ace
		}
	}
	for _, r := range []string{"RP", "WP", "LC"} {
		if !strings.Contains(iu, r) {
			t.Errorf("IU lacks %s: %s", r, iu)
		}
	}
	for _, r := range []string{"DC", "SD", "WD", "WO"} {
		if strings.Contains(iu, r) {
			t.Errorf("IU has %s: %s", r, iu)
		}
	}
}

func TestLogRotates(t *testing.T) {
	p := filepath.Join(t.TempDir(), "service.log")
	l, err := openLog(p, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		l.Logf("line %02d with some padding", i)
	}
	l.Close()
	cur, _ := os.ReadFile(p)
	old, _ := os.ReadFile(p + ".1")
	if len(cur) > 100 || len(old) == 0 || len(old) > 100 {
		t.Fatalf("current %d bytes, rotated %d bytes", len(cur), len(old))
	}
	if !strings.Contains(string(cur), "line 19") {
		t.Fatalf("newest line missing: %q", cur)
	}
}

func TestCopyInto(t *testing.T) {
	d := t.TempDir()
	src, dst := filepath.Join(d, "a"), filepath.Join(d, "b")
	os.WriteFile(src, []byte("one"), 0o644)
	os.WriteFile(dst, []byte("old"), 0o644)
	if err := copyInto(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "one" {
		t.Fatalf("dst %q", b)
	}
	if err := copyInto(src, src); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(src); string(b) != "one" {
		t.Fatalf("copy onto itself changed it: %q", b)
	}
	if _, err := os.Stat(dst + ".new"); err == nil {
		t.Fatal("temporary file left")
	}
}
