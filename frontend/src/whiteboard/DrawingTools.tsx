// The drawing bar's tools on the board itself (DrawingBar.tsx picks them):
// everything but the pen and highlighter, which are ink.ts's primitive.
//
// - <ToolSurface> lies over the board while the hand, the eraser or a shape
//   tool is on, and takes the presses: the hand pans; the eraser rubs out
//   every stroke and shape it touches, as one change when it lifts; a shape
//   tool drags out a shape (Shift for a square, circle or 45° line); text
//   opens a label to type; emoji puts the chosen emoji down.
// - <ToolLayer> sits inside the board's world and shows what's being drawn:
//   the shape being dragged, a shape being reshaped, the label being typed,
//   and the handles of the one shape selected, whose corners or ends drag.
//
// The two share a small store (`createTools`), so a pointer move redraws
// only them, not the board. Every finished change goes to the board as one
// change (`addBlock`, `replaceBlocks`, `deleteBlocks`), so it saves, syncs and
// undoes as one step.
import * as React from "react";
import { useEffect, useRef, useSyncExternalStore } from "react";
import type { BoardBlock, BoardBlocks } from "./board";
import { emojiSize, textSize, type DrawingSettings } from "./drawing";
import { inkColor, strokeId, type InkBlock, type InkStroke, type ShapeKind } from "./ink";
import { InkLine } from "./InkView";
import { constrain, inkDistance, isBoxKind, reshape, shapeBlock, shapeEnds, shapeHandles, textBox, type HandleName, type Point } from "./shapes";

/** What the board's api gives the tools (board.ts). */
type ToolsBoard = {
  blocks(): BoardBlocks;
  selected(): string[];
  addBlock(id: string, box: BoardBlock): boolean;
  replaceBlocks(replace: BoardBlocks): boolean;
  deleteBlocks(ids: string[]): boolean;
  select(ids: string[]): boolean;
  viewport: {
    toCanvas(x: number, y: number): Point;
    panBy(dx: number, dy: number): boolean;
    get(): { x: number; y: number; zoom: number };
  };
};

/** A text label being typed: where, at what size, and the label it changes, if any. */
type TextEdit = { at: Point; size: number; text: string; id?: string; color: string };

type ToolsState = {
  /** The shape being drawn or reshaped, in canvas units. */
  draft: InkBlock | null;
  /** The shape being reshaped, hidden while its draft shows. */
  reshaping: string | null;
  /** The label being typed. */
  editing: TextEdit | null;
  /** The eraser's ring, in client pixels, while the eraser is over the board. */
  eraser: Point | null;
};

type Tools = ReturnType<typeof createTools>;

/** The tools' shared store. One per board. */
function createTools() {
  let state: ToolsState = { draft: null, reshaping: null, editing: null, eraser: null };
  const subs = new Set<() => void>();
  return {
    get: () => state,
    set(change: Partial<ToolsState>) {
      state = { ...state, ...change };
      for (const f of subs) f();
    },
    subscribe(f: () => void) {
      subs.add(f);
      return () => void subs.delete(f);
    },
  };
}

function useTools(tools: Tools) {
  return useSyncExternalStore(tools.subscribe, tools.get, tools.get);
}

/** Whether a tool draws through the tool surface. The pen and highlighter have their own; select has none. */
const surfaceTools = new Set(["hand", "eraser", "rect", "ellipse", "line", "arrow", "text", "emoji"]);

/** The shortest drag, in canvas units, that makes a shape. Shorter is a click and draws nothing. */
const minDrag = 3;

const isInk = (b: BoardBlock | undefined): b is BoardBlock & { ink: InkStroke } => !!b?.ink;

/** Marks a block's element while a tool works on it (erasing, reshaping), without redrawing the board. */
function markBlock(root: Element | null, id: string, attr: string, on: boolean) {
  const el = root?.querySelector(`[data-block="${CSS.escape(id)}"]`);
  if (on) el?.setAttribute(attr, "");
  else el?.removeAttribute(attr);
}

/** The colour, width and fill a new shape takes from the drawing bar. */
function newStyle(settings: DrawingSettings, kind: ShapeKind) {
  return {
    color: settings.color,
    width: settings.widths.shape,
    fill: settings.fill && (kind === "rect" || kind === "ellipse") ? settings.color : undefined,
  };
}

