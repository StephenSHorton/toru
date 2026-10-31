import { newId } from "@/trim/edl";
import {
  addMedia,
  addTrack,
  clipEnd,
  clipLength,
  findClip,
  mediaById,
  updateClip,
  type Clip,
  type ClipPatch,
  type Media,
  type Project,
  type Track,
  type TrackKind,
} from "@/trim/project";

// multi.ts holds the timeline's operations on several clips at once (a
// multi-selection) and its clip clipboard. Pure, like project.ts: each op
// returns a new Project, or the same object when nothing changes, so one
// apply() is one undo step for the whole group.
//
// Locked tracks: delete, ripple delete, mute and paste attributes skip the
// clips on locked tracks and act on the rest (as the one-clip ops do). Move,
// duplicate and paste are all-or-nothing: a group never lands half placed.

const EPS = 1e-6;

const sorted = (clips: Clip[]) => clips.slice().sort((a, b) => a.start - b.start);
const withTracks = (p: Project, tracks: Map<string, Track>): Project =>
  tracks.size ? { ...p, tracks: p.tracks.map((t) => tracks.get(t.id) ?? t) } : p;

// liveIds keeps the ids that still name a clip in p, in order, without
// repeats (a selection can outlive its clips across undo and delete).
export function liveIds(p: Project, ids: readonly string[]): string[] {
  const seen = new Set<string>();
  return ids.filter((id) => !seen.has(id) && !!findClip(p, id) && !!seen.add(id));
}

// clipsInRange lists the clips that overlap timeline [t0, t1] on the given
// tracks (a marquee), in track then time order.
export function clipsInRange(p: Project, trackIds: readonly string[], t0: number, t1: number): string[] {
  const lo = Math.min(t0, t1);
  const hi = Math.max(t0, t1);
  const out: string[] = [];
  for (const t of p.tracks) {
    if (!trackIds.includes(t.id)) continue;
    for (const c of t.clips) if (c.start < hi - EPS && clipEnd(c) > lo + EPS) out.push(c.id);
  }
  return out;
}

// allClipIds is every clip in the project (Select all).
export const allClipIds = (p: Project): string[] => p.tracks.flatMap((t) => t.clips.map((c) => c.id));

// clipRangeOnTrack lists the clips on a's track from a to b, both included
// (Shift+click). Empty when they're on different tracks.
export function clipRangeOnTrack(p: Project, a: string, b: string): string[] {
  const ha = findClip(p, a);
  const hb = findClip(p, b);
  if (!ha || !hb || ha.track.id !== hb.track.id) return [];
  const [i, j] = ha.index <= hb.index ? [ha.index, hb.index] : [hb.index, ha.index];
  return ha.track.clips.slice(i, j + 1).map((c) => c.id);
}

// --- group edits -------------------------------------------------------------

// removeClips deletes the clips, leaving gaps.
export function removeClips(p: Project, ids: readonly string[]): Project {
  const drop = new Set(ids);
  const tracks = new Map<string, Track>();
  for (const t of p.tracks) {
    if (t.locked || !t.clips.some((c) => drop.has(c.id))) continue;
    tracks.set(t.id, { ...t, clips: t.clips.filter((c) => !drop.has(c.id)) });
  }
  return withTracks(p, tracks);
}

// rippleDeleteClips deletes the clips and, on each track, pulls every later
// clip left by the length of the deleted clips before it. Other tracks
// don't move.
export function rippleDeleteClips(p: Project, ids: readonly string[]): Project {
  const drop = new Set(ids);
  const tracks = new Map<string, Track>();
  for (const t of p.tracks) {
    if (t.locked || !t.clips.some((c) => drop.has(c.id))) continue;
    const gone = t.clips.filter((c) => drop.has(c.id));
    const clips = t.clips
      .filter((c) => !drop.has(c.id))
      .map((c) => {
        const shift = gone.filter((g) => g.start <= c.start + EPS).reduce((s, g) => s + clipLength(g), 0);
        return shift > EPS ? { ...c, start: Math.max(0, c.start - shift) } : c;
      });
    tracks.set(t.id, { ...t, clips });
  }
  return withTracks(p, tracks);
}

