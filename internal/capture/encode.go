package capture

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
)

// EncodeJPEG encodes img as JPEG at the given quality (1..100). It carries NO
// build tag (pure std-lib) so it compiles on every host.
//
// It is used ONLY for the fast dim-backdrop the webview decodes during a capture
// session — image/jpeg encodes far faster than PNG and decodes fast in the
// webview. It is NEVER used for the final lossless screenshot, which is cropped
// from the in-memory RGBA to PNG via CropImage.
func EncodeJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}
	return buf.Bytes(), nil
}
