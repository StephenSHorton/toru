// OVERLAY V2 — single-surface morph + instant re-engage + SHARED cross-monitor crop.
//
// One instance renders per monitor (one Wails window each). Each window is a
// frameless, always-on-top, TRANSPARENT window covering its monitor's full DIP
// bounds. The windows are created ONCE (pre-warmed / lazily) and kept ALIVE+HIDDEN
// between captures, so this React tree stays MOUNTED across captures — listeners
// bind once.
//
// SHARED CROP: there is ONE selection rectangle for the WHOLE virtual desktop,
// stored in VIRTUAL-DESKTOP PHYSICAL px (origin = primary top-left; monitors
// left/above carry NEGATIVE x/y). Every window receives the same rect and renders
// only its SLICE of it (clipped by the window's own bounds), so a crop can STRADDLE
// two monitors and read as one continuous box across the seam. While one window
// owns an in-progress drag it broadcasts the rect (rAF-throttled) via
// OverlayService.SetSharedCrop -> overlay:cropRect; every other window applies it.
//
// STATE MACHINE: 'capture' | 'edit' (idle == the Wails window is Hidden).
//   • overlay:engage (MonitorSession)  -> reset to capture mode; seed the shared
//     crop from session.region (the persisted/centered virtual rect).
//   • overlay:cropRect (Rect)          -> apply the shared crop relayed by whichever
//     window is dragging.
//   • overlay:edit (OverlayEditPayload)-> single-surface morph (ONLY for a crop that
//     fits one monitor). A STRADDLE capture instead stitches in Go and opens the
//     standalone editor window (EnterEditMulti) — no window can morph across a seam.
//
// CAPTURE: the DOMINANT monitor (largest overlap with the crop) owns the control
// pill; you drag the crop body/handles across monitors, or click a non-crop monitor
// to bring the selection there. Screenshot -> EnterEdit (single) / EnterEditMulti
// (straddle); Record is ENFORCED single-monitor (ddagrab can't span) so the crop
// snaps to one monitor in video mode.
//
// DPI: the shared crop is authored directly in PHYSICAL px, so it never multiplies a
// ceil'd DIP extent — each window converts to its own CSS only for rendering, using
// its own scale, so the seam lines up under mixed DPI.

import { useCallback, useEffect, useRef, useState } from "react";
import type Konva from "konva";
import { Events as WailsEvents } from "@wailsio/runtime";
import { Button } from "@/components/ui/button";
import {
  AppWindow,
  Camera,
  Maximize,
  Snowflake,
  Video,
  Volume2,
  VolumeX,
  X,
  Zap,
} from "lucide-react";
import { OverlayService } from "@/lib/api";
import type { WindowInfo } from "../../bindings/github.com/StephenSHorton/toru/internal/capture/models";
import {
  parseOverlayQuery,
  Events,
  type Rect,
  type ScreenInfo,
  type CaptureRequest,
  type MonitorSession,
  type OverlayEditPayload,
} from "@/lib/contract";
import { AudioConfig, type AudioSession } from "@/lib/api";
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
// otherwise survive a 'New' and render a stale crop rect / textarea over the new
// image. Call it on every engage and at the top of every edit.
function resetEditor(): void {
  resetCropDraft();
  resetTextEditSession();
  useEditorStore.getState().setTool("select");
}

const MIN_PHYS = 24; // minimum crop size, PHYSICAL px (drag/resize floor)
const HANDLE_HIT = 14; // resize-handle hit area, CSS px
const SAVE_DEBOUNCE_MS = 300;

// CssRect is a rectangle in CSS px within this window's viewport (render-only).
interface CssRect {
  left: number;
  top: number;
  width: number;
  height: number;
}

type Handle = "nw" | "n" | "ne" | "w" | "e" | "sw" | "s" | "se";
const HANDLES: Handle[] = ["nw", "n", "ne", "w", "e", "sw", "s", "se"];

