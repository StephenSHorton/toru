package pipe

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// keys_windows.go keeps one WireGuard static key per Windows user (by SID),
// so two accounts on one PC are two nodes. Keys live in
// %ProgramData%\Toru\lan\keys\<SID>.key, readable only by SYSTEM and
// Administrators. The private key never leaves the service: the pipe only
// ever carries the public key.

// KeySDDL protects the data folder, the keys folder and every key file
// (files get the same ACEs without the inheritance flags).
const KeySDDL = "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"

// fileSDDL drops the folder-only inheritance flags.
func fileSDDL(dirSDDL string) string { return strings.ReplaceAll(dirSDDL, "OICI", "") }

// Key is one user's node identity.
type Key struct {
	Private [32]byte
	// Salt re-derives the overlay address after a collision (Readdress).
	Salt uint32
}

type keyFile struct {
	PrivateKey string `json:"privateKey"`
	Salt       uint32 `json:"salt"`
}

// KeyStore reads and writes key files in Dir.
type KeyStore struct {
	Dir string
	// SDDL is applied to Dir and every file written; KeySDDL unless a test
	// needs to read its own files back.
	SDDL string
	// TrustedOwner refuses key files not owned by SYSTEM or Administrators,
	// so a file planted before install can never become someone's key.
	TrustedOwner bool

	mu sync.Mutex
}

// NewKeyStore returns a store in dir with the production ACL and owner check.
func NewKeyStore(dir string) *KeyStore {
	return &KeyStore{Dir: dir, SDDL: KeySDDL, TrustedOwner: true}
}

// sidRE accepts only SID strings, so a SID can never name a path elsewhere.
var sidRE = regexp.MustCompile(`^S-1-[0-9]{1,14}(-[0-9]{1,10}){1,15}$`)

func (k *KeyStore) path(sid string) (string, error) {
	if !sidRE.MatchString(sid) {
		return "", fmt.Errorf("not a SID: %q", sid)
	}
	return filepath.Join(k.Dir, sid+".key"), nil
}

// Load returns the user's key, generating and storing one on first use.
func (k *KeyStore) Load(sid string) (Key, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	p, err := k.path(sid)
	if err != nil {
		return Key{}, err
	}
	if key, err := readKey(p); err == nil {
		if k.TrustedOwner {
			if err := CheckOwner(p); err != nil {
				return Key{}, err
			}
		}
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Key{}, err
	}
	var key Key
	if _, err := rand.Read(key.Private[:]); err != nil {
		return Key{}, err
	}
	// Curve25519 clamping, as wg genkey does.
	key.Private[0] &= 248
	key.Private[31] = key.Private[31]&127 | 64
	return key, k.write(p, key)
}

// SetSalt stores a new address salt for the user (Readdress).
func (k *KeyStore) SetSalt(sid string, salt uint32) (Key, error) {
	key, err := k.Load(sid)
	if err != nil {
		return Key{}, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	p, _ := k.path(sid)
	key.Salt = salt
	return key, k.write(p, key)
}

func readKey(p string) (Key, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return Key{}, err
	}
	var f keyFile
	if err := json.Unmarshal(b, &f); err != nil {
		return Key{}, fmt.Errorf("%s: %w", p, err)
	}
	raw, err := base64.StdEncoding.DecodeString(f.PrivateKey)
	if err != nil || len(raw) != 32 {
		return Key{}, fmt.Errorf("%s: bad private key", p)
	}
	var key Key
	copy(key.Private[:], raw)
	key.Salt = f.Salt
	return key, nil
}

// write creates the file under a temporary name with the ACL already set
// (so the key is never readable by anyone else, even briefly) and renames it
// into place; a rename keeps the file's own security descriptor.
func (k *KeyStore) write(p string, key Key) error {
	if err := MakeSecureDir(k.Dir, k.SDDL); err != nil {
		return err
	}
	b, err := json.Marshal(keyFile{PrivateKey: base64.StdEncoding.EncodeToString(key.Private[:]), Salt: key.Salt})
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	os.Remove(tmp)
	if err := writeSecure(tmp, b, fileSDDL(k.SDDL)); err != nil {
		return err
	}
	from, _ := windows.UTF16PtrFromString(tmp)
	to, _ := windows.UTF16PtrFromString(p)
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("store key: %w", err)
	}
	return nil
}

func securityAttributes(sddl string) (*windows.SecurityAttributes, error) {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, fmt.Errorf("SDDL %q: %w", sddl, err)
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, nil
}

func writeSecure(p string, data []byte, sddl string) error {
	sa, err := securityAttributes(sddl)
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return fmt.Errorf("create %s: %w", p, err)
	}
	f := os.NewFile(uintptr(h), p)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// CheckOwner fails unless path is owned by SYSTEM or Administrators. Any user
// can create folders in %ProgramData%, and an owner can always rewrite the
// DACL, so a folder or file squatted before install is not trusted.
func CheckOwner(path string) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("owner of %s: %w", path, err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("owner of %s: %w", path, err)
	}
	if owner.IsWellKnown(windows.WinLocalSystemSid) || owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
		return nil
	}
	return fmt.Errorf("%s is owned by %s, not SYSTEM or Administrators", path, owner)
}

// MakeSecureDir creates dir (and parents) and sets its DACL to sddl,
// protected from inheritance. An existing folder is re-ACLed, which also
// fixes one a user loosened.
func MakeSecureDir(dir, sddl string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("SDDL %q: %w", sddl, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
