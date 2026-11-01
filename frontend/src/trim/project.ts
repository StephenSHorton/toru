import type { AudioTrack, TrackLevel } from "@/lib/api";
import type { Rect } from "@/lib/contract";
import { newId, useHistory, type Edl, type Text } from "@/trim/edl";
import { clipOutLength, fromSource, localOut, localSrc, normCurve, rateAt, retime, toSource } from "@/trim/speed";
import { trackTransitions, transitionProgress, type ClipTransition, type Transition } from "@/trim/transition";

// The v2 video editor's project (docs/video-editor-v2.md, Data model): media,
// free-form video and audio tracks of clips, v1 texts and crop. Pure data and
// pure functions like edl.ts: every op returns a NEW Project, or the same
// object when it changes nothing (so history skips no-ops), never mutates.
//
// Times are SECONDS, all on the timeline (output time) except a clip's
// in/out, which are SOURCE time. A clip's timeline length is
// (out - in) / speed, or its speed curve's length (speed.ts). Clips on
// one track are sorted by start and never overlap; a clip placed on an
// occupied spot lands at the nearest free one.
// Ops on a locked track (as source or target) do nothing.

export type MediaKind = "video" | "image" | "audio";
export type Media = {
  id: string; // stable within the project
  path: string; // absolute
  url: string; // served URL for the preview (MediaService.Register)
  kind: MediaKind;
  duration: number; // s; 0 for images
  width: number;
  height: number; // 0 for audio
  fps: number; // 0 for image/audio
  hasAudio: boolean;
  tracks: AudioTrack[]; // v1 type: audio streams with titles / mix flag
};

export type Transform = { x: number; y: number; scale: number; opacity: number; rotation?: number };
// x, y: clip centre in 0..1 of the canvas; scale: 1 = fit inside the canvas;
// rotation: degrees clockwise (v3; absent = 0).

export type Chroma = { color: string; similarity: number; blend: number }; // "#00ff00", 0.01..1, 0..1
export type Look = "none" | "bw" | "sepia" | "vintage" | "cool" | "warm";
export type Filters = { brightness: number; contrast: number; saturation: number; look: Look; blur: number };
// brightness -1..1 (0), contrast 0..2 (1), saturation 0..3 (1), blur 0..20 (0)

export type Clip = {
  id: string;
  media: string; // Media.id
  start: number; // timeline position, s
  in: number;
  out: number; // source range, s (images: 0..length)
  speed: number; // 0.25..4, 1
  volume: number; // 0..2, 1
  muted: boolean;
  fadeIn: number;
  fadeOut: number; // s
  levels?: TrackLevel[]; // v1 per-source-track levels (index = audio stream; source unused, 0)
  transform: Transform; // video/image only
  chroma?: Chroma; // video/image only; undefined = off
  filters: Filters; // video/image only
  // --- v3 (docs/video-editor-v3.md). Optional until the v2→v3 migration
  // lands; then effects replaces chroma and filters.
  effects?: EffectInstance[]; // video/image only; effects[0] runs first
  anim?: Partial<Record<AnimTarget, Keyframe[]>>; // keyframed transform / volume
  speedCurve?: SpeedCurve; // overrides speed
  hold?: boolean; // freeze frame: shows source frame `in` for (out - in) s, silent
  reversed?: boolean; // plays the source range backwards (a reverse proxy)
  blendFrames?: BlendFrames; // slow motion: off (default), mix (tmix) or smooth (minterpolate)
  transition?: ClipTransition; // with the clip before it on the track (transition.ts)
};

// --- v3 effects, keyframes and time (frozen contract for the v3 cards) ----

// Ease shapes a keyframe segment, from a keyframe to the next one.
export type Ease = "linear" | "in" | "out" | "inOut" | "hold";
// ParamValue is an effect parameter's value: number, boolean, "#rrggbb"
// colour, an enum option id, a file path (LUT) or a point [x, y] (0..1 of
// the frame).
export type ParamValue = number | boolean | string | [number, number];
// Keyframe: t is seconds from the clip's start ON THE TIMELINE (clip-local
// output time), so keys stay put when the clip's end is trimmed or its
// speed changes.
export type Keyframe = { t: number; v: ParamValue; ease: Ease };

// EffectInstance is one effect in a clip's stack. type is a registry id
// (internal/fx/defs/<type>.json); params missing from `params` take the
// definition's default; keys holds keyframes per param (absent = static).
// group ties instances applied from one saved filter (its preset id), shown
// as one card with an intensity.
export type EffectInstance = {
  id: string; // newId("fx")
  type: string;
  enabled: boolean;
  params: Record<string, ParamValue>;
  keys?: Record<string, Keyframe[]>; // sorted by t
  group?: string;
  amount?: number; // 0..1 (group intensity), 1 when absent
};

export type AnimTarget = "x" | "y" | "scale" | "opacity" | "rotation" | "volume";

// SpeedCurve: speed is piecewise linear in SOURCE time. s is seconds from
// clip.in, v the speed there (0.1..10); at least 2 points, sorted by s,
// the first at 0 and the last at out - in. audio follows the speed
// (0.25..4x; muted outside) or is muted wherever the speed isn't 1.
export type SpeedCurve = {
  points: { s: number; v: number }[];
  audio: "follow" | "mute";
  preset?: string; // the preset it started from (UI label only)
};
export const MIN_CURVE_SPEED = 0.1;
export const MAX_CURVE_SPEED = 10;
export const DEFAULT_FREEZE = 2; // s, a freeze frame's default length

