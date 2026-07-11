// renderNode — the node dispatcher. Maps each EditorNode to its Konva component,
// attaches the live Konva instance via `attachRef` (so the Transformer can bind),
// and wires drag/transform write-back to the store.
//
// THE #1 KONVA BUG (handled here): the Transformer bakes resize into
// scaleX/scaleY, NOT width/height. In onTransformEnd we BAKE scale into geometry
// then reset scale to 1, every type. onDragEnd writes {x,y} (or scaled points
// for point-pairs). With useStrictMode(true) the store is the source of truth,
// so EVERY drag/transform MUST write back or the node snaps to stored coords.

import type Konva from 'konva';
import { Image as KImage, Rect, Ellipse, Line, Arrow, Text } from 'react-konva';
import { useEffect, useRef, useState } from 'react';
import type {
  EditorNode, ImageNodeBase, PastedImageNode, PenNode, RectNode,
  EllipseNode, LineNode, ArrowNode, TextNode, EmojiNode, NodeId,
} from '../types';
import { useEditorStore } from '../store';

/** Load an HTMLImageElement (crossOrigin anonymous so toDataURL doesn't taint). */
function useHTMLImage(src: string): HTMLImageElement | null {
  const [img, setImg] = useState<HTMLImageElement | null>(null);
  useEffect(() => {
    const i = new window.Image();
    i.crossOrigin = 'anonymous';
    i.onload = () => setImg(i);
    i.src = src;
  }, [src]);
  return img;
}

type AttachRef = (id: NodeId, node: Konva.Node | null) => void;

// A line/arrow with a 3rd (middle) control point renders as a smooth curve that
// passes through it; a plain 2-point segment uses no tension. Kept in sync with
// PointPairHandles (which adds/moves the middle point) and isCurvedPoints below.
export const CURVE_TENSION = 0.5;
/** True when a point-pair node carries a middle control point (curved). */
export const isCurvedPoints = (points: number[]): boolean => points.length > 4;

interface NodeProps<T extends EditorNode> {
  node: T;
  attachRef: AttachRef;
  selectable: boolean;
}

function commonHandlers(id: NodeId) {
  const updateNode = useEditorStore.getState().updateNode;
  return {
    onDragEnd(e: Konva.KonvaEventObject<DragEvent>) {
      updateNode(id, { x: e.target.x(), y: e.target.y() });
    },
  };
}

function ImageView({ node, attachRef, selectable }: NodeProps<ImageNodeBase | PastedImageNode>) {
  const img = useHTMLImage(node.src);
  const ref = useRef<Konva.Image>(null);
  useEffect(() => {
    attachRef(node.id, ref.current);
    return () => attachRef(node.id, null);
  });
  const isBase = node.type === 'image';
  // Honor a recorded crop on the base image: Konva slices the source region
  // (image space) into the node's width/height box. The crop tool sets
  // width/height to the crop size, so computeView re-fits the cropped canvas.
  const crop =
    node.type === 'image' && node.crop
      ? { x: node.crop.x, y: node.crop.y, width: node.crop.w, height: node.crop.h }
      : undefined;
  return (
    <KImage
      id={node.id}
      ref={ref}
      image={img ?? undefined}
      x={node.x}
      y={node.y}
      width={node.width}
      height={node.height}
      crop={crop}
      scaleX={node.scaleX}
      scaleY={node.scaleY}
      rotation={node.rotation}
      opacity={node.opacity}
      draggable={node.draggable && selectable}
      listening={selectable}
      onDragEnd={(e) => useEditorStore.getState().updateNode(node.id, { x: e.target.x(), y: e.target.y() })}
      onTransformEnd={isBase ? undefined : (e) => {
        const n = e.target;
        useEditorStore.getState().updateNode(node.id, {
          x: n.x(),
          y: n.y(),
          width: Math.max(5, n.width() * n.scaleX()),
          height: Math.max(5, n.height() * n.scaleY()),
          rotation: n.rotation(),
        });
        n.scaleX(1);
        n.scaleY(1);
      }}
    />
  );
}

