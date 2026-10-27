package clicker

import (
	"encoding/json"
	"math"
	"strings"
)

// Blur AutoClicker preset import. A Blur export is
// {"type": "blur-autoclicker-preset", "version": 1, "preset": {...}}, where the
// preset carries id, name, createdAt, updatedAt and a snapshot of Blur's
// "preset fields". The specs do not pin down whether the snapshot sits at the
// preset's top level or in a nested object, so both are accepted: the field set
// is whichever object holds a known Blur key. Unknown keys are ignored and
// missing ones keep the current profile's values.

// blurKnownKeys are the Blur preset keys (current and legacy) the mapper reads.
var blurKnownKeys = []string{
	"clickSpeed", "clickInterval", "inputType", "keyboardKey", "keyboardKeyCase",
	"mouseButton", "dutyCycleMode", "clickDurationMode", "dutyCycleEnabled", "dutyCycle",
	"speedRandomizationEnabled", "speedVariationEnabled", "speedRandomization",
	"speedVariation", "speedRandomizationMax", "doubleClickEnabled",
	"holdIntervalEnabled", "holdIntervalGapMs", "clickLimitEnabled", "clickLimit",
	"timeLimitEnabled", "timeLimit", "timeLimitUnit", "cornerStopEnabled",
	"cornerStopTL", "cornerStopTR", "cornerStopBL", "cornerStopBR", "edgeStopEnabled",
	"edgeStopTop", "edgeStopBottom", "edgeStopLeft", "edgeStopRight",
	"clickPointsEnabled", "sequenceEnabled", "stopZonesEnabled", "stopWhenComplete",
	"clickPoints", "sequencePoints", "processListEnabled", "processListMode",
	"processListEntries",
}

// blurFields reads JSON values out of a Blur field map by key.
type blurFields map[string]json.RawMessage

func (f blurFields) has(k string) bool { _, ok := f[k]; return ok }

func (f blurFields) num(keys ...string) (float64, bool) {
	for _, k := range keys {
		var v float64
		if raw, ok := f[k]; ok && json.Unmarshal(raw, &v) == nil && finite(v) {
			return v, true
		}
	}
	return 0, false
}

func (f blurFields) boolean(keys ...string) (bool, bool) {
	for _, k := range keys {
		var v bool
		if raw, ok := f[k]; ok && json.Unmarshal(raw, &v) == nil {
			return v, true
		}
	}
	return false, false
}

func (f blurFields) str(keys ...string) (string, bool) {
	for _, k := range keys {
		var v string
		if raw, ok := f[k]; ok && json.Unmarshal(raw, &v) == nil {
			return v, true
		}
	}
	return "", false
}

func (f blurFields) knownCount() int {
	n := 0
	for _, k := range blurKnownKeys {
		if f.has(k) {
			n++
		}
	}
	return n
}

// blurPreset maps a Blur preset object onto a copy of base.
func blurPreset(raw json.RawMessage, base Profile) (string, Profile, error) {
	var top blurFields
	if err := json.Unmarshal(raw, &top); err != nil {
		return "", base, clickerErr(ErrImportFormat, "The preset in that file is damaged")
	}
	name, _ := top.str("name")
	fields := top
	if top.knownCount() == 0 {
		// Look one level down for the snapshot object.
		for _, v := range top {
			var inner blurFields
			if json.Unmarshal(v, &inner) == nil && inner.knownCount() > 0 {
				fields = inner
				break
			}
		}
	}
	if fields.knownCount() == 0 {
		return "", base, clickerErr(ErrImportFormat, "That Blur AutoClicker preset has no settings Toru understands")
	}
	return name, mapBlurFields(fields, base), nil
}

