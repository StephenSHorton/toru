import { useEffect } from "react";
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
import type { BoardBlocks, BoardLine } from "@/whiteboard/board";
import "@/whiteboard/toru-ui-sdk.css";

// The whiteboard window (?view=whiteboard&id=<board>, WindowsService.OpenWhiteboard).
// The board canvas (whiteboard/board.ts on UI SDK's canvas behaviors: pan,
// zoom, select, lasso, move, resize, connect) under a toolbar of UI SDK
// controls in Toru's theme. The whiteboard uses UI SDK only, never Toru's
// shadcn components/ui.
//
// Until the board document lives in Go (the board store and live-board
// cards), the window opens on a sample board kept in this window only.

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

export default function Whiteboard() {
  const id = new URLSearchParams(window.location.search).get("id") ?? "";

  useEffect(
    () =>
      registerAgentCommand(
        "board.snapshot",
        {
          description:
            "The open whiteboard as UI SDK's agent bridge sees it: every block by id with its label and box, every line with its label, the selection, and each component's actions.",
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
        defaultBlocks={sampleBlocks}
        defaultLines={sampleLines}
      >
        <SampleToolbar />
      </Board>
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
