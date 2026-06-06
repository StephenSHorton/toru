// PointPairHandles — the custom selection UI for line/arrow nodes. Instead of the
// resize/rotate Transformer (a bounding box that obscures the shape and allows
// scaling/rotation we don't want for a vector), a selected line/arrow gets:
//   • a draggable handle at each ENDPOINT (re-aim the segment), and
//   • a draggable MIDDLE handle that bends it into a smooth curve (drag a straight
//     segment's midpoint to add a control point; isCurvedPoints picks up the 3rd
//     point and the renderer applies tension).
// No rotation handle, no resize box.
//
// Rendered INSIDE the annotations view-group (image space), so handle positions
// are plain image coordinates; radius/stroke are divided by the view scale to
// stay a constant on-screen size at any zoom. Each handle is named 'pp-handle'
// so the export path hides it (see exportActions). Handles stop event bubbling so
// grabbing one never reaches the select tool (which would pan/deselect).

import type Konva from 'konva';
import { Circle } from 'react-konva';
import { useEditorStore } from './store';
import type { LineNode, ArrowNode } from './types';

/** On-screen handle radius / stroke (px), kept constant regardless of zoom. */
const HANDLE_R = 6;
const HANDLE_STROKE = 1.5;

export interface PointPairHandlesProps {
  node: LineNode | ArrowNode;
  /** image->screen scale of the view-group, to keep handles a constant size. */
  viewScale: number;
}

export function PointPairHandles({ node, viewScale }: PointPairHandlesProps) {
  const s = Math.max(viewScale, 1e-4);
  const r = HANDLE_R / s;
  const strokeWidth = HANDLE_STROKE / s;
  const pts = node.points;

  const start = { x: node.x + pts[0], y: node.y + pts[1] };
  const end = { x: node.x + pts[pts.length - 2], y: node.y + pts[pts.length - 1] };
  const curved = pts.length >= 6;
  const mid = curved
    ? { x: node.x + pts[2], y: node.y + pts[3] }
    : { x: node.x + (pts[0] + pts[2]) / 2, y: node.y + (pts[1] + pts[3]) / 2 };

  /** One undo baseline per drag gesture. */
  function beginEdit() {
    useEditorStore.getState().commit();
  }

  function moveEndpoint(which: 'start' | 'end', ix: number, iy: number) {
    const p = [...node.points];
    if (which === 'start') {
      p[0] = ix - node.x;
      p[1] = iy - node.y;
    } else {
      p[p.length - 2] = ix - node.x;
      p[p.length - 1] = iy - node.y;
    }
    useEditorStore.getState().mutateDrawingNode(node.id, { points: p });
  }

  function moveMid(ix: number, iy: number) {
    const p = node.points;
    const mx = ix - node.x;
    const my = iy - node.y;
    // Promote a straight 2-point segment to a 3-point curve on first drag.
    const next =
      p.length >= 6
        ? [p[0], p[1], mx, my, p[p.length - 2], p[p.length - 1]]
        : [p[0], p[1], mx, my, p[2], p[3]];
    useEditorStore.getState().mutateDrawingNode(node.id, { points: next });
  }

  function handle(
    key: string,
    pos: { x: number; y: number },
    fill: string,
    onMove: (ix: number, iy: number) => void,
  ) {
    return (
      <Circle
        key={key}
        name="pp-handle"
        x={pos.x}
        y={pos.y}
        radius={r}
        fill={fill}
        stroke="#0a84ff"
        strokeWidth={strokeWidth}
        draggable
        // Don't let grabbing a handle reach the select tool (pan/deselect).
        onPointerDown={(e: Konva.KonvaEventObject<PointerEvent>) => {
          e.cancelBubble = true;
        }}
        onDragStart={beginEdit}
        onDragMove={(e: Konva.KonvaEventObject<DragEvent>) => onMove(e.target.x(), e.target.y())}
      />
    );
  }

  return (
    <>
      {handle('start', start, '#ffffff', (ix, iy) => moveEndpoint('start', ix, iy))}
      {handle('end', end, '#ffffff', (ix, iy) => moveEndpoint('end', ix, iy))}
      {handle('mid', mid, curved ? '#0a84ff' : '#bcd9ff', moveMid)}
    </>
  );
}
