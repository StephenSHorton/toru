package update

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

// The update folder holds one directory per download (v<version>-<random>,
// downloadVerified) and, after a silent install that couldn't replace
// toru.exe, the installer's report (installFailedFile).
//
// Each download gets its own directory because an installer that is still
// running keeps its own file open: a fixed path made every later download of
// that version fail with "being used by another process".
var updateDir = func() string { return filepath.Join(os.TempDir(), "toru-update") }

// installFailedFile is what the installer writes into the update folder when
// a silent install gives up (build/windows/nsis/project.nsi,
// TORU_INSTALL_FAILED_FILE): one line, "<version>\t<reason>".
const installFailedFile = "install-failed.txt"

// cleanDownloads removes earlier downloads from the update folder, best
// effort: an installer that is still running can't be removed and is left.
func cleanDownloads(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Name() == installFailedFile {
			continue
		}
		_ = os.RemoveAll(filepath.Join(dir, e.Name()))
	}
}

// takeInstallFailure reads and removes the installer's failure report. It
// returns the version that didn't install and why; ok is false when there is
// no report, or it is stale (this Toru is that version or newer, so a later
// install worked).
func takeInstallFailure(dir, current string) (version, reason string, ok bool) {
	p := filepath.Join(dir, installFailedFile)
	b, err := os.ReadFile(p)
	if err != nil {
		return "", "", false
	}
	_ = os.Remove(p)
	version, reason, _ = strings.Cut(strings.TrimSpace(string(b)), "\t")
	version = strings.TrimSpace(version)
	if version == "" || !isNewer(version, current) {
		return "", "", false
	}
	return version, strings.TrimSpace(reason), true
}

// reportInstallFailure turns the installer's failure report, if there is
// one, into the error status Settings > About shows. It reports whether it
// did, so AutoUpdate doesn't download the same update straight away and hide
// the error behind a "ready" banner that would fail the same way.
func (s *UpdateService) reportInstallFailure() bool {
	v, why, ok := takeInstallFailure(updateDir(), s.current)
	if !ok {
		return false
	}
	if why == "" {
		why = "toru.exe was in use"
	}
	msg := "The update to v" + v + " didn't install: " + why + ". Check for Updates to try again."
	log.Printf("toru: update: %s", msg)
	s.setStatus(func(st *Status) {
		*st = Status{Phase: PhaseError, Version: v, Error: msg, InstallFailed: true}
	})
	return true
}

// removeOldExes deletes the toru.exe.old* files beside exe. The installer
// moves a toru.exe that "toru.exe mcp" processes still run from aside to
// toru.exe.old (or .old.1 ... .old.9); once those processes are gone, the
// files can go. Ones still running are left for a later start.
func removeOldExes(exe string) {
	dir, base := filepath.Split(exe)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := strings.ToLower(base) + ".old"
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(strings.ToLower(e.Name()), prefix) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
