package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	toast "github.com/StephenSHorton/go-toast"
	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/StephenSHorton/toru/internal/clicker"
	"github.com/StephenSHorton/toru/internal/hotkey"
	"github.com/StephenSHorton/toru/internal/replay"
	"github.com/StephenSHorton/toru/internal/transfer"
	"github.com/StephenSHorton/toru/internal/update"
)

type fakeClicker struct {
	st      clicker.Status
	set     clicker.Settings
	starts  int
	presets []string // ops run, "delete p1"
}

func (f *fakeClicker) Status() clicker.Status { return f.st }
func (f *fakeClicker) Start() (clicker.Status, error) {
	if !f.st.Armed {
		return f.st, errors.New("disarmed")
	}
	f.starts++
	f.st.State = "running"
	return f.st, nil
}
func (f *fakeClicker) Stop() clicker.Status { f.st.State = "idle"; return f.st }
func (f *fakeClicker) SetPaused(p bool) clicker.Status {
	f.st.State = map[bool]clicker.RunState{true: "paused", false: "running"}[p]
	return f.st
}
func (f *fakeClicker) SetArmed(a bool) (clicker.Settings, error) {
	f.st.Armed, f.set.Armed = a, a
	return f.set, nil
}
func (f *fakeClicker) GetSettings() clicker.Settings { return f.set }
func (f *fakeClicker) SetSettings(n clicker.Settings) (clicker.Settings, error) {
	f.set = n
	return n, nil
}
func (f *fakeClicker) CursorPosition() (clicker.Point, error) {
	return clicker.Point{X: 10, Y: 20}, nil
}
func (f *fakeClicker) preset(op, id string) (clicker.Settings, error) {
	f.presets = append(f.presets, op+" "+id)
	return f.set, nil
}
func (f *fakeClicker) SavePreset(name string) (clicker.Settings, error) {
	return f.preset("save", name)
}
func (f *fakeClicker) ApplyPreset(id string) (clicker.Settings, error) { return f.preset("apply", id) }
func (f *fakeClicker) UpdatePreset(id string) (clicker.Settings, error) {
	return f.preset("update", id)
}
func (f *fakeClicker) DeletePreset(id string) (clicker.Settings, error) {
	return f.preset("delete", id)
}
func (f *fakeClicker) Stats() clicker.Stats { return clicker.Stats{Runs: 3} }

type fakeTransfer struct {
	send transfer.SendState
	recv transfer.ReceiveState
}

func (f *fakeTransfer) StartSend(path string) error {
	f.send = transfer.SendState{Active: true, Phase: "waiting", Path: path, Code: "7-guitarist-revenge"}
	return nil
}
func (f *fakeTransfer) StopSend() error                { f.send = transfer.SendState{Phase: "stopped"}; return nil }
func (f *fakeTransfer) SendStatus() transfer.SendState { return f.send }
func (f *fakeTransfer) StartReceive(code string) error {
	f.recv = transfer.ReceiveState{Active: true, Phase: "connecting", Code: code}
	return nil
}
func (f *fakeTransfer) CancelReceive() error                 { f.recv.Phase = "cancelled"; return nil }
func (f *fakeTransfer) ReceiveStatus() transfer.ReceiveState { return f.recv }
func (f *fakeTransfer) GetReceiveDir() string                { return `C:\Downloads` }

type fakeUpload struct {
	links   map[string]string
	uploads int
}

func (f *fakeUpload) ShareLink(path string, _, _ int) (string, error) {
	f.uploads++
	return "https://files.example/abc.mp4", nil
}
func (f *fakeUpload) CancelShare()               {}
func (f *fakeUpload) LinkFor(path string) string { return f.links[path] }

type fakeHotkeys struct{ scs []hotkey.Shortcut }

func (f *fakeHotkeys) GetShortcuts() []hotkey.Shortcut { return f.scs }
func (f *fakeHotkeys) SetShortcut(action string, sc hotkey.Shortcut) error {
	for i := range f.scs {
		if f.scs[i].Action == action {
			sc.Label = f.scs[i].Label
			f.scs[i] = sc
		}
	}
	return nil
}
func (f *fakeHotkeys) ResetShortcut(action string) error {
	return f.SetShortcut(action, hotkey.Shortcut{Action: action, Key: "S", Ctrl: true, IsDefault: true})
}
func (f *fakeHotkeys) ClearShortcut(action string) error {
	return f.SetShortcut(action, hotkey.Shortcut{Action: action})
}

