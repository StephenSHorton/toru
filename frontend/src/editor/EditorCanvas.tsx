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
import { toImageSpace, type ViewTransform } from './geometry';
import type { NodeId } from './types';

useStrictMode(true);

export const STAGE_W = 900;
export const STAGE_H = 540;

/** Compute the fit-scale transform for a width×height image inside the Stage. */
export function computeView(imgW: number, imgH: number): ViewTransform & { dw: number; dh: number } {
  if (!imgW || !imgH) return { dx: 0, dy: 0, scale: 1, dw: STAGE_W, dh: STAGE_H };
  const scale = Math.min(STAGE_W / imgW, STAGE_H / imgH);
  const dw = imgW * scale;
  const dh = imgH * scale;
  return { dx: (STAGE_W - dw) / 2, dy: (STAGE_H - dh) / 2, scale, dw, dh };
}

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
  const view = computeView(baseW, baseH);

  const annotationNodes = nodes.slice(1);
  const tool = TOOLS[activeTool];

  // Set the Stage container cursor to the active tool's cursor.
  useEffect(() => {
    const c = stageRef.current?.container();
    if (c) c.style.cursor = tool.cursor;
  }, [tool, stageRef]);

  // Bind the Transformer to the selected node (select tool only).
  useEffect(() => {
    const tr = trRef.current;
    if (!tr) return;
    const sel =
      selectedId && selectedId !== BASE_IMAGE_ID && activeTool === 'select'
        ? nodeRefs.current[selectedId]
        : null;
    tr.nodes(sel ? [sel] : []);
    tr.getLayer()?.batchDraw();
  }, [selectedId, activeTool, nodes.length]);

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
      getPointer: () => toImageSpace(stage, view),
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

  const selectMode = activeTool === 'select';

  return (
    <Stage
      ref={stageRef}
      width={STAGE_W}
      height={STAGE_H}
      onPointerDown={onDown}
      onPointerMove={onMove}
      onPointerUp={onUp}
    >
      {/* base image — stored in image space, rendered through the SAME view
          Group as the annotations so it is fit-scaled + pixel-aligned with them
          (and with the crop/text overlays and the export crop). Hit-testable
          only in select mode. */}
      <Layer listening={selectMode}>
        <Group x={view.dx} y={view.dy} scaleX={view.scale} scaleY={view.scale}>
          {base ? renderNode(base, attachRef, selectMode) : null}
        </Group>
      </Layer>

      {/* annotations — stored in image space, rendered through the view Group */}
      <Layer>
        <Group x={view.dx} y={view.dy} scaleX={view.scale} scaleY={view.scale}>
          {annotationNodes.map((n) => renderNode(n, attachRef, selectMode))}
        </Group>
        <Transformer
          ref={trRef}
          rotateEnabled
          keepRatio={false}
          ignoreStroke
          boundBoxFunc={(oldBox, newBox) =>
            newBox.width < 5 || newBox.height < 5 ? oldBox : newBox
          }
        />
      </Layer>
    </Stage>
  );
}
