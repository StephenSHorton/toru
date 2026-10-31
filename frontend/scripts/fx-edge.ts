// fx-edge is what the effect scripts (effects-parity, migration-golden,
// fx-perf) share: the Go and ffmpeg they shell out to, and a page bundled
// from frontend/src running in headless Edge, driven over CDP.
import { spawn, spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

export const repo = path.resolve(import.meta.dir, "../..");
export const goBin = fs.existsSync("C:/Program Files/Go/bin/go.exe") ? "C:/Program Files/Go/bin/go.exe" : "go";
const EDGE = [
  "C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe",
  "C:/Program Files/Microsoft/Edge/Application/msedge.exe",
].find((p) => fs.existsSync(p));

export function run(cmd: string, args: string[], env?: Record<string, string>): Buffer {
  const r = spawnSync(cmd, args, { cwd: repo, env: { ...process.env, ...env }, maxBuffer: 1 << 28 });
  if (r.status !== 0) throw new Error(`${cmd} ${args.join(" ")}\n${r.stderr?.toString() ?? r.error}`);
  return r.stdout;
}

// ffmpegFor is the ffmpeg that renders the export side: $FX_FFMPEG when set
// (e.g. "ffmpeg" for the one on PATH), else the pinned LGPL build Toru ships,
// downloaded once and SHA-256 verified by internal/fx/fxtest.
export function ffmpegFor(tmp: string): string {
  if (process.env.FX_FFMPEG) return process.env.FX_FFMPEG;
  const out = path.join(tmp, "ffmpeg-path.txt");
  run(goBin, ["test", "./internal/fx/", "-run", "^TestPinnedFFmpegPath$", "-count=1"], {
    TORU_FFMPEG_PINNED: "1",
    TORU_FFMPEG_PATH_OUT: out,
  });
  return fs.readFileSync(out, "utf8").trim();
}

export function tempDir(name: string): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), `toru-${name}-`));
}

// mod is a frontend/src/trim module as an import specifier for a page.
export const mod = (f: string) => JSON.stringify(path.join(repo, "frontend/src/trim", f).replace(/\\/g, "/"));

