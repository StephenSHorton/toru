// Export pipeline: flatten the Konva Stage to a PNG data URL, hand it to the Go
// services for a temp file, then copy-to-clipboard or save-as.
//
//   flatten(stage) -> ScreenshotService.SavePNG(dataURL) -> path
//     -> ExportService.CopyToClipboard(path, "image")   (copy)
//     -> ExportService.SaveAs(path, name)               (save-as; "" === cancel)
//
// Transformers are detached/hidden BEFORE toDataURL or their handles bake into
// the PNG. We locate them on the Stage itself (no extra ref threading).
//
// CLIPPING: the base image is fit-scaled and CENTERED inside the Stage at offset
// {dx,dy} (computeView), so when the image aspect ratio differs from the Stage
// there are letterbox bars. We must clip toDataURL to the on-screen image
// rectangle [dx, dy, dw, dh] — NOT the whole Stage — or the export bakes those
// empty margins in and comes out the wrong size. With pixelRatio = baseW / dw
// (== 1/scale) the clipped region renders back to exact SOURCE-pixel dimensions.
// The view is recomputed from the (possibly cropped) base node so a post-crop
// export is also exact and un-letterboxed.

import type Konva from 'konva';
import { ExportService, ScreenshotService } from '@/lib/api';
import { useEditorStore, BASE_IMAGE_ID } from './store';
import { computeView } from './EditorCanvas';
import type { ImageNodeBase } from './types';

/**
 * Hide every selection overlay (the Transformer AND the line/arrow
 * PointPairHandles, named 'pp-handle') on the Stage, run fn, then restore them —
 * so they never bake into the exported PNG.
 */
function withoutTransformers<T>(stage: Konva.Stage, fn: () => T): T {
  const overlays = (stage.find('Transformer') as Konva.Node[]).concat(
    stage.find('.pp-handle') as Konva.Node[],
  );
  const shown = overlays.filter((t) => t.isVisible());
  shown.forEach((t) => t.hide());
  try {
    return fn();
  } finally {
    shown.forEach((t) => t.show());
  }
}

/**
 * Temporarily force the `.view-group` nodes (base + annotations) to the FIT
 * transform, run fn, then restore the live transform. The user's Ctrl+wheel zoom
 * lives on those Groups; export must ignore it so the clip rect [dx,dy,dw,dh] and
 * pixelRatio (computed from the fit) line up with what's actually on the canvas.
 * toDataURL draws synchronously, so the swap is invisible.
 */
function withFitView<T>(stage: Konva.Stage, fit: { dx: number; dy: number; scale: number }, fn: () => T): T {
  const groups = stage.find('.view-group') as Konva.Node[];
  const saved = groups.map((g) => ({ g, x: g.x(), y: g.y(), sx: g.scaleX(), sy: g.scaleY() }));
  groups.forEach((g) => g.setAttrs({ x: fit.dx, y: fit.dy, scaleX: fit.scale, scaleY: fit.scale }));
  try {
    return fn();
  } finally {
    saved.forEach((s) => s.g.setAttrs({ x: s.x, y: s.y, scaleX: s.sx, scaleY: s.sy }));
  }
}

/**
 * Flatten the Stage to a PNG data URL, clipped to the on-screen image rectangle
 * and rendered at SOURCE-pixel resolution. The base node stores its current
 * (post-crop) width/height, so computeView gives the exact fit-scaled rect the
 * base + annotations occupy; clipping to it drops the letterbox bars and yields
 * a PNG matching the captured image's dimensions.
 */
export function flattenStage(stage: Konva.Stage): string {
  const base = useEditorStore.getState().nodes.find((n) => n.id === BASE_IMAGE_ID) as
    | ImageNodeBase
    | undefined;
  const baseW = base?.width ?? 0;
  const baseH = base?.height ?? 0;
  const view = computeView(baseW, baseH);
  // pixelRatio scales the fit-down view rect back up to source pixels.
  const pixelRatio = view.dw > 0 && baseW > 0 ? baseW / view.dw : 1;
  // Render at the FIT (ignoring any live Ctrl+wheel zoom) so the clip rect and
  // pixelRatio match the on-canvas geometry and the PNG is native-resolution.
  return withoutTransformers(stage, () =>
    withFitView(stage, view, () =>
      stage.toDataURL({
        pixelRatio,
        mimeType: 'image/png',
        x: view.dx,
        y: view.dy,
        width: view.dw,
        height: view.dh,
      }),
    ),
  );
}

/** Flatten + write the multi-format image to the clipboard. */
export async function copyToClipboard(stage: Konva.Stage): Promise<void> {
  const url = flattenStage(stage);
  const path = await ScreenshotService.SavePNG(url);
  await ExportService.CopyToClipboard(path, 'image');
}

/** Flatten + open the native Save-As dialog. Returns chosen path ("" === cancel). */
export async function saveAs(stage: Konva.Stage, name = 'Screenshot.png'): Promise<string> {
  const url = flattenStage(stage);
  const path = await ScreenshotService.SavePNG(url);
  return ExportService.SaveAs(path, name);
}
