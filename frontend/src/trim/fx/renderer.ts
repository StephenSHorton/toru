import { generateLut, LUT_SIZE, type ColorFn } from "@/trim/fx/lut";
import type { ResolvedEffect } from "@/trim/fx/registry";
import type { FxCtx, Step, Uniform } from "@/trim/fx/shader";
import { shaders } from "@/trim/fx/types";

// renderer.ts draws a clip's effect stack in the preview
// (docs/video-editor-v3.md, Effect registry → Preview side). Each effect's
// shader module (types/<type>.ts) turns its params into steps, and the
// renderer packs them into as few passes as it can:
//
//   colour steps   consecutive ones compile into ONE 33³ 3D LUT (lut.ts),
//                  sampled trilinearly, the same table the export hands to
//                  ffmpeg's lut3d
//   pixel steps    GLSL functions; with the LUTs around them they fuse into
//                  one generated shader per run
//   passes         neighbourhood effects (blur, green screen, sharpen), one
//                  full-screen pass each, reading the previous result
//
// A group (effects applied from one saved filter) at less than full
// intensity draws its effects, then mixes the result with its input in one
// more pass. Programs are cached by their source and LUTs by their params,
// so a frame only recompiles when the stack's shape changes.

type Target = { tex: WebGLTexture; fbo: WebGLFramebuffer };
type Prog = { p: WebGLProgram; loc: Map<string, WebGLUniformLocation | null> };
type Tagged = { step: Step; mix?: { key: string; amount: number } };

const VERT = `#version 300 es
out vec2 v_uv;
void main() {
  // One triangle over the whole target; v_uv 0..1 maps texture rows 1:1.
  vec2 p = vec2((gl_VertexID << 1) & 2, gl_VertexID & 2);
  v_uv = p;
  gl_Position = vec4(p * 2.0 - 1.0, 0.0, 1.0);
}`;

const HEAD = `#version 300 es
precision highp float;
precision highp sampler3D;
uniform sampler2D u_src;
uniform vec2 u_texel;
in vec2 v_uv;
out vec4 o;
`;

const MIX = `uniform sampler2D u_base;
uniform float u_amount;
void main() { o = mix(texture(u_base, v_uv), texture(u_src, v_uv), u_amount); }`;

const MAX_LUTS = 32;

// FxRenderer runs clip frames through effect stacks on a WebGL2 context.
export class FxRenderer {
  private gl: WebGL2RenderingContext;
  private vao: WebGLVertexArrayObject;
  private targets: Target[] = [];
  private size = { w: 0, h: 0 };
  private progs = new Map<string, Prog>();
  private luts = new Map<string, WebGLTexture>(); // insertion order = LRU

  constructor(gl: WebGL2RenderingContext) {
    this.gl = gl;
    this.vao = gl.createVertexArray()!;
  }

  // apply runs src (the clip's own frame: straight-alpha RGBA, w×h, row 0 =
  // top) through stack at clip-local time t and returns a w×h texture with
  // the result, same alpha convention and orientation. The texture is the
  // renderer's and the next apply() overwrites it. src itself comes back
  // when the stack changes nothing.
  apply(src: WebGLTexture, w: number, h: number, stack: ResolvedEffect[], t = 0): WebGLTexture {
    if (w <= 0 || h <= 0 || !stack.length) return src;
    const ctx: FxCtx = { w, h, t };
    const steps: Tagged[] = [];
    for (const e of stack) {
      const mod = shaders[e.type];
      if (!mod || e.amount <= 0) continue;
      const mix = e.amount < 1 ? { key: e.group ?? `fx:${e.id}`, amount: e.amount } : undefined;
      for (const step of mod.steps(e.params, ctx)) steps.push({ step, mix });
    }
    if (!steps.length) return src;

    const gl = this.gl;
    this.ensure(w, h);
    const prevFbo = gl.getParameter(gl.FRAMEBUFFER_BINDING);
    const prevViewport = gl.getParameter(gl.VIEWPORT) as Int32Array;
    const blend = gl.isEnabled(gl.BLEND);
    gl.disable(gl.BLEND);
    gl.bindVertexArray(this.vao);
    gl.viewport(0, 0, w, h);

    let cur = src;
    for (let i = 0; i < steps.length; ) {
      const key = steps[i].mix?.key;
      let j = i + 1;
      while (j < steps.length && steps[j].mix?.key === key) j++;
      const span = steps.slice(i, j).map((s) => s.step);
      if (key === undefined) cur = this.run(span, cur, ctx, []);
      else {
        // A group below full intensity: keep its input, draw it, mix.
        const base = cur;
        const out = this.run(span, base, ctx, [base]);
        cur = this.pass(this.prog(MIX), out, ctx, { u_amount: steps[i].mix!.amount }, [base, out], { u_base: base });
      }
      i = j;
    }

    gl.bindVertexArray(null);
    gl.bindFramebuffer(gl.FRAMEBUFFER, prevFbo);
    gl.viewport(prevViewport[0], prevViewport[1], prevViewport[2], prevViewport[3]);
    if (blend) gl.enable(gl.BLEND);
    return cur;
  }

