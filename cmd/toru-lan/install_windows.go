package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/StephenSHorton/toru/internal/lan/adapter"
	"github.com/StephenSHorton/toru/internal/lan/api"
	"github.com/StephenSHorton/toru/internal/lan/pipe"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// install_windows.go is "toru-lan.exe install" and "uninstall", run once
// elevated (one UAC prompt) from Toru's per-user folder. Install copies
// this exe and wintun.dll into admin-only Program Files (a SYSTEM service
// registered from the user-writable folder would let any user process
// become SYSTEM), registers the service and creates the firewall rules.
// Re-running it is the update path: stop, replace, start if it was running.

// serviceSDDL is the ToruLan service's DACL. SYSTEM and Administrators get
// full control; interactive users may start, stop and query it (plus the
// read rights every service grants them), so turning LAN on and off needs
// no UAC; service users keep the default read rights.
const serviceSDDL = "D:" +
	"(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;SY)" +
	"(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;BA)" +
	"(A;;CCLCSWRPWPLOCRRC;;;IU)" +
	"(A;;CCLCSWLOCRRC;;;SU)"

// Optional files copied next to the exe when present beside the source
// (the Wintun license must ship with the DLL).
var optionalFiles = []string{"LICENSE.txt", "wintun-LICENSE.txt"}

func install() error {
	if !elevated() {
		return errors.New("install must run elevated")
	}
	p, err := machinePaths()
	if err != nil {
		return err
	}
	src, err := selfDir()
	if err != nil {
		return err
	}
	srcExe := filepath.Join(src, api.LanExe)
	if exe, err := os.Executable(); err == nil {
		srcExe = exe
	}
	srcDLL := filepath.Join(src, api.WintunDLL)
	if err := adapter.VerifySHA256(srcDLL, api.WintunSHA256); err != nil {
		return fmt.Errorf("refusing %s: %w", srcDLL, err)
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("service manager: %w", err)
	}
	defer m.Disconnect()

	// Update: stop a running service first so its files can be replaced.
	wasRunning := false
	if s, err := m.OpenService(api.ServiceName); err == nil {
		if st, err := s.Query(); err == nil && st.State != svc.Stopped {
			wasRunning = true
			fmt.Println("stopping the running service")
			if err := stopService(s); err != nil {
				s.Close()
				return err
			}
		}
		s.Close()
	}

	if err := prepareDataDir(p); err != nil {
		return err
	}
	if err := os.MkdirAll(p.installDir, 0o755); err != nil {
		return err
	}
	if err := copyInto(srcExe, p.exe()); err != nil {
		return err
	}
	if err := copyInto(srcDLL, p.dll()); err != nil {
		return err
	}
	if err := adapter.VerifySHA256(p.dll(), api.WintunSHA256); err != nil {
		return err
	}
	for _, name := range optionalFiles {
		if _, err := os.Stat(filepath.Join(src, name)); err == nil {
			if err := copyInto(filepath.Join(src, name), filepath.Join(p.installDir, name)); err != nil {
				return err
			}
		}
	}
	fmt.Println("installed", p.exe())

	if err := registerService(m, p.exe()); err != nil {
		return err
	}
	fmt.Println("registered service", api.ServiceName)

	// The firewall pins rules to an interface that exists now, so bring the
	// adapter up briefly if it is not there. Loading the DLL from Program
	// Files (just verified) keeps the per-user copy unlocked.
	if err := adapter.LoadWintun(p.dll()); err != nil {
		return err
	}
	if err := adapter.WithBareAdapter(adapter.EnsureFirewallRules); err != nil {
		return fmt.Errorf("firewall rules: %w", err)
	}
	fmt.Println("firewall rules:", strings.Join(adapter.FirewallRuleNames(), ", "))

	if wasRunning {
		s, err := m.OpenService(api.ServiceName)
		if err != nil {
			return err
		}
		defer s.Close()
		if err := s.Start(); err != nil {
			return fmt.Errorf("restart service: %w", err)
		}
		fmt.Println("service restarted")
	}
	return nil
}

