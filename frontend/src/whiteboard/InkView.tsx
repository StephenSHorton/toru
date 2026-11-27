// Freehand ink in React: the pen's surface and the stroke being drawn (ink.ts's
// primitive), and a finished stroke drawn inside its board block.
import { usePart, usePrimitive } from "@ui-sdk/react/primitive";
import { ink, inkColor, inkPath, inkShape, maxPressureScale, type InkOptions, type InkStroke } from "./ink";
import "./ink.css";

type InkHandle = ReturnType<typeof usePrimitive<typeof ink>>;

/** The pen for a board: `toCanvas` and `onStroke` come from the board. */
function usePen(options: InkOptions): InkHandle {
  return usePrimitive(ink, options);
}

/** The pen's surface over the board. Render it above the world, below the board's controls. */
function InkSurface({ pen }: { pen: InkHandle }) {
  return usePart(pen, "surface", {}, {}, "div");
}

/** The stroke being drawn. Render it inside an `<svg>` in the board's world. */
function InkDraft({ pen }: { pen: InkHandle }) {
  return usePart(pen, "draft", {}, {}, "path");
}

/**
 * A finished stroke, filling its block. A stylus stroke is a filled outline
 * that follows its pressure; a mouse stroke is a line of one width (`inkShape`).
 * The wide clear path under the line is what a press hits, so a stroke
 * selects where its ink is, not across its box.
 */
function InkLine({ stroke, width, height }: { stroke: InkStroke; width: number; height: number }) {
  const shape = inkShape(stroke.points, stroke.width);
  const color = inkColor(stroke.color);
  const widest = stroke.width * maxPressureScale(stroke.points);
  return (
    <svg data-slot="board-ink" width={width} height={height} aria-hidden="true">
      <path data-slot="board-ink-hit" d={inkPath(stroke.points)} strokeWidth={Math.max(widest + 10, 14)} />
      {shape.filled ? (
        <path data-slot="board-ink-line" data-filled="" d={shape.d} fill={color} />
      ) : (
        <path data-slot="board-ink-line" d={shape.d} stroke={color} strokeWidth={stroke.width} />
      )}
    </svg>
  );
}

export { InkDraft, InkLine, InkSurface, usePen, type InkHandle };
