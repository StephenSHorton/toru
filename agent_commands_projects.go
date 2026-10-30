package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/vid"
)

// agent_commands_projects.go registers the agent commands for the hub's
// Video Editor page: start an empty project, show the page, and list, open,
// rename or delete saved editor projects. Commands that act on an open
// project (editor.get, editor.split...) are the editor window's own.

// projectStore is what the commands use of *vid.VideoService.
type projectStore interface {
	ListProjects() ([]vid.ProjectSummary, error)
	RenameProject(id, name string) error
	DeleteProject(id string) error
}

// projectAgentDeps is what the project commands drive.
type projectAgentDeps struct {
	projects projectStore
	// newProject opens an editor window on an empty project, openPage the
	// hub's Video Editor page, openProject a saved project.
	newProject  func()
	openPage    func()
	openProject func(id string)
	// emit tells editor windows about a rename (project:renamed).
	emit func(name string, data any)
}

var propProjectID = map[string]any{"type": "string", "description": "A project's id from project.list."}

func registerProjectAgentCommands(reg *agentctl.Registry, d projectAgentDeps) {
	// known checks an id against the saved projects, so a typo gets a clear
	// error instead of an editor window that can't load it.
	known := func(id string) (vid.ProjectSummary, error) {
		if strings.TrimSpace(id) == "" {
			return vid.ProjectSummary{}, errors.New(`give "id" (project.list lists them)`)
		}
		list, err := d.projects.ListProjects()
		if err != nil {
			return vid.ProjectSummary{}, err
		}
		for _, p := range list {
			if p.ID == id {
				return p, nil
			}
		}
		return vid.ProjectSummary{}, errors.New("no project " + id + " (project.list lists them)")
	}
	byID := func(in json.RawMessage) (string, error) {
		v, err := decode[struct {
			ID string `json:"id"`
		}](in)
		return v.ID, err
	}

	reg.Register(agentctl.Command{
		Name:        "editor.new",
		Description: "Open the Edit video window on a new, empty project. Then add media with that window's editor.addMedia; the project saves itself once it has a file.",
		Handler: func(context.Context, json.RawMessage) (any, error) {
			d.newProject()
			return map[string]any{"opened": true}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "editor.page",
		Description: "Show Toru's Video Editor page (new project, saved projects, recent clips to edit) in the main window.",
		Handler: func(context.Context, json.RawMessage) (any, error) {
			d.openPage()
			return map[string]any{"opened": true}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "project.list",
		Description: "List the saved video editor projects, most recently changed first: id, name, length, clip count and missing files.",
		Handler: func(context.Context, json.RawMessage) (any, error) {
			list, err := d.projects.ListProjects()
			if err != nil {
				return nil, err
			}
			if list == nil {
				list = []vid.ProjectSummary{}
			}
			return map[string]any{"projects": list}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "project.open",
		Description: "Open a saved project in the Edit video window.",
		Input:       schema(map[string]any{"id": propProjectID}, "id"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			id, err := byID(in)
			if err != nil {
				return nil, err
			}
			if _, err := known(id); err != nil {
				return nil, err
			}
			d.openProject(id)
			return map[string]any{"opened": id}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "project.rename",
		Description: "Rename a saved project (an open editor window keeps the new name).",
		Input: schema(map[string]any{
			"id":   propProjectID,
			"name": map[string]any{"type": "string", "minLength": 1, "maxLength": 120},
		}, "id", "name"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}](in)
			if err != nil {
				return nil, err
			}
			if _, err := known(v.ID); err != nil {
				return nil, err
			}
			name := strings.TrimSpace(v.Name)
			if err := d.projects.RenameProject(v.ID, name); err != nil {
				return nil, err
			}
			if d.emit != nil {
				d.emit("project:renamed", map[string]string{"id": v.ID, "name": name})
			}
			return map[string]any{"id": v.ID, "name": name}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "project.delete",
		Description: "Delete a saved project for good (its videos, music and images stay where they are). Needs confirm: true.",
		Destructive: true,
		Input:       schema(map[string]any{"id": propProjectID, "confirm": propConfirm}, "id", "confirm"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			id, err := byID(in)
			if err != nil {
				return nil, err
			}
			if _, err := known(id); err != nil {
				return nil, err
			}
			return map[string]any{"deleted": id}, d.projects.DeleteProject(id)
		},
	})
}
