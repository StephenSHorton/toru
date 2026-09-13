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
//   • overlay:engage (MonitorSession)  -> reset to capture mode; seed shared crop.
//   • overlay:cropRect (Rect)          -> apply shared crop from another monitor.
//   • overlay:ui (OverlayUi)           -> tool / target / aspect / hover across monitors.
//   • overlay:edit (OverlayEditPayload)-> single-surface morph on the target monitor.
//     Library re-opens still use the standalone editor window.
//
// CAPTURE: dominant monitor owns the control pill. Clicking a NON-dominant
// monitor (even if the crop overflows onto it) brings the selection there.
// Screenshot -> overlay editor (or clipboard+library if the setting is off).
// Record is single-monitor (ddagrab can't span).
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
  Ratio,
  Snowflake,
  Video,
  Volume2,
  VolumeX,
  X,
  Zap,
} from "lucide-react";
import { OverlayService, AudioConfig, type AudioSession } from "@/lib/api";
import { saveToLibrary } from "@/editor/exportActions";
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
import { EditorCanvas } from "@/editor/EditorCanvas";
import { Toolbar } from "@/editor/Toolbar";
import { useEditorStore } from "@/editor/store";
import { useEditorKeyboard } from "@/editor/useEditorKeyboard";
import { useClipboardPaste } from "@/editor/useClipboardPaste";
import { TextEditingOverlay, resetTextEditSession } from "@/editor/tools/text";
import { CropOverlay, resetCropDraft } from "@/editor/tools/crop";
import { setStageSize } from "@/editor/viewStore";
import {
  ASPECTS,
  HANDLES,
  aspectRatio,
  centeredV,
  clamp,
  computeDrag,
  dominantScreen,
  fitToScreen,
  handleStyle,
  overlapArea,
  rectsEqual,
  screenRect,
  seedVcrop,
  snapToAspect,
  unionBounds,
  vToLocal,
  windowAtPoint,
  type AspectId,
  type CssRect,
  type Handle,
} from "@/overlay/cropMath";

const SAVE_DEBOUNCE_MS = 300;
const ASPECT_KEY = "toru.aspect";

function resetEditor(): void {
  resetCropDraft();
  resetTextEditSession();
  useEditorStore.getState().setTool("select");
}