  // run draws steps over src, fusing runs of colour and pixel steps into one
  // pass each. keep are textures the passes must not draw into.
  private run(steps: Step[], src: WebGLTexture, ctx: FxCtx, keep: WebGLTexture[]): WebGLTexture {
    let cur = src;
    for (let i = 0; i < steps.length; ) {
      const s = steps[i];
      if (s.kind === "pass") {
        cur = this.pass(this.prog(s.frag), cur, ctx, s.uniforms, [...keep, cur]);
        i++;
        continue;
      }
      let j = i;
      while (j < steps.length && steps[j].kind !== "pass") j++;
      cur = this.fused(steps.slice(i, j) as Exclude<Step, { kind: "pass" }>[], cur, ctx, [...keep, cur]);
      i = j;
    }
    return cur;
  }

  // fused draws a run of colour and pixel steps as one generated shader:
  // each stretch of colour steps samples its LUT, each pixel step calls its
  // function, in order.
  private fused(run: Exclude<Step, { kind: "pass" }>[], src: WebGLTexture, ctx: FxCtx, keep: WebGLTexture[]): WebGLTexture {
    const decl: string[] = [];
    const body: string[] = [];
    const uniforms: Record<string, Uniform> = {};
    const textures: Record<string, WebGLTexture> = {};
    let nLut = 0;
    let nFn = 0;
    for (let i = 0; i < run.length; ) {
      const s = run[i];
      if (s.kind === "color") {
        let j = i;
        while (j < run.length && run[j].kind === "color") j++;
        const colors = run.slice(i, j) as Extract<Step, { kind: "color" }>[];
        const name = `u_lut${nLut++}`;
        decl.push(`uniform sampler3D ${name};`);
        body.push(`  c.rgb = texture(${name}, clamp(c.rgb, 0.0, 1.0) * ${((LUT_SIZE - 1) / LUT_SIZE).toFixed(8)} + ${(0.5 / LUT_SIZE).toFixed(8)}).rgb;`);
        textures[name] = this.lut(
          colors.map((c) => c.key).join("|"),
          colors.map((c) => c.fn),
        );
        i = j;
        continue;
      }
      const pre = `p${nFn++}_`;
      decl.push(s.glsl.replaceAll("$", pre));
      body.push(`  c = ${pre}fn(c, v_uv);`);
      for (const [k, v] of Object.entries(s.uniforms)) uniforms[pre + k.replace(/^\$/, "")] = v;
      i++;
    }
    const frag = `${decl.join("\n")}\nvoid main() {\n  vec4 c = texture(u_src, v_uv);\n${body.join("\n")}\n  o = c;\n}`;
    return this.pass(this.prog(frag), src, ctx, uniforms, keep, textures);
  }

  // pass draws one full-screen pass of prog over src into a free target.
  private pass(
    prog: Prog,
    src: WebGLTexture,
    ctx: FxCtx,
    uniforms: Record<string, Uniform>,
    keep: WebGLTexture[],
    textures: Record<string, WebGLTexture> = {},
  ): WebGLTexture {
    const gl = this.gl;
    const target = this.targets.find((t) => !keep.includes(t.tex)) ?? this.addTarget();
    gl.useProgram(prog.p);
    gl.bindFramebuffer(gl.FRAMEBUFFER, target.fbo);
    bindTex(gl, 0, gl.TEXTURE_2D, src);
    setUniform(gl, prog, "u_src", { int: 0 });
    setUniform(gl, prog, "u_texel", [1 / ctx.w, 1 / ctx.h]);
    let unit = 1;
    for (const [name, tex] of Object.entries(textures)) {
      const is3D = name.startsWith("u_lut");
      bindTex(gl, unit, is3D ? gl.TEXTURE_3D : gl.TEXTURE_2D, tex);
      setUniform(gl, prog, name, { int: unit++ });
    }
    for (const [name, v] of Object.entries(uniforms)) setUniform(gl, prog, name, v);
    gl.drawArrays(gl.TRIANGLES, 0, 3);
    return target.tex;
  }

  private prog(frag: string): Prog {
    let p = this.progs.get(frag);
    if (!p) {
      p = { p: program(this.gl, VERT, HEAD + frag), loc: new Map() };
      this.progs.set(frag, p);
    }
    return p;
  }

