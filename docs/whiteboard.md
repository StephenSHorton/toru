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
- **Gallery:** the window's block gallery (whiteboard#26) lists the
  person's own blocks from `libraryBlocks()` in `custom-types.ts`.

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
