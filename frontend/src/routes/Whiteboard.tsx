import { useCallback, useEffect, useState, useSyncExternalStore, type FocusEvent, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { createAgent } from "@ui-sdk/react/agent";
import { Button } from "@ui-sdk/react/button";
import { Input } from "@ui-sdk/react/input";
import { Window } from "@wailsio/runtime";
import { Menu, MenuContent, MenuItem, MenuTrigger } from "@ui-sdk/react/menu";
import { Checkbox, CheckboxIndicator } from "@ui-sdk/react/checkbox";
import {
  Popover,
  PopoverClose,
  PopoverContent,
  PopoverDescription,
  PopoverTitle,
  PopoverTrigger,
} from "@ui-sdk/react/popover";
import { Select, SelectContent, SelectItem, SelectItemIndicator, SelectTrigger, SelectValue } from "@ui-sdk/react/select";
import { Slider, SliderControl, SliderIndicator, SliderThumb, SliderTrack } from "@ui-sdk/react/slider";
import { Toggle, ToggleGroup } from "@ui-sdk/react/toggle";
import { CheckIcon, ChevronDownIcon, LayoutGridIcon, PencilIcon, LayoutTemplateIcon, MousePointer2Icon, PenLineIcon, PlusIcon, XIcon } from "lucide-react";
import { registerAgentCommand } from "@/lib/agent";
import { AskPerson, createAskPerson } from "@/whiteboard/ask-person";
import { Board, useBoard, type PenOptions } from "@/whiteboard/BoardView";
import type { BoardBlocks } from "@/whiteboard/board";
import { BlockBody, type PropsChange } from "@/whiteboard/blocks";
import { blockLabel, blockType, blockTypes, newProps, type BlockType } from "@/whiteboard/catalog";
import { inkColor, inkColors, inkWidths, tokenWidth } from "@/whiteboard/ink";
import { layoutKinds, type LayoutKind } from "@/whiteboard/layout";
import { boardTemplates, templateBoard } from "@/whiteboard/templates";
import type { LiveBoard } from "@/whiteboard/live";
import { agentSource, openLive, sourceOf, typing, withAgent } from "@/whiteboard/window";
import "@/whiteboard/toru-ui-sdk.css";

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

/** The whiteboard window. `live` is for checks: a board on a link of their own (whiteboard/links.ts). */
export default function Whiteboard({ live: given }: { live?: LiveBoard } = {}) {
  const [live] = useState(() => given ?? openLive());
  const { view, error, doc } = useSyncExternalStore(live.subscribe, live.getState);
  const id = live.id;
  const [pen, setPen] = useState<PenOptions>(() => ({
    active: false,
    color: inkColors[0].token,
    width: tokenWidth(inkWidths[0].token, inkWidths[0].fallback),
  }));

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
      if ((event.target as Element | null)?.closest?.('[data-slot="board-name"]')) return;
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

  return (
    <div className="relative h-full overflow-hidden">
      <Board
        aria-label={doc ? `Whiteboard ${doc.name}` : `Whiteboard ${id}`}
        className="h-full w-full"
        blocks={view.blocks}
        onBlocksChange={(blocks, details) => live.edit({ blocks }, { source: sourceOf(details) })}
        lines={view.lines}
        onLinesChange={(lines, details) => live.edit({ lines }, { source: sourceOf(details) })}
        renderBlock={renderBlock}
        pen={pen}
      >
        <SampleToolbar onAdd={(type) => live.edit({ blocks: addBlock(live.getState().view.blocks, type) })} />
        <ArrangeCommand live={live} />
        <PenToolbar pen={pen} onChange={(change) => setPen((was) => ({ ...was, ...change }))} />
      </Board>
      {doc && <BoardName name={doc.name} onRename={(next) => live.rename(next)} />}
      {error && (
        <div role="alert" data-slot="board-error" className="frost absolute bottom-3 left-1/2 flex -translate-x-1/2 items-center gap-2 px-3 py-2">
          {error}
          <Button aria-label="Dismiss" onClick={() => live.dismissError()}>
            <XIcon aria-hidden="true" />
          </Button>
        </div>
      )}
      <AskPerson api={askPerson} />
    </div>
  );
}

/**
 * The board's name, top left. Its pencil (or a click on the name) turns it
 * into a field: Enter or leaving it renames the board, as one undoable
 * change; Escape keeps the old name.
 */
function BoardName({ name, onRename }: { name: string; onRename: (name: string) => Promise<boolean> }) {
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
}

/**
 * A new block of a catalog type, at its default size, below the lowest
 * block. Its id is random, so it can't meet one an agent adds meanwhile.
 */
function addBlock(blocks: BoardBlocks, type: BlockType): BoardBlocks {
  const info = blockType(type);
  if (!info) return blocks;
  const all = Object.values(blocks);
  const y = all.length ? Math.max(...all.map((b) => b.y + b.height)) + 40 : 0;
  const x = all.length ? Math.min(...all.map((b) => b.x)) : 0;
  let id = "";
  do id = `blk-${crypto.randomUUID().replaceAll("-", "").slice(0, 12)}`;
  while (blocks[id]);
  const props = structuredClone(newProps[type]) as Record<string, unknown>;
  return {
    ...blocks,
    [id]: { label: blockLabel(type, props), type, props, x, y, width: info.w, height: info.h },
  };
}

// The tools down the board's left side: select or pen, then the pen's colour
// and width (Toru's --ink-* tokens).
function PenToolbar({ pen, onChange }: { pen: PenOptions; onChange: (change: Partial<PenOptions>) => void }) {
  const widths = inkWidths.map((w) => ({ ...w, value: tokenWidth(w.token, w.fallback) }));
  const width = widths.find((w) => w.value === pen.width) ?? widths[0];
  return (
    <div
      data-slot="pen-toolbar"
      className="frost absolute top-1/2 left-3 flex -translate-y-1/2 flex-col items-center gap-2 p-1.5"
      onWheel={(e) => e.stopPropagation()}
      style={{ cursor: "default" }}
    >
      <ToggleGroup
        aria-label="Tool"
        orientation="vertical"
        deselectable={false}
        value={[pen.active ? "pen" : "select"]}
        onValueChange={(value) => value[0] && onChange({ active: value[0] === "pen" })}
        className="flex flex-col gap-1"
      >
        <Toggle value="select" aria-label="Select" title="Select">
          <MousePointer2Icon aria-hidden="true" />
        </Toggle>
        <Toggle value="pen" aria-label="Pen" title="Pen">
          <PenLineIcon aria-hidden="true" />
        </Toggle>
      </ToggleGroup>
      <div data-slot="pen-toolbar-separator" />
      <ToggleGroup
        aria-label="Pen colour"
        orientation="vertical"
        deselectable={false}
        value={[pen.color]}
        onValueChange={(value) => value[0] && onChange({ color: value[0], active: true })}
        className="flex flex-col gap-1"
      >
        {inkColors.map((c) => (
          <Toggle key={c.token} value={c.token} aria-label={c.name} title={c.name} data-pen-colour={c.token}>
            <span data-slot="pen-swatch" style={{ background: inkColor(c.token) }} />
          </Toggle>
        ))}
      </ToggleGroup>
      <div data-slot="pen-toolbar-separator" />
      <ToggleGroup
        aria-label="Pen width"
        orientation="vertical"
        deselectable={false}
        value={[width.token]}
        onValueChange={(value) => {
          const w = widths.find((x) => x.token === value[0]);
          if (w) onChange({ width: w.value, active: true });
        }}
        className="flex flex-col gap-1"
      >
        {widths.map((w) => (
          <Toggle key={w.token} value={w.token} aria-label={`${w.name} pen`} title={`${w.name} pen`} data-pen-width={w.token}>
            <span data-slot="pen-width" style={{ height: `${w.value}px`, background: inkColor(pen.color) }} />
          </Toggle>
        ))}
      </ToggleGroup>
    </div>
  );
}

// The toolbar: Add block places the chosen catalog type; Fit works. The rest
// are samples of UI SDK controls in Toru's theme, for later cards.
function SampleToolbar({ onAdd }: { onAdd: (type: BlockType) => void }) {
  const board = useBoard();
  const [kind, setKind] = useState<BlockType>("sticky");
  return (
    <div
      className="frost absolute top-3 left-1/2 flex -translate-x-1/2 items-center gap-3 px-3 py-2"
      onWheel={(e) => e.stopPropagation()}
      style={{ cursor: "default" }}
    >
      <Button
        data-variant="solid"
        onClick={() => {
          onAdd(kind);
          requestAnimationFrame(() => board.fit());
        }}
      >
        <PlusIcon aria-hidden="true" />
        Add block
      </Button>
      <Button onClick={() => board.fit()}>Fit</Button>
      <TemplateMenu />
      <LayoutMenu />
      <Select value={kind} onValueChange={(v) => v && setKind(v as BlockType)} aria-label="Block kind">
        <SelectTrigger>
          <SelectValue />
          <ChevronDownIcon aria-hidden="true" />
        </SelectTrigger>
        <SelectContent className="frost">
          {blockTypes.map(({ type: value, label }) => (
            <SelectItem key={value} value={value}>
              {label}
              <SelectItemIndicator>
                <CheckIcon aria-hidden="true" />
              </SelectItemIndicator>
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <label data-slot="label" className="flex items-center gap-2 whitespace-nowrap">
        <Checkbox defaultChecked>
          <CheckboxIndicator>
            <CheckIcon aria-hidden="true" />
          </CheckboxIndicator>
        </Checkbox>
        Snap to grid
      </label>
      <Slider defaultValue={60} style={{ minWidth: "8rem" }}>
        <SliderControl>
          <SliderTrack>
            <SliderIndicator />
            <SliderThumb aria-label="Zoom" />
          </SliderTrack>
        </SliderControl>
      </Slider>
      <Popover>
        <PopoverTrigger>Share</PopoverTrigger>
        <PopoverContent className="frost">
          <PopoverTitle>Share this board</PopoverTitle>
          <PopoverDescription>Anyone with the link can view it.</PopoverDescription>
          <div className="flex justify-end gap-2">
            <PopoverClose>Cancel</PopoverClose>
            <PopoverClose data-variant="solid">Copy link</PopoverClose>
          </div>
        </PopoverContent>
      </Popover>
    </div>
  );
}

// Template: places a brainstorm template to the right of what's on the board,
// then fits the view to show everything.
function TemplateMenu() {
  const board = useBoard();
  const place = (name: string) => {
    const blocks = board.blocks();
    const all = Object.values(blocks);
    const x = all.length ? Math.max(...all.map((b) => b.x + b.width)) + 160 : 0;
    const y = all.length ? Math.min(...all.map((b) => b.y)) : 0;
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
