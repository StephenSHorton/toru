import type { ShaderModule, Step } from "@/trim/fx/shader";
import { zoomWindow } from "./zoom";

// shake: the picture zoomed in by zoom, moved by a seeded wobble of up to
// amplitude pixels (of a 1080-line frame) and turned by up to rotation
// degrees (as internal/fx/shake.go). Two steps, as the export: the zoom
// window at a position in whole pixels (scale + crop), then ffmpeg rotate,
// whose 16.16 fixed-point maths the rotate pass repeats exactly.

const MINSTD = 2147483647;

// seeded is n numbers in 0..1 from seed (Park–Miller, as Go's seeded; the
// products stay under 2^53, so they're exact).
export function seeded(seed: number, n: number): number[] {
  let s = (Math.abs(Math.trunc(seed)) % (MINSTD - 1)) + 1;
  const out: number[] = [];
  for (let i = 0; i < n; i++) {
    s = (s * 48271) % MINSTD;
    out.push(s / MINSTD);
  }
  return out;
}

// wobbleAt is a smooth, seeded -1..1 motion at f Hz: two sines, the second
// 1.7 times as fast, with phases ph1, ph2 in turns (as Go's wobble).
export function wobbleAt(f: number, ph1: number, ph2: number, t: number): number {
  return 0.6 * Math.sin(2 * Math.PI * f * t + 2 * Math.PI * ph1) + 0.4 * Math.sin(2 * Math.PI * f * 1.7 * t + 2 * Math.PI * ph2);
}

// u_cs is (int)(cos·65536), (int)(sin·65536), exact as floats; the
// integer setup runs here, since the frame's 16.16 offsets aren't.
const ROTATE = `uniform vec2 u_cs;
int half_(int a) { return a < 0 ? -((-a) / 2) : a / 2; } // C's a / 2
vec4 px(int x, int y) { return floor(texelFetch(u_src, ivec2(x, y), 0) * 255.0 + 0.5); }
void main() {
  ivec2 size = textureSize(u_src, 0);
  int w = size.x, h = size.y, c = int(u_cs.x), s = int(u_cs.y);
  ivec2 ij = ivec2(floor(v_uv * vec2(size)));
  int x = half_(-(h - 1) * s) + half_(-(w - 1) * c) + 32768 * (w - 1) + ij.y * s + ij.x * c;
  int y = half_(-(h - 1) * c) + half_((w - 1) * s) + 32768 * (h - 1) + ij.y * c - ij.x * s;
  int x1 = x >> 16, y1 = y >> 16;
  if (x1 < -1 || x1 > w || y1 < -1 || y1 > h) { o = vec4(0.0); return; }
  int ix = clamp(x1, 0, w - 1), iy = clamp(y1, 0, h - 1);
  int ix1 = min(ix + 1, w - 1), iy1 = min(iy + 1, h - 1);
  float fx = float(x & 0xFFFF), fy = float(y & 0xFFFF);
  vec4 s0 = (65536.0 - fx) * px(ix, iy) + fx * px(ix1, iy);
  vec4 s1 = (65536.0 - fx) * px(ix, iy1) + fx * px(ix1, iy1);
  o = floor(((65536.0 - fy) * s0 + fy * s1) / 4294967296.0) / 255.0;
}`;

// rotateSteps is ffmpeg rotate by angle radians (output the input's size,
// transparent fill).
export function rotateSteps(angle: number): Step[] {
  const c = Math.trunc(Math.cos(angle) * 65536);
  const s = Math.trunc(Math.sin(angle) * 65536);
  if (s === 0 && c === 65536) return [];
  return [{ kind: "pass", id: "rotate", frag: ROTATE, uniforms: { u_cs: [c, s] } }];
}

const shake: ShaderModule = {
  type: "shake",
  steps(p, ctx) {
    const amp = ((p.amplitude as number) * ctx.h) / 1080;
    const rot = p.rotation as number;
    if (amp <= 0 && rot <= 0) return [];
    const f = p.frequency as number;
    const r = seeded(p.seed as number, 6);
    const z = p.zoom as number;
    const sw = Math.trunc(ctx.w * z);
    const sh = Math.trunc(ctx.h * z);
    const out: Step[] = [];
    if (sw > ctx.w || sh > ctx.h) {
      const pos = (size: number, n: number, ph1: number, ph2: number) => {
        const v = (size - n) / 2 + (amp > 0 ? amp * wobbleAt(f, ph1, ph2, ctx.t) : 0);
        return Math.floor(Math.max(0, Math.min(size - n, v)));
      };
      out.push(...zoomWindow(ctx.w, ctx.h, sw, sh, pos(sw, ctx.w, r[0], r[1]), pos(sh, ctx.h, r[2], r[3])));
    }
    if (rot > 0) out.push(...rotateSteps(((rot * Math.PI) / 180) * wobbleAt(f, r[4], r[5], ctx.t)));
    return out;
  },
};
export default shake;
