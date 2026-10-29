import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Scissors, Trash2, ZoomIn, ZoomOut, Type } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import {
  locate,
  move,
  outputDuration,
  remove,
  removeText,
  setEdge,
  split,
  upsertText,
  type Edl,
  type Seg,
  type Text,
} from "@/trim/edl";

// EditTimeline is the video editor's timeline: the EDL's segments back to back
// in OUTPUT time (coloured by source), a text row above them, a ruler and the
// playhead. It owns no video and no EDL: the window passes the EDL and the
// playhead in and gets edits back through `apply` (useEdlHistory's apply, so
// every edit is one undo step; drags pass a key so they merge into one).
//
// Mouse: click a segment or text to select it and seek there; click the ruler
// or empty track to seek. Drag a segment sideways to reorder it, drag its
// edges to trim it (ripple: later segments move with it), drag a text bar to
// move it or its ends to retime it. Ctrl+wheel zooms around the pointer.
// Keys (ignored while typing): S split at the playhead, Delete/Backspace the
// selection, Ctrl+Z / Ctrl+Shift+Z (or Ctrl+Y) undo/redo, +/- zoom.

export type Selection = { kind: "seg" | "text"; id: string } | null;

export interface EditTimelineProps {
  edl: Edl;
  outTime: number; // playhead, output seconds
  onSeek: (outT: number) => void;
  apply: (fn: (e: Edl) => Edl, key?: string) => void;
  selection: Selection;
  onSelect: (s: Selection) => void;
  onUndo?: () => void;
  onRedo?: () => void;
  canUndo?: boolean;
  canRedo?: boolean;
  onAddText?: () => void; // the window makes the text (it knows the player)
  onEditText?: (id: string) => void; // double-click a text bar
  keyboard?: boolean; // default true; false when a dialog owns the keys
  className?: string;
}

// One colour per source (cycled). Bars are tinted, with a solid top edge.
const SOURCE_COLORS = ["#4f8fe8", "#a970e8", "#2fb8a3", "#e3a03a", "#e0609d", "#86b84a"];
const MIN_ZOOM = 1;
const MAX_ZOOM = 40;
// A press that moves less than this (px) is a click, not a drag.
const DRAG_PX = 4;

let dragSeq = 0;