// setClipsMuted mutes or unmutes the clips.
export function setClipsMuted(p: Project, ids: readonly string[], muted: boolean): Project {
  return updateClips(p, ids, { muted });
}

// updateClips applies the same patch to every clip (updateClip's clamping;
// locked tracks skipped).
export function updateClips(p: Project, ids: readonly string[], patch: ClipPatch | ((c: Clip) => ClipPatch)): Project {
  let q = p;
  for (const id of ids) {
    const hit = findClip(q, id);
    if (!hit) continue;
    q = updateClip(q, id, typeof patch === "function" ? patch(hit.clip) : patch);
  }
  return q;
}

// moveClips shifts the clips by dt seconds on their own tracks, together. If
// some would land on another clip or before 0, dt is cut back to the
// furthest it can go in that direction; 0 there means no change.
export function moveClips(p: Project, ids: readonly string[], dt: number): Project {
  const moving = new Set(liveIds(p, ids));
  if (!moving.size || !Number.isFinite(dt) || Math.abs(dt) < EPS) return p;
  const groups: { track: Track; mine: Clip[]; rest: Clip[] }[] = [];
  for (const t of p.tracks) {
    const mine = t.clips.filter((c) => moving.has(c.id));
    if (!mine.length) continue;
    if (t.locked) return p;
    groups.push({ track: t, mine, rest: t.clips.filter((c) => !moving.has(c.id)) });
  }
  // The furthest the group can go: no clip before 0, none over a clip that stays.
  let lo = -Infinity;
  let hi = Infinity;
  for (const g of groups) {
    for (const c of g.mine) {
      lo = Math.max(lo, -c.start);
      for (const r of g.rest) {
        if (clipEnd(r) <= c.start + EPS) lo = Math.max(lo, clipEnd(r) - c.start);
        else if (r.start >= clipEnd(c) - EPS) hi = Math.min(hi, r.start - clipEnd(c));
        else return p; // already overlapping: leave it alone
      }
    }
  }
  const d = Math.max(lo, Math.min(hi, dt));
  if (Math.abs(d) < EPS) return p;
  const tracks = new Map<string, Track>();
  for (const g of groups) {
    tracks.set(g.track.id, { ...g.track, clips: sorted([...g.rest, ...g.mine.map((c) => ({ ...c, start: c.start + d }))]) });
  }
  return withTracks(p, tracks);
}

// --- clipboard ---------------------------------------------------------------

// A copied clip: the clip as it was, its media, and where it sat relative to
// the group (time from the earliest clip, and its track's place among the
// copied tracks of its kind, 0 = the lowest).
export type ClipboardItem = { clip: Clip; media: Media; kind: TrackKind; offset: number; lane: number };
export type Clipboard = { items: ClipboardItem[] };

// copyClips takes the clips (and their media) for pasting. Null when none
// of the ids is a clip.
export function copyClips(p: Project, ids: readonly string[]): Clipboard | null {
  const hits = liveIds(p, ids)
    .map((id) => findClip(p, id)!)
    .filter((h) => !!mediaById(p, h.clip.media));
  if (!hits.length) return null;
  const t0 = Math.min(...hits.map((h) => h.clip.start));
  const laneOf = (kind: TrackKind) => {
    const order = p.tracks.filter((t) => t.kind === kind && hits.some((h) => h.track.id === t.id)).map((t) => t.id);
    return (trackId: string) => order.indexOf(trackId);
  };
  const lanes = { video: laneOf("video"), audio: laneOf("audio") };
  return {
    items: hits.map((h) => ({
      clip: structuredClone(h.clip),
      media: structuredClone(mediaById(p, h.clip.media)!),
      kind: h.track.kind,
      offset: h.clip.start - t0,
      lane: lanes[h.track.kind](h.track.id),
    })),
  };
}

