package overlay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
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
//
// Freeze is the "freeze the screen during capture" preference. A POINTER so an
// absent field (older overlay.json, or never toggled) reads as the default —
// freeze ON — instead of Go's zero value (false). nil => default true.
//
// Region is the last shared crop in VIRTUAL-DESKTOP PHYSICAL px (origin = primary
// top-left; may be negative; MAY straddle monitors). It supersedes the per-monitor
// Crops map for the shared-crop overlay (one selection across the whole desktop).
// A POINTER so an absent field (older overlay.json) falls back to a centered
// default on the primary.
type cropStore struct {
	Crops  map[string]capture.Rect `json:"crops"`
	Freeze *bool                   `json:"freeze,omitempty"`
	Region *capture.Rect           `json:"region,omitempty"`
}

// defaultFreeze is the freeze-on-capture default: ON (the classic frozen-still
// overlay). Toggling it OFF shows a live, see-through overlay during selection.
const defaultFreeze = true

// freezeEnabled resolves the persisted freeze preference, defaulting to ON when
// the field is absent.
func (st cropStore) freezeEnabled() bool {
	if st.Freeze == nil {
		return defaultFreeze
	}
	return *st.Freeze
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

// seedRegion resolves the shared crop (VIRTUAL-DESKTOP PHYSICAL px) to seed an
// engage with. It prefers the persisted Region when it is still usable against the
// current monitor layout, then the legacy primary per-monitor Crop (translated to
// virtual coords), then a centered default on the primary. The front end re-clamps
// to the virtual-desktop union, so this only needs to be a sane starting rect.
func seedRegion(screens []capture.ScreenInfo, st cropStore) capture.Rect {
	if st.Region != nil && validRegion(*st.Region, screens) {
		return *st.Region
	}
	primary := primaryScreen(screens)
	if primary != nil {
		// Legacy fallback: a per-monitor crop saved by the pre-straddle overlay.
		if r, ok := st.Crops[strconv.Itoa(primary.ID)]; ok && validCrop(r, primary.W, primary.H) {
			return capture.Rect{X: primary.X + r.X, Y: primary.Y + r.Y, W: r.W, H: r.H}
		}
		d := centeredDefault(primary.W, primary.H)
		return capture.Rect{X: primary.X + d.X, Y: primary.Y + d.Y, W: d.W, H: d.H}
	}
	if len(screens) > 0 {
		sc := screens[0]
		d := centeredDefault(sc.W, sc.H)
		return capture.Rect{X: sc.X + d.X, Y: sc.Y + d.Y, W: d.W, H: d.H}
	}
	return capture.Rect{}
}

// validRegion reports whether vr (VIRTUAL-DESKTOP PHYSICAL px) is still usable:
// positive size and overlapping at least one monitor (so a saved region survives
// a monitor being unplugged or rearranged without stranding the crop off-desktop).
func validRegion(vr capture.Rect, screens []capture.ScreenInfo) bool {
	if vr.W <= 0 || vr.H <= 0 {
		return false
	}
	for _, sc := range screens {
		if intersectArea(vr, sc) > 0 {
			return true
		}
	}
	return false
}

// primaryScreen returns the primary monitor (or nil if none flagged).
func primaryScreen(screens []capture.ScreenInfo) *capture.ScreenInfo {
	for i := range screens {
		if screens[i].IsPrimary {
			return &screens[i]
		}
	}
	return nil
}

// intersectArea returns the overlap area (physical px²) between a virtual rect and
// a monitor's virtual rect — 0 when disjoint. Used to pick the dominant monitor
// and to validate a restored region.
func intersectArea(vr capture.Rect, sc capture.ScreenInfo) int {
	x0 := max(vr.X, sc.X)
	y0 := max(vr.Y, sc.Y)
	x1 := min(vr.X+vr.W, sc.X+sc.W)
	y1 := min(vr.Y+vr.H, sc.Y+sc.H)
	if x1 <= x0 || y1 <= y0 {
		return 0
	}
	return (x1 - x0) * (y1 - y0)
}
