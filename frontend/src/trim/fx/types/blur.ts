import type { ShaderModule } from "@/trim/fx/shader";

// blur: v2's box blur (ffmpeg avgblur), across then down. amount is the
// radius at 1080 lines, scaled to the frame's height.

// Largest radius the shader loops over (amount 20 at 2160 lines is 40).
export const MAX_BLUR_RADIUS = 64;

// blurRadius is the radius in pixels of an h-tall frame (as Go's BlurRadius).
export function blurRadius(amount: number, h: number): number {
  if (amount <= 0 || h <= 0) return 0;
  return Math.min(Math.round((amount * h) / 1080), Math.max(0, Math.floor(h / 2) - 1), MAX_BLUR_RADIUS);
}

const FRAG = `uniform vec2 u_step;
uniform int u_r;
void main() {
  vec4 s = vec4(0.0);
  for (int i = -${MAX_BLUR_RADIUS}; i <= ${MAX_BLUR_RADIUS}; i++) {
    if (i < -u_r || i > u_r) continue;
    s += texture(u_src, v_uv + u_step * float(i));
  }
  o = s / float(2 * u_r + 1);
}`;

const blur: ShaderModule = {
  type: "blur",
  steps(p, ctx) {
    const r = blurRadius(p.amount as number, ctx.h);
    if (r <= 0) return [];
    return [
      { kind: "pass", id: "boxBlur", frag: FRAG, uniforms: { u_step: [1 / ctx.w, 0], u_r: { int: r } } },
      { kind: "pass", id: "boxBlur", frag: FRAG, uniforms: { u_step: [0, 1 / ctx.h], u_r: { int: r } } },
    ];
  },
};
export default blur;
