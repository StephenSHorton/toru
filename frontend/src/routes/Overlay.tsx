// REAL capture overlay — one instance renders per monitor (one Wails window each).
//
// Each window is a frameless, always-on-top, OPAQUE window covering its monitor's
// full DIP bounds. Go froze a still of every monitor BEFORE any window appeared,
// and serves it at /__shot/<id>; this route paints that still fullscreen, dims it
// with a CSS mask, and reveals the bright (undimmed) still under the crop hole.
//
// EVERY monitor's window is interactive: each draws its own crop rectangle
// (body drag + 8 resize handles, min-size, CLAMPED to the monitor — cross-
// monitor crops are still deferred), a dimension badge in PHYSICAL px, and the
// frosted control pill (Screenshot/Record toggle + Full screen + Capture +
// Cancel). Committing from a monitor's pill captures THAT monitor's crop.
//
// CORRECTNESS: committing a SCREENSHOT crops the FROZEN still in Go (never a live
// re-capture, which would photograph these dim windows). VIDEO dismisses the
// overlay first (Go side), then records the live region. Esc / Cancel tears down
// ALL overlay windows and re-opens the dev Hub.
//
// DPI: all of the crop->physical math lives in cropToPhysical() using the LOCKED
// formulas — round ONCE, reuse for the emitted Rect, the frozen-still sub-rect,
// and the on-screen badge so they are byte-identical.

import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Camera, Maximize, Video, X } from "lucide-react";
import { OverlayService } from "@/lib/api";
import {
  parseOverlayQuery,
  type Rect,
  type CaptureRequest,
} from "@/lib/contract";

const MIN_CROP = 24; // minimum crop size, CSS px (drag/resize floor)
const HANDLE_HIT = 14; // resize-handle hit area, CSS px
const SAVE_DEBOUNCE_MS = 300;

// CssRect is the crop rectangle in CSS px within this window's viewport.
interface CssRect {
  left: number;
  top: number;
  width: number;
  height: number;
}

type Handle = "nw" | "n" | "ne" | "w" | "e" | "sw" | "s" | "se";
const HANDLES: Handle[] = ["nw", "n", "ne", "w", "e", "sw", "s", "se"];