export type BlendFrames = "off" | "mix" | "smooth";
// timeline length of a clip = (out - in) / speed, or the curve's length

export type TrackKind = "video" | "audio";
export type Track = {
  id: string;
  kind: TrackKind;
  muted: boolean;
  hidden: boolean;
  locked: boolean;
  clips: Clip[]; // sorted by start, no overlaps within a track
};

export type Project = {
  version: 2 | 3; // 3 once the v2→v3 migration lands (v3 cards)
  id: string;
  name: string;
  canvas: { width: number; height: number; fps: number };
  media: Media[];
  tracks: Track[]; // video tracks bottom→top, then audio tracks
  texts: Text[]; // v1 Text, output time
  crop: Rect | null; // v1 crop, canvas pixels
};

// --- Defaults and limits -------------------------------------------------

export const IMAGE_LENGTH = 5; // s, an image's default length
export const MIN_CLIP = 0.1; // s, shortest clip a trim or split leaves
export const MIN_SPEED = 0.25;
export const MAX_SPEED = 4;
export const OVERLAY_SCALE = 0.4; // clips above the bottom video track
export const DEFAULT_CANVAS = { width: 1920, height: 1080, fps: 30 };
// Green screen keys on colour only (ffmpeg chromakey), so white, grey and black
// all sit 0.377 from pure green: similarity + blend must stay below that or
// they turn partly see-through. 0.25 still keys a lit green screen (about
// 0.23). internal/vid/effects.go DefaultChroma mirrors this.
export const DEFAULT_CHROMA: Chroma = { color: "#00ff00", similarity: 0.25, blend: 0.08 };
export const DEFAULT_FILTERS: Filters = { brightness: 0, contrast: 1, saturation: 1, look: "none", blur: 0 };
export const FIT: Transform = { x: 0.5, y: 0.5, scale: 1, opacity: 1 };

// Times closer than this are the same time (float noise from drags).
const EPS = 1e-6;

const clamp = (n: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, n));
const finite = (n: number, fallback: number) => (Number.isFinite(n) ? n : fallback);

export const clipLength = (c: Clip) => clipOutLength(c);
export const clipEnd = (c: Clip) => c.start + clipLength(c);
// frameLocalTime is clip-local time lt snapped down to the start of its
// frame at fps: the time the export reads effects and keyframes at (its
// effect graph runs after fps=, so t counts whole frames from the clip's
// start). The small slack keeps a time on a frame boundary from rounding
// down a whole frame.
export const frameLocalTime = (lt: number, fps: number) => (fps > 0 ? Math.floor(lt * fps + 1e-6) / fps : lt);

// --- Lookups -------------------------------------------------------------

export const mediaById = (p: Project, id: string) => p.media.find((m) => m.id === id);
export const trackById = (p: Project, id: string) => p.tracks.find((t) => t.id === id);
export const videoTracks = (p: Project) => p.tracks.filter((t) => t.kind === "video");
export const audioTracks = (p: Project) => p.tracks.filter((t) => t.kind === "audio");

// findClip returns a clip with its track, or undefined.
export function findClip(p: Project, clipId: string): { track: Track; clip: Clip; index: number } | undefined {
  for (const track of p.tracks) {
    const index = track.clips.findIndex((c) => c.id === clipId);
    if (index >= 0) return { track, clip: track.clips[index], index };
  }
  return undefined;
}

// canHold says whether a track of this kind takes that media: video tracks
// take video and images, audio tracks take audio and the audio of a video.
export function canHold(kind: TrackKind, m: Media): boolean {
  if (kind === "video") return m.kind === "video" || m.kind === "image";
  return m.kind === "audio" || (m.kind === "video" && m.hasAudio);
}

// --- Project -------------------------------------------------------------

const newTrack = (kind: TrackKind): Track => ({ id: newId(kind === "video" ? "vt" : "at"), kind, muted: false, hidden: false, locked: false, clips: [] });

// newProject starts a project with one video track and one audio track and
// the first media on the track that takes it.
export function newProject(first: Media, name = baseName(first.path)): Project {
  const p: Project = {
    version: 2,
    id: newId("proj"),
    name,
    canvas: canvasOf(first) ?? { ...DEFAULT_CANVAS },
    media: [first],
    tracks: [newTrack("video"), newTrack("audio")],
    texts: [],
    crop: null,
  };
  return appendClip(p, first.id);
}

// addMedia adds media to the project (no-op if its id is already there).
// The canvas follows the first video or image added, as long as nothing
// visual set it before.
export function addMedia(p: Project, m: Media): Project {
  if (p.media.some((x) => x.id === m.id)) return p;
  const hadVisual = p.media.some((x) => x.kind !== "audio");
  const canvas = !hadVisual ? (canvasOf(m) ?? p.canvas) : p.canvas;
  return { ...p, media: [...p.media, m], canvas };
}

// canvasOf is a media's frame as a canvas (even size, fps default 30), or
// null for audio.
function canvasOf(m: Media): Project["canvas"] | null {
  if (m.kind === "audio" || m.width <= 0 || m.height <= 0) return null;
  const even = (n: number) => Math.max(2, Math.round(n / 2) * 2);
  return { width: even(m.width), height: even(m.height), fps: m.fps > 0 ? m.fps : DEFAULT_CANVAS.fps };
}

