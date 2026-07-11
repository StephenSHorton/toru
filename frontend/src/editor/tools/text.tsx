// TEXT tool — click to place a text node, then type into an HTML <textarea>
// overlaid on the Konva Stage. Double-clicking an existing text node re-opens it
// for editing. Commits create a `text` node (when new) or update an existing one
// via the store; an empty commit discards a freshly-placed node.
//
// WHY A .tsx FILE: the `Tool` interface (./types) is purely imperative
// (onPointerDown/Move/Up), but inline text editing needs a real DOM <textarea>
// positioned in screen space. So this module exports TWO things that Integrate
// wires up independently:
//   1. `textTool`            — the Tool singleton registered in tools/index.ts.
//   2. `<TextEditingOverlay>`— a React overlay (mounted once in routes/Editor.tsx,
//                              next to <EditorCanvas/>) that renders the textarea
//                              and owns the Stage `dblclick`-to-edit affordance.
//
// FOCUS GOTCHA (the "text tool doesn't work" bug): WebView2 + AlwaysOnTop overlays
// often fail the first ta.focus() if it runs in the same tick as the Stage
// pointerdown that opened the session. A deferred focus (rAF + short timeout)
// is required. Also: onBlur must NOT immediately discard an empty draft — a
// spurious blur during focus handoff would abort the place before the user
// types anything.

