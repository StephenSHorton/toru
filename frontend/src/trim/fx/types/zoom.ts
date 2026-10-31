import type { Ease } from "@/trim/project";
import { ease } from "@/trim/fx/keyframes";
import type { ShaderModule, Step } from "@/trim/fx/shader";

// zoom: a punch-in (as internal/fx/zoom.go). Over `in` seconds from start
// the picture scales up to scale around centre, holds, then scales back over
// `out`. The export scales the frame to trunc(W·z) × trunc(H·z) (swscale,
// bilinear) and crops a W×H window from it; the pass below reads the same
// window, sampling the source where swscale does.

// zoomAt is the zoom factor at clip-local time t (as Go's zoomExpr).
export function zoomAt(p: Record<string, unknown>, t: number): number {
  const scale = p.scale as number;
  if (!(scale > 1)) return 1;
  const e = p.ease as Ease;
  const s0 = p.start as number;
  const s1 = s0 + (p.in as number);
  const s2 = s1 + (p.hold as number);
  const out = p.out as number;
  const s3 = s2 + out;
  let u: number;
  if (t < s0) u = 0;
  else if (t < s1) u = ease(e, (t - s0) / (p.in as number));
  else if (t < s2) u = 1;
  else if (out > 0 && t < s3) u = ease(e, (s3 - t) / out);
  else u = 0;
  return 1 + (scale - 1) * u;
}

const FRAG = `uniform vec2 u_size;
uniform vec2 u_scaled;
uniform vec2 u_off;
void main() {
  // Where swscale samples the source for this pixel of the scaled frame.
  vec2 p = (floor(v_uv * u_size) + u_off + 0.5) * u_size / u_scaled - 0.5;
  vec2 f = floor(p);
  vec2 k = p - f;
  vec2 t0 = (f + 0.5) * u_texel;
  vec4 a = texture(u_src, t0), b = texture(u_src, t0 + vec2(u_texel.x, 0.0));
  vec4 c = texture(u_src, t0 + vec2(0.0, u_texel.y)), d = texture(u_src, t0 + u_texel);
  o = mix(mix(a, b, k.x), mix(c, d, k.x), k.y);
}`;

// zoomWindow is the pass that shows the W×H window at (ox, oy) of the frame
// scaled to sw × sh; none when that's the frame itself.
export function zoomWindow(w: number, h: number, sw: number, sh: number, ox: number, oy: number): Step[] {
  if (sw === w && sh === h) return [];
  return [{ kind: "pass", id: "zoomWindow", frag: FRAG, uniforms: { u_size: [w, h], u_scaled: [sw, sh], u_off: [ox, oy] } }];
}

// windowAt is the scaled size and crop of a W×H frame zoomed by z towards
// (cx, cy), as the export's scale and crop expressions compute them.
export function windowAt(z: number, cx: number, cy: number, w: number, h: number) {
  const sw = Math.trunc(w * z);
  const sh = Math.trunc(h * z);
  const clip = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, v));
  return { sw, sh, ox: Math.floor(clip(cx * sw - w / 2, 0, sw - w)), oy: Math.floor(clip(cy * sh - h / 2, 0, sh - h)) };
}

const zoom: ShaderModule = {
  type: "zoom",
  steps(p, ctx) {
    const z = zoomAt(p, ctx.t);
    if (z <= 1) return [];
    const [cx, cy] = p.center as [number, number];
    const win = windowAt(z, cx, cy, ctx.w, ctx.h);
    return zoomWindow(ctx.w, ctx.h, win.sw, win.sh, win.ox, win.oy);
  },
};
export default zoom;