// --- Tracks --------------------------------------------------------------

// addTrack adds an empty track: a video track on top of the video tracks, an
// audio track at the bottom. The new track is the last one of its kind.
export function addTrack(p: Project, kind: TrackKind): Project {
  const t = newTrack(kind);
  const tracks = p.tracks.slice();
  if (kind === "video") tracks.splice(videoTracks(p).length, 0, t);
  else tracks.push(t);
  return { ...p, tracks };
}

// removeTrack deletes a track and its clips. The last video track stays.
export function removeTrack(p: Project, trackId: string): Project {
  const t = trackById(p, trackId);
  if (!t || t.locked) return p;
  if (t.kind === "video" && videoTracks(p).length <= 1) return p;
  return { ...p, tracks: p.tracks.filter((x) => x.id !== trackId) };
}

export function setTrackFlags(p: Project, trackId: string, flags: Partial<Pick<Track, "muted" | "hidden" | "locked">>): Project {
  const t = trackById(p, trackId);
  if (!t) return p;
  const next = { ...t, ...flags };
  if (next.muted === t.muted && next.hidden === t.hidden && next.locked === t.locked) return p;
  return withTrack(p, next);
}

// --- Clips ---------------------------------------------------------------

// newClip makes a clip of the whole media at `start`. Clips on the bottom
// video track fit the canvas; on higher tracks they start at 40%, centred.
function newClip(p: Project, m: Media, trackId: string, start: number): Clip {
  const bottom = videoTracks(p)[0]?.id === trackId;
  return {
    id: newId("clip"),
    media: m.id,
    start,
    in: 0,
    out: m.kind === "image" ? IMAGE_LENGTH : m.duration,
    speed: 1,
    volume: 1,
    muted: false,
    fadeIn: 0,
    fadeOut: 0,
    transform: bottom ? { ...FIT } : { ...FIT, scale: OVERLAY_SCALE },
    filters: { ...DEFAULT_FILTERS },
  };
}

// insertClip puts the whole of a media on a track at the nearest free spot
// to `at`. The media must already be in the project.
export function insertClip(p: Project, trackId: string, mediaId: string, at: number): Project {
  const t = trackById(p, trackId);
  const m = mediaById(p, mediaId);
  if (!t || !m || t.locked || !canHold(t.kind, m)) return p;
  const c = newClip(p, m, trackId, 0);
  if (clipLength(c) <= EPS) return p;
  c.start = nearestFree(t.clips, clipLength(c), at);
  return withTrack(p, { ...t, clips: sorted([...t.clips, c]) });
}

// appendClip puts a media at the end of the bottom video track, or of the
// first audio track for audio (adding one if there is none).
export function appendClip(p: Project, mediaId: string): Project {
  const m = mediaById(p, mediaId);
  if (!m) return p;
  let q = p;
  let t = m.kind === "audio" ? audioTracks(q)[0] : videoTracks(q)[0];
  if (!t) {
    q = addTrack(q, m.kind === "audio" ? "audio" : "video");
    t = m.kind === "audio" ? audioTracks(q)[0] : videoTracks(q)[0];
  }
  const end = t.clips.reduce((e, c) => Math.max(e, clipEnd(c)), 0);
  const r = insertClip(q, t.id, mediaId, end);
  return r === q ? p : r;
}

// moveClip moves a clip to `start` on a track of the same kind (its own or
// another), at the nearest free spot. Locked source or target: no-op.
export function moveClip(p: Project, clipId: string, trackId: string, start: number): Project {
  const hit = findClip(p, clipId);
  const to = trackById(p, trackId);
  if (!hit || !to || hit.track.locked || to.locked || hit.track.kind !== to.kind) return p;
  const m = mediaById(p, hit.clip.media);
  if (!m || !canHold(to.kind, m)) return p;
  const others = to.clips.filter((c) => c.id !== clipId);
  const s = nearestFree(others, clipLength(hit.clip), Math.max(0, start));
  if (to.id === hit.track.id && Math.abs(s - hit.clip.start) < EPS) return p;
  const moved = { ...hit.clip, start: s };
  let q = p;
  if (to.id !== hit.track.id) q = withTrack(q, { ...hit.track, clips: hit.track.clips.filter((c) => c.id !== clipId) });
  return withTrack(q, { ...to, clips: sorted([...others, moved]) });
}

