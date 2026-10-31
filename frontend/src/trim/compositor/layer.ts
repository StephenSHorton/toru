import type { Clip } from "@/trim/project";
import { animAt } from "@/trim/fx/ops";

// layer.ts: how the Compositor draws one clip layer onto the canvas (or
// into a transition's buffer, transition-gl.ts). Its own module so the
// parity scripts can draw layers without the Compositor component.

// layerProgram draws one textured quad (a clip layer) with an opacity.
export function layerProgram(gl: WebGL2RenderingContext): WebGLProgram {
  const vs = `#version 300 es
uniform vec4 u_rect; // centre x, y and half width, height, canvas pixels (top-left origin)
uniform vec3 u_view; // canvas width, height; rotation (radians, clockwise)
out vec2 v_uv;
void main() {
  vec2 c = vec2(gl_VertexID & 1, (gl_VertexID >> 1) & 1); // 0,0 1,0 0,1 1,1
  v_uv = c; // texture row 0 = the frame's top
  vec2 d = (c * 2.0 - 1.0) * u_rect.zw;
  float s = sin(u_view.z), k = cos(u_view.z);
  vec2 px = u_rect.xy + vec2(d.x * k - d.y * s, d.x * s + d.y * k);
  gl_Position = vec4(px.x / u_view.x * 2.0 - 1.0, 1.0 - px.y / u_view.y * 2.0, 0.0, 1.0);
}`;
  const fs = `#version 300 es
precision mediump float;
uniform sampler2D u_tex;
uniform float u_opacity;
in vec2 v_uv;
out vec4 o;
void main() {
  vec4 c = texture(u_tex, v_uv);
  o = vec4(c.rgb, c.a * u_opacity);
}`;
  const p = gl.createProgram()!;
  for (const [type, src] of [
    [gl.VERTEX_SHADER, vs],
    [gl.FRAGMENT_SHADER, fs],
  ] as const) {
    const s = gl.createShader(type)!;
    gl.shaderSource(s, src);
    gl.compileShader(s);
    if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) throw new Error(`compositor shader: ${gl.getShaderInfoLog(s)}`);
    gl.attachShader(p, s);
  }
  gl.linkProgram(p);
  if (!gl.getProgramParameter(p, gl.LINK_STATUS)) throw new Error(`compositor program: ${gl.getProgramInfoLog(p)}`);
  return p;
}

// drawLayer places a w×h frame on the W×H canvas by the clip's transform at
// clip-local time lt (keyframes applied): scale 1 fits it inside the canvas,
// centred on (x, y) in 0..1 of the canvas, turned clockwise by rotation°.
export function drawLayer(
  gl: WebGL2RenderingContext,
  prog: WebGLProgram,
  vao: WebGLVertexArrayObject | null,
  loc: { rect: WebGLUniformLocation | null; view: WebGLUniformLocation | null; tex: WebGLUniformLocation | null; opacity: WebGLUniformLocation | null },
  tex: WebGLTexture,
  w: number,
  h: number,
  clip: Clip,
  lt: number,
  W: number,
  H: number,
) {
  const tr = animAt(clip, lt);
  if (tr.opacity <= 0 || tr.scale <= 0) return;
  const fit = Math.min(W / w, H / h) * tr.scale;
  gl.useProgram(prog);
  gl.bindVertexArray(vao);
  gl.activeTexture(gl.TEXTURE0);
  gl.bindTexture(gl.TEXTURE_2D, tex);
  // The effects output is NEAREST-sampled; scaling a layer wants LINEAR.
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
  gl.uniform1i(loc.tex, 0);
  gl.uniform4f(loc.rect, tr.x * W, tr.y * H, (w * fit) / 2, (h * fit) / 2);
  gl.uniform3f(loc.view, W, H, (tr.rotation * Math.PI) / 180);
  gl.uniform1f(loc.opacity, Math.min(1, tr.opacity));
  gl.enable(gl.BLEND);
  gl.blendFuncSeparate(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA, gl.ONE, gl.ONE_MINUS_SRC_ALPHA);
  gl.drawArrays(gl.TRIANGLE_STRIP, 0, 4);
  gl.bindVertexArray(null);
}
