# Whiteboard (plan)

Status: plan, whiteboard#1, 2026-11-27. No code yet.

A whiteboard and brainstorming tool inside Toru. Every piece of it is built
from Vercel's UI SDK (`@ui-sdk/core` + `@ui-sdk/react`, github.com/vercel-labs/ui),
so agents can read it, compose it and drive it. People's agents connect over
Toru's existing MCP entry point and build boards with the person watching.

Toru is a **Wails v3** app (Go + WebView2 + React/Vite), not Tauri. Everything
below is planned for Wails.

## What was read

- UI SDK at `0.1.0-beta.18` (Apache-2.0): `packages/core` (components,
  foundation behaviors, `page/agent`, `page/history`), the `ui-sdk` skill and
  its references (`agent-ready.md`, `styles.md`), and the canvas experiments
  in `examples/experiments/react/` (`canvas.tsx`, `board.tsx`, `edge-path.ts`).
- `ui-sdk-glass` (Stephen's fork of Liquid Glass UI): every control is a WebGL
  lens with spring physics on top of UI SDK `beta.17`. Used as a reference for
  how UI SDK was wired before, not for the look.
- Toru: `docs/agent-control.md` and `internal/agentctl` (the MCP/CLI
  registry), `frontend/src/lib/agent.ts` (window commands), `windows.go` and
  `App.tsx` (one Wails window per `?view=`), `frontend/src/index.css` (the
  design tokens), `internal/presets` (an `%AppData%\toru\…` JSON store).

## 1. What UI SDK's generative UI gives a whiteboard

UI SDK has no "generative UI" renderer or JSON spec. What it has is the
three things that make UI composable by an agent, and that is what this
whiteboard is built on:

1. **Behaviors and unstyled components.** 50 components (checkbox, select,
   slider, rating, tabs, tree, token input, toolbar, menu, popover, dialog,
   toast, color picker, timeline…) and the behaviors under them (focus,
   selection, drag and drop, direct manipulation). Every block on a board is
   one of these, so it has real keyboard, focus and ARIA for free.
2. **Canvas behaviors built for whiteboards.** `@ui-sdk/core` ships
   `createViewport` (pan/zoom one world layer, screen↔canvas points, fit,
   reveal), `createConnect` (drag a line from port to port, keyboard too),
   `createMove`, `createResize`, `createLasso`, `createSelection` and
   `spatialMove`. The `canvas.tsx` and `board.tsx` experiments are a working
   node canvas and a free-position board made from them. All of these are
   `@experimental`.
3. **An agent bridge.** `createAgent()` gives `snapshot()` (every component,
   its label, state and actions), `act({ steps })` (run steps by label, like
   `{ ref: "Vote", action: "press" }`) and `query()`. Changes made through it
   arrive with `details.source === "agent"` and can be refused;
   `data-agent-confirm` makes a person approve a step. `createHistory`
   records every change with who made it, for undo and rewind.

What that gives the board:

- **Blocks are live UI, not pictures.** A sticky note is text; a poll is a
  real radio group people and agents vote in; a checklist ticks; a table
  sorts; a slider scores ideas. An agent fills and presses them the same way
  a person does, and Toru sees which was which.
- **A fixed block catalog, rendered from board data.** Agents compose boards
  by placing typed blocks (`{ type: "poll", props: {...}, x, y, w, h }`), not
  by sending markup. Toru renders each type with UI SDK parts. The board
  window holds Wails bindings, so agent-sent HTML or scripts never run in
  it. New block types are added as code: an agent with the `ui-sdk` skill
  writes a primitive in the repo, and it ships with Toru.
- **Freehand ink is Toru's own primitive.** UI SDK has no ink or stroke
  behavior. A pen block is written as a `definePrimitive` on UI SDK's pointer
  drag session, drawing SVG paths, so the board stays UI SDK only. Toru's
  existing konva editor is not used here.

### Block catalog v1 (built, whiteboard#6)

- **One file, two readers.** `internal/board/catalog.json` lists every block
  type with its label, default size and props as JSON Schema. Go embeds it
  (`internal/board/catalog.go`: `board.catalog`, and `CheckProps` for what
  agents send); the window imports it (`frontend/src/whiteboard/catalog.ts`).
  Schemas use only the keywords Go checks; a Go test fails on any other.
- **Types:** `sticky`, `text`, `card`, `checklist`, `poll`, `rating`,
  `table`, `tag-list`, `image`, `frame`. `frame` is listed for its schema but
  stays the board file's `frames` list, not a block.
- **Renders:** `frontend/src/whiteboard/blocks.tsx`, one per type, from UI SDK
  parts (Textarea, Input, Checkbox, RadioGroup, Rating, TokenInput, FileInput,
  Popover, Button, Collapsible). Every control is named after its block, like
  "Rate How useful is this board?" or "Owners row 2 Owner", and no control's
  name holds every word of another's (the bridge finds controls by whole
  words; `test:blocks` checks it).
