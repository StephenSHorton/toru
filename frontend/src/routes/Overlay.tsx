// OVERLAY V2 — single-surface morph + instant re-engage.
//
// One instance renders per monitor (one Wails window each). Each window is a
// frameless, always-on-top, OPAQUE window covering its monitor's full DIP bounds.
// The windows are created ONCE (pre-warmed / lazily) and kept ALIVE+HIDDEN between
// captures, so this React tree stays MOUNTED across captures — listeners bind once.
//
// STATE MACHINE: 'capture' | 'edit' (idle == the Wails window is Hidden, no React
// state). ALL per-session data arrives via Go->JS events, NOT the URL:
//   • overlay:engage (MonitorSession)  -> reset to capture mode w/ the fresh
//     backdrop (cache-busted /__shot JPEG). Fires on every BeginSession/re-engage.
//   • overlay:edit (OverlayEditPayload)-> enter the single-surface morph: load the
//     served crop PNG as the editor base image, size the Konva stage to the crop's
//     CSS rect, position it where the bright region was, morph the dock into the
//     annotation Toolbar. No separate editor window is ever opened for screenshots.
// Both events broadcast to EVERY overlay window; each filters by its URL ?mon=.
//
// CAPTURE: the ACTIVE window (one at a time, synced via overlay:activeMonitor;
// starts on the primary, click any monitor to claim it) draws the crop
// rectangle (body drag + 8 handles, min-size, CLAMPED to the monitor) or the
// Full-screen ring; Capture calls OverlayService.EnterEdit (Go crops the
// FROZEN in-memory pixels -> /__file PNG -> emits overlay:edit). EDIT:
// the embedded EditorCanvas + overlays annotate in place; Copy/Save export the
// native-resolution annotated PNG (exportActions math unchanged). Done/Esc hides
// the overlay back to the tray (windows kept alive for instant re-engage).
//
// DPI: all crop->physical math lives in cropToPhysical() using the LOCKED formulas
// — round ONCE, reuse for the emitted Rect, the frozen-still sub-rect, and the
// on-screen badge so they are byte-identical.

import { useCallback, useEffect, useRef, useState } from "react";
import type Konva from "konva";
import { Events as WailsEvents } from "@wailsio/runtime";
import { Button } from "@/components/ui/button";
import { Camera, Maximize, Video, X } from "lucide-react";
import { OverlayService } from "@/lib/api";
import {
  parseOverlayQuery,
  Events,
  type Rect,
  type CaptureRequest,
  type MonitorSession,
  type OverlayEditPayload,
} from "@/lib/contract";
import { EditorCanvas } from "@/editor/EditorCanvas";
import { Toolbar } from "@/editor/Toolbar";
import { useEditorStore } from "@/editor/store";
import { useEditorKeyboard } from "@/editor/useEditorKeyboard";
import { useClipboardPaste } from "@/editor/useClipboardPaste";
import { TextEditingOverlay, resetTextEditSession } from "@/editor/tools/text";
import { CropOverlay, resetCropDraft } from "@/editor/tools/crop";
import { setStageSize } from "@/editor/viewStore";