/** Commits a text label: a new one, or new words for the one being changed. Empty text removes nothing and adds nothing. */
function commitText(board: ToolsBoard, edit: TextEdit, width: number) {
  const text = edit.text.replace(/\s+$/, "");
  if (!text.trim()) return;
  const box = textBox(text, edit.size);
  const block = shapeBlock("text", edit.at, { x: edit.at.x + box.w, y: edit.at.y + box.h }, { color: edit.color, width, text });
  if (edit.id) {
    const was = board.blocks()[edit.id];
    if (was) board.replaceBlocks({ [edit.id]: { ...was, ...block } });
  } else {
    board.addBlock(strokeId(), block);
  }
}

/**
 * Lies over the board while the hand, the eraser or a shape tool is on.
 * Touch still pans and pinches the board under it, as with the pen.
 */
function ToolSurface({ board, tools, settings }: { board: ToolsBoard; tools: Tools; settings: DrawingSettings }) {
  const ref = useRef<HTMLDivElement>(null);
  const settingsRef = useRef(settings);
  settingsRef.current = settings;
  const active = surfaceTools.has(settings.tool);
  const { eraser } = useTools(tools);

  // A tool change drops a drag in flight.
  useEffect(() => () => tools.set({ draft: null, eraser: null }), [settings.tool, tools]);

  const pointerdown = (event: React.PointerEvent<HTMLDivElement>) => {
    if (event.button !== 0 || event.pointerType === "touch") return;
    const el = ref.current;
    if (!el) return;
    const s = settingsRef.current;
    const tool = s.tool;
    event.stopPropagation();
    event.preventDefault();
    // A label being typed is finished by a press elsewhere.
    const editing = tools.get().editing;
    if (editing) {
      commitText(board, editing, s.widths.shape);
      tools.set({ editing: null });
      if (tool === "text") return;
    }
    const start = board.viewport.toCanvas(event.clientX, event.clientY);
    const root = el.closest('[data-slot="board"]');

    if (tool === "text") {
      tools.set({ editing: { at: start, size: textSize(s.widths.shape), text: "", color: s.color } });
      return;
    }
    if (tool === "emoji") {
      const size = emojiSize(s.widths.shape);
      const a = { x: start.x - size / 2, y: start.y - size / 2 };
      board.addBlock(strokeId(), shapeBlock("emoji", a, { x: a.x + size, y: a.y + size }, { color: s.color, width: s.widths.shape, text: s.emoji }));
      return;
    }

    el.setPointerCapture?.(event.pointerId);
    let last = { x: event.clientX, y: event.clientY };
    const erased = new Set<string>();
    const erase = (clientX: number, clientY: number) => {
      const p = board.viewport.toCanvas(clientX, clientY);
      const reach = s.widths.eraser / 2 / (board.viewport.get().zoom || 1);
      for (const [id, b] of Object.entries(board.blocks())) {
        // A locked stroke stays (whiteboard#29).
        if (!isInk(b) || b.locked || erased.has(id)) continue;
        // Its box first: most strokes are nowhere near.
        if (p.x < b.x - reach || p.y < b.y - reach || p.x > b.x + b.width + reach || p.y > b.y + b.height + reach) continue;
        if (inkDistance(b, p) <= reach) {
          erased.add(id);
          markBlock(root, id, "data-erasing", true);
        }
      }
    };
    if (tool === "eraser") erase(event.clientX, event.clientY);

    const move = (e: PointerEvent) => {
      if (tool === "hand") {
        board.viewport.panBy(e.clientX - last.x, e.clientY - last.y);
        last = { x: e.clientX, y: e.clientY };
      } else if (tool === "eraser") {
        tools.set({ eraser: { x: e.clientX, y: e.clientY } });
        const events = e.getCoalescedEvents?.() ?? [];
        for (const c of events.length ? events : [e]) erase(c.clientX, c.clientY);
      } else {
        const kind = tool as ShapeKind;
        const end = constrain(kind, start, board.viewport.toCanvas(e.clientX, e.clientY), e.shiftKey);
        tools.set({ draft: Math.hypot(end.x - start.x, end.y - start.y) < minDrag ? null : shapeBlock(kind, start, end, newStyle(s, kind)) });
      }
    };
    const done = (e: PointerEvent, canceled: boolean) => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", cancel);
      window.removeEventListener("keydown", key, true);
      if (tool === "eraser") {
        for (const id of erased) markBlock(root, id, "data-erasing", false);
        if (!canceled && erased.size) board.deleteBlocks([...erased]);
      } else if (tool !== "hand") {
        const draft = tools.get().draft;
        tools.set({ draft: null });
        if (!canceled && draft) {
          const kind = tool as ShapeKind;
          const end = constrain(kind, start, board.viewport.toCanvas(e.clientX, e.clientY), e.shiftKey);
          if (Math.hypot(end.x - start.x, end.y - start.y) >= minDrag) board.addBlock(strokeId(), shapeBlock(kind, start, end, newStyle(s, kind)));
        }
      }
    };
    const up = (e: PointerEvent) => done(e, false);
    const cancel = (e: PointerEvent) => done(e, true);
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        done(new PointerEvent("pointercancel"), true);
      }
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    window.addEventListener("pointercancel", cancel);
    window.addEventListener("keydown", key, true);
  };

  const size = settings.widths.eraser;
  return (
    <>
      <div
        ref={ref}
        data-slot="tool-surface"
        data-tool={settings.tool}
        data-active={active ? "" : undefined}
        hidden={!active}
        aria-hidden="true"
        // A press the surface leaves alone, like touch, pans the board under it.
        data-board-empty=""
        style={{ position: "absolute", inset: 0, touchAction: "none" }}
        // In the capture phase, so the press is the tool's before the
        // board's own handler (on its root) can start a pan or clear the
        // selection. A press the tool leaves alone, like touch, goes on.
        onPointerDownCapture={pointerdown}
        onPointerMove={(e) => settings.tool === "eraser" && e.pointerType !== "touch" && tools.set({ eraser: { x: e.clientX, y: e.clientY } })}
        onPointerLeave={() => settings.tool === "eraser" && tools.set({ eraser: null })}
      />
      {active && settings.tool === "eraser" && eraser && (
        <div
          data-slot="eraser-ring"
          aria-hidden="true"
          style={{ position: "fixed", left: eraser.x - size / 2, top: eraser.y - size / 2, width: size, height: size }}
        />
      )}
    </>
  );
}

