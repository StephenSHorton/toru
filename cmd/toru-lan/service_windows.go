package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/StephenSHorton/toru/internal/lan/adapter"
	"github.com/StephenSHorton/toru/internal/lan/api"
	"github.com/StephenSHorton/toru/internal/lan/pipe"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/debug"
	"golang.org/x/sys/windows/svc/eventlog"
)

// service_windows.go is the service process: the pipe server and the host
// behind it. It stops itself once no GUI has been connected for a minute
// (demand-start: Toru starts it when LAN is turned on), and that clean stop
// does not trigger the restart-on-failure recovery action.

type lanService struct {
	console bool
}

// runService runs as the ToruLan service, or (debug) in this console.
func runService(console bool) error {
	if console {
		if !elevated() {
			return errors.New("debug needs an elevated console (Wintun adapters are admin-only)")
		}
		return debug.Run(api.ServiceName, &lanService{console: true})
	}
	return svc.Run(api.ServiceName, &lanService{})
}

func (s *lanService) Execute(args []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	// The data folders are checked before anything is opened in them: the
	// log is created as SYSTEM, so it must not land where a junction says.
	if err := checkDataDirs(); err != nil && !s.console {
		eventLogf("toru-lan %s not starting: %v", version, err)
		return true, 1
	}
	logf, closeLog := s.openLog()
	defer closeLog()
	logf("toru-lan %s starting (pid %d)", version, os.Getpid())

	srv, h, idle, err := start(logf)
	if err != nil {
		logf("start failed: %v", err)
		// A service-specific exit code counts as a failure, so recovery
		// restarts the service (a few times, then gives up).
		return true, 1
	}
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

loop:
	for {
		select {
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				logf("stop requested")
				break loop
			}
		case <-idle:
			break loop
		}
	}

	status <- svc.Status{State: svc.StopPending}
	srv.Close()
	if err := h.stop(); err != nil {
		logf("stop: %v", err)
	}
	logf("stopped")
	return false, 0
}

// openLog opens %ProgramData%\Toru\lan\service.log, or stderr in debug mode
// or when the data folder is unusable.
func (s *lanService) openLog() (func(string, ...any), func()) {
	stderr := func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
	if s.console {
		return stderr, func() {}
	}
	p, err := machinePaths()
	if err != nil {
		return stderr, func() {}
	}
	l, err := openLog(p.logFile(), logMax)
	if err != nil {
		return stderr, func() {}
	}
	return l.Logf, func() { l.Close() }
}

// checkDataDirs: install created %ProgramData%\Toru and ...\lan as
// Administrators-owned real folders. Refuse ones a user created first (they
// could hold planted keys) and junctions or links in their place (they would
// send SYSTEM's writes elsewhere); each is checked on the folder itself.
// The keys folder is created if missing, under the same checks.
func checkDataDirs() error {
	p, err := machinePaths()
	if err != nil {
		return err
	}
	for _, d := range []string{filepath.Dir(p.dataDir), p.dataDir} {
		if err := pipe.CheckTrustedDir(d); err != nil {
			return fmt.Errorf("data folder not trusted (re-run install): %w", err)
		}
	}
	if err := pipe.MakeTrustedDir(p.keysDir(), pipe.KeySDDL); err != nil {
		return fmt.Errorf("keys folder not trusted (re-run install): %w", err)
	}
	return nil
}

// eventLogf reports to the Windows event log (Application, source ToruLan),
// for failures before the service log can be trusted.
func eventLogf(format string, args ...any) {
	l, err := eventlog.Open(api.ServiceName)
	if err != nil {
		return
	}
	defer l.Close()
	_ = l.Error(1, fmt.Sprintf(format, args...))
}

func start(logf func(string, ...any)) (*pipe.Server, *host, <-chan struct{}, error) {
	p, err := machinePaths()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := checkDataDirs(); err != nil {
		return nil, nil, nil, err
	}
	dir, err := selfDir()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := adapter.LoadWintun(filepath.Join(dir, api.WintunDLL)); err != nil {
		return nil, nil, nil, err
	}

	h := newHost(pipe.NewKeyStore(p.keysDir()), logf)
	idle := make(chan struct{})
	var once sync.Once
	srv, err := pipe.Listen(pipe.Config{
		Version: version,
		Backend: h,
		Logf:    logf,
		OnIdle:  func() { once.Do(func() { close(idle) }) },
	})
	if err != nil {
		return nil, nil, nil, err
	}
	h.out = srv
	return srv, h, idle, nil
}
