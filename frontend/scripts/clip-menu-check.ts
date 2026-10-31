// clip-menu-check.ts checks the timeline clip menu's project ops
// (src/trim/project.ts): Extract audio, Duplicate and the sound-only
// project behind "Save audio as file…". Run: bun scripts/clip-menu-check.ts
import { canExtractAudio, clipEnd, duplicateClip, extractAudio, findClip, soundProject, type Clip, type Media, type Project } from "../src/trim/project";
import { soundExportRequest } from "../src/trim/saveAudio";

let failed = 0;
function check(name: string, ok: boolean, got?: unknown) {
  if (!ok) {
    failed++;
    console.error(`FAIL ${name}${got === undefined ? "" : `: ${JSON.stringify(got)}`}`);
  }
}

const media = (id: string, kind: Media["kind"], hasAudio: boolean): Media => ({
  id,
  path: `C:\\v\\${id}.mp4`,
  url: "",
  kind,
  duration: 20,
  width: 1920,
  height: 1080,
  fps: 30,
  hasAudio,
  tracks: hasAudio ? [{ index: 0, title: "Game", isMix: false } as never, { index: 1, title: "Mic", isMix: false } as never] : [],
});

const clip = (id: string, mediaId: string, start: number, extra: Partial<Clip> = {}): Clip => ({
  id,
  media: mediaId,
  start,
  in: 2,
  out: 8,
  speed: 1,
  volume: 0.8,
  muted: false,
  fadeIn: 0.5,
  fadeOut: 1,
  transform: { x: 0.5, y: 0.5, scale: 1, opacity: 1 },
  filters: { brightness: 0, contrast: 1, saturation: 1, look: "none", blur: 0 },
  ...extra,
});

const base = (): Project => ({
  version: 3,
  id: "p",
  name: "t",
  canvas: { width: 1920, height: 1080, fps: 30 },
  media: [media("vid", "video", true), media("silent", "video", false), media("img", "image", false)],
  tracks: [
    {
      id: "v1",
      kind: "video",
      muted: false,
      hidden: false,
      locked: false,
      clips: [
        clip("a", "vid", 0, {
          speed: 2,
          reversed: true,
          levels: [{ index: 0, volume: 1, muted: false } as never, { index: 1, volume: 0.5, muted: false } as never],
          anim: { volume: [{ t: 0, v: 1, ease: "linear" }, { t: 2, v: 0, ease: "linear" }], opacity: [{ t: 0, v: 1, ease: "linear" }] },
          effects: [{ id: "fx1", type: "adjust", enabled: true, params: {} }],
        }),
        clip("b", "silent", 4),
        clip("frozen", "vid", 12, { hold: true, muted: true }),
      ],
    },
    { id: "a1", kind: "audio", muted: false, hidden: false, locked: false, clips: [clip("music", "vid", 1)] },
  ],
  texts: [],
  crop: null,
});

// --- Extract audio ----------------------------------------------------------
{
  const p = base();
  check("can extract a video clip with sound", canExtractAudio(p, "a"));
  check("not a clip without sound", !canExtractAudio(p, "b"));
  check("not a freeze frame", !canExtractAudio(p, "frozen"));
  check("not an audio-track clip", !canExtractAudio(p, "music"));
  check("no-op returns the same project", extractAudio(p, "b") === p && extractAudio(p, "music") === p);

  const q = extractAudio(p, "a");
  const v = findClip(q, "a")!.clip;
  check("video clip goes silent", v.muted === true, v.muted);
  check("video keeps its picture keys, loses volume keys", !!v.anim?.opacity && !v.anim?.volume, v.anim);
  check("video keeps its effects", v.effects?.length === 1);
  // a1 is busy at 1..4 (music, 6 s from 1), a runs 0..3 at 2x: a new track.
  const audioTracks = q.tracks.filter((t) => t.kind === "audio");
  check("busy audio track: a new one is added", audioTracks.length === 2, audioTracks.length);
  const added = audioTracks[1].clips[0];
  check("one clip on the new track", audioTracks[1].clips.length === 1);
  check("same media, place and range", added.media === "vid" && added.start === 0 && added.in === 2 && added.out === 8);
  check("same timing", added.speed === 2 && added.reversed === true && clipEnd(added) === clipEnd(v), [added.speed, added.reversed, clipEnd(added)]);
  check("same volume, fades, levels", added.volume === 0.8 && added.fadeIn === 0.5 && added.fadeOut === 1 && added.levels?.[1]?.volume === 0.5);
  check("volume keys moved over", added.anim?.volume?.length === 2 && !added.anim?.opacity, added.anim);
  check("audio clip is unmuted, no effects", added.muted === false && !added.effects);
  check("new id", added.id !== "a" && !!added.id);
  check("original untouched", findClip(p, "a")!.clip.muted === false && p.tracks.length === 2);
  check("can't extract twice", !canExtractAudio(q, "a"));

  // A free audio track is used instead of adding one.
  const free = base();
  free.tracks[1].clips = [clip("music", "vid", 10)];
  const r = extractAudio(free, "a");
  check("free audio track reused", r.tracks.length === 2 && r.tracks[1].clips.length === 2, r.tracks.map((t) => t.clips.length));
  // A locked audio track is skipped.
  const locked = base();
  locked.tracks[1].clips = [];
  locked.tracks[1].locked = true;
  const l = extractAudio(locked, "a");
  check("locked audio track skipped", l.tracks.length === 3 && l.tracks[1].clips.length === 0 && l.tracks[2].clips.length === 1);
  // A locked video track can't extract.
  const lv = base();
  lv.tracks[0].locked = true;
  check("locked video track: no-op", extractAudio(lv, "a") === lv);
}

