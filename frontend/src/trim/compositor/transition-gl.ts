import type { TransitionType } from "@/trim/transition";

// transition-gl.ts draws a transition's frame in the preview: ffmpeg xfade's
// formulas (libavfilter/vf_xfade.c, n9.0.2) for the types transition.ts
// offers, so the preview matches the export (internal/vid/transitions.go).
//
// The two clips are drawn (by the Compositor's drawLayer) into two
// canvas-size buffers cleared to transparent; the export makes the same
// canvas-size layers (transparent black around the clip) and xfades those.
// drawLayer leaves premultiplied colour, xfade mixes straight colour and
// alpha (yuva420p planes), so the shader un-premultiplies first (transparent
// pixels are black, as the export's black@0 canvas), mixes like xfade, and
// the result is blended over the tracks below as straight alpha, like the
// export's overlay. Coordinates are xfade's: x from the left, y from the top,
// whole pixels of this canvas.
//
// hblur's box blur is sampled with 32 taps across its window rather than
// every pixel (its parity limits are looser); the rest are exact formulas.
// What can't match: xfade runs per plane on 4:2:0, so the half-size chroma
// planes get their own whole-pixel offsets (slides, zoom) and colour edges
// land a pixel apart (transition-parity.ts has the limits).

const TYPE_ID: Record<TransitionType, number> = {
  fade: 0,
  fadeblack: 1,
  fadewhite: 2,
  wipeleft: 3,
  wiperight: 4,
  wipeup: 5,
  wipedown: 6,
  slideleft: 7,
  slideright: 8,
  slideup: 9,
  slidedown: 10,
  zoomin: 11,
  hblur: 12,
};

const VS = `#version 300 es
void main() {
  vec2 c = vec2(gl_VertexID & 1, (gl_VertexID >> 1) & 1);
  gl_Position = vec4(c * 2.0 - 1.0, 0.0, 1.0);
}`;

const FS = `#version 300 es
precision highp float;
precision highp int;
uniform sampler2D u_a; // outgoing clip's layer (premultiplied)
uniform sampler2D u_b; // incoming clip's layer
uniform int u_type;
uniform float u_p;      // xfade progress, 1 -> 0
uniform ivec2 u_size;   // canvas pixels
out vec4 o;

// A layer's straight colour at (x, y), top-left origin.
vec4 at(sampler2D t, int x, int y) {
  x = clamp(x, 0, u_size.x - 1);
  y = clamp(y, 0, u_size.y - 1);
  vec4 c = texelFetch(t, ivec2(x, u_size.y - 1 - y), 0);
  return c.a > 0.0 ? vec4(c.rgb / c.a, c.a) : vec4(0.0);
}
// xfade's mix(a, b, m) = a*m + b*(1-m)
vec4 mx(vec4 a, vec4 b, float m) { return a * m + b * (1.0 - m); }
float ss(float e0, float e1, float x) { float t = clamp((x - e0) / (e1 - e0), 0.0, 1.0); return t * t * (3.0 - 2.0 * t); }
int wrap(int v, int n) { int r = v - (v / n) * n; return r + (v < 0 && r != 0 ? n : 0); }

void main() {
  int w = u_size.x, h = u_size.y;
  int x = int(gl_FragCoord.x);
  int y = h - 1 - int(gl_FragCoord.y);
  float p = u_p;
  vec4 A = at(u_a, x, y), B = at(u_b, x, y);
  vec4 r;
  if (u_type == 0) {
    r = mx(A, B, p);
  } else if (u_type == 1 || u_type == 2) {
    // xfade's black is Y=0 and its white Y=255 on limited-range planes:
    // below black and above white in RGB (1.164*(Y-16)/255), so a dip goes
    // darker (brighter) than the plain colour until the output clamps.
    vec4 bg = u_type == 1 ? vec4(vec3(-0.07303), 1.0) : vec4(vec3(1.09094), 1.0);
    r = mx(mx(A, bg, ss(0.8, 1.0, p)), mx(bg, B, ss(0.2, 1.0, p)), p);
  } else if (u_type == 3) {
    int z = int(float(w) * p);
    r = x > z ? B : A;
  } else if (u_type == 4) {
    int z = int(float(w) * (1.0 - p));
    r = x > z ? A : B;
  } else if (u_type == 5) {
    int z = int(float(h) * p);
    r = y > z ? B : A;
  } else if (u_type == 6) {
    int z = int(float(h) * (1.0 - p));
    r = y > z ? A : B;
  } else if (u_type == 7 || u_type == 8) {
    int z = int((u_type == 7 ? -p : p) * float(w));
    int zx = z + x;
    int zz = wrap(zx, w);
    r = (zx >= 0 && zx < w) ? at(u_b, zz, y) : at(u_a, zz, y);
  } else if (u_type == 9 || u_type == 10) {
    int z = int((u_type == 9 ? -p : p) * float(h));
    int zy = z + y;
    int zz = wrap(zy, h);
    r = (zy >= 0 && zy < h) ? at(u_b, x, zz) : at(u_a, x, zz);
  } else if (u_type == 11) {
    float zf = ss(0.5, 1.0, p);
    float u = 0.5 + (float(x) / float(w) - 0.5) * zf;
    float v = 0.5 + (float(y) / float(h) - 0.5) * zf;
    vec4 zv = at(u_a, int(ceil(u * float(w - 1))), int(ceil(v * float(h - 1))));
    r = mx(zv, B, ss(0.0, 0.5, p));
  } else {
    float prog = p <= 0.5 ? p * 2.0 : (1.0 - p) * 2.0;
    int size = 1 + int(float(w / 2) * prog);
    // The window [x, x+size) clipped at the right edge, 32 samples across.
    int n = min(size, w - x);
    vec4 sa = vec4(0.0), sb = vec4(0.0);
    int taps = min(n, 32);
    for (int i = 0; i < 32; i++) {
      if (i >= taps) break;
      int xi = x + (i * n) / taps;
      sa += at(u_a, xi, y);
      sb += at(u_b, xi, y);
    }
    r = mx(sa / float(taps), sb / float(taps), p);
  }
  o = r;
}`;

