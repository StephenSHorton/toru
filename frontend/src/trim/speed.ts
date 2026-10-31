import { MAX_CURVE_SPEED, MIN_CURVE_SPEED, type Clip, type SpeedCurve } from "@/trim/project";

// speed.ts is the maths of speed ramps (docs/video-editor-v3.md, Speed
// ramps). internal/vid/speed.go is the same for the export, and
// internal/vid/testdata/speed_golden.json holds cases both must reproduce
// (frontend/scripts/speed-golden.ts writes it).
//
// A curve's speed is piecewise LINEAR in source time s (seconds from the
// clip's in point). Between points (s0, v0) and (s1, v1), with
// k = (v1 - v0) / (s1 - s0), the output (timeline) time spent is
//   T(s) = T0 + ln((v0 + k(s - s0)) / v0) / k      (or (s - s0) / v0 when k = 0)
// and the inverse is
//   s(T) = s0 + v0 (e^{k(T - T0)} - 1) / k          (or s0 + v0 (T - T0)).
// Closed form both ways: the preview maps timeline → source with s(T), the
// export writes T(s) into setpts.

type Pt = { s: number; v: number };

const clampV = (v: number) => Math.max(MIN_CURVE_SPEED, Math.min(MAX_CURVE_SPEED, Number.isFinite(v) ? v : 1));
// |k| below this counts as constant speed (avoids ln(..)/k blowing up).
const FLAT = 1e-9;

// normCurve fits a curve to a source range of srcLen seconds: points sorted,
// speeds clamped to 0.1..10, inside 0..srcLen, with points at both ends
// (holding the nearest speed). Fewer than one usable point gives 1x.
export function normCurve(c: SpeedCurve, srcLen: number): SpeedCurve {
  const len = Math.max(0, srcLen);
  const pts = c.points
    .filter((p) => Number.isFinite(p.s))
    .map((p) => ({ s: Math.max(0, Math.min(len, p.s)), v: clampV(p.v) }))
    .sort((a, b) => a.s - b.s);
  const out: Pt[] = [];
  for (const p of pts) if (!out.length || p.s - out[out.length - 1].s > 1e-6) out.push(p);
  if (!out.length) out.push({ s: 0, v: 1 });
  if (out[0].s > 0) out.unshift({ s: 0, v: out[0].v });
  if (out[out.length - 1].s < len) out.push({ s: len, v: out[out.length - 1].v });
  if (out.length === 1) out.push({ s: len, v: out[0].v });
  const res: SpeedCurve = { points: out, audio: c.audio === "mute" ? "mute" : "follow" };
  if (c.preset) res.preset = c.preset;
  return res;
}

// segment time spent going from a.s to s (a.s <= s <= b.s).
function segT(a: Pt, b: Pt, s: number): number {
  const ds = s - a.s;
  if (ds <= 0) return 0;
  const k = (b.v - a.v) / (b.s - a.s);
  if (Math.abs(k) < FLAT) return ds / a.v;
  return Math.log((a.v + k * ds) / a.v) / k;
}

// outAt is the output time at source time s (0..last point).
export function outAt(c: SpeedCurve, s: number): number {
  const p = c.points;
  let T = 0;
  for (let i = 0; i < p.length - 1; i++) {
    if (s <= p[i + 1].s) return T + segT(p[i], p[i + 1], Math.max(p[i].s, s));
    T += segT(p[i], p[i + 1], p[i + 1].s);
  }
  return T;
}

// outLength is how long the whole curve plays on the timeline.
export function outLength(c: SpeedCurve): number {
  const p = c.points;
  return p.length ? outAt(c, p[p.length - 1].s) : 0;
}

// srcAt is the source time shown at output time T (the inverse of outAt),
// held at the ends.
export function srcAt(c: SpeedCurve, T: number): number {
  const p = c.points;
  if (!p.length) return 0;
  if (T <= 0) return p[0].s;
  let T0 = 0;
  for (let i = 0; i < p.length - 1; i++) {
    const a = p[i];
    const b = p[i + 1];
    const len = segT(a, b, b.s);
    if (T <= T0 + len || i === p.length - 2) {
      const dT = Math.min(T - T0, len);
      const k = (b.v - a.v) / (b.s - a.s);
      const s = Math.abs(k) < FLAT ? a.s + a.v * dT : a.s + (a.v * (Math.exp(k * dT) - 1)) / k;
      return Math.min(b.s, s);
    }
    T0 += len;
  }
  return p[p.length - 1].s;
}