// trimClip moves a clip's edge to TIMELINE time t. The start edge moves the
// clip's start and its in point together (the content under the rest stays
// put); the end edge moves its out point. Edges stop at the neighbouring
// clips, the source's ends (images stretch freely) and MIN_CLIP.
export function trimClip(p: Project, clipId: string, edge: "start" | "end", t: number): Project {
  const hit = findClip(p, clipId);
  if (!hit || hit.track.locked) return p;
  const { clip: c, track, index } = hit;
  const m = mediaById(p, c.media);
  if (!m) return p;
  const srcMax = m.kind === "image" || c.hold ? Infinity : m.duration;
  const prevEnd = index > 0 ? clipEnd(track.clips[index - 1]) : 0;
  const nextStart = index < track.clips.length - 1 ? track.clips[index + 1].start : Infinity;
  let next: Clip;
  if (c.hold && edge === "start") {
    // A hold clip keeps its frame (in); the start edge only changes its length.
    const end = clipEnd(c);
    const s = clamp(finite(t, c.start), prevEnd, Math.max(prevEnd, end - MIN_CLIP));
    next = { ...c, start: s, out: c.in + (end - s) };
  } else {
    // u: source seconds from where the clip starts playing (speed.ts); a
    // reversed clip counts down from out. uMin/uMax: how far the source goes.
    const len = c.out - c.in;
    const uMin = c.reversed ? -(srcMax - c.out) : -c.in;
    const uMax = c.reversed ? c.out : srcMax - c.in;
    if (edge === "start") {
      // Earliest: the previous clip, timeline 0, or the source's start.
      const lo = Math.max(prevEnd, c.start + localOut(c, uMin));
      const hi = clipEnd(c) - MIN_CLIP;
      const s = clamp(finite(t, c.start), lo, Math.max(lo, hi));
      const u0 = Math.max(uMin, localSrc(c, s - c.start));
      next = { ...c, start: s, ...retime(c, u0, len) };
    } else {
      const lo = c.start + MIN_CLIP;
      const hi = Math.min(nextStart, c.start + localOut(c, uMax));
      const e = clamp(finite(t, clipEnd(c)), Math.min(lo, hi), hi);
      next = { ...c, ...retime(c, 0, Math.min(uMax, localSrc(c, e - c.start))) };
    }
  }
  if (Math.abs(next.start - c.start) < EPS && Math.abs(next.in - c.in) < EPS && Math.abs(next.out - c.out) < EPS) return p;
  next = fitFades(next);
  const clips = track.clips.slice();
  clips[index] = next;
  return withTrack(p, { ...track, clips });
}

// splitAt cuts every clip under timeline time t in two (on one track when
// trackId is given; locked tracks are skipped). The left part keeps the
// clip's id and fade in, the right part gets a new id and the fade out.
// Clips within MIN_CLIP of t's edge are left whole.
export function splitAt(p: Project, t: number, trackId?: string): Project {
  let changed = false;
  const tracks = p.tracks.map((track) => {
    if (track.locked || (trackId && track.id !== trackId)) return track;
    const i = track.clips.findIndex((c) => c.start + MIN_CLIP <= t + EPS && t <= clipEnd(c) - MIN_CLIP + EPS);
    if (i < 0) return track;
    const c = track.clips[i];
    // A hold clip's halves both show its frame (in); only the lengths split.
    let left: Clip;
    let right: Clip;
    if (c.hold) {
      left = fitFades({ ...c, out: c.in + (t - c.start), fadeOut: 0 });
      right = fitFades({ ...c, id: newId("clip"), start: t, out: c.in + (clipEnd(c) - t), fadeIn: 0, transition: undefined });
    } else {
      // Each half keeps its part of the speed curve (and direction).
      const u = localSrc(c, t - c.start);
      left = fitFades({ ...c, ...retime(c, 0, u), fadeOut: 0 });
      right = fitFades({ ...c, id: newId("clip"), start: t, ...retime(c, u, c.out - c.in), fadeIn: 0, transition: undefined });
    }
    const clips = track.clips.slice();
    clips.splice(i, 1, left, right);
    changed = true;
    return { ...track, clips };
  });
  return changed ? { ...p, tracks } : p;
}

// freezeFrame splits the clip under timeline time t on a track and puts a
// hold clip (the frame at t, silent, len s long) between the halves; the
// rest of that track moves right by len. Other tracks don't move. Returns
// the same project when there's no clip there (or the track is locked).
export function freezeFrame(p: Project, trackId: string, t: number, len = DEFAULT_FREEZE): Project {
  const track = trackById(p, trackId);
  if (!track || track.locked || track.kind !== "video" || !(len > 0)) return p;
  const c = track.clips.find((x) => x.start - EPS <= t && t < clipEnd(x) - EPS);
  if (!c || c.hold) return p;
  const m = mediaById(p, c.media);
  if (!m || m.kind !== "video") return p;
  // Too close to an edge to split: freeze at that edge instead.
  const at = t - c.start < MIN_CLIP ? c.start : clipEnd(c) - t < MIN_CLIP ? clipEnd(c) : t;
  const frame = clamp(toSource(c, localSrc(c, t - c.start)), c.in, c.out);
  const hold: Clip = {
    ...c,
    id: newId("clip"),
    start: at,
    in: frame,
    out: frame + len,
    speed: 1,
    muted: true,
    fadeIn: 0,
    fadeOut: 0,
    hold: true,
  };
  delete hold.speedCurve;
  delete hold.reversed;
  delete hold.blendFrames;
  // Split there (unless it's an edge), then push everything from there on.
  const split = at === t ? splitAt(p, t, trackId) : p;
  const t2 = trackById(split, trackId)!;
  const shifted = t2.clips.map((x) => (x.start >= at - EPS ? { ...x, start: x.start + len } : x));
  return withTrack(split, { ...t2, clips: sorted([...shifted, hold]) });
}

// removeClip deletes a clip and leaves a gap.
export function removeClip(p: Project, clipId: string): Project {
  const hit = findClip(p, clipId);
  if (!hit || hit.track.locked) return p;
  return withTrack(p, { ...hit.track, clips: hit.track.clips.filter((c) => c.id !== clipId) });
}

