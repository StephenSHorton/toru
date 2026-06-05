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
// The two halves are bridged by a tiny module-local Zustand store
// (`useTextEditSession`) so the imperative tool can open an editing session that
// the React overlay reacts to. No edits to the shared editor store, registry,
// or toolbar are required.
//
// COORDINATE MODEL (mirrors EditorCanvas): annotation coords are IMAGE space.
// On-screen, an image-space point (x,y) maps to the Stage-container-local point
// (view.dx + x*view.scale, view.dy + y*view.scale); add the container's
// client-rect origin to get viewport (position:fixed) coordinates.

import { useEffect, useRef, useState } from 'react';
import type Konva from 'konva';
import { create } from 'zustand';
import { Check, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import type { Tool, ToolContext } from './types';
import type { EditorNode, TextNode, NodeId } from '../types';
import { useEditorStore } from '../store';
import { newId } from '../geometry';
import { computeView } from '../EditorCanvas';

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

// ---------------------------------------------------------------------------
// Hit-testing helpers (image space)
// ---------------------------------------------------------------------------

/** Approximate the image-space bounding box of a text node (rotation ignored). */
function textBox(n: TextNode): { x: number; y: number; w: number; h: number } {
  const lines = n.text.length ? n.text.split('\n') : [''];
  const longest = lines.reduce((m, l) => Math.max(m, l.length), 1);
  // Konva wraps to node.width when set; otherwise width grows with content.
  const w = n.width ?? Math.max(longest * n.fontSize * 0.6, 20);
  const h = lines.length * n.fontSize * 1.2;
  return { x: n.x, y: n.y, w, h };
}

function findTextNodeAt(nodes: EditorNode[], px: number, py: number): TextNode | null {
  // Topmost first (later nodes render on top).
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
  // addNode commits a history step; an empty commit/cancel on close calls
  // abortDraft, which removes the draft AND pops that snapshot, so undo stays clean.
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
      // Discard the empty draft AND pop the snapshot its addNode() pushed, so the
      // aborted placement leaves no phantom undo/redo step.
      store.abortDraft(nodeId);
    } else {
      // Existing node emptied -> delete it as a normal undoable edit.
      store.select(nodeId);
      store.deleteSelected();
    }
    sess.close();
    return;
  }

  if (isNew) {
    // The draft's `addNode` already pushed the single "before this text existed"
    // history step. Write the text WITHOUT a second step so one undo removes the
    // whole text node.
    store.mutateDrawingNode(nodeId, { text: value } as Partial<EditorNode>);
  } else {
    // Re-editing an existing node: the text change is its own undoable edit.
    store.updateNode(nodeId, { text: value } as Partial<EditorNode>);
  }
  sess.close();
}

/** Cancel editing: discard an empty/new draft; leave an existing node untouched. */
function cancelSession(): void {
  const sess = useTextEditSession.getState();
  const { nodeId, isNew } = sess;
  if (nodeId !== null && isNew) {
    // The draft node was created via addNode (one history step). abortDraft drops
    // the node AND pops that snapshot in one call, so the cancelled placement
    // leaves no orphaned (phantom) undo step.
    useEditorStore.getState().abortDraft(nodeId);
  }
  // Existing nodes were never mutated during editing (text lives only in the
  // session until commit), so cancel needs no store change for them.
  sess.close();
}

// ---------------------------------------------------------------------------
// The Tool singleton
// ---------------------------------------------------------------------------

export const textTool: Tool = {
  id: 'text',
  cursor: 'text',

  onPointerDown(_e: Konva.KonvaEventObject<PointerEvent>, ctx: ToolContext) {
    // If a session is already open, commit it first (clicking away = commit).
    if (isTextEditing()) {
      commitSession();
      return;
    }
    const p = ctx.getPointer();
    if (!p) return;
    // Clicking an existing text node edits it; empty space places a new node.
    const hit = findTextNodeAt(ctx.store.nodes, p.x, p.y);
    if (hit) beginEditExisting(hit);
    else beginPlaceNew(p.x, p.y);
  },

  onPointerMove() {
    // no-op: text is placed on click, not dragged.
  },

  onPointerUp() {
    // no-op.
  },
};

