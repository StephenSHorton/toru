// CROP tool (Developer 1).
//
// Drag a crop rectangle over the base image, then confirm/cancel via a frosted
// affordance. On confirm the visible canvas/base is reduced to the crop region:
// the base image node records an undoable `crop` (image-space) and its
// width/height shrink to the crop size, so EditorCanvas.computeView re-fits the
// smaller canvas; every annotation is shifted by (-cropX, -cropY) so it stays
// pinned to the same pixels.
//
// SHIP NOTE — this file exports TWO symbols:
//   • `cropTool`     — the singleton Tool, registered in tools/index.ts.
//   • `CropOverlay`  — a React DOM overlay (the frosted confirm/cancel bar +
//                      the dim-mask + selection rectangle). It MUST be mounted
//                      over the Konva Stage container by Editor.tsx (see the
//                      wiring instructions returned to Integrate). The Tool
//                      object can't render UI itself — the Tool interface is
//                      pointer-handlers only — so the overlay is a sibling that
//                      reads the in-progress crop rect from a tiny module-level
//                      pub/sub store the tool writes to.
//
// Everything here is self-contained: it touches NO shared file. It only READS
// the Zustand store (activeTool + base image dims) and applies the crop through
// the public store API (commit() for the undo baseline, then mutateDrawingNode()
// for the batched node edits == one clean undoable step).

