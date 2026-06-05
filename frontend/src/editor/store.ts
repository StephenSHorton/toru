// Zustand store — the single source of truth for the editor scene graph.
//
// VERBATIM PUBLIC API (per BUILD SPEC §2.2): downstream tool-builders depend on
// this `EditorState` shape verbatim. Plain Zustand: `create`, no immer, no
// middleware. Z-order == array order (index 0 == bottom == base image). History
// is a full-snapshot stack capped at HISTORY_CAP.
//
// Conventions for tool authors:
//   - Read with SELECTORS only:  useEditorStore(s => s.activeTool)
//     Never destructure the whole store in a component (re-render storm).
//   - Reach the latest snapshot imperatively from event handlers via
//     useEditorStore.getState().
//   - Each "mutator" (addNode/updateNode/deleteSelected/z-order) commits one
//     history step. In-progress drawing uses mutateDrawingNode (NO history),
//     then ONE commit() on pointer-up.

import { create } from 'zustand';
import type { EditorNode, NodeId, ToolId } from './types';

const HISTORY_CAP = 50;

/** Deep clone a node array for the history snapshot stack. */
function snapshot(nodes: EditorNode[]): EditorNode[] {
  return structuredClone(nodes);
}

export interface EditorState {
  // --- scene ---
  nodes: EditorNode[];                 // z-order = array order (index 0 = bottom = base image)
  selectedId: NodeId | null;

  // --- tool/style defaults (apply to the NEXT drawn node; recolor selected if one is selected) ---
  activeTool: ToolId;
  activeColor: string;                 // default '#ff3b30'
  activeStrokeWidth: number;           // default 4
  activeFontSize: number;              // default 28
  activeEmoji: string;                 // default '😀'

  // --- history ---
  past: EditorNode[][];
  future: EditorNode[][];

  // --- tool/style setters (no history) ---
  setTool(t: ToolId): void;
  setColor(c: string): void;           // if selectedId: recolor that node (+commit); else set default
  setStrokeWidth(w: number): void;     // same selected-vs-default semantics
  setFontSize(s: number): void;
  setEmoji(e: string): void;

  // --- selection (no history) ---
  select(id: NodeId | null): void;

  // --- mutators (each commits history) ---
  addNode(n: EditorNode): void;
  updateNode(id: NodeId, patch: Partial<EditorNode>): void;   // onDragEnd/onTransformEnd/text edit
  deleteSelected(): void;
  bringForward(id: NodeId): void;
  sendBackward(id: NodeId): void;
  bringToFront(id: NodeId): void;
  sendToBack(id: NodeId): void;

  // --- in-progress drawing (NO history; one commit() on pointer up) ---
  mutateDrawingNode(id: NodeId, patch: Partial<EditorNode>): void;
  // removeNode: delete a node by id WITHOUT a history step (discard a zero-size draft).
  removeNode(id: NodeId): void;
  // abortDraft: discard a just-added draft node AND pop the matching `past`
  // snapshot that its addNode() pushed — in one step, WITHOUT writing to `future`
  // or clearing selection. Use this (not removeNode + undo()) to cancel a draft so
  // the undo/redo stacks stay clean (undo() would push a no-op redo entry and
  // wipe selection). See tools/shapes, tools/arrowline, tools/text.
  abortDraft(id: NodeId): void;

  // --- history ops ---
  commit(): void;                      // push snapshot to past, clear future, cap HISTORY_CAP
  undo(): void;
  redo(): void;

  // --- base image lifecycle ---
  loadBaseImage(src: string, width: number, height: number): void; // resets scene to [base]
}

/** The fixed id of the base captured image (always nodes[0]). */
export const BASE_IMAGE_ID = 'base-image';

/** Push a new node list, recording the PRE-mutation state on the history stack. */
function withHistory(
  set: (partial: Partial<EditorState>) => void,
  get: () => EditorState,
  nextNodes: EditorNode[],
  extra: Partial<EditorState> = {},
): void {
  const prev = get().nodes;
  const past = [...get().past, snapshot(prev)].slice(-HISTORY_CAP);
  set({ nodes: nextNodes, past, future: [], ...extra });
}

