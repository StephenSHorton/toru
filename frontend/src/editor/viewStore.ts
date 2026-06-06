// Shared, LIVE view transform for the editor canvas: the fit-scale baseline plus
// the user's Ctrl+wheel zoom/pan. It is the single source of truth read by the
// Konva canvas AND both DOM overlays (crop, text) so they stay pixel-aligned
// while zoomed.
//
// Image space -> Stage(screen) space:  screen = (dx,dy) + scale * imagePoint
// (identical formula whether zoomed or not — only dx/dy/scale change).
//
// EXPORT deliberately ignores the live zoom: flattenStage renders at the fit
// (computeFit) and neutralises the live group transforms, so the saved PNG is
// always native-resolution regardless of on-screen zoom (see exportActions).

import { useSyncExternalStore } from 'react';
import type { ViewTransform } from './geometry';

// STAGE_W/STAGE_H are the DEFAULT stage size (the standalone Editor dev route
// uses these). In overlay-v2 the embedded editor's stage is sized to the captured
// CROP REGION via setStageSize(), so the live size is module state (stageW/stageH)
// seeded from these defaults.
export const STAGE_W = 900;
export const STAGE_H = 540;

/** Max magnification relative to the fit scale (Ctrl+wheel can't exceed this). */
const MAX_ZOOM = 8;

export interface View extends ViewTransform {
  dw: number;
  dh: number;
}

// --- live stage size (module singleton) -------------------------------------
// The Konva Stage element + both DOM overlays read this so the embedded editor
// fills exactly the crop region. setStageSize re-fits + emits, sharing the SAME
// listener set as the view, so size and transform update atomically.
let stageW = STAGE_W;
let stageH = STAGE_H;
// Cached snapshot object — useSyncExternalStore's getSnapshot must return a
// STABLE reference per value (a fresh {w,h} literal each call makes React 19
// loop-warn). Replace it ONLY in setStageSize.
let sizeSnap = { w: STAGE_W, h: STAGE_H };

export function getStageSize(): { w: number; h: number } {
  return sizeSnap;
}

/**
 * Resize the stage (the embedded editor sets this to the crop region's CSS size).
 * Re-fits the current base image at the new size and notifies subscribers. MUST
 * be called BEFORE loadBaseImage in the overlay:edit handler so the first fit
 * uses the correct size (EditorCanvas's resetFit effect reads stageW/stageH).
 */
export function setStageSize(w: number, h: number): void {
  stageW = w;
  stageH = h;
  sizeSnap = { w, h };
  current = computeFit(baseSize.w, baseSize.h);
  emit();
}

/** Subscribe the Stage element + overlays to the live stage size. */
export function useStageSize(): { w: number; h: number } {
  return useSyncExternalStore(subscribe, getStageSize, getStageSize);
}

/** Fit a width×height image centered in the Stage at zoom 1 (no user zoom). */
export function computeFit(imgW: number, imgH: number): View {
  if (!imgW || !imgH) return { dx: 0, dy: 0, scale: 1, dw: stageW, dh: stageH };
  const scale = Math.min(stageW / imgW, stageH / imgH);
  const dw = imgW * scale;
  const dh = imgH * scale;
  return { dx: (stageW - dw) / 2, dy: (stageH - dh) / 2, scale, dw, dh };
}

// --- live state (module singleton) ------------------------------------------
let current: View = computeFit(0, 0);
let baseSize = { w: 0, h: 0 };
const listeners = new Set<() => void>();

function emit(): void {
  for (const l of listeners) l();
}

export function getView(): View {
  return current;
}

function subscribe(cb: () => void): () => void {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

/** Subscribe a component to live view changes (zoom/pan/fit). */
export function useView(): View {
  return useSyncExternalStore(subscribe, getView, getView);
}

/**
 * Reset to the centered fit for a (new or freshly-cropped) base image. Called by
 * EditorCanvas whenever the base image dimensions change, so a new screenshot
 * always opens at 100% fit and a crop re-fits the smaller canvas.
 */
export function resetFit(imgW: number, imgH: number): void {
  baseSize = { w: imgW, h: imgH };
  current = computeFit(imgW, imgH);
  emit();
}

function clamp(v: number, lo: number, hi: number): number {
  return Math.max(lo, Math.min(hi, v));
}

/**
 * Clamp one axis of the view offset so the scaled content can't be dragged off
 * into empty space: when the content is larger than the Stage it stays covering
 * it (offset in [stage-content, 0]); when smaller it's centered.
 */
function clampAxis(offset: number, content: number, stageSize: number): number {
  if (content <= stageSize) return (stageSize - content) / 2;
  return clamp(offset, stageSize - content, 0);
}

/**
 * Zoom toward the Stage-space point (px,py) by one wheel notch (deltaY<0 == in).
 * The image point under the cursor stays fixed (zoom-to-cursor). The fit scale is
 * the floor (you can't zoom out past the fit); reaching it snaps back to the
 * clean centered fit. The ceiling is MAX_ZOOM× the fit.
 */
export function zoomAtPointer(px: number, py: number, deltaY: number): void {
  const fit = computeFit(baseSize.w, baseSize.h);
  const minScale = fit.scale;
  const maxScale = fit.scale * MAX_ZOOM;
  const factor = deltaY < 0 ? 1.1 : 1 / 1.1;
  const newScale = clamp(current.scale * factor, minScale, maxScale);
  if (newScale === current.scale) return;
  // At the floor, snap to the clean centered fit rather than leaving it panned.
  if (newScale <= minScale) {
    current = fit;
    emit();
    return;
  }
  // Keep the image point under the cursor stationary, then clamp so a near-edge
  // zoom doesn't open an empty gap.
  const imgX = (px - current.dx) / current.scale;
  const imgY = (py - current.dy) / current.scale;
  const dw = baseSize.w * newScale;
  const dh = baseSize.h * newScale;
  current = {
    scale: newScale,
    dx: clampAxis(px - imgX * newScale, dw, stageW),
    dy: clampAxis(py - imgY * newScale, dh, stageH),
    dw,
    dh,
  };
  emit();
}

/**
 * Pan the view by a Stage-space delta (used by the select tool's drag-on-empty-
 * canvas gesture). Clamped so the image can't be dragged off into empty space.
 */
export function panBy(ddx: number, ddy: number): void {
  const dw = baseSize.w * current.scale;
  const dh = baseSize.h * current.scale;
  current = {
    ...current,
    dx: clampAxis(current.dx + ddx, dw, stageW),
    dy: clampAxis(current.dy + ddy, dh, stageH),
  };
  emit();
}

/** True when zoomed in past the fit, i.e. there is room to pan. */
export function canPan(): boolean {
  const fit = computeFit(baseSize.w, baseSize.h);
  return current.scale > fit.scale + 1e-3;
}

/** The current zoom as a multiple of the fit (1 == fit). For a HUD/indicator. */
export function zoomFactor(): number {
  const fit = computeFit(baseSize.w, baseSize.h);
  return fit.scale > 0 ? current.scale / fit.scale : 1;
}
