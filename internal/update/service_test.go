package update

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		tag, current string
		want         bool
	}{
		{"v1.2.3", "1.2.2", true},
		{"v1.2.3", "1.2.3", false},
		{"v1.2.3", "1.3.0", false},
		{"v2.0.0", "1.9.9", true},
		{"v1.0.0", "1.0.0-rc.1", true}, // release beats its own prerelease
		{"v1.2.3", "dev", false},       // dev builds never nag
		{"v1.2.3", "", false},
		{"1.2.3", "1.2.0", true}, // tolerates missing leading v
	}
	for _, c := range cases {
		if got := isNewer(c.tag, c.current); got != c.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", c.tag, c.current, got, c.want)
		}
	}
}

func TestCmpSemver(t *testing.T) {
	if cmpSemver("1.10.0", "1.9.0") <= 0 {
		t.Error("1.10.0 should sort above 1.9.0 (numeric, not lexical)")
	}
	if cmpSemver("1.2.3", "1.2.3") != 0 {
		t.Error("equal versions should compare 0")
	}
}

// fakeLauncher records every installer launch instead of running one.
type fakeLauncher struct {
	calls [][]string
	err   error
}

func (f *fakeLauncher) launch(path string, args ...string) error {
	f.calls = append(f.calls, append([]string{path}, args...))
	return f.err
}

const testInstaller = `C:\Temp\toru-update\toru-1.2.3-windows-amd64-installer.exe`

// readyService returns a service parked in the "ready" phase with a fake
// launcher and no app (Quit/Emit are nil-guarded).
func readyService(t *testing.T) (*UpdateService, *fakeLauncher) {
	t.Helper()
	s := New("x/y", "1.0.0")
	f := &fakeLauncher{}
	s.launch = f.launch
	s.status = Status{Phase: PhaseReady, Version: "1.2.3", InstallerPath: testInstaller}
	return s, f
}

func TestRestartToUpdateLaunchesSilent(t *testing.T) {
	s, f := readyService(t)
	var seen []string
	s.SetOnChange(func(st Status) { seen = append(seen, st.Phase) })

	if err := s.RestartToUpdate(); err != nil {
		t.Fatalf("RestartToUpdate: %v", err)
	}
	want := [][]string{{testInstaller, "/S"}}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("launches = %v, want %v", f.calls, want)
	}
	if got := s.GetStatus().Phase; got != PhaseInstalling {
		t.Fatalf("phase = %q, want installing", got)
	}
	if !reflect.DeepEqual(seen, []string{PhaseInstalling}) {
		t.Fatalf("onChange phases = %v, want [installing]", seen)
	}
}

func TestRestartToUpdateRequiresReady(t *testing.T) {
	s, f := readyService(t)
	s.status = Status{Phase: PhaseDownloading, Version: "1.2.3"}
	if err := s.RestartToUpdate(); err == nil {
		t.Fatal("expected an error when no update is ready")
	}
	if len(f.calls) != 0 {
		t.Fatalf("launched %v; want nothing", f.calls)
	}
}

func TestRestartToUpdateLaunchFailureRevertsToReady(t *testing.T) {
	s, f := readyService(t)
	f.err = errors.New("boom")
	if err := s.RestartToUpdate(); err == nil {
		t.Fatal("expected launch error")
	}
	if got := s.GetStatus().Phase; got != PhaseReady {
		t.Fatalf("phase = %q, want ready (retryable)", got)
	}
}

func TestInstallOnExitLaunchesNoRelaunch(t *testing.T) {
	s, f := readyService(t)
	s.InstallOnExit()
	want := [][]string{{testInstaller, "/S", "/NORELAUNCH"}}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("launches = %v, want %v", f.calls, want)
	}
	if got := s.GetStatus().Phase; got != PhaseInstalling {
		t.Fatalf("phase = %q, want installing", got)
	}
}

func TestInstallOnExitOnlyWhenReady(t *testing.T) {
	for _, phase := range []string{PhaseIdle, PhaseChecking, PhaseDownloading, PhaseInstalling, PhaseError} {
		s, f := readyService(t)
		s.status.Phase = phase
		s.InstallOnExit()
		if len(f.calls) != 0 {
			t.Errorf("phase %q: launched %v; want nothing", phase, f.calls)
		}
	}
}

func TestNeitherLaunchesTwice(t *testing.T) {
	// The real sequence: Restart now -> app.Quit -> OnShutdown -> InstallOnExit.
	s, f := readyService(t)
	if err := s.RestartToUpdate(); err != nil {
		t.Fatal(err)
	}
	s.InstallOnExit()
	if err := s.RestartToUpdate(); err == nil {
		t.Error("second RestartToUpdate should refuse while installing")
	}
	if want := [][]string{{testInstaller, "/S"}}; !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("launches = %v, want exactly %v", f.calls, want)
	}

	// Quit without restarting: InstallOnExit twice launches once.
	s, f = readyService(t)
	s.InstallOnExit()
	s.InstallOnExit()
	if len(f.calls) != 1 {
		t.Fatalf("launches = %v, want exactly one", f.calls)
	}
}

func TestCheckAndDownloadReturnsReadyStatusWithoutRerun(t *testing.T) {
	s, f := readyService(t)
	st, err := s.CheckAndDownload()
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != PhaseReady || st.Version != "1.2.3" {
		t.Fatalf("status = %+v, want the ready status back", st)
	}
	if len(f.calls) != 0 {
		t.Fatalf("launched %v; want nothing", f.calls)
	}
}

// TestAutoUpdateKeepsChecking: a Toru left running must notice a release
// published after it started, so the loop checks again on every tick until an
// update is ready, then stops.
func TestAutoUpdateKeepsChecking(t *testing.T) {
	calls := 0
	check := func() (Status, error) {
		calls++
		switch calls {
		case 1:
			return Status{Phase: PhaseIdle}, nil // up to date at startup
		case 2:
			return Status{Phase: PhaseError}, errors.New("offline") // a failed check doesn't stop it
		default:
			return Status{Phase: PhaseReady, Version: "9.9.9"}, nil // published later
		}
	}
	done := make(chan struct{})
	go func() {
		autoUpdate(time.Millisecond, nil, false, check)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("autoUpdate did not stop after an update became ready")
	}
	if calls != 3 {
		t.Fatalf("checks = %d, want 3", calls)
	}
}

func TestAutoUpdateStops(t *testing.T) {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		autoUpdate(time.Hour, stop, false, func() (Status, error) { return Status{Phase: PhaseIdle}, nil })
		close(done)
	}()
	close(stop)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("autoUpdate ignored stop")
	}
}