// rippleDelete deletes a clip and pulls the later clips on its track left by
// its length (Shift+Delete). Other tracks don't move.
export function rippleDelete(p: Project, clipId: string): Project {
  const hit = findClip(p, clipId);
  if (!hit || hit.track.locked) return p;
  const len = clipLength(hit.clip);
  const clips = hit.track.clips
    .filter((c) => c.id !== clipId)
    .map((c) => (c.start >= hit.clip.start - EPS ? { ...c, start: Math.max(0, c.start - len) } : c));
  return withTrack(p, { ...hit.track, clips });
}

// closeGap removes the empty stretch of a track under timeline time t (Close
// gap): the clips after it move left by its length. No-op when a clip is
// under t or nothing comes after it.
export function closeGap(p: Project, trackId: string, t: number): Project {
  const track = trackById(p, trackId);
  if (!track || track.locked) return p;
  if (track.clips.some((c) => c.start - EPS <= t && t < clipEnd(c) - EPS)) return p;
  const next = track.clips.find((c) => c.start >= t - EPS);
  if (!next) return p;
  const gapStart = track.clips.filter((c) => clipEnd(c) <= t + EPS).reduce((e, c) => Math.max(e, clipEnd(c)), 0);
  const len = next.start - gapStart;
  if (len <= EPS) return p;
  const clips = track.clips.map((c) => (c.start >= next.start - EPS ? { ...c, start: c.start - len } : c));
  return withTrack(p, { ...track, clips });
}

// canExtractAudio says whether a clip has sound to detach: a video clip
// with audio on a video track, not a freeze frame, not already muted.
export function canExtractAudio(p: Project, clipId: string): boolean {
  const hit = findClip(p, clipId);
  if (!hit || hit.track.locked || hit.track.kind !== "video") return false;
  const m = mediaById(p, hit.clip.media);
  return !!m && m.kind === "video" && m.hasAudio && !hit.clip.hold && !hit.clip.muted;
}

// extractAudio detaches a video clip's sound onto an audio track (Extract
// audio): soundOf(clip), on the first unlocked audio track that is free
// there, or a new one. The video clip goes silent. Returns the same project when
// the clip has nothing to extract (canExtractAudio).
export function extractAudio(p: Project, clipId: string): Project {
  if (!canExtractAudio(p, clipId)) return p;
  const { clip: c } = findClip(p, clipId)!;
  const len = clipLength(c);
  const free = (t: Track) => !t.locked && t.clips.every((x) => clipEnd(x) <= c.start + EPS || x.start >= c.start + len - EPS);
  let q = p;
  let to = audioTracks(q).find(free);
  if (!to) {
    q = addTrack(q, "audio");
    to = audioTracks(q).at(-1)!;
  }
  const audio = soundOf(c);
  q = withTrack(q, { ...to, clips: sorted([...to.clips, audio]) });
  // The video clip keeps its picture and goes silent; its volume keys
  // moved with the sound.
  const hit = findClip(q, clipId)!;
  const silent: Clip = { ...hit.clip, muted: true };
  if (silent.anim?.volume) {
    const rest = { ...silent.anim };
    delete rest.volume;
    if (Object.keys(rest).length) silent.anim = rest;
    else delete silent.anim;
  }
  const clips = hit.track.clips.slice();
  clips[hit.index] = silent;
  return withTrack(q, { ...hit.track, clips });
}

// soundOf is a clip's sound as a clip of its own (new id) for an audio
// track: the same media, place and range, the clip's timing (speed, curve,
// reverse), volume, fades, stream levels and volume keyframes, unmuted.
export function soundOf(c: Clip): Clip {
  const audio: Clip = {
    id: newId("clip"),
    media: c.media,
    start: c.start,
    in: c.in,
    out: c.out,
    speed: c.speed,
    volume: c.volume,
    muted: false,
    fadeIn: c.fadeIn,
    fadeOut: c.fadeOut,
    transform: { ...FIT },
    filters: { ...DEFAULT_FILTERS },
  };
  if (c.levels) audio.levels = c.levels.map((l) => ({ ...l }));
  if (c.speedCurve) audio.speedCurve = structuredClone(c.speedCurve);
  if (c.reversed) audio.reversed = true;
  if (c.anim?.volume?.length) audio.anim = { volume: c.anim.volume.map((k) => ({ ...k })) };
  return audio;
}

// soundProject is a project holding only a clip's sound, at 0 on an audio
// track (and an empty video track), for "Save audio as file": exported as
// m4a or wav it gives exactly what the clip plays, every audio stream mixed
// as the clip mixes them. Null when the clip has no sound.
export function soundProject(p: Project, clipId: string): Project | null {
  const hit = findClip(p, clipId);
  const m = hit && mediaById(p, hit.clip.media);
  if (!hit || !m || hit.clip.hold || !(m.kind === "audio" || (m.kind === "video" && m.hasAudio))) return null;
  const clip = { ...soundOf(hit.clip), start: 0 };
  return {
    ...p,
    media: [m],
    tracks: [newTrack("video"), { ...newTrack("audio"), clips: [clip] }],
    texts: [],
    crop: null,
  };
}

