package clicker

import (
	"crypto/rand"
	"encoding/hex"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/StephenSHorton/toru/internal/hotkey"
)

// Limits Normalize enforces. Rates are clicks per second unless named per unit.
const (
	maxRate          = 500  // clicks/s ceiling
	maxRateHigh      = 1000 // with Settings.HighRate
	maxIntervalMs    = 1000 * 3600 * 1000.0
	doubleClickOffAt = 11 // clicks/s at and above which double click is turned off
	maxPx            = 10_000
	maxPointClicks   = 999_999
	maxPointRadius   = 9_999
	maxClickLimit    = 100_000_000
	maxTimeLimitSec  = 1000 * 3600.0
	maxHoldGapMs     = 600_000
	maxDoubleGapMs   = 1_000
	maxStartDelayMs  = 10_000
	maxPresetName    = 40
)

// unitMs is the length of each RateUnit; rateUnits is their pick order.
var (
	unitMs    = map[RateUnit]float64{UnitSecond: 1e3, UnitMinute: 60e3, UnitHour: 3600e3, UnitDay: 86400e3}
	rateUnits = []RateUnit{UnitSecond, UnitMinute, UnitHour, UnitDay}
)

// Normalize is the only path settings take in or out: it clamps every field,
// fixes enums, keeps the two Cadence forms in sync, turns double click off where
// it cannot apply, and makes limits exclusive. It is idempotent.
func Normalize(s Settings) Settings {
	def := DefaultSettings()
	s.Version = 1
	if s.HotkeyMode != HotkeyToggle && s.HotkeyMode != HotkeyHold {
		s.HotkeyMode = def.HotkeyMode
	}
	s.StartDelayMs = clampInt(s.StartDelayMs, 0, maxStartDelayMs)
	s.NewPointClicks = clampInt(s.NewPointClicks, 1, maxPointClicks)
	s.NewPointRadius = clampInt(s.NewPointRadius, 0, maxPointRadius)
	s.Profile = normalizeProfile(s.Profile, s.HighRate)
	s.StopZones = normalizeZones(s.StopZones)
	s.Presets = normalizePresets(s.Presets, s.HighRate)
	if presetIndex(s.Presets, s.ActivePresetID) < 0 {
		s.ActivePresetID = ""
	}
	return s
}

// rateCeiling is the clicks/s ceiling for the HighRate setting.
func rateCeiling(highRate bool) float64 {
	if highRate {
		return maxRateHigh
	}
	return maxRate
}

// intervalFloorMs is the shortest interval the ceiling allows (2 ms, or 1 ms
// with HighRate), never under the engine's 1 ms floor.
func intervalFloorMs(highRate bool) float64 {
	return math.Max(1, 1000/rateCeiling(highRate))
}

// normalizeCadence keeps Rate/Unit and IntervalMs in agreement. Mode says which
// form the user edits; the other is always recomputed from it, which makes the
// pair stable under repeated normalizing.
func normalizeCadence(c Cadence, highRate bool) Cadence {
	if c.Mode != CadenceRate && c.Mode != CadenceInterval {
		c.Mode = CadenceRate
	}
	if _, ok := unitMs[c.Unit]; !ok {
		c.Unit = UnitSecond
	}
	floor := intervalFloorMs(highRate)
	if c.Mode == CadenceRate {
		um := unitMs[c.Unit]
		if !finite(c.Rate) || c.Rate <= 0 {
			c.Rate = DefaultProfile().Cadence.Rate
		}
		c.Rate = clampF(c.Rate, um/maxIntervalMs, rateCeiling(highRate))
		c.IntervalMs = clampF(math.Round(um/c.Rate), floor, maxIntervalMs)
		return c
	}
	if !finite(c.IntervalMs) {
		c.IntervalMs = DefaultProfile().Cadence.IntervalMs
	}
	c.IntervalMs = clampF(math.Round(c.IntervalMs), math.Ceil(floor), maxIntervalMs)
	c.Rate, c.Unit = rateForInterval(c.IntervalMs, rateCeiling(highRate))
	return c
}

// rateForInterval picks the unit whose whole-number rate (1..ceiling per
// unit, like the rate field allows) comes closest to ivMs; ties go to the
// shorter unit. 40 ms is 25/s, 33 ms is 30/s, 90 s is 40/h.
func rateForInterval(ivMs, ceiling float64) (float64, RateUnit) {
	bestRate, bestUnit, bestErr := 0.0, UnitSecond, math.Inf(1)
	for _, u := range rateUnits {
		um := unitMs[u]
		r := clampF(math.Round(um/ivMs), 1, ceiling)
		if e := math.Abs(um/r - ivMs); e < bestErr {
			bestRate, bestUnit, bestErr = r, u, e
		}
	}
	return bestRate, bestUnit
}

