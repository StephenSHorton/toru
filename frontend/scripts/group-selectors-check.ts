// group-selectors-check.ts checks the rewrite of Tailwind's group-* rules
// (group-selectors.ts, whiteboard#56):
//
// 1. It builds Toru's shared stylesheet (src/index.css) with the app's own
//    Vite config, and serves it the way the dev server does (where
//    Tailwind's rules are still nested, `&:is(…)`), and fails if any rule
//    still has a group variant's subtree-wide form,
//    `:is(:where(.group…):hover *)`, or any other `:is(<hover or focus state> *)`.
// 2. It builds the same sheet with Tailwind alone, rewrites each group rule
//    the way the plugin does, and expects the app build to hold exactly that.
// 3. In headless Chrome (or Edge), for each rule and for harder made-up ones
//    (named groups, three stacked variants, a pseudo-element, a combinator
//    after, and nested rules as the dev server has them), it builds random trees of elements and checks the old and new
//    selector match the same elements, and win and lose the cascade the same
//    (equal specificity). Pseudo-classes are stood in for by classes, which
//    the rewrite treats alike.
// 4. To show the check can fail, it also tries a wrong rewrite of stacked
//    variants (`G2 G1 .x` alone, the order Tailwind wrote them) and expects
//    it to be caught.
//
// Run: bun scripts/group-selectors-check.ts
import { build, createServer } from "vite";
import tailwindcss from "@tailwindcss/vite";
import { spawn } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { rewriteGroupSelectors, selectorsOf } from "../group-selectors";

const root = path.resolve(import.meta.dir, "..");
const entry = path.join(root, "src", "index.css");

async function buildCss(app: boolean): Promise<string> {
  const out = await build({
    configFile: app ? path.join(root, "vite.config.ts") : false,
    root,
    mode: "production",
    logLevel: "silent",
    ...(app ? {} : { plugins: [tailwindcss()] }),
    build: { write: false, emptyOutDir: false, minify: false, rollupOptions: { input: entry } },
  });
  let css = "";
  for (const r of Array.isArray(out) ? out : [out]) {
    if (!("output" in r)) throw new Error("vite build returned a watcher, not output");
    for (const f of r.output) if (f.type === "asset" && f.fileName.endsWith(".css")) css += typeof f.source === "string" ? f.source : new TextDecoder().decode(f.source);
  }
  if (!css) throw new Error("the build produced no CSS");
  return css;
}

/** index.css as the app's dev server serves it. */
async function devCss(): Promise<string> {
  const s = await createServer({ configFile: path.join(root, "vite.config.ts"), root, logLevel: "silent", server: { port: 47828, hmr: false } });
  try {
    const r = await s.transformRequest("/src/index.css?direct");
    if (!r?.code) throw new Error("the dev server served no CSS");
    return r.code;
  } finally {
    await s.close();
  }
}

/** Every leaf rule's selector list (a prelude before a body with no braces in it). */
function selectorLists(css: string) {
  const out: string[] = [];
  for (const m of css.matchAll(/([^{};]*)\{[^{}]*\}/g)) {
    const s = m[1].trim();
    if (s && !s.startsWith("@")) out.push(s.replace(/\s+/g, " "));
  }
  return out;
}

// A pointer or focus state in a subtree-wide :is(). (`:enabled` and other
// states nothing but a script changes may stay there.)
const subtreeWide = /:is\([^(){}]*:(?:hover|focus|focus-visible|focus-within|active)(?![\w-])[^(){}]* \*\)/;

let failed = false;
const fail = (msg: string) => {
  failed = true;
  console.error(`FAIL ${msg}`);
};

// ── 1 and 2: the built CSS ─────────────────────────────────────────────

