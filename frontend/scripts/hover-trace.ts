// hover-trace.ts traces hovers in Toru's pages in headless Chrome (or Edge)
// with style invalidation tracking on, and prints what each hover restyled
// (whiteboard#56). With --looks it also takes pictures of the group-*
// looks, before and after.
//
// It serves the app with Vite twice, with and without the rewrite of
// Tailwind's group-* rules (group-selectors.ts), and on each:
//
// - opens hover-trace.html (the editor's timeline and library browsers on a
//   sample project) and whiteboards-preview.html (the main window's
//   Whiteboards page), moves the mouse over every element with a `group`
//   class and a spread of the rest, and reads the trace:
//   - "restyled": the elements Blink recalculated style for (the sum of
//     UpdateLayoutTree's elementCount), and the most for one hover;
//   - "subtree": style invalidations that took an element's whole subtree
//     (StyleInvalidatorInvalidationTracking where all descendants might be
//     invalid), with the elements they were on.
// - with --looks <out.html>: forces each group element's state (hover,
//   focus-visible, focus-within) through DevTools, as a pointer or a key
//   would, reads every computed property of it and its descendants, and
//   takes a picture of it; then compares before with after (the styles must
//   be identical; the pictures' pixel differences are reported) and writes
//   them side by side to out.html.
//
//   bun scripts/hover-trace.ts [--json out.json] [--html out.html] [--looks looks.html]
import { build, createServer, preview } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { groupSelectors } from "../group-selectors";

const arg = (name: string) => {
  const i = process.argv.indexOf(`--${name}`);
  return i > 0 ? process.argv[i + 1] : undefined;
};
const root = path.resolve(import.meta.dir, "..");

const PAGES = [
  { name: "editor (timeline and library)", url: "/hover-trace.html" },
  { name: "Whiteboards page", url: "/whiteboards-preview.html?page=whiteboards" },
];

// The looks pictured: a page, the group element (CSS selector, nth match),
// the states forced on it, and what the picture shows.
// shot: the element pictured, inside the group one (default: the group one).
const LOOKS: { page: string; sel: string; nth?: number; shot?: string; states: string[]; label: string }[] = [
  { page: "/hover-trace.html", sel: "#effects fieldset > button.group", states: [], label: "Effect tile, at rest" },
  { page: "/hover-trace.html", sel: "#effects fieldset > button.group", states: ["hover"], label: "Effect tile, hovered: group-enabled:group-hover (border, + badge)" },
  { page: "/hover-trace.html", sel: "#effects fieldset > button.group", states: ["focus", "focus-visible"], label: "Effect tile, keyboard focus: group-focus-visible (ring border)" },
  { page: "/hover-trace.html", sel: "#effects-off fieldset > button.group", states: ["hover"], label: "Effect tile, disabled, hovered: group-enabled:group-hover stays off" },
  // The second filter tile: the first, None, is the one chosen, which has no hover look.
  { page: "/hover-trace.html", sel: "#filters .group", nth: 1, states: [], label: "Filter tile, at rest" },
  { page: "/hover-trace.html", sel: "#filters .group", nth: 1, states: ["hover"], label: "Filter tile, hovered: group-hover (border)" },
  { page: "/hover-trace.html", sel: "#filters .group", nth: 1, states: ["focus-within"], label: "Filter tile, focus inside: group-focus-within (ring border)" },
  { page: "/hover-trace.html", sel: "#transitions button.group", states: [], label: "Transition tile, at rest" },
  { page: "/hover-trace.html", sel: "#transitions button.group", states: ["hover"], label: "Transition tile, hovered: group-hover (primary border)" },
  { page: "/hover-trace.html", sel: "#transitions button.group", states: ["focus", "focus-visible"], label: "Transition tile, keyboard focus: group-focus-visible" },
  { page: "/hover-trace.html", sel: "#timeline .group\\/edge", states: [], label: "Timeline clip's trim edge, at rest" },
  { page: "/hover-trace.html", sel: "#timeline .group\\/edge", states: ["hover"], label: "Timeline clip's trim edge, hovered: group-hover/edge (a named group)" },
  // Its thumbnail only: the card's caption shows the time it was opened.
  { page: "/whiteboards-preview.html?page=whiteboards", sel: ".group", shot: "button", states: [], label: "Whiteboard card, at rest" },
  { page: "/whiteboards-preview.html?page=whiteboards", sel: ".group", shot: "button", states: ["hover"], label: "Whiteboard card, hovered: group-hover (icon grows)" },
];

