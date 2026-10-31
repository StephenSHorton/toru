// multi-check.ts checks the timeline's multi-clip ops and clipboard
// (src/trim/multi.ts). Run: bun scripts/multi-check.ts
import {
  allClipIds,
  attributesOf,
  clipRangeOnTrack,
  clipsInRange,
  copyClips,
  duplicateClips,
  liveIds,
  moveClips,
  pasteAttributes,
  pasteClips,
  pastedIds,
  removeClips,
  rippleDeleteClips,
  setClipsMuted,
} from "../src/trim/multi";
import { clipEnd, findClip, type Clip, type Media, type Project, type Track } from "../src/trim/project";

let failed = 0;
function check(name: string, ok: boolean, got?: unknown) {
  if (!ok) {
    failed++;
    console.error(`FAIL ${name}${got === undefined ? "" : `: ${JSON.stringify(got)}`}`);
  }
}
const near = (a: number, b: number) => Math.abs(a - b) < 1e-6;

const media = (id: string, kind: Media["kind"]): Media => ({
  id,
  path: `C:\\v\\${id}.mp4`,
  url: "",
  kind,
  duration: 60,
  width: 1920,
  height: 1080,
  fps: 30,
  hasAudio: kind !== "image",
  tracks: [],
});

// clip: `len` seconds of media from 0, at start.
const clip = (id: string, mediaId: string, start: number, len: number, extra: Partial<Clip> = {}): Clip => ({
  id,
  media: mediaId,
  start,
  in: 0,
  out: len,
  speed: 1,
  volume: 1,
  muted: false,
  fadeIn: 0,
  fadeOut: 0,
  transform: { x: 0.5, y: 0.5, scale: 1, opacity: 1 },
  filters: { brightness: 0, contrast: 1, saturation: 1, look: "none", blur: 0 },
  ...extra,
});

const track = (id: string, kind: Track["kind"], clips: Clip[], locked = false): Track => ({ id, kind, muted: false, hidden: false, locked, clips });

// v1: a 0-2, b 3-5, c 8-10 · v2: d 1-3 · a1: e 0-4
const base = (): Project => ({
  version: 3,
  id: "p",
  name: "t",
  canvas: { width: 1920, height: 1080, fps: 30 },
  media: [media("vid", "video"), media("snd", "audio")],
  tracks: [
    track("v1", "video", [clip("a", "vid", 0, 2), clip("b", "vid", 3, 2), clip("c", "vid", 8, 2)]),
    track("v2", "video", [clip("d", "vid", 1, 2)]),
    track("a1", "audio", [clip("e", "snd", 0, 4)]),
  ],
  texts: [],
  crop: null,
});
const at = (p: Project, id: string) => findClip(p, id)?.clip.start;
const trackOf = (p: Project, id: string) => findClip(p, id)?.track.id;

// --- selection helpers
{
  const p = base();
  check("liveIds drops missing and repeats", JSON.stringify(liveIds(p, ["a", "zz", "a", "c"])) === '["a","c"]');
  check("allClipIds", allClipIds(p).length === 5);
  check("range on one track", JSON.stringify(clipRangeOnTrack(p, "c", "a")) === '["a","b","c"]');
  check("range across tracks is empty", clipRangeOnTrack(p, "a", "d").length === 0);
  const m = clipsInRange(p, ["v1", "v2"], 2.5, 1.5);
  check("marquee picks overlapping clips on its tracks", JSON.stringify(m) === '["a","d"]', m);
}

