import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { cn } from "@/lib/utils";
import type { CompositorHandle } from "@/trim/compositor/Compositor";
import { animAt, setKeyColor, setValue } from "@/trim/fx/ops";
import { clipsAt, findClip, mediaById, type Clip, type Media, type Project } from "@/trim/project";

// PlayerHandles lies over the Compositor's canvas (docs/video-editor-v2.md,
// Inspector): click a picture on the player to select its clip (the topmost
// one under the pointer), drag it to move it, drag a corner to resize it
// around its centre. Moves snap to the canvas's centre lines (hold Alt to
// not snap). One drag is one undo step. While `picking` (the Inspector's
// eyedropper) a click instead reads the colour there, with the selected
// clip's own green screen off, and sets it as that clip's key colour.
//
// Put it in the same positioned parent as the Compositor: it measures the
// canvas and covers exactly it.

export interface PlayerHandlesProps {
  project: Project;
  time: number; // timeline time shown on the player
  canvas: HTMLCanvasElement | null;
  selection: string | null; // selected clip id
  onSelect: (clipId: string | null) => void;
  apply: (fn: (p: Project) => Project, key?: string) => void;
  // The Compositor's handle ref (read at click time, as it's set after mount).
  compositor?: { readonly current: CompositorHandle | null };
  picking?: boolean;
  onPickingChange?: (on: boolean) => void;
}

type Box = { left: number; top: number; width: number; height: number };
type Rect = { x0: number; y0: number; x1: number; y1: number }; // CSS px in the overlay
const SNAP_PX = 6;
const CORNERS = [
  ["nw", 0, 0],
  ["ne", 1, 0],
  ["sw", 0, 1],
  ["se", 1, 1],
] as const;

let dragSeq = 0;

