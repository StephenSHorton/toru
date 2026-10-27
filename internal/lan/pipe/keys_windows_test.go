package pipe

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func currentSID(t *testing.T) string {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return u.User.Sid.String()
}

// readableStore is the production layout with this (unelevated) user added
// to the ACL, so the test can read its own keys back.
func readableStore(t *testing.T) *KeyStore {
	return &KeyStore{Dir: filepath.Join(t.TempDir(), "keys"), SDDL: KeySDDL + "(A;OICI;FA;;;" + currentSID(t) + ")"}
}

func TestKeyStoreRoundTrip(t *testing.T) {
	k := readableStore(t)
	sid := "S-1-5-21-111-222-333-1001"
	a, err := k.Load(sid)
	if err != nil {
		t.Fatal(err)
	}
	if a.Private == [32]byte{} || a.Private[0]&7 != 0 || a.Private[31]&0xc0 != 0x40 {
		t.Fatalf("key not generated or not clamped: %x", a.Private)
	}
	b, err := k.Load(sid)
	if err != nil || b != a {
		t.Fatalf("second load %v %v", b, err)
	}
	other, err := k.Load("S-1-5-21-111-222-333-1002")
	if err != nil || other.Private == a.Private {
		t.Fatalf("second user shares a key: %v", err)
	}
	c, err := k.SetSalt(sid, 5)
	if err != nil || c.Salt != 5 || c.Private != a.Private {
		t.Fatalf("set salt: %+v %v", c, err)
	}
	if d, _ := k.Load(sid); d.Salt != 5 {
		t.Fatalf("salt not persisted: %+v", d)
	}
	if _, err := os.Stat(filepath.Join(k.Dir, sid+".key.tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("temporary file left behind")
	}
}

func TestKeyStoreRejectsNonSID(t *testing.T) {
	k := readableStore(t)
	for _, s := range []string{"", `..\..\x`, "S-1-5-21-1/..", "S-1-5-21-1.key", "s-1-5-21-1", "S-1"} {
		if _, err := k.Load(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}

// TestProductionACL checks KeySDDL as applied to a folder and a key file:
// SYSTEM and Administrators only, protected. An unelevated user cannot
// create files under such a folder at all (the service runs as SYSTEM), so
// the file is written into an ordinary temp folder here. The user owns what
// it created and still cannot read it.
func TestProductionACL(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "keys")
	file := filepath.Join(base, "S-1-5-21-1-2-3-500.key")
	// As owner this user may still rewrite the DACLs, which TempDir's
	// cleanup needs in order to delete them.
	t.Cleanup(func() {
		mine := "D:P(A;OICI;FA;;;" + currentSID(t) + ")"
		MakeSecureDir(dir, mine)
		if sd, err := windows.SecurityDescriptorFromString(fileSDDL(mine)); err == nil {
			dacl, _, _ := sd.DACL()
			windows.SetNamedSecurityInfo(file, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
		}
	})
	if err := MakeSecureDir(dir, KeySDDL); err != nil {
		t.Fatal(err)
	}
	if err := writeSecure(file, []byte(`{"privateKey":"x"}`), fileSDDL(KeySDDL)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, file} {
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		s := sd.String()
		if !strings.HasPrefix(s, "D:P") || !strings.Contains(s, ";;;SY)") || !strings.Contains(s, ";;;BA)") || strings.Count(s, "(A;") != 2 {
			t.Errorf("%s DACL %s", path, s)
		}
	}
	if _, err := os.ReadFile(file); err == nil {
		t.Error("a non-admin user could read the key file")
	}
	if f, err := os.Create(filepath.Join(dir, "planted.key")); err == nil {
		f.Close()
		t.Error("a non-admin user could create a file in the keys folder")
	}
	// A normal user's file is never trusted as a key.
	if err := CheckOwner(file); err == nil {
		t.Error("CheckOwner trusted a file owned by a normal user")
	}
	k := &KeyStore{Dir: filepath.Join(base, "trusted"), SDDL: "D:P(A;OICI;FA;;;" + currentSID(t) + ")", TrustedOwner: true}
	if _, err := k.Load("S-1-5-21-9-9-9-9"); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Load("S-1-5-21-9-9-9-9"); err == nil {
		t.Error("TrustedOwner store accepted a key file owned by a normal user")
	}
}
