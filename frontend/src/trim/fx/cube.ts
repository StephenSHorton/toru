import type { ColorFn, RGB } from "@/trim/fx/lut";

// cube.ts reads imported .cube 3D LUTs for the preview: the twin of
// internal/fx/cube.go. sampleCube must keep the same arithmetic (trilinear,
// clamped to the domain and to 0..1), so the lut effect's colour is the same
// on both sides once it's baked into the stack's generated LUT.

export const MAX_CUBE_SIZE = 65;

export type Cube = {
  title: string;
  size: number;
  data: Float64Array; // size³ RGB triples, red fastest, then green, then blue
  min: RGB;
  max: RGB;
};

function triple(f: string[]): RGB {
  if (f.length !== 3) throw new Error("expected three numbers");
  const v = f.map(Number);
  if (v.some((n) => !Number.isFinite(n))) throw new Error("not a number");
  return [v[0], v[1], v[2]];
}

// parseCube reads a .cube file's text (TITLE, DOMAIN_MIN/MAX, comments
// allowed; 1D LUTs refused). Throws with the reason on a bad file.
export function parseCube(text: string): Cube {
  let size = 0;
  let title = "";
  let min: RGB = [0, 0, 0];
  let max: RGB = [1, 1, 1];
  let data: Float64Array | null = null;
  let n = 0;
  for (const raw of text.split(/\r?\n/)) {
    const s = raw.trim();
    if (!s || s[0] === "#") continue;
    const f = s.split(/\s+/);
    const kw = f[0].toUpperCase();
    if (kw === "TITLE") {
      title = s.slice(f[0].length).trim().replace(/^"|"$/g, "");
      continue;
    }
    if (kw === "LUT_1D_SIZE") throw new Error("that's a 1D LUT; only 3D LUTs are supported");
    if (kw === "LUT_3D_SIZE") {
      size = Number(f[1]);
      if (!Number.isInteger(size) || size < 2 || size > MAX_CUBE_SIZE) throw new Error(`LUT_3D_SIZE must be 2..${MAX_CUBE_SIZE}`);
      data = new Float64Array(size * size * size * 3);
      continue;
    }
    if (kw === "DOMAIN_MIN") {
      min = triple(f.slice(1));
      continue;
    }
    if (kw === "DOMAIN_MAX") {
      max = triple(f.slice(1));
      continue;
    }
    if (kw === "LUT_3D_INPUT_RANGE" || kw === "LUT_1D_INPUT_RANGE") {
      const lo = Number(f[1]);
      const hi = Number(f[2]);
      min = [lo, lo, lo];
      max = [hi, hi, hi];
      continue;
    }
    if (!data) {
      if (Number.isNaN(Number(f[0]))) continue;
      throw new Error("colour values before LUT_3D_SIZE");
    }
    if (n >= data.length) throw new Error("too many entries");
    const v = triple(f);
    data[n++] = v[0];
    data[n++] = v[1];
    data[n++] = v[2];
  }
  if (!data) throw new Error("not a .cube LUT (no LUT_3D_SIZE line)");
  if (n !== data.length) throw new Error(`expected ${data.length / 3} entries, found ${n / 3}`);
  for (let k = 0; k < 3; k++) if (!(max[k] > min[k])) throw new Error("DOMAIN_MAX must be above DOMAIN_MIN");
  return { title, size, data, min, max };
}

// sampleCube is the LUT at colour c, trilinearly interpolated (as cube.go
// Cube.Sample).
export function sampleCube(cube: Cube, c: RGB): RGB {
  const n = cube.size;
  const i0 = [0, 0, 0];
  const fr = [0, 0, 0];
  for (let k = 0; k < 3; k++) {
    let x = (c[k] - cube.min[k]) / (cube.max[k] - cube.min[k]);
    if (!(x > 0)) x = 0;
    else if (x > 1) x = 1;
    x *= n - 1;
    let j = Math.floor(x);
    if (j > n - 2) j = n - 2;
    i0[k] = j;
    fr[k] = x - j;
  }
  const d = cube.data;
  const at = (ri: number, gi: number, bi: number, ch: number) => d[((bi * n + gi) * n + ri) * 3 + ch];
  const out: RGB = [0, 0, 0];
  for (let ch = 0; ch < 3; ch++) {
    const c00 = at(i0[0], i0[1], i0[2], ch) * (1 - fr[0]) + at(i0[0] + 1, i0[1], i0[2], ch) * fr[0];
    const c10 = at(i0[0], i0[1] + 1, i0[2], ch) * (1 - fr[0]) + at(i0[0] + 1, i0[1] + 1, i0[2], ch) * fr[0];
    const c01 = at(i0[0], i0[1], i0[2] + 1, ch) * (1 - fr[0]) + at(i0[0] + 1, i0[1], i0[2] + 1, ch) * fr[0];
    const c11 = at(i0[0], i0[1] + 1, i0[2] + 1, ch) * (1 - fr[0]) + at(i0[0] + 1, i0[1] + 1, i0[2] + 1, ch) * fr[0];
    const c0 = c00 * (1 - fr[1]) + c10 * fr[1];
    const c1 = c01 * (1 - fr[1]) + c11 * fr[1];
    let v = c0 * (1 - fr[2]) + c1 * fr[2];
    if (!(v > 0)) v = 0;
    else if (v > 1) v = 1;
    out[ch] = v;
  }
  return out;
}

// cubeFn is the lut effect's colour: clamp(mix(c, cube(c), amount)), the
// same intensity rule as the look effect.
export function cubeFn(cube: Cube, amount: number): ColorFn | null {
  if (amount <= 0) return null;
  return (c) => {
    const o = sampleCube(cube, c);
    if (amount >= 1) return o;
    const m = (k: number) => {
      const v = c[k] + (o[k] - c[k]) * amount;
      return v < 0 ? 0 : v > 1 ? 1 : v;
    };
    return [m(0), m(1), m(2)];
  };
}
