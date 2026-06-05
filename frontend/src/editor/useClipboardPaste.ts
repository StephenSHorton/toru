// Paste-image support. PRIMARY path = the browser 'paste' ClipboardEvent.
// FALLBACK path = ExportService.ReadClipboardImage() (returned by the Toolbar
// Paste button). Both converge on the same insert helper, which loads the image
// to read its natural size, creates a centered PastedImageNode in IMAGE space,
// adds it, and selects it.

import { useCallback, useEffect } from 'react';
import { ExportService } from '@/lib/api';
import { useEditorStore, BASE_IMAGE_ID } from './store';
import type { PastedImageNode } from './types';
import { newId } from './geometry';

/** Load a data/blob URL into an HTMLImageElement to read natural dimensions. */
function loadImage(url: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const img = new window.Image();
    img.crossOrigin = 'anonymous';
    img.onload = () => resolve(img);
    img.onerror = reject;
    img.src = url;
  });
}

/** Insert a data-URL image as a centered, movable PastedImageNode (image space). */
export async function insertPastedImage(dataUrl: string): Promise<void> {
  const img = await loadImage(dataUrl);
  const s = useEditorStore.getState();
  const base = s.nodes.find((n) => n.id === BASE_IMAGE_ID);
  const baseW = base && base.type === 'image' ? base.width : img.naturalWidth;
  const baseH = base && base.type === 'image' ? base.height : img.naturalHeight;

  // scale down if larger than ~60% of the base image
  let scale = 1;
  const maxW = baseW * 0.6;
  const maxH = baseH * 0.6;
  if (img.naturalWidth * scale > maxW) scale = maxW / img.naturalWidth;
  if (img.naturalHeight * scale > maxH) scale = Math.min(scale, maxH / img.naturalHeight);

  const w = img.naturalWidth;
  const h = img.naturalHeight;
  const node: PastedImageNode = {
    id: newId('paste'),
    type: 'pasted-image',
    x: baseW / 2 - (w * scale) / 2,
    y: baseH / 2 - (h * scale) / 2,
    rotation: 0,
    opacity: 1,
    draggable: true,
    src: dataUrl,
    width: w,
    height: h,
    scaleX: scale,
    scaleY: scale,
  };
  s.addNode(node);
  s.select(node.id);
  s.setTool('select');
}

/** Toolbar Paste button -> backend clipboard read fallback. */
export async function pasteFromBackend(): Promise<void> {
  const url = await ExportService.ReadClipboardImage();
  if (url) await insertPastedImage(url);
}

/** Mount the window 'paste' listener (primary path). Returns the backend fallback. */
export function useClipboardPaste(): () => void {
  useEffect(() => {
    function onPaste(ev: ClipboardEvent) {
      const items = ev.clipboardData?.items;
      if (!items) return;
      for (const item of items) {
        if (item.type.startsWith('image/')) {
          const file = item.getAsFile();
          if (!file) continue;
          ev.preventDefault();
          const reader = new FileReader();
          reader.onload = () => {
            if (typeof reader.result === 'string') void insertPastedImage(reader.result);
          };
          reader.readAsDataURL(file);
          return;
        }
      }
    }
    window.addEventListener('paste', onPaste);
    return () => window.removeEventListener('paste', onPaste);
  }, []);

  return useCallback(() => {
    void pasteFromBackend();
  }, []);
}
