//go:build windows

package export

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ---------------------------------------------------------------------------
// Win32 procs. golang.org/x/sys/windows exports NONE of the clipboard / global
// memory APIs, so we lazy-proc them exactly like internal/dpi does for
// SetProcessDpiAwarenessContext. DLLs are process-wide singletons; loading them
// lazily is cheap and thread-safe.
// ---------------------------------------------------------------------------

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procEmptyClipboard             = user32.NewProc("EmptyClipboard")
	procSetClipboardData           = user32.NewProc("SetClipboardData")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procRegisterClipboardFormatW   = user32.NewProc("RegisterClipboardFormatW")

	procGlobalAlloc  = kernel32.NewProc("GlobalAlloc")
	procGlobalLock   = kernel32.NewProc("GlobalLock")
	procGlobalUnlock = kernel32.NewProc("GlobalUnlock")
	procGlobalFree   = kernel32.NewProc("GlobalFree")
	procGlobalSize   = kernel32.NewProc("GlobalSize")

	// RtlMoveMemory(dst, src, len) lets us memcpy to/from a locked HGLOBAL
	// without ever converting the lock's uintptr result to an unsafe.Pointer
	// (which `go vet`'s unsafeptr analyzer rejects). We pass the HGLOBAL
	// pointer as a raw uintptr Call arg and the Go buffer via unsafe.Pointer(&b[0]).
	procRtlMoveMemory = kernel32.NewProc("RtlMoveMemory")
)

// copyToGlobal memcpys src into the locked HGLOBAL block at lockedPtr.
func copyToGlobal(lockedPtr uintptr, src []byte) {
	if len(src) == 0 {
		return
	}
	_, _, _ = procRtlMoveMemory.Call(lockedPtr, uintptr(unsafe.Pointer(&src[0])), uintptr(len(src)))
}

// copyFromGlobal memcpys n bytes out of the locked HGLOBAL block at lockedPtr.
func copyFromGlobal(lockedPtr uintptr, n int) []byte {
	out := make([]byte, n)
	if n == 0 {
		return out
	}
	_, _, _ = procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&out[0])), lockedPtr, uintptr(n))
	return out
}

const (
	// Standard clipboard format IDs.
	cfDIB   = 8
	cfHDROP = 15
	cfDIBV5 = 17

	// GlobalAlloc flags.
	gmemMoveable = 0x0042

	// BITMAPV5HEADER / BITMAPINFOHEADER compression modes.
	biRGB       = 0
	biBitfields = 3

	// LOGCOLORSPACE constants for BITMAPV5HEADER.
	lcsWindowsColorSpace = 0x57696E20 // 'Win ' (little-endian 'Win ')
	lcsGMImages          = 4
)

// registerPNGFormat registers (or fetches) the "PNG" clipboard format ID. The
// ID is stable per-session and identical across every process that registers
// the same string, which is exactly how modern apps exchange lossless,
// alpha-correct PNG via the clipboard.
func registerPNGFormat() (uint32, error) {
	name, err := windows.UTF16PtrFromString("PNG")
	if err != nil {
		return 0, err
	}
	id, _, callErr := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(name)))
	if id == 0 {
		return 0, fmt.Errorf("RegisterClipboardFormatW(PNG): %w", callErr)
	}
	return uint32(id), nil
}

// ---------------------------------------------------------------------------
// Clipboard transaction helpers.
// ---------------------------------------------------------------------------

// openClipboard opens the clipboard owned by no window (hwnd 0). Callers MUST
// pair it with a CloseClipboard.
func openClipboard() error {
	r, _, callErr := procOpenClipboard.Call(0)
	if r == 0 {
		return fmt.Errorf("OpenClipboard: %w", callErr)
	}
	return nil
}

func closeClipboard() {
	_, _, _ = procCloseClipboard.Call()
}

// globalAllocCopy allocates a moveable HGLOBAL of len(data) bytes and copies
// data into it. The handle is returned UNLOCKED and is NOT yet owned by the
// clipboard — the caller transfers ownership via SetClipboardData (success) or
// frees it via GlobalFree (failure).
func globalAllocCopy(data []byte) (uintptr, error) {
	if len(data) == 0 {
		return 0, fmt.Errorf("globalAllocCopy: empty payload")
	}
	h, _, callErr := procGlobalAlloc.Call(gmemMoveable, uintptr(len(data)))
	if h == 0 {
		return 0, fmt.Errorf("GlobalAlloc(%d): %w", len(data), callErr)
	}
	p, _, callErr := procGlobalLock.Call(h)
	if p == 0 {
		_, _, _ = procGlobalFree.Call(h)
		return 0, fmt.Errorf("GlobalLock: %w", callErr)
	}
	// Copy bytes into the locked block.
	copyToGlobal(p, data)
	_, _, _ = procGlobalUnlock.Call(h)
	return h, nil
}