// ---------------------------------------------------------------------------
// The React overlay
// ---------------------------------------------------------------------------

export interface TextEditingOverlayProps {
  /** Same Stage ref passed to <EditorCanvas/>. */
  stageRef: React.RefObject<Konva.Stage | null>;
}

/**
 * Renders the inline editing <textarea> while a text session is open and owns the
 * Stage `dblclick`-to-edit affordance (works from any active tool). Mount this
 * once, as a sibling of <EditorCanvas/>, inside the Stage container wrapper.
 */
export function TextEditingOverlay({ stageRef }: TextEditingOverlayProps) {
  const session = useTextEditSession();
  // Subscribe to the base image so the fit-scale view stays correct reactively.
  const baseNode = useEditorStore((s) => s.nodes[0]);
  const taRef = useRef<HTMLTextAreaElement>(null);
  const [, force] = useState(0);

  const baseW = baseNode && baseNode.type === 'image' ? baseNode.width : 0;
  const baseH = baseNode && baseNode.type === 'image' ? baseNode.height : 0;
  const view = computeView(baseW, baseH);

  // Double-click on the Stage: if it lands on a text node, open it for editing.
  useEffect(() => {
    const stage = stageRef.current;
    if (!stage) return;
    const onDbl = () => {
      const editorState = useEditorStore.getState();
      const b = editorState.nodes[0];
      const bw = b && b.type === 'image' ? b.width : 0;
      const bh = b && b.type === 'image' ? b.height : 0;
      const v = computeView(bw, bh);
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
    // stageRef is stable; the effect binds once the Stage exists. We intentionally
    // re-run when `session.nodeId` toggles so a fresh bind survives remounts.
  }, [stageRef, session.nodeId]);

  // Focus + select the textarea whenever a new session opens.
  useEffect(() => {
    if (session.nodeId === null) return;
    const ta = taRef.current;
    if (!ta) return;
    ta.focus();
    ta.select();
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
  const screenWidth = Math.max(40, session.width * view.scale);

  function finish(commit: boolean) {
    if (commit) commitSession();
    else cancelSession();
  }

  return (
    <>
      <textarea
        ref={taRef}
        value={session.value}
        spellCheck={false}
        onChange={(e) => useTextEditSession.getState().setValue(e.target.value)}
        onKeyDown={(e) => {
          // Enter commits; Shift+Enter inserts a newline; Esc cancels.
          if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            finish(true);
          } else if (e.key === 'Escape') {
            e.preventDefault();
            finish(false);
          }
          // Stop global editor shortcuts (Del/Backspace/tool keys) from firing.
          e.stopPropagation();
        }}
        onBlur={() => finish(true)}
        style={{
          position: 'fixed',
          left,
          top,
          width: screenWidth,
          minHeight: screenFontSize * 1.3,
          fontSize: screenFontSize,
          lineHeight: 1.2,
          fontFamily: FONT_FAMILY,
          color: session.fill,
          background: 'transparent',
          caretColor: session.fill,
          border: '1px dashed color-mix(in oklch, var(--color-border) 90%, transparent)',
          outline: 'none',
          padding: 0,
          margin: 0,
          resize: 'none',
          overflow: 'hidden',
          zIndex: 50,
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-word',
        }}
      />
      {/* Frosted confirm/cancel chip — sharp corners, shadcn Buttons + lucide. */}
      <div
        className="frost"
        style={{
          position: 'fixed',
          left,
          top: top - 40,
          display: 'flex',
          gap: 4,
          padding: 4,
          zIndex: 51,
        }}
        // Keep mousedown from blurring the textarea before the click handler runs.
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
