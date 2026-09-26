// Pure geometry for the capture overlay: shared crop in VIRTUAL-DESKTOP PHYSICAL
// px, aspect-ratio lock, and the click-to-bring / resize math. Kept out of
// Overlay.tsx so the React surface stays about chrome + events.

import type { CSSProperties } from "react";
import type { Rect, ScreenInfo } from "@/lib/contract";
import type { WindowInfo } from "../../bindings/github.com/StephenSHorton/toru/internal/capture/models";

export const MIN_PHYS = 24; // minimum crop size, PHYSICAL px (drag/resize floor)

/** Shared empty crop — region mode starts here (drag to create). */
export const EMPTY_VCROP: Rect = { x: 0, y: 0, w: 0, h: 0 };

/** True once a region drag has committed a usable rect. */
export function hasVcrop(vr: Rect | null | undefined): boolean {
  return !!vr && vr.w >= MIN_PHYS && vr.h >= MIN_PHYS;
}

export type Handle = "nw" | "n" | "ne" | "w" | "e" | "sw" | "s" | "se";
export const HANDLES: Handle[] = ["nw", "n", "ne", "w", "e", "sw", "s", "se"];

export type AspectId = "free" | "16:9" | "9:16" | "4:3" | "3:2" | "1:1" | "21:9";

export const ASPECTS: { id: AspectId; label: string; w: number; h: number }[] = [
  { id: "free", label: "Free", w: 0, h: 0 },
  { id: "16:9", label: "16:9", w: 16, h: 9 },
  { id: "9:16", label: "9:16", w: 9, h: 16 },
  { id: "4:3", label: "4:3", w: 4, h: 3 },
  { id: "3:2", label: "3:2", w: 3, h: 2 },
  { id: "1:1", label: "1:1", w: 1, h: 1 },
  { id: "21:9", label: "21:9", w: 21, h: 9 },
];

export function aspectRatio(id: AspectId): { w: number; h: number } | null {
  const a = ASPECTS.find((x) => x.id === id);
  if (!a || a.w <= 0 || a.h <= 0) return null;
  return { w: a.w, h: a.h };
}

export interface CssRect {
  left: number;
  top: number;
  width: number;
  height: number;
}

export interface Bounds {
  minX: number;
  minY: number;
  maxX: number;
  maxY: number;
}

export function clamp(v: number, lo: number, hi: number): number {
  if (hi < lo) return lo;
  return Math.min(Math.max(v, lo), hi);
}

export function rectsEqual(a: Rect, b: Rect): boolean {
  return a.x === b.x && a.y === b.y && a.w === b.w && a.h === b.h;
}

export function screenRect(s: ScreenInfo): Rect {
  return { x: s.x, y: s.y, w: s.w, h: s.h };
}

export function vToLocal(vr: Rect, self: ScreenInfo): CssRect {
  const s = self.scaleFactor > 0 ? self.scaleFactor : 1;
  return {
    left: (vr.x - self.x) / s,
    top: (vr.y - self.y) / s,
    width: vr.w / s,
    height: vr.h / s,
  };
}

export function overlapArea(vr: Rect, s: ScreenInfo): number {
  const x0 = Math.max(vr.x, s.x);
  const y0 = Math.max(vr.y, s.y);
  const x1 = Math.min(vr.x + vr.w, s.x + s.w);
  const y1 = Math.min(vr.y + vr.h, s.y + s.h);
  return x1 > x0 && y1 > y0 ? (x1 - x0) * (y1 - y0) : 0;
}

export function dominantScreen(vr: Rect, screens: ScreenInfo[]): ScreenInfo | null {
  let best: ScreenInfo | null = null;
  let bestA = -1;
  for (const s of screens) {
    const a = overlapArea(vr, s);
    if (a > bestA) {
      best = s;
      bestA = a;
    } else if (a === bestA && best) {
      if ((s.isPrimary && !best.isPrimary) || (s.isPrimary === best.isPrimary && s.id < best.id)) {
        best = s;
      }
    }
  }
  return best;
}