// duplicateClip puts a copy of a clip (new ids, its effects' too) on the
// same track at the nearest free spot after it.
export function duplicateClip(p: Project, clipId: string): Project {
  const hit = findClip(p, clipId);
  if (!hit || hit.track.locked) return p;
  const c = hit.clip;
  const copy: Clip = { ...structuredClone(c), id: newId("clip") };
  if (copy.effects) copy.effects = copy.effects.map((e) => ({ ...e, id: newId("fx") }));
  copy.start = nearestFree(hit.track.clips, clipLength(c), clipEnd(c));
  if (!Number.isFinite(copy.start)) return p;
  return withTrack(p, { ...hit.track, clips: sorted([...hit.track.clips, copy]) });
}

export type ClipPatch = Partial<Omit<Clip, "id" | "media" | "start" | "in" | "out">>;

// updateClip changes a clip's settings, each clamped to its range. A speed
// change keeps the clip's start; if the clip would run into the next clip,
// the speed is raised just enough to fit.
export function updateClip(p: Project, clipId: string, patch: ClipPatch): Project {
  const hit = findClip(p, clipId);
  if (!hit || hit.track.locked) return p;
  const { clip: c, track, index } = hit;
  const next: Clip = { ...c, ...patch };
  next.speed = clamp(finite(next.speed, 1), MIN_SPEED, MAX_SPEED);
  if (next.hold) {
    // A freeze frame has no time of its own to change.
    next.speed = 1;
    delete next.speedCurve;
    delete next.reversed;
    delete next.blendFrames;
  }
  if (next.speedCurve) next.speedCurve = normCurve(next.speedCurve, next.out - next.in);
  else delete next.speedCurve;
  if (!next.reversed) delete next.reversed;
  if (next.blendFrames !== "mix" && next.blendFrames !== "smooth") delete next.blendFrames;
  const nextStart = index < track.clips.length - 1 ? track.clips[index + 1].start : Infinity;
  const room = nextStart - c.start;
  // A curve that runs into the next clip is refused (its shape is the
  // point); a constant speed is raised just enough to fit.
  if (next.speedCurve && clipLength(next) > room + EPS) return p;
  if (!next.speedCurve && (next.out - next.in) / next.speed > room + EPS) next.speed = Math.min(MAX_SPEED, (next.out - next.in) / room);
  if (clipLength(next) > room + EPS) return p; // can't fit even at 4x
  next.volume = clamp(finite(next.volume, 1), 0, 2);
  const rotation = next.transform.rotation;
  next.transform = {
    x: clamp(finite(next.transform.x, 0.5), -1, 2),
    y: clamp(finite(next.transform.y, 0.5), -1, 2),
    scale: clamp(finite(next.transform.scale, 1), 0.01, 10),
    opacity: clamp(finite(next.transform.opacity, 1), 0, 1),
  };
  if (rotation !== undefined) next.transform.rotation = clamp(finite(rotation, 0), -360, 360);
  next.filters = {
    brightness: clamp(finite(next.filters.brightness, 0), -1, 1),
    contrast: clamp(finite(next.filters.contrast, 1), 0, 2),
    saturation: clamp(finite(next.filters.saturation, 1), 0, 3),
    look: next.filters.look,
    blur: clamp(finite(next.filters.blur, 0), 0, 20),
  };
  if (next.chroma) {
    next.chroma = {
      color: next.chroma.color,
      similarity: clamp(finite(next.chroma.similarity, DEFAULT_CHROMA.similarity), 0.01, 1),
      blend: clamp(finite(next.chroma.blend, DEFAULT_CHROMA.blend), 0, 1),
    };
  }
  const fitted = fitFades(next);
  if (sameClip(fitted, c)) return p;
  const clips = track.clips.slice();
  clips[index] = fitted;
  return withTrack(p, { ...track, clips });
}

// --- Texts and crop ----------------------------------------------------------

export function upsertProjectText(p: Project, t: Text): Project {
  const i = p.texts.findIndex((x) => x.id === t.id);
  const start = Math.max(0, t.start);
  const fixed = { ...t, start, end: Math.max(start, t.end) };
  const texts = p.texts.slice();
  if (i < 0) texts.push(fixed);
  else texts[i] = fixed;
  return { ...p, texts };
}

export function removeProjectText(p: Project, id: string): Project {
  if (!p.texts.some((t) => t.id === id)) return p;
  return { ...p, texts: p.texts.filter((t) => t.id !== id) };
}

export function setCrop(p: Project, crop: Rect | null): Project {
  if (crop === p.crop || (crop && p.crop && crop.x === p.crop.x && crop.y === p.crop.y && crop.w === p.crop.w && crop.h === p.crop.h)) return p;
  return { ...p, crop };
}

// --- Time ----------------------------------------------------------------

// projectDuration is where the last clip on any track ends.
export function projectDuration(p: Project): number {
  let end = 0;
  for (const t of p.tracks) for (const c of t.clips) end = Math.max(end, clipEnd(c));
  return end;
}

// srcT: the source time to show; rate: how fast the source plays there
// (speed or the curve's speed; 0 for a freeze frame).
// trans: the clip is in a transition's window (transition.ts): "out" for
// the outgoing clip, "in" for the incoming one, with xfade's progress. A
// clip outside its own range in the window holds its edge frame (rate 0).
export type ActiveClip = {
  track: Track;
  clip: Clip;
  media: Media;
  srcT: number;
  rate: number;
  trans?: { tr: Transition; role: "out" | "in"; progress: number };
};

