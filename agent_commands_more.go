package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	toast "github.com/StephenSHorton/go-toast"
	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/StephenSHorton/toru/internal/clicker"
	"github.com/StephenSHorton/toru/internal/hotkey"
	"github.com/StephenSHorton/toru/internal/replay"
	"github.com/StephenSHorton/toru/internal/transfer"
	"github.com/StephenSHorton/toru/internal/update"
)

// agent_commands_more.go registers the agent commands for the auto clicker,
// Send Files, share links, keyboard shortcuts, app settings, the recording
// settings and updates (docs/agent-control.md, docs/clicker.md,
// docs/send-files.md). Things that publish a file, delete something or quit
// Toru need "confirm": true. No result carries a secret: none of these
// settings hold one (the call room code lives in stream.json, which only the
// call.* commands read).

// clickerAgent is what the commands use of *clicker.ClickerService.
type clickerAgent interface {
	Status() clicker.Status
	Start() (clicker.Status, error)
	Stop() clicker.Status
	SetPaused(paused bool) clicker.Status
	SetArmed(armed bool) (clicker.Settings, error)
	GetSettings() clicker.Settings
	SetSettings(next clicker.Settings) (clicker.Settings, error)
	CursorPosition() (clicker.Point, error)
	SavePreset(name string) (clicker.Settings, error)
	ApplyPreset(id string) (clicker.Settings, error)
	UpdatePreset(id string) (clicker.Settings, error)
	DeletePreset(id string) (clicker.Settings, error)
	Stats() clicker.Stats
}

// transferAgent is what the commands use of *transfer.TransferService.
type transferAgent interface {
	StartSend(path string) error
	StopSend() error
	SendStatus() transfer.SendState
	StartReceive(code string) error
	CancelReceive() error
	ReceiveStatus() transfer.ReceiveState
	GetReceiveDir() string
}

// uploadAgent is what the commands use of *upload.UploadService.
type uploadAgent interface {
	ShareLink(path string, startMs, endMs int) (string, error)
	CancelShare()
	LinkFor(path string) string
}

// hotkeyAgent is what the commands use of *hotkey.HotkeyService.
type hotkeyAgent interface {
	GetShortcuts() []hotkey.Shortcut
	SetShortcut(action string, sc hotkey.Shortcut) error
	ResetShortcut(action string) error
	ClearShortcut(action string) error
}

// settingsAgent is what the commands use of *SettingsService.
type settingsAgent interface {
	GetLaunchAtLogin() (bool, error)
	SetLaunchAtLogin(enabled bool) error
	GetNotifications() toast.Options
	SetNotifications(o toast.Options) (toast.Options, error)
}

// replayConfigAgent is what the commands use of *replay.ReplayService.
type replayConfigAgent interface {
	GetSettings() replay.Settings
	SetSettings(next replay.Settings) (capture.ReplayStatus, error)
}

// updateAgent is what the commands use of *update.UpdateService.
type updateAgent interface {
	GetVersion() string
	GetStatus() update.Status
	CheckAndDownload() (update.Status, error)
	RestartToUpdate() error
}

type moreAgentDeps struct {
	clicker  clickerAgent
	transfer transferAgent
	upload   uploadAgent
	hotkeys  hotkeyAgent
	settings settingsAgent
	replay   replayConfigAgent
	update   updateAgent
}

