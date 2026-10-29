package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"time"

	"github.com/StephenSHorton/toru/internal/agentctl"
)

var (
	timeZero time.Time
	timePast = time.Unix(1, 0)
)

// Env is what the entry points need from the outside world, so tests can
// run them against a fake pipe.
type Env struct {
	// Dial connects to the running Toru's agent pipe.
	Dial func(context.Context) (net.Conn, error)
	// Enabled reports the "Let AI agents and scripts control Toru" setting,
	// to tell "turned off" apart from "not running".
	Enabled func() bool
	// Start launches Toru (no window when it boots in the tray).
	Start func() error

	Stdin          io.Reader
	Stdout, Stderr io.Writer

	// Version is Toru's, reported to MCP clients.
	Version string
}

// Main runs `toru.exe mcp` or `toru.exe ctl ...` (args[0] is "mcp" or
// "ctl") and returns the process exit code.
func Main(args []string, env Env) int {
	if len(args) == 0 {
		return usage(env.Stderr)
	}
	switch args[0] {
	case "mcp":
		if err := RunMCP(context.Background(), env); err != nil {
			sayln(env.Stderr, "toru mcp:", err)
			return 1
		}
		return 0
	case "ctl":
		return RunCtl(context.Background(), args[1:], env)
	}
	return usage(env.Stderr)
}

// Exit codes for ctl.
const (
	exitOK          = 0
	exitCommand     = 1 // the command ran and failed
	exitUsage       = 2
	exitUnreachable = 3 // Toru isn't running, or agent control is off
)

func usage(w io.Writer) int {
	say(w, `Usage:
  toru.exe mcp                          MCP server on stdin/stdout
  toru.exe ctl list                     list every command (JSON)
  toru.exe ctl <command> [--json '{}']  run a command, print its result (JSON)
  toru.exe ctl app.start                start Toru and wait until it answers

--json takes the command's input object; "--json -" reads it from stdin
(Windows PowerShell 5.1 drops the quotes inside a --json argument, so pipe
the JSON in there: '{"t":12.5}' | toru.exe ctl editor.split --json -).
Exit codes: 0 ok, 1 the command failed, 2 usage, 3 Toru not reachable.
`)
	return exitUsage
}

// unreachableMessage explains why the pipe isn't there and what to do.
func unreachableMessage(env Env, how string) string {
	if env.Enabled != nil && !env.Enabled() {
		return "Agent control is off. In Toru, open Settings > General and turn on " +
			`"Let AI agents and scripts control Toru".`
	}
	return "Toru isn't running. Start it, or " + how + "."
}

// The app.start command is the clients' own: it has to work while Toru
// (and so the registry) isn't running.
const appStartName = "app.start"

var appStartInfo = CommandInfo{
	Name:        appStartName,
	Description: "Start Toru if it isn't running and wait until it answers. Agent control must be turned on in Settings > General.",
	Input:       json.RawMessage(`{"type":"object","properties":{}}`),
}

// appStart starts Toru unless it already answers, then waits for the pipe.
func appStart(ctx context.Context, c *Client, env Env) (any, error) {
	if _, err := c.List(ctx); err == nil {
		return map[string]any{"running": true, "started": false}, nil
	}
	if env.Enabled != nil && !env.Enabled() {
		return nil, fmt.Errorf("%w: agent control is off", errUnreachable)
	}
	if env.Start == nil {
		return nil, errors.New("can't start Toru from here")
	}
	if err := env.Start(); err != nil {
		return nil, fmt.Errorf("start Toru: %w", err)
	}
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		if _, err := c.List(wait); err == nil {
			return map[string]any{"running": true, "started": true}, nil
		}
		select {
		case <-wait.Done():
			// Toru is single-instance: a running copy without agent control (an
			// older version) takes the launch over and never answers.
			return nil, errors.New("started Toru, but it didn't answer within 30s. If Toru was already running, " +
				"it may be a version without agent control: update it, or quit it from the tray and try again")
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// DefaultEnv is the real environment: this user's agent pipe, the saved
// setting, and starting this same executable in the tray.
func DefaultEnv(version string) Env {
	return Env{
		Version: version,
		Dial:    DialToru,
		Enabled: agentctl.Enabled,
		Start:   startToru,
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	}
}

// startToru launches this executable with --startup (silent in the tray,
// as at login) and doesn't wait for it.
func startToru() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "--startup")
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Output helpers: a failed write to stdout or stderr has nowhere to be
// reported.
func sayln(w io.Writer, a ...any)               { _, _ = fmt.Fprintln(w, a...) }
func sayf(w io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
func say(w io.Writer, a ...any)                 { _, _ = fmt.Fprint(w, a...) }
