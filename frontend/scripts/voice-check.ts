// voice-check.ts checks the mic voiceover pieces that run without a mic
// (Irori #129): where a take lands (placeVoiceTake), which WebView mic
// matches the Replay settings mic (matchMic) and the 16-bit conversion.
// Run: bun scripts/voice-check.ts
import { clipEnd, toExportRequest, VOICE_TRACK, type Media, type Project } from "../src/trim/project";
import { placeVoiceTake } from "../src/trim/project";
import { matchMic, toPcm16 } from "../src/trim/voice/pcm";

let failed = 0;
function check(name: string, ok: boolean, got?: unknown) {
  if (!ok) {
    failed++;
    console.error(`FAIL ${name}${got === undefined ? "" : `: ${JSON.stringify(got)}`}`);
  }
}

const video: Media = { id: "v", path: "C:\\v\\v.mp4", url: "", kind: "video", duration: 30, width: 1920, height: 1080, fps: 30, hasAudio: true, tracks: [] };
const take = (id: string, duration: number): Media => ({ id, path: `C:\\voice\\${id}.wav`, url: "", kind: "audio", duration, width: 0, height: 0, fps: 0, hasAudio: true, tracks: [] });

const base: Project = {
  version: 3,
  id: "proj",
  name: "p",
  canvas: { width: 1920, height: 1080, fps: 30 },
  media: [video],
  tracks: [
    { id: "vt", kind: "video", muted: false, hidden: false, locked: false, clips: [{ id: "c", media: "v", start: 0, in: 0, out: 30, speed: 1, volume: 1, muted: false, fadeIn: 0, fadeOut: 0, transform: { x: 0.5, y: 0.5, scale: 1, opacity: 1 }, filters: { brightness: 0, contrast: 1, saturation: 1, look: "none", blur: 0 } }] },
    { id: "at", kind: "audio", muted: false, hidden: false, locked: false, clips: [] },
  ],
  texts: [],
  crop: null,
};

// First take: a new Voice track (the empty "Audio 1" is left alone), exactly at start.
const r1 = placeVoiceTake(base, take("t1", 4), 5.25)!;
check("placed", !!r1);
const voice = r1.project.tracks.filter((t) => t.name === VOICE_TRACK);
check("one Voice track", voice.length === 1, r1.project.tracks.map((t) => t.name ?? t.kind));
check("Voice track is audio, last", voice[0]?.kind === "audio" && r1.project.tracks.at(-1)?.id === voice[0]?.id);
check("the plain audio track stays empty", r1.project.tracks.find((t) => t.id === "at")!.clips.length === 0);
const c1 = voice[0].clips[0];
check("exact start", c1.id === r1.clipId && c1.start === 5.25 && c1.in === 0 && c1.out === 4, c1);
check("media added", r1.project.media.some((m) => m.id === "t1"));
check("input untouched", base.tracks.length === 2 && base.media.length === 1);

// Second take after the first: same Voice track.
const r2 = placeVoiceTake(r1.project, take("t2", 2), 10)!;
check("reuses the free Voice track", r2.project.tracks.filter((t) => t.name === VOICE_TRACK).length === 1);
// Overlapping take: a second Voice track, still exactly at its start (not moved).
const r3 = placeVoiceTake(r2.project, take("t3", 3), 6)!;
const v3 = r3.project.tracks.filter((t) => t.name === VOICE_TRACK);
check("overlap gets another Voice track", v3.length === 2);
check("overlap not moved", v3[1].clips[0].start === 6, v3[1].clips[0]);
// Locked Voice track: not used.
const locked: Project = { ...r1.project, tracks: r1.project.tracks.map((t) => (t.name === VOICE_TRACK ? { ...t, locked: true } : t)) };
const r4 = placeVoiceTake(locked, take("t4", 1), 20)!;
check("locked Voice track skipped", r4.project.tracks.filter((t) => t.name === VOICE_TRACK).length === 2);
// Latency pushed the start below 0: the head is trimmed, the rest stays aligned.
const r5 = placeVoiceTake(base, take("t5", 3), -0.04)!;
const c5 = r5.project.tracks.find((t) => t.name === VOICE_TRACK)!.clips[0];
check("negative start trims the head", c5.start === 0 && Math.abs(c5.in - 0.04) < 1e-9 && c5.out === 3 && Math.abs(clipEnd(c5) - 2.96) < 1e-9, c5);
// Not audio, or too short: nothing.
check("video media refused", placeVoiceTake(base, video, 0) === null);
check("tiny take refused", placeVoiceTake(base, take("t6", 0.05), 0) === null);
// The export request carries the take on its audio track.
const req = toExportRequest(r1.project, { preset: "source" } as never);
const at = req.project.tracks.find((t) => t.clips.some((c) => c.media === "t1"));
check("export has the take", !!at && at.kind === "audio" && at.clips[0].start === 5250, at?.clips[0]);

// matchMic: dshow names vs WebView labels.
const mics = [
  { id: "default", label: "Default - Microphone (Shure MV7+)" },
  { id: "a", label: "Microphone (Shure MV7+)" },
  { id: "b", label: "Headset Microphone (Arctis Nova Pro Wireless)" },
  { id: "c", label: "Line In (Realtek(R) Audio)" },
];
check("exact", matchMic(mics, "Microphone (Shure MV7+)")?.id === "a");
check("case-insensitive", matchMic(mics, "line in (realtek(r) audio)")?.id === "c");
check("dshow 31-char cut", matchMic(mics, "Headset Microphone (Arctis Nova")?.id === "b");
check("never the 'default' alias", matchMic([{ id: "default", label: "Default - X Mic" }], "X Mic") === undefined);
check("none", matchMic(mics, "") === undefined && matchMic(mics, "Webcam") === undefined);

// toPcm16: clipped, little-endian, symmetric ends.
const pcm = toPcm16(new Float32Array([0, 1, -1, 2, -2, 0.5]));
const dv = new DataView(pcm.buffer);
const s = [0, 1, 2, 3, 4, 5].map((i) => dv.getInt16(i * 2, true));
check("pcm16", JSON.stringify(s) === JSON.stringify([0, 32767, -32768, 32767, -32768, 16384]), s);

if (failed) {
  console.error(`voice: ${failed} check(s) failed`);
  process.exit(1);
}
console.log("voice: all checks passed");