const [plain, app, dev] = await Promise.all([buildCss(false), buildCss(true), devCss()]);
const appLists = new Set(selectorLists(app));
const left = [...appLists].filter((s) => subtreeWide.test(s));
for (const s of left) fail(`still restyles a subtree: ${s}`);
// The dev server's CSS is nested: any prelude, leaf or not.
const devLeft = [...dev.matchAll(/([^{};]*)\{/g)].map((m) => m[1].trim()).filter((s) => subtreeWide.test(s));
for (const s of devLeft) fail(`still restyles a subtree in the dev server: ${s}`);
const devRewritten = (dev.match(/:where\(\.group[^{]*&/g) ?? []).length;
if (devRewritten < 15) fail(`the dev server's CSS has only ${devRewritten} rewritten group rules`);
console.log(`dev server CSS: ${devRewritten} group rules rewritten, ${devLeft.length} subtree-wide rules left`);

// A pair: the old and new selector, each a chain of nested rules (outermost
// first; one for a flat rule).
type Pair = [string[], string[]];
const pairs: Pair[] = [];
for (const s of new Set(selectorLists(plain))) {
  if (!s.includes(":where(.group")) continue;
  const want = rewriteGroupSelectors(s);
  if (want === s) fail(`not rewritten: ${s}`);
  // The minifier may drop the space after a comma.
  const norm = (x: string) => x.replace(/,\s*/g, ", ");
  if (![...appLists].some((a) => norm(a) === norm(want))) fail(`the app build doesn't have ${want}`);
  for (const o of selectorsOf(s)) pairs.push([[o], [rewriteGroupSelectors(o)]]);
}
console.log(`built CSS: ${pairs.length} group selectors, all rewritten; ${left.length} subtree-wide rules left`);
if (pairs.length < 15) fail(`only ${pairs.length} group selectors found; expected about 19`);

// Harder made-up cases.
const made = [
  ".a:is(:where(.group\\/edge):hover *)",
  ".a:is(:where(.group):enabled *):is(:where(.group):hover *):is(:where(.group\\/loud):focus-within *)",
  ".a:is(:where(.group):hover *)::after",
  ".a:is(:where(.group):hover *) > .b",
  ".a:is(:where(.group\\/edge):hover *):is(:where(.group):focus-visible *) .b",
  ":is(:where(.group):hover *)",
];
for (const o of made) {
  const r = rewriteGroupSelectors(o);
  if (r === o) fail(`made-up case not rewritten: ${o}`);
  pairs.push([[o], [r]]);
}
// Nested, as Tailwind writes them before they're optimized (the dev server):
// a stacked variant is one nested rule per group.
const nested = [
  [".x", "&:is(:where(.group):hover *)"],
  [".x", "&:is(:where(.group\\/edge):hover *)"],
  [".x", "&:is(:where(.group):enabled *)", "&:is(:where(.group):hover *)"],
  [".x", "&:is(:where(.group):enabled *):is(:where(.group):hover *)"],
];
for (const o of nested) {
  const r = o.map(rewriteGroupSelectors);
  if (r.every((x, i) => x === o[i])) fail(`nested case not rewritten: ${o.join(" { ")}`);
  pairs.push([o, r]);
}

// ── 3 and 4: the same meaning, in Chrome ───────────────────────────────

const browser = [
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  "C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe",
  "C:/Program Files/Microsoft/Edge/Application/msedge.exe",
  "C:/Program Files/Google/Chrome/Application/chrome.exe",
  "/usr/bin/google-chrome",
  "/usr/bin/chromium",
].find((p) => fs.existsSync(p));
if (!browser) throw new Error("no Chrome or Edge found");

const profile = fs.mkdtempSync(path.join(os.tmpdir(), "toru-group-check-"));
const port = 9300 + Math.floor(Math.random() * 500);
const proc = spawn(browser, ["--headless=new", `--remote-debugging-port=${port}`, `--user-data-dir=${profile}`, "--no-first-run", "--disable-extensions", "about:blank"], { stdio: "ignore" });

// The page's side: random trees, old and new selector compared on each element.
function compare(pairs: [string[], string[]][], seed: number) {
  let x = seed;
  const rand = () => ((x = (x * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff);
  const pseudo = /(?<!:):(hover|focus-visible|focus-within|focus|active|enabled|disabled)(?![\w-])/g;
  const plain = (s: string) => s.replace(pseudo, ".ps-$1").replace(/::[\w-]+/g, "");
  const out: string[] = [];
  let checked = 0;
  for (const [o1, r1] of pairs) {
    const o = o1.map(plain);
    const r = r1.map(plain);
    const o0 = o1.join(" { ");
    const r0 = r1.join(" { ");
    const classes = new Set<string>(["group", "ps-hover", "ps-enabled", "ps-focus-visible", "ps-focus-within"]);
    for (const m of [...o, ...r].join(" ").matchAll(/\.((?:\\.|[\w-])+)/g)) classes.add(m[1].replace(/\\(.)/g, "$1"));
    const pool = [...classes];
    // A rule with the chain's nesting around decls.
    const rule = (chain: string[], decls: string) => chain.reduceRight((inner, sel) => `${sel}{${inner}}`, decls);
    const style = document.createElement("style");
    // Which match: a property each. Equal specificity: whichever comes
    // later wins, both ways round.
    style.textContent = [rule(o, "--mo:1"), rule(r, "--mr:1"), rule(o, "--late:o"), rule(r, "--late:r"), rule(r, "--early:r"), rule(o, "--early:o")].join(" ");
    document.head.append(style);
    let bad = "";
    for (let t = 0; t < 150 && !bad; t++) {
      const host = document.createElement("div");
      const grow = (el: Element, depth: number) => {
        for (const c of pool) if (rand() < 0.3) el.classList.add(c);
        if (depth < 5) for (let k = 0, n = 1 + Math.floor(rand() * 2.4); k < n; k++) grow(el.appendChild(document.createElement("div")), depth + 1);
      };
      grow(host.appendChild(document.createElement("div")), 0);
      document.body.append(host);
      for (const el of host.querySelectorAll("*")) {
        checked++;
        const cs = getComputedStyle(el);
        const a = cs.getPropertyValue("--mo").trim() === "1";
        const b = cs.getPropertyValue("--mr").trim() === "1";
        if (a !== b) {
          bad = `${o0} → ${r0}: ${a ? "old" : "new"} matches alone at ${el.className} under ${el.parentElement?.className}`;
          break;
        }
        if (a) {
          if (cs.getPropertyValue("--late").trim() !== "r" || cs.getPropertyValue("--early").trim() !== "o") {
            bad = `${o0} → ${r0}: different specificity`;
            break;
          }
        }
      }
      host.remove();
    }
    style.remove();
    if (bad) out.push(bad);
  }
  return { checked, bad: out };
}

try {
  let wsUrl = "";
  for (let i = 0; i < 100 && !wsUrl; i++) {
    try {
      wsUrl = (await (await fetch(`http://127.0.0.1:${port}/json/new?about:blank`, { method: "PUT" })).json()).webSocketDebuggerUrl;
    } catch {
      await Bun.sleep(100);
    }
  }
  if (!wsUrl) throw new Error("the browser didn't start");
  const ws = new WebSocket(wsUrl);
  await new Promise((res, rej) => ((ws.onopen = res), (ws.onerror = rej)));
  let seq = 0;
  const pending = new Map<number, (v: any) => void>();
  ws.onmessage = (e) => {
    const m = JSON.parse(String(e.data));
    pending.get(m.id)?.(m);
  };
  const evaluate = async (expression: string) => {
    const id = ++seq;
    ws.send(JSON.stringify({ id, method: "Runtime.evaluate", params: { expression, returnByValue: true } }));
    const m: any = await new Promise((res) => pending.set(id, res));
    if (m.result?.exceptionDetails) throw new Error(JSON.stringify(m.result.exceptionDetails));
    return m.result?.result?.value;
  };
  const run = (p: Pair[]) => evaluate(`(${compare.toString()})(${JSON.stringify(p)}, 7)`) as Promise<{ checked: number; bad: string[] }>;

  const res = await run(pairs);
  for (const b of res.bad) fail(b);
  console.log(`Chrome: ${pairs.length} selector pairs, ${res.checked} elements compared, ${res.bad.length} differences`);

  // A wrong rewrite of stacked variants must be caught.
  const stacked = pairs.filter(([o]) => o.length === 1 && (o[0].match(/:is\(:where\(\.group/g) ?? []).length > 1).map(([o]) => o[0]);
  if (!stacked.length) fail("no stacked variant to try the wrong rewrite on");
  const wrong = stacked.map((o): Pair => {
    const groups = [...o.matchAll(/:is\((:where\(\.group[^ ]*) \*\)/g)].map((m) => m[1]);
    const rest = o.replace(/:is\(:where\(\.group[^ ]* \*\)/g, "") || "*";
    return [[o], [`${[...groups].reverse().join(" ")} ${rest}`]];
  });
  const caught = await run(wrong);
  if (caught.bad.length !== wrong.length) fail(`the wrong rewrite of stacked variants was caught ${caught.bad.length} of ${wrong.length} times`);
  else console.log(`self-test: the wrong rewrite of ${wrong.length} stacked selectors was caught each time`);
  ws.close();
} finally {
  proc.kill();
  await Bun.sleep(300);
  fs.rmSync(profile, { recursive: true, force: true });
}

if (failed) process.exit(1);
console.log("ok");
