// waveform-check exercises the timeline waveform maths (src/trim/waveform.ts):
// the peak pyramid, the dB volume shift, and which source each pixel column
// shows for trimmed, sped-up, reversed, ramped, frozen, faded and muted
// clips; then times a 3-hour recording drawn whole and zoomed in.
//
//   bun scripts/waveform-check.ts
import type { Clip, Track } from "../src/trim/project";
import { presetCurve } from "../src/trim/speed";
import { buildPeaks, columnHeights, peakHeight, peakIn } from "../src/trim/waveform";

let pass = 0;
let fail = 0;
function ok(name: string, cond: boolean, detail?: unknown) {
  if (cond) pass++;
  else {
    fail++;
    console.log(`FAIL ${name}${detail === undefined ? "" : `: ${JSON.stringify(detail)}`}`);
  }
}
const near = (a: number, b: number, tol = 1e-6) => Math.abs(a - b) <= tol;

const clip = (p: Partial<Clip>): Clip => ({
  id: "c",
  media: "m",
  start: 0,
  in: 0,
  out: 10,
  speed: 1,
  volume: 1,
  muted: false,
  fadeIn: 0,
  fadeOut: 0,
  transform: { x: 0.5, y: 0.5, scale: 1, opacity: 1 },
  filters: { brightness: 0, contrast: 1, saturation: 1, look: "none", blur: 0 },
  ...p,
});
const track: Track = { id: "t", kind: "audio", muted: false, hidden: false, locked: false, clips: [] };

// Pyramid and lookup.
{
  const base = new Uint8Array([1, 9, 3, 4, 200, 6, 7]);
  const p = buildPeaks(base, 1);
  ok("levels", p.levels.map((l) => Array.from(l)).join("|") === "1,9,3,4,200,6,7|9,4,200,7|9,200|200", p.levels.map((l) => Array.from(l)));
  ok("peakIn one bin", peakIn(p, 2.2, 2.2) === 3);
  ok("peakIn span", peakIn(p, 0, 4) === 9);
  ok("peakIn wide uses max", peakIn(p, 0, 7) === 200);
  ok("peakIn past end is silence", peakIn(p, 50, 60) === 0);
  ok("peakIn exact edges", peakIn(p, 5, 7) === 7 && peakIn(p, 1, 4) === 9 && peakIn(p, 2, 4) === 4);
  // Brute force against every range of a random buffer.
  const rnd = new Uint8Array(37).map((_, i) => (i * 97 + 13) % 251);
  const q = buildPeaks(rnd, 1);
  let bad = 0;
  for (let a = 0; a < 37; a++)
    for (let b = a + 1; b <= 37; b++) {
      let want = 0;
      for (let i = a; i < b; i++) want = Math.max(want, rnd[i]);
      if (peakIn(q, a, b) !== want) bad++;
    }
  ok("peakIn = brute force on every range", bad === 0, bad);
}
// Volume moves the waveform in dB: half volume is -6 dB of a 60 dB scale.
ok("full scale", near(peakHeight(255, 1), 1));
ok("half volume -6 dB", near(peakHeight(255, 0.5), 1 + (20 * Math.log10(0.5)) / 60));
ok("boost raises it", near(peakHeight(200, 2), (200 / 255) * 60 / 60 + (20 * Math.log10(2)) / 60));
ok("boost caps at 1", peakHeight(255, 2) === 1);
ok("gain 0 is flat", peakHeight(255, 0) === 0);
ok("silence is flat", peakHeight(0, 1) === 0);