// intervalMs is the base interval a run uses: exact from the rate in rate mode
// (3/s is 333.33 ms, not the rounded 333), IntervalMs in interval mode.
func intervalMs(c Cadence) float64 {
	if c.Mode == CadenceRate && c.Rate > 0 {
		return unitMs[c.Unit] / c.Rate
	}
	return c.IntervalMs
}

// clicksPerSecond is the effective rate of a normalized Cadence.
func clicksPerSecond(c Cadence) float64 {
	iv := intervalMs(c)
	if iv <= 0 {
		return 0
	}
	return 1000 / iv
}

func normalizeProfile(p Profile, highRate bool) Profile {
	def := DefaultProfile()
	p.Cadence = normalizeCadence(p.Cadence, highRate)
	if p.Input != InputMouse && p.Input != InputKeyboard {
		p.Input = def.Input
	}
	switch p.Button {
	case ButtonLeft, ButtonRight, ButtonMiddle, ButtonX1, ButtonX2:
	default:
		p.Button = def.Button
	}
	if p.Press != PressClick && p.Press != PressHold {
		p.Press = def.Press
	}
	p.Key = canonicalKey(p.Key)
	if !isLetterKey(p.Key) {
		p.Uppercase = false
	}
	p.DutyPercent = clampF(orF(p.DutyPercent, def.DutyPercent), 0, 100)
	p.HoldGapMs = clampInt(p.HoldGapMs, 0, maxHoldGapMs)
	p.DoubleGapMs = clampInt(p.DoubleGapMs, 1, maxDoubleGapMs)
	if p.Press == PressHold || clicksPerSecond(p.Cadence) >= doubleClickOffAt {
		p.DoubleClick = false
	}
	p.Jitter.Percent = clampF(orF(p.Jitter.Percent, def.Jitter.Percent), 0, 100)

	switch p.Limit.Kind {
	case LimitNone, LimitClicks, LimitTime:
	default:
		p.Limit.Kind = LimitNone
	}
	p.Limit.Clicks = clampI64(p.Limit.Clicks, 1, maxClickLimit)
	p.Limit.Seconds = clampF(orF(p.Limit.Seconds, def.Limit.Seconds), 1, maxTimeLimitSec)

	p.ClickPoints.Points = normalizePoints(p.ClickPoints.Points)

	p.Corners.TL = clampInt(p.Corners.TL, 0, maxPx)
	p.Corners.TR = clampInt(p.Corners.TR, 0, maxPx)
	p.Corners.BL = clampInt(p.Corners.BL, 0, maxPx)
	p.Corners.BR = clampInt(p.Corners.BR, 0, maxPx)
	p.Edges.Top = clampInt(p.Edges.Top, 0, maxPx)
	p.Edges.Right = clampInt(p.Edges.Right, 0, maxPx)
	p.Edges.Bottom = clampInt(p.Edges.Bottom, 0, maxPx)
	p.Edges.Left = clampInt(p.Edges.Left, 0, maxPx)

	if p.ProcessList.Mode != ListAllow && p.ProcessList.Mode != ListBlock {
		p.ProcessList.Mode = def.ProcessList.Mode
	}
	p.ProcessList.Entries = normalizeEntries(p.ProcessList.Entries)
	return p
}

// canonicalKey trims k and respells it the way hotkey.KeyName does ("a" ->
// "A", "pageup" -> "PageUp"). A name hotkey does not know is kept as typed so
// Start can report it (UNKNOWN_KEY) instead of silently dropping it.
func canonicalKey(k string) string {
	k = strings.TrimSpace(k)
	if r, ok := hotkey.ParseKeyName(k); ok {
		return hotkey.KeyName(r)
	}
	return k
}

// isLetterKey reports whether k is a single A-Z letter (the keys case control
// applies to).
func isLetterKey(k string) bool {
	return len(k) == 1 && k[0] >= 'A' && k[0] <= 'Z'
}

func normalizePoints(in []ClickPoint) []ClickPoint {
	out := make([]ClickPoint, 0, len(in))
	seen := map[string]bool{}
	for _, pt := range in {
		if pt.ID == "" || seen[pt.ID] {
			pt.ID = newID()
		}
		seen[pt.ID] = true
		pt.Clicks = clampInt(pt.Clicks, 1, maxPointClicks)
		pt.Radius = clampInt(pt.Radius, 0, maxPointRadius)
		out = append(out, pt)
	}
	return out
}

