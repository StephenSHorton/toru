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
registerAgentCommand(name, { description, input /* JSON Schema */, destructive? }, handler: (input, { caller }) => Promise<unknown>)
```

On mount it calls `AgentService.RegisterWindowCommands(windowId, specs)`;
Go adds them to the registry as proxies that emit `agent:call`
`{id, name, input, caller}` to that window (`caller` is the MCP client's
name, or `toru-ctl`, so a window can say which agent did what) and wait (30s timeout) for
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

A call may carry `"client"`: who is calling. `toru.exe mcp` sends the MCP
client's own name from its `initialize` request (`clientInfo.name`, like
`claude-code`); `toru.exe ctl` sends `toru-ctl`. Go commands read it with
`agentctl.Caller(ctx)`. The board commands tag every change with it as
`agent:<client>` (characters a tag can't hold become `-`; no name is
`agent:unknown`), so the board shows which agent placed what.

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
`editor.rippleDelete`, `editor.closeGap`, `editor.duplicateClip`,
`editor.extractAudio` (a video clip's sound onto its own audio track; the
video clip is muted), `editor.saveAudio` (a clip's sound to .m4a or .wav),
`editor.updateClip` (transform,
speed, audio, chroma, filters), `editor.setText` (size in px or s/m/l, font),
`editor.listFonts`, `editor.removeText`, `editor.addTransition` /
`editor.removeTransition` (at a clip's cut: dissolves, dips, wipes, slides,
zoom, blur),
`editor.setCrop`, `editor.undo`, `editor.redo`), several clips at once
(`editor.selectClips`, `editor.moveClips`, `editor.removeClips` (ripple
optional), `editor.duplicateClips`, `editor.setClipsMuted`), the clip
clipboard (`editor.copyClips`, `editor.cutClips`, `editor.pasteClips` at t
or the playhead, `editor.pasteAttributes`: another clip's effects,
transform and sound, not its timing), time effects
(`editor.setSpeedCurve` (preset or points, audio follow/mute),
`editor.freezeFrame`, `editor.setReverse`, `editor.setSlowMotion`), plus `editor.seek`,
`editor.play`, `editor.pause`, `editor.export` (preset, path; returns the
written path).

Effects (TS, editor v3, `frontend/src/trim/agentCommandsFx.ts`):
`editor.effects` (every effect type's params as JSON Schema, and which can
be keyframed), `editor.addEffect`, `editor.setEffect` (params, on/off),
`editor.removeEffect`, `editor.moveEffect`, `editor.setKeyframe` /
`editor.removeKeyframe` (an effect param or a clip property: x, y, scale,
opacity, rotation, volume; times in timeline seconds),
`editor.applyPreset` (append as a group, or replace) and
`editor.savePreset`. On a v3 clip, use these rather than
`editor.updateClip`'s v2 `chroma`/`filters`.

Saved filters and LUTs (Go, `agent_commands_presets.go`): `preset.list`
(saved filters and the built-in looks), `preset.textStyles` (the Text
library's saved styles, presets of kind "text"), `preset.rename`,
`preset.delete` (destructive), `preset.import` / `preset.export` (`.torufx`,
filters and text styles; an imported file keeps its kind),
`preset.luts`, `preset.importLut` (a `.cube` file; returns the id a `lut`
effect takes) and `preset.deleteLut` (destructive).

Whiteboards (Go, `agent_commands_boards.go`; docs/whiteboard.md section 2).
The board document lives in Go, so these work with the board window open
or closed. Every change is one board op tagged `agent:<client>`, goes
through the board's undo history like a person's, and is sent to open
board windows as a `board:op` event:

- `board.list`, `board.create` (`open: true` also shows it; `template`
  starts it ready to fill), `board.open` (shows the board's window).
- `board.templates`: the brainstorm templates `board.create` takes
  (`mind-map`, `affinity`, `retro`, `pros-cons`, `kanban`, `swot`). They
  live in `internal/board/templates.go`; the board window's Template menu
  places the same ones.
- `board.read`: the board as JSON, or `format: "markdown"` for an outline
  (frames as headings, checklists as `- [ ]`, tables as tables, links as
  `A → B`).
- `board.catalog`: every block type with its props as JSON Schema, from
  `internal/board/catalog.json` (the window reads the same file): `sticky`,
  `text`, `card`, `checklist`, `poll`, `rating`, `table`, `tag-list`,
  `image`, and `frame`, which is placed under `frames`, not as a block.
  Props are checked against it on add and update.
- `board.image.import` (`agent_commands_board_images.go`): copies a picture
  file or a Library screenshot (`libraryId` from `library.list`) into the
  board images and returns the `asset` an image block's props take.
- `board.add` (blocks and frames, many in one call; ids of your own are
  allowed; blocks without x, y go in rows below what's there),
  `board.update` (props merged key by key, `null` removes; frames; the
  board's name), `board.move` (x, y or dx, dy; a group moves together, a
  frame takes what's in it; `z: front/back`), `board.group` (group,
  ungroup, or put blocks in a frame).
- `board.connect` / `board.disconnect` (lines between blocks or frames).
- `board.draw` (`agent_commands_board_ink.go`): freehand ink, many strokes
  in one change. Each stroke is `points` (`[x, y]` or `[x, y, pressure]`,
  pressure 0.5 when left out), `color` (a pen token: `ink-white`,
  `ink-blue`, `ink-red`, `ink-yellow`, `ink-green`, or the colour's name
  alone; white when left out), `width` (`fine`, `medium`, `bold` or a
  number; fine when left out) and an optional `frame`. The pen in the
  window has no agent action (its parts are `aria-hidden`), so agents draw
  here; strokes come off with `board.delete`.
- `board.layout`: `grid`, `tree` (follows the lines) or `cluster` (by a
  block's first `props.tags` entry). The window's Layout menu and its
  `board.arrange` window command give the same positions on the board open
  in the window (`frontend/src/whiteboard/layout.ts`, checked against Go by
  `bun run test:templates`).
- `board.delete` (destructive, needs `confirm: true`): items, or the whole
  board when `items` is left out.
- `board.export`: `toruboard` or `markdown` to a path; an existing file is
  replaced only with `confirm: true`. PNG, PDF and HTML come with the
  export card.

- `board.shared`, `board.join`, `board.leave`
  (`agent_commands_board_share.go`): boards shared between Toru installs in
  this Toru's room (docs/whiteboard.md section 2, "Shared boards").
  `board.shared` lists the person's boards they share (and who has each
  open) and boards others in the room share; `board.join` makes this
  Toru's live copy of one and returns its id, which every board command
  then takes: each change goes to the board's owner, under the agent's name
  and its person's (`agent:Claude Code (Blake)`). Sharing itself is the
  person's: only the board window's Share button does it.

`board.snapshot` and `board.act` (the window's live controls, through UI
SDK's `createAgent`) are the board window's own commands while it is open:
`board.act` fills its blocks as a person would (type in a sticky, tick a
checklist item, vote in a poll, set a table cell or a rating, change a
colour, rename or add poll options and table columns, give an image block a
new picture). Removing a checklist item, poll option or table column is
marked `data-agent-confirm`: it needs the person.
Their changes are saved as board ops under the agent's name
(`agent:<caller>`), before the command answers, like `board.arrange`'s.

Every change to a board, from these commands, the window's live controls
or the person, reaches open board windows as one `board:op` event
`{board, op}`, sent by the store in rev order (`board.Store.OnOp`), and
goes into the board's one undo history: Ctrl+Z in the window undoes an
agent's change as well as the person's.

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
