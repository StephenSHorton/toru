// Package selftest is the one-time, elevated end-to-end check of Toru LAN on
// a real PC: install, service, pipe, adapter configuration, Private
// category, firewall rules, the broadcast go/no-go, and uninstall. It is a
// go test behind the lanselftest build tag and is run by
// scripts/lan-selftest.ps1, which handles elevation, Radmin VPN and a
// temporary firewall allowance for the test binary. Nothing here runs in an
// ordinary go test.
package selftest
