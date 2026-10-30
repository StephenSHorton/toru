import type { ShaderModule } from "@/trim/fx/shader";

// chromaKey: ffmpeg chromakey. Each pixel's U/V (Rec. 601, limited range,
// 0..255) against the key colour's, averaged over the 3x3 neighbourhood;
// alpha *= clamp((diff - similarity) / blend). Colour is left alone.

// keyUV is a "#rrggbb" key colour's U and V the way ffmpeg chromakey reads
// it: Rec. 601 FULL range (its own RGB_TO_U/V), on 0..255, while the frame's
// pixels are limited range (uvOf in the shader). Unreadable colours mean
// green.
export function keyUV(hex: string): [number, number] {
  let h = hex.trim().replace(/^#/, "");
  if (h.length === 3) h = h.replace(/./g, "$&$&");
  const n = /^[0-9a-f]{6}$/i.test(h) ? parseInt(h, 16) : 0x00ff00;
  const r = (n >> 16) & 255;
  const g = (n >> 8) & 255;
  const b = n & 255;
  return [-0.16874 * r - 0.33126 * g + 0.5 * b + 128, 0.5 * r - 0.41869 * g - 0.08131 * b + 128];
}

const FRAG = `uniform vec2 u_keyUV;
uniform float u_sim;
uniform float u_blend;

// U, V of an RGB colour, Rec. 601 limited range on 0..255 (ffmpeg's CCIR).
vec2 uvOf(vec3 c) {
  vec3 p = c * 255.0;
  float k = 224.0 / 255.0;
  return vec2((-0.16874 * p.r - 0.33126 * p.g + 0.5 * p.b) * k + 128.0,
              (0.5 * p.r - 0.41869 * p.g - 0.08131 * p.b) * k + 128.0);
}

void main() {
  vec4 c = texture(u_src, v_uv);
  float diff = 0.0;
  for (int dy = -1; dy <= 1; dy++) {
    for (int dx = -1; dx <= 1; dx++) {
      vec2 d = uvOf(texture(u_src, v_uv + vec2(dx, dy) * u_texel).rgb) - u_keyUV;
      diff += sqrt(dot(d, d) / (255.0 * 255.0 * 2.0));
    }
  }
  diff /= 9.0;
  float k = u_blend > 0.0001 ? clamp((diff - u_sim) / u_blend, 0.0, 1.0) : (diff > u_sim ? 1.0 : 0.0);
  o = vec4(c.rgb, c.a * k);
}`;

const chromaKey: ShaderModule = {
  type: "chromaKey",
  steps(p) {
    const [u, v] = keyUV(p.color as string);
    return [{ kind: "pass", id: "chromaKey", frag: FRAG, uniforms: { u_keyUV: [u, v], u_sim: p.similarity as number, u_blend: p.blend as number } }];
  },
};
export default chromaKey;
