import * as React from "react";
import { useCallback, useEffect, useMemo, useState, useSyncExternalStore, type FocusEvent, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { createAgent } from "@ui-sdk/react/agent";
import { Button } from "@ui-sdk/react/button";
import { Input } from "@ui-sdk/react/input";
import { Window } from "@wailsio/runtime";
import { Menu, MenuContent, MenuItem, MenuTrigger } from "@ui-sdk/react/menu";
import { Checkbox, CheckboxIndicator } from "@ui-sdk/react/checkbox";
import { Toggle, ToggleGroup } from "@ui-sdk/react/toggle";
import { CheckIcon, ChevronDownIcon, DownloadIcon, LayoutGridIcon, LayoutTemplateIcon, MoonIcon, PencilIcon, SunIcon, XIcon } from "lucide-react";
import { registerAgentCommand } from "@/lib/agent";
import { AskPerson, createAskPerson } from "@/whiteboard/ask-person";
import { BlockGallery } from "@/whiteboard/BlockGallery";
import { Board, useBoard, type BoardProps } from "@/whiteboard/BoardView";
import { dotGrid, onGrid, type BoardBlocks } from "@/whiteboard/board";
import { BlockBody, type PropsChange } from "@/whiteboard/blocks";
import { useBlockLibrary } from "@/whiteboard/block-library";
import { blockLabel } from "@/whiteboard/catalog";
import { DrawingBar, useDrawingSettings } from "@/whiteboard/DrawingBar";
import { layoutKinds, type LayoutKind } from "@/whiteboard/layout";
import { PropsPanel, usePropsSelection } from "@/whiteboard/PropsPanel";
import { boardTemplates, templateBoard } from "@/whiteboard/templates";
import type { LiveBoard } from "@/whiteboard/live";
import { boardThemes, parseTheme, setBoardTheme, useBoardTheme, useWindowTheme } from "@/whiteboard/theme";
import { agentSource, httpBase, openLive, sourceOf, typing, withAgent } from "@/whiteboard/window";
import { SharePopover, goShare, httpShare } from "@/whiteboard/share";
import "@/whiteboard/toru-ui-sdk.css";
import "@/whiteboard/restyle-guards";

// The whiteboard window (?view=whiteboard&id=<board>, WindowsService.OpenWhiteboard).
// The board canvas (whiteboard/board.ts on UI SDK's canvas behaviors: pan,
// zoom, select, lasso, move, resize, connect) under a toolbar of UI SDK
// controls in Toru's theme. Each block renders its block catalog type
// (whiteboard/catalog.ts, blocks.tsx). The whiteboard uses UI SDK only, never
// Toru's shadcn components/ui.
//
// The board is Go's (internal/board), kept live both ways by
// whiteboard/live.ts: agents' changes appear as they land, the person's are
// sent as board ops, and Ctrl+Z / Ctrl+Y step through the one undo history
// they share. Each block shows who made it (BoardView).
//
// `?id=<board>` opens a board in Toru's store; `&link=http` reaches it over
// HTTP instead of Wails (Go's live board demo server). With no id the window
// opens the sample board (one block of every type) in a store of its own.
//
// With no id, `?template=<name>` opens a brainstorm template (templates.ts)
// instead of the sample. The toolbar's Template menu places a template beside
// what's on the board, and Layout arranges blocks as a grid, a tree or
// clusters by tag, like Go's board.layout: each is one change, one undo.

// UI SDK's agent bridge, made before the board renders so it hears it mount.
// `board.snapshot` and `board.act` hand it to agents over Toru's agent control.
// A step on a control marked data-agent-confirm (removing a checklist item,
// poll option or table column) waits for the person's answer in <AskPerson>;
// a no fails the step with `The person said no to "…"`.
const askPerson = createAskPerson();
const agent = createAgent({ allow: askPerson.allow });

// Snap to grid: on, blocks the person drags, resizes or adds land on the
// background dots (board.ts's dotGrid); off, they go where they're put. The
// person's choice, kept in this computer's window storage; on until they
// untick it. Agents placing blocks by x/y (board.add, moveBlock) are never
// snapped.
const snapKey = "toru.whiteboard.snapToGrid";
function useSnapToGrid() {
  const [snap, setSnap] = useState(() => {
    try {
      return window.localStorage.getItem(snapKey) !== "0";
    } catch {
      return true;
    }
  });
  const change = useCallback((on: boolean) => {
    setSnap(on);
    try {
      window.localStorage.setItem(snapKey, on ? "1" : "0");
    } catch {
      // no storage: the choice lasts while the window is open
    }
  }, []);
  return [snap, change] as const;
}

/** The whiteboard window. `live` is for checks: a board on a link of their own (whiteboard/links.ts). */
export default function Whiteboard({ live: given }: { live?: LiveBoard } = {}) {
  const [live] = useState(() => given ?? openLive());
  const { view, error, doc } = useSyncExternalStore(live.subscribe, live.getState);
  // Light (the default) or slate grey dark, saved in Go for every board.
  useWindowTheme();
  const id = live.id;
  const [shareApi] = useState(() => {
    const params = new URLSearchParams(window.location.search);
    return params.get("link") === "http" ? httpShare(httpBase(params)) : goShare;
  });
  const [snap, setSnap] = useSnapToGrid();
  const grid = snap ? dotGrid : 1;
  // The person's own blocks (custom types), when the board is in Toru's store.
  useBlockLibrary(id, !given && (() => {
    const params = new URLSearchParams(window.location.search);
    return !!params.get("id") && params.get("link") !== "http";
  })());
  const [drawing, setDrawing] = useDrawingSettings();

  useEffect(() => {
    void live.start();
    // Send what's waiting before the window goes.
    const leave = () => void live.flush();
    window.addEventListener("beforeunload", leave);
    return () => {
      window.removeEventListener("beforeunload", leave);
      live.stop();
    };
  }, [live]);

  // Ctrl+Z undoes and Ctrl+Y (or Ctrl+Shift+Z) redoes the board's latest
  // change, the person's or an agent's, through Go's one history. Inside a
  // block's text too: the text is the board's, so its undo is the board's.
  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      if (!(event.ctrlKey || event.metaKey) || event.altKey) return;
      // The board's name field keeps its own text undo.
      // So does a properties panel field with typing not yet sent.
      if ((event.target as Element | null)?.closest?.('[data-slot="board-name"], [data-slot="props-panel"] [data-dirty]')) return;
      const key = event.key.toLowerCase();
      if (key === "z" && !event.shiftKey) {
        event.preventDefault();
        void live.undo();
      } else if (key === "y" || (key === "z" && event.shiftKey)) {
        event.preventDefault();
        void live.redo();
      }
    };
    window.addEventListener("keydown", keydown, true);
    return () => window.removeEventListener("keydown", keydown, true);
  }, [live]);

  // The window's title follows the board's name.
  const name = doc?.name;
  useEffect(() => {
    if (!name) return;
    document.title = `Toru — ${name}`;
    void Promise.resolve()
      .then(() => Window.SetTitle(`Toru — ${name}`))
      .catch(() => {}); // no Wails window (a check, or Vite alone)
  }, [name]);

  useEffect(() => {
    const offSnapshot = registerAgentCommand(
      "board.snapshot",
      {
        description:
          "The open whiteboard as UI SDK's agent bridge sees it: every block by id with its label, type, props and box, every line with its name, the selection, and each block's controls (text areas, checkboxes, polls, ratings, table cells, tags) with their actions.",
      },
      () => agent.snapshot(),
    );
    const offAct = registerAgentCommand(
      "board.act",
      {
        description:
          "Use the open whiteboard's controls as a person would, through UI SDK's agent bridge: steps run in order, each {ref, action, args}, where ref is a control's label or ref from board.snapshot. " +
          'Like {"ref": "Which block next?", "action": "setValue", "args": ["Timer"]} to vote, "toggle" on a checklist item, "setValue" on a sticky note, card, table cell or rating, "press" on a button.',
        input: {
          type: "object",
          required: ["steps"],
          properties: {
            steps: {
              type: "array",
              minItems: 1,
              items: {
                type: "object",
                required: ["ref", "action"],
                properties: { ref: { type: "string" }, action: { type: "string" }, args: { type: "array" } },
              },
            },
          },
        },
      },
      // The steps' changes are this agent's: they go to Go tagged with its
      // name, and are saved before the answer goes back.
      (input, call) =>
        withAgent(agentSource(call?.caller ?? ""), live, () =>
          agent.act({ steps: (input as { steps: Parameters<typeof agent.act>[0]["steps"] }).steps }),
        ),
    );
    return () => {
      offSnapshot();
      offAct();
    };
  }, [live]);

  // A block's props changed in its render, by a person or an agent
  // (details.source): an edit to send to Go. Typing waits for a pause.
  const setProps = useCallback(
    (blockId: string, props: Record<string, unknown>, details: PropsChange) => {
      const blocks = live.getState().view.blocks;
      const block = blocks[blockId];
      if (!block?.type) return;
      const source = sourceOf(details);
      live.edit(
        { blocks: { ...blocks, [blockId]: { ...block, props, label: blockLabel(block.type, props) } } },
        { source, typing: source === "person" && typing() },
      );
    },
    [live],
  );

  // What's selected, for the canvas and the properties panel.
  const pick = usePropsSelection(doc);

  const renderBlock = useCallback(
    (blockId: string, all: BoardBlocks) => {
      const block = all[blockId];
      if (!block.type) return block.label;
      return (
        <BlockBody
          id={blockId}
          type={block.type}
          props={block.props ?? {}}
          onProps={(next, details) => setProps(blockId, next, details)}
        />
      );
    },
    [setProps],
  );

  // The controls over the board, made again only when what they show
  // changes, not on each change to the board: the window renders on every
  // key typed in a block, and drawing the drawing bar, toolbar, gallery and
  // menus again then was most of the key's script in WebView2 at 238 Hz
  // (about 165 of the 375 components a key rendered; whiteboard#52).
  const undo = useCallback(() => void live.undo(), [live]);
  const redo = useCallback(() => void live.redo(), [live]);
  const chrome = useMemo(
    () => (
      <>
        <SampleToolbar
          grid={grid}
          snap={snap}
          onSnap={setSnap}
          exportMenu={<ExportMenu live={live} />}
          share={<SharePopover boardId={id} api={shareApi} />}
        />
        <BlockGallery grid={grid} />
        <ArrangeCommand live={live} />
        <DrawingBar settings={drawing} onChange={setDrawing} onUndo={undo} onRedo={redo} />
      </>
    ),
    [grid, snap, setSnap, live, id, shareApi, drawing, setDrawing, undo, redo],
  );
  const rename = useCallback((next: string) => live.rename(next), [live]);
  const ask = useMemo(() => <AskPerson api={askPerson} />, []);
  const panelSource = useCallback(() => sourceOf(), []);
  // The right panel follows the document a moment later, in a render React
  // may put off: after a drop it only rewrites the block's X and Y, and doing
  // that in the drop's own frame took the frame past two refreshes at 238 Hz
  // (whiteboard#54).
  const panelDoc = React.useDeferredValue(doc);
  // The board's own props, kept the same while its blocks and lines are: Go's
  // answer to a drop or a key changes the document (for the right panel) but
  // not the view, and the board then needn't render or check its parts again
  // (whiteboard#54).
  const onBlocksChange = useCallback<NonNullable<BoardProps["onBlocksChange"]>>(
    (blocks, details) => live.edit({ blocks }, { source: sourceOf(details) }),
    [live],
  );
  const onLinesChange = useCallback<NonNullable<BoardProps["onLinesChange"]>>(
    (lines, details) => live.edit({ lines }, { source: sourceOf(details) }),
    [live],
  );
  const overBoard = useMemo(
    () => (
      <>
        {chrome}
        {pick.bridge}
      </>
    ),
    [chrome, pick.bridge],
  );

  return (
    <div className="relative h-full overflow-hidden" onPointerDownCapture={pick.onPointerDownCapture} onKeyDownCapture={pick.onKeyDownCapture}>
      <Board
        {...pick.board}
        aria-label={doc ? `Whiteboard ${doc.name}` : `Whiteboard ${id}`}
        className="h-full w-full"
        blocks={view.blocks}
        onBlocksChange={onBlocksChange}
        lines={view.lines}
        onLinesChange={onLinesChange}
        renderBlock={renderBlock}
        drawing={drawing}
        grid={grid}
      >
        {overBoard}
      </Board>
      <PropsPanel live={live} doc={panelDoc} source={panelSource} {...pick.panel} />
      {doc && <BoardName name={doc.name} onRename={rename} />}
      {error && (
        <div role="alert" data-slot="board-error" className="frost absolute bottom-16 left-1/2 flex -translate-x-1/2 items-center gap-2 px-3 py-2">
          {error}
          <Button aria-label="Dismiss" onClick={() => live.dismissError()}>
            <XIcon aria-hidden="true" />
          </Button>
        </div>
      )}
      {ask}
    </div>
  );
}

