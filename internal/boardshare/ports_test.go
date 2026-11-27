package boardshare

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Sharing opens no port and no socket of its own: boardshare imports no
// networking package at all, and everything it sends goes through the Room
// it's given (the room Call and LAN use).
func TestNoNetworkingOfItsOwn(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range src.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if path == "net" || strings.HasPrefix(path, "net/") || strings.Contains(path, "mqtt") || strings.Contains(path, "webrtc") || strings.Contains(path, "agentctl") {
				t.Errorf("%s imports %s: sharing must only use the room it's given", f, path)
			}
		}
	}
}

// listening is the TCP and UDP ports this process listens on (Windows'
// netstat, by process id).
func listening(t *testing.T) []string {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("reads netstat -ano")
	}
	out, err := exec.Command("netstat", "-ano").Output()
	if err != nil {
		t.Fatal(err)
	}
	pid := strconv.Itoa(os.Getpid())
	var ports []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[len(f)-1] != pid {
			continue
		}
		switch {
		case f[0] == "TCP" && len(f) == 5 && f[3] == "LISTENING":
			ports = append(ports, "TCP "+f[1])
		case f[0] == "UDP":
			ports = append(ports, "UDP "+f[1])
		}
	}
	return ports
}