export default function Overlay() {
  const q = parseOverlayQuery(window.location.search);
  const [mode, setMode] = useState<"screenshot" | "video">("screenshot");
  const [busy, setBusy] = useState(false);

  // The monitor's CSS size IS the window viewport (each window already covers the
  // full monitor in DIP), so layout uses innerWidth/innerHeight, NOT the physical
  // w/h from the query.
  const monW = window.innerWidth;
  const monH = window.innerHeight;

  // Seed the crop. On the primary window, restore the persisted monitor-local
  // PHYSICAL crop (query `crop`) by dividing through by scale; fall back to a
  // centered default. Non-primary windows carry no crop.
  const [crop, setCrop] = useState<CssRect>(() =>
    seedCrop(q.crop, q.scale, monW, monH),
  );

  const saveTimer = useRef<number | null>(null);

  // Persist the crop (monitor-local PHYSICAL px) on drag/resize end, debounced.
  const persistCrop = useCallback(
    (c: CssRect) => {
      if (saveTimer.current != null) window.clearTimeout(saveTimer.current);
      saveTimer.current = window.setTimeout(() => {
        const { sub } = cropToPhysical(c, q.scale, q.bx, q.by, q.mw, q.mh);
        void OverlayService.SaveCrop(q.mon, sub);
      }, SAVE_DEBOUNCE_MS);
    },
    [q.scale, q.bx, q.by, q.mw, q.mh, q.mon],
  );

  // Cancel/Esc: tear down ALL overlay windows (Go) and re-open the Hub.
  const cancel = useCallback(() => {
    void OverlayService.Cancel();
  }, []);

  // Screenshot commit: crop the FROZEN still (Go), never a live re-capture.
  const captureScreenshot = useCallback(async () => {
    if (busy) return;
    setBusy(true);
    const { emit, sub } = cropToPhysical(crop, q.scale, q.bx, q.by, q.mw, q.mh);
    try {
      // Persist immediately (Go also persists, but flush our debounce intent).
      void OverlayService.SaveCrop(q.mon, sub);
      await OverlayService.CommitScreenshot(q.mon, emit, sub, false);
    } finally {
      setBusy(false);
    }
  }, [busy, crop, q.scale, q.bx, q.by, q.mw, q.mh, q.mon]);

  // Record: Go dismisses the overlay FIRST, then records the live region.
  const startRecording = useCallback(async () => {
    if (busy) return;
    setBusy(true);
    const { emit } = cropToPhysical(crop, q.scale, q.bx, q.by, q.mw, q.mh);
    const req: CaptureRequest = {
      mode: "video",
      sub: isFullScreen ? "fullscreen" : "region",
      monitorId: q.mon,
      rect: emit,
      dpiScale: q.scale,
      includeCursor: true,
      countdownSec: 0,
      copyOnCommit: false,
    };
    try {
      // Go opens the recording pill (timer + Stop) itself: StartRecording
      // dismisses this overlay window, so code after this await never runs.
      await OverlayService.StartRecording(req);
    } finally {
      setBusy(false);
    }
  }, [busy, crop, q.scale, q.bx, q.by, q.mw, q.mh, q.mon]);

  // Esc cancels (wired on every overlay window for safety).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        cancel();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [cancel]);

  // ----- interactive crop (every monitor) -----
  // A pointer drag either MOVES the crop body or RESIZES via a handle. All math
  // is in CSS px and clamped to the monitor (cross-monitor crop is deferred —
  // each monitor owns its OWN crop; committing from a monitor's pill captures
  // THAT monitor's crop, carried by q.mon).
  const beginDrag = useCallback(
    (e: React.PointerEvent, handle: Handle | "body") => {
      e.preventDefault();
      e.stopPropagation();
      (e.target as Element).setPointerCapture?.(e.pointerId);

      const startX = e.clientX;
      const startY = e.clientY;
      const start = crop;

      const onMove = (ev: PointerEvent) => {
        const dx = ev.clientX - startX;
        const dy = ev.clientY - startY;
        setCrop(
          handle === "body"
            ? moveCrop(start, dx, dy, monW, monH)
            : resizeCrop(start, handle, dx, dy, monW, monH),
        );
      };
      const onUp = (ev: PointerEvent) => {
        window.removeEventListener("pointermove", onMove);
        window.removeEventListener("pointerup", onUp);
        (e.target as Element).releasePointerCapture?.(ev.pointerId);
        // Read the freshest crop off the next state via functional updater.
        setCrop((c) => {
          persistCrop(c);
          return c;
        });
      };
      window.addEventListener("pointermove", onMove);
      window.addEventListener("pointerup", onUp);
    },
    [crop, monW, monH, persistCrop],
  );

  // Full screen: snap the crop to the entire monitor. The commit then carries
  // sub="fullscreen" (the contract's sub-mode for whole-monitor capture).
  const selectFullScreen = useCallback(() => {
    const full = { left: 0, top: 0, width: monW, height: monH };
    setCrop(full);
    persistCrop(full);
  }, [monW, monH, persistCrop]);

  const isFullScreen =
    crop.left <= 0 &&
    crop.top <= 0 &&
    crop.width >= monW &&
    crop.height >= monH;

  // Badge values are the SAME rounded physical numbers as the saved crop.
  const { sub } = cropToPhysical(crop, q.scale, q.bx, q.by, q.mw, q.mh);

  return (
    <div className="relative h-screen w-screen select-none overflow-hidden bg-black">
      {/* Frozen still, painted fullscreen (1:1 with the monitor in DIP). */}
      {q.stillUrl ? (
        <img
          src={q.stillUrl}
          alt=""
          draggable={false}
          className="pointer-events-none absolute inset-0 h-full w-full"
          style={{ objectFit: "fill" }}
        />
      ) : null}

      {/* Four-panel dim mask with a transparent crop hole. EVERY monitor is
          interactive: each window owns its own crop, and committing from a
          monitor's pill captures that monitor (q.mon). */}
      <DimMask crop={crop} monW={monW} monH={monH} />

      {/* Interactive crop rectangle — every monitor. */}
      {!isFullScreen ? (
        <div
          className="absolute ring-1 ring-primary/90"
          style={{
            left: crop.left,
            top: crop.top,
            width: crop.width,
            height: crop.height,
            cursor: "move",
          }}
          onPointerDown={(e) => beginDrag(e, "body")}
        >
          {/* dimension badge — PHYSICAL px (matches the saved PNG exactly) */}
          <div className="frost absolute -top-7 left-0 px-2 py-0.5 text-[11px] tabular-nums">
            {sub.w} × {sub.h}
          </div>

          {/* 8 resize handles */}
          {HANDLES.map((h) => (
            <span
              key={h}
              data-handle={h}
              onPointerDown={(e) => beginDrag(e, h)}
              className="absolute border border-background bg-primary"
              style={handleStyle(h)}
            />
          ))}
        </div>
      ) : (
        /* Full-screen mode: whole-monitor ring + badge, no drag affordances. */
        <div className="pointer-events-none absolute inset-0 ring-2 ring-inset ring-primary/90">
          <div className="frost absolute left-1/2 top-3 -translate-x-1/2 px-2 py-0.5 text-[11px] tabular-nums">
            Entire screen · {sub.w} × {sub.h}
          </div>
        </div>
      )}

      {/* frosted control pill — every monitor (commit captures THIS monitor) */}
      <div className="frost absolute bottom-8 left-1/2 flex -translate-x-1/2 items-center gap-1 p-1.5">
          <Button
            size="sm"
            variant={mode === "screenshot" ? "default" : "ghost"}
            onClick={() => setMode("screenshot")}
          >
            <Camera /> Screenshot
          </Button>
          <Button
            size="sm"
            variant={mode === "video" ? "default" : "ghost"}
            onClick={() => setMode("video")}
          >
            <Video /> Record
          </Button>
          <div className="mx-1 h-5 w-px bg-border" />
          <Button
            size="sm"
            variant={isFullScreen ? "default" : "ghost"}
            onClick={selectFullScreen}
            title="Capture the entire monitor"
          >
            <Maximize /> Full screen
          </Button>
          <div className="mx-1 h-5 w-px bg-border" />
          <Button size="sm" variant="ghost" onClick={cancel}>
            <X /> Cancel
          </Button>
          <Button
            size="sm"
            disabled={busy}
            onClick={() => (mode === "video" ? startRecording() : captureScreenshot())}
          >
            {mode === "video" ? "Start Recording" : "Capture"}
          </Button>
        </div>
    </div>
  );
}