function loadAspect(): AspectId {
  try {
    const v = window.localStorage.getItem(ASPECT_KEY);
    if (v && ASPECTS.some((a) => a.id === v)) return v as AspectId;
  } catch {
    /* ignore */
  }
  return "free";
}

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
  const [aspect, setAspect] = useState<AspectId>(loadAspect);
  const [aspectOpen, setAspectOpen] = useState(false);
  const aspectRef = useRef(aspect);
  aspectRef.current = aspect;
  // Capture target: freeform region (default), full monitor, or app-window pick.
  // Window mode: hover to highlight, click to capture (overlay editor opens).
  const [target, setTarget] = useState<"region" | "window" | "fullscreen">("region");
  const [windows, setWindows] = useState<WindowInfo[]>([]);
  const windowsRef = useRef<WindowInfo[]>([]);
  windowsRef.current = windows;
  const [hoveredHwnd, setHoveredHwnd] = useState<number | null>(null);
  const [selectedHwnd, setSelectedHwnd] = useState<number | null>(null);
  const [hoveredTitle, setHoveredTitle] = useState<string>("");

  // Per-session data, seeded empty and replaced by overlay:engage / overlay:edit.
  const [session, setSession] = useState<MonitorSession | null>(null);
  const [editPayload, setEditPayload] = useState<OverlayEditPayload | null>(null);
  const stageRef = useRef<Konva.Stage | null>(null);
  const loadBaseImage = useEditorStore((s) => s.loadBaseImage);
  // Ignore the echo of our own overlay:ui broadcast (same pattern as draggingRef).
  const uiEchoRef = useRef(false);

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

  const finishEdit = useCallback(async () => {
    const stage = stageRef.current;
    if (stage) {
      try {
        await saveToLibrary(stage);
      } catch {
        // Still dismiss on library failure so the user is never stuck.
      }
    }
    await OverlayService.Finish();
  }, []);
  useEditorKeyboard(mode === "edit", () => void finishEdit());
  useClipboardPaste(mode === "edit");

  // ----- shared-crop / shared-ui broadcast + persistence -----

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

  const broadcastUi = useCallback(
    (patch: {
      tool?: "screenshot" | "video";
      target?: "region" | "window" | "fullscreen";
      aspect?: AspectId;
      hoveredHwnd?: number;
      hoveredTitle?: string;
    }) => {
      uiEchoRef.current = true;
      void OverlayService.SetSharedUi({
        tool: patch.tool ?? toolRef.current,
        target: patch.target ?? target,
        aspect: patch.aspect ?? aspectRef.current,
        hoveredHwnd: patch.hoveredHwnd ?? hoveredHwnd ?? 0,
        hoveredTitle: patch.hoveredTitle ?? hoveredTitle,
      });
    },
    [target, hoveredHwnd, hoveredTitle],
  );

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
      setEditPayload(null);
      setMode("capture");
      setAspectOpen(false);
      // Seed the shared crop; if we re-engage while ALREADY in video mode, the seeded
      // region may straddle (persisted regions can) — confine it to one monitor now so
      // the displayed crop matches what video will record (recording also clamps).
      let seed = seedVcrop(d.region, screensRef.current);
      if (toolRef.current === "video" && screensRef.current.length) {
        const m = dominantScreen(seed, screensRef.current);
        if (m) seed = fitToScreen(seed, m, aspectRef.current);
      }
      setVcrop(seed);
      setTarget("region");
      setSelectedHwnd(null);
      setHoveredHwnd(null);
      setHoveredTitle("");
      setWindows([]);
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

    const offUi = WailsEvents.On(Events.OverlayUi, (ev) => {
      const raw = (Array.isArray(ev.data) ? ev.data[0] : ev.data) as {
        tool?: string;
        target?: string;
        aspect?: string;
        hoveredHwnd?: number;
        hoveredTitle?: string;
      };
      if (!raw) return;
      if (uiEchoRef.current) {
        uiEchoRef.current = false;
        return;
      }
      if (raw.tool === "screenshot" || raw.tool === "video") setToolMode(raw.tool);
      if (raw.target === "region" || raw.target === "window" || raw.target === "fullscreen") {
        setTarget(raw.target);
      }
      if (raw.aspect && ASPECTS.some((a) => a.id === raw.aspect)) {
        setAspect(raw.aspect as AspectId);
      }
      setHoveredHwnd(typeof raw.hoveredHwnd === "number" && raw.hoveredHwnd > 0 ? raw.hoveredHwnd : null);
      setHoveredTitle(raw.hoveredTitle ?? "");
    });

    const offEdit = WailsEvents.On(Events.OverlayEdit, (ev) => {
      const d = ev.data as OverlayEditPayload;
      if (d.monitorId !== q.mon) return;
      resetEditor();
      const img = new window.Image();
      img.onload = () => {
        const sw = Math.min(d.cssW || img.naturalWidth, window.innerWidth);
        const sh = Math.min(d.cssH || img.naturalHeight, window.innerHeight);
        setStageSize(sw, sh);
        loadBaseImage(d.cropUrl, img.naturalWidth, img.naturalHeight);
        setEditPayload({ ...d, cssW: sw, cssH: sh });
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
      offUi();
      offEdit();
    };
  }, [q.mon, applyEngage, loadScreens, loadBaseImage]);

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
    const snapped = fitToScreen(vcropRef.current, d, aspectRef.current);
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
    setSelectedHwnd(null);
    setHoveredHwnd(null);
    setHoveredTitle("");
    if (isFullScreen && target === "fullscreen") {
      const restored = prevRegion.current ?? centeredV(self);
      setTarget("region");
      setVcrop(restored);
      broadcastNow(restored);
      persistVcrop(restored);
      broadcastUi({ target: "region" });
    } else {
      prevRegion.current = vcrop;
      const full = screenRect(dom);
      setTarget("fullscreen");
      setVcrop(full);
      broadcastNow(full);
      persistVcrop(full);
      broadcastUi({ target: "fullscreen" });
    }
  }, [isFullScreen, target, vcrop, self, dom, broadcastNow, persistVcrop, broadcastUi]);

  // Refresh the Z-ordered top-level window list used for hover hit-testing.
  const refreshWindows = useCallback(() => {
    void OverlayService.ListWindows()
      .then((list) => {
        const next = Array.isArray(list) ? list : [];
        windowsRef.current = next;
        setWindows(next);
      })
      .catch(() => {
        windowsRef.current = [];
        setWindows([]);
      });
  }, []);

  // Enter window-pick mode: load windows and clear any prior selection so the
  // user must hover + click a desktop window.
  const enterWindowMode = useCallback(() => {
    setTarget("window");
    setSelectedHwnd(null);
    setHoveredHwnd(null);
    setHoveredTitle("");
    prevRegion.current = vcropRef.current;
    refreshWindows();
    broadcastUi({ target: "window", hoveredHwnd: 0, hoveredTitle: "" });
  }, [refreshWindows, broadcastUi]);

  // While window mode is active, keep the window list reasonably fresh so
  // moved/resized apps still hit-test correctly.
  useEffect(() => {
    if (target !== "window" || mode !== "capture") return;
    refreshWindows();
    const id = window.setInterval(refreshWindows, 1500);
    return () => window.clearInterval(id);
  }, [target, mode, refreshWindows]);

  // Apply a window's bounds as the shared crop (hover preview or click commit).
  // Video clamps to one monitor because ddagrab can't span. Returns the rect (or
  // null) and ALWAYS syncs vcropRef so a same-tick Capture reads the new bounds.
  const applyWindowRect = useCallback(
    (w: WindowInfo, opts: { persist: boolean }): Rect | null => {
      const r = w.rect;
      if (!r || r.w < 8 || r.h < 8) return null;
      let next: Rect = { x: r.x, y: r.y, w: r.w, h: r.h };
      if (toolRef.current === "video") {
        const list = screensRef.current.length ? screensRef.current : [selfRef.current];
        const mon =
          list.find((s) => s.id === w.monitorId) ??
          dominantScreen(next, list) ??
          selfRef.current;
        next = fitToScreen(next, mon, aspectRef.current);
      }
      vcropRef.current = next;
      setVcrop(next);
      broadcastNow(next);
      if (opts.persist) persistVcrop(next);
      return next;
    },
    [broadcastNow, persistVcrop],
  );

  // Hover hit-test: client CSS -> virtual-desktop physical, first Z-order hit wins.
  const pickWindowAt = useCallback(
    (clientX: number, clientY: number) => {
      if (target !== "window") return;
      const me = selfRef.current;
      const s = me.scaleFactor > 0 ? me.scaleFactor : 1;
      const px = me.x + Math.round(clientX * s);
      const py = me.y + Math.round(clientY * s);
      const hit = windowAtPoint(windowsRef.current, px, py);
      if (!hit) {
        if (hoveredHwnd != null && selectedHwnd == null) {
          setHoveredHwnd(null);
          setHoveredTitle("");
        }
        return;
      }
      if (hit.hwnd === hoveredHwnd) return;
      setHoveredHwnd(hit.hwnd);
      setHoveredTitle(hit.title ?? "");
      broadcastUi({ hoveredHwnd: hit.hwnd, hoveredTitle: hit.title ?? "" });
      // Live-preview the highlight before click; only lock+capture on click.
      if (selectedHwnd == null || hit.hwnd !== selectedHwnd) {
        applyWindowRect(hit, { persist: false });
      }
    },
    [target, hoveredHwnd, selectedHwnd, applyWindowRect, broadcastUi],
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
      const vr = fitToScreen(vcropRef.current, mon, aspectRef.current); // confine to the chosen monitor
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

  // Click a highlighted window: lock crop + capture immediately (screenshot) or
  // start recording (video) — Snipping Tool style, not "select then press Capture".
  // Screenshots use EnterEditWindow (macOS-style transparent pad + drop shadow);
  // video still records the live window rect (no alpha/shadow in a video stream).
  const selectWindow = useCallback(
    (w: WindowInfo) => {
      if (busy || !session) return;
      setTarget("window");
      setSelectedHwnd(w.hwnd);
      setHoveredHwnd(w.hwnd);
      setHoveredTitle(w.title ?? "");
      const next = applyWindowRect(w, { persist: true });
      if (!next) return;
      if (toolRef.current === "video") {
        void startRecording();
        return;
      }
      setBusy(true);
      void OverlayService.EnterEditWindow(w.hwnd).finally(() => setBusy(false));
    },
    [busy, session, applyWindowRect, startRecording],
  );

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
    setHoveredHwnd(null);
    setHoveredTitle("");
    broadcastUi({ target: "region", hoveredHwnd: 0, hoveredTitle: "" });

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
      const next = computeDrag(
        startV,
        handle,
        dxV,
        dyV,
        tool,
        screensRef.current,
        selfRef.current,
        aspectRef.current,
      );
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
    const next = fitToScreen(
      { x: Math.round(cx - v.w / 2), y: Math.round(cy - v.h / 2), w: v.w, h: v.h },
      me,
      aspectRef.current,
    );
    setTarget("region");
    setSelectedHwnd(null);
    setHoveredHwnd(null);
    setHoveredTitle("");
    setVcrop(next);
    broadcastNow(next);
    persistVcrop(next);
    broadcastUi({ target: "region", hoveredHwnd: 0, hoveredTitle: "" });
  };

  const applyAspect = (id: AspectId) => {
    setAspect(id);
    setAspectOpen(false);
    try {
      window.localStorage.setItem(ASPECT_KEY, id);
    } catch {
      /* ignore */
    }
    const ratio = aspectRatio(id);
    if (ratio) {
      const list = screensRef.current.length ? screensRef.current : [selfRef.current];
      const bounds =
        toolRef.current === "video"
          ? (() => {
              const d = dominantScreen(vcropRef.current, list) ?? selfRef.current;
              return { minX: d.x, minY: d.y, maxX: d.x + d.w, maxY: d.y + d.h };
            })()
          : unionBounds(list);
      const next = snapToAspect(vcropRef.current, ratio.w, ratio.h, bounds);
      setVcrop(next);
      broadcastNow(next);
      persistVcrop(next);
    }
    broadcastUi({ aspect: id });
  };

  // ===== EDIT MODE (single-monitor morph) =====
  // Center the annotation stage on this monitor — do NOT leave it at the crop's
  // original capture position (window/region often lands off-center or on a corner).
  if (mode === "edit" && editPayload) {
    const stageW = Math.min(editPayload.cssW, monW);
    const stageH = Math.min(editPayload.cssH, monH);
    const editLeft = Math.max(0, Math.round((monW - stageW) / 2));
    const editTop = Math.max(0, Math.round((monH - stageH) / 2));
    return (
      <div className="relative h-screen w-screen overflow-hidden bg-black">
        <DimMask
          crop={{
            left: editLeft,
            top: editTop,
            width: stageW,
            height: stageH,
          }}
          monW={monW}
          monH={monH}
        />
        <div
          className="absolute"
          style={{
            left: editLeft,
            top: editTop,
            width: stageW,
            height: stageH,
          }}
        >
          <EditorCanvas stageRef={stageRef} />
          <CropOverlay />
          <TextEditingOverlay stageRef={stageRef} />
        </div>
        <Toolbar
          key={editPayload.cropUrl}
          stageRef={stageRef}
          flashCopied
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
  // Window mode: freeform drag is off until the user picks (or after pick they can
  // still drag handles which drops back to region). Hover surface covers the whole
  // monitor so cross-monitor picks work on every overlay instance.
  const windowPicking = target === "window";
  const windowHighlight =
    windowPicking && (hoveredHwnd != null || selectedHwnd != null) && onThis;
  const windowLabel =
    hoveredTitle ||
    windows.find((w) => w.hwnd === selectedHwnd)?.title ||
    "";

  return (
    <div
      className={`relative h-screen w-screen select-none overflow-hidden ${
        live ? "bg-transparent" : "bg-black"
      } ${windowPicking ? "cursor-pointer" : ""}`}
      onPointerMove={
        windowPicking
          ? (e) => {
              // Don't steal hover when the cursor is over the control pill.
              if ((e.target as Element).closest?.("[data-capture-pill]")) return;
              pickWindowAt(e.clientX, e.clientY);
            }
          : undefined
      }
      onPointerDown={
        windowPicking
          ? (e) => {
              if ((e.target as Element).closest?.("[data-capture-pill]")) return;
              // Left-click only — right-click stays free for cancel-tool habits.
              if (e.button !== 0) return;
              const me = selfRef.current;
              const s = me.scaleFactor > 0 ? me.scaleFactor : 1;
              const px = me.x + Math.round(e.clientX * s);
              const py = me.y + Math.round(e.clientY * s);
              const hit = windowAtPoint(windowsRef.current, px, py);
              if (hit) {
                e.preventDefault();
                e.stopPropagation();
                selectWindow(hit);
              }
            }
          : undefined
      }
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

      {windowPicking ? (
        // Window pick: whole surface is the hit target; highlight follows hover.
        <>
          {windowHighlight ? (
            <>
              <DimMask crop={local} monW={monW} monH={monH} />
              {/* Explicit outline (not just a soft ring): dual-stroke border so the
                  window frame is obvious on both light and dark content. */}
              <div
                className="pointer-events-none absolute"
                style={{
                  left: local.left,
                  top: local.top,
                  width: local.width,
                  height: local.height,
                  border: "2px solid hsl(var(--primary))",
                  boxShadow:
                    "0 0 0 1px rgba(255,255,255,0.95), inset 0 0 0 1px rgba(255,255,255,0.85), 0 0 0 3px hsl(var(--primary) / 0.45)",
                }}
              >
                <div className="frost absolute -top-8 left-0 max-w-[min(100%,20rem)] truncate px-2 py-0.5 text-[11px]">
                  {windowLabel
                    ? `${windowLabel}  -  ${vcrop.w} x ${vcrop.h}`
                    : `${vcrop.w} x ${vcrop.h}`}
                </div>
              </div>
            </>
          ) : (
            <div className="pointer-events-none absolute inset-0 bg-black/45">
              <div className="frost absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 px-3 py-2 text-center text-xs text-muted-foreground">
                Hover a window to highlight it, then click to capture
              </div>
            </div>
          )}
        </>
      ) : iAmPill && onThis ? (
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
                {vcrop.w} x {vcrop.h}
                {aspect !== "free" ? `  -  ${aspect}` : ""}
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
                Entire screen - {vcrop.w} x {vcrop.h}
              </div>
            </div>
          )}
        </>
      ) : (
        // Crop lives on another monitor, OR this monitor only has overflow from a
        // crop that's too big. Either way a click here MOVES the selection onto
        // this screen (shrinking to fit) instead of trying to drag the sliver.
        <div className="absolute inset-0 cursor-pointer" onPointerDown={(e) => bringHere(e.clientX, e.clientY)}>
          {onThis ? <DimMask crop={local} monW={monW} monH={monH} /> : <div className="pointer-events-none absolute inset-0 bg-black/45" />}
          <div className="frost pointer-events-none absolute bottom-4 left-1/2 -translate-x-1/2 px-3 py-1.5 text-xs text-muted-foreground">
            Click to bring the selection here
          </div>
        </div>
      )}

      {/* frosted control pill — DOMINANT monitor only. */}
      {iAmPill ? (
        <div
          data-capture-pill
          className="frost absolute bottom-4 left-1/2 z-30 flex -translate-x-1/2 items-center gap-1 p-1.5"
          onPointerDown={(e) => e.stopPropagation()}
        >
          <Button
            size="sm"
            variant={tool === "screenshot" ? "default" : "ghost"}
            onClick={() => {
              setToolMode("screenshot");
              broadcastUi({ tool: "screenshot" });
            }}
          >
            <Camera /> Screenshot
          </Button>
          <Button
            size="sm"
            variant={tool === "video" ? "default" : "ghost"}
            onClick={() => {
              setToolMode("video");
              broadcastUi({ tool: "video" });
            }}
          >
            <Video /> Record
          </Button>
          {tool === "video" ? (
            <Button
              size="sm"
              variant={audioCount > 0 ? "default" : "ghost"}
              onClick={() => {
                setAspectOpen(false);
                setAudioOpen((o) => !o);
              }}
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
              setHoveredHwnd(null);
              setHoveredTitle("");
              if (isFullScreen) {
                const restored = prevRegion.current ?? centeredV(self);
                setVcrop(restored);
                broadcastNow(restored);
                persistVcrop(restored);
              }
              broadcastUi({ target: "region", hoveredHwnd: 0, hoveredTitle: "" });
            }}
            title="Drag a freeform region"
          >
            Region
          </Button>
          <Button
            size="sm"
            variant={target === "window" ? "default" : "ghost"}
            onClick={enterWindowMode}
            title="Hover a window to highlight it, click to capture"
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
          <Button
            size="sm"
            variant={aspect !== "free" ? "default" : "ghost"}
            onClick={() => {
              setAudioOpen(false);
              setAspectOpen((o) => !o);
            }}
            title="Lock the selection to an aspect ratio while resizing"
          >
            <Ratio /> {aspect === "free" ? "Aspect" : aspect}
          </Button>
          <div className="mx-1 h-5 w-px bg-border" />
          <Button size="sm" variant="ghost" onClick={cancel}>
            <X /> Cancel
          </Button>
          <Button
            size="sm"
            disabled={busy || !session || target === "window"}
            title={
              target === "window"
                ? "Click a highlighted window to capture"
                : undefined
            }
            onClick={() => (tool === "video" ? startRecording() : captureScreenshot())}
          >
            {tool === "video" ? "Start Recording" : "Capture"}
          </Button>
        </div>
      ) : null}

      {iAmPill && aspectOpen ? (
        <div
          data-capture-pill
          className="frost absolute bottom-20 left-1/2 z-30 w-56 -translate-x-1/2 p-2 text-sm"
          onPointerDown={(e) => e.stopPropagation()}
        >
          <div className="mb-1 px-2 text-xs font-medium text-muted-foreground">
            Aspect ratio
          </div>
          {ASPECTS.map((a) => (
            <button
              key={a.id}
              type="button"
              onClick={() => applyAspect(a.id)}
              className={`flex w-full items-center justify-between px-2 py-1 text-left text-xs hover:bg-accent ${
                aspect === a.id ? "text-foreground" : "text-muted-foreground"
              }`}
            >
              <span>{a.id === "free" ? "Freeform" : a.label}</span>
              {aspect === a.id ? <span className="text-primary">{"\u2713"}</span> : null}
            </button>
          ))}
        </div>
      ) : null}

      {/* Audio sources picker — every row is an independent OPT-IN. */}
      {iAmPill && audioOpen && tool === "video" ? (
        <div
          data-capture-pill
          className="frost absolute bottom-20 left-1/2 z-30 w-80 -translate-x-1/2 p-3 text-sm"
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
        {checked ? "\u2713" : ""}
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