export function unionBounds(screens: ScreenInfo[]): Bounds {
  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (const s of screens) {
    minX = Math.min(minX, s.x);
    minY = Math.min(minY, s.y);
    maxX = Math.max(maxX, s.x + s.w);
    maxY = Math.max(maxY, s.y + s.h);
  }
  return { minX, minY, maxX, maxY };
}

export function centeredV(s: ScreenInfo): Rect {
  const w = Math.round(s.w / 2);
  const h = Math.round(s.h / 2);
  return { x: s.x + Math.round((s.w - w) / 2), y: s.y + Math.round((s.h - h) / 2), w, h };
}

export function fitToScreen(vr: Rect, s: ScreenInfo, aspect?: AspectId): Rect {
  const bounds: Bounds = { minX: s.x, minY: s.y, maxX: s.x + s.w, maxY: s.y + s.h };
  const ratio = aspect ? aspectRatio(aspect) : null;
  if (ratio) return snapToAspect(vr, ratio.w, ratio.h, bounds);
  const w = Math.max(MIN_PHYS, Math.min(vr.w, s.w));
  const h = Math.max(MIN_PHYS, Math.min(vr.h, s.h));
  const x = clamp(vr.x, s.x, s.x + s.w - w);
  const y = clamp(vr.y, s.y, s.y + s.h - h);
  return { x, y, w, h };
}

/**
 * Build a crop from an origin (pointer-down) to the current point (pointer-move)
 * while creating a region. Origin corner stays put; the box is clamped to bounds
 * and optionally locked to an aspect ratio.
 */
export function rectFromDrag(
  originX: number,
  originY: number,
  curX: number,
  curY: number,
  bounds: Bounds,
  aspect: AspectId = "free",
): Rect {
  const ox = clamp(originX, bounds.minX, bounds.maxX);
  const oy = clamp(originY, bounds.minY, bounds.maxY);
  const cx = clamp(curX, bounds.minX, bounds.maxX);
  const cy = clamp(curY, bounds.minY, bounds.maxY);

  let left = Math.min(ox, cx);
  let top = Math.min(oy, cy);
  let w = Math.abs(cx - ox);
  let h = Math.abs(cy - oy);

  const ratio = aspectRatio(aspect);
  if (ratio && (w > 0 || h > 0)) {
    const wh = ratio.w / ratio.h;
    if (h === 0 || w / Math.max(h, 1) >= wh) {
      h = w / wh;
    } else {
      w = h * wh;
    }
    w = Math.round(w);
    h = Math.round(h);
    if (cx < ox) left = ox - w;
    if (cy < oy) top = oy - h;
    if (left < bounds.minX) {
      w -= bounds.minX - left;
      left = bounds.minX;
    }
    if (top < bounds.minY) {
      h -= bounds.minY - top;
      top = bounds.minY;
    }
    if (left + w > bounds.maxX) w = bounds.maxX - left;
    if (top + h > bounds.maxY) h = bounds.maxY - top;
    if (w > 0 && h > 0) {
      if (w / h > wh) w = Math.round(h * wh);
      else h = Math.round(w / wh);
      if (cx < ox) left = ox - w;
      if (cy < oy) top = oy - h;
      left = clamp(left, bounds.minX, bounds.maxX - w);
      top = clamp(top, bounds.minY, bounds.maxY - h);
    }
  }

  return {
    x: Math.round(left),
    y: Math.round(top),
    w: Math.max(0, Math.round(w)),
    h: Math.max(0, Math.round(h)),
  };
}

export function seedVcrop(region: Rect, screens: ScreenInfo[]): Rect {
  if (!screens.length) return region;
  const u = unionBounds(screens);
  const w = Math.max(MIN_PHYS, Math.min(region.w, u.maxX - u.minX));
  const h = Math.max(MIN_PHYS, Math.min(region.h, u.maxY - u.minY));
  const x = clamp(region.x, u.minX, u.maxX - w);
  const y = clamp(region.y, u.minY, u.maxY - h);
  return { x, y, w, h };
}