// serve runs the pages on port, with or without the rewrite: built for
// production and previewed ("build", the CSS the app ships: flat, optimized
// rules), or on the dev server ("dev": Tailwind's rules still nested).
async function serve(mode: "build" | "dev", rewrite: boolean, port: number): Promise<{ close: () => Promise<void> }> {
  const base = {
    configFile: false as const,
    root,
    logLevel: "silent" as const,
    resolve: { alias: { "@": fileURLToPath(new URL("../src", import.meta.url)) } },
    css: { postcss: { plugins: rewrite ? [groupSelectors()] : [] } },
    plugins: [react(), tailwindcss()],
  };
  if (mode === "dev") {
    const s = await createServer({ ...base, server: { host: "127.0.0.1", port, strictPort: true, fs: { allow: [path.resolve(root, "..")] } } });
    await s.listen();
    return { close: () => s.close() };
  }
  const outDir = fs.mkdtempSync(path.join(os.tmpdir(), "toru-hover-build-"));
  await build({
    ...base,
    mode: "production",
    build: { outDir, emptyOutDir: true, rollupOptions: { input: PAGES.map((p) => path.join(root, p.url.slice(1).split("?")[0])) } },
  });
  const s = await preview({ ...base, build: { outDir }, preview: { host: "127.0.0.1", port, strictPort: true } });
  return {
    close: async () => {
      await new Promise<void>((r) => s.httpServer.close(() => r()));
      fs.rmSync(outDir, { recursive: true, force: true });
    },
  };
}

const browser = [
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  "C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe",
  "C:/Program Files/Microsoft/Edge/Application/msedge.exe",
  "C:/Program Files/Google/Chrome/Application/chrome.exe",
].find((p) => fs.existsSync(p));
if (!browser) throw new Error("no Chrome or Edge found");

type Cdp = { send: (method: string, params?: any) => Promise<any>; on: (method: string, f: (p: any) => void) => void; close: () => void };

async function open(debugPort: number, url: string): Promise<Cdp> {
  let wsUrl = "";
  for (let i = 0; i < 100 && !wsUrl; i++) {
    try {
      wsUrl = (await (await fetch(`http://127.0.0.1:${debugPort}/json/new?about:blank`, { method: "PUT" })).json()).webSocketDebuggerUrl;
    } catch {
      await Bun.sleep(100);
    }
  }
  if (!wsUrl) throw new Error("the browser didn't start");
  const ws = new WebSocket(wsUrl);
  await new Promise((res, rej) => ((ws.onopen = res), (ws.onerror = rej)));
  let seq = 0;
  const pending = new Map<number, { ok: (v: any) => void; fail: (e: Error) => void }>();
  const handlers = new Map<string, ((p: any) => void)[]>();
  ws.onmessage = (e) => {
    const m = JSON.parse(String(e.data));
    if (m.id) {
      const p = pending.get(m.id);
      pending.delete(m.id);
      if (m.error) p?.fail(new Error(JSON.stringify(m.error)));
      else p?.ok(m.result);
    } else for (const h of handlers.get(m.method) ?? []) h(m.params);
  };
  const c: Cdp = {
    send: (method, params = {}) =>
      new Promise((ok, fail) => {
        const id = ++seq;
        pending.set(id, { ok, fail });
        ws.send(JSON.stringify({ id, method, params }));
      }),
    on: (method, f) => handlers.set(method, [...(handlers.get(method) ?? []), f]),
    close: () => ws.close(),
  };
  await c.send("Page.enable");
  await c.send("Emulation.setDeviceMetricsOverride", { width: 1600, height: 1000, deviceScaleFactor: 1, mobile: false });
  await c.send("Page.navigate", { url });
  // The page, its effect thumbnails and fonts.
  await Bun.sleep(4000);
  return c;
}

