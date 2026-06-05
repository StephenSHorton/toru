// PEN tool — freehand drawing.
//
// Pointer-drag builds a smoothed Konva.Line (tension 0.5) using the store's
// activeColor + activeStrokeWidth, and commits exactly one PenNode (one history
// step) on pointer-up. A click without a drag draws nothing and touches no
// history.
//
// COORDINATE MODEL: ctx.getPointer() already returns IMAGE space (the annotations
// Group is offset+scaled to the fit-scaled base image). We store ABSOLUTE
// image-space points in PenNode.points with x=0/y=0 — exactly how PenView renders
// (it passes points through unchanged inside the scaled Group). See
// editor/nodes/index.tsx (PenView) + editor/EditorCanvas.tsx (view Group).
//
// HISTORY: the store has a no-history `mutateDrawingNode` for in-progress drawing
// but no no-history *insert*. So we defer node creation until the first qualifying
// move (guaranteeing any created node is a genuine stroke, never a stray tap),
// then `addNode` ONCE — which records a single pre-stroke snapshot — and extend it
// live with `mutateDrawingNode` (no extra history). Result: exactly one undoable
// step per finished stroke, zero for a no-drag tap.

import type { Tool } from './types';
import type { PenNode } from '../types';
import { newId, dist } from '../geometry';

const TENSION = 0.5;
// Minimum image-space gap between recorded points. Drops near-duplicate samples so
// the smoothed polyline stays lean (fewer points → cleaner tension curve, cheaper
// hit-testing) without visibly degrading the stroke.
const MIN_POINT_GAP = 2;

// In-progress stroke state. The tool is a singleton (mirrors selectTool); pointer
// events arrive one stroke at a time, so module-level scratch state is safe.
let drawing = false;            // pointer is down and this tool owns the gesture
let draftId: string | null = null; // id of the live PenNode once created (null until first move)
let points: number[] = [];      // absolute image-space, flat [x0,y0,x1,y1,...]
let lastX = 0;
let lastY = 0;

function reset(): void {
  drawing = false;
  draftId = null;
  points = [];
}

export const penTool: Tool = {
  id: 'pen',
  cursor: 'crosshair',

  onPointerDown(_e, ctx) {
    const p = ctx.getPointer();
    if (!p) {
      reset();
      return;
    }
    // Start a fresh stroke. Node creation is deferred to the first qualifying move
    // so a click-without-drag never produces a node (and never a history step).
    drawing = true;
    draftId = null;
    points = [p.x, p.y];
    lastX = p.x;
    lastY = p.y;
  },

  onPointerMove(_e, ctx) {
    if (!drawing) return;
    const p = ctx.getPointer();
    if (!p) return;

    // Filter near-duplicate samples.
    if (dist(lastX, lastY, p.x, p.y) < MIN_POINT_GAP) return;
    points.push(p.x, p.y);
    lastX = p.x;
    lastY = p.y;

    if (draftId === null) {
      // First real movement → materialize the stroke. addNode records ONE history
      // snapshot (the pre-stroke scene) and is the only history step for this stroke.
      const store = ctx.store;
      const node: PenNode = {
        id: newId('pen'),
        type: 'pen',
        x: 0,
        y: 0,
        rotation: 0,
        opacity: 1,
        draggable: true,
        points: points.slice(),
        stroke: store.activeColor,
        strokeWidth: store.activeStrokeWidth,
        tension: TENSION,
        lineCap: 'round',
        lineJoin: 'round',
      };
      draftId = node.id;
      store.addNode(node);
    } else {
      // Extend the live stroke without adding history.
      ctx.store.mutateDrawingNode(draftId, { points: points.slice() });
    }
  },

  onPointerUp(_e, ctx) {
    // A genuine stroke (draftId set) is already committed via the single addNode
    // history step; just flush the final point set. A no-drag tap created nothing.
    if (drawing && draftId !== null) {
      ctx.store.mutateDrawingNode(draftId, { points: points.slice() });
    }
    reset();
  },
};
