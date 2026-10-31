import { setKeys } from "@/trim/fx/ops";
import { FIT, mediaById, updateClip, videoTracks, type Clip, type Project } from "@/trim/project";

// canvas.ts: the project canvas (its frame size and rate) and where clips sit
// on it. A clip's transform is relative to the canvas: scale 1 fits the
// picture inside the frame (the export's clipPlacement does the same), so
// "fill" is the scale at which the picture covers the frame instead, cropping
// what sticks out.

export type Canvas = Project["canvas"];

export interface CanvasPreset {
  id: string;
  label: string;
  hint: string;
  width: number;
  height: number;
}

export const CANVAS_PRESETS: CanvasPreset[] = [
  { id: "16:9", label: "16:9", hint: "Landscape: most video", width: 1920, height: 1080 },
  { id: "9:16", label: "9:16", hint: "Vertical: short clips for phones", width: 1080, height: 1920 },
  { id: "1:1", label: "1:1", hint: "Square", width: 1080, height: 1080 },
  { id: "4:5", label: "4:5", hint: "Portrait: feed posts", width: 1080, height: 1350 },
];

export const CANVAS_FPS = [24, 25, 30, 50, 60];
export const MIN_CANVAS = 16;
export const MAX_CANVAS = 7680;

// Placement is what a canvas change does to the clips that fill the frame:
// fit them inside it, fill it (cropping), or leave every transform alone.
export type Placement = "fit" | "fill" | "keep";
// ClipPlace is the inspector's per-clip quick action: fit or fill (centred,
// keeping rotation and opacity) or reset (the default transform, no keys).
export type ClipPlace = "fit" | "fill" | "reset";

const even = (n: number) => Math.max(2, Math.round(n / 2) * 2);
const clampDim = (n: number) => Math.max(MIN_CANVAS, Math.min(MAX_CANVAS, even(n)));

// normCanvas is a valid canvas: even sizes in range, a sane frame rate.
export function normCanvas(c: Canvas): Canvas {
  const fps = Number.isFinite(c.fps) && c.fps > 0 ? Math.max(1, Math.min(240, c.fps)) : 30;
  return { width: clampDim(c.width), height: clampDim(c.height), fps };
}

// presetOf is the preset a canvas matches (by size), or undefined.
export function presetOf(c: Canvas): CanvasPreset | undefined {
  return CANVAS_PRESETS.find((p) => p.width === c.width && p.height === c.height);
}

// aspectLabel names a canvas's shape: a preset's ratio, else W:H reduced.
export function aspectLabel(c: Canvas): string {
  const p = presetOf(c) ?? CANVAS_PRESETS.find((q) => Math.abs(q.width / q.height - c.width / c.height) < 0.005);
  if (p) return p.label;
  const g = gcd(c.width, c.height);
  return `${c.width / g}:${c.height / g}`;
}
function gcd(a: number, b: number): number {
  return b ? gcd(b, a % b) : Math.max(1, a);
}

// fillScale is the scale (as transform.scale) at which a w x h picture
// covers a cw x ch canvas: the ratio of the cover factor to the fit factor.
export function fillScale(w: number, h: number, cw: number, ch: number): number {
  if (w <= 0 || h <= 0 || cw <= 0 || ch <= 0) return 1;
  const sx = cw / w;
  const sy = ch / h;
  return Math.max(sx, sy) / Math.min(sx, sy);
}

const POS_KEYS = ["x", "y", "scale"] as const;

// placeClip fits, fills or resets one clip on the project's canvas. Fit and
// fill centre it and drop its position and size keyframes; reset is the
// default transform with no transform keyframes at all. Audio and missing
// media are left alone.
export function placeClip(p: Project, clipId: string, mode: ClipPlace): Project {
  const c = p.tracks.flatMap((t) => t.clips).find((x) => x.id === clipId);
  const m = c && mediaById(p, c.media);
  if (!c || !m || m.kind === "audio") return p;
  let q = p;
  const drop = mode === "reset" ? ([...POS_KEYS, "rotation", "opacity"] as const) : POS_KEYS;
  for (const k of drop) if (c.anim?.[k]?.length) q = setKeys(q, c.id, { anim: k }, undefined);
  const scale = mode === "fill" ? fillScale(m.width, m.height, p.canvas.width, p.canvas.height) : 1;
  const transform = mode === "reset" ? { ...FIT } : { ...c.transform, x: 0.5, y: 0.5, scale: round4(scale) };
  return updateClip(q, c.id, { transform });
}
const round4 = (n: number) => Math.round(n * 1e4) / 1e4;

// isFullFrame says whether a canvas change should re-place a clip: every
// picture on the bottom video track, and any clip above that sits centred
// at full size (as a fit or fill would leave it). Overlays placed by hand
// (picture-in-picture, a moved logo) keep their transforms.
function isFullFrame(p: Project, c: Clip, bottom: boolean): boolean {
  if (bottom) return true;
  const t = c.transform;
  if (c.anim?.x?.length || c.anim?.y?.length || c.anim?.scale?.length) return false;
  if (Math.abs(t.x - 0.5) > 1e-3 || Math.abs(t.y - 0.5) > 1e-3) return false;
  const m = mediaById(p, c.media);
  if (!m) return false;
  return Math.abs(t.scale - 1) < 1e-3 || Math.abs(t.scale - fillScale(m.width, m.height, p.canvas.width, p.canvas.height)) < 1e-3;
}

// setCanvas changes the project's canvas, as one edit: the new size and
// rate, and the clips that fill the frame placed by `place`. A crop is
// dropped when the size changes (its rectangle was in the old canvas).
// Texts are placed in fractions of the frame, so they stay where they are.
export function setCanvas(p: Project, next: Canvas, place: Placement): Project {
  const canvas = normCanvas(next);
  const sized = canvas.width !== p.canvas.width || canvas.height !== p.canvas.height;
  if (!sized && canvas.fps === p.canvas.fps) return p;
  // Which clips to re-place is decided on the old canvas.
  const bottom = videoTracks(p)[0]?.id;
  const targets =
    sized && place !== "keep"
      ? p.tracks.filter((t) => t.kind === "video").flatMap((t) => t.clips.filter((c) => isFullFrame(p, c, t.id === bottom)).map((c) => c.id))
      : [];
  let q: Project = { ...p, canvas, crop: sized ? null : p.crop };
  for (const id of targets) q = placeClip(q, id, place as ClipPlace);
  return q;
}
