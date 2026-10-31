import type { ShaderModule } from "@/trim/fx/shader";

// flash: the picture mixes towards a colour at start, then back over
// duration: k = intensity·(1 − (t − start)/duration)² (as
// internal/fx/flash.go, which does it with geq). Alpha is kept.

// flashAt is how far the picture is mixed towards the colour at clip-local
// time t (0 outside the flash).
export function flashAt(p: Record<string, unknown>, t: number): number {
  const s0 = p.start as number;
  const d = p.duration as number;
  if (t < s0 || t > s0 + d) return 0;
  return (p.intensity as number) * (1 - (t - s0) / d) ** 2;
}

const GLSL = `uniform vec3 $color;
uniform float $k;
vec4 $fn(vec4 c, vec2 uv) { return vec4(mix(c.rgb, $color, $k), c.a); }`;

const flash: ShaderModule = {
  type: "flash",
  steps(p, ctx) {
    const k = flashAt(p, ctx.t);
    if (k <= 0) return [];
    const n = parseInt((p.color as string).slice(1), 16);
    const color = [((n >> 16) & 255) / 255, ((n >> 8) & 255) / 255, (n & 255) / 255];
    return [{ kind: "pixel", id: "flash", glsl: GLSL, uniforms: { $color: color, $k: k } }];
  },
};
export default flash;