export function EditTimeline({
  edl,
  outTime,
  onSeek,
  apply,
  selection,
  onSelect,
  onUndo,
  onRedo,
  canUndo,
  canRedo,
  onAddText,
  onEditText,
  keyboard = true,
  className,
}: EditTimelineProps) {
  const duration = outputDuration(edl);
  const scrollRef = useRef<HTMLDivElement>(null);
  const [zoom, setZoom] = useState(1);
  const [viewW, setViewW] = useState(0);
  // Reorder drag in progress: which segment, where it would land, pointer x.
  const [reorder, setReorder] = useState<{ id: string; to: number; x: number } | null>(null);

  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setViewW(el.clientWidth));
    ro.observe(el);
    setViewW(el.clientWidth);
    return () => ro.disconnect();
  }, []);

  const contentW = Math.max(1, viewW * zoom);
  const pxPerSec = duration > 0 ? contentW / duration : 0;
  const x = (t: number) => t * pxPerSec;

  // Segment layout in output time.
  const layout: { seg: Seg; at: number; len: number }[] = [];
  {
    let at = 0;
    for (const seg of edl.segs) {
      const len = seg.end - seg.start;
      layout.push({ seg, at, len });
      at += len;
    }
  }

  const timeAt = (clientX: number) => {
    const el = scrollRef.current;
    if (!el || pxPerSec <= 0) return 0;
    const r = el.getBoundingClientRect();
    return Math.max(0, Math.min(duration, (clientX - r.left + el.scrollLeft) / pxPerSec));
  };

  // Zoom keeping the time under `anchorX` (client px; default the playhead)
  // in place on screen. zoomVal and the anchor are refs, not state, so a burst
  // of wheel events before the next render compounds instead of repeating
  // one step; the scroll is applied after the wider content has rendered.
  const zoomVal = useRef(zoom);
  const anchor = useRef<{ t: number; offset: number } | null>(null);
  const zoomTo = (z: number, anchorX?: number) => {
    const el = scrollRef.current;
    const next = Math.max(MIN_ZOOM, Math.min(MAX_ZOOM, z));
    if (next === zoomVal.current) return;
    if (el && duration > 0 && viewW > 0) {
      const r = el.getBoundingClientRect();
      const pps = (viewW * zoomVal.current) / duration;
      const ax = anchorX ?? Math.max(r.left, Math.min(r.right, r.left + outTime * pps - el.scrollLeft));
      anchor.current ??= { t: (ax - r.left + el.scrollLeft) / pps, offset: ax - r.left };
    }
    zoomVal.current = next;
    setZoom(next);
  };
  useLayoutEffect(() => {
    const el = scrollRef.current;
    const a = anchor.current;
    anchor.current = null;
    if (el && zoom <= MIN_ZOOM) el.scrollLeft = 0;
    else if (el && a && duration > 0) el.scrollLeft = Math.max(0, a.t * pxPerSec - a.offset);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [zoom]);

  // Ctrl+wheel zooms (non-passive so it can stop the page zoom).
  const zoomRef = useRef(zoomTo);
  zoomRef.current = zoomTo;
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      if (!e.ctrlKey) return;
      e.preventDefault();
      zoomRef.current(zoomVal.current * (e.deltaY < 0 ? 1.25 : 0.8), e.clientX);
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, []);

  // Keep the playhead in view when it moves off the visible part (not on zoom:
  // a wheel zoom stays anchored under the pointer).
  useEffect(() => {
    const el = scrollRef.current;
    if (!el || zoom <= 1) return;
    const px = x(outTime);
    if (px < el.scrollLeft || px > el.scrollLeft + el.clientWidth - 8) el.scrollLeft = Math.max(0, px - el.clientWidth * 0.1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [outTime]);

  const doSplit = () => {
    apply((e) => split(e, outTime));
  };
  const doDelete = () => {
    if (!selection) return;
    if (selection.kind === "seg") {
      if (edl.segs.length <= 1) return;
      // Select the neighbour that slides into its place, so Delete repeats.
      const i = edl.segs.findIndex((s) => s.id === selection.id);
      const next = edl.segs[i + 1] ?? edl.segs[i - 1];
      apply((e) => remove(e, selection.id));
      onSelect(next ? { kind: "seg", id: next.id } : null);
    } else {
      apply((e) => removeText(e, selection.id));
      onSelect(null);
    }
  };
  const canSplit = (() => {
    const hit = locate(edl, outTime);
    return !!hit && split(edl, outTime) !== edl;
  })();
  const canDelete = selection?.kind === "text" || (selection?.kind === "seg" && edl.segs.length > 1);

  // Keyboard. No dependency array: the handler must see the current props.
  useEffect(() => {
    if (!keyboard) return;
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.isContentEditable)) return;
      const mod = e.ctrlKey || e.metaKey;
      if (mod && (e.key === "z" || e.key === "Z")) {
        e.preventDefault();
        if (e.shiftKey) onRedo?.();
        else onUndo?.();
        return;
      }
      if (mod && (e.key === "y" || e.key === "Y")) {
        e.preventDefault();
        onRedo?.();
        return;
      }
      if (mod || e.altKey) return;
      switch (e.key) {
        case "s":
        case "S":
          e.preventDefault();
          doSplit();
          break;
        case "Delete":
        case "Backspace":
          if (selection) {
            e.preventDefault();
            doDelete();
          }
          break;
        case "+":
        case "=":
          zoomTo(zoomVal.current * 1.5);
          break;
        case "-":
          zoomTo(zoomVal.current / 1.5);
          break;
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  // --- drags ---------------------------------------------------------------
  // Each drag is pointer-captured on the element it started on (like Trim's
  // Handle) and computes from the values at pointerdown, never the live EDL,
  // so ripple layout changes mid-drag don't feed back into it.

  const startSegDrag = (e: React.PointerEvent, seg: Seg) => {
    if (e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation();
    const el = e.currentTarget as HTMLElement;
    el.setPointerCapture(e.pointerId);
    const x0 = e.clientX;
    let moved = false;
    onSelect({ kind: "seg", id: seg.id });
    // The slot it lands in: before the first other segment whose middle (as
    // laid out now) is right of the pointer. That is its index for move().
    const target = (clientX: number) => {
      const t = timeAt(clientX);
      return layout.filter((l) => l.seg.id !== seg.id && l.at + l.len / 2 < t).length;
    };
    const onMove = (ev: PointerEvent) => {
      if (!moved && Math.abs(ev.clientX - x0) < DRAG_PX) return;
      moved = true;
      setReorder({ id: seg.id, to: target(ev.clientX), x: ev.clientX });
    };
    const end = (ev: PointerEvent, commit: boolean) => {
      el.removeEventListener("pointermove", onMove);
      el.removeEventListener("pointerup", onUp);
      el.removeEventListener("lostpointercapture", onLost);
      setReorder(null);
      if (!moved) {
        if (commit) onSeek(timeAt(ev.clientX));
        return;
      }
      if (commit) apply((e2) => move(e2, seg.id, target(ev.clientX)));
    };
    const onUp = (ev: PointerEvent) => end(ev, true);
    const onLost = (ev: PointerEvent) => end(ev, false);
    el.addEventListener("pointermove", onMove);
    el.addEventListener("pointerup", onUp);
    el.addEventListener("lostpointercapture", onLost);
  };

  const startEdgeDrag = (e: React.PointerEvent, seg: Seg, edge: "start" | "end") => {
    if (e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation();
    const el = e.currentTarget as HTMLElement;
    el.setPointerCapture(e.pointerId);
    onSelect({ kind: "seg", id: seg.id });
    const x0 = e.clientX;
    const base = edge === "start" ? seg.start : seg.end;
    const pps = pxPerSec;
    const key = `edge:${seg.id}:${++dragSeq}`;
    const onMove = (ev: PointerEvent) => {
      if (pps <= 0) return;
      const t = Math.round((base + (ev.clientX - x0) / pps) * 100) / 100;
      apply((e2) => setEdge(e2, seg.id, edge, t), key);
    };
    const end = () => {
      el.removeEventListener("pointermove", onMove);
      el.removeEventListener("pointerup", end);
      el.removeEventListener("lostpointercapture", end);
    };
    el.addEventListener("pointermove", onMove);
    el.addEventListener("pointerup", end);
    el.addEventListener("lostpointercapture", end);
  };

  const startTextDrag = (e: React.PointerEvent, t: Text, part: "body" | "start" | "end") => {
    if (e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation();
    const el = e.currentTarget as HTMLElement;
    el.setPointerCapture(e.pointerId);
    onSelect({ kind: "text", id: t.id });
    const x0 = e.clientX;
    const pps = pxPerSec;
    const len = t.end - t.start;
    const key = `text:${t.id}:${++dragSeq}`;
    let moved = false;
    const onMove = (ev: PointerEvent) => {
      if (pps <= 0) return;
      if (!moved && Math.abs(ev.clientX - x0) < DRAG_PX) return;
      moved = true;
      const d = (ev.clientX - x0) / pps;
      const r2 = (n: number) => Math.round(n * 100) / 100;
      let next: Text;
      if (part === "body") {
        const start = r2(Math.max(0, Math.min(duration - len, t.start + d)));
        next = { ...t, start, end: start + len };
      } else if (part === "start") next = { ...t, start: r2(Math.max(0, Math.min(t.end - 0.1, t.start + d))) };
      else next = { ...t, end: r2(Math.min(duration, Math.max(t.start + 0.1, t.end + d))) };
      apply((e2) => upsertText(e2, next), key);
    };
    const end = (ev: PointerEvent) => {
      el.removeEventListener("pointermove", onMove);
      el.removeEventListener("pointerup", onUp);
      el.removeEventListener("lostpointercapture", onLost);
      if (!moved && ev.type === "pointerup" && part === "body") onSeek(t.start);
    };
    const onUp = (ev: PointerEvent) => end(ev);
    const onLost = (ev: PointerEvent) => end(ev);
    el.addEventListener("pointermove", onMove);
    el.addEventListener("pointerup", onUp);
    el.addEventListener("lostpointercapture", onLost);
  };

  // Seek by dragging on the ruler or an empty part of the track.
  const startScrub = (e: React.PointerEvent) => {
    if (e.button !== 0 || duration <= 0) return;
    const el = e.currentTarget as HTMLElement;
    el.setPointerCapture(e.pointerId);
    onSeek(timeAt(e.clientX));
    const onMove = (ev: PointerEvent) => onSeek(timeAt(ev.clientX));
    const end = () => {
      el.removeEventListener("pointermove", onMove);
      el.removeEventListener("pointerup", end);
      el.removeEventListener("lostpointercapture", end);
    };
    el.addEventListener("pointermove", onMove);
    el.addEventListener("pointerup", end);
    el.addEventListener("lostpointercapture", end);
  };

  // Text rows: overlapping texts stack into lanes.
  const lanes: Text[][] = [];
  for (const t of [...edl.texts].sort((a, b) => a.start - b.start)) {
    const lane = lanes.find((l) => l[l.length - 1].end <= t.start);
    if (lane) lane.push(t);
    else lanes.push([t]);
  }
  const textRows = Math.max(1, lanes.length);

  const dragged = reorder ? layout.find((l) => l.seg.id === reorder.id) : undefined;
  // Insertion marker for a reorder, on the current layout: the left edge of the
  // segment it lands before, or the end of the last one.
  const markerX = (() => {
    if (!reorder) return 0;
    const others = layout.filter((l) => l.seg.id !== reorder.id);
    const before = others[reorder.to];
    if (before) return x(before.at);
    const last = others[others.length - 1];
    return last ? x(last.at + last.len) : 0;
  })();

  return (
    <div className={cn("flex flex-col gap-2", className)}>
      <div className="flex items-center gap-1">
        <Button size="sm" variant="ghost" disabled={!canSplit} onClick={doSplit} title="Split at the playhead (S)" aria-label="Split at the playhead">
          <Scissors /> Split
        </Button>
        <Button
          size="sm"
          variant="ghost"
          disabled={!canDelete}
          onClick={doDelete}
          title={selection?.kind === "text" ? "Delete the selected text (Delete)" : "Cut the selected part out (Delete)"}
          aria-label="Delete selection"
        >
          <Trash2 /> Delete
        </Button>
        {onAddText ? (
          <Button size="sm" variant="ghost" onClick={onAddText} title="Add text at the playhead" aria-label="Add text">
            <Type /> Text
          </Button>
        ) : null}
        {onUndo || onRedo ? <div className="mx-1 h-5 w-px bg-border" /> : null}
        {onUndo ? (
          <Button size="sm" variant="ghost" disabled={!canUndo} onClick={onUndo} title="Undo (Ctrl+Z)">
            Undo
          </Button>
        ) : null}
        {onRedo ? (
          <Button size="sm" variant="ghost" disabled={!canRedo} onClick={onRedo} title="Redo (Ctrl+Shift+Z)">
            Redo
          </Button>
        ) : null}
        <span className="ml-2 whitespace-nowrap text-xs tabular-nums text-muted-foreground">
          {fmtTime(outTime)} / {fmtTime(duration)}
        </span>
        <div className="ml-auto flex items-center gap-1">
          <Button size="icon" variant="ghost" disabled={zoom <= MIN_ZOOM} onClick={() => zoomTo(zoomVal.current / 1.5)} title="Zoom out (-)" aria-label="Zoom out">
            <ZoomOut />
          </Button>
          <input
            type="range"
            min={0}
            max={1}
            step={0.01}
            value={Math.log(zoom) / Math.log(MAX_ZOOM)}
            onChange={(e) => zoomTo(Math.pow(MAX_ZOOM, Number(e.target.value)))}
            className="w-24 accent-primary"
            title="Zoom (Ctrl+wheel on the timeline)"
            aria-label="Zoom"
          />
          <Button size="icon" variant="ghost" disabled={zoom >= MAX_ZOOM} onClick={() => zoomTo(zoomVal.current * 1.5)} title="Zoom in (+)" aria-label="Zoom in">
            <ZoomIn />
          </Button>
        </div>
      </div>

      <div
        ref={scrollRef}
        className={cn("relative w-full overflow-y-hidden border bg-card/40", zoom > 1 ? "overflow-x-auto" : "overflow-x-hidden")}
      >
        <div className="relative" style={{ width: contentW }}>
          {/* ruler */}
          <div className="relative h-5 cursor-pointer select-none overflow-hidden border-b border-border/60" onPointerDown={startScrub}>
            {ticks(duration, pxPerSec).map(({ t, major }) => (
              <div key={t} className="pointer-events-none absolute bottom-0" style={{ left: x(t) }}>
                <div className={cn("w-px bg-foreground/30", major ? "h-2.5" : "h-1.5")} />
                {major ? (
                  <span className="absolute bottom-2.5 left-1 text-[10px] tabular-nums text-muted-foreground">{fmtTick(t)}</span>
                ) : null}
              </div>
            ))}
          </div>

          {/* text row(s) */}
          <div className="relative border-b border-border/60" style={{ height: textRows * 22 + 4 }} onPointerDown={startScrub}>
            {lanes.map((lane, li) =>
              lane.map((t) => {
                const sel = selection?.kind === "text" && selection.id === t.id;
                return (
                  <div
                    key={t.id}
                    className={cn(
                      "absolute flex h-[18px] cursor-grab touch-none items-center overflow-hidden border bg-foreground/15 text-[11px] text-foreground active:cursor-grabbing",
                      sel ? "border-primary bg-primary/30" : "border-foreground/30",
                    )}
                    style={{ left: x(t.start), width: Math.max(4, x(t.end) - x(t.start)), top: 2 + li * 22 }}
                    title={`${t.text || "(empty)"}: ${fmtTime(t.start)} – ${fmtTime(t.end)}. Drag to move, drag the ends to retime, double-click to edit.`}
                    onPointerDown={(e) => startTextDrag(e, t, "body")}
                    onDoubleClick={() => onEditText?.(t.id)}
                  >
                    <div className="absolute inset-y-0 left-0 w-2 cursor-ew-resize" onPointerDown={(e) => startTextDrag(e, t, "start")} />
                    <span className="pointer-events-none truncate px-2">
                      <Type className="mr-1 inline size-3 align-[-2px] opacity-70" />
                      {t.text || "(empty)"}
                    </span>
                    <div className="absolute inset-y-0 right-0 w-2 cursor-ew-resize" onPointerDown={(e) => startTextDrag(e, t, "end")} />
                  </div>
                );
              }),
            )}
          </div>

          {/* segments */}
          <div className="relative h-14" onPointerDown={startScrub}>
            {layout.map(({ seg, at, len }) => {
              const sel = selection?.kind === "seg" && selection.id === seg.id;
              const color = SOURCE_COLORS[seg.source % SOURCE_COLORS.length];
              const w = x(len);
              const src = edl.sources[seg.source];
              return (
                <div
                  key={seg.id}
                  className={cn(
                    "absolute inset-y-0 cursor-grab touch-none overflow-hidden border-r border-background active:cursor-grabbing",
                    sel && "z-10 outline-2 -outline-offset-2 outline-primary",
                    reorder?.id === seg.id && "opacity-40",
                  )}
                  style={{ left: x(at), width: w, background: `${color}40`, borderTop: `3px solid ${color}` }}
                  title={`${baseName(src?.path ?? "")} ${fmtTime(seg.start)} – ${fmtTime(seg.end)} (${fmtTime(len)}). Drag to reorder, drag the edges to trim.`}
                  onPointerDown={(e) => startSegDrag(e, seg)}
                >
                  {w > 40 ? (
                    <div className="pointer-events-none truncate px-2 pt-1 text-[11px] text-foreground/80">
                      {baseName(src?.path ?? "")}
                      <span className="ml-1.5 tabular-nums opacity-60">{fmtTime(len)}</span>
                    </div>
                  ) : null}
                  <div
                    className="group/edge absolute inset-y-0 left-0 flex w-2 cursor-ew-resize justify-start"
                    onPointerDown={(e) => startEdgeDrag(e, seg, "start")}
                    title="Drag to trim the start"
                  >
                    <div className={cn("h-full w-1 bg-primary/0 group-hover/edge:bg-primary", sel && "bg-primary/70")} />
                  </div>
                  <div
                    className="group/edge absolute inset-y-0 right-0 flex w-2 cursor-ew-resize justify-end"
                    onPointerDown={(e) => startEdgeDrag(e, seg, "end")}
                    title="Drag to trim the end"
                  >
                    <div className={cn("h-full w-1 bg-primary/0 group-hover/edge:bg-primary", sel && "bg-primary/70")} />
                  </div>
                </div>
              );
            })}
            {/* reorder: the drop slot and a ghost under the pointer */}
            {reorder && dragged ? (
              <>
                <div className="pointer-events-none absolute inset-y-0 z-20 w-0.5 -translate-x-1/2 bg-primary" style={{ left: markerX }} />
                <div
                  className="pointer-events-none absolute inset-y-1 z-20 border border-primary bg-primary/20"
                  style={{
                    left: reorder.x - (scrollRef.current?.getBoundingClientRect().left ?? 0) + (scrollRef.current?.scrollLeft ?? 0) - Math.min(x(dragged.len), 120) / 2,
                    width: Math.min(x(dragged.len), 120),
                  }}
                />
              </>
            ) : null}
          </div>

          {/* playhead across all rows */}
          {duration > 0 ? (
            <div className="pointer-events-none absolute inset-y-0 z-30 w-px bg-foreground/90" style={{ left: x(outTime) }}>
              <div className="absolute -left-1 top-0 size-2 rotate-45 bg-foreground/90" />
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}

// Ruler ticks: a "nice" major step that leaves ~90px between labels, with
// 5 minor ticks per major.
function ticks(duration: number, pxPerSec: number): { t: number; major: boolean }[] {
  if (duration <= 0 || pxPerSec <= 0) return [];
  const steps = [0.1, 0.2, 0.5, 1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 1800, 3600];
  const major = steps.find((s) => s * pxPerSec >= 90) ?? steps[steps.length - 1];
  const minor = major / 5;
  const out: { t: number; major: boolean }[] = [];
  for (let i = 0; i * minor <= duration + 1e-9; i++) {
    const t = Math.round(i * minor * 1000) / 1000;
    out.push({ t, major: i % 5 === 0 });
  }
  return out;
}

function fmtTick(s: number): string {
  if (s >= 60) {
    const m = Math.floor(s / 60);
    const r = s - m * 60;
    return `${m}:${String(Math.floor(r)).padStart(2, "0")}`;
  }
  return Number.isInteger(s) ? `${s}s` : `${s.toFixed(1)}s`;
}

// fmtTime: "12.34s", or "1m 02.50s" past a minute (Trim's fmtSec style).
function fmtTime(s: number): string {
  if (!Number.isFinite(s)) return "0.00s";
  if (s >= 60) {
    const m = Math.floor(s / 60);
    return `${m}m ${(s - m * 60).toFixed(2).padStart(5, "0")}s`;
  }
  return `${s.toFixed(2)}s`;
}

function baseName(p: string): string {
  return p.split(/[\\/]/).pop() ?? p;
}