type fakeSettings struct {
	login bool
	notes toast.Options
}

func (f *fakeSettings) GetLaunchAtLogin() (bool, error) { return f.login, nil }
func (f *fakeSettings) SetLaunchAtLogin(on bool) error  { f.login = on; return nil }
func (f *fakeSettings) GetNotifications() toast.Options { return f.notes }
func (f *fakeSettings) SetNotifications(o toast.Options) (toast.Options, error) {
	f.notes = o
	return o, nil
}

type fakeReplayConfig struct{ set replay.Settings }

func (f *fakeReplayConfig) GetSettings() replay.Settings { return f.set }
func (f *fakeReplayConfig) SetSettings(n replay.Settings) (capture.ReplayStatus, error) {
	f.set = n
	return capture.ReplayStatus{}, nil
}

type fakeUpdate struct {
	st       update.Status
	installs int
}

func (f *fakeUpdate) GetVersion() string       { return "0.14.0" }
func (f *fakeUpdate) GetStatus() update.Status { return f.st }
func (f *fakeUpdate) CheckAndDownload() (update.Status, error) {
	f.st = update.Status{Phase: "ready", Version: "0.14.1"}
	return f.st, nil
}
func (f *fakeUpdate) RestartToUpdate() error { f.installs++; return nil }

type moreFakes struct {
	clicker  *fakeClicker
	transfer *fakeTransfer
	upload   *fakeUpload
	hotkeys  *fakeHotkeys
	settings *fakeSettings
	replay   *fakeReplayConfig
	update   *fakeUpdate
}

func newMoreReg() (*agentctl.Registry, *moreFakes) {
	f := &moreFakes{
		clicker:  &fakeClicker{st: clicker.Status{Armed: true}, set: clicker.DefaultSettings()},
		transfer: &fakeTransfer{},
		upload:   &fakeUpload{links: map[string]string{}},
		hotkeys:  &fakeHotkeys{scs: []hotkey.Shortcut{{Action: "overlay", Label: "Open capture overlay", Key: "S", Ctrl: true, IsDefault: true}}},
		settings: &fakeSettings{notes: toast.Options{Enabled: true, Sound: true, Volume: 0.5, Corner: "bottom-right"}},
		replay:   &fakeReplayConfig{set: replay.DefaultSettings()},
		update:   &fakeUpdate{st: update.Status{Phase: "idle"}},
	}
	reg := agentctl.NewRegistry()
	registerMoreAgentCommands(reg, moreAgentDeps{
		clicker: f.clicker, transfer: f.transfer, upload: f.upload, hotkeys: f.hotkeys,
		settings: f.settings, replay: f.replay, update: f.update,
	})
	return reg, f
}

// callMore runs a command and returns its result as JSON text.
func callMore(t *testing.T, reg *agentctl.Registry, name, input string) (string, error) {
	t.Helper()
	if input == "" {
		input = "{}"
	}
	res, err := reg.Call(context.Background(), name, json.RawMessage(input))
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("%s: marshal: %v", name, err)
	}
	return string(b), nil
}

func TestMoreCommandsRegistered(t *testing.T) {
	reg, _ := newMoreReg()
	have := map[string]agentctl.Command{}
	for _, c := range reg.List() {
		have[c.Name] = c
	}
	for _, n := range []string{
		"clicker.status", "clicker.start", "clicker.stop", "clicker.pause", "clicker.arm", "clicker.getSettings", "clicker.setSettings", "clicker.cursor", "clicker.preset",
		"transfer.status", "transfer.send", "transfer.stopSend", "transfer.receive", "transfer.cancelReceive", "upload.shareLink", "upload.cancel",
		"shortcuts.list", "shortcuts.set", "shortcuts.reset", "shortcuts.clear",
		"settings.get", "settings.set", "replay.getSettings", "replay.setSettings", "update.status", "update.check", "update.install",
	} {
		c, ok := have[n]
		if !ok {
			t.Errorf("%s isn't registered", n)
			continue
		}
		var s map[string]any
		if err := json.Unmarshal(c.Input, &s); err != nil || s["type"] != "object" {
			t.Errorf("%s: input schema %s", n, c.Input)
		}
	}
	for _, n := range []string{"upload.shareLink", "update.install"} {
		if !have[n].Destructive {
			t.Errorf("%s must be marked destructive", n)
		}
	}
}

