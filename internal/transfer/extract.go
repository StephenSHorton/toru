package transfer

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// errZipSlip is returned for any archive entry that would land outside root.
var errZipSlip = errors.New("archive entry escapes the destination folder")

// errArchiveTooBig is returned when the archive inflates past the budget
// derived from the size the sender announced in its offer.
var errArchiveTooBig = errors.New("folder unpacks to far more than the sender announced")

// extractBudgetSlack is the fixed allowance on top of the announced size, so a
// tiny folder with imprecise accounting (directory entries, zip headers) is
// never refused and a hostile offer announcing 0 bytes still gets a hard cap.
const extractBudgetSlack = 16 << 20

// extractBudget is the most bytes extractZip will write for an archive whose
// offer announced announced uncompressed bytes: +10 % and a fixed slack. The
// stream itself is bounded by the announced zip size via the transfer, but the
// deflate ratio is the peer's to choose; this is the decompression-bomb cap.
func extractBudget(announced int64) int64 {
	if announced < 0 {
		announced = 0
	}
	return announced + announced/10 + extractBudgetSlack
}

// extractZip unpacks the archive at zipPath into root (which must already exist
// and be ours — see claimDir), writing at most budget bytes in total. The
// archive comes from the peer and is untrusted, so every entry name goes
// through safeZipRel before it touches the filesystem; one bad entry fails the
// whole extraction (the caller removes root). Symlink entries are skipped: a
// link pointing out of root followed by a later entry through it is the classic
// second zip-slip. Nothing else (mtime, mode) is preserved — Windows does not
// care and the sender's bits are not ours.
func extractZip(ctx context.Context, zipPath, root string, budget int64) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer func() { _ = zr.Close() }()

	remaining := budget
	for _, f := range zr.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := safeZipRel(root, f.Name)
		if err != nil {
			return fmt.Errorf("%w: %q", err, f.Name)
		}
		if rel == "" {
			continue // the root itself ("./" or "")
		}
		dst := filepath.Join(root, rel)
		mode := f.Mode()
		switch {
		case mode&os.ModeSymlink != 0:
			continue
		case f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/"):
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			continue
		case !mode.IsRegular() && mode&os.ModeType != 0:
			continue // devices, sockets, …
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		n, err := writeZipEntry(f, dst, remaining)
		remaining -= n
		if err != nil {
			return err
		}
	}
	return nil
}

// writeZipEntry copies one archive member to dst, writing at most limit bytes;
// an entry that inflates past the limit fails with errArchiveTooBig. The
// header's UncompressedSize64 is advisory (the peer wrote it), so the cap is
// enforced on the actual bytes. Returns the bytes written.
func writeZipEntry(f *zip.File, dst string, limit int64) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer func() { _ = rc.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	if limit < 0 {
		limit = 0
	}
	// Read one byte past the limit so "exactly at the limit" and "over it" are
	// told apart without trusting the header.
	n, err := io.Copy(out, io.LimitReader(rc, limit+1))
	if err != nil {
		_ = out.Close()
		return n, err
	}
	if n > limit {
		_ = out.Close()
		return n, errArchiveTooBig
	}
	return n, out.Close()
}

// safeZipRel validates an archive entry name and returns the relative path (OS
// separators) it may be written to under root, or errZipSlip. Rejected:
// absolute paths and anything starting with a slash (filepath.IsAbs("/evil") is
// FALSE on Windows, so the prefix check is explicit), drive-qualified names
// ("C:\evil", "C:evil" — VolumeName catches both), NUL bytes, and any ".."
// segment. Backslashes are treated as separators because that is what the
// Windows filesystem would do with them anyway. The final gate re-derives the
// path relative to root after cleaning, so no spelling of ".." slips through.
// Each surviving segment is also run through the same character scrub as
// safeName, so an archive built on Linux with "a:b" in a name still lands.
func safeZipRel(root, name string) (string, error) {
	if strings.ContainsRune(name, 0) {
		return "", errZipSlip
	}
	slashed := strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(slashed, "/") || filepath.IsAbs(slashed) || filepath.VolumeName(slashed) != "" || filepath.VolumeName(name) != "" {
		return "", errZipSlip
	}
	var parts []string
	for _, seg := range strings.Split(slashed, "/") {
		switch seg {
		case "", ".":
			continue
		case "..":
			return "", errZipSlip
		}
		seg = scrubComponent(seg)
		if seg == "" {
			continue
		}
		parts = append(parts, seg)
	}
	if len(parts) == 0 {
		return "", nil
	}
	rel := filepath.Join(parts...)
	// Belt and braces: whatever Join/Clean did, the result must still live under root.
	back, err := filepath.Rel(root, filepath.Join(root, rel))
	if err != nil || back == ".." || strings.HasPrefix(back, ".."+string(filepath.Separator)) || filepath.IsAbs(back) {
		return "", errZipSlip
	}
	return rel, nil
}

// scrubComponent applies safeName's character rules to one path segment of an
// archive entry, without the fallback (an empty result just drops the segment).
func scrubComponent(seg string) string {
	var b strings.Builder
	for _, r := range seg {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`<>:"/\|?*`, r) {
			continue
		}
		b.WriteRune(r)
	}
	s := strings.TrimRight(strings.TrimSpace(b.String()), ". ")
	if s == "" {
		return ""
	}
	s = clampName(s)
	if isReservedDeviceName(s) {
		s = "_" + s
	}
	return s
}