export default function Overlay() {
  // Read ONCE for the stable per-window identity. Carries this monitor's physical
  // origin (bx,by) + size (mw,mh) + scale so the first paint works pre-engage.
  const q = parseOverlayQuery(window.location.search);

  const [mode, setMode] = useState<"capture" | "edit">("capture");
  const [tool, setToolMode] = useState<"screenshot" | "video">("screenshot");
  // toolRef lets applyEngage (a stable listener callback) read the current tool
  // without listing it as a dep (which would rebind the bind-once event effect).
  const toolRef = useRef(tool);
  toolRef.current = tool;
  const [busy, setBusy] = useState(false);
  // Capture target: freeform region (default), full monitor, or a picked app window.
  // Window mode snaps the shared crop to the chosen window's virtual-desktop rect
  // so Capture/Record reuse the existing region pipeline.
  const [target, setTarget] = useState<"region" | "window" | "fullscreen">("region");
  const [windowOpen, setWindowOpen] = useState(false);
  const [windows, setWindows] = useState<WindowInfo[]>([]);
  const [selectedHwnd, setSelectedHwnd] = useState<number | null>(null);

  // Per-session data, seeded empty and replaced by overlay:engage / overlay:edit.
  const [session, setSession] = useState<MonitorSession | null>(null);
  const [editPayload, setEditPayload] = useState<OverlayEditPayload | null>(null);

  const stageRef = useRef<Konva.Stage>(null);
  const loadBaseImage = useEditorStore((s) => s.loadBaseImage);

  // The monitor's CSS size IS the window viewport (each window already covers the
  // full monitor in DIP), so layout uses innerWidth/innerHeight.
  const monW = window.innerWidth;
  const monH = window.innerHeight;

  // THIS window's monitor as a ScreenInfo (from the live session, else the URL).
  const self: ScreenInfo = session
    ? {
        id: q.mon,
        x: session.x,
        y: session.y,
        w: session.w,
        h: session.h,
        scaleFactor: session.scale,
        isPrimary: q.primary,
      }
    : { id: q.mon, x: q.bx, y: q.by, w: q.mw, h: q.mh, scaleFactor: q.scale, isPrimary: q.primary };
  const selfRef = useRef(self);
  selfRef.current = self;

  // The SHARED crop in virtual-desktop PHYSICAL px. Seeded from session.region on
  // each engage; updated live by drags here and by overlay:cropRect from elsewhere.
  const [vcrop, setVcrop] = useState<Rect>(() => ({
    x: q.bx + Math.round(q.mw / 4),
    y: q.by + Math.round(q.mh / 4),
    w: Math.round(q.mw / 2),
    h: Math.round(q.mh / 2),
  }));
  const vcropRef = useRef(vcrop);
  vcropRef.current = vcrop;

  // The full monitor layout (all screens) — used to clamp the crop to the desktop
  // and to decide which window owns the pill. Fetched on mount + each engage.
  const [screens, setScreens] = useState<ScreenInfo[]>([]);
  const screensRef = useRef<ScreenInfo[]>([]);
  const loadScreens = useCallback(async () => {
    try {
      const s = await OverlayService.ListScreens();
      const list = (s ?? []) as ScreenInfo[];
      screensRef.current = list;
      setScreens(list);
    } catch {
      // ListScreens shouldn't fail; if it does we fall back to self-only layout.
    }
  }, []);

  // draggingRef: while THIS window owns an active drag, ignore the echo of our own
  // SetSharedCrop broadcast so the round-trip can't fight our local state.
  const draggingRef = useRef(false);
  // rAF-coalesced broadcast of the in-drag crop to the other windows.
  const rafRef = useRef<number | null>(null);
  const pendingRef = useRef<Rect | null>(null);
  // prevRegion remembers the region crop across the Full-screen toggle.
  const prevRegion = useRef<Rect | null>(null);
  const saveTimer = useRef<number | null>(null);

  // Audio capture is a privacy-sensitive OPT-IN, per SOURCE (unchanged).
  const [audioSystem, setAudioSystem] = useState(
    () => window.localStorage.getItem("toru.audio.system") === "1",
  );
  const [audioMic, setAudioMic] = useState(
    () => window.localStorage.getItem("toru.audio.mic") ?? "",
  );
  const [audioApps, setAudioApps] = useState<number[]>([]);
  const [audioOpen, setAudioOpen] = useState(false);
  const [sessions, setSessions] = useState<AudioSession[]>([]);
  const [mics, setMics] = useState<string[]>([]);
  useEffect(() => {
    window.localStorage.setItem("toru.audio.system", audioSystem ? "1" : "0");
    window.localStorage.setItem("toru.audio.mic", audioMic);
    void OverlayService.SetAudioSources(
      new AudioConfig({ system: audioSystem, appPids: audioApps, micDevice: audioMic }),
    );
  }, [audioSystem, audioMic, audioApps]);
  useEffect(() => {
    if (!audioOpen) return;
    void OverlayService.ListAudioSessions().then((s) => setSessions(s ?? []));
    void OverlayService.ListMicrophones().then((m) => setMics(m ?? []));
  }, [audioOpen]);
  const audioCount = (audioSystem ? 1 : 0) + (audioMic ? 1 : 0) + audioApps.length;

  // Editor keyboard + clipboard paste — mounted once, GATED to edit mode.
  const finishEdit = useCallback(() => void OverlayService.Finish(), []);
  useEditorKeyboard(mode === "edit", finishEdit);
  useClipboardPaste(mode === "edit");

  // ----- shared-crop broadcast + persistence -----

  const broadcastNow = useCallback((vr: Rect) => {
    if (rafRef.current != null) {
      cancelAnimationFrame(rafRef.current);
      rafRef.current = null;
    }
    pendingRef.current = null;
    void OverlayService.SetSharedCrop(vr);
  }, []);

  const scheduleBroadcast = useCallback((vr: Rect) => {
    pendingRef.current = vr;
    if (rafRef.current != null) return;
    rafRef.current = requestAnimationFrame(() => {
      rafRef.current = null;
      const v = pendingRef.current;
      pendingRef.current = null;
      if (v) void OverlayService.SetSharedCrop(v);
    });
  }, []);

  const persistVcrop = useCallback((vr: Rect) => {
    if (saveTimer.current != null) window.clearTimeout(saveTimer.current);
    saveTimer.current = window.setTimeout(() => {
      void OverlayService.SaveSharedCrop(vr);
    }, SAVE_DEBOUNCE_MS);
  }, []);

  // applyEngage resets THIS window to capture mode and seeds the shared crop from
  // the engage's region. ACK gating (OverlayReady) is unchanged: frozen waits for
  // the backdrop to decode; live acks after a painted frame.
  const applyEngage = useCallback(
    (d: MonitorSession) => {
      resetEditor();
      // Drop any pending broadcast / save timer from the PRIOR session so a stale
      // rAF/debounce can't fire a crop or save into this fresh one.
      if (rafRef.current != null) {
        cancelAnimationFrame(rafRef.current);
        rafRef.current = null;
      }
      pendingRef.current = null;
      if (saveTimer.current != null) {
        window.clearTimeout(saveTimer.current);
        saveTimer.current = null;
      }
      setSession(d);
      // Seed the shared crop; if we re-engage while ALREADY in video mode, the seeded
      // region may straddle (persisted regions can) — confine it to one monitor now so
      // the displayed crop matches what video will record (recording also clamps).
      let seed = seedVcrop(d.region, screensRef.current);
      if (toolRef.current === "video" && screensRef.current.length) {
        const m = dominantScreen(seed, screensRef.current);
        if (m) seed = fitToScreen(seed, m);
      }
      setVcrop(seed);
      setEditPayload(null);
      setMode("capture");
      prevRegion.current = null;
      void loadScreens(); // refresh the layout (topology may have changed)
      const ack = () => void OverlayService.OverlayReady(q.mon);
      if (!d.stillUrl) {
        requestAnimationFrame(() => requestAnimationFrame(ack));
      } else {
        const img = new window.Image();
        img.onload = ack;
        img.onerror = ack;
        img.src = d.stillUrl;
      }
    },
    [q.mon, loadScreens],
  );

  // ----- Go->JS event wiring (bind ONCE; stable deps) -----
  useEffect(() => {
    void loadScreens();

    const offEngage = WailsEvents.On(Events.OverlayEngage, (ev) => {
      const d = ev.data as MonitorSession;
      if (d.monitorId !== q.mon) return;
      applyEngage(d);
    });

    // Shared-crop relay: every window applies the one rect and renders its slice.
    // Ignore our own echo while we own the drag (draggingRef) so the round-trip
    // can't stutter our local update.
    const offCrop = WailsEvents.On(Events.OverlayCropRect, (ev) => {
      if (draggingRef.current) return;
      const r = (Array.isArray(ev.data) ? ev.data[0] : ev.data) as Rect;
      if (r && typeof r.w === "number" && typeof r.h === "number") setVcrop(r);
    });

    const offEdit = WailsEvents.On(Events.OverlayEdit, (ev) => {
      const d = ev.data as OverlayEditPayload;
      if (d.monitorId !== q.mon) return;
      resetEditor();
      const img = new window.Image();
      img.onload = () => {
        setStageSize(d.cssW, d.cssH);
        loadBaseImage(d.cropUrl, img.naturalWidth, img.naturalHeight);
        setEditPayload(d);
        setMode("edit");
        void OverlayService.EditReady(q.mon);
      };
      img.onerror = () => void OverlayService.EditReady(q.mon);
      img.src = d.cropUrl;
    });

    void OverlayService.RequestEngage(q.mon).then((d) => {
      if (d && d.monitorId === q.mon) applyEngage(d);
    });

    return () => {
      offEngage();
      offCrop();
      offEdit();
    };
  }, [q.mon, loadBaseImage, applyEngage, loadScreens]);

  // Cancel any in-flight broadcast/save timer on teardown so a queued rAF/debounce
  // can't fire after the tree is gone (defensive — these windows are normally kept
  // alive, so this mainly matters under hot-reload / a real unmount).
  useEffect(() => {
    return () => {
      if (rafRef.current != null) cancelAnimationFrame(rafRef.current);
      if (saveTimer.current != null) window.clearTimeout(saveTimer.current);
    };
  }, []);

  // freeze/live reflect how THIS engage was rendered (unchanged).
  const freeze = session?.freeze ?? true;
  const live = session != null && !session.freeze;

  const toggleFreeze = useCallback(async () => {
    if (busy) return;
    setBusy(true);
    try {
      await OverlayService.SetFreezeOnCapture(!freeze);
      await OverlayService.BeginSession();
    } finally {
      setBusy(false);
    }
  }, [busy, freeze]);

  // When switching to video, the crop must be single-monitor (ddagrab can't span):
  // snap a straddling/oversized crop into its dominant monitor. Depends on `screens`
  // too so a switch-to-video that raced ahead of ListScreens still snaps once the
  // layout arrives (recording also clamps at the emit site as the hard backstop).
  useEffect(() => {
    if (tool !== "video") return;
    const list = screens.length ? screens : screensRef.current;
    if (!list.length) return;
    const d = dominantScreen(vcropRef.current, list);
    if (!d) return;
    const snapped = fitToScreen(vcropRef.current, d);
    if (!rectsEqual(snapped, vcropRef.current)) {
      setVcrop(snapped);
      broadcastNow(snapped);
      persistVcrop(snapped);
    }
  }, [tool, screens, broadcastNow, persistVcrop]);

  // ----- actions (capture mode) -----

  const cancel = useCallback(() => void OverlayService.Cancel(), []);

  // Full screen TOGGLE: on snaps the crop to the entire DOMINANT monitor; off
  // restores the prior region crop (or a centered default on this monitor).
  const layout = screens.length ? screens : [self];
  const dom = dominantScreen(vcrop, layout) ?? self;
  const isFullScreen = layout.some((s) => rectsEqual(vcrop, screenRect(s)));
  const toggleFullScreen = useCallback(() => {
    setWindowOpen(false);
    setSelectedHwnd(null);
    if (isFullScreen && target === "fullscreen") {
      const restored = prevRegion.current ?? centeredV(self);
      setTarget("region");
      setVcrop(restored);
      broadcastNow(restored);
      persistVcrop(restored);
    } else {
      prevRegion.current = vcrop;
      const full = screenRect(dom);
      setTarget("fullscreen");
      setVcrop(full);
      broadcastNow(full);
      persistVcrop(full);
    }
  }, [isFullScreen, target, vcrop, self, dom, broadcastNow, persistVcrop]);

  // Open the window picker and refresh the live window list.
  const openWindowPicker = useCallback(() => {
    setWindowOpen((o) => !o);
    void OverlayService.ListWindows()
      .then((list) => setWindows(Array.isArray(list) ? list : []))
      .catch(() => setWindows([]));
  }, []);

  // Snap the shared crop to a chosen app window (virtual-desktop physical rect).
  // Reuses the region capture path for both screenshot and video.
  const selectWindow = useCallback(
    (w: WindowInfo) => {
      const r = w.rect;
      if (!r || r.w < 8 || r.h < 8) return;
      prevRegion.current = vcropRef.current;
      setTarget("window");
      setSelectedHwnd(w.hwnd);
      setWindowOpen(false);
      // For video, clamp to dominant monitor (ddagrab can't span).
      let next: Rect = { x: r.x, y: r.y, w: r.w, h: r.h };
      if (toolRef.current === "video") {
        const list = screensRef.current.length ? screensRef.current : [selfRef.current];
        const mon =
          list.find((s) => s.id === w.monitorId) ??
          dominantScreen(next, list) ??
          selfRef.current;
        next = fitToScreen(next, mon);
      }
      setVcrop(next);
      broadcastNow(next);
      persistVcrop(next);
    },
    [broadcastNow, persistVcrop],
  );

  // Screenshot Capture: single-monitor -> EnterEdit/EnterEditLive (in-place morph
  // in THIS window); straddle -> EnterEditMulti (Go stitches + opens editor window).
  const captureScreenshot = useCallback(async () => {
    if (busy || !session) return;
    setBusy(true);
    try {
      const list = screensRef.current.length ? screensRef.current : [selfRef.current];
      const vr = vcropRef.current;
      const hit = list.filter((s) => overlapArea(vr, s) > 0);
      const mon = hit[0] ?? selfRef.current;
      const s = mon.scaleFactor > 0 ? mon.scaleFactor : 1;
      // Monitor-local physical crop, clamped to the monitor.
      const sx = Math.max(0, vr.x - mon.x);
      const sy = Math.max(0, vr.y - mon.y);
      const sr = Math.min(mon.w, vr.x - mon.x + vr.w);
      const sb = Math.min(mon.h, vr.y - mon.y + vr.h);
      const sub: Rect = { x: sx, y: sy, w: sr - sx, h: sb - sy };
      // Use the STITCH path whenever the crop is not fully contained in one monitor —
      // i.e. it straddles >1 monitor OR pokes into a dead zone between monitors. The
      // single-monitor in-place morph would silently drop the off-monitor strip (saved
      // PNG smaller than the badge); EnterEditMulti black-fills to the full selected
      // size so the saved PNG always matches the badge the user saw.
      const fullyInside = hit.length === 1 && sub.w === vr.w && sub.h === vr.h;
      if (!fullyInside) {
        await OverlayService.EnterEditMulti(vr);
        return;
      }
      const enter = live ? OverlayService.EnterEditLive : OverlayService.EnterEdit;
      await enter(
        mon.id,
        sub,
        Math.round(sx / s),
        Math.round(sy / s),
        Math.round(sub.w / s),
        Math.round(sub.h / s),
      );
    } finally {
      setBusy(false);
    }
  }, [busy, session, live]);

  // Record: ENFORCED single-monitor (ddagrab can't span). The pre-record snap keeps
  // the crop on one monitor, but we ALSO clamp here at the emit site so a straddle rect
  // can never reach ddagrab regardless of how vcrop got into its current state (a stale
  // persisted region, a relay update, or a missed snap). This is the hard backstop.
  const startRecording = useCallback(async () => {
    if (busy || !session) return;
    setBusy(true);
    try {
      const list = screensRef.current.length ? screensRef.current : [selfRef.current];
      const mon = dominantScreen(vcropRef.current, list) ?? selfRef.current;
      const vr = fitToScreen(vcropRef.current, mon); // confine to the chosen monitor
      const full = rectsEqual(vr, screenRect(mon));
      const sub =
        target === "window" ? "window" : full ? "fullscreen" : "region";
      const req: CaptureRequest = {
        mode: "video",
        sub,
        monitorId: mon.id,
        rect: vr,
        dpiScale: mon.scaleFactor > 0 ? mon.scaleFactor : 1,
        includeCursor: true,
        countdownSec: 0,
        copyOnCommit: false,
      };
      await OverlayService.StartRecording(req);
    } catch {
      // Go opens a dismissible error pill on a failed start; swallow the rejection.
    } finally {
      setBusy(false);
    }
  }, [busy, session, target]);

  // Window-level Esc: ONLY cancels in capture mode (edit mode owns its own Esc).
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

  // ----- interactive crop (drag/resize the shared rect) -----
  const beginDrag = (e: React.PointerEvent, handle: Handle | "body") => {
    e.preventDefault();
    e.stopPropagation();
    (e.target as Element).setPointerCapture?.(e.pointerId);
    // Manual crop edit exits window/fullscreen target mode.
    setTarget("region");
    setSelectedHwnd(null);
    setWindowOpen(false);

    const startX = e.clientX;
    const startY = e.clientY;
    const startV = vcropRef.current;
    const s = selfRef.current.scaleFactor > 0 ? selfRef.current.scaleFactor : 1;
    draggingRef.current = true;

    const onMove = (ev: PointerEvent) => {
      // This window keeps pointer capture even when the cursor crosses onto another
      // monitor, so clientX/Y stay in THIS window's CSS space; * this scale gives the
      // virtual-physical delta regardless of which monitor the cursor ends over.
      const dxV = Math.round((ev.clientX - startX) * s);
      const dyV = Math.round((ev.clientY - startY) * s);
      const next = computeDrag(startV, handle, dxV, dyV, tool, screensRef.current, selfRef.current);
      setVcrop(next);
      scheduleBroadcast(next);
    };
    const onUp = (ev: PointerEvent) => {
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
      (e.target as Element).releasePointerCapture?.(ev.pointerId);
      draggingRef.current = false;
      const final = vcropRef.current;
      persistVcrop(final);
      broadcastNow(final);
    };
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
  };

  // Click a non-crop monitor to BRING the selection here (centered on the click,
  // confined to this monitor). Replaces the old click-to-switch affordance.
  const bringHere = (clientX: number, clientY: number) => {
    const me = selfRef.current;
    const s = me.scaleFactor > 0 ? me.scaleFactor : 1;
    const cx = me.x + clientX * s;
    const cy = me.y + clientY * s;
    const v = vcropRef.current;
    const next = fitToScreen({ x: Math.round(cx - v.w / 2), y: Math.round(cy - v.h / 2), w: v.w, h: v.h }, me);
    setVcrop(next);
    broadcastNow(next);
    persistVcrop(next);
  };

  // ===== EDIT MODE (single-monitor in-place morph; unchanged) =====
  if (mode === "edit" && editPayload) {
    return (
      <div className="relative h-screen w-screen overflow-hidden bg-black">
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
        <Toolbar
          stageRef={stageRef}
          onNewCapture={() => void OverlayService.BeginSession()}
          onDone={finishEdit}
        />
      </div>
    );
  }

  // ===== CAPTURE MODE =====
  const backdrop = session?.stillUrl ?? "";
  const local = vToLocal(vcrop, self); // this window's slice in CSS px
  const onThis = overlapArea(vcrop, self) > 0; // does the crop touch this monitor?
  // Pill owner = dominant monitor. Before the layout loads, the primary window owns
  // it (old behaviour) so two windows never both show a pill.
  const iAmPill = screens.length ? dom.id === q.mon : q.primary;

  return (
    <div
      className={`relative h-screen w-screen select-none overflow-hidden ${
        live ? "bg-transparent" : "bg-black"
      }`}
    >
      {/* Frozen still backdrop (freeze mode only; empty when live). */}
      {backdrop ? (
        <img
          src={backdrop}
          alt=""
          draggable={false}
          className="pointer-events-none absolute inset-0 h-full w-full"
          style={{ objectFit: "fill" }}
        />
      ) : null}

      {onThis ? (
        <>
          {/* Dim everything but the crop slice on this monitor. */}
          <DimMask crop={local} monW={monW} monH={monH} />

          {!isFullScreen ? (
            <div
              className="absolute ring-1 ring-primary/90"
              style={{ left: local.left, top: local.top, width: local.width, height: local.height, cursor: "move" }}
              onPointerDown={(e) => beginDrag(e, "body")}
            >
              {/* dimension badge — total PHYSICAL px of the shared crop */}
              <div className="frost absolute -top-7 left-0 px-2 py-0.5 text-[11px] tabular-nums">
                {vcrop.w} × {vcrop.h}
              </div>
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
            <div className="pointer-events-none absolute inset-0 ring-2 ring-inset ring-primary/90">
              <div className="frost absolute left-1/2 top-3 -translate-x-1/2 px-2 py-0.5 text-[11px] tabular-nums">
                Entire screen · {vcrop.w} × {vcrop.h}
              </div>
            </div>
          )}
        </>
      ) : (
        // The crop is on another monitor: dim this one and let a click bring it here.
        <div className="absolute inset-0 cursor-pointer" onPointerDown={(e) => bringHere(e.clientX, e.clientY)}>
          <div className="pointer-events-none absolute inset-0 bg-black/45" />
          <div className="frost pointer-events-none absolute bottom-4 left-1/2 -translate-x-1/2 px-3 py-1.5 text-xs text-muted-foreground">
            Click to bring the selection here
          </div>
        </div>
      )}

      {/* frosted control pill — DOMINANT monitor only. */}
      {iAmPill ? (
        <div
          className="frost absolute bottom-4 left-1/2 flex -translate-x-1/2 items-center gap-1 p-1.5"
          onPointerDown={(e) => e.stopPropagation()}
        >
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
          {tool === "video" ? (
            <Button
              size="sm"
              variant={audioCount > 0 ? "default" : "ghost"}
              onClick={() => setAudioOpen((o) => !o)}
              title="Choose which audio sources to record — nothing is captured unless enabled here"
            >
              {audioCount > 0 ? <Volume2 /> : <VolumeX />}
              {audioCount > 0 ? `Audio: ${audioCount}` : "Audio: off"}
            </Button>
          ) : null}
          <div className="mx-1 h-5 w-px bg-border" />
          <Button
            size="sm"
            variant={target === "region" ? "default" : "ghost"}
            onClick={() => {
              setTarget("region");
              setSelectedHwnd(null);
              setWindowOpen(false);
              if (isFullScreen) {
                const restored = prevRegion.current ?? centeredV(self);
                setVcrop(restored);
                broadcastNow(restored);
                persistVcrop(restored);
              }
            }}
            title="Drag a freeform region"
          >
            Region
          </Button>
          <Button
            size="sm"
            variant={target === "window" || windowOpen ? "default" : "ghost"}
            onClick={openWindowPicker}
            title="Capture a specific app window"
          >
            <AppWindow /> Window
          </Button>
          <Button
            size="sm"
            variant={target === "fullscreen" || isFullScreen ? "default" : "ghost"}
            onClick={toggleFullScreen}
            title={isFullScreen ? "Back to region selection" : "Capture the entire monitor"}
          >
            <Maximize /> Full screen
          </Button>
          <Button
            size="sm"
            variant={live ? "default" : "ghost"}
            disabled={busy}
            onClick={() => void toggleFreeze()}
            title={
              freeze
                ? "Screen is frozen while you select — click to keep it live instead"
                : "Screen stays live while you select — click to freeze it"
            }
          >
            {freeze ? <Snowflake /> : <Zap />} {freeze ? "Frozen" : "Live"}
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

      {/* Window picker — snap crop to a top-level app window (screenshot + video). */}
      {iAmPill && windowOpen ? (
        <div
          className="frost absolute bottom-20 left-1/2 max-h-72 w-96 -translate-x-1/2 overflow-auto p-2 text-sm"
          onPointerDown={(e) => e.stopPropagation()}
        >
          <div className="mb-2 px-1 text-xs font-medium text-muted-foreground">
            Select a window — crop snaps to its bounds
          </div>
          {windows.length === 0 ? (
            <p className="px-1 py-2 text-xs text-muted-foreground">No capturable windows found.</p>
          ) : (
            windows.map((w) => (
              <button
                key={String(w.hwnd)}
                type="button"
                onClick={() => selectWindow(w)}
                className={`flex w-full items-center gap-2 px-2 py-1.5 text-left text-xs hover:bg-accent ${
                  selectedHwnd === w.hwnd ? "bg-primary/15 text-foreground" : "text-muted-foreground"
                }`}
              >
                <AppWindow className="size-3.5 shrink-0" />
                <span className="min-w-0 flex-1 truncate">{w.title}</span>
                <span className="shrink-0 tabular-nums text-[10px] opacity-60">
                  {w.rect?.w ?? 0}×{w.rect?.h ?? 0}
                </span>
              </button>
            ))
          )}
        </div>
      ) : null}

      {/* Audio sources picker — every row is an independent OPT-IN. */}
      {iAmPill && audioOpen && tool === "video" ? (
        <div
          className="frost absolute bottom-20 left-1/2 w-80 -translate-x-1/2 p-3 text-sm"
          onPointerDown={(e) => e.stopPropagation()}
        >
          <div className="mb-2 text-xs font-medium text-muted-foreground">
            Audio sources — nothing is recorded unless enabled here
          </div>
          <PickRow
            checked={audioSystem}
            label="System audio (everything you hear)"
            onClick={() => setAudioSystem((v) => !v)}
          />
          {mics.length > 0 ? (
            <>
              <div className="mb-1 mt-3 text-xs text-muted-foreground">Microphone</div>
              {mics.map((m) => (
                <PickRow
                  key={m}
                  checked={audioMic === m}
                  label={m}
                  onClick={() => setAudioMic((cur) => (cur === m ? "" : m))}
                />
              ))}
            </>
          ) : null}
          <div className="mb-1 mt-3 text-xs text-muted-foreground">
            Applications playing audio {sessions.length === 0 ? "— none right now" : ""}
          </div>
          {sessions.map((s) => (
            <PickRow
              key={s.pid}
              checked={audioApps.includes(s.pid)}
              label={`${s.name} (pid ${s.pid})`}
              onClick={() =>
                setAudioApps((cur) =>
                  cur.includes(s.pid) ? cur.filter((p) => p !== s.pid) : [...cur, s.pid],
                )
              }
            />
          ))}
        </div>
      ) : null}
    </div>
  );
}

// PickRow is one opt-in line in the audio sources picker.
function PickRow({
  checked,
  label,
  onClick,
}: {
  checked: boolean;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`flex w-full items-center gap-2 rounded px-2 py-1 text-left text-xs hover:bg-accent ${
        checked ? "text-foreground" : "text-muted-foreground"
      }`}
    >
      <span
        className={`inline-flex size-3.5 items-center justify-center border ${
          checked ? "border-primary bg-primary text-primary-foreground" : "border-border"
        }`}
      >
        {checked ? "✓" : ""}
      </span>
      <span className="min-w-0 flex-1 truncate">{label}</span>
    </button>
  );
}

// DimMask paints four black panels around the crop so the crop interior stays
// bright. Robust to a crop that extends BEYOND this window (a straddle slice): the
// hole is clamped to the viewport, so a crop partly/fully off this monitor still
// dims the right region (a fully-off crop dims the whole monitor).
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
  const left = clamp(crop.left, 0, monW);
  const top = clamp(crop.top, 0, monH);
  const right = clamp(crop.left + crop.width, 0, monW);
  const bottom = clamp(crop.top + crop.height, 0, monH);
  return (
    <div className="pointer-events-none absolute inset-0">
      <div className={dim} style={{ left: 0, top: 0, width: monW, height: top }} />
      <div className={dim} style={{ left: 0, top: bottom, width: monW, height: Math.max(0, monH - bottom) }} />
      <div className={dim} style={{ left: 0, top, width: left, height: Math.max(0, bottom - top) }} />
      <div
        className={dim}
        style={{ left: right, top, width: Math.max(0, monW - right), height: Math.max(0, bottom - top) }}
      />
    </div>
  );
}