export function PlayerHandles({ project, time, canvas, selection, onSelect, apply, compositor, picking, onPickingChange }: PlayerHandlesProps) {
  const ref = useRef<HTMLDivElement>(null);
  const [box, setBox] = useState<Box>({ left: 0, top: 0, width: 0, height: 0 });
  const [guide, setGuide] = useState<{ v: boolean; h: boolean }>({ v: false, h: false });

  // Cover the canvas: measure it against our positioned parent.
  useLayoutEffect(() => {
    const el = ref.current;
    const parent = el?.offsetParent as HTMLElement | null;
    if (!el || !canvas || !parent) return;
    const measure = () => {
      const c = canvas.getBoundingClientRect();
      const p = parent.getBoundingClientRect();
      setBox({ left: c.left - p.left, top: c.top - p.top, width: c.width, height: c.height });
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(canvas);
    ro.observe(parent);
    window.addEventListener("resize", measure);
    return () => {
      ro.disconnect();
      window.removeEventListener("resize", measure);
    };
  }, [canvas]);

  // Esc leaves the eyedropper.
  useEffect(() => {
    if (!picking) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onPickingChange?.(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [picking, onPickingChange]);

  const W = box.width;
  const H = box.height;
  // A clip's transform at the playhead (keyframes applied).
  const tf = (c: Clip) => animAt(c, time - c.start);
  // The rect a clip is drawn in (as Compositor's drawLayer, in CSS px; a
  // rotated clip's handles stay on its unrotated rect).
  const rectOf = (c: Clip, m: Media | undefined): Rect | null => {
    if (!m || m.width <= 0 || m.height <= 0 || W <= 0 || H <= 0) return null;
    const fit = Math.min(W / m.width, H / m.height) * tf(c).scale;
    const dw = m.width * fit;
    const dh = m.height * fit;
    const cx = tf(c).x * W;
    const cy = tf(c).y * H;
    return { x0: cx - dw / 2, y0: cy - dh / 2, x1: cx + dw / 2, y1: cy + dh / 2 };
  };

  // Visible picture clips at `time`, top to bottom.
  const visible = clipsAt(project, time)
    .filter((a) => a.track.kind === "video" && a.media.kind !== "audio" && tf(a.clip).opacity > 0)
    .reverse();
  const sel = selection ? findClip(project, selection) : undefined;
  const selShown = sel && visible.some((a) => a.clip.id === sel.clip.id) ? sel : undefined;
  const selRect = selShown ? rectOf(selShown.clip, mediaById(project, selShown.clip.media)) : null;

  const local = (e: { clientX: number; clientY: number }) => {
    const r = ref.current!.getBoundingClientRect();
    return { x: e.clientX - r.left, y: e.clientY - r.top };
  };

  const capture = (e: React.PointerEvent, onMove: (ev: PointerEvent) => void) => {
    const el = e.currentTarget as HTMLElement;
    el.setPointerCapture(e.pointerId);
    const done = () => {
      el.removeEventListener("pointermove", onMove);
      el.removeEventListener("pointerup", done);
      el.removeEventListener("lostpointercapture", done);
      setGuide({ v: false, h: false });
    };
    el.addEventListener("pointermove", onMove);
    el.addEventListener("pointerup", done);
    el.addEventListener("lostpointercapture", done);
  };

  const startMove = (e: React.PointerEvent, c: Clip, locked: boolean) => {
    if (locked) return;
    const p0 = local(e);
    const { x: x0, y: y0 } = tf(c);
    const lt = time - c.start;
    const key = `handles:move:${c.id}:${++dragSeq}`;
    let moved = false;
    capture(e, (ev) => {
      const p = local(ev);
      if (!moved && Math.hypot(p.x - p0.x, p.y - p0.y) < 3) return;
      moved = true;
      let x = x0 + (p.x - p0.x) / W;
      let y = y0 + (p.y - p0.y) / H;
      const snapV = !ev.altKey && Math.abs(x - 0.5) * W < SNAP_PX;
      const snapH = !ev.altKey && Math.abs(y - 0.5) * H < SNAP_PX;
      if (snapV) x = 0.5;
      if (snapH) y = 0.5;
      setGuide({ v: snapV, h: snapH });
      apply((q) => setValue(setValue(q, c.id, { anim: "x" }, round4(x), lt), c.id, { anim: "y" }, round4(y), lt), key);
    });
  };

  const startResize = (e: React.PointerEvent, c: Clip) => {
    e.preventDefault();
    e.stopPropagation();
    const t0 = tf(c);
    const lt = time - c.start;
    const cx = t0.x * W;
    const cy = t0.y * H;
    const p0 = local(e);
    const d0 = Math.max(1, Math.hypot(p0.x - cx, p0.y - cy));
    const s0 = t0.scale;
    const key = `handles:size:${c.id}:${++dragSeq}`;
    capture(e, (ev) => {
      const p = local(ev);
      const s = Math.max(0.02, Math.min(10, (s0 * Math.hypot(p.x - cx, p.y - cy)) / d0));
      apply((q) => setValue(q, c.id, { anim: "scale" }, round4(s), lt), key);
    });
  };

  const onPointerDown = (e: React.PointerEvent) => {
    if (e.button !== 0) return;
    const { x, y } = local(e);
    if (picking) {
      e.preventDefault();
      const target = sel?.clip;
      const comp = compositor?.current;
      if (!target || !comp) {
        onPickingChange?.(false);
        return;
      }
      void comp.pickColor(x / W, y / H, target.id).then((color) => {
        apply((q) => {
          const cur = findClip(q, target.id)?.clip;
          return cur ? setKeyColor(q, target.id, color, time - cur.start) : q;
        });
        onPickingChange?.(false);
      });
      return;
    }
    // The topmost picture under the pointer. (A clip covered by another can
    // be selected on the timeline, or where nothing covers it.)
    const inside = (r: Rect | null) => !!r && x >= r.x0 && x <= r.x1 && y >= r.y0 && y <= r.y1;
    const hit = visible.find((a) => inside(rectOf(a.clip, a.media)));
    if (!hit) {
      onSelect(null);
      return;
    }
    e.preventDefault();
    if (hit.clip.id !== selection) onSelect(hit.clip.id);
    startMove(e, hit.clip, hit.track.locked);
  };

  if (W <= 0 || H <= 0) return <div ref={ref} className="pointer-events-none absolute" />;
  return (
    <div
      ref={ref}
      className={cn("absolute z-10 touch-none select-none", picking ? "cursor-crosshair" : selRect ? "cursor-move" : "cursor-default")}
      style={{ left: box.left, top: box.top, width: W, height: H }}
      onPointerDown={onPointerDown}
      data-testid="player-handles"
    >
      {guide.v ? <div className="pointer-events-none absolute inset-y-0 left-1/2 w-px bg-yellow-300/80" /> : null}
      {guide.h ? <div className="pointer-events-none absolute inset-x-0 top-1/2 h-px bg-yellow-300/80" /> : null}
      {selRect && selShown && !picking ? (
        <div
          className="pointer-events-none absolute outline-1 outline-primary [outline-style:solid]"
          style={{ left: selRect.x0, top: selRect.y0, width: selRect.x1 - selRect.x0, height: selRect.y1 - selRect.y0 }}
        >
          {!selShown.track.locked
            ? CORNERS.map(([name, fx, fy]) => (
                <div
                  key={name}
                  data-corner={name}
                  className={cn(
                    "pointer-events-auto absolute size-2.5 -translate-x-1/2 -translate-y-1/2 border border-primary bg-background",
                    name === "nw" || name === "se" ? "cursor-nwse-resize" : "cursor-nesw-resize",
                  )}
                  style={{ left: `${fx * 100}%`, top: `${fy * 100}%` }}
                  onPointerDown={(e) => startResize(e, selShown.clip)}
                  title="Drag to resize"
                />
              ))
            : null}
        </div>
      ) : null}
      {picking ? (
        <div className="pointer-events-none absolute left-2 top-2 bg-black/70 px-2 py-1 text-[11px] text-white">
          Click the colour to make transparent · Esc cancels
        </div>
      ) : null}
    </div>
  );
}

const round4 = (n: number) => Math.round(n * 10000) / 10000;
