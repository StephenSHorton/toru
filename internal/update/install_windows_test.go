package update

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// TestDownloadNotBlockedByARunningInstaller is the 0.14.2 failure: the
// installer from the last attempt was still running, so its file was open
// with no sharing, and the next download of the same version failed with
// "being used by another process". Each download now gets its own folder,
// and the clean-up skips what it can't remove.
func TestDownloadNotBlockedByARunningInstaller(t *testing.T) {
	useUpdateDir(t)
	info := installerServer(t, "1.2.3", []byte("installer"))
	s := New("x/y", "1.0.0")
	first, err := s.downloadVerified(info)
	if err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(first)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(h) }()

	second, err := s.downloadVerified(info)
	if err != nil {
		t.Fatalf("download with the last installer held open: %v", err)
	}
	if second == first {
		t.Fatalf("second download reused %s", first)
	}
	if _, err := os.Stat(first); err != nil {
		t.Errorf("the held installer was removed or lost: %v", err)
	}
}