/**
 * The board's name, top left. Its pencil (or a click on the name) turns it
 * into a field: Enter or leaving it renames the board, as one undoable
 * change; Escape keeps the old name.
 */
const BoardName = React.memo(function BoardName({ name, onRename }: { name: string; onRename: (name: string) => Promise<boolean> }) {
  const [draft, setDraft] = useState<string | null>(null);
  const finish = (save: boolean) => {
    if (draft === null) return;
    const next = draft.trim();
    setDraft(null);
    if (save && next && next !== name) void onRename(next);
  };
  return (
    <div
      data-slot="board-name"
      className="frost absolute top-3 left-3 flex max-w-[22rem] items-center gap-1 py-1 pr-1 pl-3"
      onWheel={(e) => e.stopPropagation()}
      onPointerDown={(e) => e.stopPropagation()}
      style={{ cursor: "default" }}
    >
      {draft === null ? (
        <>
          <h1
            className="min-w-0 truncate text-sm font-semibold"
            title={`${name} (click to rename)`}
            onDoubleClick={() => setDraft(name)}
          >
            {name}
          </h1>
          <Button aria-label="Rename board" title="Rename board" onClick={() => setDraft(name)}>
            <PencilIcon aria-hidden="true" />
          </Button>
        </>
      ) : (
        <Input
          autoFocus
          aria-label="Board name"
          maxLength={120}
          value={draft}
          onValueChange={(v: string) => setDraft(v)}
          onFocus={(e: FocusEvent<HTMLInputElement>) => e.target.select()}
          onBlur={() => finish(true)}
          onKeyDown={(e: ReactKeyboardEvent<HTMLInputElement>) => {
            e.stopPropagation();
            if (e.key === "Enter") finish(true);
            if (e.key === "Escape") finish(false);
          }}
        />
      )}
    </div>
  );
});

