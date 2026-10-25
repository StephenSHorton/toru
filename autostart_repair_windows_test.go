//go:build windows

package main

import "testing"

func TestCommandExe(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Users\B\AppData\Local\Programs\Toru\toru.exe --startup`: `C:\Users\B\AppData\Local\Programs\Toru\toru.exe`,
		`"C:\Program Files\Toru\toru.exe" --startup`:                `C:\Program Files\Toru\toru.exe`,
		`"C:\Temp\toru-replay.exe"`:                                 `C:\Temp\toru-replay.exe`,
		`  C:\toru.exe  `:                                           `C:\toru.exe`,
	} {
		if got := commandExe(in); got != want {
			t.Errorf("commandExe(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsInstalledCopy(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\B\AppData\Local`)
	if !isInstalledCopy(`C:\Users\B\AppData\Local\Programs\Toru\toru.exe`) {
		t.Error("installer path should count as the installed copy")
	}
	if isInstalledCopy(`C:\Temp\scratch\toru-replay.exe`) {
		t.Error("a scratch build is not the installed copy")
	}
}
