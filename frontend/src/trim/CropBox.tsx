import { useRef } from "react";
import type { Rect } from "@/lib/contract";
import { cn } from "@/lib/utils";

// CropBox is the spatial-crop overlay for the clip editor. It is laid exactly
// over the rendered <video> box (the parent measures that box and passes it
// in) and lets the user drag a rectangle (move) or any of its 8 handles
// (resize). The region outside the rectangle is dimmed, like the "to be
// removed" regions on the timeline. A plain click on that dimmed region is
// handed back to the parent (play/pause), so the player's click affordance
// keeps working while cropping. The Reset control lives in the parent's
// control row so it never sits on top of a handle.
//
// The crop itself is stored in VIDEO pixels (origin top-left of the frame),
// never CSS pixels: scale = videoW / rendered width converts pointer deltas.
// All four values are kept even: yuv420p needs even dimensions, and ffmpeg
// aligns odd offsets down to the chroma grid anyway, so showing the even
// value is showing what actually gets cut. The rect always stays inside the
// frame. `null` means "no crop" (full frame); a rect dragged back out to the
// full frame collapses to null again.

export type CropRect = Rect;

export interface RenderedBox {
  left: number;
  top: number;
  width: number;
  height: number;
}

// Smallest crop the UI lets you drag to (video px). ffmpeg only needs 2, but
// anything smaller than this is a mis-drag, not a crop.
const MIN_SIDE = 16;

const even = (n: number) => Math.max(2, Math.floor(n / 2) * 2);
const evenDown = (n: number) => Math.max(0, Math.floor(n / 2) * 2);

// normalizeCrop clamps a rect inside a videoW×videoH frame, even-aligns its
// size and offset, and returns null when it covers the whole frame.
export function normalizeCrop(r: Rect, videoW: number, videoH: number): CropRect | null {
  if (videoW <= 0 || videoH <= 0) return null;
  const w = even(Math.min(Math.round(r.w), videoW));
  const h = even(Math.min(Math.round(r.h), videoH));
  // Clamp first, then floor to even: flooring only moves the rect inward, so
  // it can never leave the frame.
  const x = evenDown(Math.max(0, Math.min(Math.round(r.x), videoW - w)));
  const y = evenDown(Math.max(0, Math.min(Math.round(r.y), videoH - h)));
  // After clamping x/y the rect can't exceed the frame, but a frame with odd
  // dimensions leaves a 1px sliver we can never cover; treat "as full as it
  // can get" as no crop.
  if (x === 0 && y === 0 && w >= videoW - 1 && h >= videoH - 1) return null;
  return { x, y, w, h };
}

const fullFrame = (videoW: number, videoH: number): Rect => ({ x: 0, y: 0, w: videoW, h: videoH });

type HandleId = "n" | "s" | "e" | "w" | "ne" | "nw" | "se" | "sw";

const HANDLES: { id: HandleId; className: string }[] = [
  { id: "nw", className: "left-0 top-0 -translate-x-1/2 -translate-y-1/2 cursor-nwse-resize" },
  { id: "n", className: "left-1/2 top-0 -translate-x-1/2 -translate-y-1/2 cursor-ns-resize" },
  { id: "ne", className: "right-0 top-0 translate-x-1/2 -translate-y-1/2 cursor-nesw-resize" },
  { id: "e", className: "right-0 top-1/2 translate-x-1/2 -translate-y-1/2 cursor-ew-resize" },
  { id: "se", className: "bottom-0 right-0 translate-x-1/2 translate-y-1/2 cursor-nwse-resize" },
  { id: "s", className: "bottom-0 left-1/2 -translate-x-1/2 translate-y-1/2 cursor-ns-resize" },
  { id: "sw", className: "bottom-0 left-0 -translate-x-1/2 translate-y-1/2 cursor-nesw-resize" },
  { id: "w", className: "left-0 top-1/2 -translate-x-1/2 -translate-y-1/2 cursor-ew-resize" },
];

export interface CropBoxProps {
  videoW: number;
  videoH: number;
  box: RenderedBox;
  crop: CropRect | null;
  onChange: (crop: CropRect | null) => void;
  // A plain click on the dimmed region outside the crop rect (not a drag on
  // the rect or a handle). The parent wires this to play/pause.
  onBackdropClick?: () => void;
  backdropTitle?: string;
}