// --- Duplicate --------------------------------------------------------------
{
  const p = base();
  const q = duplicateClip(p, "a");
  const v1 = q.tracks[0].clips;
  check("one more clip", v1.length === 4, v1.length);
  const copy = v1.find((c) => c.id !== "a" && c.media === "vid" && !c.hold)!;
  check("copy lands where it fits, after the clip", copy.start >= clipEnd(findClip(p, "a")!.clip) - 1e-6, copy.start);
  check("copy overlaps nothing", v1.every((c) => c === copy || clipEnd(c) <= copy.start + 1e-6 || c.start >= clipEnd(copy) - 1e-6), copy.start);
  check("copy has new effect ids", copy.effects?.[0].id !== "fx1" && copy.effects?.[0].type === "adjust");
  check("copy keeps settings", copy.speed === 2 && copy.reversed === true && copy.anim?.volume?.length === 2);
  check("deep copy", copy.anim !== findClip(q, "a")!.clip.anim);
  const lk = base();
  lk.tracks[0].locked = true;
  check("locked: no-op", duplicateClip(lk, "a") === lk);
}

// --- Save audio as file -----------------------------------------------------
{
  const p = base();
  const s = soundProject(p, "a")!;
  check("sound project: one media", s.media.length === 1 && s.media[0].id === "vid");
  check("sound project: empty video track + audio track", s.tracks.length === 2 && s.tracks[0].kind === "video" && s.tracks[0].clips.length === 0 && s.tracks[1].kind === "audio");
  const c = s.tracks[1].clips[0];
  check("sound clip at 0, unmuted, same timing", c.start === 0 && !c.muted && c.speed === 2 && c.reversed === true);
  check("no texts or crop", s.texts.length === 0 && s.crop === null);
  check("no sound: null", soundProject(p, "b") === null && soundProject(p, "frozen") === null);
  check("audio clip works too", soundProject(p, "music")?.tracks[1].clips[0].media === "vid");
  // A muted video clip still saves its sound (the clip menu offers it).
  const m = extractAudio(p, "a");
  check("muted video clip still has a sound project", soundProject(m, "a")?.tracks[1].clips[0].muted === false);

  const wav = soundExportRequest(p, "a", "C:\\out\\x.WAV");
  check("wav request", wav.options?.format === "wav" && wav.outPath === "C:\\out\\x.WAV", wav.options);
  const m4a = soundExportRequest(p, "a", "C:\\out\\x.m4a");
  check("m4a request", m4a.options?.format === "m4a" && m4a.options?.audioKbps === 192, m4a.options);
  check("request in ms", m4a.project.tracks[1].clips[0].in === 2000 && m4a.project.tracks[1].clips[0].out === 8000);
  let threw = false;
  try {
    soundExportRequest(p, "b", "C:\\out\\x.m4a");
  } catch {
    threw = true;
  }
  check("no sound throws", threw);
}

if (failed) {
  console.error(`${failed} check(s) failed`);
  process.exit(1);
}
console.log("clip-menu-check: all checks passed");
