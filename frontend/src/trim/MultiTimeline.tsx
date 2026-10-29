import { useEffect, useLayoutEffect, useRef, useState } from "react";
import {
  Eye,
  EyeOff,
  Film,
  Image as ImageIcon,
  Lock,
  Music,
  Plus,
  Scissors,
  Trash2,
  Type,
  Unlock,
  Volume2,
  VolumeX,
  ZoomIn,
  ZoomOut,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { Text } from "@/trim/edl";
import {
  addMedia,
  addTrack,
  canHold,
  clipEnd,
  clipLength,
  closeGap,
  findClip,
  insertClip,
  mediaById,
  moveClip,
  projectDuration,
  removeClip,
  removeProjectText,
  removeTrack,
  rippleDelete,
  setTrackFlags,
  splitAt,
  trimClip,
  upsertProjectText,
  type Clip,
  type Media,
  type Project,
  type Track,
} from "@/trim/project";

// MultiTimeline is the v2 editor's timeline (docs/video-editor-v2.md):
// track headers on the left (mute, hide, lock, delete, add track) and one row
// per track on the right, video tracks top track first, then audio tracks,
// under a ruler and a text row, with the playhead across all of them. Every
// track is free-form: clips sit anywhere and never overlap within a track.
//
// It owns no player and no project: the window passes the project and the
// playhead in and gets edits back through `apply` (useProjectHistory's
// apply; drags pass a key so a whole drag is one undo step).
//
// Mouse: click a clip or text to select it; click or drag on the ruler or an
// empty part of a track to seek. Drag a clip to move it in time or onto
// another track of the same kind, drag its edges to trim it, drag a text bar
// to move it or its ends to retime it. Drags snap to the playhead and clip
// edges (hold Alt to not snap). Right-click a gap for Close gap. Drop media
// from the bin (application/x-toru-media = media id) onto a track.
// Ctrl+wheel zooms around the pointer.
// Keys (ignored while typing): S split (the selected clip's track, or every
// track), Delete/Backspace delete (leaves a gap), Shift+Delete ripple delete,
// Ctrl+Z / Ctrl+Shift+Z (or Ctrl+Y) undo/redo, +/- zoom.

export type TimelineSelection = { kind: "clip" | "text"; id: string } | null;

export const MEDIA_MIME = "application/x-toru-media";

export interface MultiTimelineProps {
  project: Project;
  outTime: number; // playhead, timeline seconds
  onSeek: (t: number) => void;
  apply: (fn: (p: Project) => Project, key?: string) => void;
  selection: TimelineSelection;
  onSelect: (s: TimelineSelection) => void;
  // Media for an id dropped from the bin that isn't in the project yet.
  mediaFor?: (id: string) => Media | undefined;
  onUndo?: () => void;
  onRedo?: () => void;
  canUndo?: boolean;
  canRedo?: boolean;
  onAddText?: () => void;
  onEditText?: (id: string) => void; // double-click a text bar
  keyboard?: boolean; // default true; false when a dialog owns the keys
  className?: string;
}

const HEADER_W = 172;
const RULER_H = 20;
const TEXT_LANE_H = 22;
const ROW_H = { video: 46, audio: 34 } as const;
const MIN_ZOOM = 1;
const MAX_ZOOM = 60;
const DRAG_PX = 4; // a press that moves less is a click
const SNAP_PX = 8;
const KIND_COLOR = { video: "#4f8fe8", image: "#2fb8a3", audio: "#58b368" } as const;

let dragSeq = 0;

export function MultiTimeline({
  project: p,
  outTime,
  onSeek,
  apply,
  selection,
  onSelect,
  mediaFor,
  onUndo,
  onRedo,
  canUndo,
  canRedo,
  onAddText,
  onEditText,
  keyboard = true,
  className,
}: MultiTimelineProps) {
  const dur = projectDuration(p);
  // The visible span: the project plus room to drop or drag past its end.
  // Held while a drag runs so the scale doesn't change under the pointer.
  const [spanHold, setSpanHold] = useState(0);
  const span = spanHold > 0 ? Math.max(spanHold, dur) : Math.max(10, dur + Math.max(5, dur * 0.15));
  const scrollRef = useRef<HTMLDivElement>(null);
  const [zoom, setZoom] = useState(1);
  const [viewW, setViewW] = useState(0);
  const [menu, setMenu] = useState<{ x: number; y: number; trackId: string; t: number } | null>(null);
  const [dropAt, setDropAt] = useState<{ trackId: string; t: number } | null>(null);
  const [snapLine, setSnapLine] = useState<number | null>(null);

  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setViewW(el.clientWidth));
    ro.observe(el);
    setViewW(el.clientWidth);
    return () => ro.disconnect();
  }, []);

  const contentW = Math.max(1, viewW * zoom);
  const pxPerSec = contentW / span;
  const x = (t: number) => t * pxPerSec;

  // Rows top to bottom: video tracks top first, then audio tracks in order.
  const video = p.tracks.filter((t) => t.kind === "video");
  const audio = p.tracks.filter((t) => t.kind === "audio");
  const rows = [...video.slice().reverse(), ...audio];
  const label = (t: Track) =>
    t.kind === "video" ? `Video ${video.indexOf(t) + 1}` : `Audio ${audio.indexOf(t) + 1}`;

  // Text lanes (overlapping texts stack).
  const lanes: Text[][] = [];
  for (const t of [...p.texts].sort((a, b) => a.start - b.start)) {
    const lane = lanes.find((l) => l[l.length - 1].end <= t.start);
    if (lane) lane.push(t);
    else lanes.push([t]);
  }
  const textH = Math.max(1, lanes.length) * TEXT_LANE_H + 4;

  const timeAt = (clientX: number) => {
    const el = scrollRef.current;
    if (!el || pxPerSec <= 0) return 0;
    const r = el.getBoundingClientRect();
    return Math.max(0, (clientX - r.left + el.scrollLeft) / pxPerSec);
  };
  // The track row under a client y (in the scroll area), if any.
  const rowAt = (clientY: number): Track | undefined => {
    for (const t of rows) {
      const el = document.getElementById(rowDomId(t.id));
      const r = el?.getBoundingClientRect();
      if (r && clientY >= r.top && clientY < r.bottom) return t;
    }
    return undefined;
  };

  // --- zoom (as v1: refs so wheel bursts compound; anchored scroll) --------
  const zoomVal = useRef(zoom);
  const anchor = useRef<{ t: number; offset: number } | null>(null);
  const zoomTo = (z: number, anchorX?: number) => {
    const el = scrollRef.current;
    const next = Math.max(MIN_ZOOM, Math.min(MAX_ZOOM, z));
    if (next === zoomVal.current) return;
    if (el && viewW > 0) {
      const r = el.getBoundingClientRect();
      const pps = (viewW * zoomVal.current) / span;
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
    else if (el && a) el.scrollLeft = Math.max(0, a.t * pxPerSec - a.offset);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [zoom]);
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
  // Keep the playhead in view when it moves off the visible part.
  useEffect(() => {
    const el = scrollRef.current;
    if (!el || zoom <= 1) return;
    const px = x(outTime);
    if (px < el.scrollLeft || px > el.scrollLeft + el.clientWidth - 8) el.scrollLeft = Math.max(0, px - el.clientWidth * 0.1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [outTime]);

  // --- snapping --------------------------------------------------------------
  // snap returns t moved onto the nearest playhead / clip edge within
  // SNAP_PX, ignoring the clip being dragged.
  const snapTargets = (skipClip?: string) => {
    const out = [0, outTime];
    for (const t of p.tracks) for (const c of t.clips) if (c.id !== skipClip) out.push(c.start, clipEnd(c));
    return out;
  };
  const snap = (t: number, targets: number[], off: boolean): { t: number; line: number | null } => {
    if (off || pxPerSec <= 0) return { t, line: null };
    let best = t;
    let bestD = SNAP_PX / pxPerSec;
    let line: number | null = null;
    for (const s of targets) {
      const d = Math.abs(s - t);
      if (d < bestD) {
        bestD = d;
        best = s;
        line = s;
      }
    }
    return { t: best, line };
  };

  // --- edit commands ---------------------------------------------------------
  const selClip = selection?.kind === "clip" ? findClip(p, selection.id) : undefined;
  const doSplit = () => apply((q) => splitAt(q, outTime, selClip?.track.id));
  const doDelete = (ripple: boolean) => {
    if (!selection) return;
    if (selection.kind === "text") {
      apply((q) => removeProjectText(q, selection.id));
      onSelect(null);
      return;
    }
    if (!selClip || selClip.track.locked) return;
    apply((q) => (ripple ? rippleDelete(q, selection.id) : removeClip(q, selection.id)));
    onSelect(null);
  };
  const canSplit = splitAt(p, outTime, selClip?.track.id) !== p;
  const canDelete = selection?.kind === "text" || (!!selClip && !selClip.track.locked);

  // Keyboard. No dependency array: the handler must see the current props.
  useEffect(() => {
    if (!keyboard) return;
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || t.isContentEditable)) return;
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
            doDelete(e.shiftKey);
          }
          break;
        case "Escape":
          setMenu(null);
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

  // --- drags -----------------------------------------------------------------
  // Pointer-captured on the element they start on, computed from the values
  // at pointerdown (never the live project), one undo step per drag.
  const capture = (e: React.PointerEvent, onMove: (ev: PointerEvent) => void, onEnd?: (ev: PointerEvent, ok: boolean) => void) => {
    const el = e.currentTarget as HTMLElement;
    el.setPointerCapture(e.pointerId);
    setSpanHold(span);
    const up = (ev: PointerEvent) => done(ev, true);
    const lost = (ev: PointerEvent) => done(ev, false);
    const done = (ev: PointerEvent, ok: boolean) => {
      el.removeEventListener("pointermove", onMove);
      el.removeEventListener("pointerup", up);
      el.removeEventListener("lostpointercapture", lost);
      setSnapLine(null);
      setSpanHold(0);
      onEnd?.(ev, ok);
    };
    el.addEventListener("pointermove", onMove);
    el.addEventListener("pointerup", up);
    el.addEventListener("lostpointercapture", lost);
  };

  const startClipDrag = (e: React.PointerEvent, track: Track, c: Clip) => {
    if (e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation();
    setMenu(null);
    onSelect({ kind: "clip", id: c.id });
    if (track.locked) return;
    const x0 = e.clientX;
    const start0 = c.start;
    const len = clipLength(c);
    const targets = snapTargets(c.id);
    const key = `move:${c.id}:${++dragSeq}`;
    let moved = false;
    capture(e, (ev) => {
      if (!moved && Math.abs(ev.clientX - x0) < DRAG_PX) return;
      moved = true;
      const raw = Math.max(0, start0 + (ev.clientX - x0) / pxPerSec);
      // Snap whichever end is nearer a target.
      const a = snap(raw, targets, ev.altKey);
      const b = snap(raw + len, targets, ev.altKey);
      const useEnd = b.line !== null && (a.line === null || Math.abs(b.t - (raw + len)) < Math.abs(a.t - raw));
      const s = useEnd ? b.t - len : a.t;
      setSnapLine(useEnd ? b.line : a.line);
      const over = rowAt(ev.clientY);
      const to = over && over.kind === track.kind && !over.locked ? over : track;
      apply((q) => moveClip(q, c.id, to.id, s), key);
    });
  };

  const startEdgeDrag = (e: React.PointerEvent, track: Track, c: Clip, edge: "start" | "end") => {
    if (e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation();
    setMenu(null);
    onSelect({ kind: "clip", id: c.id });
    if (track.locked) return;
    const x0 = e.clientX;
    const base = edge === "start" ? c.start : clipEnd(c);
    const targets = snapTargets(c.id);
    const key = `trim:${c.id}:${++dragSeq}`;
    capture(e, (ev) => {
      const { t, line } = snap(base + (ev.clientX - x0) / pxPerSec, targets, ev.altKey);
      setSnapLine(line);
      apply((q) => trimClip(q, c.id, edge, Math.round(t * 1000) / 1000), key);
    });
  };

  const startTextDrag = (e: React.PointerEvent, t: Text, part: "body" | "start" | "end") => {
    if (e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation();
    setMenu(null);
    onSelect({ kind: "text", id: t.id });
    const x0 = e.clientX;
    const len = t.end - t.start;
    const key = `text:${t.id}:${++dragSeq}`;
    const targets = snapTargets();
    let moved = false;
    capture(
      e,
      (ev) => {
        if (!moved && Math.abs(ev.clientX - x0) < DRAG_PX) return;
        moved = true;
        const d = (ev.clientX - x0) / pxPerSec;
        let next: Text;
        if (part === "body") {
          const { t: s, line } = snap(Math.max(0, t.start + d), targets, ev.altKey);
          setSnapLine(line);
          next = { ...t, start: s, end: s + len };
        } else if (part === "start") {
          const { t: s, line } = snap(Math.max(0, Math.min(t.end - 0.1, t.start + d)), targets, ev.altKey);
          setSnapLine(line);
          next = { ...t, start: Math.min(s, t.end - 0.1) };
        } else {
          const { t: s, line } = snap(Math.max(t.start + 0.1, t.end + d), targets, ev.altKey);
          setSnapLine(line);
          next = { ...t, end: Math.max(s, t.start + 0.1) };
        }
        apply((q) => upsertProjectText(q, next), key);
      },
      (_ev, ok) => {
        if (ok && !moved && part === "body") onSeek(t.start);
      },
    );
  };

  const startScrub = (e: React.PointerEvent) => {
    if (e.button !== 0) return;
    setMenu(null);
    onSelect(null);
    onSeek(timeAt(e.clientX));
    capture(e, (ev) => onSeek(timeAt(ev.clientX)));
  };

  // --- drop from the media bin -------------------------------------------------
  const isMediaDrag = (e: React.DragEvent) => e.dataTransfer.types.includes(MEDIA_MIME);
  const dropTarget = (track: Track, m: Media | undefined): Track | undefined => {
    if (!m) return undefined;
    if (canHold(track.kind, m) && !track.locked) return track;
    return undefined;
  };
  const onRowDragOver = (e: React.DragEvent, track: Track) => {
    if (!isMediaDrag(e) || track.locked) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "copy";
    setDropAt({ trackId: track.id, t: timeAt(e.clientX) });
  };
  const onRowDrop = (e: React.DragEvent, track: Track) => {
    setDropAt(null);
    if (!isMediaDrag(e)) return;
    e.preventDefault();
    const id = e.dataTransfer.getData(MEDIA_MIME);
    const m = mediaById(p, id) ?? mediaFor?.(id);
    const at = timeAt(e.clientX);
    if (!m) return;
    apply((q) => {
      let r = addMedia(q, m);
      let to = dropTarget(track, m);
      if (!to) {
        // Wrong kind for this row: the first unlocked track that takes it,
        // or a new one.
        const kind = m.kind === "audio" ? "audio" : "video";
        to = r.tracks.find((t) => t.kind === kind && !t.locked && canHold(t.kind, m));
        if (!to) {
          r = addTrack(r, kind);
          to = r.tracks.filter((t) => t.kind === kind).at(-1)!;
        }
      }
      return insertClip(r, to.id, m.id, at);
    });
  };

  // Close the menu on any outside press.
  useEffect(() => {
    if (!menu) return;
    const close = () => setMenu(null);
    window.addEventListener("pointerdown", close);
    return () => window.removeEventListener("pointerdown", close);
  }, [menu]);

  const ticks = rulerTicks(span, pxPerSec);
  const totalRowsH = rows.reduce((h, t) => h + ROW_H[t.kind], 0);

  return (
    <div className={cn("flex flex-col gap-2", className)}>
      {/* toolbar */}
      <div className="flex items-center gap-1">
        <Button size="sm" variant="ghost" disabled={!canSplit} onClick={doSplit} title="Split at the playhead (S)">
          <Scissors /> Split
        </Button>
        <Button
          size="sm"
          variant="ghost"
          disabled={!canDelete}
          onClick={() => doDelete(false)}
          title="Delete, leaving a gap (Delete). Shift+Delete closes the gap."
        >
          <Trash2 /> Delete
        </Button>
        {onAddText ? (
          <Button size="sm" variant="ghost" onClick={onAddText} title="Add text at the playhead">
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
          {fmtTime(outTime)} / {fmtTime(dur)}
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

      <div className="flex border bg-card/40">
        {/* track headers */}
        <div className="shrink-0 border-r border-border/60" style={{ width: HEADER_W }}>
          <div className="border-b border-border/60" style={{ height: RULER_H }} />
          <div className="flex items-center gap-1 border-b border-border/60 px-2 text-[11px] text-muted-foreground" style={{ height: textH }}>
            <Type className="size-3" /> Text
          </div>
          {rows.map((t) => (
            <TrackHeader
              key={t.id}
              track={t}
              label={label(t)}
              height={ROW_H[t.kind]}
              canDelete={t.kind === "audio" || video.length > 1}
              onFlags={(f) => apply((q) => setTrackFlags(q, t.id, f))}
              onDelete={() => apply((q) => removeTrack(q, t.id))}
            />
          ))}
          <div className="flex gap-1 p-1">
            <Button size="sm" variant="ghost" className="h-7 flex-1 px-1 text-[11px]" onClick={() => apply((q) => addTrack(q, "video"))} title="Add a video track on top">
              <Plus /> Video
            </Button>
            <Button size="sm" variant="ghost" className="h-7 flex-1 px-1 text-[11px]" onClick={() => apply((q) => addTrack(q, "audio"))} title="Add an audio track">
              <Plus /> Audio
            </Button>
          </div>
        </div>

        {/* rows */}
        <div
          ref={scrollRef}
          className={cn("relative min-w-0 flex-1 overflow-y-hidden", zoom > 1 ? "overflow-x-auto" : "overflow-x-hidden")}
        >
          <div className="relative" style={{ width: contentW }}>
            {/* ruler */}
            <div className="relative cursor-pointer select-none overflow-hidden border-b border-border/60" style={{ height: RULER_H }} onPointerDown={startScrub}>
              {ticks.map(({ t, major }) => (
                <div key={t} className="pointer-events-none absolute bottom-0" style={{ left: x(t) }}>
                  <div className={cn("w-px bg-foreground/30", major ? "h-2.5" : "h-1.5")} />
                  {major ? <span className="absolute bottom-2.5 left-1 text-[10px] tabular-nums text-muted-foreground">{fmtTick(t)}</span> : null}
                </div>
              ))}
            </div>

            {/* text row */}
            <div className="relative border-b border-border/60" style={{ height: textH }} onPointerDown={startScrub}>
              {lanes.map((lane, li) =>
                lane.map((t) => {
                  const sel = selection?.kind === "text" && selection.id === t.id;
                  return (
                    <div
                      key={t.id}
                      className={cn(
                        "absolute flex h-[18px] cursor-grab touch-none items-center overflow-hidden border bg-foreground/15 text-[11px] active:cursor-grabbing",
                        sel ? "border-primary bg-primary/30" : "border-foreground/30",
                      )}
                      style={{ left: x(t.start), width: Math.max(4, x(t.end) - x(t.start)), top: 2 + li * TEXT_LANE_H }}
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

            {/* tracks */}
            {rows.map((track) => (
              <div
                key={track.id}
                id={rowDomId(track.id)}
                data-track={track.id}
                className={cn(
                  "relative border-b border-border/40",
                  track.locked && "bg-[repeating-linear-gradient(135deg,transparent_0_6px,rgb(255_255_255/0.03)_6px_12px)]",
                  track.kind === "video" && track.hidden && "opacity-50",
                  dropAt?.trackId === track.id && "bg-primary/10",
                )}
                style={{ height: ROW_H[track.kind] }}
                onPointerDown={startScrub}
                onContextMenu={(e) => {
                  const t = timeAt(e.clientX);
                  if (track.locked || closeGap(p, track.id, t) === p) return;
                  e.preventDefault();
                  setMenu({ x: e.clientX, y: e.clientY, trackId: track.id, t });
                }}
                onDragOver={(e) => onRowDragOver(e, track)}
                onDragLeave={() => setDropAt((d) => (d?.trackId === track.id ? null : d))}
                onDrop={(e) => onRowDrop(e, track)}
              >
                {track.clips.map((c) => (
                  <ClipBox
                    key={c.id}
                    clip={c}
                    media={mediaById(p, c.media)}
                    track={track}
                    left={x(c.start)}
                    width={x(clipLength(c))}
                    selected={selection?.kind === "clip" && selection.id === c.id}
                    onBody={(e) => startClipDrag(e, track, c)}
                    onEdge={(e, edge) => startEdgeDrag(e, track, c, edge)}
                  />
                ))}
                {dropAt?.trackId === track.id ? (
                  <div className="pointer-events-none absolute inset-y-0 z-20 w-0.5 bg-primary" style={{ left: x(dropAt.t) }} />
                ) : null}
              </div>
            ))}

            {/* snap guide */}
            {snapLine !== null ? (
              <div className="pointer-events-none absolute z-30 w-px bg-yellow-300/90" style={{ left: x(snapLine), top: RULER_H, height: textH + totalRowsH }} />
            ) : null}
            {/* playhead */}
            <div className="pointer-events-none absolute inset-y-0 z-40 w-px bg-foreground/90" style={{ left: x(outTime) }}>
              <div className="absolute -left-1 top-0 size-2 rotate-45 bg-foreground/90" />
            </div>
          </div>
        </div>
      </div>

      {menu ? (
        <div
          className="fixed z-50 border border-border bg-popover py-1 text-xs text-popover-foreground shadow-lg"
          style={{ left: menu.x, top: menu.y }}
          onPointerDown={(e) => e.stopPropagation()}
        >
          <button
            type="button"
            className="block w-full px-3 py-1.5 text-left hover:bg-accent"
            onClick={() => {
              apply((q) => closeGap(q, menu.trackId, menu.t));
              setMenu(null);
            }}
          >
            Close gap
          </button>
        </div>
      ) : null}
    </div>
  );
}

function TrackHeader({
  track,
  label,
  height,
  canDelete,
  onFlags,
  onDelete,
}: {
  track: Track;
  label: string;
  height: number;
  canDelete: boolean;
  onFlags: (f: Partial<Pick<Track, "muted" | "hidden" | "locked">>) => void;
  onDelete: () => void;
}) {
  const btn = "size-6 shrink-0 [&_svg]:size-3.5";
  return (
    <div className="flex items-center gap-0.5 border-b border-border/40 px-1.5" style={{ height }} data-track-header={track.id}>
      <span className="min-w-0 flex-1 truncate text-[11px] text-muted-foreground" title={label}>
        {label}
      </span>
      <Button
        size="icon"
        variant="ghost"
        className={btn}
        onClick={() => onFlags({ muted: !track.muted })}
        title={track.muted ? `Unmute ${label}` : `Mute ${label}`}
        aria-label={track.muted ? `Unmute ${label}` : `Mute ${label}`}
        aria-pressed={track.muted}
      >
        {track.muted ? <VolumeX className="text-destructive" /> : <Volume2 />}
      </Button>
      {track.kind === "video" ? (
        <Button
          size="icon"
          variant="ghost"
          className={btn}
          onClick={() => onFlags({ hidden: !track.hidden })}
          title={track.hidden ? `Show ${label}` : `Hide ${label}`}
          aria-label={track.hidden ? `Show ${label}` : `Hide ${label}`}
          aria-pressed={track.hidden}
        >
          {track.hidden ? <EyeOff className="text-destructive" /> : <Eye />}
        </Button>
      ) : null}
      <Button
        size="icon"
        variant="ghost"
        className={btn}
        onClick={() => onFlags({ locked: !track.locked })}
        title={track.locked ? `Unlock ${label}` : `Lock ${label}`}
        aria-label={track.locked ? `Unlock ${label}` : `Lock ${label}`}
        aria-pressed={track.locked}
      >
        {track.locked ? <Lock className="text-primary" /> : <Unlock />}
      </Button>
      <Button
        size="icon"
        variant="ghost"
        className={btn}
        disabled={!canDelete || track.locked}
        onClick={onDelete}
        title={canDelete ? `Delete ${label} and its clips` : "The last video track can't be deleted"}
        aria-label={`Delete ${label}`}
      >
        <Trash2 />
      </Button>
    </div>
  );
}

function ClipBox({
  clip: c,
  media,
  track,
  left,
  width,
  selected,
  onBody,
  onEdge,
}: {
  clip: Clip;
  media: Media | undefined;
  track: Track;
  left: number;
  width: number;
  selected: boolean;
  onBody: (e: React.PointerEvent) => void;
  onEdge: (e: React.PointerEvent, edge: "start" | "end") => void;
}) {
  const kind = track.kind === "audio" ? "audio" : (media?.kind ?? "video");
  const color = KIND_COLOR[kind];
  const Icon = kind === "audio" ? Music : kind === "image" ? ImageIcon : Film;
  const len = clipLength(c);
  const name = media ? baseName(media.path) : "(missing)";
  const fadeInW = len > 0 ? (c.fadeIn / len) * width : 0;
  const fadeOutW = len > 0 ? (c.fadeOut / len) * width : 0;
  return (
    <div
      data-clip={c.id}
      className={cn(
        "absolute inset-y-0.5 touch-none overflow-hidden border-r border-background",
        track.locked ? "cursor-not-allowed" : "cursor-grab active:cursor-grabbing",
        selected && "z-10 outline-2 -outline-offset-2 outline-primary",
        (c.muted || track.muted) && "opacity-60",
      )}
      style={{ left, width: Math.max(2, width), background: `${color}40`, borderTop: `3px solid ${color}` }}
      title={`${name} · ${fmtTime(c.start)} – ${fmtTime(c.start + len)} (${fmtTime(len)})${c.speed !== 1 ? ` · ${c.speed}x` : ""}. Drag to move, drag the edges to trim.`}
      onPointerDown={onBody}
    >
      {fadeInW > 1 ? (
        <div className="pointer-events-none absolute inset-y-0 left-0 bg-[linear-gradient(to_bottom_right,rgb(0_0_0/0.45)_50%,transparent_50%)]" style={{ width: fadeInW }} />
      ) : null}
      {fadeOutW > 1 ? (
        <div className="pointer-events-none absolute inset-y-0 right-0 bg-[linear-gradient(to_bottom_left,rgb(0_0_0/0.45)_50%,transparent_50%)]" style={{ width: fadeOutW }} />
      ) : null}
      {width > 36 ? (
        <div className="pointer-events-none flex items-center gap-1 truncate px-1.5 pt-0.5 text-[11px] text-foreground/85">
          <Icon className="size-3 shrink-0 opacity-70" />
          <span className="truncate">{name}</span>
          {c.speed !== 1 ? <span className="shrink-0 bg-black/40 px-1 tabular-nums">{c.speed}x</span> : null}
          {c.muted ? <VolumeX className="size-3 shrink-0" /> : null}
          {c.chroma ? <span className="size-2 shrink-0 rounded-full" style={{ background: c.chroma.color }} title="Green screen" /> : null}
        </div>
      ) : null}
      {!track.locked ? (
        <>
          <div className="group/edge absolute inset-y-0 left-0 w-2 cursor-ew-resize" onPointerDown={(e) => onEdge(e, "start")} title="Drag to trim the start">
            <div className={cn("h-full w-1 group-hover/edge:bg-primary", selected && "bg-primary/70")} />
          </div>
          <div className="group/edge absolute inset-y-0 right-0 flex w-2 cursor-ew-resize justify-end" onPointerDown={(e) => onEdge(e, "end")} title="Drag to trim the end">
            <div className={cn("h-full w-1 group-hover/edge:bg-primary", selected && "bg-primary/70")} />
          </div>
        </>
      ) : null}
    </div>
  );
}

const rowDomId = (trackId: string) => `mt-row-${trackId}`;

// Ruler ticks: a "nice" major step that leaves ~90px between labels, with
// 5 minor ticks per major.
function rulerTicks(span: number, pxPerSec: number): { t: number; major: boolean }[] {
  if (span <= 0 || pxPerSec <= 0) return [];
  const steps = [0.1, 0.2, 0.5, 1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 1800, 3600];
  const major = steps.find((s) => s * pxPerSec >= 90) ?? steps[steps.length - 1];
  const minor = major / 5;
  const out: { t: number; major: boolean }[] = [];
  for (let i = 0; i * minor <= span + 1e-9; i++) out.push({ t: Math.round(i * minor * 1000) / 1000, major: i % 5 === 0 });
  return out;
}

function fmtTick(s: number): string {
  if (s >= 60) {
    const m = Math.floor(s / 60);
    return `${m}:${String(Math.floor(s - m * 60)).padStart(2, "0")}`;
  }
  return Number.isInteger(s) ? `${s}s` : `${s.toFixed(1)}s`;
}

function fmtTime(s: number): string {
  if (!Number.isFinite(s)) return "0.00s";
  if (s >= 60) {
    const m = Math.floor(s / 60);
    return `${m}m ${(s - m * 60).toFixed(2).padStart(5, "0")}s`;
  }
  return `${s.toFixed(2)}s`;
}

function baseName(p: string): string {
  return p.replace(/\\/g, "/").split("/").pop() ?? p;
}
