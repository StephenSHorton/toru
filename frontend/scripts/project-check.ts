// project-check exercises the v2 editor's project ops (src/trim/project.ts)
// on edge cases: nearest-free placement without overlaps, moves between
// tracks, trims against neighbours and source ends, splits across tracks,
// ripple delete and close gap, speed-adjusted lengths, fromEdl and the
// export request.
//
//   bun scripts/project-check.ts
import { readFileSync } from "node:fs";
import path from "node:path";
import { colorMatrix, LOOK_MATRICES, normFilters } from "../src/trim/fx/lut";
import { historyApply, historyInit, historyUndo, type Edl } from "../src/trim/edl";
import { valueAt } from "../src/trim/fx/keyframes";
import { migrateClipEffects, migrateProject } from "../src/trim/migrate";
import {
  addMedia,
  addTrack,
  appendClip,
  clipEnd,
  clipLength,
  clipsAt,
  closeGap,
  DEFAULT_FILTERS,
  findClip,
  freezeFrame,
  fromEdl,
  insertClip,
  MIN_CLIP,
  moveClip,
  nearestFree,
  newProject,
  OVERLAY_SCALE,
  projectDuration,
  removeClip,
  removeTrack,
  rippleDelete,
  setTrackFlags,
  splitAt,
  timelineTimeAt,
  toExportRequest,
  trackById,
  trimClip,
  updateClip,
  upsertProjectText,
  videoTracks,
  audioTracks,
  type EffectInstance,
  type Filters,
  type Media,
  type Project,
} from "../src/trim/project";

let failed = 0;
let passed = 0;
function check(name: string, ok: boolean, detail?: unknown) {
  if (ok) passed++;
  else {
    failed++;
    console.error(`FAIL ${name}`, detail === undefined ? "" : JSON.stringify(detail));
  }
}
const near = (a: number, b: number, eps = 1e-6) => Math.abs(a - b) < eps;
const spans = (p: Project, trackId: string) =>
  p.tracks.find((t) => t.id === trackId)!.clips.map((c) => [c.media, +c.start.toFixed(3), +clipEnd(c).toFixed(3)]);
function noOverlaps(p: Project): boolean {
  return p.tracks.every((t) => t.clips.every((c, i) => i === 0 || t.clips[i - 1].start <= c.start && clipEnd(t.clips[i - 1]) <= c.start + 1e-6));
}

const media = (id: string, kind: Media["kind"], duration: number, extra: Partial<Media> = {}): Media => ({
  id,
  path: `C:/m/${id}`,
  url: `/__media/${id}`,
  kind,
  duration,
  width: kind === "audio" ? 0 : 1920,
  height: kind === "audio" ? 0 : 1080,
  fps: kind === "video" ? 60 : 0,
  hasAudio: kind !== "image",
  tracks: [],
  ...extra,
});
const G = media("game", "video", 60, { width: 2560, height: 1440, fps: 144 });
const W = media("cam", "video", 20);
const L = media("logo", "image", 0, { width: 512, height: 512 });
const M = media("music", "audio", 90);
const S = media("silent", "video", 10, { hasAudio: false });

// newProject / addMedia / canvas
let p = newProject(G);
const V0 = videoTracks(p)[0].id;
const A0 = audioTracks(p)[0].id;
check("new: one video + one audio track", videoTracks(p).length === 1 && audioTracks(p).length === 1);
check("new: first clip on video track", JSON.stringify(spans(p, V0)) === JSON.stringify([["game", 0, 60]]));
check("new: canvas from first video", p.canvas.width === 2560 && p.canvas.height === 1440 && p.canvas.fps === 144);
check("new: bottom track clip fits canvas", videoTracks(p)[0].clips[0].transform.scale === 1);
const pa = newProject(M);
check("new from audio: clip on audio track, default canvas", audioTracks(pa)[0].clips.length === 1 && videoTracks(pa)[0].clips.length === 0 && pa.canvas.width === 1920);
check("canvas follows first visual media after audio", addMedia(pa, W).canvas.fps === 60);
p = addMedia(addMedia(addMedia(addMedia(p, W), L), M), S);
check("addMedia keeps canvas once set", p.canvas.width === 2560);
check("addMedia same id is no-op", addMedia(p, W) === p);

