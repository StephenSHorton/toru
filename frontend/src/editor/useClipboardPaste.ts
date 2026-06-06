// Paste-anything support. Pressing Ctrl+V anywhere in the editor drops whatever
// is on the clipboard onto the canvas — no button required:
//   • image bytes  -> a centered, movable PastedImageNode
//   • plain text   -> a centered TextNode
//   • nothing usable in the JS clipboard (e.g. an image copied from a native app
//     that Chromium didn't surface) -> ExportService.ReadClipboardImage() fallback
// While a text field is focused (inline text editing), paste is left to the
// browser so it lands in the field instead of spawning a node.

import { useEffect } from 'react';
import { ExportService } from '@/lib/api';
import { useEditorStore, BASE_IMAGE_ID } from './store';
import type { PastedImageNode, TextNode } from './types';
import { newId } from './geometry';

const PASTE_FONT_FAMILY = 'system-ui, -apple-system, "Segoe UI", sans-serif';

/** True while an input/textarea/contentEditable is focused (skip canvas paste). */
function isEditableTarget(): boolean {
  const el = document.activeElement;
  if (!el) return false;
  const tag = el.tagName;
  return tag === 'INPUT' || tag === 'TEXTAREA' || (el as HTMLElement).isContentEditable === true;
}

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

/** Insert clipboard plain text as a centered, movable TextNode (image space). */
export function insertPastedText(text: string): void {
  const s = useEditorStore.getState();
  const base = s.nodes.find((n) => n.id === BASE_IMAGE_ID);
  const baseW = base && base.type === 'image' ? base.width : 800;
  const baseH = base && base.type === 'image' ? base.height : 600;
  const fontSize = s.activeFontSize;
  // Wrap long text within ~60% of the image so a paragraph paste stays readable.
  const width = Math.max(120, Math.round(Math.min(baseW * 0.6, 600)));
  const node: TextNode = {
    id: newId('text'),
    type: 'text',
    x: Math.round(baseW / 2 - width / 2),
    y: Math.round(baseH / 2 - fontSize),
    rotation: 0,
    opacity: 1,
    draggable: true,
    text: text.replace(/\r\n/g, '\n'),
    fontSize,
    fontFamily: PASTE_FONT_FAMILY,
    fill: s.activeColor,
    width,
    align: 'left',
  };
  s.addNode(node);
  s.select(node.id);
  s.setTool('select');
}

/** Backend clipboard read fallback (native-app images Chromium didn't surface). */
export async function pasteFromBackend(): Promise<void> {
  const url = await ExportService.ReadClipboardImage();
  if (url) await insertPastedImage(url);
}

/**
 * Mount the window 'paste' listener — the single paste-anything entry point.
 * `enabled` (default true) lets the overlay gate paste to EDIT mode only: in
 * capture mode the editor canvas isn't rendered, so a paste would silently addNode
 * into a hidden store (later wiped by loadBaseImage) — a no-op the user can't see.
 * The standalone Editor route omits the arg and stays always-on.
 */
export function useClipboardPaste(enabled = true): void {
  useEffect(() => {
    if (!enabled) return;
    function onPaste(ev: ClipboardEvent) {
      // Editing text -> let the browser paste into the field.
      if (isEditableTarget()) return;
      const dt = ev.clipboardData;
      if (!dt) {
        void pasteFromBackend();
        return;
      }

      // 1) Image bytes -> pasted-image layer.
      for (const item of dt.items) {
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

      // 2) Plain text -> text node.
      const text = dt.getData('text/plain');
      if (text && text.trim().length > 0) {
        ev.preventDefault();
        insertPastedText(text);
        return;
      }

      // 3) Nothing in the JS clipboard -> ask the Go backend (native-app image).
      void pasteFromBackend();
    }
    window.addEventListener('paste', onPaste);
    return () => window.removeEventListener('paste', onPaste);
  }, [enabled]);
}