- **Editing in place:** sticky, card and frame have a colour swatch row
  ("Sticky colour: …"); a poll's "Options" and a table's "Columns" open a
  list to rename, remove and add ("Option 2 text in …", "Remove column 3
  from …"); checklist items have a remove cross; a filled image block has
  Replace and Library over its picture ("New picture file for …"). Swatches,
  crosses and the image buttons show while the block is hovered, selected
  or in use. Every remove control carries `data-agent-confirm`, so an
  agent's removal waits for the person (and fails with "Needs a person"
  while the window's bridge has no `allow`).
- **Click selects, double-click edits (whiteboard#30):** a press or drag
  anywhere on a block, its text fields included, selects and moves it.
  Double-click (or Enter on the focused block) edits its text, with the
  caret where you double-clicked; Escape or a press outside stops editing
  and leaves the block selected. While a block edits, presses and drags in
  it go to its text and never move it. Quick controls (ticks, votes, stars,
  buttons, swatches) still take one press. The rule is the board's, by
  selector (`blockTextFields` in `board.ts`), so a new block type gets it
  for free; `board.css` stops text fields taking the pointer until their
  block edits, and the hover cursor is "move" on blocks and "text" only in
  a field being edited. Agents fill fields through the bridge as before.
- **Images** are copied into `%AppData%\toru\boards\images`, named by their
  SHA-256, from a chosen or dropped file or a Library screenshot
  (`board.ImageService`, served at `/__board/image/<name>`). Agents import one
  with `board.image.import` and place it as `{"asset": …}`.
- **Checks:** `bun run test:blocks` renders the sample board (one block of
  every type, `sample-board.json`) and has an agent fill every type through
  UI SDK's bridge; Go's catalog test checks every sample block against the
  catalog. A new type is a catalog entry, a render, a sample block and its
  `act` steps in that check.

## 2. How people's agents connect over MCP

Toru already has one: `toru.exe mcp` (stdio MCP) and `toru.exe ctl`, both
reaching the running Toru over a named pipe only the current user can open.
Always on while Toru runs: no setting; Settings > Connect your agent shows
the commands to add it. No network port. The whiteboard adds its commands
to that registry; it opens nothing new.

```
claude mcp add --scope user toru -- "%LOCALAPPDATA%\Programs\Toru\toru.exe" mcp
```

**Board commands (Go, always available, window open or not).** The board
document lives in Go, so an agent can build a whole board without the
window, and the window shows each change live when it is open:

| Command | Does |
| --- | --- |
| `board.list`, `board.create`, `board.open` | Find, make, show a board |
| `board.read` | The board as JSON, or as a Markdown outline for reading |
| `board.catalog` | Every block type with its props schema, so an agent knows what it can place |
| `board.add`, `board.update`, `board.move`, `board.group` | Place and change blocks, many in one call |
| `board.connect`, `board.disconnect` | Lines between blocks |
| `board.draw` | Freehand ink: strokes through points, in a pen colour token and width |
| `board.shape` | Shapes on the ink layer: rectangles, ellipses, lines, arrows, text labels and emoji, each from one point to another, which the person can then select, move and reshape |
| `board.layout` | Arrange blocks (grid, tree, cluster by tag) |
| `board.delete` | Destructive: needs `"confirm": true` |
| `board.export`, `board.import` | Any format in section 3, to a path; a `.toruboard` back in |
| `board.block.define`, `.list`, `.update`, `.remove`, `.save` | The person's own block types, designed by agents (below) |

**Live controls (window commands, while the board is open).**
`board.snapshot` and `board.act` pass straight through to UI SDK's
`createAgent` in the board window, so an agent can vote in a poll, tick a
checklist or move a slider exactly as a person would. Board commands carry
the heavy lifting because the bridge's snapshot stops at 100 components and
window commands time out (30 s default).

### Custom blocks built by agents (built, whiteboard#27)

People ask their agent for a new kind of block ("a feature idea with
votes", "a bug report") and keep it. The plan's rule stands: **no markup
or code from an agent is ever run.** A custom block is data only.

- **A definition** (`board.BlockDef`, `internal/board/blockdef.go`):
  `type` (like `feature-idea`, never a built-in name), `label`, `icon`
  (one of Toru's lucide icons), default `w`/`h`, `props` (a closed JSON
  Schema in the catalog's keyword subset), `defaults` (a new block's
  props, checked against it), `layout`, optional `nameProp`, and
  `version` and `origin`, which Toru sets.
- **The layout** is a tree of parts from `internal/board/blockparts.json`,
  which Go and the window both read: `stack` (rows, columns, tinted
  panels), `heading`, `text`, `badge`, `icon`, `divider`, and parts bound
  to a prop: `field`, `checkbox`, `choice`, `counter` (votes), `slider`,
  `rating`, `progress`, `checklist`, `list`, `tags`, `poll`, `table`,
  `image`. Each part names the kinds of prop it takes, and Go refuses a
  definition with an unknown part or field, a wrong binding, an unchecked
  schema keyword, defaults that break the schema, more than 200 parts or
  12 levels. Text templates (`"Votes: {votes}"`) are plain text.
- **The library** is `%AppData%\toru\blocks\<type>.json`
  (`internal/board/library.go`). `board.block.define` saves version 1
  with a new origin; `board.block.update` saves the next version, same
  origin; `board.block.remove` (needs `confirm`) leaves boards' copies
  alone; a file that can't be read is listed under `broken`.
  `board.catalog` lists the library under `custom`, with the parts, prop
  kinds and icons, and a board's own types under `boardTypes` given `id`.
  The window gets the library from `BlockLibraryService` and
  `blocks:changed`.
- **Boards carry copies.** The board file has `types`: every custom type
  its blocks use. `board.add` (or the window placing one of the person's
  blocks) copies the definition in with the same op, so undo takes it off
  again. Ops carry `types`/`dropTypes`; a type already on the board is
  replaced only by a newer version of the same origin, and only when every
  block of it still fits. A different block with the same name is
  refused. So a board renders the same on any Toru, in shared boards, and
  in `.toruboard`, HTML, PNG and PDF exports.
- **Saving someone else's block:** a custom block the person's library
  lacks shows "Save to my blocks" (`board.block.save` for agents); a
  name clash saves it under a free name (`as`).
- **Never a crash.** A definition that fails its check when a board file
  is read is left out, so the board still opens. A block whose type the
  window doesn't have, or whose layout throws while drawing, shows as a
  plain placeholder card (`custom-blocks.tsx`).
- **Checks:** `go test ./internal/board` (validation, bad definitions,
  library, ops/undo/file/import round trips), `go test -run
  TestBoardBlock .` (the commands), `go test -run TestMCPBuildsCustomBlocks
  .` (an MCP client on the real `toru.exe mcp` code builds a feature idea
  and a bug report and exports the board; `TORU_CUSTOM_BLOCKS_OUT=<folder>`
  keeps the PNG), `bun run test:custom-blocks` (the window draws and an
  agent fills every part; text stays text; placeholders).
- **Gallery (built, whiteboard#31):** the window's block gallery
  (whiteboard#26, `BlockGallery.tsx`) has a "Your blocks" section under
  Blocks and Frames, one tile per type in `libraryBlocks()`
  (`custom-types.ts`) showing its icon and label; no section while the
  library is empty. It redraws on every library change
  (`subscribeTypes`), so a type an agent defines shows up while the window
  is open (`blocks:changed` → `block-library.ts`). Dragging or pressing a
  tile places a block with the type's `defaults` through `LiveBoard.edit`;
  `live.ts` copies the type onto the board in the same op, so one undo
  takes both off. Checked by `bun run test:gallery` (section 6).

### Live board (built, whiteboard#9)

- **Go owns the board; the window keeps a copy.** `board.Service`
  (`internal/board/service.go`, bound to the window) loads a board and
  takes the window's changes, undo and redo. The store sends every change
  from anyone as one `board:op` event, in rev order (`Store.OnOp`, wired in
  `main.go`). The window (`frontend/src/whiteboard/live.ts`) applies each
  op once, by its rev: an op it has is skipped, one after a gap waits, and
  a gap that doesn't fill reloads the board.
- **The person's changes show at once** and are sent as ops: a move,
  resize, delete (with its lines), template or layout is one op; typing is
  gathered until a pause. An edit stays on screen until Go's copy has it,
  so an agent's op landing in between never takes it away. A refused op is
  dropped and the board reloads.
- **Mapping** (`document.ts`): frames are canvas blocks of type `frame`,
  strokes are blocks with `ink`, links are lines between `block:side`
  ports. What the canvas doesn't carry (a block's frame, group, rotation; a
  link's label and props) is kept from Go's item.
- **Undo:** Ctrl+Z, Ctrl+Y and Ctrl+Shift+Z in the window go to Go's one
  history for the board, the person's and agents' changes alike, inside a
  block's text too.
- **Who made it:** each block and frame shows "You" or the agent's name
  above it (`createdBy`); its tooltip adds who changed it last. Changes an
  agent makes through `board.act` or `board.arrange` carry its name
  (`agent:call` now says who called).
- **Checks:** `bun run test:live` replays Go's ops
  (`testdata/live-ops.json`, written by Go) shuffled, doubled and with a
  gap, and drives the real window against a store in the page; the ops the
  window sent go to `testdata/window-ops.json`, which Go's `TestWindowOps`
  applies through the real store.
- **Demo in a browser:** `cd frontend && bun x vite --mode board-demo
  --port 47801`, then `/?view=whiteboard&id=brd-demo&link=http`. Vite
  starts Go's real store and board commands (`board_live_demo_test.go`) on
  a temporary folder, and while a window watches, an agent named Claude
  Code builds a board, clears what it made and builds it again.

### Properties panel (built, whiteboard#29)

- **Down the right side, over the canvas** (`frontend/src/whiteboard/PropsPanel.tsx`,
  `props-panel.css`): it shows what's selected and slides away (inert) when
  nothing is; the canvas never moves or shrinks. F6 hops from the board
  to the panel and back to the selected block.
- **A form from the schema.** A block's props are fields made from its
  catalog type's JSON Schema keywords (enum, items, properties, required,
  min/max, lengths), so a type added to the catalog later (or an AI-built
  type that `blockType()` finds, whiteboard#27) gets a form with no new
  code. The panel draws again when custom types arrive (`subscribeTypes`),
  since the library can load after the board. A schema it can't map is a JSON box. Under the props: X, Y, width,
  height, rotation, the frame it sits in, Locked, and who made it and last
  changed it. A frame shows its name and colour; ink its colour and width;
  a block lists its lines, each a button that picks the line.
- **Lines** are picked by pressing them (`board-line-hit`, a wide
  see-through stroke) or from a block's list. A line shows its label above
  its middle and its style from the link's props: `start` and `end`
  (`"arrow"` or none), `dash` (`"dashed"`, `"dotted"`, or solid) and
  `color` (a block colour). `board.connect`'s `style` takes the same keys.
- **Several selected:** the props their types share (same keywords),
  marked "(mixed)" where they differ; a change goes to each. A list of
  words (tags) adds or removes the word on each, keeping their own.
- **One change, one undo:** every edit is one `LiveBoard.update`: one op to
  Go's store, the store `board.update` changes, so Ctrl+Z takes it back.
  The op is built from the board as it is once what's pending is sent, so
  it never puts back a value that changed meanwhile.
  Text and numbers are sent when the field is left or on Enter, never per
  key; while a field has unsent typing, Ctrl+Z is the field's own.
- **Lock** (`locked` on blocks, frames, strokes and links; `board.update`
  sets it): on the canvas a locked item can't be dragged, resized, arranged
  or deleted, by the person or by an agent using the window (`board.act`,
  `board.arrange`); it still selects, so it can be unlocked, and the
  panel's X and Y still move it. A locked line has no disconnect button.
  The shared board code holds it against agents too
  (`internal/board/lock.go`): any change from an agent that would move,
  resize, delete or unlock a locked item (or delete what a locked line
  hangs on) is refused with "… is locked, so it can't be moved: only the
  person can unlock it, in the board window". That covers `board.move`,
  `board.update`, `board.delete`, `board.layout` (which skips locked
  blocks unless they're named) and an agent driving the window. Agents can
  still lock things, and change a locked item's text and style. The
  person's own changes, and undo and redo, aren't held.
- **Rotation** is drawn (CSS `rotate`); moving, resizing, lassoing and
  ports still use the unturned box.
- **Checks:** `bun run test:props-panel` drives the real window against a
  store in the page and writes the ops it sent to
  `internal/board/testdata/panel-ops.json` (`--write`), which Go's
  `TestPanelOps` applies through the real store.

**Rules.**

- Every agent change is a board op tagged with its source (`agent:<name>`
  from the MCP client name, or a person). The window shows who placed each
  block and an agent's changes go through the same undo history as a
  person's.
- Deleting blocks or boards and overwriting an export need confirmation
  (`confirm: true` from the CLI; `data-agent-confirm` in the window).
- **Other people's agents** (a friend's Claude building on your board)
  work through shared boards (below): each person's agent talks to their
  own Toru over its own pipe, and Toru syncs the board over the room
  connection Toru already uses for Call and LAN. No MCP port is opened to
  the network.

### Shared boards (built, whiteboard#13)

- **Over the room, nothing else.** `internal/boardshare` rides the room
  Call and LAN use (`internal/stream/room.go`: sealed messages on public
  MQTT brokers, topic and key from the room code) as `board` messages
  (`internal/stream/board.go`, which never reads them and passes on only
  members heard in presence). It imports no networking package and opens
  no port or socket of its own (`TestNoNetworkingOfItsOwn`; the real-room
  check lists the process's listening ports before and after: none).
- **One owner.** The person who shares a board (the board window's Share
  button, "Share in room") keeps it and its one undo history. Everyone in
  the room sees it on their Whiteboards page under "Shared in your room"
  and joins it there (or their agent does, `board.join`): their Toru keeps
  a **copy** (`board.Board.MirrorOf`, `internal/board/mirror.go`).
- **Changes.** A change to a copy, by its person or their agents, goes to
  the owner's Toru as a request; the owner stamps who made it from the
  sender's name in the room (never from the request: their person is
  `person:<name>`, their agents `agent:<agent> (<name>)`), applies it
  through its own store, which checks it like any change, and answers with
  the op as applied. The owner sends every op on a shared board to the
  room; copies apply them exactly as applied (same rev, time and who made
  what) in rev order. Undo and redo from a copy step the owner's one
  history. Ops replace whole items, so when two people change the same
  block at once, the later change wins.
- **Catching up.** The owner says what it shares, and at which rev, every
  4 s; a copy that's behind, or has a gap in revs that doesn't fill,
  asks for the whole board again. A message too big for one room message
  (a whole board, a big picture) goes in 48 KB parts. Windows reload on a
  `reset` op when a copy is replaced whole.
- **Who's who.** "You" is always this Toru's person; the owner's person
  shows by name on copies (`person:<name>`), and this Toru's person, whom
  the owner knows by name, is "You" again on its copy.
- **Pictures** go by name on demand: whoever gets a change with a picture
  it lacks asks the peer it came from, and checks the bytes against the
  name. Only pictures a shared board shows (or that this Toru just sent)
  are given out. An image that fails to load in the window tries again for
  a while, so a picture that arrives late shows.
- **Ending.** "Stop sharing", leaving the room, or the owner not heard from
  for 13 s ends it: copies stop following and stay as ordinary boards,
  labelled whose they were. A copy's "Stop following" does the same for
  one copy. Joining again follows the same copy, unless it was changed
  after it stopped following: then it's kept as it is and the new copy
  gets an id of its own. Each share has a token, so a late "stopped
  sharing" never ends a newer share. Shares don't survive a restart:
  share again.
- **Checks:** `go test ./internal/boardshare` (two Torus on a fake room,
  out of order and losing messages: share, join, both people and both
  agents building, undo, catch-up, pictures, a forged board or source
  refused, leaving, rejoining); `go test ./internal/boardshare -run
  'TestRealRoom|TestTwoStreamServices' -args -e2e` does it on the real
  brokers, with a 2 MB picture, and through two of Toru's own room
  services (`stream.StreamService`).
- **Demo in a browser:** `cd frontend && bun x vite --mode share-demo
  --port 47820`, then `/share-demo.html`: two Torus with their own stores,
  Stephen's and Blake's, sharing a board through a fresh real room, side by
  side; each one's agent builds on it through its own Toru
  (`board_share_demo_test.go`).

## 3. Export formats

| Format | How it is made | For |
| --- | --- | --- |
| `.toruboard` | The board document as JSON (blocks, positions, links, ink, images inlined or beside it). Import reads it back | Native save, sending a board to someone, version control |
| PNG | Rasterized inside the board window: the world layer at fit bounds drawn to a canvas at 2× through an SVG `foreignObject` (reliable in WebView2's Chromium; a small library such as `modern-screenshot`). Works for boards larger than the screen | Pasting into chat, docs |
| PDF | WebView2's `PrintToPdf` on a print view of the board: vector text and lines. Wails alpha.98 and its `go-webview2` don't expose it, so the card adds that one COM call (`ICoreWebView2_7`) in Go; until then Wails' `Print()` with Microsoft Print to PDF | Printing, sharing a readable copy |
| HTML | One standalone file: the board JSON, Toru's UI SDK theme CSS and a bundled UI SDK web-components build inlined. Opens offline in any browser, still live (polls, checklists work) | Sharing a living board without Toru |
| Markdown | Outline: frames as headings, stickies and text as bullets, checklists as `- [ ]`, tables as tables, links as "A → B" lines | Notes, handing a board to an agent, issues |

Copy to clipboard (PNG and Markdown) goes on the same menu. SVG is not
planned: a DOM board has no faithful SVG path (`foreignObject` breaks in
most viewers).

### Export and import (built, whiteboard#10)

- **One path for the person and agents.** The board window's Export menu
  (`BoardExportService` in `board_export.go`: Save as each format, Copy as
  picture, Copy as Markdown, Import board…) and agents' `board.export` /
  `board.import` all go through `internal/boardexport`, so they make the
  same files. None needs the board window open; the window sends what's
  waiting to Go first.
- **`.toruboard`:** the board file plus an `images` object (asset name →
  base64), from `board.EncodePortable`. `Store.ImportFile` checks each
  picture's name is the SHA-256 of its bytes, copies it into the board
  images, and keeps the file's id unless a board has it (then a new
  `brd-` id): an import never replaces a board. Toru's own board files
  never carry `images`, and a plain board file still imports.
- **HTML:** `frontend/board-export.html` + `src/board-export` is the board
  window's canvas and blocks over a store in the page (`memoryLink`),
  built as one file (`vite.board-export.config.ts`, part of `bun run
  build`) into `dist/board-export.html`, which Go embeds. Go puts the board
  and its pictures (data: URLs) in its `#toru-board` script; `json.Marshal`
  escapes `<`, so a note saying `</script>` stays text. Offline in any
  browser, polls vote, checklists tick, notes take typing; changes stay in
  the page.
- **PNG and PDF:** `internal/webprint` opens that page with `#print` in a
  WebView2 of its own (its own window, off every screen, its own data
  folder under the user cache folder, `toruoard-export-webview`). The
  page swaps the canvas for a still copy of the whole board at zoom 1
  (`whiteboard/snapshot.ts`: no ports, handles or who-made-it tags, no dot
  grid). PNG: the page draws the copy through an SVG `foreignObject` into
  tiles at 2× (4096 px a side at most) and Go joins them, so a board of
  any size comes out whole (up to 144 million pixels; past that it says to
  export a PDF). PDF: `ICoreWebView2_7::PrintToPdf`, which Wails alpha.98's
  go-webview2 lacks, called through its vtable (slot 80) with print
  settings from `ICoreWebView2Environment6` (slot 14): one page the board's
  size, backgrounds on, no header or footer; text stays text.
- **Markdown:** `board.Markdown`, as `board.read` gives it.
- **Checks:** `go test ./internal/board` (round trip, plain import, bad
  pictures, the outline), `go test ./internal/boardexport` (every format of
  the sample board plus a note far off-screen; the HTML stays live in a
  WebView2; skips when the viewer isn't built), `go test
  ./internal/webprint` (PrintToPdf). `TORU_EXPORT_SAMPLES=<folder> go test
  ./internal/boardexport` writes a sample of each format.

## 4. Theming UI SDK with Toru's design system

UI SDK components render no styles. `@ui-sdk/core/web.css` is an optional
default look themed entirely by `--ui-*` tokens. The Glass look came from
the fork's WebGL lenses, not from UI SDK, so none of it comes along.

The plan is a **Toru UI SDK theme**: one stylesheet,
`frontend/src/whiteboard/toru-ui-sdk.css`, that loads `web.css` and maps
Toru's tokens onto it:

```css
@layer ui-sdk;            /* top of index.css, above @import "tailwindcss" */

:root {
  --ui-background: var(--background);
  --ui-foreground: var(--foreground);
  --ui-muted: var(--muted);
  --ui-muted-foreground: var(--muted-foreground);
  --ui-border: var(--border);
  --ui-input: var(--input);
  --ui-primary: var(--primary);
  --ui-primary-foreground: var(--primary-foreground);
  --ui-destructive: var(--destructive);
  --ui-ring: var(--ring);
  --ui-ring-border: var(--ring);
  --ui-radius: 0;                         /* Toru is sharp-edged */
  --ui-font-sans: "Inter", ui-sans-serif, system-ui, "Segoe UI", sans-serif;
}
```

- ~~Toru's tokens stay the one source: change `index.css` and the whiteboard
  follows. Dark only, like the rest of Toru.~~ Replaced by the two themes
  below (whiteboard#25).
- Popups and toolbars take Toru's existing `.frost` (CSS blur, 1px border),
  not WebGL glass. Board blocks sit flat on the canvas.
- Icons swap to lucide (Toru's icon set) through the `--ui-icon-*` tokens or
  inline `<svg>`.
- Contrast pairs from `styles.md` are checked in a real WebView2, not jsdom.
- The theme file is written so it can later move out into its own package
  for any Toru-styled UI SDK app. The whiteboard uses none of Toru's
  shadcn `components/ui` files.

### Light and dark themes (built, whiteboard#25)

Stephen found the dark-only board too dark, and asked for a light mode as
the default. So the whiteboard has two themes; the rest of Toru keeps its
dark look.

- **Light is the default.** **Dark is slate grey** (canvas `oklch(0.35
  0.025 257)`, about `#323b48`), not the near-black of Toru's other windows.
- **One token set.** `toru-ui-sdk.css` sets the `--ui-*` tokens twice, under
  `<html data-theme="light">` and `data-theme="dark"`: canvas, cards, text,
  muted text, borders, control edges, primary (links, selection, who-made-it
  tags), shadows, the dot grid and lines (`--ui-board-dot`,
  `--ui-board-line`), the pens (`--ui-ink-*`) and the catalog's colours
  (`--ui-board-<colour>` for a sticky's paper and swatches,
  `--ui-board-<colour>-accent` for a frame's border and name and a card's
  edge). Toru's own token names (`--background`, `--card`, `--primary`, ...,
  `--ink-*`) are pointed at them in the whiteboard window only, so `.frost`,
  Tailwind classes, `board.css`, `blocks.css` and `ink.css` follow. This
  reverses the direction above: the whiteboard's `--ui-*` tokens are now the
  source, not `index.css`.
- **The switch** is a Light/Dark pair of icon toggles in the toolbar. The
  choice is one for every board, saved by Go in
  `%AppData%\toru\whiteboard.json` (`WhiteboardsService.Theme`/`SetTheme`,
  `whiteboard_theme.go`). Go puts it in a board window's address
  (`&theme=`) and window colour so the first paint is right; a switch in one
  board window reaches the others (`whiteboard:theme`).
- **Pens follow the theme.** A stroke keeps its pen's token name, so every
  existing stroke redraws in the theme's colour. `ink-white` is the board's
  ink colour: dark on a light board (its pen is called Black there), white
  on a dark one. Yellow and green are darker on light boards so they keep
  3:1 on the canvas.
- **Stickies** keep dark text on their paper in both themes; frame and card
  accents are dark on light boards and light on dark ones.
- **Exports** (PNG, PDF, HTML, and agents' `board.export`) are drawn in the
  saved theme: Go puts it in the exported page's `#toru-board` payload. An
  exported page from before themes opens light.
- **Contrast** is measured in WebView2 by
  `TestWhiteboardThemeContrastInWebView2` (`whiteboard_theme_windows_test.go`,
  `testdata/theme-contrast.js`): every text against what's behind it (4.5:1,
  3:1 when large), the edges of text fields, checkboxes, radios and selects
  (3:1) and every pen stroke on the canvas (3:1), in both themes, on the
  exported page and, with `TORU_THEME_WINDOW_URL`, the whiteboard window
  itself. `TORU_THEME_BOARD` measures a real `.toruboard` instead of the
  built-in one; `TORU_THEME_OUT` writes each theme's PNG, PDF and HTML.
- The board demo server (`bun x vite --mode board-demo`) takes
  `TORU_BOARD_DEMO_BOARD=<file.toruboard>` to show a copy of a real board,
  still, for checks like this one.

## 5. Where it sits in Toru's Wails app

- **Window:** a new Wails window, `/?view=whiteboard&id=<board>`, opened by
  `WindowsService.OpenWhiteboard(id)` in `windows.go`; `App.tsx` routes it to
  `routes/Whiteboard.tsx`. One window per open board.
- **Front end:** `frontend/src/whiteboard/` holds the canvas primitive,
  block catalog, ink primitive, theme CSS and the window's agent commands.
  `@ui-sdk/core` and `@ui-sdk/react` pinned to exactly `0.1.0-beta.18`
  (canvas behaviors are experimental and may change between betas).
- **Go:** `internal/board` (document, op log, undo, `%AppData%\toru\boards\
  <id>.toruboard`, like `internal/presets`), `agent_commands_boards.go`
  (the board commands), a `BoardService` bound to the window that streams
  ops both ways.
- **Ways in:** a Whiteboards page in the main Toru window (list, new, open,
  import), "New whiteboard" in the tray, and a "Send to whiteboard" action on
  Library screenshots so a capture lands on a board as an image block.

### Frame times (whiteboard#23)

Measured on a copy of "Boxes Inc: the game on one board" (8 frames, 45
blocks, 15 links, 15 ink strokes; 2,270 elements in the board), in WebView2
on DESKTOP-STEPHEN, window 1384×861, monitor at 238 Hz (4.2 ms a frame),
production build, after the Light/Dark themes (whiteboard#25) and custom
blocks (whiteboard#27) merged in. Time between frames over three 2.5 s runs
of each action, main before this change and after it, in each theme:

| Action | Before, light | Before, dark | After, light | After, dark |
| --- | --- | --- | --- | --- |
| Pan (drag empty space) | 68 fps, p95 37.5 ms, worst 50 ms | 55 fps, 41.6 ms, 50 ms | 239 fps, 4.3 ms, 13 ms | 239 fps, 4.3 ms, 25 ms |
| Scroll (wheel pans) | 76 fps, 37.5 ms, 46 ms | 78 fps, 37.5 ms, 46 ms | 238 fps, 4.3 ms, 17 ms | 240 fps, 4.3 ms, 4 ms |
| Zoom (Ctrl and wheel) | 134 fps, 41.6 ms, 63 ms | 128 fps, 37.5 ms, 54 ms | 240 fps, 4.3 ms, 8 ms | 240 fps, 4.3 ms, 5 ms |
| Drag a block and drop it | 55 fps, 41.7 ms, 183 ms | 55 fps, 41.7 ms, 192 ms | 234 fps, 4.3 ms, 67 ms | 238 fps, 4.3 ms, 75 ms |
| Click one block after another | 200 fps, 4.3 ms, 100 ms | 200 fps, 4.3 ms, 92 ms | 234 fps, 4.3 ms, 13 ms | 235 fps, 4.3 ms, 17 ms |

The themes change no costs: both measure the same, before and after.

What cost the frames, found in Chrome DevTools traces (style invalidation
tracking): style recalculation of the whole board, 35–60 ms a frame.

- **The zoom label rewrote its text on every store change**, so every
  pointer move of a pan or drag. A new text node inside the board makes
  Blink recheck UI SDK's `:has()` rules (in `@ui-sdk/core/dist/web.css`,
  such as `:not([data-slot="field"]):has(…)`, whose subject is any element),
  and that restyles every `[data-slot]` and `svg` under the board. Now it
  writes into its one text node, and only when the percentage changes.
- **Zoom was written as inherited custom properties**, `--board-zoom` on the
  board (for the dot grid) and UI SDK's `--canvas-zoom` on the world, so each
  zoom step restyled every element. The dot grid now takes `background-size`
  and `background-position` from the viewport (not inherited), and the world
  keeps only its transform.
- **Every change to the board re-rendered every block** in React, and a
  re-rendered block rewrote its text fields (UI SDK's `Textarea` hands React a
  `defaultValue`, which replaces the field's text node), tripping the same
  `:has()` restyle. Blocks are now memoized (`BlockItem` in `BoardView.tsx`)
  and skip moves and resizes; the pen toolbar reads its width tokens once
  instead of forcing a style recalculation while it renders.

Checked and not a cost here: the four `.frost` toolbars' backdrop blur
(frames hold the refresh rate with it on), lines and ink re-routed per move
(under 1 ms of script a frame in all), and paint or layers (no culling
needed at this size).

What was left after that: one long frame the first time a person moves or
edits a block an agent made, and a board-wide restyle of about 30–40 ms for
each keystroke or vote in a block. whiteboard#37 took those out.

### Typing and first moves (whiteboard#37)

Measured on a board the size of Boxes Inc (8 frames, 48 blocks, all made by
an agent, 15 links, 15 ink strokes; 2,646 elements), in Chrome on
MACBOOK-STEPHEN, window 1400×900, 120 Hz (8.3 ms a frame), production build
of `board-perf.html`, main before this change and after it, two runs of each
in each theme. `type` double-clicks a sticky and types a key every 50 ms;
`drag` moves a block not moved yet in each of its three runs, so each drop
is the first change to an agent's block.

| Action | Before | After |
| --- | --- | --- |
| Type in a sticky | 103–106 fps, worst 27–32 ms, 38–59 frames over 2 intervals; style 245 ms a second | 120 fps, worst 10–17 ms, none over 2 intervals; style 4 ms a second |
| Move an agent's block the first time | worst 32–35 ms (the first drop), 1 frame over 2 intervals a run | worst 10–17 ms, none over 2 intervals |

What costs a frame, found by removing rules and by DevTools traces: with
UI SDK's `:has()` rules on the page (any one of several, among them the
board's own `[data-slot="board"]:has(…) [data-slot="board-port"]`), Blink
restyles every `[data-slot]` and `svg` under the board, 17–30 ms, whenever
**an element is put into or taken out of the board** (or straight into
`#root`; not the right panel, see below), or **an element's last text is
taken out**. Text added, text changed in place, and attribute
changes cost nothing. Overriding the rules can't help (a rule's selector
costs whatever it declares), so the window now avoids those changes where
nothing on screen changes (`whiteboard/restyle-guards.ts`, loaded by the
window):

- **A textarea's words were rewritten on every keystroke.** React sets a
  controlled `<textarea>`'s `defaultValue` (its text node) after each change
  to its value, and sets an uncontrolled one's on every render (UI SDK's
  hidden form field in a Select, in the right panel). The window skips a
  `defaultValue` write that changes nothing: the same default, or the words
  the field already shows when it isn't in a form.
- **UI SDK's screen-reader announcer made its live regions on the first
  drop** ("Moved"). They're made when the window opens
  (`prepareAnnouncements`).
- **The right panel put in its "Last changed by" line** on the first change
  to an agent's block. Both lines now stay in the panel, hidden when empty.
- **The block gallery's tiles rendered again on every board change** (new
  sections each render beat `TilePreview`'s memo): script time only, now
  kept.

Still there, not from this: each drop renders every block's UI SDK canvas
item and handles again (script, under a frame here). In WebView2 at 238 Hz it
costs frames: see whiteboard#46 below.

**Where an element goes matters (whiteboard#45).** Measured again on the same
board in Chrome on MACBOOK-STEPHEN, 120 Hz, production build, both themes,
by putting one element in and taking it out again: inside the board
(`[data-slot="board"]`) or straight into `#root`, that restyles about 2,200
elements, 17–23 ms; inside the right panel (`[data-slot="props-panel"]`, a
child of `#root` beside the board, not inside it), about 6 elements, under
1 ms, with the panel open or closed. So the right panel can put in and take
out its rows freely. It does so on every selection change (its body is keyed
by what's selected, so even sticky to sticky makes it again), and a click
costs about 4 ms of style over about 300 elements and 8–9 ms of script.
`bun scripts/board-perf.ts --only select`: 119–120 fps, worst 9–18 ms, no
frame over 2 intervals in 17 of 18 measurements across both themes; the
other was the first in a browser just started (worst 50 ms, 2 frames), and a
new browser profile didn't bring it back. Not measured in WebView2.

### Typing and first moves in WebView2 at 238 Hz (whiteboard#46)

Measured on a copy of the real "Boxes Inc: the game on one board" (rev 9: 8
frames, 45 blocks, 15 links, 15 ink strokes; 212 ports and 424 resize
handles), in WebView2 on DESKTOP-STEPHEN, window 1384×861 at 1×, monitor at
238 Hz (4.2 ms a frame, so two intervals are 8.4 ms), production build of
`board-perf.html` served with `vite preview`, the `board-perf-host` window
from **Measuring again** below. `bun scripts/board-perf.ts --no-profile --only
drag,type`, two runs of three in each theme:

| Action | Light | Dark |
| --- | --- | --- |
| Move a block (press, drag 2.5 s, drop) | 229–230 fps, p95 4.3 ms, worst 54–104 ms, 8 frames over 2 intervals in 3 drags | 233–234 fps, p95 4.3 ms, worst 42 ms, 6–7 over 2 intervals |
| Type in a sticky (a key every 50 ms) | 207–209 fps, p95 12.4 ms, p99 16.7 ms, worst 38–46 ms, 107–111 over 2 intervals in about 150 keys | 205–209 fps, p95 12.4 ms, p99 16.7–20.9 ms, worst 38–50 ms, 100–106 over 2 intervals |

Main thread a second: drag about 150 ms script, 100 ms style; typing about
225 ms script, under 10 ms style. The themes cost the same; the causes below
were traced in light. Dragging holds 238 fps between the press and the drop.
The long frames are these, timed by frame in all six drags and found in
DevTools traces of an unminified build (traced times run longer, so they are
given as shares):

- **Every keystroke, 8–17 ms (two in three keys over 2 intervals).** Script
  in the `input` event: React renders every block and every port and handle
  again, 68 block parts and 636 port and handle parts a key, though
  `BlockItem`'s memo lets only the typed sticky's own `BlockItem` through.
  The board is a controlled UI SDK primitive: its blocks change with each
  key, so UI SDK's `usePrimitive` hands the board a new "preview" handle that
  render (counted: one a key), and `usePart` passes the handle to every part
  under the board by React context, which no memo stops. React's render pass
  is about two thirds of the key's script, half of it these block, port and
  handle parts, and its commit about a third, though nothing on the board
  changes in the page. The right panel's radio and check inputs
  rewrite their `name` and `type` on each key too (attributes, so no
  restyle; a little script). The 120 Hz Mac had a whole 8.3 ms interval for
  this and stayed under 2 intervals; at 238 Hz it takes 2–3.
- **The press, 42–75 ms, every drag.** Selecting a block not selected yet:
  React renders the board's parts again (as above), the right panel puts in
  its header, "made by" line and four sections, and the block takes
  `data-selected`, `aria-selected` and `tabindex`; then 228 elements are
  restyled. whiteboard#45 measured the panel's rows at about 6 elements, so
  most of that restyle is the selection's change on the board, not narrowed
  further here. The Wails runtime's own `mousedown` listener
  (`@wailsio/runtime`, checking `--wails-draggable` with `getComputedStyle`)
  makes the restyle happen at once inside the event, about a quarter of the
  frame. The restyle and the renders would cost the frame without Wails too.
- **The drop, 46–62 ms, every drag, not only the first.** The move is
  committed to the board, and React renders every block and port and handle
  part again, twice (1,272 port and handle renders in one drop), with the
  right panel's fields; about four fifths script, then a restyle of about
  150 elements in the frame. This is whiteboard#37's "each drop renders every
  block's UI SDK canvas item and handles again", which is no longer under a
  frame here.
- **The first moves, 12–17 ms, in four drags of six.** For about 350 ms after
  the press, frames restyle 30–50 elements, layerize and paint while the
  ports' 120 ms fade (`board.css`, `[data-slot="board-port"]`'s transition)
  runs and the dragged block takes its shadow. Three such frames halfway through one drag
  weren't caught in a trace. One 10 ms minor garbage collection showed in a
  trace just after a press.

So the one thing to take out at 238 Hz is the board-wide re-render on each
change to the board: one cause of the typing frames and most of the press
and the drop.

The dots stay on the board: one every 24 board units from (0, 0), the same
grid Snap to grid uses (whiteboard#24).

### Only the changed block renders on a key or a drop (whiteboard#50)

whiteboard#46's board-wide render on each change is gone. Every part under
the board got the board's handle from one React context, keyed by the
primitive, and a controlled board gets a new handle each render (UI SDK's
"previewing" handle), so React rendered every block, port and handle on each
key and twice on each drop. A provider further in with a steady value
doesn't stop that: React still renders a part whose context changed above it.

`BoardView.tsx` now gives the block, port and handle parts a steady handle
(`useSteadyHandle`) under a primitive of their own (`steadyBoard`, the board
under another name), so the root's context never reaches them. The root
keeps the previewing handle, so its `blocks` and `lines` are new in the same
render; once the new options are set, in the board's layout effect before
paint, UI SDK checks each part and renders again only those whose state or
attributes changed. `useBoard()` gets the steady handle too. Lines, their
press targets, labels and disconnect buttons are drawn by the root itself
and still render on each change (about 60 small parts here).

`bun scripts/board-perf.ts` now also counts renders, from counters
`BoardView.tsx` keeps when `board-perf.html` asks: block items, blocks, and
ports and handles, per key, per press, during the moves and per drop.

Measured in Chrome on MACBOOK-STEPHEN, 120 Hz, production build of
`board-perf.html`, on a stand-in the size of Boxes Inc (66 blocks, 15 links,
15 ink strokes; not the real board, which is on DESKTOP-STEPHEN), three runs
of each, main (with only the counters added) against this change. Not yet
measured in WebView2 at 238 Hz:

| Renders | Main | This change |
| --- | --- | --- |
| A key in a sticky | 1 block item, 71 blocks, 672 ports and handles | 1 block item, 1 block, 12 ports and handles (the typed sticky's own) |
| The press | about 4 blocks (those whose selection changed), no ports or handles | the same |
| The moves | none | none |
| A drop that changes the board | the whole board twice: 142 blocks, 1,344 ports and handles | the dropped block twice: 2 blocks, 12 ports and handles |

The press was never board-wide here; whiteboard#46's press cost is its
restyle and the right panel's sections. The drag's figure of eight ends where
it began, so most drops change nothing: of three drops, one or two changed
the board (the first change to an agent's block, which sets "last changed
by", or a block left a little off its place), and only those render. A sticky
with lines can also render its neighbours' blocks once a key, since a block's
`aria-description` names the blocks it links to by their labels.

| Frames, CPU 4× (DevTools throttling) | Main, light | This change, light | Main, dark | This change, dark |
| --- | --- | --- | --- | --- |
| Type (a key every 50 ms), six runs | 98 fps, p95 25.1 ms, worst 41 ms, 176 over 2 intervals | 109 fps, p95 16.6 ms, worst 43 ms, 6 over 2 | 98 fps, p95 25.1 ms, worst 42 ms, 166 over 2 | 109 fps, p95 16.6 ms, worst 49 ms, 14 over 2 |
| Typing's script, three runs | 351 ms a second | 234 ms a second | 336 ms a second | 232 ms a second |
| Drag (press, 2.5 s, drop) | 113 fps, worst 25–75 ms, 5 over 2 | 114 fps, worst 32–59 ms, 4 over 2 | 112 fps, worst 18–85 ms, 4 over 2 | 115 fps, worst 32–42 ms, 4 over 2 |

At CPU 1× both hold 120 fps with no frame over 2 intervals; typing's script
falls from about 110 to about 70 ms a second. At 4× the worst typing frame
is still 40–50 ms, mostly in each page's first run, as on main; in an earlier
three-run set one run each had a single 75–85 ms frame, which six runs didn't
show again. The press's long frame (its restyle and the right panel putting
in its sections) is untouched here.

### Typing, press and drop in WebView2 at 238 Hz after whiteboard#50 (whiteboard#52)

Measured as whiteboard#46 was: the same copy of "Boxes Inc: the game on one
board" (rev 9; `board-perf.ts` counts 63 blocks, 15 links, 15 ink strokes),
WebView2 on DESKTOP-STEPHEN, window 1384×861 at 1×, 238 Hz (two intervals
are 8.4 ms), production build of `board-perf.html`, the `board-perf-host`
window, `bun scripts/board-perf.ts --no-profile --only idle,drag,type`.

**What whiteboard#50 left, measured first.** A key now renders 1 block item,
1 block and 12 ports and handles (the typed sticky's own; whiteboard#46 had
68 blocks and 636 ports and handles), and a drop 1 block item, 2 blocks and
12 ports and handles. Typing still had 8–12 frames over 2 intervals in about
150 keys and 130 ms of script a second. Counting React's renders by
component (a DevTools hook on the page) showed why: each key still rendered
about 375 components, because the whole window renders on each key (its
board's blocks are its state) and made new props for everything in it. The
drawing bar was 81 of them, the right panel 74, the toolbar 40, the gallery,
export and share menus about 35, and the lines the board's root draws (each
line, its press target, label and disconnect button) about 65.

**What this card changed:**

- **The window's controls over the board are made once** (`routes/Whiteboard.tsx`):
  the toolbar, block gallery, arrange command, drawing bar, board name and
  ask dialog are kept with `useMemo`, `useCallback` and `React.memo`. They are
  made again only when their own settings change.
- **The right panel renders only when its own props change**
  (`PropsPanel.tsx`: `PropsPanel` is memoized, and the window keeps `source`
  and the selection's `bridge` steady). The board's document doesn't change
  while typing, so neither does the panel.
- **Lines are on the steady handle** (`BoardView.tsx`: `LineItem`, `LineHit`,
  `Unlink`, `LineLabel`), like whiteboard#50's blocks. Each renders again only
  when its own look or selection changes. UI SDK moves a line's path itself
  when its blocks move, during a drag and after the drop, as before. A drop
  rendered 200 components drawn by the root (the root renders twice on a drop);
  now it renders 40.

Before the lines change, a key rendered about 135 components. About half of
them were the lines, which no longer render on a key. Script while typing
fell from 130–133 to 84–91 ms a second.

`board-perf.ts` now prints typing twice. The whole run counts the press and
double click that put the cursor in the sticky. **The keys alone** counts
only the frames from the first key on. `idle` measures the same 2.5 s
runs with no input and a DevTools message every 50 ms. It is the noise floor:
frames the computer and browser lose alone.

| Action | whiteboard#46, light | whiteboard#46, dark | After #50, light | After #50, dark | This card, light | This card, dark |
| --- | --- | --- | --- | --- | --- | --- |
| Idle (no input), per set of three runs | not measured | not measured | not measured | not measured | 0–2 over 2 intervals, worst 4.5–17 ms | 0–2 over 2, worst 8.4–21 ms |
| Type, the keys alone (about 150 keys a set) | not split | not split | not split | not split | 0 over 2 in four sets, 1 in a fifth; worst 8.5–16.6 ms | 0 over 2 in three sets, 2 in a fourth; worst 8.5–12.5 ms |
| Type, the whole run | 207–209 fps, worst 38–46 ms, 107–111 over 2 | 205–209 fps, worst 38–50 ms, 100–106 over 2 | 228 fps, worst 21 ms, 8 over 2 | 228 fps, worst 21 ms, 12 over 2 | 236–237 fps, worst 13–25 ms, 4 over 2 | 235–237 fps, worst 17–25 ms, 2–5 over 2 |
| Typing's script | about 225 ms a second | | 133 ms a second | 130 ms a second | 84–91 ms a second | 85–90 ms a second |
| Move a block (3 drags of 2.5 s) | 229–230 fps, worst 54–104 ms, 8 over 2 | 233–234 fps, worst 42 ms, 6–7 over 2 | 235 fps, worst 58 ms, 7 over 2 | 236 fps, worst 29 ms, 6 over 2 | 236–238 fps, worst 17–29 ms, 5–6 over 2 | 237–238 fps, worst 17–21 ms, 5–7 over 2 |
| The press, one frame a drag | 42–75 ms | | | | 12–21 ms | 12–21 ms |
| The drop, one frame a drag | 46–62 ms | | | | 12.5–29 ms | 12.5–21 ms |

"After #50" is one set of three runs a theme, on this card's base before its
changes. "This card" is four to five sets a theme. The first two of each were
taken before the lines change, and they measure the same: the lines change
cut the drop's renders, not its long frame. whiteboard#48 (the right panel
no longer rebuilt on each selection) came into this card between the two
columns, so the drag and whole-run columns differ by it too. The fall in
typing's script was measured before it came in.

So typing itself holds the refresh rate. In 9 sets (about 1,350 keys), 3
frames went over 2 intervals, while idle lost 0–2 frames in each set of the
same length with no input. The press and the drop still take one frame of 3
to 7 intervals each, every drag. The first and second clicks into a sticky
take one or two such frames each. That is all of typing's "whole run" count.

**What's left, traced in WebView2** (an unminified build, so times run long;
given as what the frame spent):

- **The press** (also the first click into a sticky): Wails' own `mousedown`
  listener (`@wailsio/runtime`'s `primaryDown`, reading `--wails-draggable`
  with `getPropertyValue`) forces a restyle of about 167 elements inside the
  event, after the block took `data-selected`, `aria-selected` and
  `tabindex`. React then renders the about 4 blocks whose selection changed,
  and the right panel follows in a transition. This is selecting a block.
  whiteboard#51 is working on it, so this card leaves it alone.
- **The double click into a sticky's text** (`editBlock` in `board.ts`): it
  sets `data-editing` on the block, and `elementFromPoint` then forces a
  restyle of about 168 elements and a layout. The caret goes in, focus moves
  to the field, and React renders the `editing` change. A sticky has only 31
  elements, so the restyle reaches well past the block. The press and the
  drop restyle about the same 167–168 elements, so it is one set wider than
  the block, likely through UI SDK's `:has()` rules. Which rule brings it in
  isn't found here; that needs a trace with style invalidation tracking. In
  the page, flipping `data-editing` on a sticky costs about 1.2 ms with its
  restyle, and moving focus into its field 2.1 ms. The rest of the frame is
  React's render of the change and of the panel.
- **The drop**: about a third is React's render (the root twice, the dropped
  block twice, the right panel's X and Y fields). A quarter is UI SDK's
  check of every part once the board's new options are set (`setOptions`
  in the board's layout effect, whiteboard#50). About a third is UI SDK's
  drag end (`onEnd`, `setBoxes`, port points). Then about 168 elements
  restyle as the block stops dragging. The check of every part is UI SDK's
  own work for a controlled primitive, so this card leaves it.

### Selecting a block with the properties panel (whiteboard#42)

The properties panel (whiteboard#29) brought back one long frame per click
that changes the selection. What cost it, found with a mutation observer and
DevTools traces on a board like Boxes Inc (8 frames, 48 blocks, 8 links, 16
ink strokes):

- **The board's selection was controlled** (`selected` passed back to
  `<Board>`), and UI SDK rechecks every part of a primitive when a
  controlled option changes value: every block, port and handle rendered
  again on each click (74 blocks and 672 ports and handles here). The board
  now keeps its own selection; `usePropsSelection` hears of it through
  `onSelectedChange` into a small store the panel follows, and the panel's
  own picks (a line's ends, a line from a block's list) go through the
  board's `select`.
- **The whole window rendered again on each click**, since the selection was
  its state. That drew the block gallery's previews again (`catalogSections()`
  made new items each render, so `TilePreview`'s memo never held) and the
  toolbars, and their fields rewritten inside the board set off the
  board-wide `:has()` restyle. Now nothing outside the panel renders, and the
  gallery's sections are rebuilt only when the block library changes.
- **The panel built its new form in the click's own frame.** It now follows
  the selection as a React transition: the click paints the block selected,
  and the panel's render comes in the frames after, split into small tasks.
- The ink section read its pen widths from their tokens (a computed-style
  read) on every render; it reads them once.

Not a cost: UI SDK's popup code reading `display` reads an inline style, which
doesn't restyle. The computed-style read in the profiles comes from Wails'
drag code (`@wailsio/runtime`'s `primaryDown`), which reads
`--wails-draggable` on every mouse press and so pays, there, for whatever the
press changed.

Measured on this card in Chrome on a Mac (120 Hz), production build, with
DevTools' CPU throttling standing in for a slower computer: frames over 1.5
refreshes past each run's first click, three runs of 2.5 s.

| CPU | Main, light | This card, light | Main, dark | This card, dark |
| --- | --- | --- | --- | --- |
| 1× | 21 of 906, worst 33 ms | 7 of 995, worst 19 ms (reruns: 2, then 0) | 10 of 937, worst 25 ms | 0 of 985, worst 10 ms |
| 2× | 26 of 903, worst 35 ms | 11 of 909, worst 19 ms | 25 of 906, worst 43 ms | 17 of 900, worst 33 ms |
| 4× | 24 of 800, worst 100 ms | 35 of 905, worst 50 ms | 28 of 803, worst 93 ms | 35 of 906, worst 50 ms |

The Mac was busy with other work while measuring, so single runs vary. At 4×
there are more slow frames than on main but each is half as long: the click's
own frame and, separately, the panel's.

What's left is the board's own cost of selecting: at 4× throttling a click
still takes about 25 ms, half of it restyling the two blocks whose selection
changed (rules keyed on `[data-selected]` and Tailwind's `group-hover`) and
half UI SDK's selection and roving focus code. That was there before the
panel.

**In WebView2 on DESKTOP-STEPHEN (whiteboard#43).** Measured on the copy of
Boxes Inc the earlier WebView2 numbers used (board `brd-f69a6b74ac65797b`,
rev 9, taken from Toru's board folder: 45 blocks, 8 frames, 15 links, 15 ink
strokes; the script counts 63 `board-block` elements, 45 of them clickable),
window 1384×861, 238 Hz (4.2 ms a frame, so 1.5 refreshes is 6.3 ms), with
`main` at whiteboard#45 (641cc53). `bun scripts/board-perf.ts --cdp 9339
--board <copy> --only select --no-profile --theme light|dark`, three runs of
2.5 s, a click every 250 ms, about 27 clicks measured past each run's first:

| Build | Theme | Past the first click |
| --- | --- | --- |
| Vite dev server (47830) | light | worst 58.4 ms, 78 of 1723 frames over 1.5× refresh (6.3 ms) |
| Vite dev server | dark | worst 41.7 ms, 62 of 1710 frames over 1.5× refresh |
| Vite dev server | light, again | worst 37.6 ms, 62 of 1688 frames over 1.5× refresh |
| Vite dev server | dark, again | worst 41.7 ms, 68 of 1704 frames over 1.5× refresh |
| Production | light | worst 33.4 ms, 55 of 1773 frames over 1.5× refresh |
| Production | dark | worst 25.1 ms, 49 of 1749 frames over 1.5× refresh |
| Production | light, again | worst 21.0 ms, 48 of 1793 frames over 1.5× refresh |
| Production | dark, again | worst 20.9 ms, 47 of 1792 frames over 1.5× refresh |

Runs taken again for the card's proof landed in the same range (dev: 56 and
59 frames; production: 47 and 56), except one production dark run with a
single 67 ms frame, not seen in any other run. So every click still brings
about two frames over 1.5 refreshes; the themes measure the same. On a 238 Hz screen 6.3 ms is a tight budget: the click's
own task alone is twice that. A DevTools trace of ten clicks (production
build, light; frames counted from the compositor's begin-frame ticks, 22
slow frames over 9 clicks) names each one:

- **The press, every click: 13–22 ms.** UI SDK's `pointerdown` (its
  selection store `set` notifying every block part's listener, `choose` /
  `replace`, then roving focus's `setStop` / `focused`) is 3–4 ms of script,
  and the restyle of the two blocks whose selection changes (about 200
  elements: UI SDK rewrites `data-slot` / `data-part` on their spans,
  inputs and buttons, and each block's handles come and go) is 5–7 ms. That
  restyle is the one Wails' `primaryDown` forces with its
  `--wails-draggable` read on `mousedown`; the read only moves it earlier,
  but the focus change after it costs two more small restyles (about 35
  elements, 1–2 ms). Not the board-wide `:has()` restyle: the press restyles
  no more than 202 elements.
- **The release, most clicks: 6.4–10 ms.** On `pointerup` Blink restyles
  about 136 elements (4–5 ms) for the `:active` / `:hover` change on the
  board's `h-full w-full` wrapper, in the same frame as the CSS transitions
  the selection started (on spans inside the two blocks; which rules, not
  traced).
- **The panel's commit, some clicks: 8 ms.** The React transition that
  rebuilds the properties panel commits in one task (about 6–12 ms, taking
  out and putting in about 30 rows). It restyles only the panel (see
  whiteboard#45), but its script and layout run over the budget in one
  frame.
- **Not selection:** moving the mouse onto the next block (the hover before
  the press) sometimes takes 7–10 ms: a `:hover` restyle of 50–70 elements,
  forced in the `mousemove` by the same Wails read.

whiteboard#23 measured clicking in WebView2 at worst 13 ms before the panel
existed; the press's frame is that same cost, and the release and panel
frames come on top. whiteboard#51 takes these on.

**Two refreshes in WebView2 on DESKTOP-STEPHEN (whiteboard#49).** Does
clicking from block to block hold every frame within 2 refresh intervals
(8.4 ms at 238 Hz)? Not yet: after whiteboard#48 (the panel no longer
rebuilt on each selection), #50 and #52, about one click in five to eight
still has one frame over, in both themes. Before #48 it was about one frame
every click.

Measured on a copy of Boxes Inc (`brd-f69a6b74ac65797b`, rev 9: 45 blocks,
8 frames, 15 links, 15 ink strokes; 45 clickable blocks), window 1384×861,
238 Hz (4.2 ms), production build of `board-perf.html`. `bun
scripts/board-perf.ts --only select --cdp 9339 --board <copy> --theme
light|dark --no-profile`: each line is three runs of 2.5 s, a click every
250 ms, about 80 clicks past each run's first. "Over 2×" counts frames that
missed more than one refresh.

| `main` | Theme | Over 2×, past the first click | Over 1.5× | Worst |
| --- | --- | --- | --- | --- |
| Before #48 (641cc53) | light | 27, 26 of about 1,870 | 40, 51 | 25.1, 33.4 ms |
| Before #48 | dark | 24, 24 of about 1,850 | 42, 37 | 25.1, 25.0 ms |
| With #48, #50, #52 (c1ddeeb) | light | 18, 10, 14 of about 1,910 | 31, 32, 32 | 16.7, 17.2, 16.7 ms |
| With #48, #50, #52 | dark | 12, 13, 11 of about 1,930 | 29, 35, 30 | 16.8, 16.8, 20.8 ms |

Median 4.2 ms and 233–234 fps with #48; main thread per second went from
script 36–39, style 58–64, layout 4–5 ms to script 26–28, style 44–47,
layout 3 ms. The themes measure the same. whiteboard#51 measured the same
`main` and found the same (6–15 over 2× past the first click).

What each frame over 2× spends, from DevTools traces of twelve clicks in
each theme on `c1ddeeb` (main thread self time per frame, between
`BeginMainThreadFrame` ticks). Tracing makes every task slower, so the
traced click shows one or two frames over 2× where an untraced one shows
one now and then; the kinds are the same:

- **The press, every click: style, then script.** 11–20 ms traced (12.6–17
  ms untraced, so over 2× whenever it misses a third refresh), about 5–11 ms
  of style and 4–6 ms of script. The style is the restyle of the two blocks
  whose selection changed, forced inside `mousedown` by Wails' `primaryDown`
  (`@wailsio/runtime` reading `--wails-draggable`: an `UpdateLayoutTree`
  inside that call); the script is UI SDK's `pointerdown` (selection store,
  roving focus) and the `focus` that follows.
- **15–30 ms later, about one click in three: script.** 11–14 ms traced,
  7–10 ms script and 1–4 ms style: React's scheduled render of the
  properties panel. Before #48 it was 10–17 ms of script on every click.
- **Gone since #48:** the separate style frame 25–75 ms after the click.
- Layout is under 1 ms in every slow frame: not a cost.

So what is left is style (the press's restyle of the two blocks, forced
early by Wails' read) and script (UI SDK's press, then the panel's smaller
render), not layout. whiteboard#51 takes these on.

**Measuring again.** `frontend/board-perf.html` (Vite only) opens the
whiteboard window on a board in memory; `frontend/scripts/board-perf.ts`
drives it over the DevTools protocol with synthetic input only, so it never
moves the mouse or takes focus, and prints frame times per action plus where
the main thread's time went. For WebView2, a tiny Wails app opens the page in
a window that can't be activated and lets the mouse through:

```go
app := application.New(application.Options{
	Name: "board-perf-host",
	Windows: application.WindowsOptions{
		WebviewUserDataPath: os.Args[2],
		AdditionalBrowserArgs: []string{"--remote-debugging-port=9339",
			"--disable-features=CalculateNativeWinOcclusion",
			"--disable-backgrounding-occluded-windows", "--disable-renderer-backgrounding"},
	},
})
app.Window.NewWithOptions(application.WebviewWindowOptions{
	Width: 1400, Height: 900, InitialPosition: application.WindowXY, X: 200, Y: 2000, // below the screens
	URL: os.Args[1], IgnoreMouseEvents: true,
	Windows: application.WindowsWindow{ExStyle: 0x08000000 | 0x80}, // WS_EX_NOACTIVATE | WS_EX_TOOLWINDOW
})
_ = app.Run()
```

```sh
cd frontend && bunx vite --host 127.0.0.1 --port 47830
host.exe http://127.0.0.1:47830/board-perf.html <profile dir>
bun scripts/board-perf.ts --cdp 9339 --board <copy.toruboard> --no-profile --theme dark
```

In Chrome, start it with `--remote-debugging-port` and its own
`--user-data-dir`, plus `--disable-backgrounding-occluded-windows
--disable-renderer-backgrounding`, and pass that port as `--cdp`. For
production React, build `board-perf.html` with a Vite config that adds it as
`build.rollupOptions.input` and serve it with `vite preview`.

Export the board to `.toruboard` first and measure the file; the page keeps
the board in memory and never touches Toru's store.

`--only select` also prints the frames past each run's first click (the
first opens the panel), which is what "no long frame when selecting" counts,
and how many of them go over 2 refreshes.
`--cpu 4` slows the page with DevTools' CPU throttling, for measuring on a
computer much faster than the one people use. Any Chrome or Edge started with
`--remote-debugging-port=9339` serves as the browser off Windows.

## 6. Build, split into cards

In order. Cards 1 and 2 start when this plan is done; the rest wait on the
card named.

| # | Card | Waits for |
| --- | --- | --- |
| 1 | Whiteboard window and Toru's UI SDK theme | — |
| 2 | Board document and store in Go | — |
| 3 | Board canvas: pan, zoom, select, move, resize, connect | 1 |
| 4 | Block catalog v1 | 3 |
| 5 | Freehand ink block | 3 |
| 6 | Board commands for agents over MCP | 2 |
| 7 | Live board: window and Go in sync, undo, who-did-what | 4 |
| 8 | Export and import | 7 |
| 9 | Whiteboards page, tray entry and Send to whiteboard | 7 |
| 10 | Brainstorm templates and auto-layout | 6 |
| 11 | Shared boards between Toru installs (built, whiteboard#13) | Stephen |

Not built in this plan: agent-sent markup or custom code at runtime, SVG
export, a light theme, an MCP port on the network.