// tracks
p = addTrack(p, "video");
p = addTrack(p, "video");
const [, V1, V2] = videoTracks(p).map((t) => t.id);
check("addTrack video goes on top of video tracks", p.tracks.map((t) => t.kind).join() === "video,video,video,audio");
check("addTrack audio goes last", addTrack(p, "audio").tracks.at(-1)!.kind === "audio");
check("can't remove the last video track", removeTrack(newProject(G), videoTracks(newProject(G))[0].id).tracks.length === 2);

// insert: kinds, overlay default size, nearest free spot
check("video track refuses audio", insertClip(p, V1, "music", 0) === p);
check("audio track refuses silent video", insertClip(p, A0, "silent", 0) === p);
check("audio track takes a video's audio", audioTracks(insertClip(p, A0, "cam", 0))[0].clips.length === 1);
p = insertClip(p, V1, "cam", 10); // cam 10..30
check("overlay clip defaults to 40%", findClip(p, videoTracks(p)[1].clips[0].id)!.clip.transform.scale === OVERLAY_SCALE);
check("image default length 5s", clipLength(insertClip(p, V2, "logo", 0).tracks[2].clips[0]) === 5);
const q1 = insertClip(p, V1, "cam", 15); // occupied 10..30 → nearest free: 30 (dist 15) vs -10 invalid → 30
check("drop on occupied spot lands at nearest free (after)", JSON.stringify(spans(q1, V1)) === JSON.stringify([["cam", 10, 30], ["cam", 30, 50]]), spans(q1, V1));
const q2 = insertClip(p, V1, "logo", 12); // 5s, occupied 10..30 → before: 5..10 (dist 7) vs after 30 (dist 18)
check("drop lands before when that's nearer", JSON.stringify(spans(q2, V1)) === JSON.stringify([["logo", 5, 10], ["cam", 10, 30]]), spans(q2, V1));
const q3 = insertClip(insertClip(p, V1, "logo", 0), V1, "logo", 7); // gap 5..10 fits exactly
check("fits an exact gap", JSON.stringify(spans(q3, V1)) === JSON.stringify([["logo", 0, 5], ["logo", 5, 10], ["cam", 10, 30]]), spans(q3, V1));
check("gap too small is skipped", spans(insertClip(insertClip(p, V1, "logo", 2), V1, "logo", 6), V1).length === 3 && noOverlaps(insertClip(insertClip(p, V1, "logo", 2), V1, "logo", 6)));
check("negative drop clamps to 0", spans(insertClip(p, V2, "logo", -4), V2)[0][1] === 0);
check("nearestFree empty track", nearestFree([], 3, 7) === 7);

// appendClip
const ap = appendClip(appendClip(p, "cam"), "music");
check("appendClip video → end of bottom track", JSON.stringify(spans(ap, V0)) === JSON.stringify([["game", 0, 60], ["cam", 60, 80]]));
check("appendClip audio → first audio track", JSON.stringify(spans(ap, A0)) === JSON.stringify([["music", 0, 90]]));

// moveClip
const cam = videoTracks(p)[1].clips[0].id;
let m = moveClip(p, cam, V1, 40);
check("move in time", JSON.stringify(spans(m, V1)) === JSON.stringify([["cam", 40, 60]]));
m = moveClip(p, cam, V2, 3);
check("move to another track", spans(m, V1).length === 0 && JSON.stringify(spans(m, V2)) === JSON.stringify([["cam", 3, 23]]));
check("move video clip to audio track is refused", moveClip(p, cam, A0, 0) === p);
const occupied = insertClip(p, V2, "logo", 12); // logo 12..17 on V2
m = moveClip(occupied, cam, V2, 10); // cam 20s onto V2 at 10: overlaps logo → nearest: 17 (dist 7) vs before needs 20s: -8 invalid → 17
check("move onto occupied spot → nearest free", JSON.stringify(spans(m, V2)) === JSON.stringify([["logo", 12, 17], ["cam", 17, 37]]), spans(m, V2));
check("move to same spot is no-op", moveClip(p, cam, V1, 10) === p);
const lockedV2 = setTrackFlags(p, V2, { locked: true });
check("locked target refuses", moveClip(lockedV2, cam, V2, 0) === lockedV2);
const locked = setTrackFlags(p, V1, { locked: true });
check("locked source refuses move", moveClip(locked, cam, V1, 40) === locked);
check("locked track refuses insert", insertClip(locked, V1, "logo", 50) === locked);
check("setTrackFlags same is no-op", setTrackFlags(p, V1, { muted: false }) === p);