// clipsAt lists the clips playing at timeline time t: visible video tracks
// bottom→top, then the audio tracks, each with the source time to show.
// A clip covers [start, end). Track mute is left to the caller.
export function clipsAt(p: Project, t: number): ActiveClip[] {
  const out: ActiveClip[] = [];
  const at = (track: Track, clip: Clip, media: Media, lt: number): ActiveClip => {
    if (clip.hold) return { track, clip, media, srcT: clip.in, rate: 0 };
    const u = localSrc(clip, lt);
    return { track, clip, media, srcT: toSource(clip, u), rate: rateAt(clip, u) };
  };
  for (const track of p.tracks) {
    if (track.kind === "video" && track.hidden) continue;
    const i = track.clips.findIndex((c) => c.start - EPS <= t && t < clipEnd(c) - EPS);
    const clip = track.clips[i];
    if (!clip) continue;
    const media = mediaById(p, clip.media);
    if (!media) continue;
    // In a transition's window both clips show: the one under t plays, the
    // other holds its edge frame.
    const trans = track.kind === "video" ? trackTransitions(track) : null;
    const tin = trans?.get(i);
    const tout = trans?.get(i + 1);
    if (tin && t < tin.end) {
      const prev = track.clips[i - 1];
      const pm = mediaById(p, prev.media);
      const progress = transitionProgress(tin, t, p.canvas.fps);
      if (pm) out.push({ ...at(track, prev, pm, clipLength(prev) - FRAME_EPS), rate: 0, trans: { tr: tin, role: "out", progress } });
      out.push({ ...at(track, clip, media, t - clip.start), trans: { tr: tin, role: "in", progress } });
      continue;
    }
    if (tout && t >= tout.start) {
      const next = track.clips[i + 1];
      const nm = mediaById(p, next.media);
      const progress = transitionProgress(tout, t, p.canvas.fps);
      out.push({ ...at(track, clip, media, t - clip.start), trans: { tr: tout, role: "out", progress } });
      if (nm) out.push({ ...at(track, next, nm, 0), rate: 0, trans: { tr: tout, role: "in", progress } });
      continue;
    }
    out.push(at(track, clip, media, t - clip.start));
  }
  return out;
}

// FRAME_EPS keeps an outgoing clip's held frame inside the clip: its last
// frame, as the export's tpad repeats.
const FRAME_EPS = 1e-3;

// timelineTimeAt is the timeline time at which clip shows source time srcT
// (clipsAt's srcT the other way round), kept inside the clip.
export function timelineTimeAt(clip: Clip, srcT: number): number {
  const t = clip.start + (clip.hold ? 0 : localOut(clip, fromSource(clip, srcT)));
  return Math.max(clip.start, Math.min(clipEnd(clip) - EPS, t));
}

// --- v1 and export ---------------------------------------------------------

// fromEdl turns a v1 edit into a project: each v1 source becomes video media,
// the segments sit back to back on the video track, texts and crop carry over,
// and v1 per-source-track levels go onto that source's clips. `infos` (by
// source index) fills in fps and audio tracks when known.
export function fromEdl(
  edl: Edl,
  extra: {
    crop?: Rect | null;
    levels?: TrackLevel[];
    infos?: ({ fps: number; hasAudio: boolean; tracks: AudioTrack[] } | null | undefined)[];
  } = {},
): Project {
  const media: Media[] = edl.sources.map((s, i) => {
    const info = extra.infos?.[i];
    return {
      id: newId("media"),
      path: s.path,
      url: s.url,
      kind: "video",
      duration: s.duration,
      width: s.width,
      height: s.height,
      fps: info?.fps ?? 0,
      hasAudio: info?.hasAudio ?? true,
      tracks: info?.tracks ?? [],
    };
  });
  const video = newTrack("video");
  let at = 0;
  for (const seg of edl.segs) {
    const m = media[seg.source];
    if (!m) continue;
    const levels = (extra.levels ?? []).filter((l) => l.source === seg.source).map((l) => ({ ...l, source: 0 }));
    const c: Clip = {
      id: newId("clip"),
      media: m.id,
      start: at,
      in: seg.start,
      out: seg.end,
      speed: 1,
      volume: 1,
      muted: false,
      fadeIn: 0,
      fadeOut: 0,
      transform: { ...FIT },
      filters: { ...DEFAULT_FILTERS },
    };
    if (levels.length) c.levels = levels;
    video.clips.push(c);
    at += seg.end - seg.start;
  }
  const first = media[0];
  return {
    version: 2,
    id: newId("proj"),
    name: first ? baseName(first.path) : "Untitled",
    canvas: (first && canvasOf(first)) ?? { ...DEFAULT_CANVAS },
    media,
    tracks: [video, newTrack("audio")],
    texts: edl.texts.map((t) => ({ ...t })),
    crop: extra.crop ?? null,
  };
}

// The project as the Go export takes it (internal/vid/project.go,
// ProjectJSON): the same shape and names, with every time in whole
// milliseconds (Media.duration, Clip.start/in/out/fadeIn/fadeOut, Text
// start/end). Track flags are sent as they are; the export skips hidden
// video tracks and muted tracks.
export type ProjectJSON = Omit<Project, "texts"> & { texts: Text[] };
export interface ProjectExport {
  project: ProjectJSON;
  preset: "original" | "1080p" | "720p" | "discord" | "custom";
  height?: number;
  kbps?: number;
  outPath: string;
  options?: ExportSettings; // the export screen's settings (Go ExportOptions); absent = the preset's
}