func mapBlurFields(f blurFields, p Profile) Profile {
	// Cadence: Blur presets store the rate form only.
	if v, ok := f.num("clickSpeed"); ok {
		p.Cadence.Mode = CadenceRate
		p.Cadence.Rate = v
	}
	if v, ok := f.str("clickInterval"); ok {
		if _, known := unitMs[RateUnit(v)]; known {
			p.Cadence.Mode = CadenceRate
			p.Cadence.Unit = RateUnit(v)
		}
	}

	if v, ok := f.str("inputType"); ok {
		switch strings.ToLower(v) {
		case "mouse":
			p.Input = InputMouse
		case "keyboard":
			p.Input = InputKeyboard
		}
	}
	if v, ok := f.str("keyboardKey"); ok {
		p.Key = blurKeyName(v)
	}
	if v, ok := f.str("keyboardKeyCase"); ok {
		p.Uppercase = strings.EqualFold(v, "upper")
	}
	if v, ok := f.str("mouseButton"); ok {
		switch strings.ToLower(v) {
		case "left":
			p.Button = ButtonLeft
		case "right":
			p.Button = ButtonRight
		case "middle":
			p.Button = ButtonMiddle
		}
	}

	// Press shape: Blur's duty-cycle Hold, and for the mouse the fork's
	// hold-interval flag, both mean hold.
	if v, ok := f.str("dutyCycleMode", "clickDurationMode"); ok {
		if strings.EqualFold(v, "hold") {
			p.Press = PressHold
		} else if strings.EqualFold(v, "click") {
			p.Press = PressClick
		}
	}
	if v, ok := f.boolean("holdIntervalEnabled"); ok && v && p.Input == InputMouse {
		p.Press = PressHold
	}
	if v, ok := f.num("holdIntervalGapMs"); ok {
		p.HoldGapMs = int(math.Round(clampF(v, 0, maxHoldGapMs)))
	}
	if v, ok := f.num("dutyCycle"); ok {
		p.DutyPercent = v
	}
	if v, ok := f.boolean("dutyCycleEnabled"); ok && !v {
		p.DutyPercent = 0 // Blur's "off" is an almost-zero press
	}

	if v, ok := f.boolean("speedRandomizationEnabled", "speedVariationEnabled"); ok {
		p.Jitter.Enabled = v
	}
	if v, ok := f.num("speedRandomization", "speedVariation", "speedRandomizationMax"); ok {
		p.Jitter.Percent = v // Blur allows 200 %; Normalize caps at 100
	}
	if v, ok := f.boolean("doubleClickEnabled"); ok {
		p.DoubleClick = v
	}

	// Limits: Blur keeps two flags; the click limit wins if both are on.
	if v, ok := f.num("clickLimit"); ok {
		p.Limit.Clicks = int64(math.Round(clampF(v, 1, maxClickLimit)))
	}
	if v, ok := f.num("timeLimit"); ok {
		mult := 1.0
		if u, ok := f.str("timeLimitUnit"); ok {
			switch u {
			case "m":
				mult = 60
			case "h":
				mult = 3600
			}
		}
		p.Limit.Seconds = v * mult
	}
	clicksOn, hasClicks := f.boolean("clickLimitEnabled")
	timeOn, hasTime := f.boolean("timeLimitEnabled")
	switch {
	case clicksOn:
		p.Limit.Kind = LimitClicks
	case timeOn:
		p.Limit.Kind = LimitTime
	case hasClicks || hasTime:
		p.Limit.Kind = LimitNone
	}

	if v, ok := f.boolean("cornerStopEnabled"); ok {
		p.Corners.Enabled = v
	}
	setPx := func(dst *int, key string) {
		if v, ok := f.num(key); ok {
			*dst = int(math.Round(clampF(v, 0, maxPx)))
		}
	}
	setPx(&p.Corners.TL, "cornerStopTL")
	setPx(&p.Corners.TR, "cornerStopTR")
	setPx(&p.Corners.BL, "cornerStopBL")
	setPx(&p.Corners.BR, "cornerStopBR")
	if v, ok := f.boolean("edgeStopEnabled"); ok {
		p.Edges.Enabled = v
	}
	setPx(&p.Edges.Top, "edgeStopTop")
	setPx(&p.Edges.Bottom, "edgeStopBottom")
	setPx(&p.Edges.Left, "edgeStopLeft")
	setPx(&p.Edges.Right, "edgeStopRight")

	if v, ok := f.boolean("clickPointsEnabled", "sequenceEnabled"); ok {
		p.ClickPoints.Enabled = v
	}
	if v, ok := f.boolean("stopWhenComplete"); ok {
		p.ClickPoints.StopAfterPass = v
	}
	for _, k := range []string{"clickPoints", "sequencePoints"} {
		if raw, ok := f[k]; ok {
			if pts, ok := blurPoints(raw); ok {
				p.ClickPoints.Points = pts
			}
			break
		}
	}
	if v, ok := f.boolean("stopZonesEnabled"); ok {
		p.ZonesEnabled = v
	}

	if v, ok := f.boolean("processListEnabled"); ok {
		p.ProcessList.Enabled = v
	}
	if v, ok := f.str("processListMode"); ok {
		switch strings.ToLower(v) {
		case "whitelist":
			p.ProcessList.Mode = ListAllow
		case "blacklist":
			p.ProcessList.Mode = ListBlock
		}
	}
	if raw, ok := f["processListEntries"]; ok {
		if es, ok := blurEntries(raw); ok {
			p.ProcessList.Entries = es
		}
	}
	return p
}

