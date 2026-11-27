package boardshare

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/board"
)

// folder is one Toru's own pictures, for tests (both test Torus would
// otherwise share board's one ImagesDir).
type folder struct {
	mu   sync.Mutex
	pics map[string][]byte
}

func (f *folder) pictures() pictures {
	return pictures{
		has: func(a string) bool { f.mu.Lock(); defer f.mu.Unlock(); _, ok := f.pics[a]; return ok },
		read: func(a string) ([]byte, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if d, ok := f.pics[a]; ok {
				return d, nil
			}
			return nil, errors.New("no such picture")
		},
		import_: func(d []byte) (string, error) {
			sum := sha256.Sum256(d)
			name := hex.EncodeToString(sum[:]) + ".png"
			f.mu.Lock()
			f.pics[name] = d
			f.mu.Unlock()
			return name, nil
		},
	}
}

func testPNG(t *testing.T, w, h int) []byte {
	rng := rand.New(rand.NewSource(int64(w)))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(x ^ y), 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A picture placed on either side reaches the other by name, in parts
// when it's big, and only a picture a shared board shows is sent.
func TestPicturesCross(t *testing.T) {
	fast(t)
	h := newHub()
	aStore, bStore, a, b, id, local := shareAndJoin(t, h)
	fa, fb := &folder{pics: map[string][]byte{}}, &folder{pics: map[string][]byte{}}
	a.mu.Lock()
	a.pics = fa.pictures()
	a.mu.Unlock()
	b.mu.Lock()
	b.pics = fb.pictures()
	b.mu.Unlock()

	// Stephen's agent places a big picture (several parts).
	big := testPNG(t, 300, 300)
	if len(big) < 2*partSize {
		t.Fatalf("test picture is only %d bytes", len(big))
	}
	assetA, _ := fa.pictures().import_(big)
	img := func(asset string, x float64) board.Block {
		return board.Block{Type: "image", X: x, Y: 0, W: 300, H: 300, Props: json.RawMessage(`{"asset":"` + asset + `"}`)}
	}
	if _, err := aStore.Apply(id, board.Op{Source: board.Agent("Claude Code"), Blocks: []board.Block{img(assetA, 0)}}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Stephen's picture on Blake's Toru", func() bool { return fb.pictures().has(assetA) })
	if got, _ := fb.pictures().read(assetA); !bytes.Equal(got, big) {
		t.Fatal("the picture arrived changed")
	}

	// Blake's agent places a picture only Blake's Toru has.
	small := testPNG(t, 20, 20)
	assetB, _ := fb.pictures().import_(small)
	if _, err := bStore.Apply(local, board.Op{Source: board.Agent("Claude Code"), Blocks: []board.Block{img(assetB, 400)}}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Blake's picture on Stephen's Toru", func() bool { return fa.pictures().has(assetB) })

	// A picture no shared board shows isn't sent to whoever asks.
	secret := testPNG(t, 8, 8)
	assetS, _ := fa.pictures().import_(secret)
	b.mu.Lock()
	b.images[assetS] = &imageWant{from: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	b.mu.Unlock()
	b.send("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", message{T: tImgReq, Asset: assetS})
	eventually(t, "a few tries", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.sent > 0
	})
	for range 20 {
		if fb.pictures().has(assetS) {
			t.Fatal("a picture no shared board shows was sent")
		}
		sleepTick()
	}
}

func sleepTick() { time.Sleep(tickEvery * 3) }
