import type { ColorFn } from "@/trim/fx/lut";
import type { Resolved } from "@/trim/fx/registry";

// shader.ts is the contract between the renderer (renderer.ts) and the
// per-type shader modules (types/<type>.ts): a module turns an effect's
// params at one moment into steps, and the renderer packs consecutive steps
// into as few GPU passes as it can.

// FxCtx is what a step may depend on besides its params: the frame being
// drawn (its size in pixels) and the clip-local time in seconds.
export type FxCtx = { w: number; h: number; t: number };

export type Uniform = number | boolean | number[] | { int: number } | { mat3: number[] };

export type Step =
  // A colour change, c → fn(c), the same for every pixel. Consecutive colour
  // steps compile into one 3D LUT (lut.ts). key identifies fn's params, for
  // caching the table.
  | { kind: "color"; fn: ColorFn; key: string }
  // A per-pixel GLSL function that may also use the pixel's position or
  // time. Consecutive pixel steps (and LUTs between them) fuse into one
  // shader. glsl defines `vec4 $fn(vec4 c, vec2 uv)` (straight alpha), and
  // its uniforms are named `$name`; `$` becomes a prefix unique in the
  // shader. id names the GLSL source, for caching programs.
  | { kind: "pixel"; id: string; glsl: string; uniforms: Record<string, Uniform> }
  // A full-screen pass reading the previous result: frag declares its own
  // uniforms and uses the renderer's u_src (sampler2D, nearest, clamped
  // edges), u_texel (1/size), v_uv and out vec4 o. id names the source.
  | { kind: "pass"; id: string; frag: string; uniforms: Record<string, Uniform> };

export type ShaderModule = {
  type: string;
  // steps is what the effect draws with these params; [] when it changes
  // nothing.
  steps(p: Resolved, ctx: FxCtx): Step[];
};