// The toolbar: Fit, Template, Layout and Export work; Snap to grid lands
// what the person drags, resizes and adds on the dots. Blocks are added from
// the block gallery on the left (whiteboard/BlockGallery.tsx). Share shares
// the board with the people in your Toru room (whiteboard/share.tsx).
function SampleToolbar({
  grid,
  snap,
  onSnap,
  exportMenu,
  share,
}: {
  grid: number;
  snap: boolean;
  onSnap: (on: boolean) => void;
  exportMenu?: React.ReactNode;
  share?: React.ReactNode;
}) {
  const board = useBoard();
  return (
    <div
      className="frost absolute top-3 left-1/2 flex -translate-x-1/2 items-center gap-3 px-3 py-2"
      onWheel={(e) => e.stopPropagation()}
      style={{ cursor: "default" }}
    >
      <Button onClick={() => board.fit()}>Fit</Button>
      <TemplateMenu grid={grid} />
      <LayoutMenu />
      {exportMenu}
      <label data-slot="label" className="flex items-center gap-2 whitespace-nowrap">
        <Checkbox checked={snap} onCheckedChange={(checked) => onSnap(checked === true)}>
          <CheckboxIndicator>
            <CheckIcon aria-hidden="true" />
          </CheckboxIndicator>
        </Checkbox>
        Snap to grid
      </label>
      <ThemeSwitch />
      {share}
    </div>
  );
}

