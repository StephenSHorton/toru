// transition-parity checks that the preview's transitions (compositor/
// transition-gl.ts with layer.ts, as the Compositor draws them) match the
// export's (internal/vid/transitions.go → ffmpeg xfade). The Go side writes
// the export's frames (TestWriteTransitionParityCases: two stills, A then B
// with a 1 s transition, frames at progress ~0.77, 0.5, 0.27, some with B
// at 60% so the transparent canvas around it counts); this script draws the
// same frames in headless Edge and compares them.
//
//   bun scripts/transition-parity.ts        pinned LGPL ffmpeg (see
//                                            internal/fx/fxtest); TORU_FFMPEG
//                                            points at another binary
//   TRANSITION_PARITY_ONLY=<substring>      runs only matching cases
import fs from "node:fs";
import path from "node:path";
import { compare, goBin, mod, openPage, repo, run, tempDir, type Limits } from "./fx-edge";

// The export ends in yuv420p, so chroma is halved at every colour edge
// (the colour bars, a wipe's line); hard edges move by a pixel between the
// two renderers. zoomin samples A with its own rounding, and the preview's
// blur takes 32 taps where xfade averages every pixel of the window.
const SLIDE: Limits = { mode: "pixels", mean: 4, p99: 120, reason: "xfade shifts the half-width chroma planes by their own whole pixels: colour edges sit a pixel apart at odd offsets" };
const LIMITS: Record<string, Limits> = {
  default: { mode: "pixels", mean: 3, p99: 40 },
  slideleft: SLIDE,
  slideright: SLIDE,
  slideup: SLIDE,
  slidedown: SLIDE,
  zoomin: { mode: "pixels", mean: 32, p99: 100, reason: "mid-zoom the frame is one sampled pixel of A, and the chroma planes sample their own (half-size) pixel of the test pattern's sharp centre" },
  hblur: { mode: "stats", mean: 4, p99: 6, reason: "the preview samples the blur window with 32 taps" },
};

const tmp = tempDir("transition-parity");
// TORU_FFMPEG_PINNED lets the Go side download the pinned build (it still
// prefers $TORU_FFMPEG when set); without it the case writer skips.
run(goBin, ["test", "./internal/vid/", "-run", "^TestWriteTransitionParityCases$", "-count=1"], {
  TORU_TRANSITION_PARITY_DIR: tmp,
  TORU_FFMPEG_PINNED: "1",
});
const casesFile = path.join(tmp, "cases.json");
if (!fs.existsSync(casesFile)) {
  console.error(
    "transition-parity: the Go case writer wrote no cases.json (TestWriteTransitionParityCases skipped?). Run it by hand to see why:\n" +
      "  TORU_FFMPEG_PINNED=1 TORU_TRANSITION_PARITY_DIR=<dir> go test ./internal/vid/ -run ^TestWriteTransitionParityCases$ -v",
  );
  process.exit(1);
}
const spec = JSON.parse(fs.readFileSync(casesFile, "utf8")) as {
  w: number;
  h: number;
  fps: number;
  cases: { name: string; type: string; scaleB: number; frames: number[]; files: string[] }[];
};
const { w: W, h: H, fps } = spec;
const only = process.env.TRANSITION_PARITY_ONLY;
const cases = spec.cases.filter((c) => !only || c.name.includes(only));

