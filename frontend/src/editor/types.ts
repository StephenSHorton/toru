// Scene-graph node model for the Toru screenshot editor.
//
// VERBATIM CONTRACT (per BUILD SPEC §2.1): downstream tool-builders depend on
// these field names verbatim. Do NOT rename fields/types. The full discriminated
// union covers every tool — even ones not implemented yet — so tools can be
// added without touching this file.
//
// All annotation coords are stored in IMAGE space (the annotations layer is
// rendered inside a Group offset+scaled to the fit-scaled base image).

export type NodeId = string;

export type ToolId =
  | 'select' | 'pen' | 'rect' | 'ellipse' | 'line' | 'arrow'
  | 'text' | 'emoji' | 'image' | 'crop';

interface BaseNode {
  id: NodeId;
  type: string;
  x: number;
  y: number;
  rotation: number;     // degrees
  opacity: number;      // 0..1, default 1
  draggable: boolean;   // true for annotations, false for base image
}

// Base captured image — z-index 0, non-deletable, non-reorderable, draggable:false.
export interface ImageNodeBase extends BaseNode {
  type: 'image';
  src: string; width: number; height: number;
  scaleX: number; scaleY: number;
  crop?: { x: number; y: number; w: number; h: number };
}
// Pasted-from-clipboard image — selectable/deletable/layerable.
export interface PastedImageNode extends BaseNode {
  type: 'pasted-image';
  src: string; width: number; height: number;
  scaleX: number; scaleY: number;
}
export interface PenNode extends BaseNode {
  type: 'pen';
  points: number[];            // image-space, flat [x0,y0,x1,y1,...]
  stroke: string; strokeWidth: number;
  tension: number;             // 0.5
  lineCap: 'round'; lineJoin: 'round';
}
export interface RectNode extends BaseNode {
  type: 'rect';
  width: number; height: number;
  stroke: string; strokeWidth: number; fill?: string;
}
export interface EllipseNode extends BaseNode {
  type: 'ellipse';
  radiusX: number; radiusY: number;
  stroke: string; strokeWidth: number; fill?: string;
}
export interface LineNode extends BaseNode {
  type: 'line';
  points: number[];            // [x1,y1, x2,y2] straight | [x1,y1, cx,cy, x2,y2] curved
  stroke: string; strokeWidth: number;
}
export interface ArrowNode extends BaseNode {
  type: 'arrow';
  points: number[];            // [x1,y1, x2,y2] straight | [x1,y1, cx,cy, x2,y2] curved
  stroke: string; strokeWidth: number;
  pointerLength: number; pointerWidth: number; fill: string;
}
export interface TextNode extends BaseNode {
  type: 'text';
  text: string; fontSize: number; fontFamily: string; fill: string;
  width?: number; align: 'left' | 'center' | 'right';
}
export interface EmojiNode extends BaseNode {
  type: 'emoji';
  emoji: string; fontSize: number;   // rendered as a Konva Text glyph
}

export type EditorNode =
  | ImageNodeBase | PastedImageNode | PenNode | RectNode | EllipseNode
  | LineNode | ArrowNode | TextNode | EmojiNode;

export type AnnotationNode = Exclude<EditorNode, ImageNodeBase>;

export const POINT_PAIR_TYPES = ['line', 'arrow'] as const;
export const isPointPair = (n: EditorNode): n is LineNode | ArrowNode =>
  n.type === 'line' || n.type === 'arrow';