// resetEditor clears the editor sub-stores that loadBaseImage does NOT touch — the
// crop tool's module-level draft and the text-editing session — and resets the
// active tool to 'select'. loadBaseImage already resets the Zustand SCENE store
// (nodes/history/selection); these two are separate module singletons that would
// otherwise survive a 'New' (capture-mode hides them, but the next overlay:edit
// only resets the scene) and render a stale crop rect / textarea over the new
// image. Call it on every engage and at the top of every edit.
function resetEditor(): void {
  resetCropDraft();
  resetTextEditSession();
  useEditorStore.getState().setTool("select");
}

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
  // Read ONCE for the stable per-window identity (mon/primary). All session data
  // now arrives via overlay:engage — the URL no longer carries still/crop.
  const q = parseOverlayQuery(window.location.search);

  const [mode, setMode] = useState<"capture" | "edit">("capture");
  const [tool, setToolMode] = useState<"screenshot" | "video">("screenshot");
  const [busy, setBusy] = useState(false);

  // Per-session data, seeded empty and replaced by overlay:engage / overlay:edit.
  const [session, setSession] = useState<MonitorSession | null>(null);
  const [editPayload, setEditPayload] = useState<OverlayEditPayload | null>(null);

  const stageRef = useRef<Konva.Stage>(null);
  const loadBaseImage = useEditorStore((s) => s.loadBaseImage);

  // The monitor's CSS size IS the window viewport (each window already covers the
  // full monitor in DIP), so layout uses innerWidth/innerHeight.
  const monW = window.innerWidth;
  const monH = window.innerHeight;

  // The crop rectangle (capture mode). Seeded centered; reset from the restored
  // crop on each overlay:engage.
  const [crop, setCrop] = useState<CssRect>(() => centeredCrop(monW, monH));

  // Exactly ONE monitor owns the capture selection at a time (starts on the
  // primary each engage). Clicking an inactive monitor claims it via a Go
  // broadcast; every window — including the claimer — syncs off the same
  // event, so two crops can never look simultaneously active.
  const [active, setActive] = useState(q.primary);
  const claimActive = useCallback(() => {
    void OverlayService.SetActiveMonitor(q.mon);
  }, [q.mon]);

  // prevRegionCrop remembers the region crop across the Full-screen toggle so
  // toggling OFF full screen restores what the user had, not a default.
  const prevRegionCrop = useRef<CssRect | null>(null);

  const saveTimer = useRef<number | null>(null);

  // Editor keyboard + clipboard paste — mounted once, GATED to edit mode. In
  // capture mode the editor canvas isn't rendered, so leaving them live would let a
  // tool key / Ctrl+V mutate a hidden store; gating keeps capture mode clean. In
  // edit mode, an Esc with nothing left to deselect hides the overlay to the tray
  // (the spec's "Done / Esc from edit mode -> hide"); a first Esc still deselects.
  const finishEdit = useCallback(() => void OverlayService.Finish(), []);
  useEditorKeyboard(mode === "edit", finishEdit);
  useClipboardPaste(mode === "edit");

  // applyEngage resets THIS window to capture mode with a fresh session, then ACKs
  // Go (OverlayReady) ONLY AFTER the new backdrop JPEG has DECODED — so Go reveals
  // the window with the fresh backdrop already painted, never the prior session's
  // stale DOM (the HIGH stale-flash fix) and never a blank/black first-capture
  // screen (the WebView2-still-loading fix). Auxiliary editor sub-stores (crop
  // draft, text-edit session) are module singletons loadBaseImage does NOT touch,
  // so we reset them here too, or a New from a pending crop/text session would leak
  // a stale overlay into the NEXT edit (the stale-edit-state fix).
  const applyEngage = useCallback(
    (d: MonitorSession) => {
      resetEditor();
      setSession(d);
      setCrop(seedCrop(d.crop, d.scale, monW, monH)); // reset crop from restored
      setEditPayload(null);
      setMode("capture"); // a window last in edit returns to capture
      setActive(q.primary); // selection resets to the primary each engage
      prevRegionCrop.current = null;
      // Preload + decode the cache-busted backdrop, THEN ACK so Go can Show.
      const img = new window.Image();
      const ack = () => void OverlayService.OverlayReady(q.mon);
      img.onload = ack;
      img.onerror = ack; // never strand the window hidden on a decode hiccup
      img.src = d.stillUrl;
    },
    [q.mon, monW, monH],
  );

  // ----- Go->JS event wiring (bind ONCE; stable deps) -----
  useEffect(() => {
    const offEngage = WailsEvents.On(Events.OverlayEngage, (ev) => {
      const d = ev.data as MonitorSession;
      if (d.monitorId !== q.mon) return; // filter by this window
      applyEngage(d);
    });

    // Selection sync: exactly one monitor owns the capture chrome at a time.
    const offActive = WailsEvents.On("overlay:activeMonitor", (ev) => {
      const mon = Array.isArray(ev.data) ? ev.data[0] : ev.data;
      setActive(mon === q.mon);
    });

    const offEdit = WailsEvents.On(Events.OverlayEdit, (ev) => {
      const d = ev.data as OverlayEditPayload;
      if (d.monitorId !== q.mon) return; // no-op on non-target windows
      // Clear auxiliary editor stores BEFORE entering edit so a stale crop draft /
      // text session from a prior capture never renders over the NEW image (in old
      // image coords). loadBaseImage already resets the scene store; resetEditor
      // covers the two module singletons it can't reach + the active tool.
      resetEditor();
      const img = new window.Image();
      img.onload = () => {
        // Stage size BEFORE loadBaseImage so the first fit uses the correct size
        // (EditorCanvas's resetFit effect reads stageW/stageH on base-dim change).
        setStageSize(d.cssW, d.cssH);
        loadBaseImage(d.cropUrl, img.naturalWidth, img.naturalHeight);
        setEditPayload(d);
        setMode("edit");
      };
      img.src = d.cropUrl;
    });

    // Defense-in-depth: a window that finished navigating only AFTER BeginSession
    // broadcast overlay:engage (the FIRST capture) misses the event. Pull the
    // current engage for this monitor on mount; if Go has one pending, apply it.
    void OverlayService.RequestEngage(q.mon).then((d) => {
      if (d && d.monitorId === q.mon) applyEngage(d);
    });

    return () => {
      offEngage();
      offActive();
      offEdit();
    };
  }, [q.mon, loadBaseImage, applyEngage]);

  // ----- persistence + actions (capture mode) -----

  // Persist the crop (monitor-local PHYSICAL px) on drag/resize end, debounced.
  const persistCrop = useCallback(
    (c: CssRect) => {
      if (!session) return;
      if (saveTimer.current != null) window.clearTimeout(saveTimer.current);
      saveTimer.current = window.setTimeout(() => {
        const { sub } = cropToPhysical(
          c,
          session.scale,
          session.x,
          session.y,
          session.w,
          session.h,
        );
        void OverlayService.SaveCrop(q.mon, sub);
      }, SAVE_DEBOUNCE_MS);
    },
    [session, q.mon],
  );

  // Capture-mode Cancel/Esc: hide the overlay (Go keeps windows alive) + tray.
  const cancel = useCallback(() => {
    void OverlayService.Cancel();
  }, []);

  // Full screen is a TOGGLE: on snaps the crop to the entire monitor; off
  // restores the region crop the user had before (or the seeded one). Without
  // the toggle-off there is no way back to region mode.
  const isFullScreen =
    crop.left <= 0 &&
    crop.top <= 0 &&
    crop.width >= monW &&
    crop.height >= monH;
  const toggleFullScreen = useCallback(() => {
    if (isFullScreen) {
      const restored = prevRegionCrop.current ?? centeredCrop(monW, monH);
      setCrop(restored);
      persistCrop(restored);
    } else {
      prevRegionCrop.current = crop;
      const full = { left: 0, top: 0, width: monW, height: monH };
      setCrop(full);
      persistCrop(full);
    }
  }, [isFullScreen, crop, monW, monH, persistCrop]);

  // Screenshot Capture -> EnterEdit (Go crops the FROZEN pixels and emits
  // overlay:edit, which flips THIS window to edit mode). We do NOT optimistically
  // setMode('edit') — we wait for the event so the served crop URL + authoritative
  // CSS geometry (the stage size) arrive together. We do NOT call CommitScreenshot.
  const captureScreenshot = useCallback(async () => {
    if (busy || !session) return;
    setBusy(true);
    const { sub } = cropToPhysical(
      crop,
      session.scale,
      session.x,
      session.y,
      session.w,
      session.h,
    );
    try {
      await OverlayService.EnterEdit(
        q.mon,
        sub,
        Math.round(crop.left),
        Math.round(crop.top),
        Math.round(crop.width),
        Math.round(crop.height),
      );
    } finally {
      setBusy(false);
    }
  }, [busy, crop, session, q.mon]);

  // Record: Go hides the overlay FIRST, then records the live region.
  const startRecording = useCallback(async () => {
    if (busy || !session) return;
    setBusy(true);
    const { emit } = cropToPhysical(
      crop,
      session.scale,
      session.x,
      session.y,
      session.w,
      session.h,
    );
    const req: CaptureRequest = {
      mode: "video",
      sub: isFullScreen ? "fullscreen" : "region",
      monitorId: q.mon,
      rect: emit,
      dpiScale: session.scale,
      includeCursor: true,
      countdownSec: 0,
      copyOnCommit: false,
    };
    try {
      await OverlayService.StartRecording(req);
    } finally {
      setBusy(false);
    }
  }, [busy, crop, session, q.mon, isFullScreen]);

  // Window-level Esc: ONLY cancels in capture mode. In edit mode, useEditorKeyboard
  // owns Esc (clear selection / back to select tool); the Toolbar Done button is
  // the explicit hide-to-tray from edit mode.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && mode === "capture") {
        e.preventDefault();
        cancel();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [mode, cancel]);

  // ----- interactive crop (ACTIVE monitor, capture mode) -----
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

  // ===== EDIT MODE =====
  if (mode === "edit" && editPayload) {
    return (
      <div className="relative h-screen w-screen overflow-hidden bg-black">
        {/* Keep the region's surroundings dimmed; the bright hole == the region. */}
        <DimMask
          crop={{
            left: editPayload.cssLeft,
            top: editPayload.cssTop,
            width: editPayload.cssW,
            height: editPayload.cssH,
          }}
          monW={monW}
          monH={monH}
        />

        {/* The embedded editor, positioned at the region origin so the stage sits
            exactly where the bright crop was -> "annotate in place". This div is the
            positioned element so TextEditingOverlay's getBoundingClientRect()
            returns the region's true viewport origin (its textarea is fixed). */}
        <div
          className="absolute"
          style={{
            left: editPayload.cssLeft,
            top: editPayload.cssTop,
            width: editPayload.cssW,
            height: editPayload.cssH,
          }}
        >
          <EditorCanvas stageRef={stageRef} />
          <CropOverlay />
          <TextEditingOverlay stageRef={stageRef} />
        </div>

        {/* MORPHED DOCK: same bottom-center anchor as the capture pill; contents
            are the editor Toolbar (self-positions bottom-4 left-1/2). */}
        <Toolbar
          stageRef={stageRef}
          onNewCapture={() => void OverlayService.BeginSession()}
          onDone={finishEdit}
        />
      </div>
    );
  }

  // ===== CAPTURE MODE =====
  // Backdrop + scale/origin come from `session` (empty until the first engage).
  const backdrop = session?.stillUrl ?? "";
  // Badge values are the SAME rounded physical numbers as the saved crop.
  const { sub } = session
    ? cropToPhysical(crop, session.scale, session.x, session.y, session.w, session.h)
    : { sub: { x: 0, y: 0, w: 0, h: 0 } as Rect };

  return (
    <div className="relative h-screen w-screen select-none overflow-hidden bg-black">
      {/* Frozen still backdrop (fast JPEG), painted fullscreen (1:1 in DIP). */}
      {backdrop ? (
        <img
          src={backdrop}
          alt=""
          draggable={false}
          className="pointer-events-none absolute inset-0 h-full w-full"
          style={{ objectFit: "fill" }}
        />
      ) : null}

      {/* Inactive monitors are dim-only with a click-to-select hint: only the
          ACTIVE monitor shows crop + pill, so there is never any ambiguity
          about what Capture/Record will grab. */}
      {!active ? (
        <div className="absolute inset-0 cursor-pointer" onPointerDown={claimActive}>
          <div className="pointer-events-none absolute inset-0 bg-black/45" />
          <div className="frost pointer-events-none absolute bottom-4 left-1/2 -translate-x-1/2 px-3 py-1.5 text-xs text-muted-foreground">
            Click to capture this screen
          </div>
        </div>
      ) : null}

      {/* Four-panel mask with a transparent crop hole — ACTIVE monitor only. */}
      {active ? <DimMask crop={crop} monW={monW} monH={monH} /> : null}

      {/* Interactive crop rectangle — ACTIVE monitor, region mode. */}
      {active && !isFullScreen ? (
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
      ) : active ? (
        /* Full-screen mode: whole-monitor ring + badge; click Full screen
           again (or this badge) to return to the region crop. */
        <div className="pointer-events-none absolute inset-0 ring-2 ring-inset ring-primary/90">
          <div className="frost absolute left-1/2 top-3 -translate-x-1/2 px-2 py-0.5 text-[11px] tabular-nums">
            Entire screen · {sub.w} × {sub.h}
          </div>
        </div>
      ) : null}

      {/* frosted control pill — ACTIVE monitor only. bottom-4 to match the
          Toolbar's anchor so the capture->edit swap reads as a morph. */}
      {active ? (
        <div className="frost absolute bottom-4 left-1/2 flex -translate-x-1/2 items-center gap-1 p-1.5">
          <Button
            size="sm"
            variant={tool === "screenshot" ? "default" : "ghost"}
            onClick={() => setToolMode("screenshot")}
          >
            <Camera /> Screenshot
          </Button>
          <Button
            size="sm"
            variant={tool === "video" ? "default" : "ghost"}
            onClick={() => setToolMode("video")}
          >
            <Video /> Record
          </Button>
          <div className="mx-1 h-5 w-px bg-border" />
          <Button
            size="sm"
            variant={isFullScreen ? "default" : "ghost"}
            onClick={toggleFullScreen}
            title={isFullScreen ? "Back to region selection" : "Capture the entire monitor"}
          >
            <Maximize /> Full screen
          </Button>
          <div className="mx-1 h-5 w-px bg-border" />
          <Button size="sm" variant="ghost" onClick={cancel}>
            <X /> Cancel
          </Button>
          <Button
            size="sm"
            disabled={busy || !session}
            onClick={() => (tool === "video" ? startRecording() : captureScreenshot())}
          >
            {tool === "video" ? "Start Recording" : "Capture"}
          </Button>
        </div>
      ) : null}
    </div>
  );
}

// DimMask paints four black panels around the crop so the crop interior stays
// bright (the backdrop / editor shows through). Panels keep the hole crisp.
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
// crop (0,0,0,0 == none). We divide by scale to get CSS px and validate it fits
// the monitor; otherwise we center a default (half the monitor).
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
// crop of the frozen pixels), and the badge.
//
// EDGE-based (not width-based) on purpose: the CSS extent (innerWidth) is the
// Wails DIP Bounds = ceil(physical/scale). round(width*scale) on a ceil'd DIP can
// land 1px PAST the native monitor; rounding left/right independently and clamping
// the right edge to mw (and bottom to mh) guarantees the result fits the monitor.
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