// ----- pure geometry helpers (virtual-desktop PHYSICAL px) -----

function clamp(v: number, lo: number, hi: number): number {
  if (hi < lo) return lo;
  return Math.min(Math.max(v, lo), hi);
}

function rectsEqual(a: Rect, b: Rect): boolean {
  return a.x === b.x && a.y === b.y && a.w === b.w && a.h === b.h;
}

function screenRect(s: ScreenInfo): Rect {
  return { x: s.x, y: s.y, w: s.w, h: s.h };
}

// vToLocal converts a virtual-desktop crop to CSS px within ONE window's viewport.
function vToLocal(vr: Rect, self: ScreenInfo): CssRect {
  const s = self.scaleFactor > 0 ? self.scaleFactor : 1;
  return {
    left: (vr.x - self.x) / s,
    top: (vr.y - self.y) / s,
    width: vr.w / s,
    height: vr.h / s,
  };
}

// overlapArea returns the intersection area (physical px²) of a crop and a monitor.
function overlapArea(vr: Rect, s: ScreenInfo): number {
  const x0 = Math.max(vr.x, s.x);
  const y0 = Math.max(vr.y, s.y);
  const x1 = Math.min(vr.x + vr.w, s.x + s.w);
  const y1 = Math.min(vr.y + vr.h, s.y + s.h);
  return x1 > x0 && y1 > y0 ? (x1 - x0) * (y1 - y0) : 0;
}

