// realbuild-check drives a real Toru build's editor and checks that the
// preview matches the export: it builds a timeline through `toru ctl`, grabs
// preview frames (editor.frame) at sample times, exports (editor.export) and
// compares the exported file's frames at the same times. Recipe and cleanup:
// docs/editor-realbuild-check.md.
//
//   bun scripts/realbuild-check.ts --exe <test toru.exe> --out <dir> [--phases time,fx,ui] [--keep]
//   bun scripts/realbuild-check.ts --dry --out <dir> [--url http://localhost:5191]
//
// Phases:
//   time  speed ramp (bullet) + frame blending, reversed clip, freeze frame (C5)
//   fx    a saved v3 project with effects, effect keyframes and anim keys,
//         opened with project.open (C3)
//   fx7   the same kind of stack through C7's commands: a .cube imported
//         (preset.importLut) and used as a lut effect, editor.addEffect /
//         setKeyframe, a filter saved, exported to .torufx, imported and
//         applied (editor.savePreset, preset.export/import, applyPreset)
//   text  styled texts (a preset, a blurred shadow, an opacity ramp) set with
//         editor.setText and exported: the editor's text images reach the
//         export through the bridge and land where the texts are (#122;
//         editor.frame leaves texts out, so this checks the export alone)
//   ui    screenshots of every inspector tab over CDP (C6); needs the build
//         started with --remote-debugging-port (TORU_CDP, default 9333)
//   v17   v0.17.0's editor: the library rail, the Ctrl+K palette and the
//         right-click menus (CDP); multi-select copy/paste and undo (ctl);
//         waveforms on the test source and on a real recording (TORU_REAL,
//         default the newest file in Videos/Toru Replays); a 16:9 edit
//         switched to a 9:16 frame with fill, preview vs export
//
//   trans a cross dissolve and a wipe on the source's cuts (editor.split,
//         editor.addTransition): length kept, preview vs export through
//         both, the dissolve's middle a mix
//
//   ui, v17 and trans need the build started with --remote-debugging-port.
//
// --dry runs without the app: makes the test source, builds and validates the
// fx project, calibrates the frame diff on the source's own encode and, with
// --url (a `bunx vite --port 5191` dev server), runs the ui phase in headless
// Edge against ?view=edit.
//
// Needs ffmpeg/ffprobe with libx264 on PATH (or FFMPEG/FFPROBE).
import { spawn, spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { migrateProject } from "../src/trim/migrate";
import { clipEnd, newProject, projectDuration, splitAt, type Clip, type Media, type Project } from "../src/trim/project";

// ---- args ----------------------------------------------------------------

const argv = process.argv.slice(2);
const flag = (n: string) => argv.includes(`--${n}`);
const opt = (n: string, d = "") => {
  const i = argv.indexOf(`--${n}`);
  return i >= 0 && i + 1 < argv.length ? argv[i + 1] : d;
};
const DRY = flag("dry");
const KEEP = flag("keep");
const EXE = opt("exe");
const OUT = path.resolve(opt("out", path.join(os.tmpdir(), `toru-realbuild-${Date.now()}`)));
const PHASES = opt("phases", "time,fx,fx7,text,ui,v17,trans").split(",").map((s) => s.trim());
const URL_ = opt("url");
const CDP = Number(process.env.TORU_CDP || 9333);
const FFMPEG = process.env.FFMPEG || "ffmpeg";
const FFPROBE = process.env.FFPROBE || "ffprobe";
if (!DRY && !EXE) throw new Error("--exe <test build's toru.exe> is required (or --dry)");
fs.mkdirSync(OUT, { recursive: true });

// Limits for a preview frame vs the exported frame (RGB 0..255 at 160x90):
// the export is lossy, so a few levels of noise is fine; a wrong effect,
// wrong time or missing layer is far over.
const LIMIT = { mean: 8, p99: 48 };
const SAMPLE_W = 160;
const SAMPLE_H = 90;

const log: string[] = [];
const say = (s: string) => {
  console.log(s);
  log.push(s);
};
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
let failures = 0;
const fail = (s: string) => {
  failures++;
  say(`FAIL ${s}`);
};

// ---- test files ------------------------------------------------------------

// The preview only serves Library files, so the source lives in a folder
// under Videos/Toru Replays (deleted at the end unless --keep).
const stamp = new Date().toISOString().replace(/[-:T]/g, "").slice(0, 14);
const MEDIA_DIR = DRY ? path.join(OUT, "media") : path.join(os.homedir(), "Videos", "Toru Replays", `toru-check-${stamp}`);
const PROJECTS_DIR = path.join(process.env.APPDATA ?? "", "toru", "projects");
const REVERSE_DIR = path.join(process.env.LOCALAPPDATA ?? "", "toru", "cache", "reverse");
const startedAt = Date.now();
// Projects saved before the run: cleanup never removes these.
const projectsBefore = new Set(fs.existsSync(PROJECTS_DIR) ? fs.readdirSync(PROJECTS_DIR) : []);

function run(cmd: string, args: string[], input?: string): string {
  const r = spawnSync(cmd, args, { encoding: "utf8", maxBuffer: 256 << 20, input });
  if (r.status !== 0) throw new Error(`${path.basename(cmd)} ${args.slice(0, 3).join(" ")}…: ${r.stderr || r.stdout}`);
  return r.stdout;
}

// 6 s at 1280x720 30 fps: the background changes colour every second (red,
// green, blue, yellow, magenta, cyan), a white box slides along the top
// (600 px/s, wrapping every 2 s), and a 440 Hz tone plays. So colour tells
// the source second, the box the source time to about a frame (2.5 px at
// SAMPLE_W per frame), and silence a muted stretch.
const COLORS = ["red", "green", "blue", "yellow", "magenta", "cyan"];
// Tagged like Toru's own recordings (BT.601 matrix, limited range). Untagged
// HD video is read as BT.709 by the preview (WebView2) and as BT.601 by
// ffmpeg, so an untagged source shows a colour shift (worst on magenta and
// green) that real recordings don't have.
const SOURCE_TAGS = ["-colorspace", "bt470bg", "-color_primaries", "bt709", "-color_trc", "iec61966-2-1", "-color_range", "tv"];
function makeSource(): string {
  fs.mkdirSync(MEDIA_DIR, { recursive: true });
  const out = path.join(MEDIA_DIR, "source.mp4");
  const bg = COLORS.map((c, i) => `color=c=${c}:s=1280x720:r=30:d=1[c${i}]`).join(";");
  const graph = `${bg};${COLORS.map((_, i) => `[c${i}]`).join("")}concat=n=${COLORS.length}:v=1:a=0[bg];color=c=white:s=80x40:r=30:d=6[box];[bg][box]overlay=x='mod(t*600,1200)':y=44:shortest=1,format=yuv420p[v]`;
  run(FFMPEG, ["-v", "error", "-y", "-filter_complex", graph, "-f", "lavfi", "-i", "sine=f=440:d=6:sample_rate=48000", "-map", "[v]", "-map", "0:a", "-c:v", "libx264", "-g", "15", "-crf", "16", ...SOURCE_TAGS, "-c:a", "aac", "-shortest", out]);
  return out;
}

// ---- frames ----------------------------------------------------------------

// rgb is one frame as RGB24 at SAMPLE_W x SAMPLE_H: from an image, or from a
// video at time t (output-side seek: frame-exact).
function rgb(file: string, t?: number): Buffer {
  const args = ["-v", "error", "-i", file];
  if (t !== undefined) args.push("-ss", t.toFixed(3));
  args.push("-frames:v", "1", "-vf", `scale=${SAMPLE_W}:${SAMPLE_H}:flags=area,format=rgb24`, "-f", "rawvideo", "-");
  const r = spawnSync(FFMPEG, args, { maxBuffer: 16 << 20 });
  const b = r.stdout as Buffer;
  if (b.length !== SAMPLE_W * SAMPLE_H * 3) throw new Error(`no frame from ${file}${t === undefined ? "" : ` at ${t}`}: ${r.stderr}`);
  return b;
}
function diff(a: Buffer, b: Buffer): { mean: number; p99: number } {
  const d = new Array<number>(a.length);
  let sum = 0;
  for (let i = 0; i < a.length; i++) sum += d[i] = Math.abs(a[i] - b[i]);
  d.sort((x, y) => x - y);
  return { mean: sum / a.length, p99: d[Math.floor(d.length * 0.99)] };
}
// colour name of the centre pixel, and the white box's x (of SAMPLE_W) on
// the row through it: the readable part of a mismatch.
function describe(b: Buffer): string {
  const at = (x: number, y: number) => [b[(y * SAMPLE_W + x) * 3], b[(y * SAMPLE_W + x) * 3 + 1], b[(y * SAMPLE_W + x) * 3 + 2]];
  const [r, g, bl] = at(SAMPLE_W / 2, Math.round(SAMPLE_H * 0.6));
  const hi = (v: number) => v > 160, lo = (v: number) => v < 90;
  const name =
    hi(r) && lo(g) && lo(bl) ? "red" : lo(r) && hi(g) && lo(bl) ? "green" : lo(r) && lo(g) && hi(bl) ? "blue" : hi(r) && hi(g) && lo(bl) ? "yellow" : hi(r) && lo(g) && hi(bl) ? "magenta" : lo(r) && hi(g) && hi(bl) ? "cyan" : `rgb(${r},${g},${bl})`;
  const row = Math.round((64 / 720) * SAMPLE_H);
  let sx = 0, n = 0;
  for (let x = 0; x < SAMPLE_W; x++) {
    const [pr, pg, pb] = at(x, row);
    if (pr > 220 && pg > 220 && pb > 220) (sx += x), n++;
  }
  return `${name} box@${n ? (sx / n).toFixed(0) : "-"}`;
}
// boxX is the white box's centre x (of SAMPLE_W), or -1 when it isn't
// there (zoomed or blurred away: then only the pixel diff counts).
function boxX(b: Buffer): number {
  const row = Math.round((64 / 720) * SAMPLE_H);
  let sx = 0, n = 0;
  for (let x = 0; x < SAMPLE_W; x++) {
    const i = (row * SAMPLE_W + x) * 3;
    if (b[i] > 220 && b[i + 1] > 220 && b[i + 2] > 220) (sx += x), n++;
  }
  return n >= 3 ? sx / n : -1;
}
// BOX_LIMIT is how far apart (of SAMPLE_W) the box may be: under 2 frames.
const BOX_LIMIT = 4;
const boxOff = (a: Buffer, b: Buffer) => {
  const xa = boxX(a), xb = boxX(b);
  return xa < 0 || xb < 0 ? 0 : Math.abs(xa - xb);
};

// ---- toru ctl --------------------------------------------------------------

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- ctl results are JSON
function ctl(name: string, input: unknown = {}): any {
  const r = spawnSync(EXE, ["ctl", name, "--json", JSON.stringify(input)], { encoding: "utf8", maxBuffer: 64 << 20 });
  if (r.status !== 0) throw new Error(`${name}: ${(r.stderr || r.stdout).trim()}`);
  return r.stdout.trim() ? JSON.parse(r.stdout) : null;
}
async function until<T>(what: string, f: () => T | undefined | false | Promise<T | undefined | false>, ms = 30000): Promise<T> {
  const t0 = Date.now();
  let last: unknown;
  for (;;) {
    try {
      const v = await f();
      if (v) return v;
    } catch (e) {
      last = e;
    }
    if (Date.now() - t0 > ms) throw new Error(`${what}: timed out${last ? ` (${last})` : ""}`);
    await sleep(500);
  }
}
function hasCommand(name: string): boolean {
  try {
    const list = ctl("list");
    return JSON.stringify(list).includes(`"${name}"`);
  } catch {
    return false;
  }
}

// samples are frame-centred times through each clip, clear of cuts.
function samples(clips: { start: number; end: number }[], fps: number, per = 5): number[] {
  const ts: number[] = [];
  for (const c of clips) {
    for (let i = 0; i < per; i++) {
      const t = c.start + ((i + 0.5) / per) * (c.end - c.start);
      ts.push((Math.floor(t * fps) + 0.5) / fps);
    }
  }
  return ts;
}

// compare grabs preview frames at ts, exports, and compares the export's
// frames at the same times.
async function compare(phase: string, ts: number[], duration: number, fps: number) {
  const dir = path.join(OUT, phase);
  fs.mkdirSync(dir, { recursive: true });
  const prev = new Map<number, Buffer>();
  for (const t of ts) {
    // ctl saves an inline image to a temp file and returns its path.
    const fr = ctl("editor.frame", { t, maxWidth: 640 });
    const f = path.join(dir, `preview_${t.toFixed(3)}${fr.image.path ? path.extname(fr.image.path) : ".png"}`);
    if (fr.image.path) fs.renameSync(fr.image.path, f);
    else fs.writeFileSync(f, Buffer.from(fr.image.base64, "base64"));
    prev.set(t, rgb(f));
  }
  const out = path.join(dir, `export-${Date.now()}.mp4`);
  const e0 = Date.now();
  ctl("editor.export", { preset: "original", path: out });
  say(`${phase}: exported in ${((Date.now() - e0) / 1000).toFixed(1)} s → ${out}`);
  const dur = Number(run(FFPROBE, ["-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", out]));
  say(`${phase}: export ${dur.toFixed(3)} s vs timeline ${duration.toFixed(3)} s`);
  if (Math.abs(dur - duration) > 0.1) fail(`${phase}: export length is off by ${(dur - duration).toFixed(3)} s`);
  say("      t   mean   p99  preview            export");
  for (const t of ts) {
    const a = prev.get(t)!;
    // The preview shows the frame whose time is at or before t; an output
    // seek keeps the first frame at or after its time, so seek to that
    // frame's start (ts are frame-centred).
    const b = rgb(out, t - 0.5 / fps - 0.001);
    fs.writeFileSync(path.join(dir, `export_${t.toFixed(3)}.rgb`), b);
    const d = diff(a, b);
    const dx = boxOff(a, b);
    const ok = d.mean <= LIMIT.mean && d.p99 <= LIMIT.p99 && dx <= BOX_LIMIT;
    say(`${t.toFixed(3).padStart(7)} ${d.mean.toFixed(1).padStart(6)} ${String(d.p99).padStart(5)}  ${describe(a).padEnd(18)} ${describe(b)}${ok ? "" : "  MISMATCH"}`);
    if (!ok) fail(`${phase}: frame at ${t.toFixed(3)} s differs (mean ${d.mean.toFixed(1)}, p99 ${d.p99}, box ${dx.toFixed(1)} px apart)`);
  }
  return out;
}
function meanVolume(file: string, from: number, d: number): number {
  const r = spawnSync(FFMPEG, ["-hide_banner", "-ss", from.toFixed(3), "-t", d.toFixed(3), "-i", file, "-map", "0:a", "-af", "volumedetect", "-f", "null", "-"], { encoding: "utf8" });
  const m = /mean_volume: (-?[\d.]+|-inf) dB/.exec(r.stderr);
  return m ? (m[1] === "-inf" ? -999 : Number(m[1])) : NaN;
}

// openFresh opens src in the editor and waits for the window to show it as a
// new edit (one clip): the window reloads asynchronously, so
// right after editor.open, editor.get can still answer for the old edit.
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- ctl results are JSON
async function openFresh(src: string): Promise<any> {
  // editor.open reloads the window asynchronously, and the edit before it may
  // hold the same source as one clip (the text phase's does), so also wait for
  // the track ids to change: otherwise a phase can grab ids the reload replaces.
  const trackIds = (g: { tracks: { id: string }[] }) => g.tracks.map((t) => t.id).join(",");
  let before = "";
  try {
    before = trackIds(ctl("editor.get"));
  } catch {
    // no edit open yet
  }
  ctl("editor.open", { path: src });
  const norm = (x: string) => x.replaceAll("\\", "/").toLowerCase();
  return until("a fresh edit of the source", () => {
    const g = ctl("editor.get");
    const clips = g.tracks.flatMap((t: { clips: CtlClip[] }) => t.clips);
    return clips.length === 1 && g.media.length === 1 && norm(g.media[0].path) === norm(src) && trackIds(g) !== before && g;
  });
}

// ---- phase: time (C5) --------------------------------------------------------

type CtlClip = Clip & { end: number };
async function phaseTime(src: string) {
  say("== time: bullet ramp + blend, reversed clip, freeze frame");
  let g = await openFresh(src);
  const vt = g.tracks.find((t: { kind: string }) => t.kind === "video");
  const aId: string = vt.clips[0].id;
  const bId: string = ctl("editor.split", { t: 3, trackId: vt.id }).newClips[0];
  ctl("editor.moveClip", { clipId: bId, start: 60 }); // out of the way while A grows
  const a = ctl("editor.setSpeedCurve", { clipId: aId, preset: "bullet" });
  ctl("editor.setSlowMotion", { clipId: aId, mode: "blend" });
  ctl("editor.setReverse", { clipId: bId });
  ctl("editor.moveClip", { clipId: bId, start: a.end });
  ctl("editor.freezeFrame", { trackId: vt.id, t: 0.5, seconds: 1.5 });
  g = ctl("editor.get");
  const clips: CtlClip[] = g.tracks.find((t: { id: string }) => t.id === vt.id).clips;
  for (const c of clips) say(`  ${c.id} ${c.start.toFixed(3)}-${c.end.toFixed(3)} src ${c.in.toFixed(2)}-${c.out.toFixed(2)}${c.hold ? " hold" : ""}${c.reversed ? " reversed" : ""}${c.speedCurve ? ` curve=${c.speedCurve.preset}` : ""}${c.blendFrames ? ` blend=${c.blendFrames}` : ""}`);

  // The preview shows a reversed clip from its proxy; wait for it.
  const t0 = Date.now();
  await until("reverse proxy", () => fs.existsSync(REVERSE_DIR) && fs.readdirSync(REVERSE_DIR).some((f) => f.endsWith(".mp4") && fs.statSync(path.join(REVERSE_DIR, f)).mtimeMs >= startedAt), 180000);
  say(`  reverse proxy ready after ${((Date.now() - t0) / 1000).toFixed(1)} s`);
  await sleep(1500);

  const out = await compare("time", samples(clips, g.canvas.fps), g.duration, g.canvas.fps);
  const hold = clips.find((c) => c.hold);
  const ramp = clips.find((c) => c.speedCurve);
  if (hold) {
    const v = meanVolume(out, hold.start + 0.3, 0.8);
    say(`  freeze audio ${v.toFixed(1)} dB (want silence)`);
    if (v > -60) fail("time: the freeze frame isn't silent");
  }
  if (ramp) {
    const v = meanVolume(out, ramp.start + 0.05, 0.25);
    say(`  ramp 1x start audio ${v.toFixed(1)} dB (want the tone)`);
    if (v < -40) fail("time: the ramp's 1x start is silent");
  }
}

// ---- phase: fx (C3) ----------------------------------------------------------

// fxProject is a v3 project over src: clip A (source 0..3 s) with a look,
// a keyed gaussian blur, a zoom punch-in and keyed opacity + x; clip B
// (source 3..6 s) with exposure, a keyed RGB split, a flash and a seeded
// shake. Keyframe times are seconds from the clip's start on the timeline.
export function fxProject(media: Media, id: string): Project {
  let p = newProject(media, "Toru real-build check (delete me)");
  p = splitAt(p, 3);
  p = { ...p, version: 3, id };
  const vt = p.tracks.find((t) => t.kind === "video")!;
  const [a, b] = vt.clips;
  let n = 0;
  const fx = (type: string, params: Record<string, unknown>, keys?: Record<string, { t: number; v: number; ease: "linear" }[]>) => ({ id: `fx-check-${++n}`, type, enabled: true, params, ...(keys ? { keys } : {}) });
  const A: Clip = {
    ...a,
    filters: { brightness: 0, contrast: 1, saturation: 1, look: "none", blur: 0 },
    chroma: undefined,
    effects: [
      fx("look", { look: "sepia", saturation: 1, amount: 1 }),
      fx("gaussianBlur", { sigma: 0 }, { sigma: [{ t: 0, v: 0, ease: "linear" }, { t: 2.5, v: 10, ease: "linear" }] }),
      fx("zoom", { scale: 1.6, center: [0.5, 0.5], start: 1, in: 0.3, hold: 0.8, out: 0.3 }),
    ] as Clip["effects"],
    anim: {
      opacity: [{ t: 0, v: 1, ease: "linear" }, { t: 2.9, v: 0.4, ease: "linear" }],
      x: [{ t: 0, v: 0.5, ease: "linear" }, { t: 2.9, v: 0.6, ease: "inOut" }],
    },
  };
  const B: Clip = {
    ...b,
    filters: { brightness: 0, contrast: 1, saturation: 1, look: "none", blur: 0 },
    chroma: undefined,
    effects: [
      fx("adjust", { exposure: 0.5 }),
      fx("rgbSplit", { amount: 0, angle: 0 }, { amount: [{ t: 0, v: 0, ease: "linear" }, { t: 2, v: 30, ease: "linear" }] }),
      fx("flash", { color: "#ffffff", intensity: 0.8, start: 1, duration: 0.3 }),
      fx("shake", { amplitude: 12, frequency: 8, rotation: 1, zoom: 1.1, seed: 7 }),
    ] as Clip["effects"],
  };
  vt.clips = [A, B];
  const m = migrateProject(p);
  if (m !== p) throw new Error("fx project isn't v3 (migrateProject changed it)");
  return p;
}

async function phaseFx(src: string) {
  say("== fx: v3 effects, effect keys and anim keys");
  const g = await openFresh(src);
  const m = g.media[0];
  const media: Media = { id: m.id, path: m.path, url: "", kind: m.kind, duration: m.duration, width: m.width, height: m.height, fps: m.fps, hasAudio: m.hasAudio, tracks: m.audioTracks ?? [] };
  const id = `proj-toru-check-${stamp}`;
  const p = fxProject(media, id);
  fs.mkdirSync(PROJECTS_DIR, { recursive: true });
  fs.writeFileSync(path.join(PROJECTS_DIR, `${id}.json`), JSON.stringify(p));
  ctl("project.open", { id });
  const want = p.tracks.flatMap((t) => t.clips.map((c) => c.id));
  const got = await until("project opened", () => {
    const x = ctl("editor.get");
    const ids = x.tracks.flatMap((t: { clips: { id: string }[] }) => t.clips.map((c) => c.id));
    return want.every((w) => ids.includes(w)) && x;
  });
  const vt = got.tracks.find((t: { kind: string }) => t.kind === "video");
  for (const c of vt.clips as CtlClip[]) {
    say(`  ${c.id} ${c.start.toFixed(3)}-${c.end.toFixed(3)} effects ${(c.effects ?? []).map((e) => e.type + (e.keys ? "(keyed)" : "")).join(", ")}${c.anim ? ` anim ${Object.keys(c.anim).join("/")}` : ""}`);
    if (!c.effects?.length) fail(`fx: clip ${c.id} opened without its effects`);
  }
  await sleep(1500);
  await compare("fx", samples(vt.clips, got.canvas.fps, 8), projectDuration(p), got.canvas.fps);
}

// ---- phase: fx7 (C7 commands) -----------------------------------------------

// makeCube writes a 17³ .cube that swaps red and blue: unmistakable on the
// coloured seconds (red reads blue), and smooth, so the preview's 3D-LUT
// texture and ffmpeg's lut3d interpolate it the same way.
export function makeCube(file: string, n = 17) {
  const lines = ["TITLE \"Toru check swap RB\"", `LUT_3D_SIZE ${n}`];
  for (let b = 0; b < n; b++)
    for (let g = 0; g < n; g++)
      for (let r = 0; r < n; r++) lines.push(`${(b / (n - 1)).toFixed(6)} ${(g / (n - 1)).toFixed(6)} ${(r / (n - 1)).toFixed(6)}`);
  fs.writeFileSync(file, lines.join("\n") + "\n");
}

// Ids this run created in Toru's stores, removed in the phase's finally.
const made = { presets: [] as string[], luts: [] as string[] };

// phaseFx7 builds the stack through C7's agent commands instead of a saved
// project: a .cube imported with preset.importLut and put on clip A
// (editor.addEffect lut), a gaussian blur keyed with editor.setKeyframe, a
// keyed opacity; A's stack saved as a filter (editor.savePreset), written to
// a .torufx (preset.export), imported back (preset.import) and applied to
// clip B (editor.applyPreset), then a built-in look on B on top.
async function phaseFx7(src: string) {
  say("== fx7: C7 commands (LUT import, effects, keyframes, presets, .torufx)");
  if (!hasCommand("editor.addEffect")) {
    fail("fx7: this build has no editor.addEffect (C7 isn't in it)");
    return;
  }
  const dir = path.join(OUT, "fx7");
  fs.mkdirSync(dir, { recursive: true });
  try {
    // Its file name is its id in Toru's LUT folder: stamped, so it never
    // replaces one of Blake's.
    const cube = path.join(MEDIA_DIR, `toru-check-${stamp}.cube`);
    makeCube(cube);
    const lut = ctl("preset.importLut", { path: cube });
    made.luts.push(lut.id);
    say(`  imported LUT ${lut.id} (${lut.name}, size ${lut.size})`);

    let g = await openFresh(src);
    let vt = g.tracks.find((t: { kind: string }) => t.kind === "video");
    const aId: string = vt.clips[0].id;
    const bId: string = ctl("editor.split", { t: 3, trackId: vt.id }).newClips[0];

    ctl("editor.addEffect", { clipId: aId, type: "lut", params: { lut: lut.id, amount: 1 } });
    const blur = ctl("editor.addEffect", { clipId: aId, type: "gaussianBlur", params: { sigma: 0 } });
    ctl("editor.setKeyframe", { clipId: aId, effectId: blur.effectId, param: "sigma", t: 0.2, value: 0, ease: "linear" });
    ctl("editor.setKeyframe", { clipId: aId, effectId: blur.effectId, param: "sigma", t: 2.6, value: 8, ease: "linear" });
    ctl("editor.setKeyframe", { clipId: aId, target: "opacity", t: 0, value: 1, ease: "linear" });
    ctl("editor.setKeyframe", { clipId: aId, target: "opacity", t: 2.9, value: 0.5, ease: "linear" });

    // A's stack → My filters → .torufx → imported copy → clip B.
    const saved = ctl("editor.savePreset", { clipId: aId, name: `Toru check ${stamp}` });
    made.presets.push(saved.id);
    const torufx = ctl("preset.export", { id: saved.id, path: path.join(dir, `toru-check-${stamp}.torufx`) }).path;
    const imported = ctl("preset.import", { path: torufx });
    made.presets.push(imported.id);
    say(`  saved filter ${saved.id} (${saved.effects} effects) → ${torufx} → imported ${imported.id}`);
    ctl("editor.applyPreset", { clipId: bId, presetId: imported.id, replace: true });
    const look = (ctl("preset.list").presets as { id: string; name: string; builtin?: boolean }[]).find((x) => x.builtin && /sepia|cool|warm|vintage/i.test(x.name + x.id));
    if (look) ctl("editor.applyPreset", { clipId: bId, presetId: look.id });
    else fail("fx7: preset.list has no built-in look");

    g = ctl("editor.get");
    vt = g.tracks.find((t: { id: string }) => t.id === vt.id);
    for (const c of vt.clips as CtlClip[]) say(`  ${c.id} ${c.start.toFixed(3)}-${c.end.toFixed(3)} effects ${(c.effects ?? []).map((e) => e.type + (e.keys ? "(keyed)" : "")).join(", ")}${c.anim ? ` anim ${Object.keys(c.anim).join("/")}` : ""}`);
    const B = (vt.clips as CtlClip[]).find((c) => c.id === bId);
    if (!B?.effects?.some((e) => e.type === "lut")) fail("fx7: the imported .torufx didn't bring the LUT to clip B");

    // The LUT must show in the export too: A's first second is red in the
    // source, blue through the swap.
    await sleep(1500);
    const out = await compare("fx7", samples(vt.clips, g.canvas.fps, 8), g.duration, g.canvas.fps);
    const first = describe(rgb(out, 0.5));
    say(`  export at 0.5 s: ${first} (source is red there; the LUT makes it blue)`);
    if (!first.startsWith("blue")) fail("fx7: the export doesn't show the LUT");
  } finally {
    for (const id of made.presets.splice(0)) {
      try {
        ctl("preset.delete", { id, confirm: true });
      } catch (e) {
        say(`  cleanup: preset ${id}: ${e}`);
      }
    }
    for (const id of made.luts.splice(0)) {
      try {
        ctl("preset.deleteLut", { id, confirm: true });
      } catch (e) {
        say(`  cleanup: LUT ${id}: ${e}`);
      }
    }
  }
}

// ---- phase: ui (C6) ------------------------------------------------------------

// A minimal CDP client: one page target, Runtime.evaluate, real mouse
// clicks (synthetic pointer events can't take pointer capture in WebView2)
// and screenshots.
// phaseText puts two styled texts on the source and exports: inside each
// text's box the export must differ from the source (the text is there),
// outside it must match (nothing else moved), and the faded-in text must be
// fainter early than late.
async function phaseText(src: string) {
  await openFresh(src);
  ctl("editor.setText", { text: "CLUTCH", x: 0.3, y: 0.5, size: 120, start: 0.5, end: 5, preset: "builtin:caption", style: { shadowOpacity: 0.9, shadowBlur: 0.3 } });
  ctl("editor.setText", { text: "fade", x: 0.75, y: 0.5, size: 120, start: 1, end: 5, preset: "builtin:neon", style: { opacityKeys: [{ t: 0, v: 0.1, ease: "linear" }, { t: 3, v: 1, ease: "linear" }] } });
  const dir = path.join(OUT, "text");
  fs.mkdirSync(dir, { recursive: true });
  const out = path.join(dir, `export-${Date.now()}.mp4`);
  ctl("editor.export", { preset: "original", path: out });
  say(`text: exported → ${out}`);
  // Boxes (of SAMPLE_W x SAMPLE_H) around each text and a control area.
  // Mean per-pixel difference from the source inside a box. (Comparing the
  // boxes' average colours misses text: a dark caption box with white letters
  // can average out to the plain background.)
  const delta = (t: number, x0: number, x1: number) => {
    const a = rgb(out, t), b = rgb(src, t);
    let sum = 0, n = 0;
    for (let y = 38; y < 52; y++)
      for (let x = x0; x < x1; x++) for (let c = 0; c < 3; c++) (sum += Math.abs(a[(y * SAMPLE_W + x) * 3 + c] - b[(y * SAMPLE_W + x) * 3 + c])), n++;
    return sum / n;
  };
  const caption = delta(2.5, 36, 60);
  const fadeEarly = delta(1.2, 108, 132);
  const fadeLate = delta(3.8, 108, 132);
  // Left of the caption's box (the caption preset's box spans x≈15-80).
  const control = delta(2.5, 2, 12);
  const before = delta(0.2, 36, 60);
  say(`text: change in caption box ${caption.toFixed(1)}, fade early ${fadeEarly.toFixed(1)} late ${fadeLate.toFixed(1)}, between ${control.toFixed(1)}, before start ${before.toFixed(1)}`);
  if (caption < 12) fail("text: the caption text isn't in the export");
  if (fadeLate < 12) fail("text: the faded-in text isn't in the export");
  if (!(fadeLate > fadeEarly + 4)) fail("text: the opacity ramp didn't show (early vs late)");
  if (control > 6 || before > 6) fail("text: the export changed where there's no text");
}

async function cdpPage(port: number, match: (url: string) => boolean) {
  const list = (await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()) as { type: string; url: string; webSocketDebuggerUrl: string }[];
  const t = list.find((x) => x.type === "page" && match(x.url));
  if (!t) throw new Error(`no page on CDP ${port} matching (have: ${list.map((x) => x.url).join(", ")})`);
  const ws = new WebSocket(t.webSocketDebuggerUrl);
  await new Promise((r, j) => ((ws.onopen = r), (ws.onerror = j)));
  let n = 0;
  const wait = new Map<number, (v: { result?: unknown; error?: { message: string } }) => void>();
  ws.onmessage = (ev) => {
    const msg = JSON.parse(String(ev.data));
    wait.get(msg.id)?.(msg);
    wait.delete(msg.id);
  };
  // eslint-disable-next-line @typescript-eslint/no-explicit-any -- CDP results
  const send = (method: string, params: object = {}): Promise<any> =>
    new Promise((res, rej) => {
      const id = ++n;
      wait.set(id, (m) => (m.error ? rej(new Error(`${method}: ${m.error.message}`)) : res(m.result)));
      ws.send(JSON.stringify({ id, method, params }));
    });
  const evaluate = async (expr: string) => {
    const r = await send("Runtime.evaluate", { expression: expr, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error(`${expr.slice(0, 60)}…: ${r.exceptionDetails.exception?.description ?? r.exceptionDetails.text}`);
    return r.result.value;
  };
  const click = async (x: number, y: number) => {
    for (const type of ["mousePressed", "mouseReleased"]) await send("Input.dispatchMouseEvent", { type, x, y, button: "left", clickCount: 1 });
  };
  const shot = async (file: string) => fs.writeFileSync(file, Buffer.from((await send("Page.captureScreenshot", { format: "png" })).data, "base64"));
  return { send, evaluate, click, shot, close: () => ws.close() };
}

async function phaseUi(port: number, match: (url: string) => boolean) {
  say("== ui: inspector tabs");
  const dir = path.join(OUT, "ui");
  fs.mkdirSync(dir, { recursive: true });
  const page = await cdpPage(port, match);
  try {
    // Select the first video clip unless one is selected (the tabs only show
    // for a clip).
    const center = async (sel: string) =>
      (await page.evaluate(`(() => { const e = document.querySelector(${JSON.stringify(sel)}); if (!e) return null; const r = e.getBoundingClientRect(); return [r.x + r.width / 2, r.y + r.height / 2]; })()`)) as [number, number] | null;
    // Click it, then give the inspector a few seconds before clicking again
    // (a second click on a selected clip can deselect it).
    const tablist = '[role="tablist"][aria-label="Clip settings"]';
    for (let i = 0; i < 6 && !(await center(tablist)); i++) {
      const c = await until("a timeline clip", () => center("[data-clip]"), 20000).catch(() => null);
      if (c) await page.click(c[0], c[1]);
      await until("inspector", () => center(tablist), 5000).catch(() => null);
    }
    if (!(await center(tablist))) await page.shot(path.join(dir, "no-inspector.png"));
    const tabs = (await page.evaluate(`[...document.querySelectorAll('[role="tablist"][aria-label="Clip settings"] [role="tab"]')].map((e) => e.textContent.trim())`)) as string[];
    if (!tabs.length) {
      fail("ui: no inspector tabs (no clip selected?)");
      return;
    }
    say(`  tabs: ${tabs.join(", ")}`);
    for (let i = 0; i < tabs.length; i++) {
      await page.evaluate(`document.querySelectorAll('[role="tablist"][aria-label="Clip settings"] [role="tab"]')[${i}].click()`);
      await sleep(600);
      const on = await page.evaluate(`document.querySelectorAll('[role="tablist"][aria-label="Clip settings"] [role="tab"]')[${i}].getAttribute("aria-selected")`);
      const f = path.join(dir, `inspector_${i}_${tabs[i].toLowerCase().replace(/\W+/g, "-")}.png`);
      await page.shot(f);
      say(`  ${tabs[i]}: ${on === "true" ? "shown" : "NOT SELECTED"} → ${f}`);
      if (on !== "true") fail(`ui: tab ${tabs[i]} didn't select`);
    }
    const errs = await page.evaluate(`document.body.innerText.includes("Something went wrong")`);
    if (errs) fail("ui: the editor shows an error boundary");
  } finally {
    page.close();
  }
}

// ---- phase: v17 (v0.17.0 editor) --------------------------------------------

type Page = Awaited<ReturnType<typeof cdpPage>>;
const v17Dir = () => {
  const d = path.join(OUT, "v17");
  fs.mkdirSync(d, { recursive: true });
  return d;
};
const centerOf = (page: Page, sel: string) =>
  page.evaluate(`(() => { const e = document.querySelector(${JSON.stringify(sel)}); if (!e) return null; const r = e.getBoundingClientRect(); return r.width > 0 ? [r.x + r.width / 2, r.y + r.height / 2] : null; })()`) as Promise<[number, number] | null>;

// v17Ui checks the layout rail, the palette and the menus. It needs only the
// page, so the dry run does it against vite too.
async function v17Ui(page: Page) {
  const dir = v17Dir();
  // Rail: every library tab opens its panel.
  const tabs = (await page.evaluate(`[...document.querySelectorAll('[role="tablist"][aria-label="Libraries"] [role="tab"]')].map((e) => e.textContent.trim())`)) as string[];
  say(`  rail: ${tabs.join(", ") || "(none)"}`);
  if (tabs.length < 3) fail("v17: the library rail has fewer than 3 tabs");
  for (let i = 0; i < tabs.length; i++) {
    await page.evaluate(`(() => { const t = document.querySelectorAll('[role="tablist"][aria-label="Libraries"] [role="tab"]')[${i}]; if (t.getAttribute('aria-selected') !== 'true') t.click(); return true })()`);
    await sleep(400);
    const shown = await page.evaluate(`(() => { const p = document.querySelector('[role="tabpanel"]'); return !!p && p.getBoundingClientRect().width > 0 && document.querySelectorAll('[role="tablist"][aria-label="Libraries"] [role="tab"]')[${i}].getAttribute('aria-selected') === 'true' })()`);
    if (!shown) fail(`v17: library tab ${tabs[i]} didn't open its panel`);
    await page.shot(path.join(dir, `rail_${i}_${tabs[i].toLowerCase().replace(/\W+/g, "-")}.png`));
  }
  // Palette: Ctrl+K opens it, a search finds Split, Escape closes it.
  await page.evaluate(`(document.activeElement && document.activeElement.blur && document.activeElement.blur(), true)`);
  for (const type of ["keyDown", "keyUp"]) await page.send("Input.dispatchKeyEvent", { type, key: "k", code: "KeyK", windowsVirtualKeyCode: 75, modifiers: 2 });
  const open = await until("palette", () => page.evaluate(`!!document.querySelector('[role="dialog"][aria-label="Search actions"]')`), 5000).catch(() => false);
  if (!open) fail("v17: Ctrl+K didn't open the palette");
  else {
    await page.send("Input.insertText", { text: "split" });
    await sleep(400);
    const opts = (await page.evaluate(`[...document.querySelectorAll('#palette-list [role="option"]')].map((o) => o.textContent.trim())`)) as string[];
    say(`  palette "split": ${opts.slice(0, 4).join(" | ") || "(nothing)"}`);
    if (!opts.some((o) => /split/i.test(o))) fail("v17: the palette doesn't find Split");
    await page.shot(path.join(dir, "palette.png"));
    for (const type of ["keyDown", "keyUp"]) await page.send("Input.dispatchKeyEvent", { type, key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
    await sleep(300);
    if (await page.evaluate(`!!document.querySelector('[role="dialog"][aria-label="Search actions"]')`)) fail("v17: Escape didn't close the palette");
  }
  // Menus: right-click a clip, then an empty part of a track.
  const rightClick = async (x: number, y: number) => {
    for (const type of ["mousePressed", "mouseReleased"]) await page.send("Input.dispatchMouseEvent", { type, x, y, button: "right", clickCount: 1 });
  };
  const menuItems = () => page.evaluate(`[...document.querySelectorAll('[role="menu"] [role="menuitem"]')].map((e) => e.textContent.trim())`) as Promise<string[]>;
  const clip = await centerOf(page, "[data-clip]");
  if (!clip) fail("v17: no clip on the timeline to right-click");
  else {
    await rightClick(clip[0], clip[1]);
    await sleep(400);
    const items = await menuItems();
    say(`  clip menu: ${items.length} items (${items.slice(0, 5).join(", ")}…)`);
    if (items.length < 4) fail("v17: the clip menu didn't open");
    await page.shot(path.join(dir, "menu_clip.png"));
    for (const type of ["keyDown", "keyUp"]) await page.send("Input.dispatchKeyEvent", { type, key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
    await sleep(300);
  }
  // The player: the biggest canvas on the page (clip waveforms are canvases too).
  const player = (await page.evaluate(`(() => { let best = null, area = 0; for (const c of document.querySelectorAll('canvas')) { const r = c.getBoundingClientRect(); if (r.width * r.height > area) { area = r.width * r.height; best = [r.x + r.width / 2, r.y + r.height / 2]; } } return best })()`)) as [number, number] | null;
  if (player) {
    await rightClick(player[0], player[1]);
    await sleep(400);
    const items = await menuItems();
    say(`  player menu: ${items.length} items`);
    if (!items.length) fail("v17: the player's menu didn't open");
    await page.shot(path.join(dir, "menu_player.png"));
    for (const type of ["keyDown", "keyUp"]) await page.send("Input.dispatchKeyEvent", { type, key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
    await sleep(300);
  }
  if (await page.evaluate(`document.body.innerText.includes("Something went wrong")`)) fail("v17: the editor shows an error boundary");
}

// waveCheck says whether the clips of the open edit draw a waveform that
// isn't flat (some column of a clip canvas has paint well off the middle).
async function waveCheck(page: Page, label: string, shot: string) {
  await until(`${label} waveform`, () => page.evaluate(`[...document.querySelectorAll('[data-clip] canvas')].some((c) => c.style.display !== 'none' && c.width > 0)`), 30000).catch(() => false);
  const stat = (await page.evaluate(`(() => {
    let best = 0, n = 0;
    for (const c of document.querySelectorAll('[data-clip] canvas')) {
      if (c.style.display === 'none' || !c.width) continue;
      n++;
      const g = c.getContext('2d'); const d = g.getImageData(0, 0, c.width, c.height).data;
      for (let x = 0; x < c.width; x += 2) { let top = c.height; for (let y = 0; y < c.height; y++) if (d[(y * c.width + x) * 4 + 3] > 40) { top = y; break; } best = Math.max(best, (c.height - top) / c.height); }
    }
    return { n, best };
  })()`)) as { n: number; best: number };
  say(`  ${label}: ${stat.n} waveform canvas(es), tallest column ${(stat.best * 100).toFixed(0)}% of the clip`);
  if (!stat.n) fail(`v17: no waveform on ${label}`);
  else if (stat.best < 0.6) fail(`v17: the ${label} waveform is flat`);
  await page.shot(path.join(v17Dir(), shot));
}

// realRecording is the newest recording in Videos/Toru Replays (or TORU_REAL):
// it is only opened, never changed.
function realRecording(): string | null {
  if (process.env.TORU_REAL) return process.env.TORU_REAL;
  const root = path.join(os.homedir(), "Videos", "Toru Replays");
  let best: { f: string; t: number } | null = null;
  const walk = (d: string, depth: number) => {
    for (const e of fs.existsSync(d) ? fs.readdirSync(d, { withFileTypes: true }) : []) {
      const f = path.join(d, e.name);
      if (e.isDirectory() && depth < 2 && !e.name.startsWith("toru-check-")) walk(f, depth + 1);
      else if (e.isFile() && /\.(mp4|mkv)$/i.test(e.name)) {
        const t = fs.statSync(f).mtimeMs;
        if (!best || t > best.t) best = { f, t };
      }
    }
  };
  walk(root, 0);
  return best ? (best as { f: string }).f : null;
}

const realOpened = new Set<string>();

async function phaseV17(src: string) {
  say("== v17: rail, palette, menus, multi-select, waveforms, 9:16 fill");
  let g = await openFresh(src);
  const page = await cdpPage(CDP, (u) => u.includes("view=edit"));
  try {
    await sleep(1500);
    await v17Ui(page);

    // Multi-select: split into three, select two, copy, paste after the end.
    const vt = g.tracks.find((t: { kind: string }) => t.kind === "video");
    ctl("editor.split", { t: 2, trackId: vt.id });
    ctl("editor.split", { t: 4, trackId: vt.id });
    g = ctl("editor.get");
    const ids: string[] = g.tracks.find((t: { id: string }) => t.id === vt.id).clips.map((c: { id: string }) => c.id);
    const count = () => (ctl("editor.get").tracks as { clips: unknown[] }[]).reduce((n, t) => n + t.clips.length, 0);
    const before = count();
    ctl("editor.selectClips", { clipIds: [ids[0], ids[2]] });
    await sleep(400);
    const selected = (await page.evaluate(`document.querySelectorAll('[data-clip][aria-selected="true"], [data-clip].outline-primary, [data-clip][data-selected="true"]').length`)) as number;
    ctl("editor.copyClips", { clipIds: [ids[0], ids[2]] });
    const pasted = ctl("editor.pasteClips", { t: 7 });
    const after = count();
    say(`  multi-select: ${selected} shown selected; pasted ${JSON.stringify(pasted).slice(0, 80)}; clips ${before} → ${after}`);
    if (after !== before + 2) fail(`v17: pasting 2 clips made ${after - before}`);
    await page.shot(path.join(v17Dir(), "multiselect_paste.png"));
    ctl("editor.undo");
    if (count() !== before) fail("v17: undo didn't take the pasted clips back");

    // Waveforms: the test source's tone, then a real recording.
    await waveCheck(page, "the test source", "wave_source.png");
    const real = realRecording();
    if (!real) say("  (no recording in Videos/Toru Replays: real-recording waveform skipped)");
    else {
      realOpened.add(real);
      say(`  real recording: ${real}`);
      await openFresh(real);
      await sleep(1500);
      await waveCheck(page, path.basename(real), "wave_real.png");
    }

    // 9:16 with fill: preview vs export, and the export's size.
    g = await openFresh(src);
    ctl("editor.setCanvas", { preset: "9:16", place: "fill" });
    g = await until("the 9:16 frame", () => {
      const x = ctl("editor.get");
      return x.canvas.width === 1080 && x.canvas.height === 1920 && x;
    });
    await sleep(1500);
    await page.shot(path.join(v17Dir(), "canvas_9x16_fill.png"));
    const clips = g.tracks.flatMap((t: { clips: CtlClip[] }) => t.clips);
    const out = await compare("v17-canvas", samples(clips, 30, 4), projectDuration(g as Project), 30);
    const size = run(FFPROBE, ["-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "csv=p=0:s=x", out]).trim();
    say(`  9:16 export: ${size}`);
    if (size !== "1080x1920") fail(`v17: the 9:16 export is ${size}`);
  } finally {
    page.close();
  }
}

// ---- phase: trans (transitions) ----------------------------------------------

// The source cut at 2 s and 4 s: a cross dissolve on the first cut, a wipe
// on the second. Transitions sit centred on the cut, so the length stays 6 s;
// frames through both (and in the plain parts) must match preview vs export,
// and the dissolve's middle must be a mix of red and green, neither alone.
async function phaseTrans(src: string) {
  say("== trans: cross dissolve + wipe on cuts");
  const g0 = await openFresh(src);
  const vt = g0.tracks.find((t: { kind: string }) => t.kind === "video");
  const b: string = ctl("editor.split", { t: 2, trackId: vt.id }).newClips[0];
  const c: string = ctl("editor.split", { t: 4, trackId: vt.id }).newClips[0];
  const t1 = ctl("editor.addTransition", { clipId: b, type: "fade", duration: 1 });
  const t2 = ctl("editor.addTransition", { clipId: c, type: "wipeleft", duration: 0.8 });
  say(`  added ${JSON.stringify(t1)} ${JSON.stringify(t2)}`);
  const g = await until("both transitions", () => {
    const x = ctl("editor.get");
    const clips = x.tracks.flatMap((t: { clips: (CtlClip & { transition?: unknown })[] }) => t.clips);
    return clips.filter((k: { transition?: unknown }) => k.transition).length === 2 && x;
  });
  const dur = projectDuration(g as Project);
  if (Math.abs(dur - 6) > 0.05) fail(`trans: the timeline is ${dur.toFixed(2)} s with transitions, want 6`);
  const fps = 30;
  const at = (t: number) => (Math.floor(t * fps) + 0.5) / fps;
  const ts = [0.6, 1.6, 1.85, 2.0, 2.2, 2.4, 3.0, 3.7, 4.0, 4.3, 5.2].map(at);
  const out = await compare("trans", ts, dur, fps);
  const mid = describe(rgb(out, at(2.0) - 0.5 / fps - 0.001));
  say(`  dissolve middle (export): ${mid}`);
  if (/^(red|green) /.test(mid)) fail(`trans: the dissolve's middle is plain ${mid.split(" ")[0]}, not a mix`);
  // Timeline screenshot with the transition boxes.
  const page = await cdpPage(CDP, (u) => u.includes("view=edit"));
  try {
    await sleep(800);
    const dir = path.join(OUT, "trans");
    await page.shot(path.join(dir, "timeline.png"));
  } finally {
    page.close();
  }
}

// ---- cleanup ---------------------------------------------------------------

function cleanup() {
  if (KEEP) {
    say(`--keep: left ${MEDIA_DIR}, the test projects and reverse proxies`);
    return;
  }
  // Projects this run saved: ours, and the autosaves of the opened source.
  if (fs.existsSync(PROJECTS_DIR)) {
    for (const f of fs.readdirSync(PROJECTS_DIR)) {
      const full = path.join(PROJECTS_DIR, f);
      if (!f.endsWith(".json") || fs.statSync(full).mtimeMs < startedAt) continue;
      const doc = fs.readFileSync(full, "utf8");
      // Any run's test media (an earlier run's edit can be autosaved again
      // when the window reopens it), and new autosaves of a real recording
      // the v17 phase opened (projects that existed before the run stay).
      const real = [...realOpened].some((r) => doc.includes(JSON.stringify(r).slice(1, -1)) || doc.includes(r.replaceAll("\\", "/")));
      if (/toru-check-\d{14}/.test(doc) || f.startsWith("proj-toru-check-") || (real && !projectsBefore.has(f))) {
        fs.rmSync(full);
        say(`removed project ${f}`);
      }
    }
  }
  if (fs.existsSync(REVERSE_DIR)) {
    for (const f of fs.readdirSync(REVERSE_DIR)) {
      const full = path.join(REVERSE_DIR, f);
      if (fs.statSync(full).mtimeMs >= startedAt) {
        fs.rmSync(full, { recursive: true, force: true });
        say(`removed reverse proxy ${f}`);
      }
    }
  }
  if (!DRY) fs.rmSync(MEDIA_DIR, { recursive: true, force: true });
  say(`removed ${MEDIA_DIR} (exports and frames stay in ${OUT})`);
}

// ---- dry run -----------------------------------------------------------------

async function dry(src: string) {
  say("== dry: fx project");
  const info = JSON.parse(run(FFPROBE, ["-v", "error", "-show_entries", "stream=codec_type,width,height,r_frame_rate:format=duration", "-of", "json", src]));
  const v = info.streams.find((s: { codec_type: string }) => s.codec_type === "video");
  const [fn, fd] = String(v.r_frame_rate).split("/").map(Number);
  const media: Media = { id: "m1", path: src, url: "", kind: "video", duration: Number(info.format.duration), width: v.width, height: v.height, fps: fn / fd, hasAudio: info.streams.some((s: { codec_type: string }) => s.codec_type === "audio"), tracks: [] };
  const p = fxProject(media, `proj-toru-check-${stamp}`);
  const f = path.join(OUT, "fx-project.json");
  fs.writeFileSync(f, JSON.stringify(p, null, 2));
  const vt = p.tracks.find((t) => t.kind === "video")!;
  say(`  ${f}: ${vt.clips.map((c) => `${c.id} ${c.start}-${clipEnd(c).toFixed(2)} [${(c.effects ?? []).map((e) => e.type).join(", ")}]`).join("; ")}`);

  say("== dry: .cube LUT through ffmpeg lut3d (the export side of fx7)");
  {
    const cube = path.join(OUT, "swap-rb.cube");
    makeCube(cube);
    const png = path.join(OUT, "lut.png");
    run(FFMPEG, ["-v", "error", "-y", "-ss", "0.5", "-i", src, "-frames:v", "1", "-vf", `lut3d=file='${cube.replaceAll("\\", "/").replace(":", "\\:")}'`, png]);
    const got = describe(rgb(png));
    say(`  red second through the LUT: ${got}`);
    if (!got.startsWith("blue")) fail("dry: the swap-RB cube doesn't turn red into blue");
  }

  say("== dry: frame diff calibration (source encode vs its own frames)");
  const dir = path.join(OUT, "calib");
  fs.mkdirSync(dir, { recursive: true });
  const re = path.join(dir, "reencode.mp4");
  run(FFMPEG, ["-v", "error", "-y", "-i", src, "-c:v", "libx264", "-crf", "23", "-c:a", "copy", re]);
  let worst = 0, worstBox = 0;
  for (const t of samples([{ start: 0, end: 6 }], 30, 12)) {
    const png = path.join(dir, `src_${t.toFixed(3)}.png`);
    run(FFMPEG, ["-v", "error", "-y", "-i", src, "-ss", t.toFixed(3), "-frames:v", "1", png]);
    const a = rgb(png), b = rgb(re, t);
    const d = diff(a, b);
    worst = Math.max(worst, d.mean);
    worstBox = Math.max(worstBox, boxOff(a, b));
    if (d.mean > LIMIT.mean || d.p99 > LIMIT.p99 || boxOff(a, b) > BOX_LIMIT) fail(`calibration: ${t.toFixed(3)} s already over the limit (mean ${d.mean.toFixed(1)}, p99 ${d.p99})`);
  }
  // A frame a third of a second off must be over the limit, or the check
  // can't see a timing error.
  // compare's seek (frame start minus 1 ms) must land on frame n itself.
  for (const n of [17, 76, 133]) {
    const exact = path.join(dir, `frame_${n}.png`);
    run(FFMPEG, ["-v", "error", "-y", "-i", src, "-vf", `select='eq(n,${n})'`, "-frames:v", "1", "-fps_mode", "passthrough", exact]);
    const t = (n + 0.5) / 30;
    const dx = boxOff(rgb(exact), rgb(src, t - 0.5 / 30 - 0.001));
    if (dx > 1) fail(`calibration: the export seek for frame ${n} lands ${dx.toFixed(1)} px away`);
  }
  // And 2 frames off must be caught by the box.
  for (const [t0, t1, label] of [[2.55, 2.883, "10 frames"], [2.55, 2.617, "2 frames"]] as const) {
    const a = rgb(src, t0), b = rgb(src, t1);
    const off = diff(a, b);
    const dx = boxOff(a, b);
    say(`  ${label} off: mean ${off.mean.toFixed(1)} p99 ${off.p99} box ${dx.toFixed(1)} px (limits mean ${LIMIT.mean}, p99 ${LIMIT.p99}, box ${BOX_LIMIT})`);
    if (off.mean <= LIMIT.mean && off.p99 <= LIMIT.p99 && dx <= BOX_LIMIT) fail(`calibration: a ${label} timing error passes the limits`);
  }
  say(`  worst same-time: mean ${worst.toFixed(2)}, box ${worstBox.toFixed(1)} px`);

  if (URL_) {
    // Headless Edge on the vite dev server; ?view=edit loads its sample
    // without Go. Edge may resolve localhost to a different address than
    // vite listens on: start vite with --host 127.0.0.1 and pass that.
    const ok = await fetch(URL_).then((r) => r.ok).catch(() => false);
    if (!ok) throw new Error(`${URL_} doesn't answer (start: bunx vite --host 127.0.0.1 --port 5191)`);
    const edge = process.env.EDGE || "C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe";
    const profile = fs.mkdtempSync(path.join(os.tmpdir(), "toru-check-edge-"));
    const port = CDP + 1;
    const proc = spawn(edge, ["--headless=new", `--remote-debugging-port=${port}`, `--user-data-dir=${profile}`, "--window-size=1600,1000", "--autoplay-policy=no-user-gesture-required", `${URL_.replace(/\/$/, "")}/?view=edit`], { stdio: "ignore" });
    try {
      await until("edge", async () => (await fetch(`http://127.0.0.1:${port}/json/list`)).ok, 20000);
      await sleep(4000);
      await phaseUi(port, (u) => u.includes("view=edit"));
      say("== dry: v17 rail, palette, menus (vite)");
      const page = await cdpPage(port, (u) => u.includes("view=edit"));
      try {
        await v17Ui(page);
      } finally {
        page.close();
      }
    } finally {
      proc.kill();
      await sleep(500);
      fs.rmSync(profile, { recursive: true, force: true });
    }
  }
}

// ---- main ------------------------------------------------------------------

try {
  say(`out: ${OUT}`);
  const src = makeSource();
  say(`source: ${src}`);
  if (DRY) await dry(src);
  else {
    if (PHASES.includes("time")) await phaseTime(src);
    if (PHASES.includes("fx")) await phaseFx(src);
    if (PHASES.includes("fx7")) await phaseFx7(src);
    if (PHASES.includes("text")) await phaseText(src);
    if (PHASES.includes("ui")) await phaseUi(CDP, (u) => u.includes("view=edit"));
    if (PHASES.includes("v17")) await phaseV17(src);
    if (PHASES.includes("trans")) await phaseTrans(src);
  }
} catch (e) {
  fail(`error: ${e instanceof Error ? e.message : e}`);
} finally {
  if (!DRY) {
    try {
      cleanup();
    } catch (e) {
      say(`cleanup: ${e}`);
    }
  }
  say(failures ? `${failures} FAILED` : "all checks passed");
  fs.writeFileSync(path.join(OUT, "log.txt"), log.join("\n"));
  process.exitCode = failures ? 1 : 0;
}