// trimClip (timeline time)
let tr = insertClip(p, V1, "cam", 40); // cam 10..30, cam 40..60 (in 0..20)
const second = videoTracks(tr)[1].clips[1].id;
let r = trimClip(tr, second, "start", 45);
check("trim start moves start + in", near(findClip(r, second)!.clip.start, 45) && near(findClip(r, second)!.clip.in, 5) && near(clipEnd(findClip(r, second)!.clip), 60));
r = trimClip(tr, second, "start", 20);
check("trim start can't pass source 0", near(findClip(r, second)!.clip.start, 40));
r = trimClip(trimClip(tr, second, "start", 45), second, "start", 20);
check("trim start back stops at previous clip / source 0", near(findClip(r, second)!.clip.start, 40) && near(findClip(r, second)!.clip.in, 0));
r = trimClip(tr, cam, "end", 35);
check("trim end can't pass the source end", near(clipEnd(findClip(r, cam)!.clip), 30));
r = trimClip(trimClip(tr, cam, "end", 20), cam, "end", 99);
check("trim end stops at source end even with room", near(clipEnd(findClip(r, cam)!.clip), 30));
const withImg = insertClip(tr, V1, "logo", 31); // logo 31..36
const logoId = videoTracks(withImg)[1].clips.find((c) => c.media === "logo")!.id;
r = trimClip(withImg, logoId, "end", 50);
check("image end stretches up to next clip", near(clipEnd(findClip(r, logoId)!.clip), 40), clipEnd(findClip(r, logoId)!.clip));
r = trimClip(withImg, logoId, "start", 0);
check("image start can't go before its in point 0", near(findClip(r, logoId)!.clip.start, 31) && near(findClip(r, logoId)!.clip.in, 0));
const img2 = trimClip(trimClip(withImg, logoId, "start", 33), logoId, "start", 0);
check("image start back stops at in 0", near(findClip(img2, logoId)!.clip.start, 31), findClip(img2, logoId)!.clip.start);
r = trimClip(tr, cam, "end", 10);
check("trim keeps MIN_CLIP", near(clipLength(findClip(r, cam)!.clip), MIN_CLIP));
check("trim no change is no-op", trimClip(tr, cam, "end", 30) === tr);

// speed-adjusted lengths
let sp = updateClip(tr, cam, { speed: 2 });
check("2x halves the length", near(clipLength(findClip(sp, cam)!.clip), 10) && near(clipEnd(findClip(sp, cam)!.clip), 20));
check("2x keeps start", near(findClip(sp, cam)!.clip.start, 10));
sp = updateClip(tr, cam, { speed: 0.5 }); // wants 40s from 10, next clip at 40 → room 30 → speed 20/30
check("slow-down that would overlap is raised to fit", near(clipEnd(findClip(sp, cam)!.clip), 40) && near(findClip(sp, cam)!.clip.speed, 20 / 30), findClip(sp, cam)!.clip.speed);
sp = updateClip(tr, second, { speed: 0.25 }); // last clip, no limit
check("0.25x on last clip = 4x length", near(clipLength(findClip(sp, second)!.clip), 80));
check("speed clamps to 4", findClip(updateClip(tr, cam, { speed: 9 }), cam)!.clip.speed === 4);
const slow = updateClip(tr, second, { speed: 0.5 });
const at50 = clipsAt(slow, 50).find((a) => a.clip.id === second)!;
check("clipsAt source time uses speed", near(at50.srcT, 5), at50.srcT);

