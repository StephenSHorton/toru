// PASTE-IMAGE tool + hook (Developer 1, screenshot editor).
//
// Implements the `image` ToolId from the node/tool contract: pasting an image
// from the clipboard as a movable / resizable / rotatable / layerable
// `pasted-image` node. The node model, store API, select tool, Transformer, and
// z-order ops already give those affordances once the node exists — this file
// owns ONLY the insertion path.
//
// TWO ENTRY POINTS converge on one insert helper (`insertClipboardImageNode`):
//   1. PRIMARY  — the browser `paste` ClipboardEvent (real image bytes off the
//      OS clipboard). Mounted via `usePasteImage()`.
//   2. FALLBACK — ExportService.ReadClipboardImage() (Go-side multi-format read
//      returning a base64 data URL). Used by the tool's pointer-down and by the
//      returned `pasteFromClipboard` callback / the <PasteImageButton>.
//
// A synthetic `paste` event cannot be dispatched from a click, so when the
// `image` tool is active and the user clicks the canvas, we drop back to the
// backend read and drop the image at the clicked point (image space).
//
// Self-contained: imports only the foundation contracts (store, types,
// geometry, @/lib/api) and shared UI primitives. Edits no shared file.

import { useCallback, useEffect } from 'react';
import { Clipboard } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { ExportService } from '@/lib/api';
import type { Tool, ToolContext } from './types';
import { useEditorStore, BASE_IMAGE_ID } from '../store';
import type { PastedImageNode } from '../types';
import { newId } from '../geometry';

/** Load a data/blob URL into an HTMLImageElement to read natural dimensions. */
function loadImage(url: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const img = new window.Image();
    img.crossOrigin = 'anonymous';
    img.onload = () => resolve(img);
    img.onerror = () => reject(new Error('paste-image: failed to decode clipboard image'));
    img.src = url;
  });
}

/** Read the base image's intrinsic size from the live store (fallbacks to the pasted image). */
function baseSize(fallbackW: number, fallbackH: number): { w: number; h: number } {
  const base = useEditorStore.getState().nodes.find((n) => n.id === BASE_IMAGE_ID);
  if (base && base.type === 'image') return { w: base.width, h: base.height };
  return { w: fallbackW, h: fallbackH };
}

/**
 * Insert a data-URL image as a `pasted-image` node and select it.
 *
 * Coords are IMAGE space. If `at` is given (e.g. the clicked point), the image
 * is centered there; otherwise it is centered on the base image. The image is
 * scaled down so it never exceeds ~60% of the base in either dimension.
 */
export async function insertClipboardImageNode(
  dataUrl: string,
  at?: { x: number; y: number } | null,
): Promise<void> {
  if (!dataUrl) return;
  const img = await loadImage(dataUrl);
  const w = img.naturalWidth || img.width;
  const h = img.naturalHeight || img.height;
  if (!w || !h) return;

  const { w: baseW, h: baseH } = baseSize(w, h);

  // Fit to ~60% of the base image (uniform scale).
  let scale = 1;
  const maxW = baseW * 0.6;
  const maxH = baseH * 0.6;
  if (w * scale > maxW) scale = maxW / w;
  if (h * scale > maxH) scale = Math.min(scale, maxH / h);

  const cx = at ? at.x : baseW / 2;
  const cy = at ? at.y : baseH / 2;

  const node: PastedImageNode = {
    id: newId('paste'),
    type: 'pasted-image',
    x: cx - (w * scale) / 2,
    y: cy - (h * scale) / 2,
    rotation: 0,
    opacity: 1,
    draggable: true,
    src: dataUrl,
    width: w,
    height: h,
    scaleX: scale,
    scaleY: scale,
  };

  const s = useEditorStore.getState();
  s.addNode(node);           // one history step
  s.select(node.id);         // immediately selectable / transformable
  s.setTool('select');       // hand control to the Transformer
}

/** FALLBACK path: read the OS clipboard via Go and insert (optionally at a point). */
export async function pasteFromBackend(at?: { x: number; y: number } | null): Promise<void> {
  const url = await ExportService.ReadClipboardImage();
  if (url) await insertClipboardImageNode(url, at);
}

/** Extract the first image item from a ClipboardEvent and insert it (PRIMARY path). */
function handlePasteEvent(ev: ClipboardEvent): boolean {
  const items = ev.clipboardData?.items;
  if (!items) return false;
  for (const item of items) {
    if (item.kind === 'file' && item.type.startsWith('image/')) {
      const file = item.getAsFile();
      if (!file) continue;
      ev.preventDefault();
      const reader = new FileReader();
      reader.onload = () => {
        if (typeof reader.result === 'string') void insertClipboardImageNode(reader.result);
      };
      reader.readAsDataURL(file);
      return true;
    }
  }
  return false;
}

/**
 * Mount the window-level `paste` listener (primary clipboard path) and return a
 * `pasteFromClipboard` callback (backend fallback) for a button / menu item.
 *
 * Headless: renders nothing, edits no shared file. Mount once near the editor
 * root. Safe to use as the editor's single paste hook OR alongside another.
 */
export function usePasteImage(): () => void {
  useEffect(() => {
    function onPaste(ev: ClipboardEvent) {
      handlePasteEvent(ev);
    }
    window.addEventListener('paste', onPaste);
    return () => window.removeEventListener('paste', onPaste);
  }, []);

  return useCallback(() => {
    void pasteFromBackend();
  }, []);
}

/**
 * The `image` Tool. Clicking the canvas while this tool is active inserts the
 * current clipboard image (backend read) at the clicked image-space point. The
 * resulting `pasted-image` node is movable/resizable/rotatable/layerable via the
 * select tool + Transformer + z-order ops.
 *
 * Register by replacing `image: noop('image', 'default')` with `image: pasteImageTool`
 * in editor/tools/index.ts.
 */
export const pasteImageTool: Tool = {
  id: 'image',
  cursor: 'copy',

  onPointerDown(_e, ctx: ToolContext) {
    const at = ctx.getPointer();
    void pasteFromBackend(at);
  },

  onPointerMove() {
    // no-op: insertion is a single discrete action on pointer-down.
  },

  onPointerUp() {
    // no-op.
  },
};

/**
 * Optional frosted toolbar button (shadcn Button + lucide, zero rounded corners).
 * Triggers the backend-fallback paste. Integrate may drop this into the Toolbar's
 * action cluster, or ignore it and wire `usePasteImage()` to the existing button.
 */
export function PasteImageButton({ onPaste }: { onPaste?: () => void }) {
  const handle = useCallback(() => {
    if (onPaste) onPaste();
    else void pasteFromBackend();
  }, [onPaste]);
  return (
    <Button size="sm" variant="ghost" title="Paste image from clipboard" onClick={handle}>
      <Clipboard /> Paste
    </Button>
  );
}
