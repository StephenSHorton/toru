package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/presets"
)

// agent_commands_presets.go registers the agent commands for saved filters
// ("My filters", docs/video-editor-v3.md, User presets) and imported LUTs:
// list, rename, delete, import and export them. Saving a clip's effects as a
// filter and applying one to a clip are the editor window's own commands
// (editor.savePreset, editor.applyPreset), since they act on an open edit.

// presetStore is what the commands use of *presets.Service.
type presetStore interface {
	List() ([]presets.Preset, error)
	Rename(id, name string) (presets.Preset, error)
	Delete(id string) error
	Import(path string) (presets.Preset, error)
	Export(id, path string) (string, error)
	ListLuts() ([]presets.Lut, error)
	ImportLut(path string) (presets.Lut, error)
	DeleteLut(id string) error
}

type presetAgentDeps struct {
	presets presetStore
	// emit tells editor windows the list changed (presets:changed), so an
	// open Filters tab shows it.
	emit func(name string, data any)
}

// presetBrief is a preset as preset.list shows it: no thumbnail.
type presetBrief struct {
	ID      string           `json:"id"`
	Name    string           `json:"name"`
	Builtin bool             `json:"builtin,omitempty"`
	Effects []presets.Effect `json:"effects"`
}

func registerPresetAgentCommands(reg *agentctl.Registry, d presetAgentDeps) {
	changed := func() {
		if d.emit != nil {
			d.emit("presets:changed", nil)
		}
	}
	propID := map[string]any{"type": "string", "description": "A filter's id from preset.list."}
	propPath := func(what string) map[string]any {
		return map[string]any{"type": "string", "description": what}
	}

	reg.Register(agentctl.Command{
		Name: "preset.list",
		Description: "List the saved filters (My filters) and the built-in looks: id, name and their effects (types and params; keyframe times are fractions 0..1 of the clip's length). " +
			"Apply one to a clip with the editor window's editor.applyPreset.",
		Handler: func(context.Context, json.RawMessage) (any, error) {
			list, err := d.presets.List()
			if err != nil {
				return nil, err
			}
			out := make([]presetBrief, 0, len(list))
			for _, p := range list {
				out = append(out, presetBrief{ID: p.ID, Name: p.Name, Builtin: p.Builtin, Effects: p.Effects})
			}
			return map[string]any{"presets": out}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "preset.rename",
		Description: "Rename a saved filter (built-in looks can't be renamed).",
		Input: schema(map[string]any{
			"id":   propID,
			"name": map[string]any{"type": "string", "minLength": 1, "maxLength": presets.MaxNameLen},
		}, "id", "name"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}](in)
			if err != nil {
				return nil, err
			}
			p, err := d.presets.Rename(strings.TrimSpace(v.ID), v.Name)
			if err != nil {
				return nil, err
			}
			changed()
			return map[string]any{"id": p.ID, "name": p.Name}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "preset.delete",
		Description: "Delete a saved filter for good. Clips it was applied to keep their effects. Needs confirm: true.",
		Destructive: true,
		Input:       schema(map[string]any{"id": propID, "confirm": propConfirm}, "id", "confirm"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				ID string `json:"id"`
			}](in)
			if err != nil {
				return nil, err
			}
			if err := d.presets.Delete(strings.TrimSpace(v.ID)); err != nil {
				return nil, err
			}
			changed()
			return map[string]any{"deleted": v.ID}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "preset.import",
		Description: "Add a shared filter from a .torufx file (by absolute path) to My filters. Returns its new id.",
		Input:       schema(map[string]any{"path": propPath("Absolute path of the .torufx file.")}, "path"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				Path string `json:"path"`
			}](in)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(v.Path) == "" {
				return nil, errors.New(`give "path"`)
			}
			p, err := d.presets.Import(v.Path)
			if err != nil {
				return nil, err
			}
			changed()
			return map[string]any{"id": p.ID, "name": p.Name}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "preset.export",
		Description: "Write a filter to a .torufx file to share it (.torufx is added if missing; an existing file is never overwritten). Returns the path written.",
		Input: schema(map[string]any{
			"id":   propID,
			"path": propPath("Absolute output path."),
		}, "id", "path"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				ID   string `json:"id"`
				Path string `json:"path"`
			}](in)
			if err != nil {
				return nil, err
			}
			out, err := d.presets.Export(strings.TrimSpace(v.ID), v.Path)
			if err != nil {
				return nil, err
			}
			return map[string]any{"path": out}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "preset.luts",
		Description: "List the imported .cube LUTs: id (what a lut effect's \"lut\" param takes), name and size.",
		Handler: func(context.Context, json.RawMessage) (any, error) {
			list, err := d.presets.ListLuts()
			if err != nil {
				return nil, err
			}
			return map[string]any{"luts": list}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "preset.importLut",
		Description: "Import a .cube 3D LUT (by absolute path): it's checked and copied into Toru's LUT folder. Returns its id; " +
			"put it on a clip with editor.addEffect {type: \"lut\", params: {lut: id}}.",
		Input: schema(map[string]any{"path": propPath("Absolute path of the .cube file.")}, "path"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				Path string `json:"path"`
			}](in)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(v.Path) == "" {
				return nil, errors.New(`give "path"`)
			}
			l, err := d.presets.ImportLut(v.Path)
			if err != nil {
				return nil, err
			}
			changed()
			return l, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "preset.deleteLut",
		Description: "Delete an imported LUT for good. Clips that use it draw without it. Needs confirm: true.",
		Destructive: true,
		Input:       schema(map[string]any{"id": map[string]any{"type": "string", "description": "From preset.luts."}, "confirm": propConfirm}, "id", "confirm"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				ID string `json:"id"`
			}](in)
			if err != nil {
				return nil, err
			}
			if err := d.presets.DeleteLut(v.ID); err != nil {
				return nil, err
			}
			changed()
			return map[string]any{"deleted": v.ID}, nil
		},
	})
}