// --- delete / ripple
{
  const p = base();
  const q = removeClips(p, ["a", "c", "e"]);
  check("remove leaves gaps", allClipIds(q).join() === "b,d" && at(q, "b") === 3);
  check("remove of nothing is a no-op", removeClips(p, ["zz"]) === p);
  const r = rippleDeleteClips(p, ["a", "b"]);
  check("ripple pulls later clips by the removed lengths", near(at(r, "c")!, 4), at(r, "c"));
  check("ripple leaves other tracks", at(r, "d") === 1);
  const r2 = rippleDeleteClips(p, ["b"]);
  check("ripple only shifts clips after", at(r2, "a") === 0 && near(at(r2, "c")!, 6));
  const locked = { ...p, tracks: p.tracks.map((t) => (t.id === "v2" ? { ...t, locked: true } : t)) };
  const l = removeClips(locked, ["a", "d"]);
  check("delete skips locked tracks", !findClip(l, "a") && !!findClip(l, "d"));
}

// --- mute
{
  const p = base();
  const q = setClipsMuted(p, ["a", "e"], true);
  check("mute several", findClip(q, "a")!.clip.muted && findClip(q, "e")!.clip.muted && !findClip(q, "b")!.clip.muted);
  check("mute again is a no-op", setClipsMuted(q, ["a", "e"], true) === q);
}

// --- group move
{
  const p = base();
  const q = moveClips(p, ["a", "d"], 0.5);
  check("group moves together", near(at(q, "a")!, 0.5) && near(at(q, "d")!, 1.5));
  const r = moveClips(p, ["a", "d"], 10);
  check("group stops at the first clip in the way", near(at(r, "a")!, 1) && near(at(r, "d")!, 2), [at(r, "a"), at(r, "d")]);
  const s = moveClips(p, ["b", "d"], -5);
  check("group stops at 0 and at clips before", near(at(s, "b")!, 2) && near(at(s, "d")!, 0), [at(s, "b"), at(s, "d")]);
  check("blocked move is a no-op", moveClips(p, ["a"], 0) === p && moveClips(p, ["a"], -3) === p);
  const t = moveClips(p, ["b", "c"], 4);
  check("selected clips don't block each other", near(at(t, "b")!, 7) && near(at(t, "c")!, 12), [at(t, "b"), at(t, "c")]);
  const locked = { ...p, tracks: p.tracks.map((x) => (x.id === "v2" ? { ...x, locked: true } : x)) };
  check("move with a locked clip does nothing", moveClips(locked, ["a", "d"], 0.5) === locked);
}

// --- duplicate
{
  const p = base();
  const q = duplicateClips(p, ["a", "d"]);
  const added = pastedIds(p, q);
  check("duplicate adds a copy of each", added.length === 2, added);
  // group a 0-2 / d 1-3 ends at 3; v1 is busy 3-5 so the copy (a' at s, d' at s+1) first fits at 5.
  const starts = added.map((id) => [trackOf(q, id), at(q, id)]);
  check("duplicate lands after the group where all fit", JSON.stringify(starts) === '[["v1",5],["v2",6]]', starts);
  const fx = { ...base() };
  fx.tracks[0].clips[0].effects = [{ id: "fx1", type: "blur", enabled: true, params: {} }];
  const fq = duplicateClips(fx, ["a"]);
  const copy = findClip(fq, pastedIds(fx, fq)[0])!.clip;
  check("duplicate gives effects new ids", copy.effects?.[0].id !== "fx1" && copy.effects?.[0].type === "blur");
}