// timelineTimeAt is clipsAt's srcT the other way (the preview's pause
// moves the clock to the frame a video stopped on).
for (const [label, proj, id] of [["1x", tr, cam], ["0.5x", slow, second], ["2x", updateClip(tr, cam, { speed: 2 }), cam]] as const) {
  const c = findClip(proj, id)!.clip;
  const mid = c.start + clipLength(c) * 0.37;
  const a = clipsAt(proj, mid).find((x) => x.clip.id === id)!;
  check(`timelineTimeAt round-trips (${label})`, near(timelineTimeAt(c, a.srcT), mid), [timelineTimeAt(c, a.srcT), mid]);
  check(`timelineTimeAt stays in the clip (${label})`, timelineTimeAt(c, c.in - 5) === c.start && timelineTimeAt(c, c.out + 5) < clipEnd(c));
}
sp = updateClip(tr, cam, { fadeIn: 15, fadeOut: 15 });
check("fades fit inside the clip", near(findClip(sp, cam)!.clip.fadeIn, 15) && near(findClip(sp, cam)!.clip.fadeOut, 5));
check("volume clamps", findClip(updateClip(tr, cam, { volume: 5 }), cam)!.clip.volume === 2);
check("updateClip no change is no-op", updateClip(tr, cam, { volume: 1 }) === tr);

// splitAt across tracks
let sa = insertClip(tr, A0, "music", 0); // music 0..90 on A0
sa = splitAt(sa, 15);
check("split hits every track under t", spans(sa, V0).length === 2 && spans(sa, V1).length === 3 && spans(sa, A0).length === 2);
check("split pieces line up", JSON.stringify(spans(sa, V1)) === JSON.stringify([["cam", 10, 15], ["cam", 15, 30], ["cam", 40, 60]]), spans(sa, V1));
const right = videoTracks(sa)[1].clips[1];
check("right piece in point", near(right.in, 5) && near(right.out, 20));
check("split one track only", spans(splitAt(insertClip(tr, A0, "music", 0), 15, A0), V0).length === 1);
check("split in a gap is no-op on that track", spans(splitAt(tr, 35, V1), V1).length === 2);
check("split near an edge is no-op", splitAt(tr, 10 + MIN_CLIP / 2, V1) === tr);
const fastSplit = splitAt(updateClip(tr, second, { speed: 2 }), 45, V1); // second: 40..50 at 2x, in 0..20
const fs = videoTracks(fastSplit)[1].clips;
check("split respects speed", near(fs[1].out, 10) && near(fs[2].in, 10) && near(fs[2].start, 45), fs.map((c) => [c.start, c.in, c.out]));
const faded = splitAt(updateClip(tr, cam, { fadeIn: 2, fadeOut: 3 }), 20, V1);
check("split keeps fade in left, fade out right", videoTracks(faded)[1].clips[0].fadeIn === 2 && videoTracks(faded)[1].clips[0].fadeOut === 0 && videoTracks(faded)[1].clips[1].fadeOut === 3);
const lockedV1 = setTrackFlags(tr, V1, { locked: true });
check("split skips a locked track", splitAt(lockedV1, 15, V1) === lockedV1 && spans(splitAt(lockedV1, 15), V1).length === 2 && spans(splitAt(lockedV1, 15), V0).length === 2);

// remove / ripple / close gap
let rd = removeClip(tr, cam);
check("delete leaves a gap", JSON.stringify(spans(rd, V1)) === JSON.stringify([["cam", 40, 60]]));
rd = rippleDelete(tr, cam);
check("ripple delete pulls later clips left by its length", JSON.stringify(spans(rd, V1)) === JSON.stringify([["cam", 20, 40]]), spans(rd, V1));
check("ripple delete leaves other tracks alone", JSON.stringify(spans(rd, V0)) === JSON.stringify(spans(tr, V0)));
let cg = closeGap(tr, V1, 35); // gap 30..40
check("close gap pulls later clips to the gap start", JSON.stringify(spans(cg, V1)) === JSON.stringify([["cam", 10, 30], ["cam", 30, 50]]), spans(cg, V1));
cg = closeGap(tr, V1, 5); // leading gap 0..10
check("close the leading gap", JSON.stringify(spans(cg, V1)) === JSON.stringify([["cam", 0, 20], ["cam", 30, 50]]), spans(cg, V1));
check("close gap on a clip is no-op", closeGap(tr, V1, 20) === tr);
check("close gap after the last clip is no-op", closeGap(tr, V1, 70) === tr);
check("no overlaps after all that", [sa, rd, cg, sp, m, r].every(noOverlaps));

