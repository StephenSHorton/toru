//go:build windows

package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"

	"github.com/StephenSHorton/toru/internal/agentctl"
)

// DialToru connects to this user's agent pipe and checks who serves it
// before anything is sent: another program could create the pipe name first
// (pipe squatting) and read every command. The server must run as the
// current user and be Toru's executable.
func DialToru(ctx context.Context) (net.Conn, error) {
	conn, err := agentctl.Dial(ctx, "")
	if err != nil {
		return nil, err
	}
	if err := verifyPipeServer(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func verifyPipeServer(conn net.Conn) error {
	f, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return fmt.Errorf("%w: can't inspect the pipe", ErrNotToru)
	}
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(windows.Handle(f.Fd()), &pid); err != nil {
		return fmt.Errorf("%w: can't find the pipe's server process: %v", ErrNotToru, err)
	}
	exe, sid, err := processIdentity(pid)
	if err != nil {
		return fmt.Errorf("%w: can't inspect the pipe's server (pid %d): %v", ErrNotToru, pid, err)
	}
	me, err := currentSID()
	if err != nil {
		return err
	}
	if sid != me {
		return fmt.Errorf("%w: the pipe is served by another user's process (%s, pid %d)", ErrNotToru, filepath.Base(exe), pid)
	}
	if !isToruExe(exe) {
		return fmt.Errorf("%w: the pipe is served by %s (pid %d), not Toru", ErrNotToru, exe, pid)
	}
	return nil
}

// isToruExe accepts toru.exe, or a build with the same file name as this
// one (a test build of Toru runs both sides).
func isToruExe(exe string) bool {
	base := filepath.Base(exe)
	if strings.EqualFold(base, "toru.exe") {
		return true
	}
	self, err := os.Executable()
	return err == nil && strings.EqualFold(base, filepath.Base(self))
}

func processIdentity(pid uint32) (exe, sid string, err error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return "", "", err
	}
	exe = windows.UTF16ToString(buf[:n])
	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		return exe, "", err
	}
	defer func() { _ = tok.Close() }()
	u, err := tok.GetTokenUser()
	if err != nil {
		return exe, "", err
	}
	return exe, u.User.Sid.String(), nil
}

func currentSID() (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("current user: %w", err)
	}
	return u.User.Sid.String(), nil
}

var procAttachConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")

// attachParentProcess is ATTACH_PARENT_PROCESS ((DWORD)-1).
const attachParentProcess = ^uintptr(0) & 0xFFFFFFFF

// AttachConsole gives `toru.exe ctl` a console to print to. Toru is a GUI
// program, so run from cmd or PowerShell it starts with no stdout; this
// attaches the parent's console. Redirected output (a pipe or a file, as
// when an agent runs it) already has handles and is left alone.
func AttachConsole() {
	if h, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE); err == nil && h != 0 && h != windows.InvalidHandle {
		return
	}
	if r, _, _ := procAttachConsole.Call(attachParentProcess); r == 0 {
		return // no parent console (started from Explorer): nothing to print to
	}
	if out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = out, out
	}
	if in, err := os.OpenFile("CONIN$", os.O_RDONLY, 0); err == nil {
		os.Stdin = in
	}
}