func registerService(m *mgr.Mgr, exe string) error {
	cfg := mgr.Config{
		ServiceType:  windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:    mgr.StartManual,
		ErrorControl: mgr.ErrorNormal,
		DisplayName:  api.ServiceDisplayName,
		Description:  api.ServiceDescription,
		SidType:      windows.SERVICE_SID_TYPE_UNRESTRICTED,
	}
	s, err := m.OpenService(api.ServiceName)
	if err == nil {
		old, err := s.Config()
		if err != nil {
			s.Close()
			return err
		}
		cfg.BinaryPathName = syscall.EscapeArg(exe)
		cfg.ServiceStartName = "LocalSystem"
		cfg.Dependencies = old.Dependencies
		if err := s.UpdateConfig(cfg); err != nil {
			s.Close()
			return fmt.Errorf("update service: %w", err)
		}
	} else {
		if s, err = m.CreateService(api.ServiceName, exe, cfg); err != nil {
			return fmt.Errorf("create service: %w", err)
		}
	}
	defer s.Close()
	// Restart on failure (crash or a non-zero exit); a clean idle stop exits 0.
	actions := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 2 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
	}
	if err := s.SetRecoveryActions(actions, 24*60*60); err != nil {
		return fmt.Errorf("recovery actions: %w", err)
	}
	if err := s.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		return fmt.Errorf("recovery on non-crash failures: %w", err)
	}
	return setServiceSD(s)
}

