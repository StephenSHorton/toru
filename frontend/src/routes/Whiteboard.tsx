import { useEffect, useState } from "react";
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
import { layoutKinds, type LayoutKind } from "@/whiteboard/layout";
import { blockText, boardTemplates, templateBoard } from "@/whiteboard/templates";
import { inkColor, inkColors, inkWidths, tokenWidth } from "@/whiteboard/ink";
import "@/whiteboard/toru-ui-sdk.css";
import "@/whiteboard/templates.css";

// The whiteboard window (?view=whiteboard&id=<board>, WindowsService.OpenWhiteboard).
// The board canvas (whiteboard/board.ts on UI SDK's canvas behaviors: pan,
// zoom, select, lasso, move, resize, connect) under a toolbar of UI SDK
// controls in Toru's theme. The whiteboard uses UI SDK only, never Toru's
// shadcn components/ui.
//
// Until the board document lives in Go (the board store and live-board
// cards), the window opens on a sample board kept in this window only, or on
// a brainstorm template with ?template=<name> (templates.ts). The toolbar's
// Template menu places a template beside what's on the board, and Layout
// arranges blocks as grid, tree or clusters by tag, like Go's board.layout.

// UI SDK's agent bridge, made before the board renders so it hears it mount.
// `board.snapshot` hands its snapshot to agents over Toru's agent control.
const agent = createAgent();

const sampleBlocks: BoardBlocks = {
  goal: { label: "Goal: ship the whiteboard", x: 0, y: 160, width: 200, height: 72 },
  users: { label: "Who uses it", x: 280, y: 40, width: 160, height: 64 },
  ideas: { label: "Ideas", x: 280, y: 200, width: 160, height: 64 },
  sticky: { label: "Sticky notes", x: 520, y: 120, width: 152, height: 56 },
  polls: { label: "Polls", x: 520, y: 200, width: 152, height: 56 },
  ink: { label: "Freehand ink", x: 520, y: 280, width: 152, height: 56 },
  risks: { label: "Risks", x: 280, y: 360, width: 160, height: 64 },
};
const sampleLines: BoardLine[] = [
  { id: "goal:right->users:left", from: "goal:right", to: "users:left" },
  { id: "goal:right->ideas:left", from: "goal:right", to: "ideas:left" },
  { id: "ideas:right->sticky:left", from: "ideas:right", to: "sticky:left" },
  { id: "ideas:right->polls:left", from: "ideas:right", to: "polls:left" },
  { id: "ideas:right->ink:left", from: "ideas:right", to: "ink:left" },
];

// The board the window opens on: ?template=<name>, else the sample.
function startBoard(): { blocks: BoardBlocks; lines: BoardLine[] } {
  const name = new URLSearchParams(window.location.search).get("template");
  return (name && templateBoard(name)) || { blocks: sampleBlocks, lines: sampleLines };
}

export default function Whiteboard() {
  const id = new URLSearchParams(window.location.search).get("id") ?? "";
  const [start] = useState(startBoard);
  const [pen, setPen] = useState<PenOptions>(() => ({
    active: false,
    color: inkColors[0].token,
    width: tokenWidth(inkWidths[0].token, inkWidths[0].fallback),
  }));

  useEffect(
    () =>
      registerAgentCommand(
        "board.snapshot",
        {
          description:
            "The open whiteboard as UI SDK's agent bridge sees it: every block by id with its label and box, every line with its name, the selection, and each component's actions.",
        },
        () => agent.snapshot(),
      ),
    [],
  );

  return (
    <div className="relative h-full overflow-hidden">
      <Board
        aria-label={`Whiteboard ${id}`.trim()}
        className="h-full w-full"
        defaultBlocks={start.blocks}
        defaultLines={start.lines}
        renderBlock={(blockId, blocks) => <BlockPreview block={blocks[blockId]} />}
        pen={pen}
      >
        <SampleToolbar />
        <ArrangeCommand />
        <PenToolbar pen={pen} onChange={(change) => setPen((was) => ({ ...was, ...change }))} />
      </Board>
    </div>
  );
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

// A sample of UI SDK controls, to see the theme next to Toru's own windows.
// Fit works; the rest wait for the block catalog and later cards.
function SampleToolbar() {
  const board = useBoard();
  return (
    <div
      className="frost absolute top-3 left-1/2 flex -translate-x-1/2 items-center gap-3 px-3 py-2"
      onWheel={(e) => e.stopPropagation()}
      style={{ cursor: "default" }}
    >
      <Button data-variant="solid">
        <PlusIcon aria-hidden="true" />
        Add block
      </Button>
      <Button onClick={() => board.fit()}>Fit</Button>
      <TemplateMenu />
      <LayoutMenu />
      <Select defaultValue="sticky" aria-label="Block kind">
        <SelectTrigger>
          <SelectValue />
          <ChevronDownIcon aria-hidden="true" />
        </SelectTrigger>
        <SelectContent className="frost">
          {[
            ["sticky", "Sticky note"],
            ["text", "Text"],
            ["shape", "Shape"],
            ["image", "Image"],
          ].map(([value, label]) => (
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

// What a block shows until the block catalog renders each type: its words,
// and a checklist's items or a poll's options under its title.
function BlockPreview({ block }: { block: BoardBlocks[string] }) {
  const list =
    block.type === "checklist"
      ? (block.props?.items as { text: string }[] | undefined)?.map((x) => x.text)
      : block.type === "poll"
        ? (block.props?.options as { label: string }[] | undefined)?.map((x) => x.label)
        : undefined;
  if (!list || !block.type) return block.label;
  return (
    <span data-slot="template-preview" data-kind={block.type}>
      <strong>{blockText(block.type, block.props ?? {})}</strong>
      <ul>
        {list.map((text, i) => (
          <li key={i}>{text}</li>
        ))}
      </ul>
    </span>
  );
}

// Template: places a brainstorm template to the right of what's on the board,
// then fits the view to show everything.
function TemplateMenu() {
  const board = useBoard();
  const place = (name: string) => {
    const blocks = Object.values(board.blocks());
    const right = blocks.length ? Math.max(...blocks.map((b) => b.x + b.width)) + 160 : 0;
    const top = blocks.length ? Math.min(...blocks.map((b) => b.y)) : 0;
    const taken = new Set(Object.keys(board.blocks()));
    let n = 1;
    while ([...taken].some((id) => id.startsWith(`t${n}-`))) n++;
    const t = templateBoard(name, { prefix: `t${n}-`, x: right, y: top });
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
            "Every block, unless blocks lists some ids from board.snapshot. Returns how many moved. For a saved board, use board.layout.",
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