export function CropBox({ videoW, videoH, box, crop, onChange, onBackdropClick, backdropTitle }: CropBoxProps) {
  // Drag state lives on the element via pointer capture (same pattern as the
  // timeline Handle: no window listeners that could outlive a lost pointerup).
  const drag = useRef<{ handle: HandleId | "move"; x0: number; y0: number; start: Rect } | null>(null);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  if (videoW <= 0 || videoH <= 0 || box.width <= 0 || box.height <= 0) return null;

  const rect = crop ?? fullFrame(videoW, videoH);
  const scale = videoW / box.width; // video px per CSS px
  const toCss = (n: number) => n / scale;

  const apply = (clientX: number, clientY: number) => {
    const d = drag.current;
    if (!d) return;
    const dx = (clientX - d.x0) * scale;
    const dy = (clientY - d.y0) * scale;
    const s = d.start;
    let { x, y, w, h } = s;
    if (d.handle === "move") {
      x = Math.max(0, Math.min(s.x + dx, videoW - s.w));
      y = Math.max(0, Math.min(s.y + dy, videoH - s.h));
    } else {
      const right = s.x + s.w;
      const bottom = s.y + s.h;
      if (d.handle.includes("w")) {
        x = Math.max(0, Math.min(s.x + dx, right - MIN_SIDE));
        w = right - x;
      }
      if (d.handle.includes("e")) {
        w = Math.max(MIN_SIDE, Math.min(s.w + dx, videoW - s.x));
      }
      if (d.handle.includes("n")) {
        y = Math.max(0, Math.min(s.y + dy, bottom - MIN_SIDE));
        h = bottom - y;
      }
      if (d.handle.includes("s")) {
        h = Math.max(MIN_SIDE, Math.min(s.h + dy, videoH - s.y));
      }
    }
    onChangeRef.current(normalizeCrop({ x, y, w, h }, videoW, videoH));
  };

  const begin = (handle: HandleId | "move") => (e: React.PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation();
    e.currentTarget.setPointerCapture(e.pointerId);
    drag.current = { handle, x0: e.clientX, y0: e.clientY, start: rect };
  };
  const end = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!drag.current) return;
    apply(e.clientX, e.clientY);
    drag.current = null;
    if (e.currentTarget.hasPointerCapture(e.pointerId)) e.currentTarget.releasePointerCapture(e.pointerId);
  };
  const dragProps = (handle: HandleId | "move") => ({
    onPointerDown: begin(handle),
    onPointerMove: (e: React.PointerEvent<HTMLDivElement>) => {
      if (drag.current && drag.current.handle === handle) apply(e.clientX, e.clientY);
    },
    onPointerUp: end,
    onLostPointerCapture: () => {
      drag.current = null;
    },
  });

  // CSS-px edges of the kept rect, for the four dim bands around it. Bands
  // (not a giant box-shadow) so the overlay needs no overflow-hidden, which
  // would clip the handles to half their size along the frame edges.
  const l = toCss(rect.x);
  const t = toCss(rect.y);
  const r = toCss(rect.x + rect.w);
  const b = toCss(rect.y + rect.h);
  const dim = "pointer-events-none absolute bg-black/55";

  return (
    <div
      className="absolute z-10 touch-none select-none"
      style={{ left: box.left, top: box.top, width: box.width, height: box.height }}
      role="group"
      aria-label="Crop region"
      title={backdropTitle}
      onClick={(e) => {
        // Only a click that lands on the overlay itself: drags on the rect or
        // a handle end with a click on THAT element (pointer capture), so
        // they bubble here with a different target and are ignored.
        if (e.target === e.currentTarget) onBackdropClick?.();
      }}
    >
      {/* "to be removed" bands: above, below, left of and right of the rect */}
      <div className={dim} style={{ left: 0, top: 0, right: 0, height: t }} />
      <div className={dim} style={{ left: 0, top: b, right: 0, bottom: 0 }} />
      <div className={dim} style={{ left: 0, top: t, width: l, height: b - t }} />
      <div className={dim} style={{ left: r, top: t, right: 0, height: b - t }} />
      {/* the kept region */}
      <div
        {...dragProps("move")}
        className="absolute cursor-move border-2 border-primary"
        style={{ left: l, top: t, width: r - l, height: b - t }}
        title="Drag to move the crop"
      >
        {/* thirds grid, for lining things up */}
        <div className="pointer-events-none absolute inset-y-0 left-1/3 w-px bg-white/20" />
        <div className="pointer-events-none absolute inset-y-0 left-2/3 w-px bg-white/20" />
        <div className="pointer-events-none absolute inset-x-0 top-1/3 h-px bg-white/20" />
        <div className="pointer-events-none absolute inset-x-0 top-2/3 h-px bg-white/20" />
        {/* size in VIDEO pixels */}
        <div className="pointer-events-none absolute left-1 top-1 bg-black/70 px-1.5 py-0.5 text-[11px] tabular-nums text-white">
          {rect.w} × {rect.h}
          {crop ? (
            <span className="ml-1.5 text-white/60">
              @ {rect.x},{rect.y}
            </span>
          ) : (
            <span className="ml-1.5 text-white/60">full frame</span>
          )}
        </div>
        {HANDLES.map((h) => (
          <div
            key={h.id}
            {...dragProps(h.id)}
            className={cn("absolute z-10 size-3.5 border border-black/60 bg-primary", h.className)}
          />
        ))}
      </div>
    </div>
  );
}
