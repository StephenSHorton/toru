import type { Filters } from "@/trim/project";
import type { Resolved } from "@/trim/fx/registry";

// lut.ts is the colour maths of the v3 editor (docs/video-editor-v3.md,
// Decisions): every per-pixel colour effect (adjust's colour part, look) is a
// function on RGB, and each run of them in a stack compiles into ONE 33³ 3D
// LUT. The preview samples it with a sampler3D (renderer.ts); the export
// writes the same table as a .cube for ffmpeg's lut3d (internal/fx, C3, must
// produce the same numbers: internal/fx/testdata/lut_golden.json holds cases
// both sides check, frontend/scripts/fx-lut-check.ts writes and checks it).
//
// Keep the maths plain float64 arithmetic in a fixed order, so Go's twin
// gets the same values. Every effect clamps its output to 0..1, as v2 and the
// ffmpeg chains do (frontend/src/trim/migrate.ts relies on it).

export const LUT_SIZE = 33;

export type RGB = [number, number, number];
// ColorFn maps a colour (0..1 per channel) to its new value. It may return
// values outside 0..1 only where its effect says so (see colorFns).
export type ColorFn = (c: RGB) => RGB;

const clamp01 = (n: number) => (n < 0 ? 0 : n > 1 ? 1 : n);
const clampRGB = (c: RGB): RGB => [clamp01(c[0]), clamp01(c[1]), clamp01(c[2])];
const LUMA = [0.299, 0.587, 0.114] as const;
const luma = (c: RGB) => LUMA[0] * c[0] + LUMA[1] * c[1] + LUMA[2] * c[2];
const smoothstep = (a: number, b: number, x: number) => {
  const t = clamp01((x - a) / (b - a));
  return t * t * (3 - 2 * t);
};

// The looks as RGB matrices, row-major (out.r = m[0]*r + m[1]*g + m[2]*b ...).
// Same numbers as LookMatrices in internal/vid/effects.go (v2).
export const LOOK_MATRICES: Record<Exclude<Filters["look"], "none">, number[]> = {
  bw: [0.299, 0.587, 0.114, 0.299, 0.587, 0.114, 0.299, 0.587, 0.114],
  sepia: [0.393, 0.769, 0.189, 0.349, 0.686, 0.168, 0.272, 0.534, 0.131],
  vintage: [0.62, 0.32, 0.06, 0.22, 0.68, 0.1, 0.18, 0.28, 0.54],
  cool: [0.9, 0.0, 0.1, 0.0, 0.97, 0.08, 0.0, 0.05, 1.1],
  warm: [1.1, 0.05, 0.0, 0.03, 1.0, 0.0, 0.0, 0.0, 0.85],
};

// satMatrix is v2's saturation as an RGB matrix (around Rec. 601 luma).
export function satMatrix(s: number): number[] {
  const m: number[] = [];
  for (let r = 0; r < 3; r++) for (let c = 0; c < 3; c++) m.push((1 - s) * LUMA[c] + (r === c ? s : 0));
  return m;
}

const mul = (m: number[], c: RGB): RGB => [
  m[0] * c[0] + m[1] * c[1] + m[2] * c[2],
  m[3] * c[0] + m[4] * c[1] + m[5] * c[2],
  m[6] * c[0] + m[7] * c[1] + m[8] * c[2],
];

// --- v2 helpers (kept for frontend/scripts/project-check.ts) --------------

const clampN = (n: number, lo: number, hi: number) => (Number.isNaN(n) ? lo : Math.max(lo, Math.min(hi, n)));

// normFilters clamps v2 filters to their ranges (as v2's effects.go).
export function normFilters(f: Filters): Filters {
  return {
    brightness: clampN(f.brightness, -1, 1),
    contrast: clampN(f.contrast, 0, 2),
    saturation: clampN(f.saturation, 0, 3),
    look: f.look,
    blur: clampN(f.blur, 0, 20),
  };
}