// Theme: Light (the default) or Dark, for every board; saved in Go
// (whiteboard/theme.ts), and what PNG, PDF and HTML exports are drawn in.
function ThemeSwitch() {
  const theme = useBoardTheme();
  return (
    <ToggleGroup
      aria-label="Theme"
      data-slot="theme-switch"
      deselectable={false}
      value={[theme]}
      onValueChange={(value) => {
        const t = parseTheme(value[0]);
        if (t && t !== theme) void setBoardTheme(t);
      }}
      className="flex"
    >
      {boardThemes.map((t) => (
        <Toggle key={t.theme} value={t.theme} aria-label={`${t.title} theme`} title={`${t.title} theme`} data-theme-option={t.theme}>
          {t.theme === "light" ? <SunIcon aria-hidden="true" /> : <MoonIcon aria-hidden="true" />}
        </Toggle>
      ))}
    </ToggleGroup>
  );
}

// Template: places a brainstorm template to the right of what's on the board,
// its corner on the grid, then fits the view to show everything.
function TemplateMenu({ grid }: { grid: number }) {
  const board = useBoard();
  const place = (name: string) => {
    const blocks = board.blocks();
    const all = Object.values(blocks);
    const right = all.length ? Math.max(...all.map((b) => b.x + b.width)) + 160 : 0;
    const x = grid > 1 ? Math.ceil(right / grid) * grid : right;
    const y = onGrid(all.length ? Math.min(...all.map((b) => b.y)) : 0, grid);
    let n = 1;
    while (Object.keys(blocks).some((id) => id.startsWith(`t${n}-`))) n++;
    const t = templateBoard(name, { prefix: `t${n}-`, x, y });
    if (t && board.addBlocks(t.blocks, t.lines)) requestAnimationFrame(() => board.fit());
  };
  return (
    <Menu>
      <MenuTrigger data-menu="template">
        <LayoutTemplateIcon aria-hidden="true" />
        Template
        <ChevronDownIcon aria-hidden="true" />
      </MenuTrigger>
      <MenuContent className="frost">
        {boardTemplates.map((t) => (
          <MenuItem key={t.name} value={t.name} title={t.description} data-template={t.name} onSelect={() => place(t.name)}>
            {t.title}
          </MenuItem>
        ))}
      </MenuContent>
    </Menu>
  );
}

