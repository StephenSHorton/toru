// SELECT tool: pointer-down hit-tests + canvas panning.
//
// Clicking an annotation node selects it; clicking the Stage background or the
// base image clears selection. Drag/resize/rotate of a node is handled by its
// `draggable` flag + the Transformer, written back to the store in
// onDragEnd/onTransformEnd (see node views). The tool owns selection AND — when
// zoomed in — panning: a left-drag that starts on empty canvas (not on an
// annotation, not on a Transformer handle) pans the view; a click without a drag
// still clears the selection.

import type Konva from 'konva';
import type { Tool, ToolContext } from './types';
import { BASE_IMAGE_ID } from '../store';
import { panBy, canPan } from '../viewStore';

/** Drag-to-pan gesture state (singleton; only one pointer gesture at a time). */
interface PanState {
  lastX: number;
  lastY: number;
  moved: boolean;
}
let pan: PanState | null = null;

/** Pixels of movement before a press becomes a pan (vs a click-to-deselect). */
const PAN_THRESHOLD = 3;

/** True when the hit node is part of a Transformer (its anchors/rotater/border). */
function isTransformerPart(node: Konva.Node | null): boolean {
  let n: Konva.Node | null = node;
  while (n) {
    if (typeof n.getClassName === 'function' && n.getClassName() === 'Transformer') return true;
    n = n.getParent();
  }
  return false;
}

/** Resolve the annotation id a hit node belongs to ('' for background/base/chrome). */
function annotationIdOf(target: Konva.Node, stage: Konva.Stage | null): string {
  if (target === stage) return '';
  let id = target.id();
  if (!id) {
    const parent = target.getParent();
    id = parent ? parent.id() : '';
  }
  return id && id !== BASE_IMAGE_ID ? id : '';
}

function setCursor(stage: Konva.Stage | null, cursor: string): void {
  const c = stage?.container();
  if (c) c.style.cursor = cursor;
}

export const selectTool: Tool = {
  id: 'select',
  cursor: 'default',

  onPointerDown(e, ctx: ToolContext) {
    const stage = e.target.getStage();
    const target = e.target;

    // Grabbing a Transformer handle to resize/rotate: leave selection + view alone.
    if (isTransformerPart(target)) return;

    const id = annotationIdOf(target, stage);
    if (id) {
      // On an annotation -> select it (Konva handles the node's own drag).
      ctx.store.select(id);
      return;
    }

    // Empty canvas (background / base image). When zoomed in, start a pan; a
    // plain click (no drag) clears the selection on pointer-up. When not zoomed,
    // clear immediately.
    const p = stage?.getPointerPosition();
    if (canPan() && p) {
      pan = { lastX: p.x, lastY: p.y, moved: false };
      setCursor(stage, 'grabbing');
      return;
    }
    ctx.store.select(null);
  },

  onPointerMove(e) {
    if (!pan) return;
    const stage = e.target.getStage();
    const p = stage?.getPointerPosition();
    if (!p) return;
    const dx = p.x - pan.lastX;
    const dy = p.y - pan.lastY;
    if (!pan.moved && Math.hypot(dx, dy) < PAN_THRESHOLD) return;
    pan.moved = true;
    panBy(dx, dy);
    pan.lastX = p.x;
    pan.lastY = p.y;
  },

  onPointerUp(e, ctx: ToolContext) {
    const gesture = pan;
    pan = null;
    const stage = e.target.getStage();
    setCursor(stage, selectTool.cursor);
    // A press on empty canvas that never moved is a click -> clear selection.
    if (gesture && !gesture.moved) ctx.store.select(null);
  },
};
