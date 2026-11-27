import { useCallback, useEffect, useState } from "react";
import { createAgent } from "@ui-sdk/react/agent";
import { Button } from "@ui-sdk/react/button";
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
import { CheckIcon, ChevronDownIcon, PlusIcon } from "lucide-react";
import { registerAgentCommand } from "@/lib/agent";
import { Board, useBoard } from "@/whiteboard/BoardView";
import type { BoardBlocks } from "@/whiteboard/board";
import { BlockBody, type PropsChange } from "@/whiteboard/blocks";
import { blockLabel, blockType, blockTypes, newProps, type BlockType } from "@/whiteboard/catalog";
import { sampleBoard } from "@/whiteboard/sample";
import "@/whiteboard/toru-ui-sdk.css";

// The whiteboard window (?view=whiteboard&id=<board>, WindowsService.OpenWhiteboard).
// The board canvas (whiteboard/board.ts on UI SDK's canvas behaviors: pan,
// zoom, select, lasso, move, resize, connect) under a toolbar of UI SDK
// controls in Toru's theme. Each block renders its block catalog type
// (whiteboard/catalog.ts, blocks.tsx). The whiteboard uses UI SDK only, never
// Toru's shadcn components/ui.
//
// Until the board document lives in Go (the live board card), the window
// opens on a sample board with one block of every type, kept in this window.

// UI SDK's agent bridge, made before the board renders so it hears it mount.
// `board.snapshot` hands its snapshot to agents over Toru's agent control.
const agent = createAgent();

export default function Whiteboard() {
  const id = new URLSearchParams(window.location.search).get("id") ?? "";
  const [blocks, setBlocks] = useState<BoardBlocks>(sampleBoard.blocks);

  useEffect(
    () =>
      registerAgentCommand(
        "board.snapshot",
        {
          description:
            "The open whiteboard as UI SDK's agent bridge sees it: every block by id with its label, type, props and box, every line with its name, the selection, and each block's controls (text areas, checkboxes, polls, ratings, table cells, tags) with their actions.",
        },
        () => agent.snapshot(),
      ),
    [],
  );

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
        defaultLines={sampleBoard.lines}
        renderBlock={renderBlock}
      >
        <SampleToolbar onAdd={(type) => setBlocks((prev) => addBlock(prev, type))} />
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
