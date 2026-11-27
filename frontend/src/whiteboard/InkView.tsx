// Freehand ink in React: the pen's surface and the stroke being drawn (ink.ts's
// primitive), and a finished stroke drawn inside its board block.
import type * as React from "react";
import { usePart, usePrimitive } from "@ui-sdk/react/primitive";
import { ink, inkColor, inkPath, inkShape, maxPressureScale, type InkOptions, type InkStroke } from "./ink";
import { arrowHeadPoints, arrowShaftEnd, boxOf, lineHeight, localEnds, textLayout } from "./shapes";
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
  if (stroke.kind) return <ShapeLine stroke={stroke} width={width} height={height} />;
  const shape = inkShape(stroke.points, stroke.width);
  const color = inkColor(stroke.color);
  const widest = stroke.width * maxPressureScale(stroke.points);
  return (
    <svg data-slot="board-ink" data-highlight={stroke.highlight ? "" : undefined} width={width} height={height} aria-hidden="true">
      <path data-slot="board-ink-hit" d={inkPath(stroke.points)} strokeWidth={Math.max(widest + 10, 14)} />
      {shape.filled ? (
        <path data-slot="board-ink-line" data-filled="" d={shape.d} fill={color} />
      ) : (
        <path data-slot="board-ink-line" d={shape.d} stroke={color} strokeWidth={stroke.width} />
      )}
    </svg>
  );
}

/**
 * A shape (shapes.ts), filling its block: a rectangle, ellipse, line, arrow,
 * text label or emoji, in its colour, width and fill. Like a stroke, it takes
 * presses only where it shows: its outline, its inside when it's filled, or
 * its box for a text label or emoji.
 */
function ShapeLine({ stroke, width, height }: { stroke: InkStroke; width: number; height: number }) {
  const [a, b] = localEnds(stroke);
  const color = inkColor(stroke.color);
  const fill = stroke.fill ? inkColor(stroke.fill) : undefined;
  const box = boxOf(a, b);
  const hitWidth = Math.max(stroke.width + 10, 14);
  let body: React.ReactNode;
  let hit: React.ReactNode;
  switch (stroke.kind) {
    case "rect":
      body = (
        <rect data-slot="board-shape-line" x={box.x} y={box.y} width={box.w} height={box.h} rx={Math.min(4, box.w / 4, box.h / 4)} stroke={color} strokeWidth={stroke.width} fill={fill ?? "none"} data-fill={fill ? "" : undefined} />
      );
      hit = <rect data-slot="board-ink-hit" data-fill={fill ? "" : undefined} x={box.x} y={box.y} width={box.w} height={box.h} strokeWidth={hitWidth} />;
      break;
    case "ellipse":
      body = (
        <ellipse data-slot="board-shape-line" cx={box.x + box.w / 2} cy={box.y + box.h / 2} rx={box.w / 2} ry={box.h / 2} stroke={color} strokeWidth={stroke.width} fill={fill ?? "none"} data-fill={fill ? "" : undefined} />
      );
      hit = <ellipse data-slot="board-ink-hit" data-fill={fill ? "" : undefined} cx={box.x + box.w / 2} cy={box.y + box.h / 2} rx={box.w / 2} ry={box.h / 2} strokeWidth={hitWidth} />;
      break;
    case "line":
    case "arrow": {
      const end = stroke.kind === "arrow" ? arrowShaftEnd(a, b, stroke.width) : b;
      const head = stroke.kind === "arrow" ? arrowHeadPoints(a, b, stroke.width) : null;
      body = (
        <>
          <line data-slot="board-shape-line" x1={a.x} y1={a.y} x2={end.x} y2={end.y} stroke={color} strokeWidth={stroke.width} />
          {head && <polygon data-slot="board-shape-head" points={head.map((p) => `${p.x},${p.y}`).join(" ")} fill={color} stroke={color} strokeWidth={Math.min(stroke.width, 3)} />}
        </>
      );
      hit = <line data-slot="board-ink-hit" x1={a.x} y1={a.y} x2={b.x} y2={b.y} strokeWidth={hitWidth} />;
      break;
    }
    case "text": {
      const { lines, size } = textLayout(stroke.text ?? "", box.h);
      body = (
        <text data-slot="board-shape-text" x={box.x} y={box.y} fontSize={size} fill={color}>
          {lines.map((line, i) => (
            <tspan key={i} x={box.x} y={box.y + size * (i * lineHeight + 1)}>
              {line || "\u00a0"}
            </tspan>
          ))}
        </text>
      );
      hit = <rect data-slot="board-ink-hit" data-fill="" x={box.x} y={box.y} width={box.w} height={box.h} />;
      break;
    }
    case "emoji": {
      const size = Math.min(box.w, box.h);
      body = (
        <text data-slot="board-shape-emoji" x={box.x + box.w / 2} y={box.y + box.h / 2} fontSize={size * 0.86} textAnchor="middle" dominantBaseline="central">
          {stroke.text}
        </text>
      );
      hit = <rect data-slot="board-ink-hit" data-fill="" x={box.x} y={box.y} width={box.w} height={box.h} />;
      break;
    }
  }
  return (
    <svg data-slot="board-ink" data-shape={stroke.kind} width={width} height={height} aria-hidden="true">
      {hit}
      {body}
    </svg>
  );
}

export { InkDraft, InkLine, InkSurface, ShapeLine, usePen, type InkHandle };