// mergeSettings applies patch (a JSON object of the fields to change; nested
// objects merge the same way, arrays and other values replace) to cur. A
// field cur doesn't have, or one in locked, is an error, so a typo doesn't
// silently do nothing.
func mergeSettings[T any](cur T, patch json.RawMessage, locked ...string) (T, error) {
	var zero T
	var p map[string]any
	if err := json.Unmarshal(patch, &p); err != nil || p == nil {
		return zero, errors.New("settings must be an object of the fields to change")
	}
	for _, k := range locked {
		if _, ok := p[k]; ok {
			return zero, fmt.Errorf("%q can't be set here", k)
		}
	}
	b, err := json.Marshal(cur)
	if err != nil {
		return zero, err
	}
	var base map[string]any
	if err := json.Unmarshal(b, &base); err != nil {
		return zero, err
	}
	if err := mergeInto(base, p, ""); err != nil {
		return zero, err
	}
	b, err = json.Marshal(base)
	if err != nil {
		return zero, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var out T
	if err := dec.Decode(&out); err != nil {
		return zero, fmt.Errorf("bad settings: %w", err)
	}
	return out, nil
}

func mergeInto(base, patch map[string]any, prefix string) error {
	for k, v := range patch {
		old, ok := base[k]
		if !ok {
			return fmt.Errorf("no setting %q (the get command lists them)", prefix+k)
		}
		om, oldIsObj := old.(map[string]any)
		nm, newIsObj := v.(map[string]any)
		if oldIsObj && newIsObj {
			if err := mergeInto(om, nm, prefix+k+"."); err != nil {
				return err
			}
			continue
		}
		base[k] = v
	}
	return nil
}

var (
	propSettingsPatch = map[string]any{"type": "object", "description": "Only the fields to change, with the names the matching get command returns. Nested objects merge; arrays replace."}
	propPath          = map[string]any{"type": "string", "description": "Absolute path."}
	propAction        = map[string]any{"type": "string", "description": "The shortcut's action id, from shortcuts.list."}
)

func registerMoreAgentCommands(reg *agentctl.Registry, d moreAgentDeps) {
	registerClickerAgentCommands(reg, d.clicker)
	registerTransferAgentCommands(reg, d.transfer, d.upload)
	registerShortcutAgentCommands(reg, d.hotkeys)
	registerSettingsAgentCommands(reg, d.settings, d.replay, d.update)
}

// ---- auto clicker -----------------------------------------------------------

func registerClickerAgentCommands(reg *agentctl.Registry, c clickerAgent) {
	reg.Register(agentctl.Command{
		Name:        "clicker.status",
		Description: "The auto clicker now: idle, counting down, running or paused, clicks so far, why it last stopped, whether it's armed, its start/stop shortcut, and lifetime stats.",
		Input:       schema(map[string]any{}),
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return map[string]any{"status": c.Status(), "stats": c.Stats()}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "clicker.start",
		Description: "Start the auto clicker with its current settings (after the start delay). It clicks for real wherever its settings say, " +
			"so it needs confirm: true. It stops by itself on the screen-corner failsafes, stop zones, limits and Alt+Tab; clicker.stop stops it. " +
			"It must be armed (clicker.arm).",
		Input: schema(map[string]any{"confirm": map[string]any{"type": "boolean", "description": "Must be true: the clicker clicks the real mouse/keyboard."}}, "confirm"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				Confirm bool `json:"confirm"`
			}](in)
			if err != nil {
				return nil, err
			}
			if !v.Confirm {
				return nil, errors.New(`clicker.start clicks for real: send "confirm": true`)
			}
			return c.Start()
		},
	})

	reg.Register(agentctl.Command{
		Name:        "clicker.stop",
		Description: "Stop the auto clicker (or cancel its countdown).",
		Input:       schema(map[string]any{}),
		Handler:     func(context.Context, json.RawMessage) (any, error) { return c.Stop(), nil },
	})

	reg.Register(agentctl.Command{
		Name:        "clicker.pause",
		Description: "Pause (paused: true) or resume (false) a running auto clicker.",
		Input:       schema(map[string]any{"paused": map[string]any{"type": "boolean"}}, "paused"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				Paused *bool `json:"paused"`
			}](in)
			if err != nil {
				return nil, err
			}
			if v.Paused == nil {
				return nil, errors.New("paused is required")
			}
			return c.SetPaused(*v.Paused), nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "clicker.arm",
		Description: "Arm (armed: true) or disarm the auto clicker. While disarmed its shortcut goes to other apps and it can't start; disarming stops a run.",
		Input:       schema(map[string]any{"armed": map[string]any{"type": "boolean"}}, "armed"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				Armed *bool `json:"armed"`
			}](in)
			if err != nil {
				return nil, err
			}
			if v.Armed == nil {
				return nil, errors.New("armed is required")
			}
			if _, err := c.SetArmed(*v.Armed); err != nil {
				return nil, err
			}
			return c.Status(), nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "clicker.getSettings",
		Description: "The auto clicker's settings: the profile (button, interval, limits, points, keys), stop zones, failsafe options and presets. " +
			"Screen positions are physical pixels on the virtual desktop (clicker.cursor reads the pointer).",
		Input:   schema(map[string]any{}),
		Handler: func(context.Context, json.RawMessage) (any, error) { return c.GetSettings(), nil },
	})

	reg.Register(agentctl.Command{
		Name: "clicker.setSettings",
		Description: "Change auto clicker settings: pass only what changes, e.g. {\"settings\":{\"profile\":{\"cadence\":{\"mode\":\"interval\",\"intervalMs\":50}}}}. " +
			"Presets, armed and the internal fields have their own commands and can't be set here. Returns the settings as saved.",
		Input: schema(map[string]any{"settings": propSettingsPatch}, "settings"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				Settings json.RawMessage `json:"settings"`
			}](in)
			if err != nil {
				return nil, err
			}
			next, err := mergeSettings(c.GetSettings(), v.Settings, "version", "armed", "presets", "activePresetId", "introduced")
			if err != nil {
				return nil, err
			}
			return c.SetSettings(next)
		},
	})

	reg.Register(agentctl.Command{
		Name:        "clicker.cursor",
		Description: "Where the mouse pointer is now, in physical pixels on the virtual desktop (the space clicker points and zones use).",
		Input:       schema(map[string]any{}),
		Handler:     func(context.Context, json.RawMessage) (any, error) { return c.CursorPosition() },
	})

	reg.Register(agentctl.Command{
		Name: "clicker.preset",
		Description: "Auto clicker presets (named profiles; ids from clicker.getSettings presets). action: apply (load it), save (the current profile as a new preset named name), " +
			"update (overwrite it with the current profile; needs confirm: true) or delete (needs confirm: true).",
		Input: schema(map[string]any{
			"action":  map[string]any{"type": "string", "enum": []string{"apply", "save", "update", "delete"}},
			"id":      map[string]any{"type": "string", "description": "The preset's id (apply, update, delete)."},
			"name":    map[string]any{"type": "string", "description": "The new preset's name (save), 1..40 characters."},
			"confirm": map[string]any{"type": "boolean", "description": "Needed for update and delete: the old preset is gone."},
		}, "action"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				Action  string `json:"action"`
				ID      string `json:"id"`
				Name    string `json:"name"`
				Confirm bool   `json:"confirm"`
			}](in)
			if err != nil {
				return nil, err
			}
			switch v.Action {
			case "save":
				return c.SavePreset(v.Name)
			case "apply", "update", "delete":
				if v.ID == "" {
					return nil, fmt.Errorf("%s needs the preset's id", v.Action)
				}
			default:
				return nil, errors.New("action must be apply, save, update or delete")
			}
			if v.Action != "apply" && !v.Confirm {
				return nil, fmt.Errorf(`%s replaces the saved preset for good: send "confirm": true`, v.Action)
			}
			switch v.Action {
			case "apply":
				return c.ApplyPreset(v.ID)
			case "update":
				return c.UpdatePreset(v.ID)
			default:
				return c.DeletePreset(v.ID)
			}
		},
	})
}

