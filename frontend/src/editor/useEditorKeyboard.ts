// Global keyboard shortcuts for the editor. Mounted once in routes/Editor.tsx.
//
// CRITICAL: early-return when an input/textarea/contentEditable is focused, so
// inline text editing (and any future form field) gets raw keystrokes — otherwise
// Del/Backspace deletes the node instead of a character.
//
//   Del / Backspace          delete selected
//   Ctrl+Z                   undo
//   Ctrl+Shift+Z / Ctrl+Y    redo
//   Esc                      clear selection / back to select tool
//   V P R O A L T C          select / pen / rect / ellipse / arrow / line / text / crop
//   E                        emoji
//   Ctrl+]  / Ctrl+[         bring forward / send backward
//   Ctrl+0                   reset zoom to fit
//   Ctrl+= / Ctrl+-          zoom in / out (mouse wheel zooms toward the cursor;
//                            in Select mode, drag empty canvas to pan when zoomed)

import { useEffect } from 'react';
import { useEditorStore } from './store';
import { resetFit, zoomAtPointer, getStageSize } from './viewStore';
import type { ToolId } from './types';

function isEditableTarget(): boolean {
  const el = document.activeElement;
  if (!el) return false;
  const tag = el.tagName;
  return (
    tag === 'INPUT' ||
    tag === 'TEXTAREA' ||
    (el as HTMLElement).isContentEditable === true
  );
}

const KEY_TOOLS: Record<string, ToolId> = {
  v: 'select',
  p: 'pen',
  r: 'rect',
  o: 'ellipse',
  a: 'arrow',
  l: 'line',
  t: 'text',
  e: 'emoji',
  c: 'crop',
};

// `enabled` (default true) lets the overlay gate the editor shortcuts to EDIT mode
// only: in capture mode the editor canvas isn't rendered, so a tool-key/z-order/
// undo would mutate a hidden store (later wiped by loadBaseImage). The standalone
// Editor route omits the arg and stays always-on.
//
// `onEscapeEmpty` (optional) escalates Esc: in the overlay edit mode, Esc first
// clears selection / returns to the select tool (normal editor UX); when there is
// NOTHING left to clear (no selection AND already on select), it fires this to hide
// the overlay to the tray — matching the spec's "Done / Esc from edit mode -> hide"
// without surprising the user mid-edit. The standalone Editor route omits it, so
// Esc there only ever deselects.
export function useEditorKeyboard(enabled = true, onEscapeEmpty?: () => void) {
  useEffect(() => {
    if (!enabled) return;
    function onKeyDown(ev: KeyboardEvent) {
      if (isEditableTarget()) return;
      const s = useEditorStore.getState();
      const ctrl = ev.ctrlKey || ev.metaKey;

      // delete
      if (ev.key === 'Delete' || ev.key === 'Backspace') {
        if (s.selectedId) {
          ev.preventDefault();
          s.deleteSelected();
        }
        return;
      }

      // undo / redo
      if (ctrl && (ev.key === 'z' || ev.key === 'Z')) {
        ev.preventDefault();
        if (ev.shiftKey) s.redo();
        else s.undo();
        return;
      }
      if (ctrl && (ev.key === 'y' || ev.key === 'Y')) {
        ev.preventDefault();
        s.redo();
        return;
      }

      // z-order
      if (ctrl && ev.key === ']') {
        if (s.selectedId) { ev.preventDefault(); s.bringForward(s.selectedId); }
        return;
      }
      if (ctrl && ev.key === '[') {
        if (s.selectedId) { ev.preventDefault(); s.sendBackward(s.selectedId); }
        return;
      }

      // zoom — Ctrl+0 resets to fit; Ctrl+= / Ctrl+- zoom about the canvas center
      // (the mouse wheel, handled in EditorCanvas, zooms toward the cursor).
      if (ctrl && ev.key === '0') {
        ev.preventDefault();
        const base = s.nodes[0];
        const bw = base && base.type === 'image' ? base.width : 0;
        const bh = base && base.type === 'image' ? base.height : 0;
        resetFit(bw, bh);
        return;
      }
      if (ctrl && (ev.key === '=' || ev.key === '+')) {
        ev.preventDefault();
        const { w, h } = getStageSize();
        zoomAtPointer(w / 2, h / 2, -1);
        return;
      }
      if (ctrl && ev.key === '-') {
        ev.preventDefault();
        const { w, h } = getStageSize();
        zoomAtPointer(w / 2, h / 2, 1);
        return;
      }

      // escape
      if (ev.key === 'Escape') {
        // Nothing to clear (no selection, already on select) -> escalate to hide.
        if (!s.selectedId && s.activeTool === 'select') {
          if (onEscapeEmpty) {
            ev.preventDefault();
            onEscapeEmpty();
          }
          return;
        }
        s.select(null);
        if (s.activeTool !== 'select') s.setTool('select');
        return;
      }

      // tool hotkeys (no modifiers)
      if (!ctrl && !ev.altKey) {
        const t = KEY_TOOLS[ev.key.toLowerCase()];
        if (t) {
          ev.preventDefault();
          s.setTool(t);
        }
      }
    }

    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [enabled, onEscapeEmpty]);
}
