package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/StephenSHorton/toru/internal/export"
	"github.com/StephenSHorton/toru/internal/history"
	"github.com/StephenSHorton/toru/internal/overlay"
	"github.com/StephenSHorton/toru/internal/replay"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// agent_commands.go registers Toru's app-wide agent commands
// (docs/agent-control.md, "First command set"): status, capture, recording,
// instant replay, Library, clipboard and opening the editor. Windows add
// their own (the video editor's) from TypeScript.

// appAgentDeps is what the app-wide commands drive.
type appAgentDeps struct {
	version  string
	overlay  *overlay.OverlayService
	capturer capture.Capturer
	recorder *capture.Recorder
	history  *history.HistoryService
	replay   *replay.ReplayService
	export   *export.ExportService
	windows  *WindowsService
	// app is resolved lazily (the Wails app exists only after New).
	app func() *application.App
}

// schema builds a JSON Schema object with the given properties and required
// names.
func schema(props map[string]any, required ...string) json.RawMessage {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	b, _ := json.Marshal(s)
	return b
}

func decode[T any](input json.RawMessage) (T, error) {
	var v T
	if err := json.Unmarshal(input, &v); err != nil {
		return v, fmt.Errorf("bad input: %w", err)
	}
	return v, nil
}

var (
	propMonitor = map[string]any{"type": "integer", "minimum": 0, "description": "Monitor index from app.status screens (default: the primary monitor)."}
	propRect    = map[string]any{
		"type":        "object",
		"description": "Region in physical pixels on the virtual desktop (app.status screens use the same space).",
		"properties": map[string]any{
			"x": map[string]any{"type": "integer"}, "y": map[string]any{"type": "integer"},
			"w": map[string]any{"type": "integer", "minimum": 1}, "h": map[string]any{"type": "integer", "minimum": 1},
		},
		"required": []string{"x", "y", "w", "h"},
	}
	propWindow  = map[string]any{"type": "string", "description": "For mode window: part of the window's title (case-insensitive), or its hwnd as a number string."}
	propConfirm = map[string]any{"type": "boolean", "description": "Must be true: this can't be undone."}
)

// target is a capture target as commands take it.
type target struct {
	Mode    string        `json:"mode"` // "fullscreen" | "region" | "window"
	Monitor *int          `json:"monitor"`
	Rect    *capture.Rect `json:"rect"`
	Window  string        `json:"window"`
}

// resolve turns a target into the capture sub-mode, monitor and physical
// rect, the way the overlay would have.
func (d *appAgentDeps) resolve(t target) (sub string, monitor int, rect capture.Rect, err error) {
	screens, err := d.overlay.ListScreens()
	if err != nil || len(screens) == 0 {
		return "", 0, rect, fmt.Errorf("no screens found: %v", err)
	}
	screenOf := func(id int) (capture.ScreenInfo, bool) {
		for _, s := range screens {
			if s.ID == id {
				return s, true
			}
		}
		return capture.ScreenInfo{}, false
	}
	primary := screens[0]
	for _, s := range screens {
		if s.IsPrimary {
			primary = s
			break
		}
	}
	// The monitor that holds a rect's centre (else the primary).
	monitorAt := func(r capture.Rect) capture.ScreenInfo {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		for _, s := range screens {
			if cx >= s.X && cx < s.X+s.W && cy >= s.Y && cy < s.Y+s.H {
				return s
			}
		}
		return primary
	}

	switch t.Mode {
	case "", capture.SubFullscreen:
		sc := primary
		if t.Monitor != nil {
			s, ok := screenOf(*t.Monitor)
			if !ok {
				return "", 0, rect, fmt.Errorf("no monitor %d (app.status lists them)", *t.Monitor)
			}
			sc = s
		}
		return capture.SubFullscreen, sc.ID, capture.Rect{X: sc.X, Y: sc.Y, W: sc.W, H: sc.H}, nil
	case capture.SubRegion:
		if t.Rect == nil || t.Rect.W < 2 || t.Rect.H < 2 {
			return "", 0, rect, errors.New(`mode "region" needs a rect {x, y, w, h} of at least 2x2`)
		}
		sc := monitorAt(*t.Rect)
		if t.Monitor != nil {
			s, ok := screenOf(*t.Monitor)
			if !ok {
				return "", 0, rect, fmt.Errorf("no monitor %d", *t.Monitor)
			}
			sc = s
		}
		return capture.SubRegion, sc.ID, *t.Rect, nil
	case capture.SubWindow:
		w, err := d.findWindow(t.Window)
		if err != nil {
			return "", 0, rect, err
		}
		sc := monitorAt(w.Rect)
		if w.MonitorID >= 0 {
			if s, ok := screenOf(w.MonitorID); ok {
				sc = s
			}
		}
		return capture.SubWindow, sc.ID, w.Rect, nil
	}
	return "", 0, rect, fmt.Errorf(`mode must be "fullscreen", "region" or "window", not %q`, t.Mode)
}

