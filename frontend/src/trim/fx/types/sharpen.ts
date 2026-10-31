import type { ShaderModule, Step } from "@/trim/fx/shader";

// sharpen: contrast-adaptive sharpening as ffmpeg's cas filter does it (per
// channel: a cross of 4 neighbours weighted by how much headroom the 3x3
// neighbourhood has; strength 0..1). adjust's sharpen slider uses it too.

const CAS = `uniform float u_peak;
vec3 tap(int x, int y) { return texture(u_src, v_uv + vec2(x, y) * u_texel).rgb; }
void main() {
  vec4 e4 = texture(u_src, v_uv);
  vec3 a = tap(-1, -1), b = tap(0, -1), c = tap(1, -1);
  vec3 d = tap(-1, 0), e = e4.rgb, f = tap(1, 0);
  vec3 g = tap(-1, 1), h = tap(0, 1), i = tap(1, 1);
  vec3 mn = min(min(min(d, e), min(f, b)), h);
  mn += min(mn, min(min(a, c), min(g, i)));
  vec3 mx = max(max(max(d, e), max(f, b)), h);
  mx += max(mx, max(max(a, c), max(g, i)));
  vec3 amp = sqrt(clamp(min(mn, 2.0 - mx) / max(mx, vec3(1e-5)), 0.0, 1.0));
  vec3 w = amp * u_peak;
  o = vec4(clamp((b * w + d * w + f * w + h * w + e) / (1.0 + 4.0 * w), 0.0, 1.0), e4.a);
}`;

// casSteps is the CAS pass at strength s (as Go's casStep); none at 0.
// u_peak follows ffmpeg's cas: -1/lerp(16, 4.01, s).
export function casSteps(s: number): Step[] {
  return s > 0 ? [{ kind: "pass", id: "cas", frag: CAS, uniforms: { u_peak: -1 / (16 - 11.99 * s) } }] : [];
}

const sharpen: ShaderModule = {
  type: "sharpen",
  steps: (p) => casSteps(p.amount as number),
};
export default sharpen;