// The app's "@/" imports resolve to frontend/src.
const alias: import("bun").BunPlugin = {
  name: "at-alias",
  setup(b) {
    b.onResolve({ filter: /^@\// }, (a) => ({ path: Bun.resolveSync("./" + a.path.slice(2), path.join(repo, "frontend/src")) }));
  },
};

export type Page = { evaluate: (expression: string) => Promise<any>; close: () => Promise<void> };

// openPage bundles code (a TS module that sets window functions) and opens
// it in headless Edge. files are served at their keys ("/src" → bytes).
// gpu: false renders with SwiftShader (deterministic, what parity uses);
// true asks for the real GPU through ANGLE D3D11 (fx-perf).
export async function openPage(tmp: string, code: string, files: Record<string, Uint8Array>, opts: { gpu?: boolean } = {}): Promise<Page> {
  const entry = path.join(tmp, "page.ts");
  fs.writeFileSync(entry, code);
  const built = await Bun.build({ entrypoints: [entry], target: "browser", plugins: [alias] });
  if (!built.success) throw new Error(built.logs.join("\n"));
  const js = await built.outputs[0].text();
  const server = Bun.serve({
    port: 0,
    fetch(req) {
      const p = new URL(req.url).pathname;
      if (files[p]) return new Response(files[p]);
      if (p === "/page.js") return new Response(js, { headers: { "content-type": "text/javascript" } });
      return new Response(`<!doctype html><script src="/page.js"></script>`, { headers: { "content-type": "text/html" } });
    },
  });
  if (!EDGE) throw new Error("Microsoft Edge not found");
  const port = 9300 + Math.floor(Math.random() * 500);
  const profile = path.join(tmp, "edge");
  const gl = opts.gpu
    ? ["--use-angle=d3d11", "--enable-gpu", "--ignore-gpu-blocklist", "--disable-gpu-vsync", "--disable-frame-rate-limit"]
    : ["--use-angle=swiftshader", "--enable-unsafe-swiftshader"];
  const edge = spawn(EDGE, [
    "--headless=new", `--remote-debugging-port=${port}`, `--user-data-dir=${profile}`,
    "--no-first-run", "--disable-extensions", ...gl, "about:blank",
  ]);
  const close = async () => {
    edge.kill();
    server.stop(true);
    // Edge may still hold its profile for a moment after it's killed.
    for (let i = 0; i < 20; i++) {
      try {
        fs.rmSync(profile, { recursive: true, force: true });
        break;
      } catch {
        await Bun.sleep(250);
      }
    }
  };
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
    for (let i = 0; i < 100 && !(await evaluate("document.readyState === 'complete' && typeof window.ready !== 'undefined'")); i++) await Bun.sleep(100);
    return {
      evaluate,
      close: async () => {
        ws.close();
        await close();
      },
    };
  } catch (e) {
    await close();
    throw e;
  }
}

// RENDER_PAGE is a page that sets window.render(w, h, effects, t): the
// frame at /src (raw RGBA, w×h) through FxRenderer with the stack resolved
// at t, read back as base64 RGBA, plus the GL renderer's name. effects are
// EffectInstances (keyframe t in seconds); with chroma/filters instead, the
// v2 settings are migrated the way the editor migrates a v2 clip.
export const RENDER_PAGE = `import { FxRenderer } from ${mod("fx/renderer.ts")};
import { resolveStack } from ${mod("fx/registry.ts")};
import { migrateClipEffects } from ${mod("migrate.ts")};
(window as any).migrate = (chroma: any, filters: any) => migrateClipEffects(chroma, filters);
(window as any).render = async (w: number, h: number, fx: any, t = 0) => {
  const src = new Uint8Array(await (await fetch("/src")).arrayBuffer());
  const canvas = document.createElement("canvas");
  canvas.width = w; canvas.height = h;
  const gl = canvas.getContext("webgl2", { premultipliedAlpha: false })!;
  const tex = gl.createTexture()!;
  gl.bindTexture(gl.TEXTURE_2D, tex);
  gl.pixelStorei(gl.UNPACK_ALIGNMENT, 1);
  gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, w, h, 0, gl.RGBA, gl.UNSIGNED_BYTE, src);
  const r = new FxRenderer(gl);
  const out = r.apply(tex, w, h, resolveStack(fx.effects ?? migrateClipEffects(fx.chroma, fx.filters), t), t);
  const fbo = gl.createFramebuffer();
  gl.bindFramebuffer(gl.FRAMEBUFFER, fbo);
  gl.framebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, out, 0);
  const px = new Uint8Array(w * h * 4);
  gl.readPixels(0, 0, w, h, gl.RGBA, gl.UNSIGNED_BYTE, px);
  r.dispose();
  let s = "";
  for (let i = 0; i < px.length; i += 0x8000) s += String.fromCharCode(...px.subarray(i, i + 0x8000));
  return { renderer: String(gl.getParameter(gl.RENDERER)), pixels: btoa(s) };
};
(window as any).ready = true;`;

export type Limits = { mode: "pixels" | "stats" | "none"; mean?: number; p99?: number; reason?: string };

// compare holds two RGBA frames of the same size to limits, skipping
// border pixels at the edge. "pixels" compares colour premultiplied by alpha,
// plus alpha, per pixel and channel (0..255 levels): mean and p99 of the
// differences. "stats" (noise, grain: random per pixel on each side)
// compares each channel's mean and standard deviation over the frame
// instead, against mean and p99 as the allowed differences.
export function compare(exp: Uint8Array, got: Uint8Array, w: number, h: number, border: number, lim: Limits) {
  const at = (buf: Uint8Array, o: number, k: number) => (k === 3 ? buf[o + 3] : (buf[o + k] * buf[o + 3]) / 255);
  if (lim.mode === "stats") {
    let worstMean = 0;
    let worstStd = 0;
    for (let k = 0; k < 4; k++) {
      const s = [exp, got].map((buf) => {
        let sum = 0;
        let sq = 0;
        let n = 0;
        for (let y = border; y < h - border; y++)
          for (let x = border; x < w - border; x++) {
            const v = at(buf, (y * w + x) * 4, k);
            sum += v;
            sq += v * v;
            n++;
          }
        const mean = sum / n;
        return { mean, std: Math.sqrt(Math.max(0, sq / n - mean * mean)) };
      });
      worstMean = Math.max(worstMean, Math.abs(s[0].mean - s[1].mean));
      worstStd = Math.max(worstStd, Math.abs(s[0].std - s[1].std));
    }
    return { mean: worstMean, p99: worstStd, max: worstStd, ok: worstMean <= (lim.mean ?? 1) && worstStd <= (lim.p99 ?? 6) };
  }
  const diffs: number[] = [];
  for (let y = border; y < h - border; y++)
    for (let x = border; x < w - border; x++) {
      const o = (y * w + x) * 4;
      for (let k = 0; k < 4; k++) diffs.push(Math.abs(at(exp, o, k) - at(got, o, k)));
    }
  diffs.sort((a, b) => a - b);
  const mean = diffs.reduce((s, d) => s + d, 0) / diffs.length;
  const p99 = diffs[Math.floor(diffs.length * 0.99)];
  const max = diffs[diffs.length - 1];
  return { mean, p99, max, ok: mean <= (lim.mean ?? 1) && p99 <= (lim.p99 ?? 6) };
}