// dominantScreen returns the monitor the crop overlaps most (ties -> primary, then
// lowest id). With zero overlap everywhere it still returns a stable choice so the
// pill never vanishes.
function dominantScreen(vr: Rect, screens: ScreenInfo[]): ScreenInfo | null {
  let best: ScreenInfo | null = null;
  let bestA = -1;
  for (const s of screens) {
    const a = overlapArea(vr, s);
    if (a > bestA) {
      best = s;
      bestA = a;
    } else if (a === bestA && best) {
      if ((s.isPrimary && !best.isPrimary) || (s.isPrimary === best.isPrimary && s.id < best.id)) {
        best = s;
      }
    }
  }
  return best;
}

function unionBounds(screens: ScreenInfo[]): { minX: number; minY: number; maxX: number; maxY: number } {
  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (const s of screens) {
    minX = Math.min(minX, s.x);
    minY = Math.min(minY, s.y);
    maxX = Math.max(maxX, s.x + s.w);
    maxY = Math.max(maxY, s.y + s.h);
  }
  return { minX, minY, maxX, maxY };
}

// centeredV centers a half-monitor crop on one screen (virtual coords).
function centeredV(s: ScreenInfo): Rect {
  const w = Math.round(s.w / 2);
  const h = Math.round(s.h / 2);
  return { x: s.x + Math.round((s.w - w) / 2), y: s.y + Math.round((s.h - h) / 2), w, h };
}

