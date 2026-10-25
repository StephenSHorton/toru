package hato

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeName(t *testing.T) {
	cases := map[string]string{
		"report.pdf":            "report.pdf",
		"../../etc/passwd":      "passwd",
		`..\..\windows\evil`:    "evil",
		`C:\Users\x\secret.txt`: "secret.txt",
		"/abs/path/file":        "file",
		`a<b>c:d"e|f?g*h`:       "abcdefgh",
		`dir/a<b>\c|d`:          "cd",
		"trailing. . ":          "trailing",
		"  padded  ":            "padded",
		"NUL":                   "_NUL",
		"con.txt":               "_con.txt",
		"COM1.log":              "_COM1.log",
		"COMX":                  "COMX",
		"":                      "received",
		"...":                   "received",
		"/":                     "received",
		"<>:\"|?*":              "received",
		"ctl\x01\x1fname":       "ctlname",
		"ünïcödé.txt":           "ünïcödé.txt",
		// Length clamp: stem is cut, extension survives, total ≤ maxNameBytes.
		strings.Repeat("a", 300) + ".pdf": strings.Repeat("a", maxNameBytes-4) + ".pdf",
		// Multibyte stem is cut on a rune boundary (no torn UTF-8).
		strings.Repeat("é", 150) + ".txt": strings.Repeat("é", (maxNameBytes-4)/2) + ".txt",
		// An absurd "extension" is not one: the whole thing is the stem.
		strings.Repeat("b", 10) + "." + strings.Repeat("c", 300): strings.Repeat("b", 10) + "." + strings.Repeat("c", maxNameBytes-11),
	}
	for in, want := range cases {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCandidateName(t *testing.T) {
	cases := []struct {
		name string
		i    int
		want string
	}{
		{"a.txt", 1, "a.txt"},
		{"a.txt", 2, "a (2).txt"},
		{"a.tar.gz", 3, "a.tar (3).gz"},
		{"noext", 2, "noext (2)"},
		{".bashrc", 2, ".bashrc (2)"},
	}
	for _, c := range cases {
		if got := candidateName(c.name, c.i); got != c.want {
			t.Errorf("candidateName(%q,%d) = %q, want %q", c.name, c.i, got, c.want)
		}
	}
}

func TestClaimFileCollisionSuffix(t *testing.T) {
	dir := t.TempDir()
	want := []string{"a.txt", "a (2).txt", "a (3).txt"}
	for _, w := range want {
		p, err := claimFile(dir, "a.txt")
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Base(p) != w {
			t.Fatalf("claimFile → %q, want %q", filepath.Base(p), w)
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("placeholder not created: %v", err)
		}
	}
}

func TestClaimDirCollisionSuffix(t *testing.T) {
	dir := t.TempDir()
	// Pre-existing FILE with the folder's name must also count as a collision.
	if err := os.WriteFile(filepath.Join(dir, "pics"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := claimDir(dir, "pics")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "pics (2)" {
		t.Fatalf("claimDir → %q, want %q", filepath.Base(p), "pics (2)")
	}
	p2, err := claimDir(dir, "pics")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p2) != "pics (3)" {
		t.Fatalf("claimDir → %q, want %q", filepath.Base(p2), "pics (3)")
	}
	st, err := os.Stat(p2)
	if err != nil || !st.IsDir() {
		t.Fatalf("claimed dir missing: %v", err)
	}
}

func TestNormalizeCode(t *testing.T) {
	cases := map[string]string{
		"7-guitarist-revenge":       "7-guitarist-revenge",
		"  7-Guitarist-Revenge\n":   "7-guitarist-revenge",
		"7 guitarist revenge":       "7-guitarist-revenge",
		"7   guitarist\t revenge":   "7-guitarist-revenge",
		"22-CONFORMIST-BELFAST":     "22-conformist-belfast",
		"":                          "",
		"   ":                       "",
		"7-guitarist-revenge extra": "7-guitarist-revenge-extra",
	}
	for in, want := range cases {
		if got := NormalizeCode(in); got != want {
			t.Errorf("NormalizeCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStartReceiveRejectsBadCodes(t *testing.T) {
	m := NewManager()
	for _, code := range []string{"", "   ", "guitarist-revenge", "abc"} {
		if err := m.StartReceive(code, t.TempDir()); err == nil {
			t.Errorf("StartReceive(%q) accepted a malformed code", code)
		}
	}
	if m.ReceiveStatus().Phase != RecvIdle {
		t.Fatalf("rejected code changed state: %+v", m.ReceiveStatus())
	}
}

func TestStartSendRejectsMissingAndRoot(t *testing.T) {
	m := NewManager()
	if err := m.StartSend(filepath.Join(t.TempDir(), "nope.bin")); err == nil {
		t.Error("StartSend accepted a missing path")
	}
	if err := m.StartSend(""); err == nil {
		t.Error("StartSend accepted an empty path")
	}
	root := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	if root == string(filepath.Separator) {
		root = "/"
	}
	if err := m.StartSend(root); err == nil {
		t.Errorf("StartSend accepted the drive root %q", root)
	}
	if m.SendStatus().Phase != SendIdle {
		t.Fatalf("rejected send changed state: %+v", m.SendStatus())
	}
}

func TestStopSendIdempotentWhenIdle(t *testing.T) {
	m := NewManager()
	if err := m.StopSend(); err != nil {
		t.Fatal(err)
	}
	if err := m.CancelReceive(); err != nil {
		t.Fatal(err)
	}
	if m.SendStatus().Phase != SendIdle || m.ReceiveStatus().Phase != RecvIdle {
		t.Fatal("stop/cancel on idle changed state")
	}
}