func TestClickerStartNeedsConfirm(t *testing.T) {
	reg, f := newMoreReg()
	if _, err := callMore(t, reg, "clicker.start", ""); err == nil || f.clicker.starts != 0 {
		t.Fatalf("start without confirm: err %v, starts %d", err, f.clicker.starts)
	}
	if out, err := callMore(t, reg, "clicker.start", `{"confirm":true}`); err != nil || f.clicker.starts != 1 || !strings.Contains(out, "running") {
		t.Fatalf("start: %s %v", out, err)
	}
	if out, _ := callMore(t, reg, "clicker.stop", ""); !strings.Contains(out, "idle") {
		t.Errorf("stop: %s", out)
	}
}

func TestClickerPresetsConfirm(t *testing.T) {
	reg, f := newMoreReg()
	for _, in := range []string{`{"action":"delete","id":"p1"}`, `{"action":"update","id":"p1"}`, `{"action":"apply"}`, `{"action":"rename","id":"p1"}`} {
		if _, err := callMore(t, reg, "clicker.preset", in); err == nil {
			t.Errorf("%s: want an error", in)
		}
	}
	for _, in := range []string{`{"action":"apply","id":"p1"}`, `{"action":"save","name":"Fast"}`, `{"action":"delete","id":"p1","confirm":true}`} {
		if _, err := callMore(t, reg, "clicker.preset", in); err != nil {
			t.Errorf("%s: %v", in, err)
		}
	}
	if got := strings.Join(f.clicker.presets, ","); got != "apply p1,save Fast,delete p1" {
		t.Errorf("preset ops = %s", got)
	}
}

func TestClickerSetSettingsMerges(t *testing.T) {
	reg, f := newMoreReg()
	before := f.clicker.set
	if _, err := callMore(t, reg, "clicker.setSettings", `{"settings":{"showIndicator":false,"startDelayMs":500}}`); err != nil {
		t.Fatal(err)
	}
	got := f.clicker.set
	if got.ShowIndicator || got.StartDelayMs != 500 || got.StopOnTaskSwitcher != before.StopOnTaskSwitcher || got.Profile.DoubleGapMs != before.Profile.DoubleGapMs {
		t.Errorf("merged = %+v", got)
	}
	for _, in := range []string{`{"settings":{"presets":[]}}`, `{"settings":{"armed":true}}`, `{"settings":{"noSuch":1}}`, `{"settings":{"profile":{"noSuch":1}}}`, `{"settings":5}`} {
		if _, err := callMore(t, reg, "clicker.setSettings", in); err == nil {
			t.Errorf("%s: want an error", in)
		}
	}
}

