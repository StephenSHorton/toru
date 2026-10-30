import type { EffectInstance } from "@/trim/project";
import { resolveStack } from "@/trim/fx/registry";
import { FxRenderer } from "@/trim/fx/renderer";

// thumb.ts renders small previews of a frame through an effect stack (the
// effect browser's and the filter tiles' thumbnails), with the same renderer
// as the player, on one shared offscreen WebGL2 context.

let shared: { canvas: OffscreenCanvas; gl: WebGL2RenderingContext; fx: FxRenderer; src: WebGLTexture; blit: WebGLProgram; vao: WebGLVertexArrayObject } | null = null;

const BLIT_VS = `#version 300 es
out vec2 v_uv;
void main() {
  vec2 p = vec2((gl_VertexID << 1) & 2, gl_VertexID & 2);
  v_uv = vec2(p.x, 1.0 - p.y); // texture row 0 = top; the canvas's row 0 is its bottom
  gl_Position = vec4(p * 2.0 - 1.0, 0.0, 1.0);
}`;
const BLIT_FS = `#version 300 es
precision highp float;
uniform sampler2D u_src;
in vec2 v_uv;
out vec4 o;
void main() { o = texture(u_src, v_uv); }`;

function context() {
  if (shared) return shared;
  const canvas = new OffscreenCanvas(16, 16);
  const gl = canvas.getContext("webgl2", { alpha: true, premultipliedAlpha: false, antialias: false }) as WebGL2RenderingContext | null;
  if (!gl) throw new Error("WebGL2 isn't available");
  const blit = gl.createProgram()!;
  for (const [type, src] of [
    [gl.VERTEX_SHADER, BLIT_VS],
    [gl.FRAGMENT_SHADER, BLIT_FS],
  ] as const) {
    const s = gl.createShader(type)!;
    gl.shaderSource(s, src);
    gl.compileShader(s);
    gl.attachShader(blit, s);
  }
  gl.linkProgram(blit);
  shared = { canvas, gl, fx: new FxRenderer(gl), src: gl.createTexture()!, blit, vao: gl.createVertexArray()! };
  return shared;
}

// renderThumb draws frame (a video element on a decoded frame, an image, a
// canvas or a bitmap) scaled to w×h through the stack (its keyframes read
// at clip-local time t) and returns the result as a bitmap.
export function renderThumb(frame: TexImageSource, stack: EffectInstance[], w: number, h: number, t = 0): ImageBitmap {
  const c = context();
  const { gl } = c;
  w = Math.max(1, Math.round(w));
  h = Math.max(1, Math.round(h));
  // Scale on the CPU side first, so the effects run at thumbnail size (their
  // sizes are in 1080-line units, so they look the same).
  const scaled = new OffscreenCanvas(w, h);
  scaled.getContext("2d")!.drawImage(frame as CanvasImageSource, 0, 0, w, h);
  gl.bindTexture(gl.TEXTURE_2D, c.src);
  gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, false);
  gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, scaled);
  const out = c.fx.apply(c.src, w, h, resolveStack(stack, t), t);
  c.canvas.width = w;
  c.canvas.height = h;
  gl.bindFramebuffer(gl.FRAMEBUFFER, null);
  gl.viewport(0, 0, w, h);
  gl.useProgram(c.blit);
  gl.bindVertexArray(c.vao);
  gl.activeTexture(gl.TEXTURE0);
  gl.bindTexture(gl.TEXTURE_2D, out);
  gl.uniform1i(gl.getUniformLocation(c.blit, "u_src"), 0);
  gl.drawArrays(gl.TRIANGLES, 0, 3);
  return c.canvas.transferToImageBitmap();
}