// DimMask paints four black panels around the crop so the crop interior stays
// bright (the frozen still shows through unmodified). Using panels — rather than a
// single box-shadow — keeps the hole crisp at sharp corners and avoids a giant
// blur layer.
function DimMask({
  crop,
  monW,
  monH,
}: {
  crop: CssRect;
  monW: number;
  monH: number;
}) {
  const dim = "absolute bg-black/45";
  const right = crop.left + crop.width;
  const bottom = crop.top + crop.height;
  return (
    <div className="pointer-events-none absolute inset-0">
      {/* top */}
      <div className={dim} style={{ left: 0, top: 0, width: monW, height: crop.top }} />
      {/* bottom */}
      <div
        className={dim}
        style={{ left: 0, top: bottom, width: monW, height: Math.max(0, monH - bottom) }}
      />
      {/* left */}
      <div
        className={dim}
        style={{ left: 0, top: crop.top, width: crop.left, height: crop.height }}
      />
      {/* right */}
      <div
        className={dim}
        style={{
          left: right,
          top: crop.top,
          width: Math.max(0, monW - right),
          height: crop.height,
        }}
      />
    </div>
  );
}

// ----- pure geometry helpers -----

// seedCrop derives the initial CSS crop. `restored` is the monitor-local PHYSICAL
// crop from the query (0,0,0,0 == none). We divide by scale to get CSS px and
// validate it fits the monitor; otherwise we center a default (half the monitor).
function seedCrop(restored: Rect, scale: number, monW: number, monH: number): CssRect {
  const s = scale > 0 ? scale : 1;
  if (restored.w > 0 && restored.h > 0) {
    const c: CssRect = {
      left: restored.x / s,
      top: restored.y / s,
      width: restored.w / s,
      height: restored.h / s,
    };
    if (
      c.left >= 0 &&
      c.top >= 0 &&
      c.width >= MIN_CROP &&
      c.height >= MIN_CROP &&
      c.left + c.width <= monW + 1 &&
      c.top + c.height <= monH + 1
    ) {
      return clampCrop(c, monW, monH);
    }
  }
  return centeredCrop(monW, monH);
}

function centeredCrop(monW: number, monH: number): CssRect {
  const width = Math.round(monW / 2);
  const height = Math.round(monH / 2);
  return {
    left: Math.round((monW - width) / 2),
    top: Math.round((monH - height) / 2),
    width,
    height,
  };
}

// clampCrop keeps the crop fully inside the monitor (cross-monitor deferred).
function clampCrop(c: CssRect, monW: number, monH: number): CssRect {
  const width = Math.min(Math.max(MIN_CROP, c.width), monW);
  const height = Math.min(Math.max(MIN_CROP, c.height), monH);
  const left = Math.min(Math.max(0, c.left), monW - width);
  const top = Math.min(Math.max(0, c.top), monH - height);
  return { left, top, width, height };
}

