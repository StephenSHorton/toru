// Frame times on the whiteboard while it pans, zooms and drags a block.
//
// Measures board-perf.html (the whiteboard window on a board in memory) in a
// browser that serves the Chrome DevTools Protocol, driving it with
// synthetic input only: it never moves the real mouse or takes focus.
//
//   bunx vite --host 127.0.0.1 --port 47830          # in another terminal
//   bun scripts/board-perf.ts --cdp 9339 --board <file.toruboard> [--url http://127.0.0.1:47830] [--runs 3] [--only pan,zoom] [--no-profile] [--theme light|dark] [--json out.json]
//
// --cdp is the port of a WebView2 (or Edge) started with
// --remote-debugging-port. For WebView2, a tiny Wails app with that browser
// argument, WS_EX_NOACTIVATE and IgnoreMouseEvents keeps it out of the way.
// Without --board the sample board opens.
//
// The actions: pan (drag empty space), scroll (the wheel, which pans), zoom
// (Ctrl and the wheel), drag (a block, then drop it) and select (click one
// block after another). Per action it prints the time between frames
// (requestAnimationFrame): median, 95th and 99th percentile, worst, frames per second, and how many
// frames took longer than 1.5 refresh intervals; then where the main thread's
// time went (script, style, layout) and the functions that took the most.
import { readFileSync, writeFileSync } from "node:fs";

const arg = (name: string, fallback?: string) => {
  const i = process.argv.indexOf(`--${name}`);
  return i > 0 ? process.argv[i + 1] : fallback;
};
const cdpPort = arg("cdp", "9339");
const base = arg("url", "http://127.0.0.1:47830")!.replace(/\/$/, "");
const boardFile = arg("board");
const runs = Number(arg("runs", "3"));
const jsonOut = arg("json");
// The whiteboard theme to measure in (light, the default, or dark).
const theme = arg("theme");
const only = arg("only")?.split(",");
// The CPU profiler's sampling can itself cost a frame now and then; leave it
// off for clean frame times.
const profiling = !process.argv.includes("--no-profile");

// ── CDP ──────────────────────────────────────────────────────────────────

type Target = { type: string; url: string; webSocketDebuggerUrl: string };
const targets = (await (await fetch(`http://127.0.0.1:${cdpPort}/json`)).json()) as Target[];
const page = targets.find((t) => t.type === "page");
if (!page) throw new Error(`no page on CDP port ${cdpPort}`);
const ws = new WebSocket(page.webSocketDebuggerUrl);
await new Promise((ok, fail) => {
  ws.onopen = ok;
  ws.onerror = fail;
});
let nextId = 1;
const waiting = new Map<number, { ok: (v: any) => void; fail: (e: Error) => void }>();
const listeners: ((method: string, params: any) => void)[] = [];
ws.onmessage = (ev) => {
  const msg = JSON.parse(String(ev.data));
  if (msg.id && waiting.has(msg.id)) {
    const w = waiting.get(msg.id)!;
    waiting.delete(msg.id);
    if (msg.error) w.fail(new Error(`${msg.error.message}`));
    else w.ok(msg.result);
  } else if (msg.method) {
    for (const l of listeners) l(msg.method, msg.params);
  }
};
const send = <T = any>(method: string, params: object = {}) =>
  new Promise<T>((ok, fail) => {
    const id = nextId++;
    waiting.set(id, { ok, fail });
    ws.send(JSON.stringify({ id, method, params }));
  });
const evaluate = async <T = any>(expression: string): Promise<T> => {
  const r = await send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true });
  if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description ?? r.exceptionDetails.text);
  return r.result.value as T;
};
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

// ── Load the board ───────────────────────────────────────────────────────

await send("Page.enable");
await send("Runtime.enable");
await send("Performance.enable");
await send("Profiler.enable");
if (boardFile) {
  const doc = JSON.parse(readFileSync(boardFile, "utf8"));
  await send("Page.addScriptToEvaluateOnNewDocument", { source: `window.__perfBoard = ${JSON.stringify(doc)};` });
}
// Opens the board afresh, fitted to the window, as each action starts.
async function load() {
  const loaded = new Promise<void>((ok) => listeners.push((m) => m === "Page.loadEventFired" && ok()));
  await send("Page.navigate", { url: `${base}/board-perf.html${theme ? `?theme=${theme}` : ""}` });
  await loaded;
  // The board fits itself to the window once it has a size; let it settle.
  for (let i = 0; i < 50 && !(await evaluate<boolean>(`!!document.querySelector('[data-slot="board-block"]')`)); i++) await sleep(100);
  await sleep(1500);
}
await load();