// ---- Send Files and share links ---------------------------------------------

func registerTransferAgentCommands(reg *agentctl.Registry, t transferAgent, up uploadAgent) {
	status := func() map[string]any {
		return map[string]any{"send": t.SendStatus(), "receive": t.ReceiveStatus(), "receiveDir": t.GetReceiveDir()}
	}

	reg.Register(agentctl.Command{
		Name: "transfer.status",
		Description: "Send Files now: the outgoing transfer (its short code once issued, progress) and the incoming one (progress, where it was saved), " +
			"plus the folder received files go to.",
		Input:   schema(map[string]any{}),
		Handler: func(context.Context, json.RawMessage) (any, error) { return status(), nil },
	})

	reg.Register(agentctl.Command{
		Name: "transfer.send",
		Description: "Offer a file or folder through Send Files. Returns once the short code is issued (or after 30s, still preparing); " +
			"the file goes to whoever types that code, end-to-end encrypted, so only give the code to the person it's for. One send at a time.",
		Input: schema(map[string]any{"path": propPath}, "path"),
		Handler: func(ctx context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				Path string `json:"path"`
			}](in)
			if err != nil {
				return nil, err
			}
			if _, err := os.Stat(v.Path); err != nil {
				return nil, fmt.Errorf("nothing at %q", v.Path)
			}
			if err := t.StartSend(v.Path); err != nil {
				return nil, err
			}
			waitFor(ctx, 30*time.Second, func() bool {
				s := t.SendStatus()
				return s.Code != "" || s.Phase == "error" || !s.Active
			})
			return t.SendStatus(), nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "transfer.stopSend",
		Description: "Stop the outgoing Send Files transfer; its code stops working.",
		Input:       schema(map[string]any{}),
		Handler: func(context.Context, json.RawMessage) (any, error) {
			if err := t.StopSend(); err != nil {
				return nil, err
			}
			return t.SendStatus(), nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "transfer.receive",
		Description: "Receive what someone offers with a Send Files code (like 22-conformist-belfast). It's saved in the receive folder and never overwrites " +
			"(a copy gets a (2) suffix). Returns at once; transfer.status shows progress and the saved path.",
		Input: schema(map[string]any{"code": map[string]any{"type": "string", "description": "The code the sender gave you."}}, "code"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				Code string `json:"code"`
			}](in)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(v.Code) == "" {
				return nil, errors.New("code is required")
			}
			if err := t.StartReceive(v.Code); err != nil {
				return nil, err
			}
			return t.ReceiveStatus(), nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "transfer.cancelReceive",
		Description: "Cancel the incoming Send Files transfer.",
		Input:       schema(map[string]any{}),
		Handler: func(context.Context, json.RawMessage) (any, error) {
			if err := t.CancelReceive(); err != nil {
				return nil, err
			}
			return t.ReceiveStatus(), nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "upload.shareLink",
		Description: "Upload a video to a public host and return its link (anyone with the link can watch it, and it can't be taken down from Toru), " +
			"so it needs confirm: true. Optional startMs/endMs share part of it. Big clips are compressed to fit first, which can take a while. " +
			"A clip shared before returns its saved link without uploading again.",
		Input: schema(map[string]any{
			"path":    propPath,
			"startMs": map[string]any{"type": "integer", "minimum": 0},
			"endMs":   map[string]any{"type": "integer", "minimum": 0, "description": "0 or left out = to the end."},
			"confirm": map[string]any{"type": "boolean", "description": "Must be true to upload: the video becomes public."},
		}, "path"),
		Destructive: true,
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				Path    string `json:"path"`
				StartMs int    `json:"startMs"`
				EndMs   int    `json:"endMs"`
			}](in)
			if err != nil {
				return nil, err
			}
			if fi, err := os.Stat(v.Path); err != nil || fi.IsDir() {
				return nil, fmt.Errorf("no video at %q", v.Path)
			}
			if v.StartMs == 0 && v.EndMs == 0 {
				if link := up.LinkFor(v.Path); link != "" {
					return map[string]any{"url": link, "uploaded": false}, nil
				}
			}
			link, err := up.ShareLink(v.Path, v.StartMs, v.EndMs)
			if err != nil {
				return nil, err
			}
			return map[string]any{"url": link, "uploaded": true}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "upload.cancel",
		Description: "Cancel a share link upload in progress.",
		Input:       schema(map[string]any{}),
		Handler: func(context.Context, json.RawMessage) (any, error) {
			up.CancelShare()
			return map[string]any{"cancelled": true}, nil
		},
	})
}