import { useEffect, useRef, useState } from 'react';
import type Konva from 'konva';
import { create } from 'zustand';
import { Check, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import type { Tool, ToolContext } from './types';
import type { EditorNode, TextNode, NodeId } from '../types';
import { useEditorStore } from '../store';
import { newId } from '../geometry';
import { useView, getView } from '../viewStore';

const FONT_FAMILY = 'system-ui, -apple-system, "Segoe UI", sans-serif';
const DEFAULT_TEXT_WIDTH = 220; // image-space px; matches the textarea start width

// ---------------------------------------------------------------------------
// Editing-session bridge (module-local store)
// ---------------------------------------------------------------------------

interface SessionData {
  /** The node id being edited. `isNew` drafts are discarded on empty commit. */
  nodeId: NodeId | null;
  isNew: boolean;
  /** Live textarea value (uncommitted). */
  value: string;
  /** Image-space placement of the node being edited (for overlay positioning). */
  x: number;
  y: number;
  fontSize: number; // image-space font size
  fill: string;
  width: number; // image-space text box width
}

/** Fields supplied when opening a session (everything except the live `value`). */
type SessionOpenInput = Omit<SessionData, 'value'> & { value?: string };

interface TextEditSession extends SessionData {
  open(s: SessionOpenInput): void;
  setValue(v: string): void;
  close(): void;
}

const useTextEditSession = create<TextEditSession>((set) => ({
  nodeId: null,
  isNew: false,
  value: '',
  x: 0,
  y: 0,
  fontSize: 28,
  fill: '#ff3b30',
  width: DEFAULT_TEXT_WIDTH,

  open(s) {
    set({ ...s, value: s.value ?? '' });
  },
  setValue(v) {
    set({ value: v });
  },
  close() {
    set({ nodeId: null, isNew: false, value: '' });
  },
}));

/** True while a text node is being edited (overlay textarea is mounted). */
export function isTextEditing(): boolean {
  return useTextEditSession.getState().nodeId !== null;
}

/**
 * Force-close any open text-editing session (module singleton) WITHOUT committing
 * or aborting a draft. Used when the overlay re-engages / re-enters edit mode.
 */
export function resetTextEditSession(): void {
  if (useTextEditSession.getState().nodeId === null) return;
  useTextEditSession.getState().close();
}

/** Cancel the current text session if any (right-click / Esc tool cancel). */
export function cancelTextEditIfAny(): void {
  if (!isTextEditing()) return;
  cancelSession();
}

// ---------------------------------------------------------------------------
// Hit-testing helpers (image space)
// ---------------------------------------------------------------------------

/** Approximate the image-space bounding box of a text node (rotation ignored). */
function textBox(n: TextNode): { x: number; y: number; w: number; h: number } {
  const lines = n.text.length ? n.text.split('\n') : [''];
  const longest = lines.reduce((m, l) => Math.max(m, l.length), 1);
  const w = n.width ?? Math.max(longest * n.fontSize * 0.6, 20);
  const h = Math.max(lines.length, 1) * n.fontSize * 1.2;
  return { x: n.x, y: n.y, w, h };
}

function findTextNodeAt(nodes: EditorNode[], px: number, py: number): TextNode | null {
  for (let i = nodes.length - 1; i >= 0; i--) {
    const n = nodes[i];
    if (n.type !== 'text') continue;
    const b = textBox(n);
    if (px >= b.x && px <= b.x + b.w && py >= b.y && py <= b.y + b.h) return n;
  }
  return null;
}

// ---------------------------------------------------------------------------
// Session control (shared by the tool + the dblclick affordance)
// ---------------------------------------------------------------------------

function beginEditExisting(node: TextNode): void {
  useEditorStore.getState().select(node.id);
  useTextEditSession.getState().open({
    nodeId: node.id,
    isNew: false,
    value: node.text,
    x: node.x,
    y: node.y,
    fontSize: node.fontSize,
    fill: node.fill,
    width: node.width ?? DEFAULT_TEXT_WIDTH,
  });
}

function beginPlaceNew(px: number, py: number): void {
  const store = useEditorStore.getState();
  const id = newId('text');
  const node: TextNode = {
    id,
    type: 'text',
    x: px,
    y: py,
    rotation: 0,
    opacity: 1,
    draggable: true,
    text: '',
    fontSize: store.activeFontSize,
    fontFamily: FONT_FAMILY,
    fill: store.activeColor,
    width: DEFAULT_TEXT_WIDTH,
    align: 'left',
  };
  store.addNode(node);
  store.select(id);
  useTextEditSession.getState().open({
    nodeId: id,
    isNew: true,
    value: '',
    x: px,
    y: py,
    fontSize: node.fontSize,
    fill: node.fill,
    width: DEFAULT_TEXT_WIDTH,
  });
}

/** Commit the live textarea value to the store (or discard an empty new draft). */
function commitSession(): void {
  const sess = useTextEditSession.getState();
  const { nodeId, isNew, value } = sess;
  if (nodeId === null) return;
  const trimmed = value.replace(/\s+$/g, '');
  const store = useEditorStore.getState();

  if (trimmed.length === 0) {
    if (isNew) {
      store.abortDraft(nodeId);
    } else {
      store.select(nodeId);
      store.deleteSelected();
    }
    sess.close();
    return;
  }

  if (isNew) {
    store.mutateDrawingNode(nodeId, { text: value } as Partial<EditorNode>);
  } else {
    store.updateNode(nodeId, { text: value } as Partial<EditorNode>);
  }
  sess.close();
}

/** Cancel editing: discard an empty/new draft; leave an existing node untouched. */
function cancelSession(): void {
  const sess = useTextEditSession.getState();
  const { nodeId, isNew } = sess;
  if (nodeId !== null && isNew) {
    useEditorStore.getState().abortDraft(nodeId);
  }
  sess.close();
}

// ---------------------------------------------------------------------------
// The Tool singleton
// ---------------------------------------------------------------------------

export const textTool: Tool = {
  id: 'text',
  cursor: 'text',

  onPointerDown(e: Konva.KonvaEventObject<PointerEvent>, ctx: ToolContext) {
    // Right-click is handled globally (cancel tool) — ignore here.
    if (e.evt.button === 2) return;

    // If a session is already open, commit it first (clicking away = commit).
    if (isTextEditing()) {
      commitSession();
      return;
    }
    const p = ctx.getPointer();
    if (!p) return;
    const hit = findTextNodeAt(ctx.store.nodes, p.x, p.y);
    if (hit) beginEditExisting(hit);
    else beginPlaceNew(p.x, p.y);
  },

  onPointerMove() {},
  onPointerUp() {},
};

// ---------------------------------------------------------------------------
// The React overlay
// ---------------------------------------------------------------------------

export interface TextEditingOverlayProps {
  stageRef: React.RefObject<Konva.Stage | null>;
}

export function TextEditingOverlay({ stageRef }: TextEditingOverlayProps) {
  const session = useTextEditSession();
  const taRef = useRef<HTMLTextAreaElement>(null);
  const chipRef = useRef<HTMLDivElement>(null);
  const [, force] = useState(0);
  // Ignore blur until focus has successfully landed (prevents place→abort race).
  const armedRef = useRef(false);

  const view = useView();

  // Double-click on the Stage: if it lands on a text node, open it for editing.
  useEffect(() => {
    const stage = stageRef.current;
    if (!stage) return;
    const onDbl = () => {
      const editorState = useEditorStore.getState();
      const v = getView();
      const pt = stage.getPointerPosition();
      if (!pt) return;
      const px = (pt.x - v.dx) / v.scale;
      const py = (pt.y - v.dy) / v.scale;
      const hit = findTextNodeAt(editorState.nodes, px, py);
      if (hit) beginEditExisting(hit);
    };
    stage.on('dblclick', onDbl);
    stage.on('dbltap', onDbl);
    return () => {
      stage.off('dblclick', onDbl);
      stage.off('dbltap', onDbl);
    };
  }, [stageRef, session.nodeId]);

  // Focus the textarea AFTER the Stage pointerdown settles. WebView2 + AlwaysOnTop
  // routinely drops a same-tick focus() call; rAF + short timeout is reliable.
  useEffect(() => {
    if (session.nodeId === null) {
      armedRef.current = false;
      return;
    }
    armedRef.current = false;
    let cancelled = false;
    const focusTA = () => {
      if (cancelled) return;
      const ta = taRef.current;
      if (!ta) return;
      ta.focus({ preventScroll: true });
      // Only select existing text (re-edit); leave caret at end for new places.
      if (!useTextEditSession.getState().isNew) {
        ta.select();
      }
      if (document.activeElement === ta) {
        armedRef.current = true;
      }
    };
    const raf = requestAnimationFrame(() => {
      focusTA();
      // Second attempt — covers the common AlwaysOnTop / WebView2 late-focus case.
      window.setTimeout(focusTA, 30);
      window.setTimeout(() => {
        focusTA();
        armedRef.current = true; // arm blur handling even if focus failed
      }, 80);
    });
    return () => {
      cancelled = true;
      cancelAnimationFrame(raf);
    };
  }, [session.nodeId]);

  // Keep the overlay aligned if the window resizes while editing.
  useEffect(() => {
    if (session.nodeId === null) return;
    const onResize = () => force((n) => n + 1);
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, [session.nodeId]);

  if (session.nodeId === null) return null;

  const stage = stageRef.current;
  if (!stage) return null;

  const containerRect = stage.container().getBoundingClientRect();
  const left = containerRect.left + view.dx + session.x * view.scale;
  const top = containerRect.top + view.dy + session.y * view.scale;
  const screenFontSize = Math.max(8, session.fontSize * view.scale);
  const screenWidth = Math.max(80, session.width * view.scale);

  function finish(commit: boolean) {
    if (commit) commitSession();
    else cancelSession();
  }

  function handleBlur(e: React.FocusEvent<HTMLTextAreaElement>) {
    // Focus moving to the confirm/cancel chip — ignore (mousedown already preventDefault).
    const next = e.relatedTarget as Node | null;
    if (next && chipRef.current?.contains(next)) return;
    if (!armedRef.current) {
      // Spurious blur during focus handoff — re-focus.
      window.setTimeout(() => taRef.current?.focus({ preventScroll: true }), 0);
      return;
    }
    // Defer so a mousedown on the chip / canvas can run first.
    window.setTimeout(() => {
      if (!isTextEditing()) return;
      if (document.activeElement === taRef.current) return;
      if (chipRef.current?.contains(document.activeElement)) return;
      finish(true);
    }, 0);
  }

  return (
    <>
      <textarea
        ref={taRef}
        value={session.value}
        spellCheck={false}
        autoFocus
        placeholder="Type…"
        onChange={(e) => useTextEditSession.getState().setValue(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            finish(true);
          } else if (e.key === 'Escape') {
            e.preventDefault();
            finish(false);
          }
          e.stopPropagation();
        }}
        onBlur={handleBlur}
        style={{
          position: 'fixed',
          left,
          top,
          width: screenWidth,
          minHeight: screenFontSize * 1.4,
          fontSize: screenFontSize,
          lineHeight: 1.2,
          fontFamily: FONT_FAMILY,
          color: session.fill,
          // Slightly opaque fill so the caret is always visible on dark crops.
          background: 'color-mix(in oklch, var(--color-card) 55%, transparent)',
          caretColor: session.fill,
          border: '1px dashed color-mix(in oklch, var(--color-ring) 80%, transparent)',
          outline: 'none',
          padding: '2px 4px',
          margin: 0,
          resize: 'none',
          overflow: 'hidden',
          zIndex: 50,
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-word',
          boxShadow: '0 2px 12px oklch(0 0 0 / 0.35)',
        }}
      />
      <div
        ref={chipRef}
        className="frost"
        style={{
          position: 'fixed',
          left,
          top: Math.max(4, top - 40),
          display: 'flex',
          gap: 4,
          padding: 4,
          zIndex: 51,
        }}
        onMouseDown={(e) => e.preventDefault()}
      >
        <Button
          size="icon"
          variant="ghost"
          className="h-7 w-7"
          title="Apply (Enter)"
          onClick={() => finish(true)}
        >
          <Check />
        </Button>
        <Button
          size="icon"
          variant="ghost"
          className="h-7 w-7"
          title="Cancel (Esc)"
          onClick={() => finish(false)}
        >
          <X />
        </Button>
      </div>
    </>
  );
}