// clipsAt / duration
const stack = insertClip(insertClip(p, V2, "logo", 12), A0, "music", 0);
const at12 = clipsAt(stack, 12).map((a) => a.media.id);
check("clipsAt bottom→top then audio", JSON.stringify(at12) === JSON.stringify(["game", "cam", "logo", "music"]), at12);
check("clipsAt skips hidden video tracks", !clipsAt(setTrackFlags(stack, V1, { hidden: true }), 12).some((a) => a.media.id === "cam"));
check("clipsAt end is exclusive", !clipsAt(stack, 30).some((a) => a.media.id === "cam"));
check("projectDuration is the latest end", projectDuration(stack) === 90);

// fromEdl
const edl: Edl = {
  sources: [
    { path: "C:/a.mp4", url: "/__file/a.mp4", duration: 120, width: 2560, height: 1440 },
    { path: "C:/b.mp4", url: "/__replayclip/b.mp4", duration: 30, width: 1920, height: 1080 },
  ],
  segs: [
    { id: "s1", source: 0, start: 0, end: 30 },
    { id: "s2", source: 1, start: 5, end: 15 },
    { id: "s3", source: 0, start: 60, end: 90 },
  ],
  texts: [{ id: "t1", text: "hi", x: 0.5, y: 0.2, size: "l", start: 5, end: 10 }],
};
const fe = fromEdl(edl, {
  crop: { x: 0, y: 0, w: 1280, h: 720 },
  levels: [{ source: 0, index: 2, volume: 0, muted: true }],
  infos: [{ fps: 144, hasAudio: true, tracks: [] }, null],
});
const fv = videoTracks(fe)[0];
check("fromEdl: media per source", fe.media.length === 2 && fe.media[0].fps === 144 && fe.media[1].fps === 0);
check(
  "fromEdl: segments back to back",
  JSON.stringify(fv.clips.map((c) => [c.start, c.in, c.out])) === JSON.stringify([[0, 0, 30], [30, 5, 15], [40, 60, 90]]),
  fv.clips.map((c) => [c.start, c.in, c.out]),
);
check("fromEdl: levels go on that source's clips", fv.clips[0].levels?.[0].index === 2 && fv.clips[0].levels?.[0].muted === true && !fv.clips[1].levels && !!fv.clips[2].levels);
check("fromEdl: texts, crop, canvas", fe.texts.length === 1 && fe.crop?.w === 1280 && fe.canvas.width === 2560 && fe.canvas.fps === 144);
check("fromEdl: has an audio track", audioTracks(fe).length === 1);
check("fromEdl: duration", projectDuration(fe) === 70);

// toExportRequest
let ex = upsertProjectText(upsertProjectText(stack, { id: "a", text: "  ", x: 0.5, y: 0.5, size: "m", start: 1, end: 2 }), { id: "b", text: "GG", x: 0.5, y: 0.5, size: "m", start: 80, end: 200 });
ex = updateClip(ex, cam, { fadeIn: 1.25 });
const req = toExportRequest(ex, { preset: "custom", height: 720, kbps: 6000, outPath: "C:/out.mp4" });
const rc = req.project.tracks[1].clips[0];
check("export: clip times in ms", rc.start === 10000 && rc.in === 0 && rc.out === 20000 && rc.fadeIn === 1250, rc);
check("export: media duration in ms", req.project.media.find((x) => x.id === "game")!.duration === 60000);
check("export: blank text dropped, long text clipped", req.project.texts.length === 1 && req.project.texts[0].end === 90000, req.project.texts);
check("export: custom keeps height/kbps", req.height === 720 && req.kbps === 6000 && req.outPath === "C:/out.mp4");
check("export: other presets drop height", toExportRequest(ex, { preset: "1080p", height: 720 }).height === undefined);
check("export: doesn't change the project", findClip(ex, cam)!.clip.start === 10);