// colorMatrix is v2's saturation then look as one RGB matrix (look · sat).
export function colorMatrix(f: Filters): number[] {
  const sat = satMatrix(f.saturation);
  if (f.look === "none") return sat;
  const look = LOOK_MATRICES[f.look];
  const m = new Array(9).fill(0);
  for (let r = 0; r < 3; r++) for (let c = 0; c < 3; c++) for (let k = 0; k < 3; k++) m[r * 3 + c] += look[r * 3 + k] * sat[k * 3 + c];
  return m;
}

// --- colour effects -------------------------------------------------------

// kelvinRGB is the colour of light at temperature k (Kelvin), 0..255 per
// channel (Tanner Helland's fit of the black-body curve).
function kelvinRGB(k: number): RGB {
  const t = k / 100;
  const r = t <= 66 ? 255 : 329.698727446 * Math.pow(t - 60, -0.1332047592);
  const g = t <= 66 ? 99.4708025861 * Math.log(t) - 161.1195681661 : 288.1221695283 * Math.pow(t - 60, -0.0755148492);
  const b = t >= 66 ? 255 : t <= 19 ? 0 : 138.5177312231 * Math.log(t - 10) - 305.0447927307;
  return [Math.max(0, Math.min(255, r)), Math.max(0, Math.min(255, g)), Math.max(0, Math.min(255, b))];
}

// whiteBalance is the per-channel gain for a light temperature (6500 K is
// neutral) and a tint (-1 green .. 1 magenta). The gain is the square root
// of the light's colour relative to 6500 K (gentler than the raw ratio),
// scaled so it keeps luma.
export function whiteBalance(kelvin: number, tint: number): RGB {
  const n = kelvinRGB(6500);
  const l = kelvinRGB(kelvin);
  const g: RGB = [Math.sqrt(l[0] / n[0]), Math.sqrt(l[1] / n[1]), Math.sqrt(l[2] / n[2])];
  const m = tint * 0.2;
  g[0] *= 1 + m / 2;
  g[1] *= 1 - m;
  g[2] *= 1 + m / 2;
  const y = luma(g);
  return [g[0] / y, g[1] / y, g[2] / y];
}

const num = (p: Resolved, id: string) => p[id] as number;