/**
 * Inside the board's world: the shape being drawn or reshaped, the label
 * being typed, and the handles of the one shape selected (with the select
 * tool), which drag its corners or ends.
 */
function ToolLayer({
  board,
  tools,
  settings,
  selection,
  blocks,
}: {
  board: ToolsBoard;
  tools: Tools;
  settings: DrawingSettings;
  /** The board's store, to follow its selection without redrawing the board. */
  selection: { get(): { selected: string[] }; subscribe(listener: () => void): () => void };
  blocks: BoardBlocks;
}) {
  const { draft, editing, reshaping } = useTools(tools);
  const read = () => selection.get().selected;
  const selected = useSyncExternalStore(selection.subscribe, read, read);
  const only = selected.length === 1 ? blocks[selected[0]] : undefined;
  // A locked shape gets no handles: it doesn't move or reshape (whiteboard#29).
  const shape = settings.tool === "select" && isInk(only) && only.ink.kind && !only.locked ? { id: selected[0], block: only } : null;
  return (
    <>
      {draft && (
        <div data-slot="tool-draft" style={{ position: "absolute", left: draft.x, top: draft.y, width: draft.width, height: draft.height, pointerEvents: "none" }}>
          <InkLine stroke={draft.ink} width={draft.width} height={draft.height} />
        </div>
      )}
      {shape && reshaping !== shape.id && <ShapeHandles board={board} tools={tools} id={shape.id} block={shape.block} />}
      {editing && (
        <TextEditor
          edit={editing}
          onChange={(text) => tools.set({ editing: { ...editing, text } })}
          onDone={(keep) => {
            const edit = tools.get().editing;
            tools.set({ editing: null });
            if (keep && edit) commitText(board, edit, settings.widths.shape);
          }}
        />
      )}
    </>
  );
}