// clipboard is the editor's clip clipboard: one per window, so a copy can be
// pasted into another project opened in the same window. Not the system
// clipboard (clips aren't text or files).
let held: Clipboard | null = null;
export const clipboard = {
  get: (): Clipboard | null => held,
  set: (c: Clipboard | null) => {
    held = c;
  },
};

// freshCopy is a clip with new ids (its effects' too) at start.
function freshCopy(c: Clip, start: number): Clip {
  const copy: Clip = { ...structuredClone(c), id: newId("clip"), start };
  if (copy.effects) copy.effects = copy.effects.map((e) => ({ ...e, id: newId("fx") }));
  return copy;
}

const fits = (track: Track, start: number, len: number) =>
  track.clips.every((c) => clipEnd(c) <= start + EPS || c.start >= start + len - EPS);

// firstFit is the earliest t >= at where every item fits on its track: at
// itself, or just after one of the clips in the way.
function firstFit(placed: { track: Track; offset: number; len: number }[], at: number): number {
  const candidates = [Math.max(0, at)];
  for (const pl of placed) for (const c of pl.track.clips) candidates.push(clipEnd(c) - pl.offset);
  for (const t of candidates.filter((x) => x >= at - EPS).sort((a, b) => a - b)) {
    const s = Math.max(0, t);
    if (placed.every((pl) => fits(pl.track, s + pl.offset, pl.len))) return s;
  }
  return Infinity;
}

// pasteClips puts the clipboard's clips (new ids) on the timeline at the
// earliest time >= at where they all fit, keeping their spacing and their
// tracks' order. The lowest copied track of each kind lands on `anchor`
// when it is of that kind, else on the first unlocked track of that kind;
// tracks are added when there aren't enough. Media missing from the project
// is added. Returns p when nothing can be placed (a locked target track).
export function pasteClips(p: Project, cb: Clipboard | null, at: number, anchor?: string): Project {
  if (!cb?.items.length) return p;
  let q = p;
  for (const it of cb.items) q = addMedia(q, it.media);
  const targets: Record<TrackKind, Track[]> = { video: [], audio: [] };
  for (const kind of ["video", "audio"] as const) {
    const lanes = Math.max(-1, ...cb.items.filter((i) => i.kind === kind).map((i) => i.lane)) + 1;
    if (!lanes) continue;
    let ofKind = q.tracks.filter((t) => t.kind === kind);
    const anchorTrack = ofKind.find((t) => t.id === anchor);
    let first = anchorTrack ? ofKind.indexOf(anchorTrack) : ofKind.findIndex((t) => !t.locked);
    if (first < 0) first = ofKind.length;
    while (ofKind.length < first + lanes) {
      q = addTrack(q, kind);
      ofKind = q.tracks.filter((t) => t.kind === kind);
    }
    targets[kind] = ofKind.slice(first, first + lanes);
  }
  const placed = cb.items.map((it) => ({ it, track: targets[it.kind][it.lane], offset: it.offset, len: clipLength(it.clip) }));
  if (placed.some((pl) => !pl.track || pl.track.locked)) return p;
  const s = firstFit(placed, at);
  if (!Number.isFinite(s)) return p;
  const add = new Map<string, Clip[]>();
  for (const pl of placed) add.set(pl.track.id, [...(add.get(pl.track.id) ?? []), freshCopy(pl.it.clip, s + pl.offset)]);
  const tracks = new Map<string, Track>();
  for (const t of q.tracks) {
    const extra = add.get(t.id);
    if (extra) tracks.set(t.id, { ...t, clips: sorted([...t.clips, ...extra]) });
  }
  return withTracks(q, tracks);
}

// pastedIds names the clips pasteClips added: the ids in `after` that
// weren't in `before` (to select what was pasted).
export function pastedIds(before: Project, after: Project): string[] {
  const had = new Set(allClipIds(before));
  return allClipIds(after).filter((id) => !had.has(id));
}