const evaluate = async (c: Cdp, expression: string) => (await c.send("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true })).result.value;
const twoFrames = "new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)))";

// The points hovered: every element with a `group` class, then a spread of
// leaf elements, up to 60 in all.
const PICK = `(() => {
  const vis = (el) => { const r = el.getBoundingClientRect(); return r.width > 2 && r.height > 2 && r.bottom > 0 && r.right > 0 && r.top < innerHeight && r.left < innerWidth; };
  const all = [...document.body.querySelectorAll("*")];
  const shown = all.filter(vis);
  const groups = shown.filter((el) => [...el.classList].some((c) => c === "group" || c.startsWith("group/")));
  const rest = shown.filter((el) => !groups.includes(el) && el.children.length === 0);
  const room = Math.max(0, 60 - groups.length);
  const step = Math.max(1, Math.floor(rest.length / Math.max(1, room)));
  const pick = [...groups, ...rest.filter((_, i) => i % step === 0).slice(0, room)];
  // What each hovered element is: its group class and the part of the page it's in, or "other".
  const kind = (el) => { const g = [...el.classList].find((c) => c === "group" || c.startsWith("group/")); if (!g) return "other elements"; const part = el.closest("[id]:not(#root)"); return g + " " + el.tagName.toLowerCase() + (part ? " in #" + part.id : ""); };
  return { elements: all.length, groups: groups.length, points: pick.map((el) => { const r = el.getBoundingClientRect(); return [Math.round(r.left + Math.min(r.width / 2, 20)), Math.round(r.top + r.height / 2)]; }), kinds: pick.map(kind) };
})()`;

type Trace = {
  page: string;
  elements: number;
  groups: number;
  hovers: number;
  restyled: number;
  worst: number;
  subtree: number;
  subtreeRecalc: number;
  selectors: Record<string, number>;
  byKind: Record<string, { hovers: number; restyled: number }>;
};

async function trace(c: Cdp, page: string): Promise<Trace> {
  const picked = await evaluate(c, PICK);
  await c.send("Input.dispatchMouseEvent", { type: "mouseMoved", x: 1, y: 999 });
  await evaluate(c, twoFrames);
  const events: any[] = [];
  const done = new Promise<void>((res) => c.on("Tracing.tracingComplete", () => res()));
  c.on("Tracing.dataCollected", (p) => events.push(...p.value));
  await c.send("Tracing.start", {
    transferMode: "ReportEvents",
    traceConfig: { includedCategories: ["devtools.timeline", "disabled-by-default-devtools.timeline", "disabled-by-default-devtools.timeline.invalidationTracking", "blink.user_timing"] },
  });
  // One hover at a time: a mark, the move, then two frames for its style update.
  for (const [i, [x, y]] of picked.points.entries()) {
    await evaluate(c, `performance.mark("hover-${i}")`);
    await c.send("Input.dispatchMouseEvent", { type: "mouseMoved", x, y });
    await evaluate(c, twoFrames);
  }
  await c.send("Tracing.end");
  await done;

  // Style updates, counted to the hover they followed (the marks split the trace).
  const perHover: number[] = picked.points.map(() => 0);
  const sorted = events.filter((e) => e.ts).sort((a, b) => a.ts - b.ts);
  let cur = -1;
  for (const e of sorted) {
    const m = /^hover-(\d+)$/.exec(e.name);
    if (m && e.cat?.includes("user_timing")) cur = Number(m[1]);
    const n = e.name === "UpdateLayoutTree" ? Number(e.args?.elementCount ?? e.args?.endData?.elementCount ?? 0) : 0;
    if (n && cur >= 0) perHover[cur] += n;
  }
  const byKind: Trace["byKind"] = {};
  picked.kinds.forEach((k: string, i: number) => {
    const b = (byKind[k] ??= { hovers: 0, restyled: 0 });
    b.hovers++;
    b.restyled += perHover[i];
  });
  const restyled = events.filter((e) => e.name === "UpdateLayoutTree").reduce((a, e) => a + Number(e.args?.elementCount ?? e.args?.endData?.elementCount ?? 0), 0);
  const selectors: Record<string, number> = {};
  let subtree = 0;
  // Elements Blink marked to restyle with their whole subtree.
  const subtreeRecalc = events.filter((e) => e.name === "StyleRecalcInvalidationTracking" && e.args?.data?.subtree === true).length;
  for (const e of events) {
    const d = e.args?.data;
    if (e.name !== "StyleInvalidatorInvalidationTracking" || !(d?.invalidationList ?? []).some((s: any) => s.allDescendantsMightBeInvalid || s.subtree)) continue;
    subtree++;
    const node = String(d.nodeName ?? "?").slice(0, 90);
    selectors[node] = (selectors[node] ?? 0) + 1;
  }
  return { page, elements: picked.elements, groups: picked.groups, hovers: picked.points.length, restyled, worst: Math.max(0, ...perHover), subtree, subtreeRecalc, selectors, byKind };
}

type Look = { label: string; png: string; box: string; styles: string; elements: number };

// Every computed property of an element and each of its descendants.
const STYLES = `function () {
  const els = [this, ...this.querySelectorAll("*")];
  return JSON.stringify(els.map((el) => { const cs = getComputedStyle(el); const o = {}; for (const p of [...cs].sort()) o[p] = cs.getPropertyValue(p); return [el.tagName, o]; }));
}`;

async function look(c: Cdp, l: (typeof LOOKS)[number]): Promise<Look> {
  await c.send("DOM.enable");
  await c.send("CSS.enable");
  const { root: doc } = await c.send("DOM.getDocument", { depth: -1 });
  const { nodeIds } = await c.send("DOM.querySelectorAll", { nodeId: doc.nodeId, selector: l.sel });
  const nodeId = nodeIds[l.nth ?? 0];
  if (!nodeId) throw new Error(`no ${l.sel} on ${l.page}`);
  await c.send("DOM.scrollIntoViewIfNeeded", { nodeId });
  // focus-within is set on the element whose descendant has focus: forced
  // on the group element itself here, as Blink matches it.
  await c.send("CSS.forcePseudoState", { nodeId, forcedPseudoClasses: l.states });
  await evaluate(c, twoFrames);
  await Bun.sleep(150);
  const shotId = l.shot ? (await c.send("DOM.querySelector", { nodeId, selector: l.shot })).nodeId : nodeId;
  const { model } = await c.send("DOM.getBoxModel", { nodeId: shotId });
  const [x0, y0, x1, , , y2] = model.border;
  const pad = 6;
  const clip = { x: Math.max(0, x0 - pad), y: Math.max(0, y0 - pad), width: Math.max(4, x1 - x0 + pad * 2), height: Math.max(4, y2 - y0 + pad * 2), scale: 2 };
  const { data } = await c.send("Page.captureScreenshot", { format: "png", clip, captureBeyondViewport: false });
  const { object } = await c.send("DOM.resolveNode", { nodeId });
  const styles = (await c.send("Runtime.callFunctionOn", { objectId: object.objectId, functionDeclaration: STYLES, returnByValue: true })).result.value as string;
  await c.send("CSS.forcePseudoState", { nodeId, forcedPseudoClasses: [] });
  return { label: l.label, png: data, box: `${Math.round(clip.width)}×${Math.round(clip.height)}`, styles, elements: JSON.parse(styles).length };
}

const profile = fs.mkdtempSync(path.join(os.tmpdir(), "toru-hover-trace-"));
const debugPort = 9300 + Math.floor(Math.random() * 500);
const proc = spawn(browser, ["--headless=new", `--remote-debugging-port=${debugPort}`, `--user-data-dir=${profile}`, "--no-first-run", "--disable-extensions", "--hide-scrollbars", "--window-size=1600,1000", "about:blank"], { stdio: "ignore" });

// The dev server first: a production build leaves NODE_ENV=production set in
// this process, which a dev server started after it can't run with.
const MODES = (arg("modes") ?? "dev,build").split(",").sort((a, b) => (a === "dev" ? -1 : b === "dev" ? 1 : 0)) as ("build" | "dev")[];
// The mode the looks and the by-element table use: the production build if traced.
const MAIN = MODES.includes("build") ? "build" : MODES[0];
// Keyed "<mode> before" and "<mode> after".
const traces: Record<string, Trace[]> = {};
const looks: Record<string, Look[]> = { before: [], after: [] };
const pixels: { differ: number; total: number; max: number }[] = [];
const wantLooks = arg("looks");
try {
  for (const mode of MODES)
  for (const [label, rewrite, port] of [
    ["before", false, mode === "build" ? 47826 : 47824],
    ["after", true, mode === "build" ? 47827 : 47825],
  ] as const) {
    const server = await serve(mode, rewrite, port);
    const key = `${mode} ${label}`;
    traces[key] = [];
    for (const p of PAGES) {
      const c = await open(debugPort, `http://127.0.0.1:${port}${p.url}`);
      const t = await trace(c, p.name);
      traces[key].push(t);
      console.log(`${key.padEnd(12)} ${p.name}: ${t.elements} elements, ${t.groups} groups on screen, ${t.hovers} hovers: ${t.restyled} restyled (at most ${t.worst} for one hover), whole-subtree: ${t.subtree} invalidations, ${t.subtreeRecalc} recalcs`);
      for (const [k, b] of Object.entries(t.byKind)) console.log(`             ${b.hovers}× ${k}: ${b.restyled} restyled`);
      c.close();
    }
    if (wantLooks && mode === MAIN) {
      for (const page of [...new Set(LOOKS.map((l) => l.page))]) {
        const c = await open(debugPort, `http://127.0.0.1:${port}${page}`);
        await c.send("Input.dispatchMouseEvent", { type: "mouseMoved", x: 1, y: 999 });
        // Transitions off, so a picture never lands mid-way through one,
        // and the effect thumbnails' canvases hidden: they draw when their
        // frame comes, so their pixels differ from load to load.
        await evaluate(c, `document.head.insertAdjacentHTML("beforeend", "<style>*,*::before,*::after{transition:none!important;animation:none!important;caret-color:transparent!important}canvas{visibility:hidden!important}</style>")`);
        for (const l of LOOKS.filter((x) => x.page === page)) looks[label].push(await look(c, l));
        c.close();
      }
    }
    await server.close();
  }
  if (wantLooks) {
    // The pictures compared pixel for pixel, in the browser.
    const c = await open(debugPort, "about:blank");
    for (let i = 0; i < looks.before.length; i++) {
      pixels.push(
        await evaluate(
          c,
          `(async () => {
            const load = async (b64) => { const img = new Image(); img.src = "data:image/png;base64," + b64; await img.decode(); const cv = new OffscreenCanvas(img.width, img.height); const g = cv.getContext("2d"); g.drawImage(img, 0, 0); return g.getImageData(0, 0, img.width, img.height); };
            const [a, b] = await Promise.all([load(${JSON.stringify(looks.before[i].png)}), load(${JSON.stringify(looks.after[i].png)})]);
            if (a.width !== b.width || a.height !== b.height) return { differ: -1, total: 0, max: 255 };
            let differ = 0, max = 0;
            for (let p = 0; p < a.data.length; p += 4) { let d = 0; for (let k = 0; k < 4; k++) d = Math.max(d, Math.abs(a.data[p + k] - b.data[p + k])); if (d) differ++; if (d > max) max = d; }
            return { differ, total: a.width * a.height, max };
          })()`,
        ),
      );
    }
    c.close();
  }
} finally {
  proc.kill();
  await Bun.sleep(300);
  fs.rmSync(profile, { recursive: true, force: true });
}

const json = arg("json");
if (json) fs.writeFileSync(json, JSON.stringify(traces, null, 2));

const style = `<style>body{font:14px system-ui,sans-serif;background:#16171a;color:#e8e8ea;padding:24px;margin:0}h2{font-size:18px;margin:0 0 8px}p{color:#a8a9ad;max-width:900px}table{border-collapse:collapse;margin:12px 0}td,th{border:1px solid #3a3b40;padding:6px 10px;text-align:right;vertical-align:middle}td:first-child,th:first-child{text-align:left}th{background:#222327;font-weight:600}b{color:#7fb4ff}code{font-size:12px;color:#c9c9cf}.ok{color:#6fd39a}.bad{color:#ff8a80}img{display:block;image-rendering:auto}</style>`;
const html = arg("html");
if (html) {
  const rows = MODES.flatMap((mode) =>
    PAGES.map((p, i) => {
      const b = traces[`${mode} before`][i];
      const a = traces[`${mode} after`][i];
      return `<tr><td>${p.name}</td><td>${mode === "build" ? "production build" : "dev server"}</td><td>${a.hovers}</td><td>${b.restyled}</td><td><b>${a.restyled}</b></td><td>${b.worst}</td><td><b>${a.worst}</b></td><td>${b.subtree} / ${b.subtreeRecalc}</td><td><b>${a.subtree} / ${a.subtreeRecalc}</b></td></tr>`;
    }),
  ).join("");
  const m = MAIN;
  const kinds = PAGES.flatMap((p, i) => {
    const b = traces[`${m} before`][i].byKind;
    const a = traces[`${m} after`][i].byKind;
    return Object.keys(b).map((k) => `<tr><td>${p.name}</td><td><code>${k}</code></td><td>${b[k].hovers}</td><td>${b[k].restyled}</td><td><b>${a[k]?.restyled ?? "?"}</b></td></tr>`);
  }).join("");
  fs.writeFileSync(
    html,
    `<!doctype html><meta charset="utf-8"><title>Hover trace</title>${style}
<h2>What a hover restyles, traced in Chrome with style invalidation tracking</h2>
<p>Headless Chrome, no Go behind it. The mouse moved over every element with a <code>group</code> class on screen, then a spread of other elements, two frames after each. <i>Before</i>: Tailwind's group rules as Tailwind builds them. <i>After</i>: rewritten by <code>group-selectors.ts</code>. Same pages, same hovers, in a production build and on the dev server.</p>
<table><tr><th>page</th><th>CSS</th><th>hovers</th><th>elements restyled, before</th><th>after</th><th>most for one hover, before</th><th>after</th><th>whole-subtree invalidations / recalcs, before</th><th>after</th></tr>${rows}</table>
<h2>By what was hovered (${m === "build" ? "production build" : "dev server"})</h2>
<table><tr><th>page</th><th>hovered</th><th>hovers</th><th>elements restyled, before</th><th>after</th></tr>${kinds}</table>`,
  );
}

if (wantLooks) {
  let same = 0;
  let failedLooks = 0;
  const rows = looks.before
    .map((b, i) => {
      const a = looks.after[i];
      const eq = a.styles === b.styles;
      const px = pixels[i];
      if (eq) same++;
      else {
        failedLooks++;
        const [x, y] = [JSON.parse(b.styles), JSON.parse(a.styles)];
        const diffs: string[] = [];
        x.forEach(([tag, o]: [string, Record<string, string>], k: number) => {
          for (const prop of new Set([...Object.keys(o), ...Object.keys(y[k]?.[1] ?? {})])) if (o[prop] !== y[k]?.[1]?.[prop]) diffs.push(`${tag} ${prop}: ${o[prop]} → ${y[k]?.[1]?.[prop]}`);
        });
        console.error(`${b.label}: ${diffs.slice(0, 5).join("; ")}`);
      }
      const pxText = px.differ === 0 ? "pixels identical" : `${px.differ} of ${px.total} pixels differ, by at most ${px.max}/255`;
      const [label, what] = b.label.split(/: (.*)/);
      return `<div class="look"><div class="t">${label}</div><div class="w">${what ?? "&nbsp;"}</div><div class="pair"><figure><img src="data:image/png;base64,${b.png}"><figcaption>before</figcaption></figure><figure><img src="data:image/png;base64,${a.png}"><figcaption>after</figcaption></figure></div><div class="${eq ? "ok" : "bad"}">${eq ? `✓ computed styles identical (${b.elements} el.)` : "✗ computed styles differ"} · ${pxText}</div></div>`;
    })
    .join("");
  fs.writeFileSync(
    wantLooks,
    `<!doctype html><meta charset="utf-8"><title>Group looks, before and after</title>${style}
<style>body{padding:14px}.grid{display:grid;grid-template-columns:repeat(5,1fr);gap:8px}.look{border:1px solid #3a3b40;padding:6px;font-size:11px}.t{font-weight:600;font-size:12px}.w{color:#a8a9ad;min-height:28px}.pair{display:flex;gap:4px;align-items:flex-start}figure{margin:0;flex:1;min-width:0}figure img{width:100%;height:auto;max-height:110px;object-fit:contain}figcaption{color:#77787d;font-size:10px}.ok,.bad{font-size:10px;margin-top:3px}p{font-size:12px;margin:4px 0 10px}</style>
<h2>Tailwind's group looks, before and after the rewrite (production build, headless Chrome)</h2>
<p>Each group element with its state forced through DevTools, as the pointer or keyboard sets it. Before: Tailwind's rules as built. After: rewritten. Every computed property of the element and all its descendants compared: <b>${same} of ${looks.before.length} identical</b>; and the pictures pixel for pixel. Transitions off and effect thumbnails' canvases hidden while pictured (they draw whenever their frame arrives).</p>
<div class="grid">${rows}</div>`,
  );
  console.log(`looks: computed styles identical before and after in ${same} of ${looks.before.length}; pixels identical in ${pixels.filter((p) => !p.differ).length}`);
  if (failedLooks) process.exit(1);
}
process.exit(0);
