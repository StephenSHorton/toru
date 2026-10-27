package pipe

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestJunctionNotFollowed: a junction where a data folder should be (any
// user can make one in %ProgramData%, no privilege needed) is refused by
// every check and never has its target re-ACLed.
func TestJunctionNotFollowed(t *testing.T) {
	if windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("elevated: an Administrators-owned folder is trusted by design; this checks a normal user (CI runs elevated)")
	}
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(target, "still-mine.txt")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "Toru")
	if out, err := exec.Command("cmd", "/d", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("mklink /J: %v %s", err, out)
	}
	t.Cleanup(func() { _ = os.Remove(link) })

	for name, fn := range map[string]func(string) error{
		"MakeSecureDir":   func(d string) error { return MakeSecureDir(d, KeySDDL) },
		"MakeTrustedDir":  func(d string) error { return MakeTrustedDir(d, KeySDDL) },
		"CheckTrustedDir": CheckTrustedDir,
		"CheckOwner":      CheckOwner,
	} {
		if err := fn(link); !errors.Is(err, ErrUntrusted) {
			t.Errorf("%s on a junction: %v, want ErrUntrusted", name, err)
		}
	}
	// The target kept its DACL: this user can still read inside it.
	if _, err := os.ReadFile(marker); err != nil {
		t.Fatalf("the junction's target was re-ACLed: %v", err)
	}
	// A folder this user owns is untrusted too; a file is not a folder.
	if err := CheckTrustedDir(target); !errors.Is(err, ErrUntrusted) {
		t.Errorf("CheckTrustedDir on a user's folder: %v", err)
	}
	if err := CheckTrustedDir(marker); !errors.Is(err, ErrUntrusted) {
		t.Errorf("CheckTrustedDir on a file: %v", err)
	}
	// MakeSecureDir still works on a real folder (and creates a missing one).
	fresh := filepath.Join(base, "fresh", "keys")
	if err := MakeSecureDir(fresh, KeySDDL+"(A;OICI;FA;;;"+currentSID(t)+")"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(fresh); err != nil || !fi.IsDir() {
		t.Fatalf("fresh folder: %v", err)
	}
}