function PenView({ node, attachRef, selectable }: NodeProps<PenNode>) {
  const ref = useRef<Konva.Line>(null);
  useEffect(() => {
    attachRef(node.id, ref.current);
    return () => attachRef(node.id, null);
  });
  return (
    <Line
      id={node.id}
      ref={ref}
      points={node.points}
      x={node.x}
      y={node.y}
      stroke={node.stroke}
      strokeWidth={node.strokeWidth}
      tension={node.tension}
      lineCap={node.lineCap}
      lineJoin={node.lineJoin}
      rotation={node.rotation}
      opacity={node.opacity}
      draggable={node.draggable && selectable}
      hitStrokeWidth={Math.max(node.strokeWidth, 12)}
      {...commonHandlers(node.id)}
      onTransformEnd={(e) => {
        const n = e.target;
        const sx = n.scaleX();
        const sy = n.scaleY();
        useEditorStore.getState().updateNode(node.id, {
          x: n.x(),
          y: n.y(),
          rotation: n.rotation(),
          points: node.points.map((p, i) => (i % 2 === 0 ? p * sx : p * sy)),
        });
        n.scaleX(1);
        n.scaleY(1);
      }}
    />
  );
}

function RectView({ node, attachRef, selectable }: NodeProps<RectNode>) {
  const ref = useRef<Konva.Rect>(null);
  useEffect(() => {
    attachRef(node.id, ref.current);
    return () => attachRef(node.id, null);
  });
  return (
    <Rect
      id={node.id}
      ref={ref}
      x={node.x}
      y={node.y}
      width={node.width}
      height={node.height}
      stroke={node.stroke}
      strokeWidth={node.strokeWidth}
      fill={node.fill}
      rotation={node.rotation}
      opacity={node.opacity}
      draggable={node.draggable && selectable}
      {...commonHandlers(node.id)}
      onTransformEnd={(e) => {
        const n = e.target;
        useEditorStore.getState().updateNode(node.id, {
          x: n.x(),
          y: n.y(),
          width: Math.max(5, n.width() * n.scaleX()),
          height: Math.max(5, n.height() * n.scaleY()),
          rotation: n.rotation(),
        });
        n.scaleX(1);
        n.scaleY(1);
      }}
    />
  );
}

function EllipseView({ node, attachRef, selectable }: NodeProps<EllipseNode>) {
  const ref = useRef<Konva.Ellipse>(null);
  useEffect(() => {
    attachRef(node.id, ref.current);
    return () => attachRef(node.id, null);
  });
  return (
    <Ellipse
      id={node.id}
      ref={ref}
      x={node.x}
      y={node.y}
      radiusX={node.radiusX}
      radiusY={node.radiusY}
      stroke={node.stroke}
      strokeWidth={node.strokeWidth}
      fill={node.fill}
      rotation={node.rotation}
      opacity={node.opacity}
      draggable={node.draggable && selectable}
      {...commonHandlers(node.id)}
      onTransformEnd={(e) => {
        const n = e.target;
        useEditorStore.getState().updateNode(node.id, {
          x: n.x(),
          y: n.y(),
          radiusX: Math.max(3, node.radiusX * n.scaleX()),
          radiusY: Math.max(3, node.radiusY * n.scaleY()),
          rotation: n.rotation(),
        });
        n.scaleX(1);
        n.scaleY(1);
      }}
    />
  );
}

function LineView({ node, attachRef, selectable }: NodeProps<LineNode>) {
  const ref = useRef<Konva.Line>(null);
  useEffect(() => {
    attachRef(node.id, ref.current);
    return () => attachRef(node.id, null);
  });
  return (
    <Line
      id={node.id}
      ref={ref}
      points={node.points}
      x={node.x}
      y={node.y}
      stroke={node.stroke}
      strokeWidth={node.strokeWidth}
      lineCap="round"
      tension={isCurvedPoints(node.points) ? CURVE_TENSION : 0}
      rotation={node.rotation}
      opacity={node.opacity}
      draggable={node.draggable && selectable}
      hitStrokeWidth={Math.max(node.strokeWidth, 12)}
      {...commonHandlers(node.id)}
    />
  );
}