// duplicateClips puts a copy of the group right after it (the earliest
// place after the group's end where it all fits), on the same tracks.
export function duplicateClips(p: Project, ids: readonly string[]): Project {
  const live = liveIds(p, ids);
  if (!live.length) return p;
  const hits = live.map((id) => findClip(p, id)!);
  if (hits.some((h) => h.track.locked)) return p;
  const t0 = Math.min(...hits.map((h) => h.clip.start));
  const end = Math.max(...hits.map((h) => clipEnd(h.clip)));
  const placed = hits.map((h) => ({ clip: h.clip, track: h.track, offset: h.clip.start - t0, len: clipLength(h.clip) }));
  const s = firstFit(placed, end);
  if (!Number.isFinite(s)) return p;
  const add = new Map<string, Clip[]>();
  for (const pl of placed) add.set(pl.track.id, [...(add.get(pl.track.id) ?? []), freshCopy(pl.clip, s + pl.offset)]);
  const tracks = new Map<string, Track>();
  for (const t of p.tracks) {
    const extra = add.get(t.id);
    if (extra) tracks.set(t.id, { ...t, clips: sorted([...t.clips, ...extra]) });
  }
  return withTracks(p, tracks);
}

// --- paste attributes ----------------------------------------------------------

// What "Paste attributes" copies from one clip onto others: the look and
// the sound, not the timing. Speed, speed curve, direction and freeze
// change a clip's length, so they only travel with a pasted clip.
export type AttributeSet = "effects" | "transform" | "audio";
export const ALL_ATTRIBUTES: readonly AttributeSet[] = ["effects", "transform", "audio"];

// attributesOf is the patch Paste attributes applies from src: effects (new
// ids, keyframes kept), green screen and filters; position, scale, opacity,
// rotation and their keyframes; volume, mute, fades, stream levels and
// volume keyframes. Video-only parts are left out for a target on an audio
// track.
export function attributesOf(src: Clip, onAudio: boolean, sets: readonly AttributeSet[] = ALL_ATTRIBUTES): ClipPatch {
  const patch: ClipPatch = {};
  const anim: NonNullable<Clip["anim"]> = {};
  if (sets.includes("effects") && !onAudio) {
    patch.effects = (src.effects ?? []).map((e) => ({ ...structuredClone(e), id: newId("fx") }));
    patch.filters = { ...src.filters };
    patch.chroma = src.chroma ? { ...src.chroma } : undefined;
  }
  if (sets.includes("transform") && !onAudio) {
    patch.transform = { ...src.transform };
    for (const k of ["x", "y", "scale", "opacity", "rotation"] as const) if (src.anim?.[k]) anim[k] = structuredClone(src.anim[k]);
  }
  if (sets.includes("audio")) {
    patch.volume = src.volume;
    patch.muted = src.muted;
    patch.fadeIn = src.fadeIn;
    patch.fadeOut = src.fadeOut;
    patch.levels = src.levels ? src.levels.map((l) => ({ ...l })) : undefined;
    if (src.anim?.volume) anim.volume = structuredClone(src.anim.volume);
  }
  if (sets.includes("transform") || sets.includes("audio")) patch.anim = anim;
  return patch;
}

// pasteAttributes gives every target clip src's attributes (attributesOf).
// The parts of a target's keyframes the set doesn't cover are kept.
export function pasteAttributes(p: Project, src: Clip, ids: readonly string[], sets: readonly AttributeSet[] = ALL_ATTRIBUTES): Project {
  return updateClips(p, ids, (c) => {
    const hit = findClip(p, c.id);
    const onAudio = hit?.track.kind === "audio";
    const patch = attributesOf(src, onAudio, sets);
    if (patch.anim) {
      // Keep the target's keyframes on targets this paste doesn't replace.
      const covered = new Set<string>([
        ...(sets.includes("transform") && !onAudio ? ["x", "y", "scale", "opacity", "rotation"] : []),
        ...(sets.includes("audio") ? ["volume"] : []),
      ]);
      const kept = Object.fromEntries(Object.entries(c.anim ?? {}).filter(([k]) => !covered.has(k)));
      const merged = { ...kept, ...patch.anim };
      patch.anim = Object.keys(merged).length ? merged : undefined;
    }
    return patch;
  });
}