func (d *appAgentDeps) findWindow(q string) (capture.WindowInfo, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return capture.WindowInfo{}, errors.New(`mode "window" needs "window": part of its title`)
	}
	wins := d.overlay.ListWindows()
	for _, w := range wins {
		if fmt.Sprint(w.HWND) == q {
			return w, nil
		}
	}
	var hits []capture.WindowInfo
	for _, w := range wins {
		if strings.Contains(strings.ToLower(w.Title), strings.ToLower(q)) {
			hits = append(hits, w)
		}
	}
	switch len(hits) {
	case 0:
		titles := make([]string, 0, min(len(wins), 15))
		for _, w := range wins[:min(len(wins), 15)] {
			titles = append(titles, w.Title)
		}
		return capture.WindowInfo{}, fmt.Errorf("no window title contains %q; open windows include: %s", q, strings.Join(titles, " | "))
	case 1:
		return hits[0], nil
	}
	for _, w := range hits {
		if strings.EqualFold(w.Title, q) {
			return w, nil
		}
	}
	return hits[0], nil // windows are listed front to back: the topmost match
}

// imageResult is how commands return a picture (MCP turns it into image
// content).
type imageResult struct {
	Mime   string `json:"mime"`
	Base64 string `json:"base64"`
}

func pngImage(path string) (*imageResult, int, int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, 0, err
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, 0, 0, err
	}
	return &imageResult{Mime: "image/png", Base64: base64.StdEncoding.EncodeToString(b)}, cfg.Width, cfg.Height, nil
}

// libraryCopyOf finds the Library item archived from src: the newest
// recording with the same size.
func libraryCopyOf(h *history.HistoryService, src string) (history.Item, bool) {
	fi, err := os.Stat(src)
	if err != nil {
		return history.Item{}, false
	}
	for _, it := range h.List() {
		if it.Kind != history.KindVideo {
			continue
		}
		if c, err := os.Stat(it.Path); err == nil && c.Size() == fi.Size() {
			return it, true
		}
		return history.Item{}, false // only the newest recording can be it
	}
	return history.Item{}, false
}