function ArrowView({ node, attachRef, selectable }: NodeProps<ArrowNode>) {
  const ref = useRef<Konva.Arrow>(null);
  useEffect(() => {
    attachRef(node.id, ref.current);
    return () => attachRef(node.id, null);
  });
  return (
    <Arrow
      id={node.id}
      ref={ref}
      points={node.points}
      x={node.x}
      y={node.y}
      stroke={node.stroke}
      strokeWidth={node.strokeWidth}
      fill={node.fill}
      pointerLength={node.pointerLength}
      pointerWidth={node.pointerWidth}
      tension={isCurvedPoints(node.points) ? CURVE_TENSION : 0}
      rotation={node.rotation}
      opacity={node.opacity}
      draggable={node.draggable && selectable}
      hitStrokeWidth={Math.max(node.strokeWidth, 12)}
      {...commonHandlers(node.id)}
    />
  );
}

function TextView({ node, attachRef, selectable }: NodeProps<TextNode>) {
  const ref = useRef<Konva.Text>(null);
  useEffect(() => {
    attachRef(node.id, ref.current);
    return () => attachRef(node.id, null);
  });
  // Scale is the warp: dragging transformer handles stretches the glyph instead of
  // reflowing wrap-width (old path baked scaleX into width + scaleY into fontSize).
  const sx = node.scaleX ?? 1;
  const sy = node.scaleY ?? 1;
  return (
    <Text
      id={node.id}
      ref={ref}
      x={node.x}
      y={node.y}
      text={node.text}
      fontSize={node.fontSize}
      fontFamily={node.fontFamily}
      fill={node.fill}
      width={node.width}
      align={node.align}
      scaleX={sx}
      scaleY={sy}
      rotation={node.rotation}
      opacity={node.opacity}
      draggable={node.draggable && selectable}
      {...commonHandlers(node.id)}
      onTransformEnd={(e) => {
        const n = e.target as Konva.Text;
        // Persist the live transformer scale as warping. Do NOT bake into width /
        // fontSize — that reflows the text to the new box instead of stretching it.
        useEditorStore.getState().updateNode(node.id, {
          x: n.x(),
          y: n.y(),
          rotation: n.rotation(),
          scaleX: n.scaleX() === 0 ? 0.01 : n.scaleX(),
          scaleY: n.scaleY() === 0 ? 0.01 : n.scaleY(),
        });
      }}
    />
  );
}

function EmojiView({ node, attachRef, selectable }: NodeProps<EmojiNode>) {
  const ref = useRef<Konva.Text>(null);
  useEffect(() => {
    attachRef(node.id, ref.current);
    return () => attachRef(node.id, null);
  });
  return (
    <Text
      id={node.id}
      ref={ref}
      x={node.x}
      y={node.y}
      text={node.emoji}
      fontSize={node.fontSize}
      rotation={node.rotation}
      opacity={node.opacity}
      draggable={node.draggable && selectable}
      {...commonHandlers(node.id)}
      onTransformEnd={(e) => {
        const n = e.target as Konva.Text;
        useEditorStore.getState().updateNode(node.id, {
          x: n.x(),
          y: n.y(),
          fontSize: Math.max(8, node.fontSize * Math.max(n.scaleX(), n.scaleY())),
          rotation: n.rotation(),
        });
        n.scaleX(1);
        n.scaleY(1);
      }}
    />
  );
}

/** Dispatch a node to its view. `selectable` is false in non-select tools. */
export function renderNode(node: EditorNode, attachRef: AttachRef, selectable: boolean) {
  switch (node.type) {
    case 'image':
    case 'pasted-image':
      return <ImageView key={node.id} node={node} attachRef={attachRef} selectable={selectable} />;
    case 'pen':
      return <PenView key={node.id} node={node} attachRef={attachRef} selectable={selectable} />;
    case 'rect':
      return <RectView key={node.id} node={node} attachRef={attachRef} selectable={selectable} />;
    case 'ellipse':
      return <EllipseView key={node.id} node={node} attachRef={attachRef} selectable={selectable} />;
    case 'line':
      return <LineView key={node.id} node={node} attachRef={attachRef} selectable={selectable} />;
    case 'arrow':
      return <ArrowView key={node.id} node={node} attachRef={attachRef} selectable={selectable} />;
    case 'text':
      return <TextView key={node.id} node={node} attachRef={attachRef} selectable={selectable} />;
    case 'emoji':
      return <EmojiView key={node.id} node={node} attachRef={attachRef} selectable={selectable} />;
    default:
      return null;
  }
}