// moveCrop translates the body, clamped to the monitor.
function moveCrop(start: CssRect, dx: number, dy: number, monW: number, monH: number): CssRect {
  return clampCrop(
    { ...start, left: start.left + dx, top: start.top + dy },
    monW,
    monH,
  );
}

// resizeCrop adjusts edges per the dragged handle, enforcing MIN_CROP and clamping
// each moved edge to the monitor bounds.
function resizeCrop(
  start: CssRect,
  handle: Handle,
  dx: number,
  dy: number,
  monW: number,
  monH: number,
): CssRect {
  let left = start.left;
  let top = start.top;
  let right = start.left + start.width;
  let bottom = start.top + start.height;

  if (handle.includes("w")) left = clamp(start.left + dx, 0, right - MIN_CROP);
  if (handle.includes("e")) right = clamp(right + dx, left + MIN_CROP, monW);
  if (handle.includes("n")) top = clamp(start.top + dy, 0, bottom - MIN_CROP);
  if (handle.includes("s")) bottom = clamp(bottom + dy, top + MIN_CROP, monH);

  return { left, top, width: right - left, height: bottom - top };
}

function clamp(v: number, lo: number, hi: number): number {
  return Math.min(Math.max(v, lo), hi);
}

// cropToPhysical is the LOCKED DPI math (mirrors capture.CropToPhysical in Go).
// Round the crop EDGES once, clamp the far edges to the monitor's physical size,
// and reuse for emit (virtual-desktop physical Rect), sub (monitor-local physical
// crop of the frozen PNG), and the badge.
//
// EDGE-based (not width-based) on purpose: the CSS extent (innerWidth) is the
// Wails DIP Bounds = ceil(physical/scale). round(width*scale) on a ceil'd DIP can
// land 1px PAST the native monitor (e.g. 2560@150%: DIP 1707 -> round(1707*1.5)
// = 2561 > 2560), making the badge/emit/recorded Rect overshoot the frozen still.
// Rounding left and right independently and clamping the right edge to mw (and
// bottom to mh) guarantees the result fits the monitor and the saved PNG.
//   rl=round(cl*s); rr=min(round((cl+cw)*s), mw); rw=rr-rl  (and likewise y/h)
//   sub  = { rl, rt, rw, rh }            (crops the frozen still, monitor-local)
//   emit = { bx+rl, by+rt, rw, rh }      (CaptureRequest.Rect, virtual-desktop)
//   badge = rw × rh = sub.w × sub.h
function cropToPhysical(
  c: CssRect,
  scale: number,
  bx: number,
  by: number,
  mw: number,
  mh: number,
): { emit: Rect; sub: Rect } {
  const s = scale > 0 ? scale : 1;
  const rl = Math.round(c.left * s);
  const rt = Math.round(c.top * s);
  let rr = Math.round((c.left + c.width) * s);
  let rb = Math.round((c.top + c.height) * s);
  if (mw > 0 && rr > mw) rr = mw;
  if (mh > 0 && rb > mh) rb = mh;
  const rw = rr - rl;
  const rh = rb - rt;
  return {
    sub: { x: rl, y: rt, w: rw, h: rh },
    emit: { x: bx + rl, y: by + rt, w: rw, h: rh },
  };
}

// handleStyle positions + sizes one of the 8 resize handles, centered on its
// edge/corner. Cursor reflects the resize axis.
function handleStyle(h: Handle): React.CSSProperties {
  const s: React.CSSProperties = {
    width: HANDLE_HIT,
    height: HANDLE_HIT,
    transform: "translate(-50%, -50%)",
    cursor: handleCursor(h),
  };
  if (h.includes("n")) s.top = 0;
  if (h.includes("s")) s.top = "100%";
  if (h === "e" || h === "w") s.top = "50%";
  if (h.includes("w")) s.left = 0;
  if (h.includes("e")) s.left = "100%";
  if (h === "n" || h === "s") s.left = "50%";
  return s;
}

function handleCursor(h: Handle): string {
  switch (h) {
    case "n":
    case "s":
      return "ns-resize";
    case "e":
    case "w":
      return "ew-resize";
    case "nw":
    case "se":
      return "nwse-resize";
    case "ne":
    case "sw":
      return "nesw-resize";
  }
}
