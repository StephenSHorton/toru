// effects-parity checks that the preview's effects (src/trim/fx/renderer.ts,
// WebGL2) match the export's (internal/vid/effects.go, ffmpeg).
// For each case in internal/vid/effects_parity_test.go it renders one frame
// with a green box on it through ffmpeg using the exact filter chain the
// export builds, and through the shader in headless Edge, then compares the
// two pixel by pixel (colour premultiplied by alpha, plus alpha).
//
//   bun scripts/effects-parity.ts           needs ffmpeg and Go on PATH (or in
//                                           C:\Program Files\Go\bin) and Edge
import { spawn, spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

// Pass limits, in 0..255 levels over every pixel and channel.
const MAX_MEAN = 1;
const MAX_P99 = 6;

const W = 320;
const here = import.meta.dir;
const repo = path.resolve(here, "../..");
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "toru-fx-parity-"));
const EDGE = [
  "C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe",
  "C:/Program Files/Microsoft/Edge/Application/msedge.exe",
].find((p) => fs.existsSync(p));

function run(cmd: string, args: string[], env?: Record<string, string>): Buffer {
  const r = spawnSync(cmd, args, { env: { ...process.env, ...env }, maxBuffer: 1 << 28 });
  if (r.status !== 0) throw new Error(`${cmd} ${args.join(" ")}\n${r.stderr?.toString() ?? r.error}`);
  return r.stdout;
}

// 1. The cases and their ffmpeg chains, from the Go side.
const goBin = fs.existsSync("C:/Program Files/Go/bin/go.exe") ? "C:/Program Files/Go/bin/go.exe" : "go";
process.chdir(repo);
run(goBin, ["test", "./internal/vid/", "-run", "TestWriteEffectsParityCases", "-count=1"], { TORU_FX_PARITY_DIR: tmp });
const { height: H, cases } = JSON.parse(fs.readFileSync(path.join(tmp, "cases.json"), "utf8")) as {
  height: number;
  cases: { name: string; chroma?: unknown; filters: unknown; filter: string }[];
};

// The first case is the editor's default green screen; both sides must agree
// on what that is.
const { DEFAULT_CHROMA } = await import("../src/trim/project");
if (JSON.stringify(cases[0].chroma) !== JSON.stringify(DEFAULT_CHROMA)) {
  throw new Error(`Go DefaultChroma ${JSON.stringify(cases[0].chroma)} != project.ts DEFAULT_CHROMA ${JSON.stringify(DEFAULT_CHROMA)}`);
}

// 2. The source frame: test pattern, a pure green box, a near-green box (so
// the soft key edge shows), and white and black boxes (which a green screen
// must leave opaque), as PNG and as raw RGBA.
const src = path.join(tmp, "src.png");
const WHITE_BOX = { x: 250, y: 120, w: 50, h: 40 };
run("ffmpeg", [
  "-v", "error", "-y", "-f", "lavfi", "-i", `testsrc2=s=${W}x${H}:r=1:d=1`,
  "-vf", "drawbox=x=40:y=30:w=110:h=80:c=0x00FF00:t=fill,drawbox=x=170:y=40:w=70:h=60:c=0x3CC850:t=fill," +
    `drawbox=x=${WHITE_BOX.x}:y=${WHITE_BOX.y}:w=${WHITE_BOX.w}:h=${WHITE_BOX.h}:c=0xFFFFFF:t=fill,drawbox=x=40:y=125:w=60:h=40:c=0x000000:t=fill`,
  "-frames:v", "1", src,
]);
const srcRGBA = run("ffmpeg", ["-v", "error", "-i", src, "-f", "rawvideo", "-pix_fmt", "rgba", "-"]);

// 3. What the export draws.
const expected = cases.map((c) =>
  run("ffmpeg", ["-v", "error", "-i", src, "-vf", (c.filter ? c.filter + "," : "") + "format=rgba", "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgba", "-"]),
);

