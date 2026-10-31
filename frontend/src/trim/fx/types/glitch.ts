import type { ShaderModule, Step } from "@/trim/fx/shader";
import { rgbShiftSteps, roundAway } from "./rgbSplit";

// glitch: bursts in time slots of 1/rate s (as internal/fx/glitch.go); a
// slot bursts when its first random is under density. During a burst: red
// and blue split by shift px, up to six horizontal slices moved sideways by
// up to sliceShift px (drawn over the frame in order, as the export's
// overlays), then noise. Pixel sizes are in pixels of a 1080-line frame.
// The noise is random on both sides, so it can't match pixel for pixel;
// everything else does.

const MINSTD = 2147483647;

// slotRand is the k-th 0..1 random of the slot t falls in (as Go's
// slotRandExpr; every product stays under 2^53, so it's exact).
export function slotRand(rate: number, seed: number, k: number, t: number): number {
  const mix = Math.floor(t * rate) * 7919 + (Math.trunc(seed) * 104729 + k * 15485863 + 1);
  return (((mix * 48271) % MINSTD) * 48271 % MINSTD) / MINSTD;
}

// glitchSlices is the number of slices drawn (as Go's GlitchSlices).
export function glitchSlices(p: Record<string, unknown>): number {
  return Math.max(0, Math.min(6, roundAway(p.slices as number)));
}

const glitchPx = (v: number, h: number) => roundAway((v * h) / 1080);

// u_b<k> is slice k's (top row, x offset); later slices draw over earlier
// ones, "over" with straight alpha (ffmpeg's overlay blends partly
// transparent pixels a little differently).
const SLICES = `uniform int u_n;
uniform float u_bh;
uniform vec2 u_b0, u_b1, u_b2, u_b3, u_b4, u_b5;
vec4 over(vec4 s, vec4 d) {
  float a = s.a + d.a * (1.0 - s.a);
  return a > 0.0 ? vec4((s.rgb * s.a + d.rgb * d.a * (1.0 - s.a)) / a, a) : vec4(0.0);
}
void main() {
  ivec2 size = textureSize(u_src, 0);
  vec2 ij = floor(v_uv * vec2(size));
  vec4 c = texelFetch(u_src, ivec2(ij), 0);
  vec2 b[6] = vec2[6](u_b0, u_b1, u_b2, u_b3, u_b4, u_b5);
  for (int k = 0; k < 6; k++) {
    if (k >= u_n) break;
    if (ij.y < b[k].x || ij.y >= b[k].x + u_bh) continue;
    float sx = ij.x - b[k].y;
    if (sx >= 0.0 && sx < float(size.x)) c = over(texelFetch(u_src, ivec2(int(sx), int(ij.y)), 0), c);
  }
  o = c;
}`;

const NOISE = `uniform float $s;
uniform float $seed;
vec4 $fn(vec4 c, vec2 uv) {
  vec3 n = fract(sin(vec3(dot(uv, vec2(12.9898, 78.233)), dot(uv, vec2(39.3468, 11.135)), dot(uv, vec2(73.156, 52.235))) * 43758.5453 + $seed) * 4.7);
  return vec4(clamp(c.rgb + (n - 0.5) * $s, 0.0, 1.0), c.a);
}`;

const glitch: ShaderModule = {
  type: "glitch",
  steps(p, ctx) {
    const density = p.density as number;
    if (!(density > 0)) return [];
    const rate = p.rate as number;
    const seed = p.seed as number;
    const r = (k: number) => slotRand(rate, seed, k, ctx.t);
    if (!(r(0) < density)) return [];
    const out: Step[] = [...rgbShiftSteps(glitchPx(p.shift as number, ctx.h), 0)];
    const n = glitchSlices(p);
    const bh = Math.max(1, roundAway((p.sliceSize as number) * ctx.h));
    const off = glitchPx(p.sliceShift as number, ctx.h);
    if (n > 0 && off > 0 && bh < ctx.h) {
      const u: Record<string, number[]> = {};
      for (let k = 0; k < 6; k++) u[`u_b${k}`] = k < n ? [Math.floor(r(1 + 2 * k) * (ctx.h - bh)), Math.floor((r(2 + 2 * k) * 2 - 1) * off)] : [0, 0];
      out.push({ kind: "pass", id: "glitchSlices", frag: SLICES, uniforms: { u_n: { int: n }, u_bh: bh, ...u } });
    }
    const s = Math.round((p.noise as number) * 40);
    if (s > 0) out.push({ kind: "pixel", id: "glitchNoise", glsl: NOISE, uniforms: { $s: s / 255, $seed: Math.floor(ctx.t * 60) % 997 } });
    return out;
  },
};
export default glitch;
