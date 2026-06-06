package overlay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/StephenSHorton/toru/internal/capture"
)

// cropFileMu serializes the load-modify-save of overlay.json so concurrent
// SaveCrop calls (debounced drag-end + the immediate commit save + the Go-side
// commit save) can't interleave and drop a monitor's crop update. The atomic
// tmp+rename in saveCrops already prevents a corrupt file; this prevents a lost
// update on the read-modify-write.
var cropFileMu sync.Mutex

// cropStore is the on-disk shape of %AppData%\toru\overlay.json. Crops are keyed
// by monitor ID (stringified, since JSON object keys are strings) and stored as
// MONITOR-LOCAL PHYSICAL px so they are DPI-stable across scale changes.
type cropStore struct {
	Crops map[string]capture.Rect `json:"crops"`
}

// overlayStorePath returns %AppData%\toru\overlay.json (os.UserConfigDir on
// Windows is %AppData%). It also ensures the parent directory exists.
func overlayStorePath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cfg, "toru")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "overlay.json"), nil
}

// loadCrops reads the persisted crop store. A missing or unparseable file yields
// an empty (non-nil) store so callers can index/assign without nil checks.
func loadCrops() cropStore {
	st := cropStore{Crops: map[string]capture.Rect{}}
	path, err := overlayStorePath()
	if err != nil {
		return st
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	var parsed cropStore
	if err := json.Unmarshal(b, &parsed); err != nil {
		return st
	}
	if parsed.Crops == nil {
		parsed.Crops = map[string]capture.Rect{}
	}
	return parsed
}

// saveCrops writes the store atomically (tmp + rename) so a crash mid-write never
// corrupts overlay.json.
func saveCrops(st cropStore) error {
	if st.Crops == nil {
		st.Crops = map[string]capture.Rect{}
	}
	path, err := overlayStorePath()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "overlay-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// centeredDefault returns a monitor-local PHYSICAL-px crop covering the centered
// half of a monitor that is monW x monH physical px: width/height = half the
// monitor, positioned so the gap on each side is equal.
func centeredDefault(monW, monH int) capture.Rect {
	cw := monW / 2
	ch := monH / 2
	return capture.Rect{
		X: (monW - cw) / 2,
		Y: (monH - ch) / 2,
		W: cw,
		H: ch,
	}
}

// validCrop reports whether r (monitor-local PHYSICAL px) fits inside a monitor
// of monW x monH physical px.
func validCrop(r capture.Rect, monW, monH int) bool {
	return r.W > 0 && r.H > 0 &&
		r.X >= 0 && r.Y >= 0 &&
		r.X+r.W <= monW && r.Y+r.H <= monH
}