const info = await evaluate<{ w: number; h: number; dpr: number; blocks: number; lines: number; ink: number; theme?: string }>(`({
  theme: document.documentElement.dataset.theme,
  w: innerWidth, h: innerHeight, dpr: devicePixelRatio,
  blocks: document.querySelectorAll('[data-slot="board-block"]:not([data-ink])').length,
  lines: document.querySelectorAll('path[data-slot="board-line"]').length,
  ink: document.querySelectorAll('[data-slot="board-block"][data-ink]').length,
})`);
// The refresh interval: the median gap between frames while nothing happens.
const idle = await evaluate<number[]>(`(async () => { __perf.start(); await new Promise(r => setTimeout(r, 1000)); return __perf.stop(); })()`);
const refresh = median(idle);
console.log(
  `board: ${info.blocks} blocks, ${info.lines} links, ${info.ink} ink strokes; ${info.theme ?? "no"} theme; window ${info.w}×${info.h} at ${info.dpr}×; refresh ${(1000 / refresh).toFixed(0)} Hz (${refresh.toFixed(2)} ms)`,
);

// ── Where to press ───────────────────────────────────────────────────────

// An empty spot on the board (pan), and plain bodies of blocks (drag,
// select), found again before each run since the board moves.
type Point = { x: number; y: number };
const findSpots = () =>
  evaluate<{ empty: Point | null; blocks: Point[] }>(`(() => {
  const board = document.querySelector('[data-slot="board"]');
  const r = board.getBoundingClientRect();
  let empty = null;
  const blocks = [], seen = new Set();
  // Away from the toolbars along the edges; frames cover most of a full
  // board, so empty space may be only the gaps between them.
  for (let y = r.top + 80; y < r.bottom - 80; y += 11) {
    for (let x = r.left + 80; x < r.right - 80; x += 13) {
      const el = document.elementFromPoint(x, y);
      if (!el) continue;
      if (!empty && (el === board || el.hasAttribute('data-board-empty'))) empty = { x, y };
      const b = el.closest('[data-slot="board-block"]');
      if (b && !seen.has(b) && !b.hasAttribute('data-ink') && b.dataset.type !== 'frame' && (el === b || el.closest('[data-slot="board-block-body"]')) && !el.closest('${"input, textarea, select, button, a[href], label, [contenteditable], [role=checkbox], [role=radio], [role=slider], [role=textbox], [data-slot=rating]"}')) {
        seen.add(b);
        blocks.push({ x, y });
      }
    }
  }
  return { empty, blocks };
})()`);
type Spots = Awaited<ReturnType<typeof findSpots>>;
function spot(found: Spots, kind: "empty" | "block") {
  const at = kind === "empty" ? found.empty : found.blocks[0];
  if (!at) throw new Error(`no ${kind === "empty" ? "empty spot" : "block"} to press on`);
  return at;
}

// ── Actions ──────────────────────────────────────────────────────────────

const mouse = (type: string, x: number, y: number, extra: object = {}) =>
  send("Input.dispatchMouseEvent", { type, x, y, button: "left", buttons: type === "mouseReleased" ? 0 : 1, pointerType: "mouse", ...extra });

const DURATION = 2500;
// Drags from a point in a figure of eight that ends where it began, one move
// every few ms (a mouse reports at hundreds of Hz), for DURATION.
async function dragFrom(at: Point, size: number) {
  await mouse("mouseMoved", at.x, at.y, { buttons: 0 });
  await mouse("mousePressed", at.x, at.y, { clickCount: 1 });
  const t0 = performance.now();
  let x = at.x,
    y = at.y;
  while (performance.now() - t0 < DURATION) {
    const turn = (2 * Math.PI * (performance.now() - t0)) / DURATION;
    x = at.x + Math.sin(turn) * size;
    y = at.y + Math.sin(turn * 2) * size * 0.4;
    await mouse("mouseMoved", x, y);
    await sleep(2);
  }
  await mouse("mouseReleased", x, y, { clickCount: 1 });
}

async function wheelZoom(at: Point) {
  const t0 = performance.now();
  let i = 0;
  while (performance.now() - t0 < DURATION) {
    // In for a while, then out, like a person rolling the wheel with Ctrl
    // held (the wheel alone pans).
    const dir = Math.floor(i / 25) % 2 ? 1 : -1;
    await send("Input.dispatchMouseEvent", { type: "mouseWheel", x: at.x, y: at.y, deltaX: 0, deltaY: dir * 40, modifiers: 2, pointerType: "mouse" });
    i++;
    await sleep(6);
  }
}

async function wheelPan(at: Point) {
  const t0 = performance.now();
  let i = 0;
  while (performance.now() - t0 < DURATION) {
    const dir = Math.floor(i / 40) % 2 ? 1 : -1;
    await send("Input.dispatchMouseEvent", { type: "mouseWheel", x: at.x, y: at.y, deltaX: 0, deltaY: dir * 20, pointerType: "mouse" });
    i++;
    await sleep(4);
  }
}

