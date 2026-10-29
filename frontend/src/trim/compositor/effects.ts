import type { Chroma, Filters } from "@/trim/project";

// effects.ts draws a v2 clip's effects in the preview (docs/video-editor-v2.md,
// Effects): green screen, colour (saturation and look, then contrast and
// brightness) and blur, in that order. internal/vid/effects.go builds the
// same effects as ffmpeg filters for the export; keep the two in step
// (frontend/scripts/effects-parity.ts renders both and compares them).
//
// The export only has LGPL ffmpeg filters, so the maths here follows those:
//   green screen   ffmpeg chromakey: each pixel's U/V (Rec. 601, limited
//                  range, 0..255) against the key colour's, averaged over the
//                  3x3 neighbourhood; alpha = clamp((diff - similarity) / blend).
//   colour         colorchannelmixer with one 3x3 RGB matrix (look · saturation),
//                  then lutrgb: (v - 127.5) * contrast + 127.5 + brightness * 255.
//   blur           avgblur: a box of radius r on every channel, r = blur at
//                  1080 lines scaled to the frame's height.

export type EffectInput = { chroma?: Chroma; filters: Filters };

// The looks as RGB matrices, row-major (out.r = m[0]*r + m[1]*g + m[2]*b ...).
// Same numbers as LookMatrices in internal/vid/effects.go.
export const LOOK_MATRICES: Record<Exclude<Filters["look"], "none">, number[]> = {
  bw: [0.299, 0.587, 0.114, 0.299, 0.587, 0.114, 0.299, 0.587, 0.114],
  sepia: [0.393, 0.769, 0.189, 0.349, 0.686, 0.168, 0.272, 0.534, 0.131],
  vintage: [0.62, 0.32, 0.06, 0.22, 0.68, 0.1, 0.18, 0.28, 0.54],
  cool: [0.9, 0.0, 0.1, 0.0, 0.97, 0.08, 0.0, 0.05, 1.1],
  warm: [1.1, 0.05, 0.0, 0.03, 1.0, 0.0, 0.0, 0.0, 0.85],
};

const LUMA = [0.299, 0.587, 0.114];
const IDENTITY = [1, 0, 0, 0, 1, 0, 0, 0, 1];
// Largest blur radius the shader loops over (blur 20 at 2160 lines is 40).
export const MAX_BLUR_RADIUS = 64;

const clamp = (n: number, lo: number, hi: number) => (Number.isNaN(n) ? lo : Math.max(lo, Math.min(hi, n)));

// normFilters clamps each filter to its range (as effects.go).
export function normFilters(f: Filters): Filters {
  return {
    brightness: clamp(f.brightness, -1, 1),
    contrast: clamp(f.contrast, 0, 2),
    saturation: clamp(f.saturation, 0, 3),
    look: f.look,
    blur: clamp(f.blur, 0, 20),
  };
}

// colorMatrix is saturation then look as one RGB matrix (look · sat).
export function colorMatrix(f: Filters): number[] {
  const s = f.saturation;
  const sat = [0, 1, 2].flatMap((r) => [0, 1, 2].map((c) => (1 - s) * LUMA[c] + (r === c ? s : 0)));
  const look = f.look !== "none" ? LOOK_MATRICES[f.look] : undefined;
  if (!look) return sat;
  const m = new Array(9).fill(0);
  for (let r = 0; r < 3; r++) for (let c = 0; c < 3; c++) for (let k = 0; k < 3; k++) m[r * 3 + c] += look[r * 3 + k] * sat[k * 3 + c];
  return m;
}

// blurRadius is the blur in pixels of a srcH-tall frame (as BlurRadius in Go).
export function blurRadius(blur: number, srcH: number): number {
  if (blur <= 0 || srcH <= 0) return 0;
  return Math.min(Math.round((blur * srcH) / 1080), Math.max(0, Math.floor(srcH / 2) - 1), MAX_BLUR_RADIUS);
}