// history over a Project
let h = historyInit(p);
h = historyApply(h, (x) => splitAt(x, 20));
for (const t of [11, 12, 13]) h = historyApply(h, (x) => trimClip(x, cam, "start", t), "trim:1");
check("history: keyed drag merges", h.past.length === 2 && near(findClip(h.present, cam)!.clip.start, 13));
h = historyUndo(h);
check("history: undo the whole drag", near(findClip(h.present, cam)!.clip.start, 10));

// --- v3: keyframes golden table (shared with internal/fx/keyframes_test.go)
{
  const golden = JSON.parse(readFileSync(path.resolve(import.meta.dir, "../../internal/fx/testdata/keyframes_golden.json"), "utf8"));
  let bad = 0;
  for (const c of golden.cases) {
    const got = valueAt(c.keys, c.t, c.base);
    const ok = typeof got === "number" ? Math.abs(got - c.want) < 1e-9 : JSON.stringify(got) === JSON.stringify(c.want);
    if (!ok) {
      bad++;
      console.log(`  keyframes ${c.name}: got ${JSON.stringify(got)}, want ${JSON.stringify(c.want)}`);
    }
  }
  check(`keyframes: ${golden.cases.length} golden cases match`, bad === 0 && golden.cases.length > 100);
}

// --- v3: migration draws v2's pixels. v2's colour is clamp(look·sat) then
// clamp(contrast/brightness); the migrated stack, run with each effect's
// meaning (every effect clamps its output), must give the same colour.
{
  const clamp01 = (v: number) => Math.max(0, Math.min(1, v));
  const mul = (m: number[], c: number[]) => [0, 1, 2].map((r) => m[r * 3] * c[0] + m[r * 3 + 1] * c[1] + m[r * 3 + 2] * c[2]);
  const satM = (s: number) => colorMatrix({ brightness: 0, contrast: 1, saturation: s, look: "none", blur: 0 });
  const cb = (c: number[], k: number, b: number) => c.map((v) => clamp01((v - 0.5) * k + 0.5 + b));
  const v2 = (rgb: number[], f: Filters) => {
    const n = normFilters(f);
    return cb(mul(colorMatrix(n), rgb).map(clamp01), n.contrast, n.brightness);
  };
  const num = (v: unknown, d: number) => (typeof v === "number" ? v : d);
  const run = (rgb: number[], fx: EffectInstance[]) => {
    let c = rgb;
    for (const e of fx) {
      const q = e.params;
      if (e.type === "adjust") c = cb(mul(satM(num(q.saturation, 1)), c).map(clamp01), num(q.contrast, 1), num(q.brightness, 0));
      else if (e.type === "look") {
        const L = LOOK_MATRICES[q.look as keyof typeof LOOK_MATRICES];
        const out = mul(L, mul(satM(num(q.saturation, 1)), c)).map(clamp01);
        const a = num(q.amount, 1);
        c = c.map((v, i) => v + (out[i] - v) * a);
      }
    }
    return c;
  };
  let seed = 7;
  const rnd = () => ((seed = (seed * 16807) % 2147483647) / 2147483647);
  const looks: Filters["look"][] = ["none", "bw", "sepia", "vintage", "cool", "warm"];
  let worst = 0;
  for (let i = 0; i < 400; i++) {
    const f: Filters = {
      brightness: i % 4 === 0 ? 0 : rnd() * 2 - 1,
      contrast: i % 3 === 0 ? 1 : rnd() * 2,
      saturation: i % 5 === 0 ? 1 : rnd() * 3,
      look: looks[i % looks.length],
      blur: 0,
    };
    const fx = migrateClipEffects(undefined, f);
    for (let j = 0; j < 20; j++) {
      const rgb = [rnd(), rnd(), rnd()];
      const a = v2(rgb, f);
      const b = run(rgb, fx);
      worst = Math.max(worst, ...a.map((v, k) => Math.abs(v - b[k])));
    }
  }
  check("migration: 8000 colours through 400 filter sets match v2", worst < 1e-9, worst);
  const types = (f: Partial<Filters>, chroma = false) =>
    migrateClipEffects(chroma ? { color: "#00ff00", similarity: 0.25, blend: 0.08 } : undefined, { ...DEFAULT_FILTERS, ...f }).map((e) => e.type).join(",");
  check("migration: defaults give no effects", types({}) === "");
  check("migration: order chroma, look, adjust, blur", types({ look: "sepia", contrast: 1.2, blur: 3 }, true) === "chromaKey,look,adjust,blur", types({ look: "sepia", contrast: 1.2, blur: 3 }, true));
  check("migration: no look is one adjust", types({ saturation: 2, brightness: 0.1 }) === "adjust");
  check("migration: saturation rides on the look", migrateClipEffects(undefined, { ...DEFAULT_FILTERS, look: "warm", saturation: 1.5 })[0].params.saturation === 1.5);
  const v2p: Project = { ...tr, version: 2 };
  const v3p = migrateProject(v2p);
  check("migration: project becomes v3", v3p.version === 3 && v3p.tracks.every((t) => t.clips.every((c) => Array.isArray(c.effects) && c.chroma === undefined)));
  check("migration: timing untouched", JSON.stringify(v3p.tracks.map((t) => t.clips.map((c) => [c.start, c.in, c.out, c.speed]))) === JSON.stringify(v2p.tracks.map((t) => t.clips.map((c) => [c.start, c.in, c.out, c.speed]))));
  check("migration: a v3 project is left alone", migrateProject(v3p) === v3p);
}