// blurPoints reads [{id, x, y, clicks, radius}]; points without finite x/y are
// dropped, missing clicks default to 1.
func blurPoints(raw json.RawMessage) ([]ClickPoint, bool) {
	var in []map[string]any
	if json.Unmarshal(raw, &in) != nil {
		return nil, false
	}
	out := make([]ClickPoint, 0, len(in))
	for _, m := range in {
		x, okx := m["x"].(float64)
		y, oky := m["y"].(float64)
		if !okx || !oky {
			continue
		}
		pt := ClickPoint{
			X:      int32(clampF(math.Trunc(x), math.MinInt32, math.MaxInt32)),
			Y:      int32(clampF(math.Trunc(y), math.MinInt32, math.MaxInt32)),
			Clicks: 1,
		}
		if id, ok := m["id"].(string); ok {
			pt.ID = id
		}
		if c, ok := m["clicks"].(float64); ok {
			pt.Clicks = int(clampF(c, 1, maxPointClicks))
		}
		if r, ok := m["radius"].(float64); ok {
			pt.Radius = int(clampF(r, 0, maxPointRadius))
		}
		out = append(out, pt)
	}
	return out, true
}

// blurEntries reads [{name, enabled}] or bare strings.
func blurEntries(raw json.RawMessage) ([]ProcessEntry, bool) {
	var in []any
	if json.Unmarshal(raw, &in) != nil {
		return nil, false
	}
	out := make([]ProcessEntry, 0, len(in))
	for _, v := range in {
		switch e := v.(type) {
		case string:
			out = append(out, ProcessEntry{Exe: e, Enabled: true})
		case map[string]any:
			name, _ := e["name"].(string)
			enabled, ok := e["enabled"].(bool)
			if !ok {
				enabled = true
			}
			out = append(out, ProcessEntry{Exe: name, Enabled: enabled})
		}
	}
	return out, true
}

// blurKeyAliases maps Blur's lowercase key tokens to Toru's key names where the
// spelling differs. Letters, digits and F-keys parse as they are.
var blurKeyAliases = map[string]string{
	"spacebar": "Space", "space": "Space",
	"pgup": "PageUp", "pgdn": "PageDown",
	"del": "Delete", "ins": "Insert",
	"arrowup": "Up", "arrowdown": "Down", "arrowleft": "Left", "arrowright": "Right",
	"prtsc": "PrintScreen", "snapshot": "PrintScreen", "break": "Pause",
	"return": "Enter", "esc": "Escape",
	"numpadadd": "Num+", "numadd": "Num+", "numplus": "Num+", "numpadplus": "Num+",
	"numpadsubtract": "Num-", "numsub": "Num-", "numminus": "Num-",
	"numpadmultiply": "Num*", "nummul": "Num*",
	"numpaddivide": "Num/", "numdiv": "Num/",
	"numpaddecimal": "Num.", "numdot": "Num.", "numdel": "Num.",
	"slash": "/", "backslash": `\`, "semicolon": ";", "quote": "'", "apostrophe": "'",
	"bracketleft": "[", "bracketright": "]", "minus": "-", "equal": "=", "equals": "=",
	"backquote": "`", "grave": "`", "comma": ",", "period": ".",
}

// blurKeyName converts a Blur key token ("keya", "numpad5", "pgup") to Toru's
// spelling. Anything unrecognised is returned trimmed so Start can name it.
func blurKeyName(k string) string {
	t := strings.ToLower(strings.TrimSpace(k))
	if a, ok := blurKeyAliases[t]; ok {
		return canonicalKey(a)
	}
	switch {
	case strings.HasPrefix(t, "key") && len(t) == 4:
		t = t[3:]
	case strings.HasPrefix(t, "digit") && len(t) == 6:
		t = t[5:]
	case strings.HasPrefix(t, "numpad") && len(t) == 7:
		t = "num" + t[6:]
	}
	return canonicalKey(t)
}
