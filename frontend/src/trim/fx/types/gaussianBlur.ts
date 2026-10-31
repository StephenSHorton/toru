import type { ShaderModule } from "@/trim/fx/shader";

// gaussianBlur: ffmpeg gblur with 2 steps. gblur is a recursive filter (a
// causal and an anti-causal pass per step, then a gain), which equals a
// convolution with nu^|n| per step; two steps give the kernel
// nu^|n| * (|n| + (1 + nu²) / (1 - nu²)), sampled here across then down.
// sigma is in pixels of a 1080-line frame (as Go's GblurSigma).

const STEPS = 2;
// Largest radius the shader loops over: past ~4 sigma the kernel is < 1e-3
// of its peak.
export const MAX_GBLUR_RADIUS = 256;

// gblurSigma is sigma in pixels of an h-tall frame, rounded as the export
// writes it (and read as a float, as ffmpeg parses it).
export function gblurSigma(sigma: number, h: number): number {
  if (sigma <= 0 || h <= 0) return 0;
  return Math.fround(Math.round(((sigma * h) / 1080) * 1e4) / 1e4);
}

// gblurNu is gblur's pole for sigma: the root of lambda(1 - nu)² = nu with
// lambda = sigma² / (2 steps) (its set_params).
export function gblurNu(sigma: number): number {
  const lambda = (sigma * sigma) / (2 * STEPS);
  return (1 + 2 * lambda - Math.sqrt(1 + 4 * lambda)) / (2 * lambda);
}

const FRAG = `uniform vec2 u_step;
uniform int u_r;
uniform float u_nu;
uniform float u_c;
void main() {
  vec4 s = texture(u_src, v_uv) * u_c;
  float sw = u_c;
  float p = 1.0;
  for (int i = 1; i <= ${MAX_GBLUR_RADIUS}; i++) {
    if (i > u_r) break;
    p *= u_nu;
    float w = p * (float(i) + u_c);
    s += (texture(u_src, v_uv + u_step * float(i)) + texture(u_src, v_uv - u_step * float(i))) * w;
    sw += 2.0 * w;
  }
  o = s / sw;
}`;

const gaussianBlur: ShaderModule = {
  type: "gaussianBlur",
  steps(p, ctx) {
    const sigma = gblurSigma(p.sigma as number, ctx.h);
    if (sigma < 0.05) return [];
    const nu = gblurNu(sigma);
    const c = (1 + nu * nu) / (1 - nu * nu);
    // Smallest radius whose next tap weighs under 1e-4 of the centre's.
    let r = 1;
    for (let w = nu; r < MAX_GBLUR_RADIUS && w * (r + c) > 1e-4 * c; r++) w *= nu;
    const u = { u_r: { int: r }, u_nu: nu, u_c: c };
    return [
      { kind: "pass", id: "gblur", frag: FRAG, uniforms: { u_step: [1 / ctx.w, 0], ...u } },
      { kind: "pass", id: "gblur", frag: FRAG, uniforms: { u_step: [0, 1 / ctx.h], ...u } },
    ];
  },
};
export default gaussianBlur;