// A ramp of 10 s where second k has byte (k+1)*20: so a column's byte tells
// which source second it shows.
const ramp = buildPeaks(new Uint8Array(Array.from({ length: 1000 }, (_, i) => (Math.floor(i / 100) + 1) * 20)), 100);
const srcSecond = (h: number) => Math.round((h * 255) / 20) - 1; // at gain 1, height = byte/255
function cols(c: Clip, pxPerSec: number, n: number, x0 = 0, tr = track) {
  const out = new Float32Array(n);
  columnHeights(out, ramp, c, tr, x0, pxPerSec);
  return Array.from(out);
}
{
  const a = cols(clip({}), 1, 10);
  ok("plain: column k shows second k", a.map(srcSecond).join() === "0,1,2,3,4,5,6,7,8,9", a.map(srcSecond));
  const t = cols(clip({ in: 3, out: 6 }), 1, 3);
  ok("trimmed: starts at in", t.map(srcSecond).join() === "3,4,5", t.map(srcSecond));
  const f = cols(clip({ speed: 2 }), 1, 5);
  ok("2x: a column covers 2 s (max is the later one)", f.map(srcSecond).join() === "1,3,5,7,9", f.map(srcSecond));
  const r = cols(clip({ in: 2, out: 5, reversed: true }), 1, 3);
  ok("reversed: plays from out", r.map(srcSecond).join() === "4,3,2", r.map(srcSecond));
  const h = cols(clip({ in: 4, out: 6, hold: true }), 1, 2);
  ok("freeze frame is silent", h.every((v) => v === 0), h);
  const v = cols(clip({ in: 9, volume: 0.5 }), 1, 1);
  ok("volume lowers it", near(v[0], peakHeight(200, 0.5), 1e-6) && v[0] < peakHeight(200, 1), v);
  const m = cols(clip({ muted: true }), 1, 1);
  ok("muted clip still drawn (dimmed by the caller)", m[0] > 0);
  const mt = cols(clip({}), 1, 1, 0, { ...track, muted: true });
  ok("muted track still drawn", mt[0] > 0);
  const fi = cols(clip({ fadeIn: 4 }), 4, 16);
  ok("fade in rises", fi[0] < fi[7] && fi[7] < fi[15], fi);
  const z = cols(clip({}), 100, 3, 512);
  ok("zoomed in: x0 picks the time", z.map(srcSecond).join() === "5,5,5", z.map(srcSecond));
  const kv = cols(clip({ anim: { volume: [{ t: 0, v: 1, ease: "linear" }, { t: 10, v: 0, ease: "linear" }] } }), 1, 10);
  ok("volume keys fade it out", near(kv[0], a[0], 0.02) && kv[9] < a[9] - 0.2 && kv.every((h, i) => h <= a[i] + 1e-6), kv);
  const mute = cols(clip({ speedCurve: { ...presetCurve("bullet", 10), audio: "mute" } }), 10, 100);
  ok("ramp with muted audio is silent where speed isn't 1", mute.some((x) => x === 0));
}

// Perf: a 3-hour recording, whole on a 2000 px timeline and zoomed in.
{
  const n = 3 * 3600 * 100;
  const base = new Uint8Array(n);
  for (let i = 0; i < n; i++) base[i] = (i * 2654435761) >>> 24;
  let t0 = performance.now();
  const p = buildPeaks(base, 100);
  const build = performance.now() - t0;
  const long = clip({ out: 3 * 3600 });
  const out = new Float32Array(2000 * 2); // device pixels at 2x
  t0 = performance.now();
  for (let k = 0; k < 20; k++) columnHeights(out, p, long, track, 0, (2000 * 2) / (3 * 3600));
  const whole = (performance.now() - t0) / 20;
  t0 = performance.now();
  for (let k = 0; k < 20; k++) columnHeights(out, p, long, track, 5_000_000, (2000 * 2 * 60) / (3 * 3600));
  const zoomed = (performance.now() - t0) / 20;
  console.log(`3 h: pyramid ${build.toFixed(1)} ms once; a 4000-column draw ${whole.toFixed(2)} ms whole, ${zoomed.toFixed(2)} ms at 60x zoom`);
  ok("pyramid under 200 ms", build < 200, build);
  ok("whole-view columns under 8 ms", whole < 8, whole);
  ok("zoomed columns under 8 ms", zoomed < 8, zoomed);
}

console.log(`${pass} passed, ${fail} failed`);
if (fail) process.exit(1);