// keyUV is a "#rrggbb" key colour's U and V the way ffmpeg chromakey reads
// it: Rec. 601 FULL range (its own RGB_TO_U/V), on 0..255, while the
// frame's pixels are limited range (uvOf in the shader). Unreadable colours
// mean green.
export function keyUV(hex: string): [number, number] {
  let h = hex.trim().replace(/^#/, "");
  if (h.length === 3) h = h.replace(/./g, "$&$&");
  const n = /^[0-9a-f]{6}$/i.test(h) ? parseInt(h, 16) : 0x00ff00;
  const r = (n >> 16) & 255;
  const g = (n >> 8) & 255;
  const b = n & 255;
  return [-0.16874 * r - 0.33126 * g + 0.5 * b + 128, 0.5 * r - 0.41869 * g - 0.08131 * b + 128];
}

const isIdentity = (m: number[]) => m.every((v, i) => Math.abs(v - IDENTITY[i]) < 1e-9);

// hasEffects says whether a clip changes anything (else apply() returns src).
export function hasEffects(fx: EffectInput, srcH: number): boolean {
  const f = normFilters(fx.filters);
  return !!fx.chroma || !isIdentity(colorMatrix(f)) || f.contrast !== 1 || f.brightness !== 0 || blurRadius(f.blur, srcH) > 0;
}

const VERT = `#version 300 es
out vec2 v_uv;
void main() {
  // One triangle over the whole target; v_uv 0..1 maps texture rows 1:1.
  vec2 p = vec2((gl_VertexID << 1) & 2, gl_VertexID & 2);
  v_uv = p;
  gl_Position = vec4(p * 2.0 - 1.0, 0.0, 1.0);
}`;

const COLOR_FRAG = `#version 300 es
precision highp float;
uniform sampler2D u_src;
uniform vec2 u_texel;
uniform bool u_key;
uniform vec2 u_keyUV;
uniform float u_sim;
uniform float u_blend;
uniform bool u_matOn;
uniform mat3 u_mat;
uniform float u_contrast;
uniform float u_bright;
in vec2 v_uv;
out vec4 o;

// U, V of an RGB colour, Rec. 601 limited range on 0..255 (ffmpeg's CCIR).
vec2 uvOf(vec3 c) {
  vec3 p = c * 255.0;
  float k = 224.0 / 255.0;
  return vec2((-0.16874 * p.r - 0.33126 * p.g + 0.5 * p.b) * k + 128.0,
              (0.5 * p.r - 0.41869 * p.g - 0.08131 * p.b) * k + 128.0);
}

void main() {
  vec4 c = texture(u_src, v_uv);
  float a = c.a;
  if (u_key) {
    float diff = 0.0;
    for (int dy = -1; dy <= 1; dy++) {
      for (int dx = -1; dx <= 1; dx++) {
        vec2 d = uvOf(texture(u_src, v_uv + vec2(dx, dy) * u_texel).rgb) - u_keyUV;
        diff += sqrt(dot(d, d) / (255.0 * 255.0 * 2.0));
      }
    }
    diff /= 9.0;
    a *= u_blend > 0.0001 ? clamp((diff - u_sim) / u_blend, 0.0, 1.0) : (diff > u_sim ? 1.0 : 0.0);
  }
  vec3 rgb = c.rgb;
  if (u_matOn) rgb = clamp(u_mat * rgb, 0.0, 1.0);
  rgb = clamp((rgb - 0.5) * u_contrast + 0.5 + u_bright, 0.0, 1.0);
  o = vec4(rgb, a);
}`;

const BLUR_FRAG = `#version 300 es
precision highp float;
uniform sampler2D u_src;
uniform vec2 u_step;
uniform int u_r;
in vec2 v_uv;
out vec4 o;
void main() {
  vec4 s = vec4(0.0);
  for (int i = -${MAX_BLUR_RADIUS}; i <= ${MAX_BLUR_RADIUS}; i++) {
    if (i < -u_r || i > u_r) continue;
    s += texture(u_src, v_uv + u_step * float(i));
  }
  o = s / float(2 * u_r + 1);
}`;

type Target = { tex: WebGLTexture; fbo: WebGLFramebuffer };

// EffectsRenderer runs clip frames through the effects on a WebGL2 context.
export class EffectsRenderer {
  private gl: WebGL2RenderingContext;
  private color: WebGLProgram;
  private blur: WebGLProgram;
  private vao: WebGLVertexArrayObject;
  private targets: Target[] = [];
  private size = { w: 0, h: 0 };

  constructor(gl: WebGL2RenderingContext) {
    this.gl = gl;
    this.color = program(gl, VERT, COLOR_FRAG);
    this.blur = program(gl, VERT, BLUR_FRAG);
    this.vao = gl.createVertexArray()!;
  }

  // apply runs src (the clip's own frame: straight-alpha RGBA, w×h, row 0 =
  // top) through the clip's effects and returns a w×h texture with the
  // result, same alpha convention and orientation. The texture is the
  // renderer's and the next apply() overwrites it. src itself comes back
  // when the clip has no effects.
  apply(src: WebGLTexture, w: number, h: number, fx: EffectInput): WebGLTexture {
    if (!hasEffects(fx, h) || w <= 0 || h <= 0) return src;
    const gl = this.gl;
    const f = normFilters(fx.filters);
    this.ensure(w, h);
    const prevFbo = gl.getParameter(gl.FRAMEBUFFER_BINDING);
    const prevViewport = gl.getParameter(gl.VIEWPORT) as Int32Array;
    const blend = gl.isEnabled(gl.BLEND);
    gl.disable(gl.BLEND);
    gl.bindVertexArray(this.vao);
    gl.viewport(0, 0, w, h);

    // Pass 1: green screen and colour into targets[0].
    const m = colorMatrix(f);
    gl.useProgram(this.color);
    gl.bindFramebuffer(gl.FRAMEBUFFER, this.targets[0].fbo);
    bindTex(gl, src);
    const u = (n: string) => gl.getUniformLocation(this.color, n);
    gl.uniform1i(u("u_src"), 0);
    gl.uniform2f(u("u_texel"), 1 / w, 1 / h);
    gl.uniform1i(u("u_key"), fx.chroma ? 1 : 0);
    if (fx.chroma) {
      const [ku, kv] = keyUV(fx.chroma.color);
      gl.uniform2f(u("u_keyUV"), ku, kv);
      gl.uniform1f(u("u_sim"), clamp(fx.chroma.similarity, 0.01, 1));
      gl.uniform1f(u("u_blend"), clamp(fx.chroma.blend, 0, 1));
    }
    gl.uniform1i(u("u_matOn"), isIdentity(m) ? 0 : 1);
    gl.uniformMatrix3fv(u("u_mat"), true, m);
    gl.uniform1f(u("u_contrast"), f.contrast);
    gl.uniform1f(u("u_bright"), f.brightness);
    gl.drawArrays(gl.TRIANGLES, 0, 3);
    let out = this.targets[0].tex;

    // Passes 2 and 3: box blur across, then down (targets[1], then back to [0]).
    const r = blurRadius(f.blur, h);
    if (r > 0) {
      gl.useProgram(this.blur);
      const b = (n: string) => gl.getUniformLocation(this.blur, n);
      gl.uniform1i(b("u_src"), 0);
      gl.uniform1i(b("u_r"), r);
      gl.bindFramebuffer(gl.FRAMEBUFFER, this.targets[1].fbo);
      bindTex(gl, this.targets[0].tex);
      gl.uniform2f(b("u_step"), 1 / w, 0);
      gl.drawArrays(gl.TRIANGLES, 0, 3);
      gl.bindFramebuffer(gl.FRAMEBUFFER, this.targets[0].fbo);
      bindTex(gl, this.targets[1].tex);
      gl.uniform2f(b("u_step"), 0, 1 / h);
      gl.drawArrays(gl.TRIANGLES, 0, 3);
      out = this.targets[0].tex;
    }

    gl.bindVertexArray(null);
    gl.bindFramebuffer(gl.FRAMEBUFFER, prevFbo);
    gl.viewport(prevViewport[0], prevViewport[1], prevViewport[2], prevViewport[3]);
    if (blend) gl.enable(gl.BLEND);
    return out;
  }

  dispose() {
    const gl = this.gl;
    this.freeTargets();
    gl.deleteProgram(this.color);
    gl.deleteProgram(this.blur);
    gl.deleteVertexArray(this.vao);
  }

  // ensure keeps two w×h RGBA8 render targets.
  private ensure(w: number, h: number) {
    if (this.size.w === w && this.size.h === h && this.targets.length) return;
    this.freeTargets();
    const gl = this.gl;
    for (let i = 0; i < 2; i++) {
      const tex = gl.createTexture()!;
      gl.bindTexture(gl.TEXTURE_2D, tex);
      gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, w, h, 0, gl.RGBA, gl.UNSIGNED_BYTE, null);
      edgeClamp(gl);
      const fbo = gl.createFramebuffer()!;
      gl.bindFramebuffer(gl.FRAMEBUFFER, fbo);
      gl.framebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, tex, 0);
      this.targets.push({ tex, fbo });
    }
    this.size = { w, h };
  }

  private freeTargets() {
    for (const t of this.targets) {
      this.gl.deleteFramebuffer(t.fbo);
      this.gl.deleteTexture(t.tex);
    }
    this.targets = [];
    this.size = { w: 0, h: 0 };
  }
}

function bindTex(gl: WebGL2RenderingContext, tex: WebGLTexture) {
  gl.activeTexture(gl.TEXTURE0);
  gl.bindTexture(gl.TEXTURE_2D, tex);
  edgeClamp(gl);
}

// Nearest sampling with clamped edges: each tap reads exactly one texel, as
// the ffmpeg filters do, and taps past the edge repeat the edge texel.
function edgeClamp(gl: WebGL2RenderingContext) {
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
}

function program(gl: WebGL2RenderingContext, vs: string, fs: string): WebGLProgram {
  const p = gl.createProgram()!;
  for (const [type, src] of [
    [gl.VERTEX_SHADER, vs],
    [gl.FRAGMENT_SHADER, fs],
  ] as const) {
    const s = gl.createShader(type)!;
    gl.shaderSource(s, src);
    gl.compileShader(s);
    if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) throw new Error(`effects shader: ${gl.getShaderInfoLog(s)}`);
    gl.attachShader(p, s);
  }
  gl.linkProgram(p);
  if (!gl.getProgramParameter(p, gl.LINK_STATUS)) throw new Error(`effects program: ${gl.getProgramInfoLog(p)}`);
  return p;
}
