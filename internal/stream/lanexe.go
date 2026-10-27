package stream

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/StephenSHorton/toru/internal/lan/api"
)

// lanexe.go finds the LAN helper, toru-lan.exe, which the installer puts
// next to toru.exe with wintun.dll beside it. In a dev build the exe comes
// from the go build output and the DLL from build/windows/wintun, so it is
// copied next to the exe (after checking it is the vendored, signed one).

// lanExeDirs is where toru-lan.exe may be, in order: next to Toru, the
// per-user install, then dev build outputs under the working directory.
func lanExeDirs(exeDir, localAppData, cwd string) []string {
	var dirs []string
	add := func(d string) {
		if d == "" {
			return
		}
		for _, have := range dirs {
			if filepath.Clean(have) == filepath.Clean(d) {
				return
			}
		}
		dirs = append(dirs, d)
	}
	add(exeDir)
	if localAppData != "" {
		add(filepath.Join(localAppData, "Programs", "Toru"))
	}
	if cwd != "" {
		add(cwd)
		add(filepath.Join(cwd, "bin"))
		add(filepath.Join(cwd, "build", "bin"))
	}
	return dirs
}

// wintunDirs is where a dev build finds the vendored wintun.dll.
func wintunDirs(exeDir, cwd string) []string {
	var dirs []string
	for _, base := range []string{cwd, exeDir, filepath.Dir(exeDir)} {
		if base != "" {
			dirs = append(dirs, filepath.Join(base, "build", "windows", "wintun"))
		}
	}
	return dirs
}

// findLanExe returns the toru-lan.exe to run, with wintun.dll next to it.
func findLanExe() (string, error) {
	exe, _ := os.Executable()
	exeDir := ""
	if exe != "" {
		exeDir = filepath.Dir(exe)
	}
	cwd, _ := os.Getwd()
	var found string
	for _, d := range lanExeDirs(exeDir, os.Getenv("LOCALAPPDATA"), cwd) {
		p := filepath.Join(d, api.LanExe)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			found = p
			break
		}
	}
	if found == "" {
		return "", fmt.Errorf("%s is missing from Toru's folder; reinstall Toru", api.LanExe)
	}
	if err := prepareWintun(found, wintunDirs(exeDir, cwd)); err != nil {
		return "", err
	}
	return found, nil
}

// prepareWintun makes sure wintun.dll is next to lanExe, copying the
// vendored one from searchDirs if it isn't. Only a file with the pinned
// hash is copied.
func prepareWintun(lanExe string, searchDirs []string) error {
	dst := filepath.Join(filepath.Dir(lanExe), api.WintunDLL)
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	for _, d := range searchDirs {
		src := filepath.Join(d, api.WintunDLL)
		sum, err := fileSHA256(src)
		if err != nil {
			continue
		}
		if sum != api.WintunSHA256 {
			return fmt.Errorf("%s isn't the expected Wintun build", src)
		}
		return copyFile(src, dst)
	}
	return errors.New("wintun.dll is missing from Toru's folder; reinstall Toru")
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
