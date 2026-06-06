// TS mirror of internal/capture/contract.go — the SHARED seam between the two
// developers. Keep in sync with contract.go (the Go side is the source of
// truth; once `wails3 generate bindings` is run, prefer the generated models).
//
// Rect is ALWAYS virtual-desktop PHYSICAL pixels (origin = primary top-left;
// monitors left/above the primary have NEGATIVE x/y). args.go rebases for ddagrab.

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export type CaptureMode = "screenshot" | "video";
export type CaptureSub = "region" | "window" | "fullscreen";

export interface CaptureRequest {
  mode: CaptureMode;
  sub: CaptureSub;
  monitorId: number;
  rect: Rect;
  dpiScale: number;
  includeCursor: boolean;
  countdownSec: number;
  micDevice?: string;
  copyOnCommit: boolean;
}

export interface CaptureResult {
  mode: CaptureMode;
  imagePath?: string; // set when mode === "screenshot"
  videoPath?: string; // set when mode === "video"
  handleId?: string;
  rect: Rect;
  monitorId: number;
  cancelled: boolean;
}

export interface TrimRequest {
  videoPath: string;
  startMs: number;
  endMs: number;
  precise: boolean;
  outPath: string;
}

export interface ScreenInfo {
  id: number;
  x: number;
  y: number;
  w: number;
  h: number;
  scaleFactor: number;
  isPrimary: boolean;
}

/**
 * MonitorSession is the per-overlay-window payload returned by
 * OverlayService.BeginSession() (one per monitor). Mirrors the Go struct in
 * internal/overlay/session.go. Once `wails3 generate bindings` runs, prefer the
 * generated model from the overlay bindings package over this hand-mirror.
 */
export interface MonitorSession {
  monitorId: number; // == kbinani idx == ScreenInfo.id == ddagrab output_idx
  stillUrl: string; // "/__shot/<id>" — served by ShotMiddleware
  // Monitor geometry in VIRTUAL-DESKTOP PHYSICAL px (origin = primary top-left;
  // monitors left/above carry NEGATIVE x/y).
  x: number;
  y: number;
  w: number;
  h: number;
  scale: number; // physical = css * scale
  isPrimary: boolean; // only the primary window is interactive (crop + pill)
  crop: Rect; // monitor-local PHYSICAL px; {0,0,0,0} == none
}

/**
 * parseOverlayQuery reads the canonical overlay numbers from an overlay window's
 * URL query string. Each overlay window is launched at:
 *   /?view=overlay&mon=<id>&primary=<0|1>&scale=<float>&bx=<X>&by=<Y>&still=<urlenc "/__shot/<id>">&crop=<urlenc "x,y,w,h">
 * so the front end can render on first paint without a binding round-trip.
 *
 * Note: monitor W/H in CSS = window.innerWidth/innerHeight (each overlay window
 * already covers the full monitor in DIP), so the front end does NOT need the
 * physical w/h for layout — only `scale` and the physical origin (bx,by) for the
 * CropToPhysical emit math, plus `crop` (monitor-local physical px) to seed the
 * crop and `still` for the <img> src.
 */
export interface OverlayQuery {
  mon: number;
  primary: boolean;
  scale: number;
  bx: number; // monitor virtual-desktop physical origin X (may be negative)
  by: number; // monitor virtual-desktop physical origin Y (may be negative)
  mw: number; // monitor PHYSICAL width (clamps the rounded crop right edge)
  mh: number; // monitor PHYSICAL height (clamps the rounded crop bottom edge)
  stillUrl: string; // decoded "/__shot/<id>"
  crop: Rect; // monitor-local PHYSICAL px ({0,0,0,0} == none)
}

export function parseOverlayQuery(search: string): OverlayQuery {
  const q = new URLSearchParams(search);
  const cropCSV = q.get("crop") ?? "0,0,0,0";
  const [cx, cy, cw, ch] = cropCSV.split(",").map((n) => parseInt(n, 10) || 0);
  return {
    mon: parseInt(q.get("mon") ?? "0", 10) || 0,
    primary: q.get("primary") === "1",
    scale: parseFloat(q.get("scale") ?? "1") || 1,
    bx: parseInt(q.get("bx") ?? "0", 10) || 0,
    by: parseInt(q.get("by") ?? "0", 10) || 0,
    mw: parseInt(q.get("mw") ?? "0", 10) || 0,
    mh: parseInt(q.get("mh") ?? "0", 10) || 0,
    stillUrl: q.get("still") ?? "",
    crop: { x: cx, y: cy, w: cw, h: ch },
  };
}

/** Event names broadcast Go→JS. capture:done routes by `mode`. */
export const Events = {
  CaptureDone: "capture:done",
  CaptureCancelled: "capture:cancelled",
  RecordProgress: "record:progress",
  OverlayDismiss: "overlay:dismiss",
  CaptureThumbnail: "capture:thumbnail",
} as const;

/** Media types for ExportService.CopyToClipboard. */
export const Media = { Image: "image", Video: "video" } as const;