func registerAppAgentCommands(reg *agentctl.Registry, d *appAgentDeps) {
	reg.Register(agentctl.Command{
		Name:        "app.status",
		Description: "What Toru is doing now: version, monitors (ids and physical bounds, for capture targets), screen recordings in progress, instant replay and game recording state.",
		Handler: func(context.Context, json.RawMessage) (any, error) {
			screens, _ := d.overlay.ListScreens()
			return map[string]any{
				"version":    d.version,
				"screens":    screens,
				"recordings": d.recorder.Handles(),
				"replay":     d.replay.GetStatus(),
				"recorder":   d.replay.GetRecorderState(),
				"libraryDir": d.history.GetDir(),
				"clipsDir":   d.replay.GetSaveDir(),
			}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "app.openSettings",
		Description: "Open (or bring forward) Toru's main window: Library, settings and the other tabs.",
		Handler: func(context.Context, json.RawMessage) (any, error) {
			d.windows.OpenSettings()
			return map[string]any{"opened": true}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "capture.screenshot",
		Description: "Take a screenshot without the capture overlay and save it to the Library. " +
			`mode "fullscreen" (a whole monitor), "region" (rect in physical pixels) or "window" (by title). ` +
			"Returns the saved file's path and, unless image is false, the PNG.",
		Input: schema(map[string]any{
			"mode":    map[string]any{"type": "string", "enum": []string{"fullscreen", "region", "window"}, "default": "fullscreen"},
			"monitor": propMonitor,
			"rect":    propRect,
			"window":  propWindow,
			"copy":    map[string]any{"type": "boolean", "description": "Also copy it to the clipboard."},
			"image":   map[string]any{"type": "boolean", "default": true, "description": "Return the PNG in the result (false: just the path)."},
		}),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				target
				Copy  bool  `json:"copy"`
				Image *bool `json:"image"`
			}](in)
			if err != nil {
				return nil, err
			}
			sub, mon, rect, err := d.resolve(v.target)
			if err != nil {
				return nil, err
			}
			res, err := d.capturer.Capture(capture.CaptureRequest{Mode: capture.ModeScreenshot, Sub: sub, MonitorID: mon, Rect: rect})
			if err != nil {
				return nil, err
			}
			item, err := d.history.Add(res.ImagePath, history.KindImage)
			if err != nil {
				return nil, fmt.Errorf("saved the screenshot at %s but couldn't add it to the Library: %w", res.ImagePath, err)
			}
			_ = os.Remove(res.ImagePath) // the Library has its own copy
			if v.Copy {
				if err := d.export.CopyToClipboardAs(item.Path, "image", "Screenshot"); err != nil {
					return nil, fmt.Errorf("saved %s, but copying it failed: %w", item.Path, err)
				}
			}
			out := map[string]any{"path": item.Path, "id": item.ID, "rect": rect, "monitor": mon}
			if v.Image == nil || *v.Image {
				img, w, h, err := pngImage(item.Path)
				if err == nil {
					out["image"], out["width"], out["height"] = img, w, h
				}
			}
			return out, nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "record.start",
		Description: "Start a screen recording (the same as choosing Video in the capture overlay): " +
			`mode "fullscreen", "region" or "window". The recording pill appears with a Stop button. ` +
			"Audio follows the sources picked in the overlay's Audio menu. Returns the recording's handle for record.stop.",
		Input: schema(map[string]any{
			"mode":    map[string]any{"type": "string", "enum": []string{"fullscreen", "region", "window"}, "default": "fullscreen"},
			"monitor": propMonitor,
			"rect":    propRect,
			"window":  propWindow,
			"cursor":  map[string]any{"type": "boolean", "default": true, "description": "Show the mouse cursor in the video."},
		}),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				target
				Cursor *bool `json:"cursor"`
			}](in)
			if err != nil {
				return nil, err
			}
			sub, mon, rect, err := d.resolve(v.target)
			if err != nil {
				return nil, err
			}
			cursor := v.Cursor == nil || *v.Cursor
			handle, err := d.overlay.StartRecording(capture.CaptureRequest{Mode: capture.ModeVideo, Sub: sub, MonitorID: mon, Rect: rect, IncludeCursor: cursor})
			if err != nil {
				return nil, err
			}
			return map[string]any{"handle": handle, "rect": rect, "monitor": mon}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "record.stop",
		Description: "Stop a screen recording and save it to the Library. Without handle, stops the most recent one. Returns the video's path.",
		Input: schema(map[string]any{
			"handle": map[string]any{"type": "string", "description": "From record.start or record.status."},
			"open":   map[string]any{"type": "boolean", "description": "Open the result in the trim window, as the Stop button does."},
		}),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				Handle string `json:"handle"`
				Open   bool   `json:"open"`
			}](in)
			if err != nil {
				return nil, err
			}
			handles := d.recorder.Handles()
			if len(handles) == 0 {
				return nil, errors.New("no screen recording is running")
			}
			h := v.Handle
			if h == "" {
				h = handles[len(handles)-1]
			} else if !slices.Contains(handles, h) {
				return nil, fmt.Errorf("no recording %q is running (running: %s)", h, strings.Join(handles, ", "))
			}
			res, err := d.overlay.StopRecording(h)
			if err != nil {
				return nil, err
			}
			// The pill showed this recording's timer; it has nothing left to stop.
			if app := d.app(); app != nil {
				if w, ok := app.Window.GetByName("toru-recording-pill"); ok {
					w.Close()
				}
			}
			if v.Open && res.VideoPath != "" {
				d.windows.OpenTrim(res.VideoPath)
			}
			out := map[string]any{"path": res.VideoPath, "handle": h}
			// StopRecording archived a copy into the Library; point at that one
			// (the temp file is cleaned up later).
			if it, ok := libraryCopyOf(d.history, res.VideoPath); ok {
				out["path"], out["id"] = it.Path, it.ID
			}
			return out, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "record.status",
		Description: "Screen recordings in progress (handles), plus game recording's state (mode, whether it is capturing, the game it follows, disk use).",
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return map[string]any{"recordings": d.recorder.Handles(), "gameRecording": d.replay.GetRecorderState()}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "replay.save",
		Description: "Save the instant replay buffer (the last minutes of gameplay) as a clip, like the Save replay hotkey. Instant replay must be on in Settings > Game recording. Returns the clip's path.",
		Handler: func(context.Context, json.RawMessage) (any, error) {
			p, err := d.replay.SaveReplay()
			if err != nil {
				return nil, err
			}
			return map[string]any{"path": p}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "replay.status",
		Description: "Instant replay's state: running, recording, encoder, seconds buffered, last error.",
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return map[string]any{"replay": d.replay.GetStatus(), "settings": d.replay.GetSettings()}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "library.list",
		Description: "List the Library, newest first: screenshots and screen recordings (with an id), saved clips and game sessions (with a session id). " +
			"Use the id or path with library.open / library.delete, or a path with editor.open.",
		Input: schema(map[string]any{
			"kind":  map[string]any{"type": "string", "enum": []string{"all", "shots", "recordings", "clips", "sessions"}, "default": "all"},
			"limit": map[string]any{"type": "integer", "minimum": 1, "default": 50},
		}),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				Kind  string `json:"kind"`
				Limit int    `json:"limit"`
			}](in)
			if err != nil {
				return nil, err
			}
			if v.Kind == "" {
				v.Kind = "all"
			}
			if v.Limit <= 0 {
				v.Limit = 50
			}
			type entry struct {
				Kind    string    `json:"kind"` // "shot" | "recording" | "clip" | "session"
				ID      string    `json:"id,omitempty"`
				Session int       `json:"session,omitempty"`
				Path    string    `json:"path,omitempty"`
				Name    string    `json:"name"`
				Game    string    `json:"game,omitempty"`
				Seconds float64   `json:"seconds,omitempty"`
				When    time.Time `json:"when"`
			}
			var out []entry
			want := func(k string) bool { return v.Kind == "all" || v.Kind == k }
			for _, it := range d.history.List() {
				k := "shots"
				kind := "shot"
				if it.Kind == history.KindVideo {
					k, kind = "recordings", "recording"
				}
				if want(k) {
					out = append(out, entry{Kind: kind, ID: it.ID, Path: it.Path, Name: it.Label, When: it.TakenAt})
				}
			}
			if want("clips") {
				for _, c := range d.replay.ListClips() {
					out = append(out, entry{Kind: "clip", Path: c.Path, Name: c.Name, Game: c.Game, When: c.SavedAt})
				}
			}
			if want("sessions") {
				for _, s := range d.replay.ListSessions() {
					name := s.Game
					if name == "" {
						name = "Desktop"
					}
					out = append(out, entry{Kind: "session", Session: s.ID, Name: name, Game: s.Game, Seconds: s.DurationSec, When: time.UnixMilli(s.StartMs)})
				}
			}
			slices.SortStableFunc(out, func(a, b entry) int { return b.When.Compare(a.When) })
			total := len(out)
			out = out[:min(len(out), v.Limit)]
			if out == nil {
				out = []entry{}
			}
			return map[string]any{"items": out, "total": total}, nil
		},
	})

	// libRef is how library.open / library.delete name an item.
	type libRef struct {
		ID      string `json:"id"`
		Path    string `json:"path"`
		Session int    `json:"session"`
	}
	refProps := map[string]any{
		"id":      map[string]any{"type": "string", "description": "A screenshot's or recording's id from library.list."},
		"path":    map[string]any{"type": "string", "description": "A saved clip's path (or any Library file's path)."},
		"session": map[string]any{"type": "integer", "description": "A game session's id from library.list."},
	}
	historyByPath := func(p string) (history.Item, bool) {
		for _, it := range d.history.List() {
			if strings.EqualFold(filepath.Clean(it.Path), filepath.Clean(p)) {
				return it, true
			}
		}
		return history.Item{}, false
	}

	reg.Register(agentctl.Command{
		Name:        "library.open",
		Description: "Open a Library item in Toru: a screenshot in the screenshot editor, a recording or clip in the trim window, a game session in its timeline. Give one of id, path or session.",
		Input:       schema(refProps),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			r, err := decode[libRef](in)
			if err != nil {
				return nil, err
			}
			switch {
			case r.Session != 0:
				return map[string]any{"opened": true}, d.replay.OpenSession(r.Session)
			case r.ID != "":
				return map[string]any{"opened": true}, d.history.Open(r.ID)
			case r.Path != "":
				if it, ok := historyByPath(r.Path); ok {
					return map[string]any{"opened": true}, d.history.Open(it.ID)
				}
				return map[string]any{"opened": true}, d.replay.OpenClip(r.Path)
			}
			return nil, errors.New("give id, path or session")
		},
	})

	reg.Register(agentctl.Command{
		Name:        "library.delete",
		Description: "Delete a Library item and its file for good: a screenshot or recording (id or path), a saved clip (path) or a game session (session). Needs confirm: true.",
		Destructive: true,
		Input: schema(map[string]any{
			"id": refProps["id"], "path": refProps["path"], "session": refProps["session"], "confirm": propConfirm,
		}, "confirm"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			r, err := decode[libRef](in)
			if err != nil {
				return nil, err
			}
			switch {
			case r.Session != 0:
				return map[string]any{"deleted": true}, d.replay.DeleteSession(r.Session)
			case r.ID != "":
				return map[string]any{"deleted": true}, d.history.Delete(r.ID)
			case r.Path != "":
				if it, ok := historyByPath(r.Path); ok {
					return map[string]any{"deleted": true}, d.history.Delete(it.ID)
				}
				return map[string]any{"deleted": true}, d.replay.DeleteClip(r.Path)
			}
			return nil, errors.New("give id, path or session")
		},
	})

	reg.Register(agentctl.Command{
		Name:        "export.copyToClipboard",
		Description: "Copy a file to the clipboard so it pastes into chat apps: images as a picture, videos as a file.",
		Input: schema(map[string]any{
			"path": map[string]any{"type": "string", "description": "Absolute path of an image or video."},
		}, "path"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				Path string `json:"path"`
			}](in)
			if err != nil {
				return nil, err
			}
			if _, err := os.Stat(v.Path); err != nil {
				return nil, err
			}
			kind, what := "video", "Video"
			switch strings.ToLower(filepath.Ext(v.Path)) {
			case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
				kind, what = "image", "Image"
			}
			if err := d.export.CopyToClipboardAs(v.Path, kind, what); err != nil {
				return nil, err
			}
			return map[string]any{"copied": v.Path, "as": kind}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "editor.open",
		Description: "Open a video (a clip, recording or any video file) in the Edit video window. Once it's open, the editor.* commands that window offers act on it.",
		Input: schema(map[string]any{
			"path": map[string]any{"type": "string", "description": "Absolute path of the video."},
		}, "path"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				Path string `json:"path"`
			}](in)
			if err != nil {
				return nil, err
			}
			if fi, err := os.Stat(v.Path); err != nil || fi.IsDir() {
				return nil, fmt.Errorf("no video at %q", v.Path)
			}
			d.windows.OpenEditVideo(v.Path)
			return map[string]any{"opened": v.Path}, nil
		},
	})
}
