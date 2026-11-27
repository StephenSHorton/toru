// The whiteboard canvas in React: board.ts's primitive rendered as parts, in
// Toru's look (board.css). Rendering follows UI SDK 0.1.0-beta.18's
// examples/experiments/react/canvas.tsx and board.tsx (Apache-2.0, Copyright
// Vercel, Inc.); see board.ts for what changed.
import * as React from "react";
import { createParts, usePart, usePrimitive, type PartProps } from "@ui-sdk/react/primitive";
import { MaximizeIcon, MinusIcon, PlusIcon, XIcon } from "lucide-react";
import { board, boardInstructions, handleNames, sides, type BoardBlocks, type BoardBlockState, type BoardOptions } from "./board";
import "./board.css";

const handles = ["n", "ne", "e", "se", "s", "sw", "w", "nw"] as const;

const parts = createParts(board, {
  root: "div",
  world: "div",
  line: "path",
  unlink: "button",
  wire: "path",
  lasso: "div",
  "zoom-in": "button",
  "zoom-out": "button",
  fit: "button",
  zoom: "span",
  instructions: "div",
});

type BoardHandle = ReturnType<typeof usePrimitive<typeof board>>;

// The board's handle, for controls drawn over it, like a toolbar's Fit.
const BoardContext = React.createContext<BoardHandle | null>(null);

/** The nearest board's api: `fit()`, `viewport`, the agent actions. */
function useBoard() {
  const handle = React.useContext(BoardContext);
  if (!handle) throw new Error("useBoard must be used inside <Board>.");
  return handle.api;
}

type BoardProps = BoardOptions & {
  "aria-label": string;
  className?: string;
  /** What each block shows, like its catalog type's render. Defaults to its label. */
  renderBlock?: (id: string, blocks: BoardBlocks) => React.ReactNode;
  /** Controls drawn over the board, which can call `useBoard()`. */
  children?: React.ReactNode;
};

function Board({ renderBlock, children, className, "aria-label": label, ...options }: BoardProps) {
  const handle = usePrimitive(board, options);
  const root = usePart(handle, "root", {}, {
    "aria-label": label,
    className,
    children: ({ blocks, lines }) => (
      <>
        <parts.World>
          <svg data-slot="board-lines" width="1" height="1">
            {lines.map((line) => (
              <parts.Line key={line.id} value={line.id} strokeWidth={2} />
            ))}
            <parts.Wire strokeWidth={2} strokeDasharray="6 4" />
          </svg>
          {lines.map((line) => (
            <parts.Unlink key={line.id} value={line.id}>
              <XIcon aria-hidden="true" />
            </parts.Unlink>
          ))}
          {Object.keys(blocks).map((id) => (
            <BoardBlock key={id} value={id} data-block={id} data-type={blocks[id].type}>
              {renderBlock ? (
                <div data-slot="board-block-body">{renderBlock(id, blocks)}</div>
              ) : (
                <span data-slot="board-block-label">{blocks[id].label}</span>
              )}
              {sides.map((side) => (
                <BoardPart key={side} slot="port" value={`${id}:${side}`} />
              ))}
              {handles.map((h) => (
                <BoardPart key={h} slot="handle" value={`${id}:${h}`} title={`Resize from ${handleNames[h]}`} />
              ))}
            </BoardBlock>
          ))}
        </parts.World>
        <parts.Lasso />
        <div data-slot="board-controls" className="frost">
          <parts.ZoomOut>
            <MinusIcon aria-hidden="true" />
          </parts.ZoomOut>
          <parts.Zoom />
          <parts.ZoomIn>
            <PlusIcon aria-hidden="true" />
          </parts.ZoomIn>
          <parts.Fit>
            <MaximizeIcon aria-hidden="true" />
          </parts.Fit>
        </div>
        <parts.Instructions>{boardInstructions}</parts.Instructions>
        {children}
      </>
    ),
  });
  return <BoardContext.Provider value={handle}>{root}</BoardContext.Provider>;
}

function BoardBlock({ value, ...props }: { value: string } & PartProps<"div", BoardBlockState>) {
  return usePart(board, "block", { value }, props);
}

// A port or resize handle on a block's edge, keyed `<block>:<side or handle>`.
function BoardPart({ slot, value, ...props }: { slot: "port" | "handle"; value: string } & PartProps<"span">) {
  return usePart(board, slot, { value }, props, "span");
}

export { Board, useBoard, type BoardProps };