// setClipboardFormat publishes one format. On SUCCESS the system takes
// ownership of h (we must NEVER free it). On FAILURE ownership stays with us
// and we free h to avoid a leak (freeing a successfully-set handle would be a
// double-free / corruption).
func setClipboardFormat(format uint32, h uintptr) error {
	r, _, callErr := procSetClipboardData.Call(uintptr(format), h)
	if r == 0 {
		_, _, _ = procGlobalFree.Call(h)
		return fmt.Errorf("SetClipboardData(format=%d): %w", format, callErr)
	}
	return nil
}

// writeFormat is the alloc+set convenience used inside an open transaction.
func writeFormat(format uint32, data []byte) error {
	h, err := globalAllocCopy(data)
	if err != nil {
		return err
	}
	return setClipboardFormat(format, h)
}

// ---------------------------------------------------------------------------
// Public seam: copyImageToClipboard.
// ---------------------------------------------------------------------------

// copyImageToClipboard publishes the PNG at pngPath as a MULTI-FORMAT image in
// ONE OpenClipboard/EmptyClipboard transaction, in order:
//
//	registered "PNG" -> raw file bytes (lossless, alpha-correct, modern targets)
//	CF_DIBV5         -> 32bpp premultiplied BGRA, top-down (BI_BITFIELDS)
//	CF_DIB           -> 24bpp BGR composited over white, top-down (legacy targets)
func copyImageToClipboard(pngPath string) error {
	pngBytes, err := os.ReadFile(pngPath)
	if err != nil {
		return fmt.Errorf("copyImage: read png: %w", err)
	}
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return fmt.Errorf("copyImage: decode png: %w", err)
	}
	rgba := toRGBA(img)

	pngFmt, err := registerPNGFormat()
	if err != nil {
		return fmt.Errorf("copyImage: %w", err)
	}

	dibv5 := buildDIBV5(rgba)
	dib := buildDIB24(rgba)

	if err := openClipboard(); err != nil {
		return fmt.Errorf("copyImage: %w", err)
	}
	defer closeClipboard()

	if r, _, callErr := procEmptyClipboard.Call(); r == 0 {
		return fmt.Errorf("copyImage: EmptyClipboard: %w", callErr)
	}

	// Order: PNG, CF_DIBV5, CF_DIB. Each handle's ownership transfers to the
	// system on success; on failure writeFormat frees it for us.
	if err := writeFormat(pngFmt, pngBytes); err != nil {
		return fmt.Errorf("copyImage: png: %w", err)
	}
	if err := writeFormat(cfDIBV5, dibv5); err != nil {
		return fmt.Errorf("copyImage: dibv5: %w", err)
	}
	if err := writeFormat(cfDIB, dib); err != nil {
		return fmt.Errorf("copyImage: dib: %w", err)
	}
	return nil
}