/** Snap vr to aw:ah, keeping centre, clamped to bounds. */
export function snapToAspect(vr: Rect, aw: number, ah: number, bounds: Bounds): Rect {
  const ratio = aw / ah;
  const maxW = Math.max(MIN_PHYS, bounds.maxX - bounds.minX);
  const maxH = Math.max(MIN_PHYS, bounds.maxY - bounds.minY);
  const cx = vr.x + vr.w / 2;
  const cy = vr.y + vr.h / 2;
  let w = Math.max(MIN_PHYS, vr.w);
  let h = w / ratio;
  if (h > vr.h && vr.h >= MIN_PHYS) {
    h = vr.h;
    w = h * ratio;
  }
  if (w > maxW) {
    w = maxW;
    h = w / ratio;
  }
  if (h > maxH) {
    h = maxH;
    w = h * ratio;
  }
  w = Math.max(MIN_PHYS, Math.round(w));
  h = Math.max(MIN_PHYS, Math.round(h));
  if (w > maxW) w = maxW;
  if (h > maxH) h = maxH;
  const x = clamp(Math.round(cx - w / 2), bounds.minX, bounds.maxX - w);
  const y = clamp(Math.round(cy - h / 2), bounds.minY, bounds.maxY - h);
  return { x, y, w, h };
}

export function computeDrag(
  startV: Rect,
  handle: Handle | "body",
  dxV: number,
  dyV: number,
  tool: "screenshot" | "video" | "share",
  screens: ScreenInfo[],
  self: ScreenInfo,
  aspect: AspectId = "free",
): Rect {
  const list = screens.length ? screens : [self];
  let bounds: Bounds;
  // Share uses the same grab as recording, so it is stuck to one monitor too.
  if (tool === "video" || tool === "share") {
    const proposed =
      handle === "body" ? { x: startV.x + dxV, y: startV.y + dyV, w: startV.w, h: startV.h } : startV;
    const d = dominantScreen(proposed, list) ?? self;
    bounds = { minX: d.x, minY: d.y, maxX: d.x + d.w, maxY: d.y + d.h };
  } else {
    bounds = unionBounds(list);
  }
  const ratio = aspectRatio(aspect);
  return applyDrag(startV, handle, dxV, dyV, bounds, ratio);
}

export function applyDrag(
  startV: Rect,
  handle: Handle | "body",
  dxV: number,
  dyV: number,
  bounds: Bounds,
  ratio: { w: number; h: number } | null,
): Rect {
  if (handle === "body") {
    const x = clamp(startV.x + dxV, bounds.minX, bounds.maxX - startV.w);
    const y = clamp(startV.y + dyV, bounds.minY, bounds.maxY - startV.h);
    return { x, y, w: startV.w, h: startV.h };
  }
  if (!ratio) {
    let left = startV.x;
    let top = startV.y;
    let right = startV.x + startV.w;
    let bottom = startV.y + startV.h;
    if (handle.includes("w")) left = clamp(startV.x + dxV, bounds.minX, right - MIN_PHYS);
    if (handle.includes("e")) right = clamp(right + dxV, left + MIN_PHYS, bounds.maxX);
    if (handle.includes("n")) top = clamp(startV.y + dyV, bounds.minY, bottom - MIN_PHYS);
    if (handle.includes("s")) bottom = clamp(bottom + dyV, top + MIN_PHYS, bounds.maxY);
    return { x: left, y: top, w: right - left, h: bottom - top };
  }
  return applyDragAspect(startV, handle, dxV, dyV, bounds, ratio.w / ratio.h);
}