// Layout: arranges the selection (two or more blocks) or every block.
function LayoutMenu() {
  const board = useBoard();
  return (
    <Menu>
      <MenuTrigger data-menu="layout">
        <LayoutGridIcon aria-hidden="true" />
        Layout
        <ChevronDownIcon aria-hidden="true" />
      </MenuTrigger>
      <MenuContent className="frost">
        {layoutKinds.map((k) => (
          <MenuItem
            key={k.kind}
            value={k.kind}
            title={k.description}
            data-layout={k.kind}
            onSelect={() => {
              if (board.arrange(k.kind)) requestAnimationFrame(() => board.fit());
            }}
          >
            {k.title}
          </MenuItem>
        ))}
      </MenuContent>
    </Menu>
  );
}

// board.arrange: the Layout menu for agents, on the board open in this window.
// (Go's board.layout arranges a saved board, window open or not.)
function ArrangeCommand({ live }: { live: LiveBoard }) {
  const board = useBoard();
  useEffect(
    () =>
      registerAgentCommand(
        "board.arrange",
        {
          description:
            "Arrange the blocks on the whiteboard open in this window, as its Layout menu does: grid, tree (follows the lines) or cluster (one column per tag). " +
            "Every block, unless blocks lists some ids from board.snapshot. Frames and ink stay put. Returns how many moved. For a saved board, use board.layout.",
          input: {
            type: "object",
            required: ["kind"],
            properties: {
              kind: { type: "string", enum: layoutKinds.map((k) => k.kind) },
              blocks: { type: "array", items: { type: "string" } },
              gap: { type: "number", minimum: 0 },
              columns: { type: "integer", minimum: 1 },
              direction: { type: "string", enum: ["right", "down"] },
            },
          },
        },
        (input: { kind: LayoutKind; blocks?: string[]; gap?: number; columns?: number; direction?: "right" | "down" }, call) =>
          // The agent's change, sent to Go under its name before it answers.
          withAgent(agentSource(call?.caller ?? ""), live, () => {
            const moved = board.arrange(input.kind, input.blocks?.length ? input.blocks : Object.keys(board.blocks()), {
              gap: input.gap,
              columns: input.columns,
              direction: input.direction,
            });
            board.fit();
            return { moved };
          }),
      ),
    [board, live],
  );
  return null;
}

