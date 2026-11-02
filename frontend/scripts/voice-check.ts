// voice-check exercises the voice preset project ops (src/trim/voice.ts):
// which clips can take a preset, applying one (the clip plays the render,
// remembers its original and levels), switching presets (always from the
// original), Off (everything back), undo-safety (inputs untouched) and that
// the clip's range, timing and volume survive.
//
//   bun scripts/voice-check.ts
import { appendClip, findClip, insertClip, newProject, type Media, type Project } from "../src/trim/project";
import { canVoice, mixStream, voicePreset, voiceSource, withVoice } from "../src/trim/voice";

let pass = 0;
let fail = 0;
function ok(name: string, cond: boolean, detail?: unknown) {
  if (cond) pass++;
  else {
    fail++;
    console.log(`FAIL ${name}${detail === undefined ? "" : `: ${JSON.stringify(detail)}`}`);
  }
}

const media = (id: string, kind: Media["kind"], extra: Partial<Media> = {}): Media => ({
  id,
  path: `C:/clips/${id}`,
  url: `/__media/${id}/x`,
  kind,
  duration: 20,
  width: kind === "audio" ? 0 : 1920,
  height: kind === "audio" ? 0 : 1080,
  fps: kind === "audio" ? 0 : 60,
  hasAudio: true,
  tracks: [{ index: 0, title: "", isMix: false }],
  ...extra,
});

const rec = media("rec", "video", {
  tracks: [
    { index: 0, title: "All audio", isMix: true },
    { index: 1, title: "Game", isMix: false },
    { index: 2, title: "Microphone", isMix: false },
  ],
});
const take = media("take", "audio");
const processed = media("take-voice", "audio", { path: "C:/cache/voice-1.flac" });
const processed2 = media("take-polish", "audio", { path: "C:/cache/polish-1.flac" });

let p: Project = newProject(rec);
const audioTrack = p.tracks.find((t) => t.kind === "audio")!;
p = { ...p, media: [...p.media, take] };
p = insertClip(p, audioTrack.id, take.id, 2);
const videoClip = p.tracks.find((t) => t.kind === "video")!.clips[0];
const takeClip = p.tracks.find((t) => t.kind === "audio")!.clips[0];
// Give the take some settings that must survive.
p = {
  ...p,
  tracks: p.tracks.map((t) =>
    t.id === audioTrack.id
      ? { ...t, clips: t.clips.map((c) => ({ ...c, in: 1.5, out: 9, volume: 1.4, fadeIn: 0.5, levels: [{ source: 0, index: 0, volume: 80, muted: false }] })) }
      : t,
  ),
};

ok("mixStream picks the mix", mixStream(rec) === 0);
ok("mixStream without a mix", mixStream(media("x", "audio", { tracks: [{ index: 3, title: "", isMix: false }] })) === 3);
ok("mixStream no tracks", mixStream(media("y", "audio", { tracks: [] })) === 0);

ok("video clip can't take a preset", !canVoice(p, videoClip.id));
ok("audio clip can", canVoice(p, takeClip.id));
ok("unknown clip", !canVoice(p, "nope"));
{
  const locked = { ...p, tracks: p.tracks.map((t) => (t.id === audioTrack.id ? { ...t, locked: true } : t)) };
  ok("locked track can't", !canVoice(locked, takeClip.id));
  ok("locked track: withVoice no-op", withVoice(locked, takeClip.id, "voice", processed) === locked);
}
{
  // Extract audio puts a recording's sound on an audio track: that can.
  const q = appendClip({ ...p, tracks: p.tracks.map((t) => (t.id === audioTrack.id ? { ...t, clips: [] } : t)) }, rec.id);
  const onAudio = q.tracks.find((t) => t.kind === "audio")!.clips[0];
  if (onAudio) ok("recording sound on an audio track can", canVoice(q, onAudio.id));
}

ok("preset off by default", voicePreset(takeClip) === "off");

// Apply.
const before = JSON.stringify(p);
const a = withVoice(p, takeClip.id, "voice", processed);
ok("input untouched", JSON.stringify(p) === before);
const ac = findClip(a, takeClip.id)!.clip;
ok("plays the render", ac.media === processed.id, ac.media);
ok("render added to media", a.media.some((m) => m.id === processed.id));
ok("remembers the original", ac.voice?.media === take.id && ac.voice?.preset === "voice", ac.voice);
ok("remembers the levels", ac.voice?.levels?.[0]?.volume === 80, ac.voice);
ok("levels baked in", ac.levels === undefined);
ok("range/timing/volume kept", ac.in === 1.5 && ac.out === 9 && ac.start === takeClip.start && ac.volume === 1.4 && ac.fadeIn === 0.5, ac);
ok("voicePreset", voicePreset(ac) === "voice");
ok("voiceSource is the original", voiceSource(a, ac).media?.id === take.id && voiceSource(a, ac).levels?.[0]?.volume === 80);
ok("can still change it", canVoice(a, takeClip.id));

// Another preset: from the original, not stacked on the first render.
const b = withVoice(a, takeClip.id, "polish", processed2);
const bc = findClip(b, takeClip.id)!.clip;
ok("switch plays the new render", bc.media === processed2.id);
ok("switch keeps the true original", bc.voice?.media === take.id && bc.voice?.preset === "polish", bc.voice);
ok("switch keeps the levels", bc.voice?.levels?.[0]?.volume === 80);

// Off.
const c = withVoice(b, takeClip.id, "off", null);
const cc = findClip(c, takeClip.id)!.clip;
ok("off plays the original", cc.media === take.id);
ok("off clears voice", cc.voice === undefined);
ok("off restores levels", cc.levels?.[0]?.volume === 80);
ok("off keeps the rest", cc.in === 1.5 && cc.out === 9 && cc.volume === 1.4);
ok("off on a clean clip is harmless", findClip(withVoice(p, takeClip.id, "off", null), takeClip.id)!.clip.media === take.id);

// A clip without levels stays without them.
{
  const q = { ...p, tracks: p.tracks.map((t) => (t.id === audioTrack.id ? { ...t, clips: t.clips.map((x) => ({ ...x, levels: undefined })) } : t)) };
  const on = withVoice(q, takeClip.id, "light", processed);
  const off = withVoice(on, takeClip.id, "off", null);
  const oc = findClip(off, takeClip.id)!.clip;
  ok("no levels: none stored", findClip(on, takeClip.id)!.clip.voice?.levels === undefined);
  ok("no levels: none after off", oc.levels === undefined && !("levels" in oc));
}

// Survives a save (JSON round trip).
{
  const saved = JSON.parse(JSON.stringify(b)) as Project;
  const sc = findClip(saved, takeClip.id)!.clip;
  ok("saved project keeps voice", sc.voice?.preset === "polish" && sc.voice.media === take.id);
}

console.log(`${pass} passed, ${fail} failed`);
if (fail) process.exit(1);
