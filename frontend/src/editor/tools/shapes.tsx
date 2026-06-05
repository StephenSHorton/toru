// RECTANGLE + ELLIPSE tools.
//
// Both draw by click-dragging from a corner of the shape's bounding box.
// Holding Shift constrains the drag to a square / circle (via the foundation's
// `squareFromDrag` helper, which yields a top-left-origin, positive-size box).
//
// Geometry conventions (must match editor/nodes/index.tsx renderers):
//   - RectNode  : (x, y) is the TOP-LEFT corner; width/height span the box.
//   - EllipseNode: (x, y) is the CENTER; radiusX/radiusY are half the box size.
//
// History model (per store conventions in store.ts):
//   pointer-down  -> addNode(draft)            : ONE history step ("shape created")
//   pointer-move  -> mutateDrawingNode(...)    : live resize, NO history
//   pointer-up    -> select(id)                : finalize, OR
//                    abortDraft(id)            : discard a zero-size draft cleanly
// Net result: exactly one undo entry per committed shape, none for a discarded
// (tap, no drag) draft. abortDraft pops the addNode snapshot WITHOUT polluting
// the redo stack or clearing selection (which undo() would do).

import type Konva from 'konva';
import type { Tool, ToolContext } from './types';
import type { EditorNode, RectNode, EllipseNode, NodeId } from '../types';
import { newId, squareFromDrag, dist } from '../geometry';

/** Minimum bounding-box diagonal (image px) below which a draft is discarded. */
const MIN_DRAG = 4;

/** Per-gesture drawing state. Module-scoped because tools are singletons; only
 * one pointer gesture can be active at a time, and start/end are paired. */
interface DrawState {
  id: NodeId;
  startX: number;
  startY: number;
}

let active: DrawState | null = null;

/** Common pointer-up finalize/discard shared by both shape tools. */
function finish(ctx: ToolContext): void {
  if (!active) return;
  const { id, startX, startY } = active;
  active = null;

  const p = ctx.getPointer();
  // If we never got a valid end pointer or the drag was negligible, discard the
  // draft AND its creation history step in one clean step (no redo-stack noise,
  // no selection wipe).
  if (!p || dist(startX, startY, p.x, p.y) < MIN_DRAG) {
    ctx.store.abortDraft(id);
    return;
  }
  // Committed shape: select it so the user can immediately move/resize/recolor.
  ctx.store.select(id);
}

function shiftHeld(e: Konva.KonvaEventObject<PointerEvent>): boolean {
  const evt = e.evt as PointerEvent;
  return evt.shiftKey === true;
}

export const rectTool: Tool = {
  id: 'rect',
  cursor: 'crosshair',

  onPointerDown(_e, ctx) {
    const p = ctx.getPointer();
    if (!p) return;
    const s = ctx.store;
    const id = newId('rect');
    const draft: RectNode = {
      id,
      type: 'rect',
      x: p.x,
      y: p.y,
      width: 0,
      height: 0,
      rotation: 0,
      opacity: 1,
      draggable: true,
      stroke: s.activeColor,
      strokeWidth: s.activeStrokeWidth,
      // No fill by default (outline rectangle). The color palette / fill control
      // can add one later via the store's setColor / updateNode path.
    };
    active = { id, startX: p.x, startY: p.y };
    s.addNode(draft as EditorNode);
  },

  onPointerMove(e, ctx) {
    if (!active) return;
    const p = ctx.getPointer();
    if (!p) return;
    const box = squareFromDrag(active.startX, active.startY, p.x, p.y, shiftHeld(e));
    ctx.store.mutateDrawingNode(active.id, {
      x: box.x,
      y: box.y,
      width: box.width,
      height: box.height,
    } as Partial<EditorNode>);
  },

  onPointerUp(_e, ctx) {
    finish(ctx);
  },
};

export const ellipseTool: Tool = {
  id: 'ellipse',
  cursor: 'crosshair',

  onPointerDown(_e, ctx) {
    const p = ctx.getPointer();
    if (!p) return;
    const s = ctx.store;
    const id = newId('ellipse');
    const draft: EllipseNode = {
      id,
      type: 'ellipse',
      // Center-origin: starts as a zero-radius point at the press location.
      x: p.x,
      y: p.y,
      radiusX: 0,
      radiusY: 0,
      rotation: 0,
      opacity: 1,
      draggable: true,
      stroke: s.activeColor,
      strokeWidth: s.activeStrokeWidth,
    };
    active = { id, startX: p.x, startY: p.y };
    s.addNode(draft as EditorNode);
  },

  onPointerMove(e, ctx) {
    if (!active) return;
    const p = ctx.getPointer();
    if (!p) return;
    // Drag defines the bounding box; convert to center + radii for Konva.Ellipse.
    const box = squareFromDrag(active.startX, active.startY, p.x, p.y, shiftHeld(e));
    ctx.store.mutateDrawingNode(active.id, {
      x: box.x + box.width / 2,
      y: box.y + box.height / 2,
      radiusX: box.width / 2,
      radiusY: box.height / 2,
    } as Partial<EditorNode>);
  },

  onPointerUp(_e, ctx) {
    finish(ctx);
  },
};