function applyDragAspect(
  startV: Rect,
  handle: Handle,
  dxV: number,
  dyV: number,
  bounds: Bounds,
  wh: number, // width / height
): Rect {
  const moveW = handle.includes("w") || handle.includes("e");
  const moveH = handle.includes("n") || handle.includes("s");
  const maxW = bounds.maxX - bounds.minX;
  const maxH = bounds.maxY - bounds.minY;

  const fromWidth = (w: number) => {
    let nw = Math.max(MIN_PHYS, w);
    let nh = nw / wh;
    if (nh < MIN_PHYS) {
      nh = MIN_PHYS;
      nw = nh * wh;
    }
    if (nw > maxW) {
      nw = maxW;
      nh = nw / wh;
    }
    if (nh > maxH) {
      nh = maxH;
      nw = nh * wh;
    }
    return { w: Math.max(MIN_PHYS, Math.round(nw)), h: Math.max(MIN_PHYS, Math.round(nh)) };
  };
  const fromHeight = (h: number) => fromWidth(h * wh);

  if (moveW && moveH) {
    const fixedX = handle.includes("w") ? startV.x + startV.w : startV.x;
    const fixedY = handle.includes("n") ? startV.y + startV.h : startV.y;
    const rawW = handle.includes("e") ? startV.w + dxV : startV.w - dxV;
    const rawH = handle.includes("s") ? startV.h + dyV : startV.h - dyV;
    const fromW = fromWidth(rawW);
    const fromH = fromHeight(rawH);
    const useW = Math.abs(fromW.h - rawH) <= Math.abs(fromH.w - rawW);
    const { w, h } = useW ? fromW : fromH;
    let x = handle.includes("w") ? fixedX - w : fixedX;
    let y = handle.includes("n") ? fixedY - h : fixedY;
    x = clamp(x, bounds.minX, bounds.maxX - w);
    y = clamp(y, bounds.minY, bounds.maxY - h);
    return { x, y, w, h };
  }

  if (moveW) {
    const rawW = handle.includes("e") ? startV.w + dxV : startV.w - dxV;
    const { w, h } = fromWidth(rawW);
    const cy = startV.y + startV.h / 2;
    let x = handle.includes("w") ? startV.x + startV.w - w : startV.x;
    let y = Math.round(cy - h / 2);
    x = clamp(x, bounds.minX, bounds.maxX - w);
    y = clamp(y, bounds.minY, bounds.maxY - h);
    return { x, y, w, h };
  }

  const rawH = handle.includes("s") ? startV.h + dyV : startV.h - dyV;
  const { w, h } = fromHeight(rawH);
  const cx = startV.x + startV.w / 2;
  let x = Math.round(cx - w / 2);
  let y = handle.includes("n") ? startV.y + startV.h - h : startV.y;
  x = clamp(x, bounds.minX, bounds.maxX - w);
  y = clamp(y, bounds.minY, bounds.maxY - h);
  return { x, y, w, h };
}

export function windowAtPoint(windows: WindowInfo[], px: number, py: number): WindowInfo | null {
  for (const w of windows) {
    const r = w.rect;
    if (!r || r.w < 8 || r.h < 8) continue;
    if (px >= r.x && py >= r.y && px < r.x + r.w && py < r.y + r.h) {
      return w;
    }
  }
  return null;
}

export function handleStyle(h: Handle): CSSProperties {
  const s: CSSProperties = {
    width: 14,
    height: 14,
    transform: "translate(-50%, -50%)",
    cursor: handleCursor(h),
  };
  if (h.includes("n")) s.top = 0;
  if (h.includes("s")) s.top = "100%";
  if (h === "e" || h === "w") s.top = "50%";
  if (h.includes("w")) s.left = 0;
  if (h.includes("e")) s.left = "100%";
  if (h === "n" || h === "s") s.left = "50%";
  return s;
}

export function handleCursor(h: Handle): string {
  switch (h) {
    case "n":
    case "s":
      return "ns-resize";
    case "e":
    case "w":
      return "ew-resize";
    case "nw":
    case "se":
      return "nwse-resize";
    case "ne":
    case "sw":
      return "nesw-resize";
  }
}
