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