import { useSyncExternalStore } from 'react';
import { Check, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { Tool, ToolContext } from './types';
import type { EditorNode } from '../types';
import { useEditorStore, BASE_IMAGE_ID } from '../store';
import { STAGE_W, STAGE_H } from '../EditorCanvas';
import { useView } from '../viewStore';

// --- minimum crop size (image px) below which a drag is discarded ----------
const MIN_CROP = 8;

// --- in-progress crop rect, in IMAGE space (matches how nodes are stored) ---
interface CropRect {
  x: number;
  y: number;
  w: number;
  h: number;
}

interface CropDraft {
  /** Pointer-down anchor (image space); null when no drag in flight. */
  anchor: { x: number; y: number } | null;
  /** Live rect while dragging or pending confirm; null when idle. */
  rect: CropRect | null;
  /** True after pointer-up while awaiting Confirm/Cancel. */
  pending: boolean;
}

// Tiny vanilla pub/sub store (no React dependency) the tool writes to and the
// overlay subscribes to via useSyncExternalStore.
let draft: CropDraft = { anchor: null, rect: null, pending: false };
const listeners = new Set<() => void>();

function emit(): void {
  for (const l of listeners) l();
}
function setDraft(next: CropDraft): void {
  draft = next;
  emit();
}
function getDraft(): CropDraft {
  return draft;
}
function subscribe(cb: () => void): () => void {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

/** Reset the crop draft (used by Cancel, tool-switch, and after a confirm). */
export function resetCropDraft(): void {
  if (draft.anchor === null && draft.rect === null && !draft.pending) return;
  setDraft({ anchor: null, rect: null, pending: false });
}

// --- base-image helpers ------------------------------------------------------

/** The current base image node (always nodes[0]); null until loaded. */
function baseImage() {
  const base = useEditorStore.getState().nodes[0];
  return base && base.type === 'image' ? base : null;
}

/** Clamp v into [lo, hi]. */
function clamp(v: number, lo: number, hi: number): number {
  return Math.max(lo, Math.min(hi, v));
}

/**
 * Build a normalized crop rect (image space) from the anchor + a current image
 * point, clamped to the base image bounds. The base occupies [0..width] ×
 * [0..height] in image space (origin 0,0).
 */
function rectFrom(
  anchor: { x: number; y: number },
  cur: { x: number; y: number },
  baseW: number,
  baseH: number,
): CropRect {
  const ax = clamp(anchor.x, 0, baseW);
  const ay = clamp(anchor.y, 0, baseH);
  const cx = clamp(cur.x, 0, baseW);
  const cy = clamp(cur.y, 0, baseH);
  return {
    x: Math.min(ax, cx),
    y: Math.min(ay, cy),
    w: Math.abs(cx - ax),
    h: Math.abs(cy - ay),
  };
}

/**
 * Apply the pending crop. Reduces the base to the crop region (records `crop`
 * + shrinks width/height) and shifts every annotation by (-rect.x, -rect.y) so
 * it stays aligned with the surviving pixels. One undoable step:
 *   commit() pushes the pre-crop snapshot onto `past`, then the batched node
 *   edits go through mutateDrawingNode() (no extra history). undo() restores it.
 */
function applyCrop(rect: CropRect): void {
  const store = useEditorStore.getState();
  const base = baseImage();
  if (!base) return;
  if (rect.w < MIN_CROP || rect.h < MIN_CROP) {
    resetCropDraft();
    return;
  }

  // If a previous crop already exists, the new crop is relative to the
  // currently-visible region, so compose offsets onto any prior crop origin.
  const prior = base.crop;
  const nextCrop = {
    x: (prior?.x ?? 0) + rect.x,
    y: (prior?.y ?? 0) + rect.y,
    w: rect.w,
    h: rect.h,
  };

  // 1) Baseline for undo == the exact pre-crop scene.
  store.commit();

  // 2) Base image: record the crop + resize the canvas to the crop region.
  store.mutateDrawingNode(BASE_IMAGE_ID, {
    crop: nextCrop,
    width: rect.w,
    height: rect.h,
  } as Partial<EditorNode>);

  // 3) Shift every annotation so it tracks the surviving pixels.
  for (const n of useEditorStore.getState().nodes) {
    if (n.id === BASE_IMAGE_ID) continue;
    store.mutateDrawingNode(n.id, {
      x: n.x - rect.x,
      y: n.y - rect.y,
    } as Partial<EditorNode>);
  }

  // 4) Clear selection (positions/Transformer would be stale) + drop the draft.
  store.select(null);
  resetCropDraft();
}

// --- the Tool ----------------------------------------------------------------

export const cropTool: Tool = {
  id: 'crop',
  cursor: 'crosshair',

  onPointerDown(_e, ctx: ToolContext) {
    const base = baseImage();
    if (!base) return;
    const p = ctx.getPointer();
    if (!p) return;
    const anchor = { x: clamp(p.x, 0, base.width), y: clamp(p.y, 0, base.height) };
    setDraft({ anchor, rect: { ...anchor, w: 0, h: 0 }, pending: false });
  },

  onPointerMove(_e, ctx: ToolContext) {
    const d = getDraft();
    if (!d.anchor || d.pending) return;
    const base = baseImage();
    if (!base) return;
    const p = ctx.getPointer();
    if (!p) return;
    setDraft({
      anchor: d.anchor,
      rect: rectFrom(d.anchor, p, base.width, base.height),
      pending: false,
    });
  },

  onPointerUp(_e, _ctx: ToolContext) {
    const d = getDraft();
    if (!d.anchor || !d.rect) return;
    // Too small to be a real crop -> discard the draft, stay idle.
    if (d.rect.w < MIN_CROP || d.rect.h < MIN_CROP) {
      resetCropDraft();
      return;
    }
    // Keep the rect on screen and await Confirm/Cancel.
    setDraft({ anchor: null, rect: d.rect, pending: true });
  },
};

// --- the overlay (frosted confirm/cancel + dim mask + selection rect) --------

function useCropDraft(): CropDraft {
  return useSyncExternalStore(subscribe, getDraft, getDraft);
}

/**
 * CropOverlay — an absolutely-positioned DOM layer matching the Stage. Renders
 * only while the crop tool is active. Shows the live/pending selection rectangle
 * with a dimmed surround and a frosted Confirm/Cancel bar. Mount it as a sibling
 * of <EditorCanvas/> inside a position:relative container of size STAGE_W×STAGE_H.
 */
export function CropOverlay() {
  const activeTool = useEditorStore((s) => s.activeTool);
  const base = useEditorStore((s) => s.nodes[0]);
  const d = useCropDraft();
  // Live view so the crop rect/mask track the canvas while Ctrl+wheel-zoomed.
  const view = useView();

  // Only meaningful while cropping and the base image is present.
  if (activeTool !== 'crop' || !base || base.type !== 'image') return null;

  const rect = d.rect;

  // Convert an image-space rect to on-screen Stage px.
  const screen = rect
    ? {
        left: view.dx + rect.x * view.scale,
        top: view.dy + rect.y * view.scale,
        width: rect.w * view.scale,
        height: rect.h * view.scale,
      }
    : null;

  const onConfirm = () => {
    if (rect) applyCrop(rect);
  };
  const onCancel = () => resetCropDraft();

  return (
    <div
      className="pointer-events-none absolute inset-0 select-none"
      style={{ width: STAGE_W, height: STAGE_H }}
      // Capture keyboard while a pending crop exists (Enter=confirm, Esc=cancel).
      onKeyDown={(e) => {
        if (!d.pending) return;
        if (e.key === 'Enter') {
          e.preventDefault();
          onConfirm();
        } else if (e.key === 'Escape') {
          e.preventDefault();
          onCancel();
        }
      }}
      tabIndex={-1}
    >
      {/* Dim mask outside the selection (four panels around the rect). */}
      {screen && (
        <>
          <MaskPanel left={0} top={0} width={STAGE_W} height={screen.top} />
          <MaskPanel
            left={0}
            top={screen.top}
            width={screen.left}
            height={screen.height}
          />
          <MaskPanel
            left={screen.left + screen.width}
            top={screen.top}
            width={STAGE_W - (screen.left + screen.width)}
            height={screen.height}
          />
          <MaskPanel
            left={0}
            top={screen.top + screen.height}
            width={STAGE_W}
            height={STAGE_H - (screen.top + screen.height)}
          />
        </>
      )}

      {/* Hint when idle (no rect yet). */}
      {!screen && (
        <div className="absolute left-1/2 top-3 -translate-x-1/2">
          <div className="frost px-3 py-1.5 text-xs text-foreground">
            Drag to select a crop region
          </div>
        </div>
      )}

      {/* The selection rectangle. */}
      {screen && (
        <div
          className="absolute border border-primary"
          style={{
            left: screen.left,
            top: screen.top,
            width: screen.width,
            height: screen.height,
            boxShadow: '0 0 0 1px oklch(1 0 0 / 0.35) inset',
          }}
        >
          {/* corner ticks for affordance */}
          <Corner cls="left-0 top-0 border-l-2 border-t-2" />
          <Corner cls="right-0 top-0 border-r-2 border-t-2" />
          <Corner cls="left-0 bottom-0 border-l-2 border-b-2" />
          <Corner cls="right-0 bottom-0 border-r-2 border-b-2" />

          {/* live size readout */}
          {rect && (
            <div className="absolute left-0 top-0 -translate-y-full pb-1">
              <span className="frost px-1.5 py-0.5 font-mono text-[10px] text-foreground">
                {Math.round(rect.w)} × {Math.round(rect.h)}
              </span>
            </div>
          )}
        </div>
      )}

      {/* Confirm / Cancel affordance (only once a rect is pending). */}
      {d.pending && rect && (
        <div className="pointer-events-auto absolute bottom-3 left-1/2 -translate-x-1/2">
          <div className="frost flex items-center gap-1 px-2 py-1.5">
            <Button size="sm" variant="ghost" title="Cancel (Esc)" onClick={onCancel}>
              <X /> Cancel
            </Button>
            <Button size="sm" title="Apply crop (Enter)" onClick={onConfirm}>
              <Check /> Crop
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

function MaskPanel({
  left,
  top,
  width,
  height,
}: {
  left: number;
  top: number;
  width: number;
  height: number;
}) {
  if (width <= 0 || height <= 0) return null;
  return (
    <div
      className="absolute bg-black/50"
      style={{ left, top, width, height }}
    />
  );
}

function Corner({ cls }: { cls: string }) {
  return (
    <div className={cn('absolute h-3 w-3 border-primary', cls)} />
  );
}