func TestShareLinkConfirmAndCache(t *testing.T) {
	reg, f := newMoreReg()
	clip := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(clip, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := func(extra string) string { b, _ := json.Marshal(clip); return `{"path":` + string(b) + extra + `}` }
	if _, err := callMore(t, reg, "upload.shareLink", in("")); err == nil || f.upload.uploads != 0 {
		t.Fatalf("upload without confirm: err %v, uploads %d", err, f.upload.uploads)
	}
	if out, err := callMore(t, reg, "upload.shareLink", in(`,"confirm":true`)); err != nil || f.upload.uploads != 1 || !strings.Contains(out, `"uploaded":true`) {
		t.Fatalf("upload: %s %v", out, err)
	}
	// Shared before: the saved link, no second upload.
	f.upload.links[clip] = "https://files.example/saved.mp4"
	if out, err := callMore(t, reg, "upload.shareLink", in(`,"confirm":true`)); err != nil || f.upload.uploads != 1 || !strings.Contains(out, "saved.mp4") {
		t.Fatalf("cached: %s %v", out, err)
	}
	if _, err := callMore(t, reg, "upload.shareLink", `{"path":"C:\\nope\\missing.mp4","confirm":true}`); err == nil {
		t.Error("missing file: want an error")
	}
}

func TestTransferSendReturnsCode(t *testing.T) {
	reg, _ := newMoreReg()
	dir := t.TempDir()
	b, _ := json.Marshal(dir)
	out, err := callMore(t, reg, "transfer.send", `{"path":`+string(b)+`}`)
	if err != nil || !strings.Contains(out, "7-guitarist-revenge") {
		t.Fatalf("send: %s %v", out, err)
	}
	if _, err := callMore(t, reg, "transfer.receive", `{"code":"  "}`); err == nil {
		t.Error("blank code: want an error")
	}
	if out, err := callMore(t, reg, "transfer.receive", `{"code":"3-a-b"}`); err != nil || !strings.Contains(out, "connecting") {
		t.Errorf("receive: %s %v", out, err)
	}
	if out, _ := callMore(t, reg, "transfer.status", ""); !strings.Contains(out, `"receiveDir":"C:\\Downloads"`) {
		t.Errorf("status: %s", out)
	}
}

func TestShortcutCommands(t *testing.T) {
	reg, f := newMoreReg()
	if _, err := callMore(t, reg, "shortcuts.set", `{"action":"nope","key":"K"}`); err == nil {
		t.Error("unknown action: want an error")
	}
	out, err := callMore(t, reg, "shortcuts.set", `{"action":"overlay","key":"K","alt":true}`)
	if err != nil || !strings.Contains(out, `"key":"K"`) || !strings.Contains(out, `"label":"Open capture overlay"`) {
		t.Fatalf("set: %s %v", out, err)
	}
	if _, err := callMore(t, reg, "shortcuts.clear", `{"action":"overlay"}`); err != nil || f.hotkeys.scs[0].Key != "" {
		t.Errorf("clear: %v %+v", err, f.hotkeys.scs[0])
	}
	if _, err := callMore(t, reg, "shortcuts.reset", `{"action":"overlay"}`); err != nil || f.hotkeys.scs[0].Key != "S" {
		t.Errorf("reset: %v %+v", err, f.hotkeys.scs[0])
	}
}

func TestSettingsCommands(t *testing.T) {
	reg, f := newMoreReg()
	out, err := callMore(t, reg, "settings.set", `{"launchAtLogin":true,"notifications":{"volume":0.2}}`)
	if err != nil || !f.settings.login || f.settings.notes.Volume != 0.2 || !f.settings.notes.Sound || f.settings.notes.Corner != "bottom-right" {
		t.Fatalf("set: %s %v %+v", out, err, f.settings)
	}
	if _, err := callMore(t, reg, "settings.set", ""); err == nil {
		t.Error("nothing to change: want an error")
	}
	if _, err := callMore(t, reg, "settings.set", `{"notifications":{"colour":"red"}}`); err == nil {
		t.Error("unknown notification field: want an error")
	}
}

func TestReplaySettingsConfirmOnShrink(t *testing.T) {
	reg, f := newMoreReg()
	f.replay.set.MaxDiskGB, f.replay.set.RetentionSec = 50, 0
	if _, err := callMore(t, reg, "replay.setSettings", `{"settings":{"mode":"replay","durationSec":120,"desktopAudio":false}}`); err != nil {
		t.Fatal(err)
	}
	if s := f.replay.set; s.Mode != "replay" || s.DurationSec != 120 || s.DesktopAudio || s.MaxDiskGB != 50 {
		t.Errorf("merged = %+v", s)
	}
	for _, in := range []string{`{"settings":{"maxDiskGB":10}}`, `{"settings":{"retentionSec":3600}}`} {
		if _, err := callMore(t, reg, "replay.setSettings", in); err == nil {
			t.Errorf("%s without confirm: want an error", in)
		}
	}
	if f.replay.set.MaxDiskGB != 50 {
		t.Errorf("maxDiskGB changed without confirm: %d", f.replay.set.MaxDiskGB)
	}
	if _, err := callMore(t, reg, "replay.setSettings", `{"settings":{"maxDiskGB":10},"confirm":true}`); err != nil || f.replay.set.MaxDiskGB != 10 {
		t.Errorf("with confirm: %v %d", err, f.replay.set.MaxDiskGB)
	}
	if _, err := callMore(t, reg, "replay.setSettings", `{"settings":{"maxDiskGB":20}}`); err != nil {
		t.Errorf("raising the limit needs no confirm: %v", err)
	}
}

func TestUpdateInstall(t *testing.T) {
	reg, f := newMoreReg()
	if _, err := callMore(t, reg, "update.install", `{"confirm":true}`); err == nil || f.update.installs != 0 {
		t.Fatalf("install with nothing ready: err %v", err)
	}
	if out, err := callMore(t, reg, "update.check", ""); err != nil || !strings.Contains(out, "0.14.1") {
		t.Fatalf("check: %s %v", out, err)
	}
	if _, err := callMore(t, reg, "update.install", ""); err == nil || f.update.installs != 0 {
		t.Fatalf("install without confirm: err %v", err)
	}
	if _, err := callMore(t, reg, "update.install", `{"confirm":true}`); err != nil || f.update.installs != 1 {
		t.Fatalf("install: %v %d", err, f.update.installs)
	}
}
