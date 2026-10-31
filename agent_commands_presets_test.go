package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/fx"
	"github.com/StephenSHorton/toru/internal/presets"
)

func TestPresetAgentCommands(t *testing.T) {
	root := t.TempDir()
	oldDir, oldLut := presets.Dir, fx.LutDir
	presets.Dir = func() (string, error) { return filepath.Join(root, "presets"), nil }
	fx.LutDir = func() (string, error) { return filepath.Join(root, "luts"), nil }
	t.Cleanup(func() { presets.Dir, fx.LutDir = oldDir, oldLut })

	svc := presets.NewService()
	p, err := svc.Save(`{"name":"Night","effects":[{"type":"look","enabled":true,"params":{"look":"cool"}}]}`)
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	reg := agentctl.NewRegistry()
	registerPresetAgentCommands(reg, presetAgentDeps{presets: svc, emit: func(name string, _ any) { events = append(events, name) }})
	call := func(name, in string) (string, error) {
		res, err := reg.Call(context.Background(), name, json.RawMessage(in))
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(res)
		return string(b), nil
	}

	out, err := call("preset.list", "{}")
	if err != nil || !strings.Contains(out, `"builtin-bw"`) || !strings.Contains(out, p.ID) || strings.Contains(out, "thumb") {
		t.Fatalf("list = %s, %v", out, err)
	}
	if out, err = call("preset.rename", `{"id":"`+p.ID+`","name":"Day"}`); err != nil || !strings.Contains(out, `"Day"`) {
		t.Errorf("rename = %s, %v", out, err)
	}
	if _, err = call("preset.rename", `{"id":"builtin-bw","name":"x"}`); err == nil {
		t.Error("renamed a built-in")
	}
	exp := filepath.Join(root, "day.torufx")
	if out, err = call("preset.export", `{"id":"`+p.ID+`","path":`+jsonStr(exp)+`}`); err != nil || !strings.Contains(out, "day.torufx") {
		t.Errorf("export = %s, %v", out, err)
	}
	if out, err = call("preset.import", `{"path":`+jsonStr(exp)+`}`); err != nil || !strings.Contains(out, `"Day"`) {
		t.Errorf("import = %s, %v", out, err)
	}
	if _, err = call("preset.delete", `{"id":"`+p.ID+`"}`); err == nil {
		t.Error("delete without confirm")
	}
	if _, err = call("preset.delete", `{"id":"`+p.ID+`","confirm":true}`); err != nil {
		t.Error(err)
	}
	if out, err = call("preset.luts", "{}"); err != nil || out != `{"luts":[]}` {
		t.Errorf("luts = %s, %v", out, err)
	}
	if _, err = call("preset.importLut", `{"path":""}`); err == nil {
		t.Error("empty path accepted")
	}
	if got := strings.Join(events, ","); got != "presets:changed,presets:changed,presets:changed" {
		t.Errorf("events = %s", got)
	}
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
