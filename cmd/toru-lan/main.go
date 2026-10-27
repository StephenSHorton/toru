// Command toru-lan is Toru LAN's one machine-wide binary. Run elevated, it
// installs, updates and uninstalls itself as the ToruLan service; started by
// the service manager, it is that service: the pipe server, the per-user
// WireGuard keys, the Wintun adapter and the LAN node. Design: docs/lan.md,
// "Elevation, the service and installer changes".
//
//	toru-lan.exe install     copy to %ProgramFiles%\Toru\lan, register the service, firewall rules
//	toru-lan.exe uninstall   remove all of it again
//	toru-lan.exe version     print the build version
//	toru-lan.exe debug       run the service in this console (elevated; for development)
package main

import (
	"fmt"
	"os"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func usage() {
	fmt.Fprintln(os.Stderr, "usage: toru-lan.exe install | uninstall | version | debug")
}

func main() {
	os.Exit(run(os.Args[1:]))
}