// speedAt is the speed at source time s (held past the ends).
export function speedAt(c: SpeedCurve, s: number): number {
  const p = c.points;
  if (!p.length) return 1;
  if (s <= p[0].s) return p[0].v;
  for (let i = 0; i < p.length - 1; i++) {
    if (s <= p[i + 1].s) {
      const a = p[i];
      const b = p[i + 1];
      return a.v + ((b.v - a.v) * (s - a.s)) / (b.s - a.s);
    }
  }
  return p[p.length - 1].v;
}

// subCurve is the part of a curve between source times s0 and s1, rebased
// to start at 0 (for splits and trims). s0 < 0 or s1 past the end extend
// the curve at its end speeds.
export function subCurve(c: SpeedCurve, s0: number, s1: number): SpeedCurve {
  const pts: Pt[] = [{ s: 0, v: speedAt(c, s0) }];
  for (const p of c.points) if (p.s > s0 + 1e-6 && p.s < s1 - 1e-6) pts.push({ s: p.s - s0, v: p.v });
  pts.push({ s: s1 - s0, v: speedAt(c, s1) });
  const res: SpeedCurve = { points: pts, audio: c.audio };
  if (c.preset) res.preset = c.preset;
  return res;
}

// Audio during a ramp follows the speed (atempo) only within 0.25..4x;
// outside it, and everywhere off 1x with audio "mute", it's silent.
export const AUDIO_MIN_SPEED = 0.25;
export const AUDIO_MAX_SPEED = 4;
export function audibleAtSpeed(audio: SpeedCurve["audio"], v: number): boolean {
  if (audio === "mute") return Math.abs(v - 1) < 1e-6;
  return v >= AUDIO_MIN_SPEED - 1e-9 && v <= AUDIO_MAX_SPEED + 1e-9;
}
export function audioAudibleAt(c: SpeedCurve, s: number): boolean {
  return audibleAtSpeed(c.audio, speedAt(c, s));
}

// --- Clips -------------------------------------------------------------------
//
// A clip's "local" times: u is source seconds from where playback starts
// (in, or out for a reversed clip, counting down), T is timeline seconds
// from the clip's start. A reversed clip's curve applies to u, so the curve
// runs over the reversed footage.

// clipCurve is a clip's speed curve fitted to its range, or undefined when
// it plays at a constant speed (or is a freeze frame).
export function clipCurve(c: Clip): SpeedCurve | undefined {
  if (!c.speedCurve || c.hold) return undefined;
  return normCurve(c.speedCurve, c.out - c.in);
}

const constSpeed = (c: Clip) => (c.hold ? 1 : c.speed > 0 ? c.speed : 1);

// clipOutLength is the clip's timeline length.
export function clipOutLength(c: Clip): number {
  const cv = clipCurve(c);
  return cv ? outLength(cv) : (c.out - c.in) / constSpeed(c);
}

// localOut is T at u. Past the clip's range it carries on at the end speed,
// so trims can work out how far an edge may move.
export function localOut(c: Clip, u: number): number {
  const cv = clipCurve(c);
  if (!cv) return u / constSpeed(c);
  const len = c.out - c.in;
  if (u < 0) return u / cv.points[0].v;
  if (u > len) return outLength(cv) + (u - len) / cv.points[cv.points.length - 1].v;
  return outAt(cv, u);
}

// localSrc is u at T (localOut the other way round, extended the same way).
export function localSrc(c: Clip, T: number): number {
  const cv = clipCurve(c);
  if (!cv) return T * constSpeed(c);
  const L = outLength(cv);
  if (T < 0) return T * cv.points[0].v;
  if (T > L) return c.out - c.in + (T - L) * cv.points[cv.points.length - 1].v;
  return srcAt(cv, T);
}