// adjustFn is the adjust effect's colour (everything but sharpen):
//   1. white balance (temperature, tint), exposure (2^EV in linear light,
//      gamma 2.2), saturation
//      (v2's matrix), vibrance; then clamp
//   2. whites/blacks (levels), shadows/highlights, contrast/brightness
//      (v2's (c - 0.5) * contrast + 0.5 + brightness), fade; then clamp
// Steps at their defaults are skipped, so a migrated v2 adjust is exactly
// v2's clamp(sat · c), then clamp(contrast/brightness).
export function adjustFn(p: Resolved): ColorFn | null {
  const ev = num(p, "exposure");
  const contrast = num(p, "contrast");
  const brightness = num(p, "brightness");
  const highlights = num(p, "highlights");
  const shadows = num(p, "shadows");
  const whites = num(p, "whites");
  const blacks = num(p, "blacks");
  const kelvin = num(p, "temperature");
  const tint = num(p, "tint");
  const sat = num(p, "saturation");
  const vib = num(p, "vibrance");
  const fade = num(p, "fade");
  const wb = kelvin !== 6500 || tint !== 0 ? whiteBalance(kelvin, tint) : null;
  const gain = ev !== 0 ? Math.pow(2, ev) : 1;
  const satM = sat !== 1 ? satMatrix(sat) : null;
  const levels = whites !== 0 || blacks !== 0;
  const lo = -blacks * 0.1;
  const hi = 1 - whites * 0.1;
  const tone = highlights !== 0 || shadows !== 0;
  const cb = contrast !== 1 || brightness !== 0;
  if (!wb && gain === 1 && !satM && vib === 0 && !levels && !tone && !cb && fade === 0) return null;
  return (c) => {
    let o: RGB = c;
    if (wb) o = [o[0] * wb[0], o[1] * wb[1], o[2] * wb[2]];
    if (gain !== 1) {
      // In linear light (gamma 2.2), as a camera's exposure works.
      const e = (v: number) => Math.pow(Math.pow(Math.max(v, 0), 2.2) * gain, 1 / 2.2);
      o = [e(o[0]), e(o[1]), e(o[2])];
    }
    if (satM) o = mul(satM, o);
    if (vib !== 0) {
      const mx = Math.max(o[0], o[1], o[2]);
      const mn = Math.min(o[0], o[1], o[2]);
      const k = 1 + vib * (1 - clamp01(mx - mn));
      const y = luma(o);
      o = [y + (o[0] - y) * k, y + (o[1] - y) * k, y + (o[2] - y) * k];
    }
    o = clampRGB(o);
    if (levels) o = [(o[0] - lo) / (hi - lo), (o[1] - lo) / (hi - lo), (o[2] - lo) / (hi - lo)];
    if (tone) {
      const y = luma(o);
      const d = shadows * 0.25 * (1 - smoothstep(0, 0.6, y)) + highlights * 0.25 * smoothstep(0.4, 1, y);
      o = [o[0] + d, o[1] + d, o[2] + d];
    }
    if (cb) o = [(o[0] - 0.5) * contrast + 0.5 + brightness, (o[1] - 0.5) * contrast + 0.5 + brightness, (o[2] - 0.5) * contrast + 0.5 + brightness];
    if (fade !== 0) {
      const k = 1 - fade * 0.2;
      const lift = fade * 0.12;
      o = [lift + o[0] * k, lift + o[1] * k, lift + o[2] * k];
    }
    return clampRGB(o);
  };
}

// lookFn is the look effect: clamp(mix(c, look · sat · c, amount)).
// With amount 1 that is v2's clamp(look · saturation).
export function lookFn(p: Resolved): ColorFn | null {
  const look = p.look as Filters["look"];
  const sat = num(p, "saturation");
  const amount = num(p, "amount");
  const m = look !== "none" ? LOOK_MATRICES[look] : null;
  const satM = sat !== 1 ? satMatrix(sat) : null;
  if ((!m && !satM) || amount === 0) return null;
  return (c) => {
    let o = satM ? mul(satM, c) : c;
    if (m) o = mul(m, o);
    if (amount !== 1) o = [c[0] + (o[0] - c[0]) * amount, c[1] + (o[1] - c[1]) * amount, c[2] + (o[2] - c[2]) * amount];
    return clampRGB(o);
  };
}

// generateLut runs every grid colour through fns in order: a LUT_SIZE³ table,
// RGB triples with red changing fastest, then green, then blue (the .cube
// order). Each fn clamps its own output.
export function generateLut(fns: ColorFn[], n = LUT_SIZE): Float64Array {
  const out = new Float64Array(n * n * n * 3);
  let i = 0;
  for (let b = 0; b < n; b++) {
    for (let g = 0; g < n; g++) {
      for (let r = 0; r < n; r++) {
        let c: RGB = [r / (n - 1), g / (n - 1), b / (n - 1)];
        for (const f of fns) c = f(c);
        out[i++] = c[0];
        out[i++] = c[1];
        out[i++] = c[2];
      }
    }
  }
  return out;
}

// cubeText is a LUT as a .cube file (6 decimals), what lut3d reads.
export function cubeText(lut: Float64Array, n = LUT_SIZE): string {
  const lines = [`LUT_3D_SIZE ${n}`];
  for (let i = 0; i < lut.length; i += 3) lines.push(`${lut[i].toFixed(6)} ${lut[i + 1].toFixed(6)} ${lut[i + 2].toFixed(6)}`);
  return lines.join("\n") + "\n";
}