// Export: the board as a file (a .toruboard with its pictures, a 2× PNG of
// the whole board, a vector PDF, a standalone live HTML page, a Markdown
// outline), or on the clipboard as a picture or an outline; and Import, a
// .toruboard into a new board in its own window. Go makes every file
// (board_export.go, internal/boardexport), the same ones agents get from
// board.export. What's waiting to be sent goes to Go first, so the file has
// it.
const exportService = () => import("../../bindings/github.com/StephenSHorton/toru/boardexportservice.js");

const saveFormats = [
  { format: "toruboard", title: "Toru board (.toruboard)", description: "The board and its pictures, to import into Toru" },
  { format: "png", title: "PNG picture", description: "The whole board at 2×" },
  { format: "pdf", title: "PDF", description: "One vector page the board's size" },
  { format: "html", title: "Web page (.html)", description: "One file that opens in any browser, offline, still live" },
  { format: "markdown", title: "Markdown outline", description: "Frames, notes, checklists, tables and links as text" },
] as const;

function ExportMenu({ live }: { live: LiveBoard }) {
  const [status, setStatus] = useState<string | null>(null);
  useEffect(() => {
    if (!status || status.endsWith("…")) return;
    const t = setTimeout(() => setStatus(null), 6000);
    return () => clearTimeout(t);
  }, [status]);
  const run = async (working: string, work: () => Promise<string | null>) => {
    setStatus(working);
    try {
      await live.flush();
      setStatus(await work());
    } catch (e) {
      setStatus(e instanceof Error ? e.message : String(e));
    }
  };
  const save = (format: string, title: string) =>
    run(`Exporting ${title}…`, async () => {
      const path = await (await exportService()).Save(live.id, format);
      return path ? `Saved ${path}` : null;
    });
  const copy = (format: string, what: string) =>
    run(`Copying ${what}…`, async () => {
      await (await exportService()).Copy(live.id, format);
      return `Copied ${what}`;
    });
  return (
    <>
      <Menu>
        <MenuTrigger data-menu="export">
          <DownloadIcon aria-hidden="true" />
          Export
          <ChevronDownIcon aria-hidden="true" />
        </MenuTrigger>
        <MenuContent className="frost">
          {saveFormats.map((f) => (
            <MenuItem key={f.format} value={f.format} title={f.description} data-export={f.format} onSelect={() => void save(f.format, f.title)}>
              Save as {f.title}
            </MenuItem>
          ))}
          <MenuItem value="copy-png" data-export="copy-png" title="The whole board at 2×, to paste anywhere" onSelect={() => void copy("png", "the board as a picture")}>
            Copy as picture
          </MenuItem>
          <MenuItem value="copy-markdown" data-export="copy-markdown" title="The board's outline as Markdown" onSelect={() => void copy("markdown", "the board's outline")}>
            Copy as Markdown
          </MenuItem>
          <MenuItem
            value="import"
            data-export="import"
            title="Open a .toruboard as a new board"
            onSelect={() =>
              void run("Importing…", async () => {
                const id = await (await exportService()).Import();
                return id ? "Imported: it opened in its own window" : null;
              })
            }
          >
            Import board…
          </MenuItem>
        </MenuContent>
      </Menu>
      {status && (
        <span role="status" data-slot="export-status" className="frost absolute top-full left-1/2 mt-2 -translate-x-1/2 whitespace-nowrap px-3 py-1.5">
          {status}
        </span>
      )}
    </>
  );
}