// rateAt is how fast the source plays at u (0 for a freeze frame).
export function rateAt(c: Clip, u: number): number {
  if (c.hold) return 0;
  const cv = clipCurve(c);
  return cv ? speedAt(cv, u) : constSpeed(c);
}

// RAMP_CHUNK is how long (source s) each piece of a ramped clip's audio is
// in the export (speed.go rampAudioChunks): each piece is heard or silent
// as a whole.
export const RAMP_CHUNK = 0.25;

// clipAudible says whether the speed rule lets the clip's sound play at u:
// a constant speed always does (atempo covers 0.25..4), a freeze never. A
// curve decides per export chunk, by the chunk's middle speed and its
// stretch, exactly as speed.go does.
export function clipAudible(c: Clip, u: number): boolean {
  if (c.hold) return false;
  const cv = clipCurve(c);
  if (!cv) return true;
  const end = cv.points[cv.points.length - 1].s;
  if (end <= 0) return false;
  const n = Math.max(1, Math.ceil(end / RAMP_CHUNK - 1e-9));
  const i = Math.max(0, Math.min(n - 1, Math.floor((u / end) * n)));
  const s0 = (i * end) / n;
  const s1 = ((i + 1) * end) / n;
  const dT = outAt(cv, s1) - outAt(cv, s0);
  if (dT <= 0) return false;
  return audibleAtSpeed(cv.audio, speedAt(cv, (s0 + s1) / 2)) && audibleAtSpeed(cv.audio, (s1 - s0) / dT);
}

// toSource is the source time shown at u; fromSource the other way.
export const toSource = (c: Clip, u: number) => (c.reversed ? c.out - u : c.in + u);
export const fromSource = (c: Clip, src: number) => (c.reversed ? c.out - src : src - c.in);

// retime is the clip's range and curve cut down (or out) to u0..u1, in the
// direction it plays: what trims and splits keep.
export function retime(c: Clip, u0: number, u1: number): Pick<Clip, "in" | "out" | "speedCurve"> {
  const a = toSource(c, u0);
  const b = toSource(c, u1);
  const out: Pick<Clip, "in" | "out" | "speedCurve"> = { in: Math.min(a, b), out: Math.max(a, b) };
  const cv = clipCurve(c);
  if (cv) out.speedCurve = subCurve(cv, u0, u1);
  return out;
}

// --- Presets ---------------------------------------------------------------

export type RampPreset = "montage" | "hero" | "bullet" | "jump" | "flashIn" | "flashOut";
export const RAMP_PRESETS: { id: RampPreset; label: string }[] = [
  { id: "montage", label: "Montage" },
  { id: "hero", label: "Hero" },
  { id: "bullet", label: "Bullet" },
  { id: "jump", label: "Jump Cut" },
  { id: "flashIn", label: "Flash In" },
  { id: "flashOut", label: "Flash Out" },
];

// Each preset as (position 0..1 of the source range, speed). Slow parts
// hold for a stretch, so the moment reads before it speeds up again.
const SHAPES: Record<RampPreset, [number, number][]> = {
  montage: [[0, 1], [0.2, 3], [0.45, 0.3], [0.55, 0.3], [0.8, 3], [1, 1]],
  hero: [[0, 1], [0.35, 1], [0.45, 0.2], [0.65, 0.2], [0.75, 1], [1, 1]],
  bullet: [[0, 1], [0.4, 0.1], [0.6, 0.1], [1, 1]],
  jump: [[0, 1], [0.3, 1], [0.35, 6], [0.65, 6], [0.7, 1], [1, 1]],
  flashIn: [[0, 4], [0.3, 1], [1, 1]],
  flashOut: [[0, 1], [0.7, 1], [1, 4]],
};

export const isRampPreset = (id: string): id is RampPreset => id in SHAPES;

// presetCurve is a preset laid over a source range of srcLen seconds.
export function presetCurve(id: RampPreset, srcLen: number, audio: SpeedCurve["audio"] = "follow"): SpeedCurve {
  return normCurve({ points: SHAPES[id].map(([f, v]) => ({ s: f * srcLen, v })), audio, preset: id }, srcLen);
}