export const useEditorStore = create<EditorState>((set, get) => ({
  nodes: [],
  selectedId: null,

  activeTool: 'select',
  activeColor: '#ff3b30',
  activeStrokeWidth: 4,
  activeFontSize: 28,
  activeEmoji: '😀',

  past: [],
  future: [],

  // --- tool/style setters ---
  setTool(t) {
    set({ activeTool: t });
  },
  setColor(c) {
    const { selectedId } = get();
    set({ activeColor: c });
    if (selectedId && selectedId !== BASE_IMAGE_ID) {
      // recolor the selected node's stroke/fill where applicable.
      const node = get().nodes.find((n) => n.id === selectedId);
      if (!node) return;
      const patch: Partial<EditorNode> = {};
      if ('stroke' in node) (patch as { stroke?: string }).stroke = c;
      if (node.type === 'text' || node.type === 'emoji') (patch as { fill?: string }).fill = c;
      if (node.type === 'arrow') (patch as { fill?: string }).fill = c;
      get().updateNode(selectedId, patch);
    }
  },
  setStrokeWidth(w) {
    const { selectedId } = get();
    set({ activeStrokeWidth: w });
    if (selectedId && selectedId !== BASE_IMAGE_ID) {
      const node = get().nodes.find((n) => n.id === selectedId);
      if (node && 'strokeWidth' in node) get().updateNode(selectedId, { strokeWidth: w } as Partial<EditorNode>);
    }
  },
  setFontSize(s) {
    const { selectedId } = get();
    set({ activeFontSize: s });
    if (selectedId && selectedId !== BASE_IMAGE_ID) {
      const node = get().nodes.find((n) => n.id === selectedId);
      if (node && (node.type === 'text' || node.type === 'emoji')) {
        get().updateNode(selectedId, { fontSize: s } as Partial<EditorNode>);
      }
    }
  },
  setEmoji(e) {
    set({ activeEmoji: e });
  },

  // --- selection ---
  select(id) {
    set({ selectedId: id });
  },

  // --- mutators (commit history) ---
  addNode(n) {
    withHistory(set, get, [...get().nodes, n]);
  },
  updateNode(id, patch) {
    const nextNodes = get().nodes.map((n) =>
      n.id === id ? ({ ...n, ...patch } as EditorNode) : n,
    );
    withHistory(set, get, nextNodes);
  },
  deleteSelected() {
    const { selectedId } = get();
    if (!selectedId || selectedId === BASE_IMAGE_ID) return;
    const nextNodes = get().nodes.filter((n) => n.id !== selectedId);
    withHistory(set, get, nextNodes, { selectedId: null });
  },

  bringForward(id) {
    const nodes = get().nodes;
    const i = nodes.findIndex((n) => n.id === id);
    // base image (index 0) never moves; annotations never go below index 1.
    if (i <= 0 || i >= nodes.length - 1) return;
    const next = nodes.slice();
    [next[i], next[i + 1]] = [next[i + 1], next[i]];
    withHistory(set, get, next);
  },
  sendBackward(id) {
    const nodes = get().nodes;
    const i = nodes.findIndex((n) => n.id === id);
    if (i <= 1) return; // already just above base, or is base
    const next = nodes.slice();
    [next[i], next[i - 1]] = [next[i - 1], next[i]];
    withHistory(set, get, next);
  },
  bringToFront(id) {
    const nodes = get().nodes;
    const i = nodes.findIndex((n) => n.id === id);
    if (i <= 0 || i === nodes.length - 1) return;
    const next = nodes.slice();
    const [node] = next.splice(i, 1);
    next.push(node);
    withHistory(set, get, next);
  },
  sendToBack(id) {
    const nodes = get().nodes;
    const i = nodes.findIndex((n) => n.id === id);
    if (i <= 1) return; // base stays at 0; annotation pinned to index 1
    const next = nodes.slice();
    const [node] = next.splice(i, 1);
    next.splice(1, 0, node);
    withHistory(set, get, next);
  },

  // --- in-progress drawing (no history) ---
  mutateDrawingNode(id, patch) {
    set({
      nodes: get().nodes.map((n) =>
        n.id === id ? ({ ...n, ...patch } as EditorNode) : n,
      ),
    });
  },
  removeNode(id) {
    set({ nodes: get().nodes.filter((n) => n.id !== id) });
  },
  abortDraft(id) {
    // Remove the draft node and pop the single `past` snapshot its addNode()
    // pushed. `future` and `selectedId` are left untouched, so cancelling a draft
    // never pollutes the redo stack or clears the user's prior selection.
    const { nodes, past } = get();
    set({
      nodes: nodes.filter((n) => n.id !== id),
      past: past.length > 0 ? past.slice(0, -1) : past,
    });
  },

  // --- history ops ---
  commit() {
    const past = [...get().past, snapshot(get().nodes)].slice(-HISTORY_CAP);
    set({ past, future: [] });
  },
  undo() {
    const { past, nodes, future } = get();
    if (past.length === 0) return;
    const prev = past[past.length - 1];
    set({
      nodes: prev,
      past: past.slice(0, -1),
      future: [snapshot(nodes), ...future].slice(0, HISTORY_CAP),
      selectedId: null,
    });
  },
  redo() {
    const { past, nodes, future } = get();
    if (future.length === 0) return;
    const next = future[0];
    set({
      nodes: next,
      past: [...past, snapshot(nodes)].slice(-HISTORY_CAP),
      future: future.slice(1),
      selectedId: null,
    });
  },

  // --- base image lifecycle ---
  loadBaseImage(src, width, height) {
    const base: EditorNode = {
      id: BASE_IMAGE_ID,
      type: 'image',
      x: 0,
      y: 0,
      rotation: 0,
      opacity: 1,
      draggable: false,
      src,
      width,
      height,
      scaleX: 1,
      scaleY: 1,
    };
    set({ nodes: [base], selectedId: null, past: [], future: [] });
  },
}));
