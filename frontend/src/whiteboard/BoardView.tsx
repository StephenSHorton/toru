// The whiteboard canvas in React: board.ts's primitive rendered as parts, in
// Toru's look (board.css). Rendering follows UI SDK 0.1.0-beta.18's
// examples/experiments/react/canvas.tsx and board.tsx (Apache-2.0, Copyright
// Vercel, Inc.); see board.ts for what changed.
import * as React from "react";
import { createParts, usePart, usePrimitive, type PartProps } from "@ui-sdk/react/primitive";
import { MaximizeIcon, MinusIcon, PlusIcon, XIcon } from "lucide-react";
import { board, boardInstructions, handleNames, sides, type BoardBlocks, type BoardBlockState, type BoardOptions } from "./board";
import { InkDraft, InkLine, InkSurface, usePen } from "./InkView";
import type { DrawingSettings } from "./drawing";
import { createTools, editText, ToolLayer, ToolSurface } from "./DrawingTools";
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
  /**
   * The drawing bar's tool and pen (drawing.ts, DrawingBar.tsx). The pen and
   * highlighter draw freehand ink (ink.ts); the hand, eraser and shape tools
   * work through DrawingTools.tsx. Left out, the board only selects.
   */
  drawing?: DrawingSettings;
  className?: string;
  /**
   * What each block shows, like its catalog type's render. Defaults to its
   * label. It's called again only when that block (or this function)
   * changes, so draw the block from `blocks[id]` alone.
   */
  renderBlock?: (id: string, blocks: BoardBlocks) => React.ReactNode;
  /** Controls drawn over the board, which can call `useBoard()`. */
  children?: React.ReactNode;
};