func setServiceSD(s *mgr.Service) error {
	sd, err := windows.SecurityDescriptorFromString(serviceSDDL)
	if err != nil {
		return fmt.Errorf("service SDDL: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if err := windows.SetSecurityInfo(s.Handle, windows.SE_SERVICE, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return fmt.Errorf("service security: %w", err)
	}
	return nil
}

func stopService(s *mgr.Service) error {
	st, err := s.Control(svc.Stop)
	if err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
		return fmt.Errorf("stop service: %w", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for st.State != svc.Stopped {
		if time.Now().After(deadline) {
			return errors.New("service did not stop within 30 s")
		}
		time.Sleep(250 * time.Millisecond)
		if st, err = s.Query(); err != nil {
			return err
		}
	}
	return nil
}

// prepareDataDir makes %ProgramData%\Toru and ...\Toru\lan Administrators-
// owned and SYSTEM/Administrators-only. Any user can create folders in
// ProgramData, so one that already exists under another owner is moved
// aside rather than trusted.
func prepareDataDir(p paths) error {
	for _, d := range []string{filepath.Dir(p.dataDir), p.dataDir} {
		if _, err := os.Stat(d); err == nil {
			if err := pipe.CheckOwner(d); err != nil {
				aside := fmt.Sprintf("%s.untrusted-%d", d, time.Now().Unix())
				fmt.Printf("%v; moving it to %s\n", err, aside)
				if err := os.Rename(d, aside); err != nil {
					return fmt.Errorf("move untrusted %s aside: %w", d, err)
				}
			}
		}
		if err := pipe.MakeSecureDir(d, pipe.KeySDDL); err != nil {
			return fmt.Errorf("%s: %w", d, err)
		}
	}
	return pipe.MakeSecureDir(p.keysDir(), pipe.KeySDDL)
}

// copyInto copies src to dst through a temporary file and a rename, so an
// interrupted install never leaves half a binary. Copying a file onto
// itself (install run from Program Files) does nothing.
func copyInto(src, dst string) error {
	if same, _ := sameFile(src, dst); same {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	from, _ := windows.UTF16PtrFromString(tmp)
	to, _ := windows.UTF16PtrFromString(dst)
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", dst, err)
	}
	return nil
}

func sameFile(a, b string) (bool, error) {
	sa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	sb, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(sa, sb), nil
}

func uninstall() error {
	if !elevated() {
		return errors.New("uninstall must run elevated")
	}
	p, err := machinePaths()
	if err != nil {
		return err
	}
	var errs []error
	if err := removeService(); err != nil {
		errs = append(errs, err)
	}

	// A stopped service has already removed its adapter; this catches one
	// left by a crash. Prefer the DLL beside us, so the Program Files copy
	// is not locked by this process when it is deleted.
	if adapter.Exists() {
		dll := p.dll()
		if src, err := selfDir(); err == nil {
			if c := filepath.Join(src, api.WintunDLL); adapter.VerifySHA256(c, api.WintunSHA256) == nil {
				dll = c
			}
		}
		if err := adapter.LoadWintun(dll); err != nil {
			errs = append(errs, err)
		} else if err := adapter.Remove(); err != nil {
			errs = append(errs, fmt.Errorf("remove adapter: %w", err))
		} else {
			fmt.Println("removed adapter", api.AdapterName)
		}
	}

	if err := adapter.RemoveFirewallRules(); err != nil {
		errs = append(errs, fmt.Errorf("firewall rules: %w", err))
	} else {
		fmt.Println("removed firewall rules")
	}

	if err := os.RemoveAll(p.dataDir); err != nil {
		errs = append(errs, err)
	}
	removeIfEmpty(filepath.Dir(p.dataDir))

	os.RemoveAll(p.installDir) // fails on this exe (and DLL) if they are running from there
	if _, err := os.Stat(p.installDir); err == nil {
		if err := deleteAfterExit(p.installDir); err != nil {
			errs = append(errs, err)
		} else {
			fmt.Println("scheduled removal of", p.installDir)
		}
	} else {
		removeIfEmpty(filepath.Dir(p.installDir))
		fmt.Println("removed", p.installDir)
	}
	return errors.Join(errs...)
}

func removeService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("service manager: %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(api.ServiceName)
	if err != nil {
		return nil // not installed
	}
	if st, err := s.Query(); err == nil && st.State != svc.Stopped {
		if err := stopService(s); err != nil {
			s.Close()
			return err
		}
	}
	if err := s.Delete(); err != nil {
		s.Close()
		return fmt.Errorf("delete service: %w", err)
	}
	s.Close()
	// Deletion completes once the last handle closes; wait for it so a
	// re-install right after works.
	for i := 0; i < 40; i++ {
		s, err := m.OpenService(api.ServiceName)
		if err != nil {
			fmt.Println("removed service", api.ServiceName)
			return nil
		}
		s.Close()
		time.Sleep(250 * time.Millisecond)
	}
	return errors.New("service still registered (marked for deletion; it goes at next reboot)")
}

func removeIfEmpty(dir string) {
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
		os.Remove(dir)
	}
}

// deleteAfterExit removes dir a moment after this process exits (its own exe
// may be in it), with a reboot-time delete as the fallback. The wait is a
// loopback ping: no listener, no firewall prompt.
func deleteAfterExit(dir string) error {
	parent := filepath.Dir(dir)
	script := fmt.Sprintf(`ping -n 4 127.0.0.1 >nul & rmdir /s /q "%s" & rmdir "%s"`, dir, parent)
	cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"))
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       `cmd.exe /d /c "` + script + `"`,
		CreationFlags: windows.CREATE_NO_WINDOW | windows.DETACHED_PROCESS,
		HideWindow:    true,
	}
	err := cmd.Start()
	// Belt and braces: files still there at the next boot are removed then.
	if entries, rerr := os.ReadDir(dir); rerr == nil {
		for _, e := range entries {
			name, _ := windows.UTF16PtrFromString(filepath.Join(dir, e.Name()))
			windows.MoveFileEx(name, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
		}
		name, _ := windows.UTF16PtrFromString(dir)
		windows.MoveFileEx(name, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
	}
	if err != nil {
		return fmt.Errorf("schedule removal of %s (it goes at next reboot): %w", dir, err)
	}
	return cmd.Process.Release()
}