// ---- keyboard shortcuts -----------------------------------------------------

func registerShortcutAgentCommands(reg *agentctl.Registry, h hotkeyAgent) {
	find := func(action string) (hotkey.Shortcut, error) {
		for _, sc := range h.GetShortcuts() {
			if sc.Action == action {
				return sc, nil
			}
		}
		return hotkey.Shortcut{}, fmt.Errorf("no shortcut %q (shortcuts.list has the actions)", action)
	}

	reg.Register(agentctl.Command{
		Name:        "shortcuts.list",
		Description: "Every keyboard shortcut: its action id, label, keys (key \"\" = not set) and whether it's the default.",
		Input:       schema(map[string]any{}),
		Handler:     func(context.Context, json.RawMessage) (any, error) { return h.GetShortcuts(), nil },
	})

	reg.Register(agentctl.Command{
		Name: "shortcuts.set",
		Description: "Set a shortcut's keys. key is \"A\"..\"Z\", \"0\"..\"9\", \"F1\"..\"F24\" or a named key (\"PageUp\", \"Num5\", \"Mouse4\"); " +
			"ctrl/shift/alt/win add modifiers. A combo another shortcut uses is refused. shortcuts.reset puts the default back.",
		Input: schema(map[string]any{
			"action": propAction,
			"key":    map[string]any{"type": "string"},
			"ctrl":   map[string]any{"type": "boolean"}, "shift": map[string]any{"type": "boolean"},
			"alt": map[string]any{"type": "boolean"}, "win": map[string]any{"type": "boolean"},
		}, "action", "key"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[hotkey.Shortcut](in)
			if err != nil {
				return nil, err
			}
			if _, err := find(v.Action); err != nil {
				return nil, err
			}
			if strings.TrimSpace(v.Key) == "" {
				return nil, errors.New("key is required (shortcuts.clear unsets a shortcut)")
			}
			v.Sides, v.Extra = []string{}, []string{}
			if err := h.SetShortcut(v.Action, v); err != nil {
				return nil, err
			}
			return find(v.Action)
		},
	})

	for _, c := range []struct {
		name, what string
		fn         func(string) error
	}{
		{"shortcuts.reset", "Put a shortcut back to its default keys.", h.ResetShortcut},
		{"shortcuts.clear", "Unset a shortcut (no keys).", h.ClearShortcut},
	} {
		reg.Register(agentctl.Command{
			Name:        c.name,
			Description: c.what,
			Input:       schema(map[string]any{"action": propAction}, "action"),
			Handler: func(_ context.Context, in json.RawMessage) (any, error) {
				v, err := decode[struct {
					Action string `json:"action"`
				}](in)
				if err != nil {
					return nil, err
				}
				if _, err := find(v.Action); err != nil {
					return nil, err
				}
				if err := c.fn(v.Action); err != nil {
					return nil, err
				}
				return find(v.Action)
			},
		})
	}
}