const PAGE = `import { TransitionGL } from ${mod("compositor/transition-gl.ts")};
import { drawLayer, layerProgram } from ${mod("compositor/layer.ts")};
import { transitionProgress } from ${mod("transition.ts")};
const load = async (u: string) => new Uint8Array(await (await fetch(u)).arrayBuffer());
(window as any).render = async (W: number, H: number, fps: number, type: string, scaleB: number, frame: number) => {
  const canvas = document.createElement("canvas");
  canvas.width = W; canvas.height = H;
  const gl = canvas.getContext("webgl2", { alpha: false, antialias: false, premultipliedAlpha: false, preserveDrawingBuffer: true })!;
  const tex = async (u: string) => {
    const t = gl.createTexture()!;
    gl.bindTexture(gl.TEXTURE_2D, t);
    gl.pixelStorei(gl.UNPACK_ALIGNMENT, 1);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, W, H, 0, gl.RGBA, gl.UNSIGNED_BYTE, await load(u));
    return t;
  };
  const ta = await tex("/a"), tb = await tex("/b");
  const prog = layerProgram(gl);
  const loc = { rect: gl.getUniformLocation(prog, "u_rect"), view: gl.getUniformLocation(prog, "u_view"), tex: gl.getUniformLocation(prog, "u_tex"), opacity: gl.getUniformLocation(prog, "u_opacity") };
  const vao = gl.createVertexArray();
  const clip = (scale: number) => ({ transform: { x: 0.5, y: 0.5, scale, opacity: 1, rotation: 0 } }) as any;
  const tr = { index: 1, type, duration: 1, cut: 2, start: 1.5, end: 2.5 } as any;
  const progress = transitionProgress(tr, frame / fps, fps);
  const T = new TransitionGL(gl);
  gl.bindFramebuffer(gl.FRAMEBUFFER, null);
  gl.viewport(0, 0, W, H);
  gl.clearColor(0, 0, 0, 1);
  gl.clear(gl.COLOR_BUFFER_BIT);
  T.layer(0, W, H);
  drawLayer(gl, prog, vao, loc, ta, W, H, clip(1), 0, W, H);
  T.layer(1, W, H);
  drawLayer(gl, prog, vao, loc, tb, W, H, clip(scaleB), 0, W, H);
  T.composite(type as any, progress, W, H);
  const px = new Uint8Array(W * H * 4);
  gl.readPixels(0, 0, W, H, gl.RGBA, gl.UNSIGNED_BYTE, px);
  // readPixels is bottom-up; the export's frames are top-down.
  const flipped = new Uint8Array(px.length);
  for (let y = 0; y < H; y++) flipped.set(px.subarray((H - 1 - y) * W * 4, (H - y) * W * 4), y * W * 4);
  let s = "";
  for (let i = 0; i < flipped.length; i += 0x8000) s += String.fromCharCode(...flipped.subarray(i, i + 0x8000));
  return { progress, renderer: String(gl.getParameter(gl.RENDERER)), pixels: btoa(s) };
};
(window as any).ready = true;`;

const page = await openPage(tmp, PAGE, {
  "/a": fs.readFileSync(path.join(tmp, "a.rgba")),
  "/b": fs.readFileSync(path.join(tmp, "b.rgba")),
});
let failed = 0;
let renderer = "";
try {
  for (const c of cases) {
    const lim = LIMITS[c.type] ?? LIMITS.default;
    for (let i = 0; i < c.frames.length; i++) {
      const got = await page.evaluate(`render(${W}, ${H}, ${fps}, ${JSON.stringify(c.type)}, ${c.scaleB}, ${c.frames[i]})`);
      renderer = got.renderer;
      const prev = Uint8Array.from(atob(got.pixels), (ch) => ch.charCodeAt(0));
      const exp = new Uint8Array(fs.readFileSync(path.join(tmp, c.files[i])));
      const r = compare(exp, prev, W, H, 2, lim);
      if (!r.ok) failed++;
      console.log(`${r.ok ? "ok  " : "FAIL"} ${c.name.padEnd(16)} frame ${c.frames[i]} p=${got.progress.toFixed(3)}  mean ${r.mean.toFixed(2)}  p99 ${r.p99.toFixed(1)}  max ${r.max.toFixed(0)}`);
      if (!r.ok && process.env.TRANSITION_PARITY_KEEP) {
        fs.writeFileSync(path.join(tmp, `${c.name}-${c.frames[i]}.preview.rgba`), prev);
      }
    }
  }
} finally {
  await page.close();
}
console.log(`renderer: ${renderer}`);
if (process.env.TRANSITION_PARITY_KEEP) console.log(`frames kept in ${tmp}`);
else fs.rmSync(tmp, { recursive: true, force: true });
if (failed) {
  console.log(`${failed} frame(s) over their limits`);
  process.exit(1);
}
console.log("transitions: preview matches the export");
void repo;