// fitToScreen confines a crop fully inside one monitor, shrinking it if it is
// larger than the monitor and enforcing the minimum size.
function fitToScreen(vr: Rect, s: ScreenInfo): Rect {
  const w = Math.max(MIN_PHYS, Math.min(vr.w, s.w));
  const h = Math.max(MIN_PHYS, Math.min(vr.h, s.h));
  const x = clamp(vr.x, s.x, s.x + s.w - w);
  const y = clamp(vr.y, s.y, s.y + s.h - h);
  return { x, y, w, h };
}

// seedVcrop clamps a restored region to the virtual-desktop union so a stale saved
// region (after a monitor change) can never open fully off-desktop.
function seedVcrop(region: Rect, screens: ScreenInfo[]): Rect {
  if (!screens.length) return region;
  const u = unionBounds(screens);
  const w = Math.max(MIN_PHYS, Math.min(region.w, u.maxX - u.minX));
  const h = Math.max(MIN_PHYS, Math.min(region.h, u.maxY - u.minY));
  const x = clamp(region.x, u.minX, u.maxX - w);
  const y = clamp(region.y, u.minY, u.maxY - h);
  return { x, y, w, h };
}

// computeDrag applies a body move / edge resize to the shared crop, clamped to the
// allowed bounds: the whole virtual-desktop union (screenshot), or the dominant
// single monitor (video — the crop can't straddle when recording).
function computeDrag(
  startV: Rect,
  handle: Handle | "body",
  dxV: number,
  dyV: number,
  tool: "screenshot" | "video",
  screens: ScreenInfo[],
  self: ScreenInfo,
): Rect {
  const list = screens.length ? screens : [self];
  let bounds: { minX: number; minY: number; maxX: number; maxY: number };
  if (tool === "video") {
    const proposed =
      handle === "body" ? { x: startV.x + dxV, y: startV.y + dyV, w: startV.w, h: startV.h } : startV;
    const d = dominantScreen(proposed, list) ?? self;
    bounds = { minX: d.x, minY: d.y, maxX: d.x + d.w, maxY: d.y + d.h };
  } else {
    bounds = unionBounds(list);
  }
  return applyDrag(startV, handle, dxV, dyV, bounds);
}

function applyDrag(
  startV: Rect,
  handle: Handle | "body",
  dxV: number,
  dyV: number,
  bounds: { minX: number; minY: number; maxX: number; maxY: number },
): Rect {
  if (handle === "body") {
    const x = clamp(startV.x + dxV, bounds.minX, bounds.maxX - startV.w);
    const y = clamp(startV.y + dyV, bounds.minY, bounds.maxY - startV.h);
    return { x, y, w: startV.w, h: startV.h };
  }
  let left = startV.x;
  let top = startV.y;
  let right = startV.x + startV.w;
  let bottom = startV.y + startV.h;
  if (handle.includes("w")) left = clamp(startV.x + dxV, bounds.minX, right - MIN_PHYS);
  if (handle.includes("e")) right = clamp(right + dxV, left + MIN_PHYS, bounds.maxX);
  if (handle.includes("n")) top = clamp(startV.y + dyV, bounds.minY, bottom - MIN_PHYS);
  if (handle.includes("s")) bottom = clamp(bottom + dyV, top + MIN_PHYS, bounds.maxY);
  return { x: left, y: top, w: right - left, h: bottom - top };
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
