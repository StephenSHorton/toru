import type { ShaderModule } from "@/trim/fx/shader";

// rgbSplit: ffmpeg rgbashift. Red moves by whole pixels one way and blue the
// other, green and alpha stay; taps past the edge repeat the edge pixel
// (edge=smear). amount is in pixels of a 1080-line frame; angle 0 moves red
// right.

// roundAway rounds halves away from zero, as Go's math.Round.
export function roundAway(v: number): number {
  return Math.sign(v) * Math.round(Math.abs(v));
}

// rgbSplitShift is red's shift in whole pixels on an h-tall frame (as Go's
// RGBSplitShift).
export function rgbSplitShift(amount: number, angle: number, h: number): [number, number] {
  const d = (amount * h) / 1080;
  const a = (angle * Math.PI) / 180;
  return [roundAway(d * Math.cos(a)) || 0, roundAway(d * Math.sin(a)) || 0];
}

const FRAG = `uniform vec2 u_shift;
void main() {
  vec4 c = texture(u_src, v_uv);
  c.r = texture(u_src, v_uv - u_shift * u_texel).r;
  c.b = texture(u_src, v_uv + u_shift * u_texel).b;
  o = c;
}`;

const rgbSplit: ShaderModule = {
  type: "rgbSplit",
  steps(p, ctx) {
    const [dx, dy] = rgbSplitShift(p.amount as number, p.angle as number, ctx.h);
    if (!dx && !dy) return [];
    return [{ kind: "pass", id: "rgbSplit", frag: FRAG, uniforms: { u_shift: [dx, dy] } }];
  },
};
export default rgbSplit;
