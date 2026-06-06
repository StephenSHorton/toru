package overlay

import (
	"image"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/StephenSHorton/toru/internal/capture"
)

// swapImagesForTest installs a fresh in-memory frozen set exactly like
// BeginSession's step (3): drop the old refs, install the new maps, publish a
// pending session — all under one write lock. It exists so the race test can drive
// engage-style swaps WITHOUT a real screen grab or a running Wails app.
func (s *OverlayService) swapImagesForTest(frozen map[int]*image.RGBA, jpegs map[int][]byte, sessions map[int]MonitorSession) {
	s.mu.Lock()
	s.dropImagesLocked()
	s.frozenImg = frozen
	s.jpegCache = jpegs
	s.pending = sessions
	s.mu.Unlock()
}

// TestServiceConcurrentImageAccess locks in the mutex discipline for the in-memory
// frozen-image / backdrop-JPEG / pending maps: engage-style swaps (write lock) must
// be safe against ShotMiddleware backdrop reads, RequestEngage pulls, and the
// crop-temp tracker, all concurrently. Run with -race (CI does: `go test -race`).
//
// No real screen grab is needed — *image.RGBA + pre-encoded JPEG bytes are injected
// directly — so this is host-independent and fast.
func TestServiceConcurrentImageAccess(t *testing.T) {
	const monitors = 3

	s := NewService(nil)
	mw := s.ShotMiddleware()
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot) // a non-__shot request falls through to next
	}))

	makeSet := func(seed byte) (map[int]*image.RGBA, map[int][]byte, map[int]MonitorSession) {
		frozen := make(map[int]*image.RGBA, monitors)
		jpegs := make(map[int][]byte, monitors)
		sessions := make(map[int]MonitorSession, monitors)
		for id := 0; id < monitors; id++ {
			img := image.NewRGBA(image.Rect(0, 0, 8, 8))
			img.Pix[0] = seed
			frozen[id] = img
			jpg, err := capture.EncodeJPEG(img, 85)
			if err != nil {
				t.Fatalf("encode jpeg: %v", err)
			}
			jpegs[id] = jpg
			sessions[id] = MonitorSession{MonitorID: id, IsPrimary: id == 0}
		}
		return frozen, jpegs, sessions
	}

	// Seed an initial set so reads have something to find from the start.
	f0, j0, ss0 := makeSet(1)
	s.swapImagesForTest(f0, j0, ss0)

	const iters = 200
	var wg sync.WaitGroup

	// Writer: engage-style swaps.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			f, j, ss := makeSet(byte(i))
			s.swapImagesForTest(f, j, ss)
		}
	}()

	// Reader A: ShotMiddleware backdrop fetches (RLock + Write the bytes out).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			id := i % monitors
			req := httptest.NewRequest(http.MethodGet, "/__shot/"+strconv.Itoa(id)+"?g="+strconv.Itoa(i), nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}
	}()

	// Reader B: RequestEngage pulls (RLock read of s.pending).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = s.RequestEngage(i % monitors)
		}
	}()

	// Reader/Writer C: crop-temp tracking (Lock append + Lock take).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			s.trackCropTemp("ignored-" + strconv.Itoa(i) + ".png")
			if i%10 == 0 {
				s.mu.Lock()
				_ = s.takeCropTempsLocked()
				s.mu.Unlock()
			}
		}
	}()

	wg.Wait()

	// Final sanity: a known set is resident and served.
	fN, jN, ssN := makeSet(9)
	s.swapImagesForTest(fN, jN, ssN)
	req := httptest.NewRequest(http.MethodGet, "/__shot/0", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /__shot/0, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("expected image/jpeg, got %q", ct)
	}
	if got := s.RequestEngage(0); got == nil || got.MonitorID != 0 || !got.IsPrimary {
		t.Fatalf("RequestEngage(0) = %+v, want primary monitor 0", got)
	}
}