// --- v3: hold clips (freeze frames)
{
  const base = newProject(G); // game 0..60 on the first video track
  const vt = videoTracks(base)[0].id;
  const fz = freezeFrame(base, vt, 10, 2);
  const cs = trackById(fz, vt)!.clips;
  const hold = cs.find((c) => c.hold)!;
  check("freeze: split + hold + rest pushed right", cs.length === 3 && near(hold.start, 10) && near(hold.in, 10) && near(clipLength(hold), 2) && near(cs[2].start, 12) && near(cs[2].in, 10), spans(fz, vt));
  check("freeze: hold is silent at 1x", hold.muted && hold.speed === 1);
  check("freeze: clipsAt shows the held frame", near(clipsAt(fz, 11.5).find((a) => a.clip.id === hold.id)!.srcT, 10));
  const sp = splitAt(fz, 11, vt);
  const halves = trackById(sp, vt)!.clips.filter((c) => c.hold);
  check("freeze: split keeps the frame in both halves", halves.length === 2 && halves.every((c) => near(c.in, 10)) && near(clipLength(halves[0]) + clipLength(halves[1]), 2));
  const ts = trimClip(fz, hold.id, "start", 9.5);
  const th = findClip(ts, hold.id);
  check("freeze: start trim keeps the frame", !th || near(th.clip.in, 10));
  const te = trimClip(fz, hold.id, "end", 11);
  check("freeze: end trim shortens, frame kept", near(clipLength(findClip(te, hold.id)!.clip), 1) && near(findClip(te, hold.id)!.clip.in, 10));
  const us = updateClip(fz, hold.id, { speed: 3 });
  check("freeze: speed stays 1", findClip(us, hold.id)!.clip.speed === 1);
  const edge = freezeFrame(base, vt, 0.02, 1);
  check("freeze: near the start freezes at the start", near(trackById(edge, vt)!.clips.find((c) => c.hold)!.start, 0) && noOverlaps(edge), spans(edge, vt));
  check("freeze: no overlaps", noOverlaps(fz));
}

console.log(`${passed} passed, ${failed} failed`);
if (failed) process.exit(1);
