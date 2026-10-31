import { clipLength, type Clip, type Track } from "@/trim/project";

// transition.ts: transitions between adjacent clips on a video track, the
// preview's side of internal/vid/transitions.go (keep the two in step).
//
// A transition belongs to the incoming clip (clip.transition) and plays at
// its cut with the clip before it, centred on the cut: duration d runs from
// cut - d/2 to cut + d/2 (the first half rounds down to a whole ms, as Go's
// Half). The outgoing clip holds its last frame for the second half and the
// incoming one its first frame for the first half. The picture is ffmpeg
// xfade's (types are its names), ported in compositor/transition-gl.ts; the
// sound fades out over the half before the cut and in over the half after.
//
// A transition that doesn't fit is ignored, never an error: it needs a clip
// right before it on the same visible video track (ending where it starts,
// within 1 ms), both drawing a picture, and room for each half next to the
// clip's other transition.

export type TransitionType =
  | "fade"
  | "fadeblack"
  | "fadewhite"
  | "wipeleft"
  | "wiperight"
  | "wipeup"
  | "wipedown"
  | "slideleft"
  | "slideright"
  | "slideup"
  | "slidedown"
  | "zoomin"
  | "hblur";

// Clip.transition: seconds in the editor (ms in the export request).
export type ClipTransition = { type: TransitionType; duration: number };

// The offered transitions, in library order, with their names in the UI.
export const TRANSITIONS: { type: TransitionType; label: string; group: string }[] = [
  { type: "fade", label: "Cross dissolve", group: "Dissolve" },
  { type: "fadeblack", label: "Dip to black", group: "Dissolve" },
  { type: "fadewhite", label: "Dip to white", group: "Dissolve" },
  { type: "wipeleft", label: "Wipe left", group: "Wipe" },
  { type: "wiperight", label: "Wipe right", group: "Wipe" },
  { type: "wipeup", label: "Wipe up", group: "Wipe" },
  { type: "wipedown", label: "Wipe down", group: "Wipe" },
  { type: "slideleft", label: "Slide left", group: "Slide" },
  { type: "slideright", label: "Slide right", group: "Slide" },
  { type: "slideup", label: "Slide up", group: "Slide" },
  { type: "slidedown", label: "Slide down", group: "Slide" },
  { type: "zoomin", label: "Zoom in", group: "Motion" },
  { type: "hblur", label: "Blur", group: "Motion" },
];
export const transitionLabel = (t: string) => TRANSITIONS.find((x) => x.type === t)?.label ?? t;
export const isTransitionType = (t: unknown): t is TransitionType => TRANSITIONS.some((x) => x.type === t);

// Duration limits, seconds (Go: Min/Max/DefaultTransitionMs).
export const MIN_TRANSITION = 0.1;
export const MAX_TRANSITION = 5;
export const DEFAULT_TRANSITION = 0.5;

// Transition is a valid transition on a track, in seconds: clip `index` (the
// incoming one) with the one before it.
export type Transition = {
  index: number;
  type: TransitionType;
  duration: number;
  cut: number;
  start: number; // cut - half
  end: number; // cut + (duration - half)
};

const ms = (s: number) => Math.round(s * 1000);

// shows says whether a clip draws a picture (Go: shows in BuildProjectArgs):
// not when it's fully transparent all the way through.
export function clipShows(c: Clip): boolean {
  const keys = c.anim?.opacity;
  const hi = keys?.length ? Math.max(...keys.map((k) => Number(k.v))) : c.transform.opacity;
  return Math.min(1, hi) > 0;
}

// trackTransitions lists the valid transitions on a track by incoming clip
// index (Go: trackTransitions; a track that isn't a visible video track has
// none). Works in whole ms, as the export does.
export function trackTransitions(track: Track, shows: (c: Clip) => boolean = clipShows): Map<number, Transition> {
  const out = new Map<number, Transition>();
  if (track.kind !== "video" || track.hidden) return out;
  type Cand = { index: number; type: TransitionType; d: number; half: number; cut: number };
  const cand = new Map<number, Cand>();
  for (let i = 1; i < track.clips.length; i++) {
    const c = track.clips[i];
    const prev = track.clips[i - 1];
    const tr = c.transition;
    if (!tr || !isTransitionType(tr.type)) continue;
    const d = Math.min(Math.max(ms(tr.duration), ms(MIN_TRANSITION)), ms(MAX_TRANSITION));
    const cut = ms(prev.start) + ms(clipLength(prev));
    if (Math.abs(cut - ms(c.start)) > 1 || !shows(c) || !shows(prev)) continue;
    cand.set(i, { index: i, type: tr.type, d, half: Math.floor(d / 2), cut: ms(c.start) });
  }
  for (const [i, tr] of cand) {
    const prevIn = cand.get(i - 1);
    const next = cand.get(i + 1);
    const roomPrev = tr.half + (prevIn ? prevIn.d - prevIn.half : 0);
    const roomThis = tr.d - tr.half + (next ? next.half : 0);
    if (roomPrev > ms(clipLength(track.clips[i - 1])) || roomThis > ms(clipLength(track.clips[i]))) continue;
    out.set(i, {
      index: i,
      type: tr.type,
      duration: tr.d / 1000,
      cut: tr.cut / 1000,
      start: (tr.cut - tr.half) / 1000,
      end: (tr.cut + tr.d - tr.half) / 1000,
    });
  }
  return out;
}

// transitionProgress is xfade's progress at timeline time t on a canvas
// running at fps: 1 at the window's first frame, falling to 0, computed
// from the frame the export draws at t (frames start at k / fps).
export function transitionProgress(tr: Transition, t: number, fps: number): number {
  const frame = Math.floor(t * fps + 1e-6) / fps;
  return Math.min(1, Math.max(0, 1 - (frame - tr.start) / tr.duration));
}

// cutFade is the extra audio gain of track clip `index` at timeline time t
// from its transitions: fading out over the half before its outgoing cut,
// in over the half after its incoming one (linear, as afade).
export function cutFade(trans: Map<number, Transition>, index: number, t: number): number {
  let g = 1;
  const tin = trans.get(index);
  if (tin && t < tin.end) g *= Math.max(0, Math.min(1, (t - tin.cut) / (tin.end - tin.cut)));
  const tout = trans.get(index + 1);
  if (tout && t > tout.start) g *= Math.max(0, Math.min(1, (tout.cut - t) / (tout.cut - tout.start)));
  return g;
}
