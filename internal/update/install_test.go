package update

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// useUpdateDir points the update folder at a test directory.
func useUpdateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := updateDir
	updateDir = func() string { return dir }
	t.Cleanup(func() { updateDir = old })
	return dir
}

// installerServer serves body as the installer and returns UpdateInfo for it.
func installerServer(t *testing.T, version string, body []byte) UpdateInfo {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(body)
	return UpdateInfo{Version: version, AssetURL: srv.URL, AssetName: "toru-" + version + "-windows-amd64-installer.exe", SHA256: hex.EncodeToString(sum[:])}
}

func TestDownloadGetsItsOwnFolder(t *testing.T) {
	useUpdateDir(t)
	info := installerServer(t, "1.2.3", []byte("installer"))
	s := New("x/y", "1.0.0")
	first, err := s.downloadVerified(info)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.downloadVerified(info)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("both downloads went to %s; want a folder each", first)
	}
	if filepath.Base(second) != info.AssetName || !strings.HasPrefix(filepath.Base(filepath.Dir(second)), "v1.2.3-") {
		t.Fatalf("installer at %s; want <root>/v1.2.3-*/%s", second, info.AssetName)
	}
	// The earlier download was cleaned up (nothing holds it here).
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Errorf("earlier download %s still there (err %v)", first, err)
	}
	if b, _ := os.ReadFile(second); string(b) != "installer" {
		t.Errorf("installer content %q", b)
	}
}

func TestCleanDownloadsKeepsTheInstallerReport(t *testing.T) {
	root := useUpdateDir(t)
	report := filepath.Join(root, installFailedFile)
	if err := os.WriteFile(report, []byte("1.2.3\tx"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "toru-1.2.3-windows-amd64-installer.exe"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	cleanDownloads(root)
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 || entries[0].Name() != installFailedFile {
		t.Fatalf("left %v; want only %s", entries, installFailedFile)
	}
}

func TestTakeInstallFailure(t *testing.T) {
	cases := []struct {
		file, current string
		ok            bool
		version, why  string
	}{
		{"1.2.3\ttoru.exe is in use\r\n", "1.2.2", true, "1.2.3", "toru.exe is in use"},
		{"1.2.3\tx\r\n", "1.2.3", false, "", ""}, // a later install worked
		{"1.2.3\tx\r\n", "1.3.0", false, "", ""},
		{"1.2.3\r\n", "1.0.0", true, "1.2.3", ""},
		{"\r\n", "1.0.0", false, "", ""},
	}
	for _, c := range cases {
		dir := t.TempDir()
		p := filepath.Join(dir, installFailedFile)
		if err := os.WriteFile(p, []byte(c.file), 0o644); err != nil {
			t.Fatal(err)
		}
		v, why, ok := takeInstallFailure(dir, c.current)
		if ok != c.ok || v != c.version || why != c.why {
			t.Errorf("%q on %s: got (%q, %q, %v), want (%q, %q, %v)", c.file, c.current, v, why, ok, c.version, c.why, c.ok)
		}
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%q: report not removed", c.file)
		}
	}
	if _, _, ok := takeInstallFailure(t.TempDir(), "1.0.0"); ok {
		t.Error("no report: ok = true")
	}
}

func TestReportInstallFailure(t *testing.T) {
	root := useUpdateDir(t)
	if err := os.WriteFile(filepath.Join(root, installFailedFile), []byte("1.2.3\tToru didn't exit within 60 seconds\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New("x/y", "1.0.0")
	if !s.reportInstallFailure() {
		t.Fatal("reportInstallFailure = false")
	}
	st := s.GetStatus()
	if st.Phase != PhaseError || st.Version != "1.2.3" || !st.InstallFailed ||
		st.Error != "The update to v1.2.3 didn't install: Toru didn't exit within 60 seconds. Check for Updates to try again." {
		t.Fatalf("status = %+v", st)
	}
	if s.reportInstallFailure() {
		t.Error("reported twice")
	}
}

func TestAutoUpdateWaitsAfterAFailedInstall(t *testing.T) {
	stop := make(chan struct{})
	done := make(chan struct{})
	checked := make(chan struct{}, 1)
	go func() {
		autoUpdate(time.Hour, stop, true, func() (Status, error) {
			checked <- struct{}{}
			return Status{Phase: PhaseIdle}, nil
		})
		close(done)
	}()
	select {
	case <-checked:
		t.Fatal("checked straight away after a failed install")
	case <-time.After(50 * time.Millisecond):
	}
	close(stop)
	<-done
}

func TestRemoveOldExes(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"toru.exe", "toru.exe.old", "Toru.exe.old.3", "toru-lan.exe", "toru.exe.config"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	removeOldExes(filepath.Join(dir, "toru.exe"))
	var left []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if strings.Join(left, ",") != "toru-lan.exe,toru.exe,toru.exe.config" {
		t.Fatalf("left %v", left)
	}
}

// TestInstallerNames keeps the installer in step with the app: the mutex it
// waits for is the one Wails creates for main.go's UniqueID
// ("wails-app-<id>-sim", pkg/application/single_instance_windows.go), and the
// report it writes is the one takeInstallFailure reads.
func TestInstallerNames(t *testing.T) {
	nsi, err := os.ReadFile("../../build/windows/nsis/project.nsi")
	if err != nil {
		t.Fatal(err)
	}
	mainGo, err := os.ReadFile("../../main.go")
	if err != nil {
		t.Fatal(err)
	}
	const id = "com.stephenshorton.toru"
	if !strings.Contains(string(mainGo), `UniqueID: "`+id+`"`) {
		t.Fatalf("main.go's SingleInstance UniqueID isn't %q: update TORU_INSTANCE_MUTEX in project.nsi and this test", id)
	}
	for _, want := range []string{
		`!define TORU_INSTANCE_MUTEX "wails-app-` + id + `-sim"`,
		`!define TORU_INSTALL_FAILED_FILE "` + installFailedFile + `"`,
		`$TEMP\toru-update\${TORU_INSTALL_FAILED_FILE}`,
	} {
		if !strings.Contains(string(nsi), want) {
			t.Errorf("project.nsi lacks %s", want)
		}
	}
}