// 4. What the preview draws: the renderer in headless Edge, with each case's
// v2 settings migrated to an effect stack as the editor does.
const entry = path.join(tmp, "page.ts");
const mod = (f: string) => JSON.stringify(path.join(repo, "frontend/src/trim", f).replace(/\\/g, "/"));
fs.writeFileSync(
  entry,
  `import { FxRenderer } from ${mod("fx/renderer.ts")};
import { resolveStack } from ${mod("fx/registry.ts")};
import { migrateClipEffects } from ${mod("migrate.ts")};
(window as any).render = async (w: number, h: number, fx: any) => {
  const src = new Uint8Array(await (await fetch("/src")).arrayBuffer());
  const canvas = document.createElement("canvas");
  canvas.width = w; canvas.height = h;
  const gl = canvas.getContext("webgl2", { premultipliedAlpha: false })!;
  const tex = gl.createTexture()!;
  gl.bindTexture(gl.TEXTURE_2D, tex);
  gl.pixelStorei(gl.UNPACK_ALIGNMENT, 1);
  gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, w, h, 0, gl.RGBA, gl.UNSIGNED_BYTE, src);
  const r = new FxRenderer(gl);
  const out = r.apply(tex, w, h, resolveStack(migrateClipEffects(fx.chroma, fx.filters), 0));
  const fbo = gl.createFramebuffer();
  gl.bindFramebuffer(gl.FRAMEBUFFER, fbo);
  gl.framebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, out, 0);
  const px = new Uint8Array(w * h * 4);
  gl.readPixels(0, 0, w, h, gl.RGBA, gl.UNSIGNED_BYTE, px);
  r.dispose();
  let s = "";
  for (let i = 0; i < px.length; i += 0x8000) s += String.fromCharCode(...px.subarray(i, i + 0x8000));
  return { renderer: String(gl.getParameter(gl.RENDERER)), pixels: btoa(s) };
};`,
);
// The app's "@/" imports resolve to frontend/src.
const alias: import("bun").BunPlugin = {
  name: "at-alias",
  setup(b) {
    b.onResolve({ filter: /^@\// }, (a) => ({ path: Bun.resolveSync("./" + a.path.slice(2), path.join(repo, "frontend/src")) }));
  },
};
const built = await Bun.build({ entrypoints: [entry], target: "browser", plugins: [alias] });
if (!built.success) throw new Error(built.logs.join("\n"));
const js = await built.outputs[0].text();
const server = Bun.serve({
  port: 0,
  fetch(req) {
    const p = new URL(req.url).pathname;
    if (p === "/src") return new Response(srcRGBA);
    if (p === "/page.js") return new Response(js, { headers: { "content-type": "text/javascript" } });
    return new Response(`<!doctype html><script src="/page.js"></script>`, { headers: { "content-type": "text/html" } });
  },
});

if (!EDGE) throw new Error("Microsoft Edge not found");
const port = 9300 + Math.floor(Math.random() * 500);
const edge = spawn(EDGE, [
  "--headless=new", `--remote-debugging-port=${port}`, `--user-data-dir=${path.join(tmp, "edge")}`,
  "--no-first-run", "--disable-extensions", "--use-angle=swiftshader", "--enable-unsafe-swiftshader", "about:blank",
]);
let failed = 0;
try {
  let wsUrl = "";
  for (let i = 0; i < 100 && !wsUrl; i++) {
    try {
      const t = await (await fetch(`http://127.0.0.1:${port}/json/new?http://127.0.0.1:${server.port}/`, { method: "PUT" })).json();
      wsUrl = t.webSocketDebuggerUrl;
    } catch {
      await Bun.sleep(100);
    }
  }
  if (!wsUrl) throw new Error("Edge didn't start");
  const ws = new WebSocket(wsUrl);
  await new Promise((res, rej) => ((ws.onopen = res), (ws.onerror = rej)));
  let seq = 0;
  const pending = new Map<number, (v: any) => void>();
  ws.onmessage = (e) => {
    const m = JSON.parse(String(e.data));
    pending.get(m.id)?.(m);
    pending.delete(m.id);
  };
  const evaluate = async (expression: string) => {
    const id = ++seq;
    ws.send(JSON.stringify({ id, method: "Runtime.evaluate", params: { expression, awaitPromise: true, returnByValue: true } }));
    const m: any = await new Promise((res) => pending.set(id, res));
    if (m.result?.exceptionDetails) throw new Error(JSON.stringify(m.result.exceptionDetails));
    return m.result?.result?.value;
  };
  for (let i = 0; i < 100 && !(await evaluate("typeof window.render === 'function'")); i++) await Bun.sleep(100);

  // 5. Compare.
  console.log(`effects parity, ${W}x${H}, limits mean <= ${MAX_MEAN}, p99 <= ${MAX_P99}`);
  for (const [i, c] of cases.entries()) {
    const fx = { chroma: c.chroma, filters: c.filters };
    const got = await evaluate(`render(${W}, ${H}, ${JSON.stringify(fx)})`);
    if (i === 0) console.log(`renderer: ${got.renderer}`);
    const preview = Buffer.from(got.pixels, "base64");
    const exp = expected[i];
    // Skip the frame's edge, where the two blurs treat the border differently.
    const border = Math.ceil(((c.filters as any).blur * H) / 1080) + 2;
    const diffs: number[] = [];
    for (let y = border; y < H - border; y++) {
      for (let x = border; x < W - border; x++) {
        const o = (y * W + x) * 4;
        const a1 = exp[o + 3] / 255;
        const a2 = preview[o + 3] / 255;
        for (let k = 0; k < 3; k++) diffs.push(Math.abs(exp[o + k] * a1 - preview[o + k] * a2));
        diffs.push(Math.abs(exp[o + 3] - preview[o + 3]));
      }
    }
    diffs.sort((a, b) => a - b);
    const mean = diffs.reduce((s, d) => s + d, 0) / diffs.length;
    const p99 = diffs[Math.floor(diffs.length * 0.99)];
    const max = diffs[diffs.length - 1];
    let ok = mean <= MAX_MEAN && p99 <= MAX_P99;
    if (c.chroma) {
      // A green screen leaves white opaque, in the export and the preview.
      const o = ((WHITE_BOX.y + WHITE_BOX.h / 2) * W + WHITE_BOX.x + WHITE_BOX.w / 2) * 4 + 3;
      if (exp[o] !== 255 || preview[o] !== 255) {
        console.log(`     white box alpha: export ${exp[o]}, preview ${preview[o]} (want 255)`);
        ok = false;
      }
    }
    if (!ok) failed++;
    console.log(`${ok ? "ok  " : "FAIL"} ${c.name.padEnd(28)} mean ${mean.toFixed(2).padStart(5)}  p99 ${p99.toFixed(1).padStart(5)}  max ${max.toFixed(1).padStart(5)}`);
    if (process.env.FX_PARITY_KEEP) {
      fs.writeFileSync(path.join(tmp, `${i}-export.rgba`), exp);
      fs.writeFileSync(path.join(tmp, `${i}-preview.rgba`), preview);
    }
  }
  ws.close();
} finally {
  edge.kill();
  server.stop(true);
  // Edge may still hold its profile for a moment after it's killed.
  if (!process.env.FX_PARITY_KEEP) for (let i = 0; i < 20; i++) {
    try {
      fs.rmSync(tmp, { recursive: true, force: true });
      break;
    } catch {
      await Bun.sleep(250);
    }
  }
  else console.log(`kept ${tmp}`);
}
console.log(failed ? `${failed} case(s) failed` : "all cases match");
process.exit(failed ? 1 : 0);
