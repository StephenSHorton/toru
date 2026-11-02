package voice

import (
	"os"
	"strings"
	"testing"
)

func TestPresetList(t *testing.T) {
	ids := map[string]bool{}
	for _, p := range Presets() {
		if ids[p.ID] {
			t.Fatalf("duplicate id %q", p.ID)
		}
		ids[p.ID] = true
		if p.Name == "" || p.Description == "" {
			t.Errorf("%s: no name or description", p.ID)
		}
		if _, err := Chain(p.ID, "C:/m/sh.rnnn"); err != nil {
			t.Errorf("%s: %v", p.ID, err)
		}
		// Live presets can't need the model or a second pass.
		if p.Live {
			if c, err := Chain(p.ID, ""); err != nil || strings.Contains(c, "arnndn") || NeedsLoudness(p.ID) {
				t.Errorf("live %s: %q %v", p.ID, c, err)
			}
		}
	}
	for _, id := range []string{Off, Light, Strong} {
		if !IsLive(id) {
			t.Errorf("%s must stay live: the recorder uses it", id)
		}
	}
	if IsLive("nope") || IsLive("") {
		t.Error("unknown ids aren't live")
	}
	if _, err := Chain("nope", ""); err == nil {
		t.Error("unknown preset accepted")
	}
	if _, err := Chain(Clean, ""); err == nil {
		t.Error("Clean voice ran without the model")
	}
}

// The recorder's chains must not change under it: a mic set to "light" or
// "strong" keeps the sound it had before the presets moved here.
func TestLiveChainsUnchanged(t *testing.T) {
	for id, want := range map[string]string{
		Off:    "",
		Light:  "highpass=f=80,afftdn=nr=12:nf=-50:tn=1",
		Strong: "highpass=f=100,afftdn=nr=24:nf=-45:tn=1,agate=threshold=0.01:ratio=4:attack=5:release=300:range=0.08",
	} {
		if got, _ := Chain(id, ""); got != want {
			t.Errorf("%s = %q, want %q", id, got, want)
		}
	}
}

func TestFilterPath(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Users\Blake\AppData\Local\toru\voice\sh.rnnn`: `'C\:/Users/Blake/AppData/Local/toru/voice/sh.rnnn'`,
		`C:\Users\O'Neil\sh.rnnn`:                         `'C\:/Users/O'\''Neil/sh.rnnn'`,
	} {
		if got := FilterPath(in); got != want {
			t.Errorf("FilterPath(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestParseMeasured(t *testing.T) {
	out := `[Parsed_loudnorm_9 @ 0000]
{
	"input_i" : "-21.40",
	"input_tp" : "-2.52",
	"input_lra" : "3.30",
	"input_thresh" : "-32.29",
	"output_i" : "-16.68",
	"output_tp" : "-1.50",
	"output_lra" : "2.70",
	"output_thresh" : "-27.75",
	"normalization_type" : "dynamic",
	"target_offset" : "0.68"
}
`
	m, err := ParseMeasured(out)
	if err != nil {
		t.Fatal(err)
	}
	if (m != Measured{I: "-21.40", TP: "-2.52", LRA: "3.30", Thresh: "-32.29", Offset: "0.68"}) {
		t.Fatalf("%+v", m)
	}
	if !strings.Contains(Loudness(m), "measured_I=-21.40:measured_TP=-2.52") {
		t.Fatal(Loudness(m))
	}
	silent := strings.ReplaceAll(out, `"-21.40"`, `"-inf"`)
	if _, err := ParseMeasured(silent); !IsSilent(err) {
		t.Fatalf("silent: %v", err)
	}
	if _, err := ParseMeasured("no json here"); err == nil {
		t.Fatal("parsed nothing")
	}
	bad := strings.ReplaceAll(out, `"0.68"`, `"0.68:x=1"`)
	if _, err := ParseMeasured(bad); err == nil {
		t.Fatal("accepted a value that would inject a filter option")
	}
}

func TestModelPath(t *testing.T) {
	dir := t.TempDir()
	prev := modelDir
	modelDir = func() string { return dir }
	modelMu.Lock()
	modelPath = ""
	modelMu.Unlock()
	defer func() {
		modelDir = prev
		modelMu.Lock()
		modelPath = ""
		modelMu.Unlock()
	}()
	if ModelSHA256() != "70bb6685eb0c2a1d18e2918dca3fbfbd39317010b1802eb1b6ea73a92f3fdec0" {
		t.Fatalf("embedded model changed: %s (update models/README.md)", ModelSHA256())
	}
	p, err := ModelPath()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || !strings.HasPrefix(string(b), "rnnoise-nu model file version 1") {
		t.Fatalf("model file %s: %v", p, err)
	}
	// A damaged copy is written again.
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	modelMu.Lock()
	modelPath = ""
	modelMu.Unlock()
	if p2, err := ModelPath(); err != nil || p2 != p {
		t.Fatalf("%s %v", p2, err)
	}
	if b, _ := os.ReadFile(p); len(b) != len(model) {
		t.Fatal("damaged model not replaced")
	}
}