  // lut is the 3D texture for a run of colour steps, generated once per
  // distinct key (the steps' params) and kept for the next MAX_LUTS keys.
  private lut(key: string, fns: ColorFn[]): WebGLTexture {
    const gl = this.gl;
    const hit = this.luts.get(key);
    if (hit) {
      this.luts.delete(key);
      this.luts.set(key, hit);
      return hit;
    }
    const data = new Float32Array(generateLut(fns));
    const tex = gl.createTexture()!;
    gl.bindTexture(gl.TEXTURE_3D, tex);
    gl.pixelStorei(gl.UNPACK_ALIGNMENT, 4);
    gl.texImage3D(gl.TEXTURE_3D, 0, gl.RGB16F, LUT_SIZE, LUT_SIZE, LUT_SIZE, 0, gl.RGB, gl.FLOAT, data);
    gl.texParameteri(gl.TEXTURE_3D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_3D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
    for (const wrap of [gl.TEXTURE_WRAP_S, gl.TEXTURE_WRAP_T, gl.TEXTURE_WRAP_R]) gl.texParameteri(gl.TEXTURE_3D, wrap, gl.CLAMP_TO_EDGE);
    this.luts.set(key, tex);
    if (this.luts.size > MAX_LUTS) {
      const [oldKey, old] = this.luts.entries().next().value!;
      gl.deleteTexture(old);
      this.luts.delete(oldKey);
    }
    return tex;
  }

  dispose() {
    const gl = this.gl;
    this.freeTargets();
    for (const p of this.progs.values()) gl.deleteProgram(p.p);
    for (const t of this.luts.values()) gl.deleteTexture(t);
    this.progs.clear();
    this.luts.clear();
    gl.deleteVertexArray(this.vao);
  }

  // ensure sizes the render targets to w×h (they're RGBA8, as many as the
  // stack needs: 2, or 3 with a group mix).
  private ensure(w: number, h: number) {
    if (this.size.w === w && this.size.h === h) return;
    this.freeTargets();
    this.size = { w, h };
  }

  private addTarget(): Target {
    const gl = this.gl;
    const tex = gl.createTexture()!;
    gl.bindTexture(gl.TEXTURE_2D, tex);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, this.size.w, this.size.h, 0, gl.RGBA, gl.UNSIGNED_BYTE, null);
    edgeClamp(gl, gl.TEXTURE_2D);
    const fbo = gl.createFramebuffer()!;
    gl.bindFramebuffer(gl.FRAMEBUFFER, fbo);
    gl.framebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, tex, 0);
    const t = { tex, fbo };
    this.targets.push(t);
    return t;
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

function setUniform(gl: WebGL2RenderingContext, prog: Prog, name: string, v: Uniform) {
  let loc = prog.loc.get(name);
  if (loc === undefined) {
    loc = gl.getUniformLocation(prog.p, name);
    prog.loc.set(name, loc);
  }
  if (loc === null) return;
  if (typeof v === "number") gl.uniform1f(loc, v);
  else if (typeof v === "boolean") gl.uniform1i(loc, v ? 1 : 0);
  else if (Array.isArray(v)) {
    if (v.length === 2) gl.uniform2fv(loc, v);
    else if (v.length === 3) gl.uniform3fv(loc, v);
    else if (v.length === 4) gl.uniform4fv(loc, v);
    else gl.uniform1fv(loc, v);
  } else if ("int" in v) gl.uniform1i(loc, v.int);
  else gl.uniformMatrix3fv(loc, true, v.mat3);
}

// bindTex binds tex to a texture unit. 2D textures read with nearest
// sampling and clamped edges, so each tap reads exactly one texel, as the
// ffmpeg filters do, and taps past the edge repeat the edge texel.
function bindTex(gl: WebGL2RenderingContext, unit: number, target: number, tex: WebGLTexture) {
  gl.activeTexture(gl.TEXTURE0 + unit);
  gl.bindTexture(target, tex);
  if (target === gl.TEXTURE_2D) edgeClamp(gl, target);
}

function edgeClamp(gl: WebGL2RenderingContext, target: number) {
  gl.texParameteri(target, gl.TEXTURE_MIN_FILTER, gl.NEAREST);
  gl.texParameteri(target, gl.TEXTURE_MAG_FILTER, gl.NEAREST);
  gl.texParameteri(target, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
  gl.texParameteri(target, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
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
    if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) throw new Error(`effects shader: ${gl.getShaderInfoLog(s)}\n${src}`);
    gl.attachShader(p, s);
    gl.deleteShader(s);
  }
  gl.linkProgram(p);
  if (!gl.getProgramParameter(p, gl.LINK_STATUS)) throw new Error(`effects program: ${gl.getProgramInfoLog(p)}`);
  return p;
}