// toRGBA returns m as an *image.RGBA (straight alpha), copying only if needed.
func toRGBA(m image.Image) *image.RGBA {
	if r, ok := m.(*image.RGBA); ok {
		return r
	}
	b := m.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			dst.Set(x, y, m.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// buildDIBV5 builds a CF_DIBV5 payload: a 124-byte BITMAPV5HEADER followed by
// 32bpp PREMULTIPLIED BGRA pixels, top-down (negative height). Premultiplying
// avoids bright fringes on transparent edges in targets that honour alpha.
func buildDIBV5(img *image.RGBA) []byte {
	w := img.Rect.Dx()
	h := img.Rect.Dy()
	sizeImage := uint32(w * h * 4)

	var buf bytes.Buffer
	le := func(v any) { _ = binary.Write(&buf, binary.LittleEndian, v) }

	le(uint32(124))                  // bV5Size
	le(int32(w))                     // bV5Width
	le(int32(-h))                    // bV5Height (negative => top-down)
	le(uint16(1))                    // bV5Planes
	le(uint16(32))                   // bV5BitCount
	le(uint32(biBitfields))          // bV5Compression
	le(sizeImage)                    // bV5SizeImage
	le(int32(0))                     // bV5XPelsPerMeter
	le(int32(0))                     // bV5YPelsPerMeter
	le(uint32(0))                    // bV5ClrUsed
	le(uint32(0))                    // bV5ClrImportant
	le(uint32(0x00FF0000))           // bV5RedMask
	le(uint32(0x0000FF00))           // bV5GreenMask
	le(uint32(0x000000FF))           // bV5BlueMask
	le(uint32(0xFF000000))           // bV5AlphaMask
	le(uint32(lcsWindowsColorSpace)) // bV5CSType
	// bV5Endpoints: CIEXYZTRIPLE = 9 * uint32 = 36 zero bytes.
	for i := 0; i < 9; i++ {
		le(uint32(0))
	}
	le(uint32(0))           // bV5GammaRed
	le(uint32(0))           // bV5GammaGreen
	le(uint32(0))           // bV5GammaBlue
	le(uint32(lcsGMImages)) // bV5Intent
	le(uint32(0))           // bV5ProfileData
	le(uint32(0))           // bV5ProfileSize
	le(uint32(0))           // bV5Reserved

	// Pixels: premultiplied BGRA, top-down row order (matches negative height).
	pix := make([]byte, 0, sizeImage)
	for y := 0; y < h; y++ {
		row := img.Pix[y*img.Stride : y*img.Stride+w*4]
		for x := 0; x < w*4; x += 4 {
			r := uint32(row[x])
			g := uint32(row[x+1])
			b := uint32(row[x+2])
			a := uint32(row[x+3])
			// Premultiply each channel by alpha.
			pr := byte(r * a / 255)
			pg := byte(g * a / 255)
			pb := byte(b * a / 255)
			pix = append(pix, pb, pg, pr, byte(a)) // BGRA
		}
	}
	buf.Write(pix)
	return buf.Bytes()
}

// buildDIB24 builds a CF_DIB payload: a 40-byte BITMAPINFOHEADER followed by
// 24bpp BGR pixels COMPOSITED OVER WHITE, top-down (negative height), with each
// row padded to a 4-byte boundary. Compositing over white means transparency
// pastes as white (not black) into legacy CF_DIB-only targets.
func buildDIB24(img *image.RGBA) []byte {
	w := img.Rect.Dx()
	h := img.Rect.Dy()
	stride := (w*3 + 3) &^ 3 // 4-byte-aligned row stride

	var buf bytes.Buffer
	le := func(v any) { _ = binary.Write(&buf, binary.LittleEndian, v) }

	le(uint32(40))         // biSize
	le(int32(w))           // biWidth
	le(int32(-h))          // biHeight (negative => top-down)
	le(uint16(1))          // biPlanes
	le(uint16(24))         // biBitCount
	le(uint32(biRGB))      // biCompression
	le(uint32(stride * h)) // biSizeImage
	le(int32(0))           // biXPelsPerMeter
	le(int32(0))           // biYPelsPerMeter
	le(uint32(0))          // biClrUsed
	le(uint32(0))          // biClrImportant

	rowBuf := make([]byte, stride)
	for y := 0; y < h; y++ {
		src := img.Pix[y*img.Stride : y*img.Stride+w*4]
		for i := range rowBuf {
			rowBuf[i] = 0
		}
		di := 0
		for x := 0; x < w*4; x += 4 {
			r := uint32(src[x])
			g := uint32(src[x+1])
			b := uint32(src[x+2])
			a := uint32(src[x+3])
			// Composite src over white: out = src*a + 255*(255-a), /255.
			inv := 255 - a
			cr := byte((r*a + 255*inv) / 255)
			cg := byte((g*a + 255*inv) / 255)
			cb := byte((b*a + 255*inv) / 255)
			rowBuf[di] = cb // BGR
			rowBuf[di+1] = cg
			rowBuf[di+2] = cr
			di += 3
		}
		buf.Write(rowBuf)
	}
	return buf.Bytes()
}

// ---------------------------------------------------------------------------
// copyFileToClipboard: CF_HDROP (file-drop reference). This is the video copy
// path, but we implement it here behind the frozen seam so both halves share it.
// ---------------------------------------------------------------------------

// copyFileToClipboard publishes a single file path as CF_HDROP so it pastes
// into Explorer / Discord / Slack / Teams as a file reference.
//
// Layout: a 20-byte DROPFILES header (pFiles offset = 20, fWide = 1) followed
// by the UTF-16LE path and a DOUBLE NUL terminator (the path list end marker).
func copyFileToClipboard(path string) error {
	abs := path
	wpath, err := windows.UTF16FromString(abs) // includes a single trailing NUL
	if err != nil {
		return fmt.Errorf("copyFile: encode path: %w", err)
	}

	// DROPFILES: { DWORD pFiles; POINT pt; BOOL fNC; BOOL fWide; } = 20 bytes.
	const dropfilesSize = 20
	// Payload = header + path (UTF-16, already NUL-terminated) + one extra NUL
	// (uint16) to make the list double-NUL terminated.
	payload := make([]byte, dropfilesSize+len(wpath)*2+2)

	binary.LittleEndian.PutUint32(payload[0:], dropfilesSize) // pFiles
	binary.LittleEndian.PutUint32(payload[4:], 0)             // pt.x
	binary.LittleEndian.PutUint32(payload[8:], 0)             // pt.y
	binary.LittleEndian.PutUint32(payload[12:], 0)            // fNC
	binary.LittleEndian.PutUint32(payload[16:], 1)            // fWide = TRUE

	// Copy UTF-16LE path right after the header.
	off := dropfilesSize
	for _, u := range wpath {
		binary.LittleEndian.PutUint16(payload[off:], u)
		off += 2
	}
	// payload already zero-filled, so the final extra uint16 is the second NUL.

	if err := openClipboard(); err != nil {
		return fmt.Errorf("copyFile: %w", err)
	}
	defer closeClipboard()

	if r, _, callErr := procEmptyClipboard.Call(); r == 0 {
		return fmt.Errorf("copyFile: EmptyClipboard: %w", callErr)
	}
	if err := writeFormat(cfHDROP, payload); err != nil {
		return fmt.Errorf("copyFile: hdrop: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// readClipboardImage: pull an image off the clipboard as a base64 data URL.
// ---------------------------------------------------------------------------

// readClipboardImage returns a base64 data URL for an image on the clipboard.
// It prefers the registered "PNG" format (lossless, alpha); otherwise it wraps
// a CF_DIBV5 / CF_DIB DIB in a BMP file header and returns image/bmp. Returns
// ("", nil) when no image is present.
func readClipboardImage() (string, error) {
	if err := openClipboard(); err != nil {
		return "", fmt.Errorf("readClipboard: %w", err)
	}
	defer closeClipboard()

	// 1) Registered PNG.
	if pngFmt, err := registerPNGFormat(); err == nil {
		if data, ok := readClipboardFormat(pngFmt); ok && len(data) > 0 {
			return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data), nil
		}
	}

	// 2) CF_DIBV5 / CF_DIB -> wrap in a BMP file header.
	for _, fmtID := range []uint32{cfDIBV5, cfDIB} {
		if data, ok := readClipboardFormat(fmtID); ok && len(data) >= 4 {
			bmp := dibToBMP(data)
			return "data:image/bmp;base64," + base64.StdEncoding.EncodeToString(bmp), nil
		}
	}
	return "", nil
}

// readClipboardFormat copies the bytes of one clipboard format out of its
// HGLOBAL. The returned bool is false when the format is absent. The clipboard
// must already be open; the system owns the handle, so we only lock/unlock.
func readClipboardFormat(format uint32) ([]byte, bool) {
	if r, _, _ := procIsClipboardFormatAvailable.Call(uintptr(format)); r == 0 {
		return nil, false
	}
	h, _, _ := procGetClipboardData.Call(uintptr(format))
	if h == 0 {
		return nil, false
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return nil, false
	}
	defer func() { _, _, _ = procGlobalUnlock.Call(h) }()

	size, _, _ := procGlobalSize.Call(h)
	if size == 0 {
		return nil, false
	}
	return copyFromGlobal(p, int(size)), true
}

// dibToBMP prepends a 14-byte BITMAPFILEHEADER to a DIB (BITMAPINFOHEADER or
// BITMAPV5HEADER + pixels) so it becomes a standalone .bmp. The pixel-data
// offset is the header size plus any color masks/table, derived from biSize.
func dibToBMP(dib []byte) []byte {
	// biSize is the first DWORD of the info header.
	headerSize := binary.LittleEndian.Uint32(dib[0:4])
	bitCount := uint16(0)
	if len(dib) >= 16 {
		bitCount = binary.LittleEndian.Uint16(dib[14:16])
	}
	compression := uint32(0)
	if len(dib) >= 20 {
		compression = binary.LittleEndian.Uint32(dib[16:20])
	}

	// Offset to pixel bits = 14 (file header) + info header + color masks/table.
	pixOffset := uint32(14) + headerSize
	// BITMAPINFOHEADER (40) with BI_BITFIELDS carries three trailing DWORD masks.
	if headerSize == 40 && compression == biBitfields {
		pixOffset += 12
	}
	// Palettes for <=8bpp are uncommon from the clipboard; assume 16/24/32bpp.
	_ = bitCount

	fileSize := uint32(14) + uint32(len(dib))
	out := make([]byte, 14+len(dib))
	out[0] = 'B'
	out[1] = 'M'
	binary.LittleEndian.PutUint32(out[2:], fileSize)
	binary.LittleEndian.PutUint32(out[6:], 0) // reserved
	binary.LittleEndian.PutUint32(out[10:], pixOffset)
	copy(out[14:], dib)
	return out
}