func normalizeZones(in []StopZone) []StopZone {
	out := make([]StopZone, 0, len(in))
	seen := map[string]bool{}
	for _, z := range in {
		if z.ID == "" || seen[z.ID] {
			z.ID = newID()
		}
		seen[z.ID] = true
		if z.W < 1 {
			z.W = 1
		}
		if z.H < 1 {
			z.H = 1
		}
		switch z.Action {
		case ZoneStop, ZonePause, ZoneStart:
		default:
			z.Action = ZoneStop
		}
		out = append(out, z)
	}
	return out
}

// normalizeEntries lowercases exe names, keeps only the file name, appends
// ".exe" when missing, and drops empties and duplicates (first one wins).
func normalizeEntries(in []ProcessEntry) []ProcessEntry {
	out := make([]ProcessEntry, 0, len(in))
	seen := map[string]bool{}
	for _, e := range in {
		e.Exe = normalizeExe(e.Exe)
		if e.Exe == "" || seen[e.Exe] {
			continue
		}
		seen[e.Exe] = true
		out = append(out, e)
	}
	return out
}

func normalizeExe(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if i := strings.LastIndexAny(name, `\/`); i >= 0 {
		name = name[i+1:]
	}
	if name == "" || name == ".exe" {
		return ""
	}
	if !strings.HasSuffix(name, ".exe") {
		name += ".exe"
	}
	return name
}

// normalizePresets trims and bounds names, drops nameless presets, makes names
// unique ignoring case (a clash gets " (2)", " (3)", ...), fills ids and
// timestamps, and normalizes every snapshot.
func normalizePresets(in []Preset, highRate bool) []Preset {
	out := make([]Preset, 0, len(in))
	ids := map[string]bool{}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, p := range in {
		p.Name = cleanPresetName(p.Name)
		if p.Name == "" {
			continue
		}
		p.Name = uniqueName(out, p.Name, "")
		if p.ID == "" || ids[p.ID] {
			p.ID = newID()
		}
		ids[p.ID] = true
		if _, err := time.Parse(time.RFC3339, p.CreatedAt); err != nil {
			p.CreatedAt = now
		}
		if _, err := time.Parse(time.RFC3339, p.UpdatedAt); err != nil {
			p.UpdatedAt = p.CreatedAt
		}
		p.Profile = normalizeProfile(p.Profile, highRate)
		out = append(out, p)
	}
	return out
}

// cleanPresetName trims a name and cuts it to maxPresetName characters.
func cleanPresetName(name string) string {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > maxPresetName {
		name = strings.TrimSpace(string([]rune(name)[:maxPresetName]))
	}
	return name
}

// nameTaken reports whether another preset (not exceptID) uses name, ignoring case.
func nameTaken(ps []Preset, name, exceptID string) bool {
	for _, p := range ps {
		if p.ID != exceptID && strings.EqualFold(p.Name, name) {
			return true
		}
	}
	return false
}

// uniqueName returns name, or "base (n)" with the lowest free n >= 2 when name
// is taken. An existing " (n)" suffix is dropped first, and base is shortened
// so the result still fits maxPresetName.
func uniqueName(ps []Preset, name, exceptID string) string {
	if !nameTaken(ps, name, exceptID) {
		return name
	}
	base := stripCopySuffix(name)
	for n := 2; ; n++ {
		suffix := " (" + strconv.Itoa(n) + ")"
		b := []rune(base)
		if keep := maxPresetName - utf8.RuneCountInString(suffix); len(b) > keep {
			b = b[:keep]
		}
		cand := strings.TrimSpace(string(b)) + suffix
		if !nameTaken(ps, cand, exceptID) {
			return cand
		}
	}
}

// stripCopySuffix removes a trailing " (n)".
func stripCopySuffix(name string) string {
	if !strings.HasSuffix(name, ")") {
		return name
	}
	i := strings.LastIndex(name, " (")
	if i < 0 {
		return name
	}
	if _, err := strconv.Atoi(name[i+2 : len(name)-1]); err != nil {
		return name
	}
	return name[:i]
}

func presetIndex(ps []Preset, id string) int {
	if id == "" {
		return -1
	}
	for i, p := range ps {
		if p.ID == id {
			return i
		}
	}
	return -1
}

// newID returns a random 16-hex-digit id for points, zones and presets.
func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// orF returns f, or def when f is NaN or infinite.
func orF(f, def float64) float64 {
	if !finite(f) {
		return def
	}
	return f
}

func clampF(v, lo, hi float64) float64 { return math.Min(math.Max(v, lo), hi) }

func clampInt(v, lo, hi int) int { return min(max(v, lo), hi) }

func clampI64(v, lo, hi int64) int64 { return min(max(v, lo), hi) }