function Board({ renderBlock, children, className, drawing, "aria-label": label, ...options }: BoardProps) {
  const handle = usePrimitive(board, options);
  const [tools] = React.useState(createTools);
  const tool = drawing?.tool ?? "select";
  const highlight = tool === "highlighter";
  const pen = usePen({
    active: tool === "pen" || highlight,
    color: drawing?.color,
    width: drawing ? (highlight ? drawing.widths.highlighter : drawing.widths.pen) : undefined,
    highlight,
    toCanvas: ({ x, y }) => handle.api.viewport.toCanvas(x, y),
    // A finished stroke goes on the board as a block.
    onStroke: (id, stroke) => {
      handle.api.addBlock(id, stroke);
    },
  });
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
          {frameFirst(blocks).map((id) => (
            <BlockItem key={id} id={id} block={blocks[id]} blocks={blocks} renderBlock={renderBlock} tool={tool} tools={tools} />
          ))}
          {/* The stroke being drawn, above the blocks. */}
          <svg data-slot="board-lines" width="1" height="1">
            <InkDraft pen={pen} />
          </svg>
          {drawing && <ToolLayer board={handle.api} tools={tools} settings={drawing} selection={handle.instance} blocks={blocks} />}
        </parts.World>
        <parts.Lasso />
        <InkSurface pen={pen} />
        {drawing && <ToolSurface board={handle.api} tools={tools} settings={drawing} />}
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

/**
 * Block ids in the order they stack: frames first, so they sit behind the
 * blocks in them, then blocks, then highlighter strokes, then the rest of
 * the ink, so a highlighter is drawn under the ink it highlights.
 */
function frameFirst(blocks: BoardBlocks) {
  const ids = Object.keys(blocks);
  const rank = (id: string) => {
    const b = blocks[id];
    if (b.type === "frame") return 0;
    if (!b.ink) return 1;
    return b.ink.highlight ? 2 : 3;
  };
  return [0, 1, 2, 3].flatMap((r) => ids.filter((id) => rank(id) === r));
}

/** Who a source is, for people: "You" for the person, else the agent's or
 * the other person's name ("person:<name>" is someone at another Toru the
 * board is shared with). */
function who(source: string) {
  if (source === "person") return "You";
  if (source.startsWith("person:")) return source.slice("person:".length);
  return source.startsWith("agent:") ? source.slice("agent:".length) : source;
}

/** "Made by …", and who changed it last when that's someone else. */
function madeBy(block: { createdBy?: string; updatedBy?: string }) {
  if (!block.createdBy) return undefined;
  const made = `Made by ${who(block.createdBy) === "You" ? "you" : who(block.createdBy)}`;
  if (!block.updatedBy || block.updatedBy === block.createdBy) return made;
  return `${made}, last changed by ${who(block.updatedBy) === "You" ? "you" : who(block.updatedBy)}`;
}

type BlockItemProps = {
  id: string;
  block: BoardBlocks[string];
  blocks: BoardBlocks;
  renderBlock?: BoardProps["renderBlock"];
  /** The drawing bar's tool: a text label's words change on a double click with Select. */
  tool: string;
  tools: ReturnType<typeof createTools>;
};

/**
 * One block on the board. It renders again only when its own block or
 * `renderBlock` changes, not on every change to the board: a drop, a vote or
 * an agent's edit then re-renders that block alone, where re-rendering every
 * block body cost a long frame on a full board. Its parts still follow the
 * board's selection, drags and lines by themselves. So `renderBlock` must
 * draw a block from that block alone, as Toru's do.
 */
const BlockItem = React.memo(
  function BlockItem({ id, block, blocks, renderBlock, tool, tools }: BlockItemProps) {
    // A freehand stroke or a shape shows its ink, with no ports or resize
    // handles (a selected shape's handles are DrawingTools'). A double click
    // on a text label changes its words.
    if (block.ink) {
      const ink = block.ink;
      return (
        <BoardBlock
          value={id}
          data-block={id}
          data-by={block.createdBy}
          data-shape={ink.kind}
          title={madeBy(block)}
          onDoubleClick={ink.kind === "text" && tool === "select" ? () => editText(tools, id, { ...block, ink }) : undefined}
        >
          <InkLine stroke={ink} width={block.width} height={block.height} />
        </BoardBlock>
      );
    }
    return (
      <BoardBlock value={id} data-block={id} data-type={block.type} data-by={block.createdBy}>
        {block.createdBy && (
          <span data-slot="board-block-by" data-agent={block.createdBy.startsWith("agent:") ? "" : undefined} title={madeBy(block)}>
            {who(block.createdBy)}
          </span>
        )}
        {renderBlock ? (
          <div data-slot="board-block-body">{renderBlock(id, blocks)}</div>
        ) : (
          <span data-slot="board-block-label">{block.label}</span>
        )}
        {sides.map((side) => (
          <BoardPart key={side} slot="port" value={`${id}:${side}`} />
        ))}
        {handles.map((h) => (
          <BoardPart key={h} slot="handle" value={`${id}:${h}`} title={`Resize from ${handleNames[h]}`} />
        ))}
      </BoardBlock>
    );
  },
  (a, b) =>
    a.id === b.id && a.renderBlock === b.renderBlock && a.tool === b.tool && a.tools === b.tools && sameLook(a.block, b.block),
);

/**
 * Whether a block draws the same: everything but where it is. The board
 * places and sizes a block's element itself, so a move or resize needn't
 * render the block again; doing so rewrote its fields, and a field rewritten
 * inside the board makes the browser restyle every block (UI SDK's :has()
 * rules), a long frame on each drop. Ink blocks have no fields, so they
 * follow every change.
 */
function sameLook(a: BoardBlocks[string], b: BoardBlocks[string]) {
  if (a === b) return true;
  return (
    a.label === b.label &&
    a.type === b.type &&
    a.props === b.props &&
    a.ink === b.ink &&
    a.createdBy === b.createdBy &&
    a.updatedBy === b.updatedBy &&
    // An ink block is a light svg: it follows its size, and its place too,
    // which a text label's double click edits from.
    (!a.ink || (a.x === b.x && a.y === b.y && a.width === b.width && a.height === b.height))
  );
}

function BoardBlock({ value, ...props }: { value: string } & PartProps<"div", BoardBlockState>) {
  return usePart(board, "block", { value }, props);
}

// A port or resize handle on a block's edge, keyed `<block>:<side or handle>`.
function BoardPart({ slot, value, ...props }: { slot: "port" | "handle"; value: string } & PartProps<"span">) {
  return usePart(board, slot, { value }, props, "span");
}

export { Board, madeBy, useBoard, who, type BoardHandle, type BoardProps };
