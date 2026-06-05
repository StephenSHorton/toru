// ARROW + LINE tools — click-drag from start to end, Shift snaps the vector to
// the nearest 45°. ARROW draws a Konva.Arrow head; LINE is a plain stroke.
//
// DRAWING / HISTORY MODEL (per store contract §2.2):
//   - pointer-down only records the start point (image space). No node yet, so a
//     bare click leaves zero spurious history.
//   - the FIRST pointer-move past a tiny threshold calls addNode(draft) — exactly
//     ONE history step is recorded (the pre-draw snapshot). The node is then
//     LIVE-updated with mutateDrawingNode (NO further history) for the rest of the
//     drag, so one undo removes the whole shape.
//   - pointer-up finalizes. A degenerate (zero-length) drag never produced a node,
//     so nothing to clean up.
//
// Coords are stored in IMAGE space. LineNode/ArrowNode keep the node origin at the
// drag start (x,y) and store points as a LOCAL [0,0, dx,dy] segment — this matches
// the renderNode() views (which read node.x/node.y + node.points) and keeps drag
// write-back (which moves x,y) consistent.

import type { Tool, ToolContext } from './types';
import type { ArrowNode, EditorNode, LineNode } from '../types';
import { useEditorStore } from '../store';
import { newId, constrainAngle45, dist } from '../geometry';

// Minimum drag length (image px) before a node is created / kept.
const MIN_LEN = 2;

interface DraftState {
  id: string | null; // null until the draft node has actually been added
  startX: number;
  startY: number;
}

/** Resolve the end point of the current drag, honoring Shift -> 45° snap. */
function resolveEnd(
  draft: DraftState,
  ctx: ToolContext,
  shift: boolean,
): { x: number; y: number } | null {
  const p = ctx.getPointer();
  if (!p) return null;
  if (shift) return constrainAngle45(draft.startX, draft.startY, p.x, p.y);
  return p;
}

/**
 * Build the LOCAL point pair for a node anchored at (startX, startY).
 * Stored as [0, 0, dx, dy] so node.x/node.y is the segment origin (drag-friendly).
 */
function localPoints(draft: DraftState, end: { x: number; y: number }): number[] {
  return [0, 0, end.x - draft.startX, end.y - draft.startY];
}

/** Shared factory for the two near-identical point-pair tools. */
function makePointPairTool(kind: 'arrow' | 'line'): Tool {
  // Per-tool drag state. A tool is a singleton, and only one drag is ever in
  // flight (pointer capture is 1:1), so module-local mutable state is safe.
  let draft: DraftState | null = null;

  function createNode(
    id: string,
    draftState: DraftState,
    end: { x: number; y: number },
  ): ArrowNode | LineNode {
    const s = useEditorStore.getState();
    const stroke = s.activeColor;
    const strokeWidth = s.activeStrokeWidth;
    const points = localPoints(draftState, end);

    if (kind === 'arrow') {
      const head = Math.max(8, strokeWidth * 3);
      const node: ArrowNode = {
        id,
        type: 'arrow',
        x: draftState.startX,
        y: draftState.startY,
        rotation: 0,
        opacity: 1,
        draggable: true,
        points,
        stroke,
        strokeWidth,
        pointerLength: head,
        pointerWidth: head,
        fill: stroke,
      };
      return node;
    }

    const node: LineNode = {
      id,
      type: 'line',
      x: draftState.startX,
      y: draftState.startY,
      rotation: 0,
      opacity: 1,
      draggable: true,
      points,
      stroke,
      strokeWidth,
    };
    return node;
  }

  return {
    id: kind,
    cursor: 'crosshair',

    onPointerDown(_e, ctx) {
      const p = ctx.getPointer();
      if (!p) {
        draft = null;
        return;
      }
      // Record the anchor. The node itself is created lazily on first real move.
      draft = { id: null, startX: p.x, startY: p.y };
    },

    onPointerMove(e, ctx) {
      if (!draft) return;
      const end = resolveEnd(draft, ctx, e.evt.shiftKey);
      if (!end) return;

      const len = dist(draft.startX, draft.startY, end.x, end.y);

      // Lazily create the node once the drag is meaningful — exactly one history
      // step. Bare clicks / sub-threshold jitters never spawn a node.
      if (!draft.id) {
        if (len < MIN_LEN) return;
        const id = newId(kind);
        const store = useEditorStore.getState();
        store.addNode(createNode(id, draft, end));
        store.select(id);
        draft.id = id;
        return;
      }

      // Live-update geometry without touching history.
      useEditorStore.getState().mutateDrawingNode(draft.id, {
        points: localPoints(draft, end),
      } as Partial<EditorNode>);
    },

    onPointerUp(e, ctx) {
      const active = draft;
      draft = null;
      if (!active || !active.id) return; // never crossed the threshold

      const end = resolveEnd(active, ctx, e.evt.shiftKey);
      const store = useEditorStore.getState();

      if (end) {
        const len = dist(active.startX, active.startY, end.x, end.y);
        if (len < MIN_LEN) {
          // Collapsed back to ~nothing (dragged out then back to origin): unwind
          // the addNode history step so undo isn't littered with an empty shape.
          // abortDraft removes the node + pops its pre-draw snapshot WITHOUT
          // pushing a no-op onto the redo stack (which undo() would do).
          store.abortDraft(active.id);
          return;
        }
        // Final geometry as a clean live write (history step from addNode stands).
        store.mutateDrawingNode(active.id, {
          points: localPoints(active, end),
        } as Partial<EditorNode>);
      }

      store.setTool('select');
    },
  };
}

/** ARROW tool — straight segment terminated by a filled arrowhead. */
export const arrowTool: Tool = makePointPairTool('arrow');

/** LINE tool — straight stroked segment, no head. */
export const lineTool: Tool = makePointPairTool('line');