// --- copy / paste
{
  const p = base();
  const cb = copyClips(p, ["b", "d", "e"])!;
  const offs = Object.fromEntries(cb.items.map((i) => [i.clip.id, i.offset]));
  check("copy offsets", offs.b === 3 && offs.d === 1 && offs.e === 0, offs);
  const lanes = Object.fromEntries(cb.items.map((i) => [i.clip.id, i.lane]));
  check("copy lanes per kind", lanes.b === 0 && lanes.d === 1 && lanes.e === 0, lanes);
  const q = pasteClips(p, cb, 20);
  const added = pastedIds(p, q);
  check("paste adds every clip with new ids", added.length === 3 && !added.includes("b"));
  const placed = added.map((id) => [trackOf(q, id), at(q, id)]).sort();
  check("paste keeps spacing and tracks", JSON.stringify(placed) === '[["a1",20],["v1",23],["v2",21]]', placed);
  const r = pasteClips(p, copyClips(p, ["a"]), 0);
  const ra = pastedIds(p, r).map((id) => at(r, id));
  check("paste moves later to the first free spot", JSON.stringify(ra) === "[5]", ra);
  const s = pasteClips(p, copyClips(p, ["a"]), 0, "v2");
  const sa = pastedIds(p, s).map((id) => [trackOf(s, id), at(s, id)]);
  check("paste anchors on the selected track", JSON.stringify(sa) === '[["v2",3]]', sa);
  const two = pasteClips(p, copyClips(p, ["a", "d"]), 30, "v2");
  check("paste adds tracks it needs", two.tracks.filter((t) => t.kind === "video").length === 3);
  // Into another project: media comes along.
  const other: Project = { ...base(), media: [], tracks: [track("x", "video", []), track("y", "audio", [])] };
  const o = pasteClips(other, copyClips(p, ["a"]), 0);
  check("paste into another project adds the media", o.media.some((m) => m.id === "vid") && pastedIds(other, o).length === 1);
  const lockedAll: Project = { ...base(), tracks: base().tracks.map((t) => ({ ...t, locked: t.kind === "video" })) };
  check("paste onto a locked track is a no-op", pasteClips(lockedAll, copyClips(p, ["a"]), 0, "v1") === lockedAll);
  check("paste of nothing is a no-op", pasteClips(p, null, 0) === p && copyClips(p, ["zz"]) === null);
  const cbA = copyClips(p, ["a"])!;
  const before = JSON.stringify(cbA);
  pasteClips(p, cbA, 40);
  check("paste doesn't change the clipboard", JSON.stringify(cbA) === before);
}

// --- paste attributes
{
  const p = base();
  const src = clip("s", "vid", 0, 2, {
    volume: 0.4,
    fadeIn: 0.3,
    speed: 2,
    reversed: true,
    transform: { x: 0.2, y: 0.3, scale: 0.5, opacity: 0.8 },
    effects: [{ id: "fxs", type: "blur", enabled: true, params: { amount: 3 } }],
    anim: { opacity: [{ t: 0, v: 0, ease: "linear" }], volume: [{ t: 0, v: 1, ease: "linear" }] },
  });
  const tgt = { ...p };
  tgt.tracks[0].clips[1].anim = { scale: [{ t: 0, v: 1, ease: "linear" }], x: [{ t: 1, v: 0.1, ease: "linear" }] };
  const q = pasteAttributes(tgt, src, ["b", "e"]);
  const b = findClip(q, "b")!.clip;
  const e = findClip(q, "e")!.clip;
  check("attributes: look and sound copied", b.volume === 0.4 && b.fadeIn === 0.3 && b.transform.scale === 0.5 && b.effects?.[0].type === "blur");
  check("attributes: effect ids are new", b.effects?.[0].id !== "fxs");
  check("attributes: timing not copied", b.speed === 1 && !b.reversed && near(clipEnd(b), 5));
  check("attributes: transform keys replace, volume keys copied", !!b.anim?.opacity && !b.anim?.scale && !b.anim?.x && !!b.anim?.volume, b.anim);
  check("attributes: audio target gets sound only", e.volume === 0.4 && !e.effects?.length && e.transform.scale === 1 && !e.anim?.opacity && !!e.anim?.volume);
  const onlyAudio = pasteAttributes(tgt, src, ["b"], ["audio"]);
  const ob = findClip(onlyAudio, "b")!.clip;
  check("attributes: a set keeps the other parts", ob.volume === 0.4 && ob.transform.scale === 1 && !!ob.anim?.scale && !!ob.anim?.volume, ob.anim);
  check("attributesOf leaves timing out", !("speed" in attributesOf(src, false)) && !("reversed" in attributesOf(src, false)));
}

if (failed) {
  console.error(`${failed} check(s) failed`);
  process.exit(1);
}
console.log("multi-check: all passed");
