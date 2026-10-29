# Agent control (MCP + CLI)

Status: decided on Irori card #1 (blocker b12), 2026-10-29.

## Goal

Let AI agents (Claude Code and others) and scripts drive Toru: every
video editor tool first, then the rest of the app (capture, recording,
instant replay, Library, export). One command list inside Toru, reached
two ways:

- **MCP**: `toru.exe mcp` speaks MCP over stdio. Add it to Claude Code
  with `claude mcp add toru -- "%LOCALAPPDATA%\Programs\Toru\toru.exe" mcp`.
- **CLI**: `toru.exe ctl <command> [--json '{...}']` prints the result as
  JSON. `toru.exe ctl list` lists every command with its input schema.

Both talk to the running Toru over a Windows named pipe that only the
current user can open. No network port.

## Rules

- **Off by default.** Settings > General: "Let AI agents and scripts
  control Toru". When it's off, the pipe isn't created and `mcp` / `ctl`
  exit with a clear message saying how to turn it on.
- **One registry, every feature.** A feature isn't done until its actions
  are registered (add this to CONTRIBUTING.md's checklist). MCP tools and
  CLI commands are generated from the registry; nothing is hand-listed
  twice.
- **Editor commands go through the window's undo history**, so the person
  sees each change and can undo it. Drags become single calls with exact
  values.
- **Destructive commands** (delete a Library item, overwrite a file) say
  so in their description and need `"confirm": true` in their input.
- App text about this never compares Toru to other products.

## Design

### Registry (Go, `internal/agentctl`)

```go
type Command struct {
	Name        string          // "editor.split", "capture.screenshot"
	Description string          // one or two sentences; becomes the MCP tool description
	Input       json.RawMessage // JSON Schema for the input object
	Destructive bool
	Handler     func(ctx context.Context, input json.RawMessage) (any, error)
}
func (r *Registry) Register(c Command)
func (r *Registry) List() []Command
func (r *Registry) Call(ctx context.Context, name string, input json.RawMessage) (any, error)
```

Go services register their commands in `main.go` after construction (or
via a `RegisterAgentCommands(r *agentctl.Registry)` method on each
service). Names are `area.verb` in lower camel: `editor.*`, `capture.*`,
`record.*`, `replay.*`, `library.*`, `app.*`.

### Window commands (TS)

Commands that act on a window's state (the editor's project) are
registered by that window while it's open:

```ts
// frontend/src/lib/agent.ts
registerAgentCommand(name, { description, input /* JSON Schema */, destructive? }, handler: (input) => Promise<unknown>)
```

On mount it calls `AgentService.RegisterWindowCommands(windowId, specs)`;
Go adds them to the registry as proxies that emit `agent:call`
`{id, name, input}` to that window and wait (30s timeout) for
`AgentService.Respond(id, result, error)`. On unmount they are removed.
If several editor windows are open, editor commands take an optional
`window` input (from `editor.list`); default is the most recently focused.

### Pipe protocol

`\\.\pipe\toru-agent-<user SID>`, created with go-winio (already a
dependency) and an SDDL that allows only the current user. One JSON
request per line, one JSON response per line:

```json
{"id":1,"method":"list"}
{"id":2,"method":"call","name":"editor.split","input":{"t":12.5}}
→ {"id":2,"result":{...}}  or  {"id":2,"error":{"message":"..."}}
```

Results that are images (preview frames, screenshots) come back as
`{"image":{"mime":"image/png","base64":"..."}}` or a file path, so MCP
can return image content.

### Entry points (`toru.exe mcp`, `toru.exe ctl`)

Handled at the very top of `main()`, before Wails and SingleInstance, so
they never open a window or hand off to the running app. `mcp` runs an MCP
stdio server (the official Go SDK, `github.com/modelcontextprotocol/go-sdk`)
whose tools are the registry's `list`, refreshed on each `tools/list` (window
commands come and go), and whose `tools/call` forwards to the pipe. `ctl`
does one call and prints JSON. If Toru isn't running, both say so; `ctl
app.start` launches it.

### First command set

App-wide (Go): `app.status`, `app.openSettings`, `capture.screenshot`
(region / window / fullscreen, returns path + image), `record.start` /
`record.stop` / `record.status`, `replay.save`, `replay.status`,
`library.list` (shots, recordings, clips, projects), `library.open`,
`library.delete` (destructive), `export.copyToClipboard`, `editor.open`
(path or project id).

Editor (TS, registered by the Edit video window): read: `editor.get`
(the project JSON), `editor.frame` (PNG of the preview at time t),
`editor.media`; edit: one command per project op (`editor.addMedia`
from paths, `editor.addTrack`, `editor.insertClip`, `editor.moveClip`,
`editor.trimClip`, `editor.split`, `editor.removeClip`,
`editor.rippleDelete`, `editor.closeGap`, `editor.updateClip` (transform,
speed, audio, chroma, filters), `editor.setText`, `editor.removeText`,
`editor.setCrop`, `editor.undo`, `editor.redo`), plus `editor.seek`,
`editor.play`, `editor.pause`, `editor.export` (preset, path; returns the
written path).

## Work split (cards under #1)

| Card | Owns | Needs |
| --- | --- | --- |
| AC-1 Registry + pipe + window bridge | `internal/agentctl/**`, `frontend/src/lib/agent.ts`, Settings toggle, `main.go` wiring (lease) | — |
| AC-2 `mcp` + `ctl` entry points | `internal/agentctl/cli/**`, top of `main.go` (lease) | AC-1 protocol (this spec) |
| AC-3 App-wide commands | `RegisterAgentCommands` on existing services | AC-1 API (this spec) |
| AC-4 Editor commands | editor's `registerAgentCommand` calls (a file next to `EditVideo.tsx`) | v2-H (#19) window, AC-1 |

## Done

- With the toggle on, `claude mcp add toru -- toru.exe mcp` lists every
  command; an agent opens a clip in the editor, splits it, adds a PNG on a
  second track with green screen, exports, and the person sees each step
  and can undo it.
- `toru.exe ctl capture.screenshot --json '{"mode":"fullscreen"}'` prints
  the saved path.
- With the toggle off, nothing listens and both entry points explain how
  to turn it on.
- Another Windows user on the same PC can't open the pipe.
- `go vet`, `go test ./internal/...`, golangci-lint v2.12.2 and
  `bun run build` pass.