/** The handles on a selected shape: its four corners, or a line's two ends. */
function ShapeHandles({ board, tools, id, block }: { board: ToolsBoard; tools: Tools; id: string; block: BoardBlock & { ink: InkStroke } }) {
  const zoom = board.viewport.get().zoom || 1;
  const size = 10 / zoom;
  const drag = (name: HandleName) => (event: React.PointerEvent<HTMLSpanElement>) => {
    if (event.button !== 0) return;
    event.stopPropagation();
    event.preventDefault();
    const root = event.currentTarget.closest('[data-slot="board"]');
    const was = block;
    const at = (e: PointerEvent) => reshape(was, name, board.viewport.toCanvas(e.clientX, e.clientY), e.shiftKey);
    markBlock(root, id, "data-reshaping", true);
    tools.set({ reshaping: id, draft: at(event.nativeEvent) });
    const move = (e: PointerEvent) => tools.set({ draft: at(e) });
    const end = (e: PointerEvent, keep: boolean) => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", cancel);
      markBlock(root, id, "data-reshaping", false);
      const next = at(e);
      tools.set({ reshaping: null, draft: null });
      if (keep) board.replaceBlocks({ [id]: { ...was, ...next } });
    };
    const up = (e: PointerEvent) => end(e, true);
    const cancel = (e: PointerEvent) => end(e, false);
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    window.addEventListener("pointercancel", cancel);
  };
  const names: Record<HandleName, string> = { start: "start", end: "end", nw: "top left corner", ne: "top right corner", se: "bottom right corner", sw: "bottom left corner" };
  const line = !isBoxKind(block.ink.kind!);
  return (
    <>
      {shapeHandles(block).map((h) => (
        <span
          key={h.name}
          data-slot="shape-handle"
          data-handle={h.name}
          data-round={line ? "" : undefined}
          title={`Drag the ${names[h.name]}${line ? " (Shift for 45°)" : " (Shift keeps it square)"}`}
          style={{ position: "absolute", left: h.at.x - size / 2, top: h.at.y - size / 2, width: size, height: size, borderWidth: 1.5 / zoom }}
          onPointerDownCapture={drag(h.name)}
        />
      ))}
    </>
  );
}

/** The label being typed, where it will sit on the board. Enter keeps it, Shift+Enter is a new line, Escape drops it. */
function TextEditor({ edit, onChange, onDone }: { edit: TextEdit; onChange: (text: string) => void; onDone: (keep: boolean) => void }) {
  const ref = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.focus();
    el.select();
  }, []);
  const lines = edit.text.split("\n");
  const longest = Math.max(4, ...lines.map((l) => [...l].length));
  return (
    <textarea
      ref={ref}
      data-slot="shape-text-editor"
      aria-label="Text label"
      value={edit.text}
      rows={lines.length}
      cols={longest + 1}
      spellCheck={false}
      style={{
        position: "absolute",
        left: edit.at.x,
        top: edit.at.y,
        fontSize: edit.size,
        lineHeight: 1.25,
        color: inkColor(edit.color),
      }}
      onChange={(e) => onChange(e.target.value)}
      onPointerDown={(e) => e.stopPropagation()}
      onKeyDown={(e) => {
        e.stopPropagation();
        if (e.key === "Escape") onDone(false);
        else if (e.key === "Enter" && !e.shiftKey) {
          e.preventDefault();
          onDone(true);
        }
      }}
      onBlur={() => onDone(true)}
    />
  );
}

/** Opens a text label to change its words, as a double click on it does with the select tool. */
function editText(tools: Tools, id: string, block: BoardBlock & { ink: InkStroke }) {
  const [a, b] = shapeEnds(block);
  const lines = (block.ink.text ?? "").split("\n").length;
  const top = { x: Math.min(a.x, b.x), y: Math.min(a.y, b.y) };
  const size = Math.abs(b.y - a.y) / (lines * 1.25);
  tools.set({ editing: { at: top, size, text: block.ink.text ?? "", id, color: block.ink.color } });
}

export { createTools, editText, ToolLayer, ToolSurface, type Tools, type ToolsBoard };
