import { useCallback, useEffect, useState } from "react";
import { createAgent } from "@ui-sdk/react/agent";
import { Button } from "@ui-sdk/react/button";
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
import { CheckIcon, ChevronDownIcon, LayoutGridIcon, LayoutTemplateIcon, MousePointer2Icon, PenLineIcon, PlusIcon } from "lucide-react";
import { registerAgentCommand } from "@/lib/agent";
import { Board, useBoard, type PenOptions } from "@/whiteboard/BoardView";
import type { BoardBlocks, BoardLine } from "@/whiteboard/board";
import { BlockBody, type PropsChange } from "@/whiteboard/blocks";
import { blockLabel, blockType, blockTypes, newProps, type BlockType } from "@/whiteboard/catalog";
import { inkColor, inkColors, inkWidths, tokenWidth } from "@/whiteboard/ink";
import { layoutKinds, type LayoutKind } from "@/whiteboard/layout";
import { sampleBoard } from "@/whiteboard/sample";
import { boardTemplates, templateBoard } from "@/whiteboard/templates";
import "@/whiteboard/toru-ui-sdk.css";

// The whiteboard window (?view=whiteboard&id=<board>, WindowsService.OpenWhiteboard).
// The board canvas (whiteboard/board.ts on UI SDK's canvas behaviors: pan,
// zoom, select, lasso, move, resize, connect) under a toolbar of UI SDK
// controls in Toru's theme. Each block renders its block catalog type
// (whiteboard/catalog.ts, blocks.tsx). The whiteboard uses UI SDK only, never
// Toru's shadcn components/ui.
//
// Until the board document lives in Go (the live board card), the window
// opens on a sample board with one block of every type, kept in this window,
// or on a brainstorm template with ?template=<name> (templates.ts). The
// toolbar's Template menu places a template beside what's on the board, and
// Layout arranges blocks as a grid, a tree or clusters by tag, like Go's
// board.layout.

// UI SDK's agent bridge, made before the board renders so it hears it mount.
// `board.snapshot` hands its snapshot to agents over Toru's agent control.
const agent = createAgent();

// The board the window opens on: ?template=<name>, else the sample.
function startBoard(): { blocks: BoardBlocks; lines: BoardLine[] } {
  const name = new URLSearchParams(window.location.search).get("template");
  return (name && templateBoard(name)) || sampleBoard;
}

export default function Whiteboard() {
  const id = new URLSearchParams(window.location.search).get("id") ?? "";
  const [start] = useState(startBoard);
  const [blocks, setBlocks] = useState<BoardBlocks>(start.blocks);
  const [pen, setPen] = useState<PenOptions>(() => ({
    active: false,
    color: inkColors[0].token,
    width: tokenWidth(inkWidths[0].token, inkWidths[0].fallback),
  }));

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
      (input) => agent.act({ steps: (input as { steps: Parameters<typeof agent.act>[0]["steps"] }).steps }),
    );
    return () => {
      offSnapshot();
      offAct();
    };
  }, []);

  // A block's props changed in its render, by a person or an agent
  // (details.source). The live board card sends these to Go as board ops.
  const setProps = useCallback((blockId: string, props: Record<string, unknown>, _details: PropsChange) => {
    setBlocks((prev) => {
      const block = prev[blockId];
      if (!block?.type) return prev;
      return { ...prev, [blockId]: { ...block, props, label: blockLabel(block.type, props) } };
    });
  }, []);

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
        aria-label={`Whiteboard ${id}`.trim()}
        className="h-full w-full"
        blocks={blocks}
        onBlocksChange={setBlocks}
        defaultLines={start.lines}
        renderBlock={renderBlock}
        pen={pen}
      >
        <SampleToolbar onAdd={(type) => setBlocks((prev) => addBlock(prev, type))} />
        <ArrangeCommand />
        <PenToolbar pen={pen} onChange={(change) => setPen((was) => ({ ...was, ...change }))} />
      </Board>
    </div>
  );
}

/** A new block of a catalog type, at its default size, below the lowest block. */
function addBlock(blocks: BoardBlocks, type: BlockType): BoardBlocks {
  const info = blockType(type);
  if (!info) return blocks;
  const all = Object.values(blocks);
  const y = all.length ? Math.max(...all.map((b) => b.y + b.height)) + 40 : 0;
  const x = all.length ? Math.min(...all.map((b) => b.x)) : 0;
  let n = all.length + 1;
  while (blocks[`${type}-${n}`]) n++;
  const props = structuredClone(newProps[type]) as Record<string, unknown>;
  return {
    ...blocks,
    [`${type}-${n}`]: { label: blockLabel(type, props), type, props, x, y, width: info.w, height: info.h },
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
function ArrangeCommand() {
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
        (input: { kind: LayoutKind; blocks?: string[]; gap?: number; columns?: number; direction?: "right" | "down" }) => {
          const moved = board.arrange(input.kind, input.blocks?.length ? input.blocks : Object.keys(board.blocks()), {
            gap: input.gap,
            columns: input.columns,
            direction: input.direction,
          });
          board.fit();
          return { moved };
        },
      ),
    [board],
  );
  return null;
}
