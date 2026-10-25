package hato

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// fallbackName is used when nothing printable survives sanitising.
const fallbackName = "received"

// maxNameBytes caps one path component. NTFS allows 255 UTF-16 units and the
// classic Win32 path limit is 260 for the whole path; 200 bytes leaves room for
// a deep Save-to folder and the " (N)" collision suffix, and keeps a peer from
// offering a name that only fails at the final rename.
const maxNameBytes = 200

// maxExtBytes is the longest suffix clampName treats as an extension worth
// preserving; anything longer is just part of the name.
const maxExtBytes = 32

// maxCollisionSuffix bounds the "name (N)" search so a hostile or absurd
// directory can't spin us forever.
const maxCollisionSuffix = 10000

// safeName turns the sender's offered name into a single safe path component.
// The offer is untrusted: it may carry directories ("../../x"), a Windows drive
// ("C:\x"), characters NTFS rejects (<>:"/\|?*), control bytes, or a trailing
// dot/space that Win32 silently strips (which would make OutPath name a file
// that doesn't exist and break "Show in Explorer"). Reserved device names (CON,
// NUL, COM1…) get a leading underscore — "<dir>\NUL" would otherwise write to the
// null device. Empty after all that → "received".
func safeName(offered string) string {
	n := strings.ReplaceAll(offered, "\\", "/")
	n = filepath.Base(filepath.FromSlash(n))
	if n == "." || n == ".." || n == string(filepath.Separator) {
		n = ""
	}
	var b strings.Builder
	for _, r := range n {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`<>:"/\|?*`, r) {
			continue
		}
		b.WriteRune(r)
	}
	n = strings.TrimSpace(b.String())
	n = strings.TrimRight(n, ". ")
	if n == "" {
		return fallbackName
	}
	n = clampName(n)
	if isReservedDeviceName(n) {
		n = "_" + n
	}
	return n
}

// clampName shortens a sanitised component to maxNameBytes, cutting the stem
// on a rune boundary and keeping the extension so "report.pdf" stays a PDF.
func clampName(n string) string {
	if len(n) <= maxNameBytes {
		return n
	}
	stem, ext := splitExt(n)
	if len(ext) > maxExtBytes {
		stem, ext = n, ""
	}
	keep := maxNameBytes - len(ext)
	b := []byte(stem)
	for len(b) > keep {
		_, size := utf8.DecodeLastRune(b)
		b = b[:len(b)-size]
	}
	stem = strings.TrimRight(string(b), ". ")
	if stem == "" {
		stem = fallbackName
	}
	return stem + ext
}

// isReservedDeviceName reports whether the stem (before the first dot) is one of
// the Win32 device names that cannot be used as a file name in any directory.
func isReservedDeviceName(name string) bool {
	stem, _, _ := strings.Cut(name, ".")
	stem = strings.ToUpper(strings.TrimSpace(stem))
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) {
		return stem[3] >= '0' && stem[3] <= '9'
	}
	return false
}

// splitExt splits "report.final.pdf" into ("report.final", ".pdf") and keeps a
// dotfile like ".bashrc" whole so the suffix lands as ".bashrc (2)", not " (2).bashrc".
func splitExt(name string) (stem, ext string) {
	ext = filepath.Ext(name)
	stem = strings.TrimSuffix(name, ext)
	if stem == "" {
		return name, ""
	}
	return stem, ext
}

// candidateName returns name for i == 1 and "stem (i)ext" after that.
func candidateName(name string, i int) string {
	if i <= 1 {
		return name
	}
	stem, ext := splitExt(name)
	return fmt.Sprintf("%s (%d)%s", stem, i, ext)
}

// claimFile reserves a never-before-existing file named name (or "name (2)",
// "name (3)"…) in dir and returns its path. It CREATES the file with O_EXCL
// rather than Stat-ing: os.Rename on Windows replaces an existing target
// (MOVEFILE_REPLACE_EXISTING), so "never overwrite" has to rest on an atomic
// claim, not a check-then-rename. The caller renames its .part over the empty
// placeholder (or removes the placeholder on failure).
func claimFile(dir, name string) (string, error) {
	for i := 1; i <= maxCollisionSuffix; i++ {
		p := filepath.Join(dir, candidateName(name, i))
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_ = f.Close()
			return p, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("too many files named %q in %s", name, dir)
}

// claimDir is claimFile for a folder: os.Mkdir (not MkdirAll) so an existing
// folder reports ErrExist and we move on to the next suffix.
func claimDir(dir, name string) (string, error) {
	for i := 1; i <= maxCollisionSuffix; i++ {
		p := filepath.Join(dir, candidateName(name, i))
		err := os.Mkdir(p, 0o755)
		if err == nil {
			return p, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("too many folders named %q in %s", name, dir)
}