// ---- settings, recording settings and updates -------------------------------

func registerSettingsAgentCommands(reg *agentctl.Registry, s settingsAgent, r replayConfigAgent, u updateAgent) {
	get := func() (map[string]any, error) {
		login, err := s.GetLaunchAtLogin()
		if err != nil {
			return nil, err
		}
		return map[string]any{"launchAtLogin": login, "notifications": s.GetNotifications()}, nil
	}

	reg.Register(agentctl.Command{
		Name: "settings.get",
		Description: "Toru's general settings: launch at Windows login, and notifications (on, sound, volume 0..1, corner). " +
			"Recording settings are replay.getSettings; shortcuts are shortcuts.list.",
		Input:   schema(map[string]any{}),
		Handler: func(context.Context, json.RawMessage) (any, error) { return get() },
	})

	reg.Register(agentctl.Command{
		Name:        "settings.set",
		Description: "Change general settings: launchAtLogin, and/or notifications with only the fields that change. Returns the settings as applied.",
		Input: schema(map[string]any{
			"launchAtLogin": map[string]any{"type": "boolean"},
			"notifications": propSettingsPatch,
		}),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				LaunchAtLogin *bool           `json:"launchAtLogin"`
				Notifications json.RawMessage `json:"notifications"`
			}](in)
			if err != nil {
				return nil, err
			}
			if v.LaunchAtLogin == nil && v.Notifications == nil {
				return nil, errors.New("nothing to change: pass launchAtLogin and/or notifications")
			}
			if v.Notifications != nil {
				next, err := mergeSettings(s.GetNotifications(), v.Notifications)
				if err != nil {
					return nil, err
				}
				if _, err := s.SetNotifications(next); err != nil {
					return nil, err
				}
			}
			if v.LaunchAtLogin != nil {
				if err := s.SetLaunchAtLogin(*v.LaunchAtLogin); err != nil {
					return nil, err
				}
			}
			return get()
		},
	})

	reg.Register(agentctl.Command{
		Name: "replay.getSettings",
		Description: "The recording settings: enabled, mode (replay = keep the last durationSec for the save shortcut; background = record whole sessions; " +
			"manual = record between presses), storage limit, video (monitor, fps, height, bitrate, codec) and audio (desktop, Discord, mic, volumes, separate tracks), and folders.",
		Input:   schema(map[string]any{}),
		Handler: func(context.Context, json.RawMessage) (any, error) { return r.GetSettings(), nil },
	})

	reg.Register(agentctl.Command{
		Name: "replay.setSettings",
		Description: "Change recording settings: pass only what changes, e.g. {\"settings\":{\"mode\":\"replay\",\"durationSec\":120,\"desktopAudio\":true}}. " +
			"Lowering maxDiskGB or retentionSec deletes the oldest recordings that no longer fit, so that needs confirm: true. " +
			"Changing mode or turning recording off ends a manual recording. Returns the settings as saved and the recorder's status.",
		Input: schema(map[string]any{
			"settings": propSettingsPatch,
			"confirm":  map[string]any{"type": "boolean", "description": "Needed to lower maxDiskGB or retentionSec: old recordings get deleted."},
		}, "settings"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				Settings json.RawMessage `json:"settings"`
				Confirm  bool            `json:"confirm"`
			}](in)
			if err != nil {
				return nil, err
			}
			cur := r.GetSettings()
			next, err := mergeSettings(cur, v.Settings)
			if err != nil {
				return nil, err
			}
			if shrinksRetention(cur, next) && !v.Confirm {
				return nil, errors.New(`lowering maxDiskGB or retentionSec deletes old recordings: send "confirm": true`)
			}
			st, err := r.SetSettings(next)
			if err != nil {
				return nil, err
			}
			return map[string]any{"settings": r.GetSettings(), "status": st}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "update.status",
		Description: "Toru's version and the updater's state: idle, checking, downloading, ready (an update is downloaded; update.install applies it) or error.",
		Input:       schema(map[string]any{}),
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return map[string]any{"version": u.GetVersion(), "update": u.GetStatus()}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "update.check",
		Description: "Check for a newer Toru and download it if there is one (it isn't installed until update.install or the next quit).",
		Input:       schema(map[string]any{}),
		Handler: func(context.Context, json.RawMessage) (any, error) {
			st, err := u.CheckAndDownload()
			if err != nil {
				return nil, err
			}
			return map[string]any{"version": u.GetVersion(), "update": st}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "update.install",
		Description: "Quit Toru and install the downloaded update (update.status must say ready). Recording stops and this agent connection drops until Toru is back, " +
			"so it needs confirm: true.",
		Input:       schema(map[string]any{"confirm": map[string]any{"type": "boolean", "description": "Must be true: Toru quits."}}, "confirm"),
		Destructive: true,
		Handler: func(context.Context, json.RawMessage) (any, error) {
			if ph := u.GetStatus().Phase; ph != "ready" {
				return nil, fmt.Errorf("no update is ready to install (updater: %s); run update.check first", ph)
			}
			if err := u.RestartToUpdate(); err != nil {
				return nil, err
			}
			return map[string]any{"installing": true}, nil
		},
	})
}

// shrinksRetention reports whether next keeps less footage than cur, which
// makes the recorder delete old recordings. RetentionSec 0 means no limit.
func shrinksRetention(cur, next replay.Settings) bool {
	if next.MaxDiskGB < cur.MaxDiskGB {
		return true
	}
	return next.RetentionSec != 0 && (cur.RetentionSec == 0 || next.RetentionSec < cur.RetentionSec)
}

// waitFor polls done until it's true, the wait runs out or ctx ends.
func waitFor(ctx context.Context, wait time.Duration, done func() bool) {
	deadline := time.Now().Add(wait)
	for !done() && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}
