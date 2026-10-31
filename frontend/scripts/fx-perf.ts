// fx-perf holds the preview's effect renderer to its GPU budget
// (docs/video-editor-v3.md, "Preview budget"): a 3440x1440 frame through the
// "everything" stack, every registered effect once (so a new effect is in
// it automatically), must take at most 6 ms of GPU time at p95. It runs
// the stack at full intensity and again at 75% (each effect then also
// pays its mix pass), on the real GPU in headless Edge (ANGLE D3D11), and
// fails if the page got a software renderer, where the numbers mean
// nothing.
//
// GPU time comes from EXT_disjoint_timer_query_webgl2 when the browser
// offers it; otherwise each frame is timed on the wall clock up to a
// 1-pixel readback (CPU + GPU, so stricter), and the output says so.
//
//   bun scripts/fx-perf.ts            FX_PERF_FRAMES=<n> (default 300)
import { mod, openPage, tempDir } from "./fx-edge";
import fs from "node:fs";

const W = 3440;
const H = 1440;
const BUDGET_MS = 6;
const FRAMES = Number(process.env.FX_PERF_FRAMES ?? 300);
const WARMUP = 30; // shader compiles and LUT builds land here

const PAGE = `import { FxRenderer } from ${mod("fx/renderer.ts")};
import { defs, resolveStack } from ${mod("fx/registry.ts")};

// everything is one instance of every def, each param pushed off its
// default: numbers at max, enums at their last option, booleans flipped.
// (A default adjust or blur changes nothing and would be skipped.)
function everything(amount: number) {
  return defs.map((d, i) => {
    const params: Record<string, unknown> = {};
    for (const p of d.params) {
      if (p.type === "number") params[p.id] = p.max;
      else if (p.type === "enum") params[p.id] = p.options[p.options.length - 1].id;
      else if (p.type === "boolean") params[p.id] = !p.default;
    }
    return { id: "fx" + i, type: d.type, enabled: true, params, amount };
  });
}

(window as any).measure = async (w: number, h: number, frames: number, warmup: number, amount: number) => {
  const canvas = document.createElement("canvas");
  canvas.width = w; canvas.height = h;
  const gl = canvas.getContext("webgl2", { premultipliedAlpha: false, powerPreference: "high-performance" })!;
  const info = gl.getExtension("WEBGL_debug_renderer_info");
  const renderer = String(gl.getParameter(info ? info.UNMASKED_RENDERER_WEBGL : gl.RENDERER));
  // A busy frame: gradients, a checkerboard and noise, so nothing is uniform.
  const px = new Uint8Array(w * h * 4);
  let seed = 7;
  for (let y = 0; y < h; y++)
    for (let x = 0; x < w; x++) {
      const o = (y * w + x) * 4;
      seed = (seed * 1103515245 + 12345) >>> 0;
      const n = seed >>> 25;
      const c = ((x >> 5) + (y >> 5)) & 1 ? 40 : 0;
      px[o] = ((x * 255) / w + n) & 255;
      px[o + 1] = ((y * 255) / h + c) & 255;
      px[o + 2] = (128 + n - c) & 255;
      px[o + 3] = 255;
    }
  const tex = gl.createTexture()!;
  gl.bindTexture(gl.TEXTURE_2D, tex);
  gl.pixelStorei(gl.UNPACK_ALIGNMENT, 1);
  gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, w, h, 0, gl.RGBA, gl.UNSIGNED_BYTE, px);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
  const r = new FxRenderer(gl);
  const stack = everything(amount);
  const fbo = gl.createFramebuffer();
  const one = new Uint8Array(4);
  const timer = gl.getExtension("EXT_disjoint_timer_query_webgl2") as any;
  const queries: WebGLQuery[] = [];
  const wall: number[] = [];
  for (let f = 0; f < frames; f++) {
    // Resolve per frame, as the compositor does.
    const resolved = resolveStack(stack as any, f / 60);
    const q = timer ? gl.createQuery()! : null;
    if (q) gl.beginQuery(timer.TIME_ELAPSED_EXT, q);
    const t0 = performance.now();
    const out = r.apply(tex, w, h, resolved, f / 60);
    if (q) {
      gl.endQuery(timer.TIME_ELAPSED_EXT);
      queries.push(q);
    }
    // Read one pixel of the result so the work can't be skipped (and, for
    // the wall clock, so it has finished).
    gl.bindFramebuffer(gl.FRAMEBUFFER, fbo);
    gl.framebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, out, 0);
    gl.readPixels(0, 0, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, one);
    gl.bindFramebuffer(gl.FRAMEBUFFER, null);
    wall.push(performance.now() - t0);
  }
  let ms: number[] = wall;
  let source = "wall clock to a 1-pixel readback (no GPU timer)";
  if (timer) {
    const got: number[] = [];
    for (const q of queries) {
      for (let i = 0; i < 200 && !gl.getQueryParameter(q, gl.QUERY_RESULT_AVAILABLE); i++) await new Promise((res) => setTimeout(res, 5));
      got.push(gl.getQueryParameter(q, gl.QUERY_RESULT) / 1e6);
    }
    if (!gl.getParameter(timer.GPU_DISJOINT_EXT)) {
      ms = got;
      source = "GPU timer queries";
    }
  }
  r.dispose();
  return { renderer, source, ms: ms.slice(warmup), types: stack.map((e) => e.type) };
};
(window as any).ready = true;`;

const pct = (xs: number[], p: number) => xs.slice().sort((a, b) => a - b)[Math.min(xs.length - 1, Math.floor(xs.length * p))];

const tmp = tempDir("fx-perf");
let failed = false;
const page = await openPage(tmp, PAGE, {}, { gpu: true });
try {
  for (const amount of [1, 0.75]) {
    const r = await page.evaluate(`measure(${W}, ${H}, ${FRAMES + WARMUP}, ${WARMUP}, ${amount})`);
    if (amount === 1) {
      console.log(`renderer: ${r.renderer}`);
      console.log(`timing: ${r.source}`);
      console.log(`stack: ${r.types.join(", ")}`);
      if (/swiftshader|software|llvmpipe|basic render|warp/i.test(r.renderer)) {
        console.log("FAIL the page has a software renderer, not the GPU; these numbers mean nothing");
        failed = true;
        break;
      }
    }
    const p50 = pct(r.ms, 0.5);
    const p95 = pct(r.ms, 0.95);
    const max = Math.max(...r.ms);
    const ok = p95 <= BUDGET_MS;
    failed ||= !ok;
    console.log(`${ok ? "ok  " : "FAIL"} everything at ${Math.round(amount * 100)}%, ${W}x${H}, ${r.ms.length} frames: p50 ${p50.toFixed(2)} ms  p95 ${p95.toFixed(2)} ms  max ${max.toFixed(2)} ms  (budget p95 <= ${BUDGET_MS} ms)`);
  }
} finally {
  await page.close();
  fs.rmSync(tmp, { recursive: true, force: true });
}
process.exit(failed ? 1 : 0);
