// Coordinate + drawing helpers. All annotation coords are stored in IMAGE space
// (the annotations Group is offset by {dx,dy} and scaled by `scale`), so the
// pointer must be converted from Stage space to image space before use.

import type Konva from 'konva';

export interface ViewTransform {
  dx: number;
  dy: number;
  scale: number;
}

/** Convert the Stage pointer position into image space. Returns null off-canvas. */
export function toImageSpace(stage: Konva.Stage, view: ViewTransform): { x: number; y: number } | null {
  const p = stage.getPointerPosition();
  if (!p) return null;
  return { x: (p.x - view.dx) / view.scale, y: (p.y - view.dy) / view.scale };
}

/** Snap the vector (x1,y1)->(x2,y2) to the nearest 45° while preserving length. */
export function constrainAngle45(x1: number, y1: number, x2: number, y2: number): { x: number; y: number } {
  const dx = x2 - x1;
  const dy = y2 - y1;
  const len = Math.hypot(dx, dy);
  if (len === 0) return { x: x2, y: y2 };
  const angle = Math.atan2(dy, dx);
  const snapped = Math.round(angle / (Math.PI / 4)) * (Math.PI / 4);
  return { x: x1 + Math.cos(snapped) * len, y: y1 + Math.sin(snapped) * len };
}

/** Build a square/origin rect from a drag, honoring a shift-to-square constraint. */
export function squareFromDrag(
  startX: number,
  startY: number,
  curX: number,
  curY: number,
  square: boolean,
): { x: number; y: number; width: number; height: number } {
  let w = curX - startX;
  let h = curY - startY;
  if (square) {
    const s = Math.max(Math.abs(w), Math.abs(h));
    w = Math.sign(w || 1) * s;
    h = Math.sign(h || 1) * s;
  }
  return {
    x: Math.min(startX, startX + w),
    y: Math.min(startY, startY + h),
    width: Math.abs(w),
    height: Math.abs(h),
  };
}

/** Distance between two points (used for zero-size draft discard + pen filtering). */
export function dist(x1: number, y1: number, x2: number, y2: number): number {
  return Math.hypot(x2 - x1, y2 - y1);
}

/** Cheap unique id generator for annotation nodes. */
export function newId(prefix = 'n'): string {
  return `${prefix}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}
