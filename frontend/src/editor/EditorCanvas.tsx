// EditorCanvas — the Konva Stage. Two layers (base image + annotations) and a
// Transformer bound to the current selection. Pointer events are routed to the
// active tool via the TOOLS registry with a freshly-built ToolContext.
//
// COORDINATE MODEL: the base image is fit-scaled into the Stage with offset
// {dx,dy} and uniform `scale`. The annotations layer is wrapped in a single
// <Group x={dx} y={dy} scaleX={scale} scaleY={scale}>, so all annotation coords
// are stored in IMAGE space and render aligned. getPointer() returns image space.
//
// useStrictMode(true): the store is the single source of truth. Every node view
// writes back x/y/rotation/geometry on drag/transform end (see nodes/index.tsx).

import { useEffect, useRef } from 'react';
import type Konva from 'konva';
import { Stage, Layer, Group, Transformer, useStrictMode } from 'react-konva';
import { useEditorStore, BASE_IMAGE_ID } from './store';
import { TOOLS } from './tools';
import { renderNode } from './nodes';
import { toImageSpace } from './geometry';
import type { NodeId, LineNode, ArrowNode } from './types';
import { PointPairHandles } from './PointPairHandles';
import { STAGE_W, STAGE_H, useView, useStageSize, resetFit, zoomAtPointer, getView } from './viewStore';

useStrictMode(true);

// Re-exported for existing importers (exportActions/crop/text). `computeView` is
// the FIT transform with NO user zoom; the live zoom/pan lives in ./viewStore
// and is read via useView()/getView(). Export uses the fit so the saved PNG is
// always native resolution regardless of on-screen zoom.
export { STAGE_W, STAGE_H };
export { computeFit as computeView } from './viewStore';

export interface EditorCanvasProps {
  /** Exposes the Stage ref upward (for export/flatten). */
  stageRef: React.RefObject<Konva.Stage | null>;
}

export function EditorCanvas({ stageRef }: EditorCanvasProps) {
  const nodes = useEditorStore((s) => s.nodes);
  const selectedId = useEditorStore((s) => s.selectedId);
  const activeTool = useEditorStore((s) => s.activeTool);

  const trRef = useRef<Konva.Transformer>(null);
  const nodeRefs = useRef<Record<NodeId, Konva.Node>>({});

  const base = nodes[0];
  const baseW = base && base.type === 'image' ? base.width : 0;
  const baseH = base && base.type === 'image' ? base.height : 0;
  // Live view (fit + user Ctrl+wheel zoom/pan), shared with the crop/text overlays.
  const view = useView();
  // Live stage size — fixed defaults on the standalone route, the crop region's
  // CSS size when embedded in the overlay edit mode.
  const { w: sw, h: sh } = useStageSize();

  const annotationNodes = nodes.slice(1);
  const tool = TOOLS[activeTool];

  // A selected line/arrow uses custom endpoint/curve handles instead of the
  // resize/rotate Transformer.
  const selectedNode = selectedId ? nodes.find((n) => n.id === selectedId) ?? null : null;
  const selectedPointPair =
    activeTool === 'select' && selectedNode && (selectedNode.type === 'line' || selectedNode.type === 'arrow')
      ? (selectedNode as LineNode | ArrowNode)
      : null;

  // Re-fit (and clear any zoom) whenever the base image size changes — a fresh
  // screenshot loads, or a crop shrinks the canvas.
  useEffect(() => {
    resetFit(baseW, baseH);
  }, [baseW, baseH]);

  // Set the Stage container cursor to the active tool's cursor.
  useEffect(() => {
    const c = stageRef.current?.container();
    if (c) c.style.cursor = tool.cursor;
  }, [tool, stageRef]);

  // Bind the Transformer to the selected node (select tool only). Line/arrow
  // nodes are EXCLUDED — they use PointPairHandles, not the resize/rotate box.
  useEffect(() => {
    const tr = trRef.current;
    if (!tr) return;
    const node = selectedId ? nodes.find((n) => n.id === selectedId) : undefined;
    const isPointPair = !!node && (node.type === 'line' || node.type === 'arrow');
    const usesTransformer =
      !!selectedId && selectedId !== BASE_IMAGE_ID && activeTool === 'select' && !isPointPair;
    const sel = usesTransformer ? nodeRefs.current[selectedId] : null;
    tr.nodes(sel ? [sel] : []);
    tr.getLayer()?.batchDraw();
  }, [selectedId, activeTool, nodes]);

  const attachRef = (id: NodeId, node: Konva.Node | null) => {
    if (node) nodeRefs.current[id] = node;
    else delete nodeRefs.current[id];
  };

  function buildCtx() {
    const stage = stageRef.current;
    if (!stage) return null;
    return {
      store: useEditorStore.getState(),
      stage,
      // Read the LIVE view so pointer->image mapping stays correct while zoomed.
      getPointer: () => toImageSpace(stage, getView()),
    };
  }

  const onDown = (e: Konva.KonvaEventObject<PointerEvent>) => {
    const ctx = buildCtx();
    if (ctx) tool.onPointerDown(e, ctx);
  };
  const onMove = (e: Konva.KonvaEventObject<PointerEvent>) => {
    const ctx = buildCtx();
    if (ctx) tool.onPointerMove(e, ctx);
  };
  const onUp = (e: Konva.KonvaEventObject<PointerEvent>) => {
    const ctx = buildCtx();
    if (ctx) tool.onPointerUp(e, ctx);
  };

  // Mouse wheel (and trackpad pinch) zooms toward the cursor. preventDefault so
  // the page never scrolls under the canvas.
  const onWheel = (e: Konva.KonvaEventObject<WheelEvent>) => {
    const evt = e.evt;
    evt.preventDefault();
    const p = stageRef.current?.getPointerPosition();
    if (!p) return;
    zoomAtPointer(p.x, p.y, evt.deltaY);
  };

  const selectMode = activeTool === 'select';

  return (
    <Stage
      ref={stageRef}
      width={sw}
      height={sh}
      onPointerDown={onDown}
      onPointerMove={onMove}
      onPointerUp={onUp}
      onWheel={onWheel}
    >
      {/* base image — stored in image space, rendered through the SAME view
          Group as the annotations so it is fit-scaled + pixel-aligned with them
          (and with the crop/text overlays and the export crop). Hit-testable
          only in select mode. The `view-group` name lets the export path
          neutralise the live zoom (see exportActions). */}
      <Layer listening={selectMode}>
        <Group name="view-group" x={view.dx} y={view.dy} scaleX={view.scale} scaleY={view.scale}>
          {base ? renderNode(base, attachRef, selectMode) : null}
        </Group>
      </Layer>

      {/* annotations — stored in image space, rendered through the view Group */}
      <Layer>
        <Group name="view-group" x={view.dx} y={view.dy} scaleX={view.scale} scaleY={view.scale}>
          {annotationNodes.map((n) => renderNode(n, attachRef, selectMode))}
          {selectedPointPair && (
            <PointPairHandles node={selectedPointPair} viewScale={view.scale} />
          )}
        </Group>
        <Transformer
          ref={trRef}
          rotateEnabled
          keepRatio={false}
          ignoreStroke
          // Hide the bounding-box border lines so they don't obscure the shape;
          // keep the resize/rotate anchors for manipulation.
          borderEnabled={false}
          boundBoxFunc={(oldBox, newBox) =>
            newBox.width < 5 || newBox.height < 5 ? oldBox : newBox
          }
        />
      </Layer>
    </Stage>
  );
}