// Clicks one block after another, so the selection changes each time.
async function selectBlocks({ blocks }: Spots) {
  const t0 = performance.now();
  for (let i = 0; performance.now() - t0 < DURATION; i++) {
    const at = blocks[i % blocks.length];
    await mouse("mouseMoved", at.x, at.y, { buttons: 0 });
    await mouse("mousePressed", at.x, at.y, { clickCount: 1 });
    await mouse("mouseReleased", at.x, at.y, { clickCount: 1 });
    await sleep(250);
  }
}

const actions: Record<string, (spots: Spots) => Promise<void>> = {
  pan: (s) => dragFrom(spot(s, "empty"), 260),
  scroll: (s) => wheelPan(spot(s, "empty")),
  zoom: (s) => wheelZoom(spot(s, "empty")),
  drag: (s) => dragFrom(spot(s, "block"), 180),
  select: selectBlocks,
};

// ── Measure ──────────────────────────────────────────────────────────────

function median(a: number[]) {
  return pct(a, 50);
}
function pct(a: number[], p: number) {
  if (!a.length) return NaN;
  const s = [...a].sort((x, y) => x - y);
  return s[Math.min(s.length - 1, Math.floor((p / 100) * s.length))];
}
async function metrics() {
  const { metrics } = await send<{ metrics: { name: string; value: number }[] }>("Performance.getMetrics");
  return Object.fromEntries(metrics.map((m) => [m.name, m.value]));
}

type Profile = { nodes: { id: number; callFrame: { functionName: string; url: string; lineNumber: number } }[]; samples: number[]; timeDeltas: number[] };
function topFunctions(profile: Profile, n = 8) {
  const byId = new Map(profile.nodes.map((node) => [node.id, node]));
  const self = new Map<string, number>();
  profile.samples.forEach((id, i) => {
    const node = byId.get(id)!;
    const f = node.callFrame;
    if (["(idle)", "(program)", "(root)"].includes(f.functionName)) return;
    const file = f.url.split("/").pop()?.split("?")[0] ?? "";
    const key = `${f.functionName || "(anonymous)"} ${file}:${f.lineNumber + 1}`;
    self.set(key, (self.get(key) ?? 0) + (profile.timeDeltas[i] ?? 0) / 1000);
  });
  return [...self.entries()].sort((a, b) => b[1] - a[1]).slice(0, n);
}

const results: Record<string, any> = { board: info, refreshMs: refresh, actions: {} };
for (const [name, run] of Object.entries(actions)) {
  if (only && !only.includes(name)) continue;
  await load();
  const all: number[] = [];
  const worstByRun: number[] = [];
  const before = await metrics();
  if (profiling) {
    await send("Profiler.setSamplingInterval", { interval: 200 });
    await send("Profiler.start");
  }
  for (let r = 0; r < runs; r++) {
    const spots = await findSpots();
    await evaluate(`__perf.start()`);
    await run(spots);
    // Keep recording a little past the release, which commits the change.
    await sleep(300);
    const got = await evaluate<number[]>(`__perf.stop()`);
    worstByRun.push(Math.max(...got));
    all.push(...got);
    await sleep(400);
  }
  const profile = profiling ? (await send<{ profile: Profile }>("Profiler.stop")).profile : null;
  const after = await metrics();
  const busy = (k: string) => ((after[k] - before[k]) * 1000) / (runs * DURATION / 1000);
  const slow = all.filter((d) => d > refresh * 1.5).length;
  const row = {
    frames: all.length,
    p50: median(all),
    p95: pct(all, 95),
    p99: pct(all, 99),
    worst: Math.max(...all),
    worstByRun,
    fps: 1000 / (all.reduce((a, b) => a + b, 0) / all.length),
    slowPct: (100 * slow) / all.length,
    // Main-thread milliseconds per second of the action.
    scriptMsPerS: busy("ScriptDuration"),
    styleMsPerS: busy("RecalcStyleDuration"),
    layoutMsPerS: busy("LayoutDuration"),
    top: profile ? topFunctions(profile) : [],
  };
  results.actions[name] = row;
  console.log(
    `\n${name}: ${row.frames} frames, median ${row.p50.toFixed(2)} ms, p95 ${row.p95.toFixed(2)}, p99 ${row.p99.toFixed(2)}, worst ${row.worst.toFixed(1)} (by run: ${row.worstByRun.map((w) => w.toFixed(0)).join(", ")}); ${row.fps.toFixed(0)} fps; ${row.slowPct.toFixed(1)}% frames over 1.5× refresh`,
  );
  console.log(
    `  main thread per second: script ${row.scriptMsPerS.toFixed(0)} ms, style ${row.styleMsPerS.toFixed(0)} ms, layout ${row.layoutMsPerS.toFixed(0)} ms`,
  );
  for (const [fn, ms] of row.top) console.log(`    ${ms.toFixed(0).padStart(5)} ms  ${fn}`);
}
if (jsonOut) writeFileSync(jsonOut, JSON.stringify(results, null, 2));
ws.close();
process.exit(0);
