package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/vid"
)

type fakeProjects struct {
	list    []vid.ProjectSummary
	renamed map[string]string
	deleted []string
}

func (f *fakeProjects) ListProjects() ([]vid.ProjectSummary, error) { return f.list, nil }
func (f *fakeProjects) RenameProject(id, name string) error {
	f.renamed[id] = name
	return nil
}
func (f *fakeProjects) DeleteProject(id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}

type projectCalls struct {
	news, pages int
	opened      []string
	emitted     []string
}

func newProjectReg(f *fakeProjects, c *projectCalls) *agentctl.Registry {
	reg := agentctl.NewRegistry()
	registerProjectAgentCommands(reg, projectAgentDeps{
		projects:    f,
		newProject:  func() { c.news++ },
		openPage:    func() { c.pages++ },
		openProject: func(id string) { c.opened = append(c.opened, id) },
		emit: func(name string, data any) {
			b, _ := json.Marshal(data)
			c.emitted = append(c.emitted, name+" "+string(b))
		},
	})
	return reg
}

func runProject(t *testing.T, reg *agentctl.Registry, name, input string) (string, error) {
	t.Helper()
	if input == "" {
		input = "{}"
	}
	res, err := reg.Call(context.Background(), name, json.RawMessage(input))
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(res)
	return string(b), nil
}

func TestProjectAgentCommands(t *testing.T) {
	f := &fakeProjects{list: []vid.ProjectSummary{{ID: "proj-a", Name: "clutch"}, {ID: "proj-b", Name: "Untitled"}}, renamed: map[string]string{}}
	c := &projectCalls{}
	reg := newProjectReg(f, c)

	if _, err := runProject(t, reg, "editor.new", ""); err != nil || c.news != 1 {
		t.Fatalf("editor.new: %v, news %d", err, c.news)
	}
	if _, err := runProject(t, reg, "editor.page", ""); err != nil || c.pages != 1 {
		t.Fatalf("editor.page: %v, pages %d", err, c.pages)
	}
	out, err := runProject(t, reg, "project.list", "")
	if err != nil || !strings.Contains(out, `"proj-a"`) || !strings.Contains(out, `"clutch"`) {
		t.Fatalf("project.list: %s %v", out, err)
	}

	if _, err := runProject(t, reg, "project.open", `{"id":"proj-b"}`); err != nil || len(c.opened) != 1 || c.opened[0] != "proj-b" {
		t.Fatalf("project.open: %v %v", err, c.opened)
	}
	if _, err := runProject(t, reg, "project.open", `{"id":"proj-zzz"}`); err == nil || len(c.opened) != 1 {
		t.Errorf("project.open of an unknown id should fail without opening: %v", err)
	}

	if _, err := runProject(t, reg, "project.rename", `{"id":"proj-a","name":"  Ace round  "}`); err != nil || f.renamed["proj-a"] != "Ace round" {
		t.Fatalf("project.rename: %v %v", err, f.renamed)
	}
	if len(c.emitted) != 1 || c.emitted[0] != `project:renamed {"id":"proj-a","name":"Ace round"}` {
		t.Errorf("rename event: %v", c.emitted)
	}
	if _, err := runProject(t, reg, "project.rename", `{"id":"nope","name":"x"}`); err == nil {
		t.Error("project.rename of an unknown id should fail")
	}

	if _, err := runProject(t, reg, "project.delete", `{"id":"proj-a"}`); err == nil || len(f.deleted) != 0 {
		t.Fatalf("project.delete without confirm: %v %v", err, f.deleted)
	}
	if _, err := runProject(t, reg, "project.delete", `{"id":"proj-a","confirm":true}`); err != nil || len(f.deleted) != 1 {
		t.Fatalf("project.delete: %v %v", err, f.deleted)
	}
	for _, cmd := range reg.List() {
		if cmd.Name == "project.delete" && !cmd.Destructive {
			t.Error("project.delete must be destructive")
		}
	}
}