// ExportSettings mirrors Go's ExportOptions (internal/vid/export_options.go):
// format and size, quality, audio, the range (ms on the timeline) and GIF
// settings. Zero / absent fields take the Go defaults.
export type ExportFormat = "mp4" | "hevc" | "webm" | "gif" | "m4a";
export interface ExportSettings {
  format: ExportFormat | "wav"; // wav: audio only, PCM (Save audio as file)
  width?: number;
  height?: number;
  fit?: "fit" | "fill";
  fps?: number;
  quality?: "low" | "medium" | "high" | "best";
  kbps?: number;
  encoder?: string;
  noAudio?: boolean;
  audioKbps?: number;
  sampleRate?: number;
  rangeIn?: number;
  rangeOut?: number;
  gif?: { fps?: number; width?: number; loop?: number; dither?: string; colors?: number };
}

// toExportRequest builds the export request. Blank texts and texts with no
// time on screen are dropped, and texts are clipped to the project's length.
export function toExportRequest(
  p: Project,
  extra: { preset: ProjectExport["preset"]; height?: number; kbps?: number; outPath?: string; options?: ExportSettings },
): ProjectExport {
  const ms = (s: number) => Math.round(s * 1000);
  const dur = projectDuration(p);
  const project: ProjectJSON = {
    ...p,
    media: p.media.map((m) => ({ ...m, duration: ms(m.duration) })),
    tracks: p.tracks.map((t) => ({
      ...t,
      clips: t.clips.map((c) => ({
        ...c,
        start: ms(c.start),
        in: ms(c.in),
        out: ms(c.out),
        fadeIn: ms(c.fadeIn),
        fadeOut: ms(c.fadeOut),
        ...v3Ms(c, ms),
      })),
    })),
    texts: p.texts
      .filter((t) => t.text.trim())
      .map((t) => ({ ...t, start: ms(Math.min(t.start, dur)), end: ms(Math.min(t.end, dur)) }))
      .filter((t) => t.end > t.start),
  };
  const req: ProjectExport = { project, preset: extra.preset, outPath: extra.outPath ?? "" };
  if (extra.preset === "custom") {
    if (extra.height) req.height = extra.height;
    if (extra.kbps) req.kbps = extra.kbps;
  }
  if (extra.options) req.options = extra.options;
  return req;
}

// v3Ms is a clip's v3 fields with keyframe times and speed-curve points in
// ms, the way the export takes them (only the fields the clip has).
function v3Ms(c: Clip, ms: (s: number) => number): Partial<Clip> {
  const keys = (ks: Keyframe[]) => ks.map((k) => ({ ...k, t: ms(k.t) }));
  const out: Partial<Clip> = {};
  if (c.effects) out.effects = c.effects.map((e) => (e.keys ? { ...e, keys: Object.fromEntries(Object.entries(e.keys).map(([p, ks]) => [p, keys(ks)])) } : e));
  if (c.anim) out.anim = Object.fromEntries(Object.entries(c.anim).map(([p, ks]) => [p, keys(ks ?? [])]));
  if (c.speedCurve) out.speedCurve = { ...c.speedCurve, points: c.speedCurve.points.map((pt) => ({ s: ms(pt.s), v: pt.v })) };
  if (c.transition) out.transition = { ...c.transition, duration: ms(c.transition.duration) };
  return out;
}

// --- History -------------------------------------------------------------

// useProjectHistory: v1's undo/redo (keyed drag merges) over a Project.
export function useProjectHistory(init: Project) {
  const { doc, ...rest } = useHistory(init);
  return { project: doc, ...rest };
}

// --- Helpers -------------------------------------------------------------

function withTrack(p: Project, t: Track): Project {
  return { ...p, tracks: p.tracks.map((x) => (x.id === t.id ? t : x)) };
}

const sorted = (clips: Clip[]) => clips.slice().sort((a, b) => a.start - b.start);

// nearestFree is the start nearest to `at` (>= 0) where a clip of length len
// fits among `clips` (sorted, none overlapping) without overlapping any.
export function nearestFree(clips: Clip[], len: number, at: number): number {
  const want = Math.max(0, finite(at, 0));
  const list = sorted(clips);
  let best = Infinity;
  let bestDist = Infinity;
  let gapStart = 0;
  for (let i = 0; i <= list.length; i++) {
    const gapEnd = i < list.length ? list[i].start : Infinity;
    if (gapEnd - gapStart >= len - EPS) {
      const s = clamp(want, gapStart, gapEnd - len);
      const d = Math.abs(s - want);
      if (d < bestDist - EPS) {
        best = s;
        bestDist = d;
      }
    }
    if (i < list.length) gapStart = Math.max(gapStart, clipEnd(list[i]));
  }
  return best;
}

// fitFades keeps fades non-negative and together no longer than the clip.
function fitFades(c: Clip): Clip {
  const len = clipLength(c);
  const fadeIn = clamp(finite(c.fadeIn, 0), 0, len);
  const fadeOut = clamp(finite(c.fadeOut, 0), 0, len - fadeIn);
  return fadeIn === c.fadeIn && fadeOut === c.fadeOut ? c : { ...c, fadeIn, fadeOut };
}

function sameClip(a: Clip, b: Clip): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

function baseName(p: string): string {
  return p.replace(/\\/g, "/").split("/").pop() ?? p;
}