type Target = { fbo: WebGLFramebuffer; tex: WebGLTexture };

export class TransitionGL {
  private prog: WebGLProgram;
  private vao: WebGLVertexArrayObject | null;
  private targets: Target[] = [];
  private w = 0;
  private h = 0;
  private loc: Record<string, WebGLUniformLocation | null>;

  constructor(private gl: WebGL2RenderingContext) {
    this.prog = program(gl, VS, FS);
    this.vao = gl.createVertexArray();
    this.loc = Object.fromEntries(["u_a", "u_b", "u_type", "u_p", "u_size"].map((n) => [n, gl.getUniformLocation(this.prog, n)]));
  }

  // layer binds buffer i (0 = outgoing, 1 = incoming) at the canvas size,
  // cleared to transparent, for drawLayer to draw into.
  layer(i: 0 | 1, W: number, H: number) {
    const gl = this.gl;
    if (W !== this.w || H !== this.h || this.targets.length === 0) this.resize(W, H);
    gl.bindFramebuffer(gl.FRAMEBUFFER, this.targets[i].fbo);
    gl.viewport(0, 0, W, H);
    gl.clearColor(0, 0, 0, 0);
    gl.clear(gl.COLOR_BUFFER_BIT);
  }

  // composite draws the transition of the two buffers onto the canvas.
  composite(type: TransitionType, progress: number, W: number, H: number) {
    const gl = this.gl;
    gl.bindFramebuffer(gl.FRAMEBUFFER, null);
    gl.viewport(0, 0, W, H);
    gl.useProgram(this.prog);
    gl.bindVertexArray(this.vao);
    gl.activeTexture(gl.TEXTURE0);
    gl.bindTexture(gl.TEXTURE_2D, this.targets[0].tex);
    gl.activeTexture(gl.TEXTURE1);
    gl.bindTexture(gl.TEXTURE_2D, this.targets[1].tex);
    gl.uniform1i(this.loc.u_a, 0);
    gl.uniform1i(this.loc.u_b, 1);
    gl.uniform1i(this.loc.u_type, TYPE_ID[type] ?? 0);
    gl.uniform1f(this.loc.u_p, progress);
    gl.uniform2i(this.loc.u_size, W, H);
    gl.enable(gl.BLEND);
    gl.blendFuncSeparate(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA, gl.ONE, gl.ONE_MINUS_SRC_ALPHA);
    gl.drawArrays(gl.TRIANGLE_STRIP, 0, 4);
    gl.bindVertexArray(null);
    gl.activeTexture(gl.TEXTURE0);
  }

  private resize(W: number, H: number) {
    const gl = this.gl;
    this.disposeTargets();
    for (let i = 0; i < 2; i++) {
      const tex = gl.createTexture()!;
      gl.bindTexture(gl.TEXTURE_2D, tex);
      gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, W, H, 0, gl.RGBA, gl.UNSIGNED_BYTE, null);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST);
      const fbo = gl.createFramebuffer()!;
      gl.bindFramebuffer(gl.FRAMEBUFFER, fbo);
      gl.framebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, tex, 0);
      this.targets.push({ fbo, tex });
    }
    gl.bindFramebuffer(gl.FRAMEBUFFER, null);
    this.w = W;
    this.h = H;
  }

  private disposeTargets() {
    for (const t of this.targets) {
      this.gl.deleteFramebuffer(t.fbo);
      this.gl.deleteTexture(t.tex);
    }
    this.targets = [];
  }

  dispose() {
    this.disposeTargets();
    this.gl.deleteProgram(this.prog);
    this.gl.deleteVertexArray(this.vao);
  }
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
    if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) throw new Error(`transition shader: ${gl.getShaderInfoLog(s)}`);
    gl.attachShader(p, s);
  }
  gl.linkProgram(p);
  if (!gl.getProgramParameter(p, gl.LINK_STATUS)) throw new Error(`transition program: ${gl.getProgramInfoLog(p)}`);
  return p;
}
