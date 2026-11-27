// frost-check.ts builds Toru's shared stylesheet (src/index.css) the way the
// production build does (Vite + Tailwind, whose lightningcss pass optimizes
// the CSS) and fails if the built `.frost` rule has no unprefixed
// `backdrop-filter`. WebView2 reads only the unprefixed property, so without
// it every frosted surface loses its blur (whiteboard#15).
//
// The usual way to lose it: writing `-webkit-backdrop-filter` *after*
// `backdrop-filter` in the same rule. lightningcss treats the later prefixed
// line as an override and drops the unprefixed one.
//
// To show the check can fail, it also builds a copy of index.css with the
// unprefixed line taken out, and expects that copy to be caught.
// Run: bun scripts/frost-check.ts
import { build, type Plugin } from "vite";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath } from "node:url";
import { join } from "node:path";

const root = fileURLToPath(new URL("..", import.meta.url));
const entry = join(root, "src", "index.css");

// edit rewrites index.css before Tailwind sees it (for the self-test).
function edit(change: (css: string) => string): Plugin {
  return {
    name: "frost-check-edit",
    enforce: "pre",
    transform(code, id) {
      if (id.split("?")[0].replace(/\\/g, "/").endsWith("/src/index.css")) return { code: change(code), map: null };
    },
  };
}

async function buildCss(change?: (css: string) => string): Promise<string> {
  const out = await build({
    configFile: false,
    root,
    mode: "production",
    logLevel: "silent",
    // The edit goes first: Tailwind's own plugins also run "pre".
    plugins: [...(change ? [edit(change)] : []), tailwindcss()],
    build: { write: false, emptyOutDir: false, rollupOptions: { input: entry } },
  });
  const results = Array.isArray(out) ? out : [out];
  let css = "";
  for (const r of results) {
    if (!("output" in r)) throw new Error("vite build returned a watcher, not output");
    for (const f of r.output) {
      if (f.type === "asset" && f.fileName.endsWith(".css")) css += typeof f.source === "string" ? f.source : new TextDecoder().decode(f.source);
    }
  }
  if (!css) throw new Error("the build produced no CSS");
  return css;
}

// frostDecls lists the declarations of every built rule whose selector names
// the `.frost` class (not `.frost-something`).
function frostDecls(css: string): { rules: number; props: string[] } {
  const props: string[] = [];
  let rules = 0;
  // Leaf rules only: a selector and a body with no nested braces.
  for (const m of css.matchAll(/([^{}]*)\{([^{}]*)\}/g)) {
    if (!/\.frost(?![\w-])/.test(m[1])) continue;
    rules++;
    for (const d of m[2].split(";")) {
      const i = d.indexOf(":");
      if (i > 0) props.push(d.slice(0, i).trim().toLowerCase());
    }
  }
  return { rules, props };
}

// problem says what is wrong with the built CSS's .frost, or "" if nothing.
function problem(css: string): string {
  const { rules, props } = frostDecls(css);
  if (rules === 0) return "the built CSS has no .frost rule";
  if (!props.includes("backdrop-filter")) {
    return `.frost has no unprefixed backdrop-filter (it has: ${props.join(", ") || "nothing"})`;
  }
  return "";
}

let failed = 0;

const real = problem(await buildCss());
if (real) {
  failed++;
  console.error(`FAIL src/index.css: ${real}`);
  console.error("  WebView2 needs the unprefixed property, or frosted surfaces lose their blur.");
  console.error("  Keep -webkit-backdrop-filter *before* backdrop-filter in .frost: lightningcss drops");
  console.error("  the unprefixed line when the prefixed one comes after it.");
} else {
  console.log("ok   src/index.css: built .frost keeps its unprefixed backdrop-filter");
}

// Self-test: the same stylesheet with the unprefixed line removed must fail.
const unprefixed = /^([ \t]*)backdrop-filter:([^;]*);[ \t]*\r?\n/m;
const prefixed = /^[ \t]*-webkit-backdrop-filter:[^;]*;[ \t]*\r?\n/m;
let found = false;
const removed = problem(
  await buildCss((css) => {
    found = unprefixed.test(css);
    return css.replace(unprefixed, "");
  }),
);
if (!found) {
  if (!real) {
    failed++;
    console.error("FAIL self-test: no line starting `backdrop-filter:` in src/index.css to take out; update this script");
  }
} else if (!removed) {
  failed++;
  console.error("FAIL self-test: with backdrop-filter removed from index.css the check still passed");
} else {
  console.log(`ok   self-test (unprefixed line removed) is caught: ${removed}`);
}

// Note only, never a failure: the old order (prefixed line after the
// unprefixed one), the bug whiteboard#15 fixed. If this stops being caught,
// lightningcss no longer drops the line and the order rule could be relaxed.
if (found && prefixed.test(await Bun.file(entry).text())) {
  const oldOrder = problem(
    await buildCss((css) => {
      const p = css.match(prefixed)![0];
      const u = css.match(unprefixed)!;
      return css.replace(p, "").replace(u[0], u[0] + p);
    }),
  );
  console.log(oldOrder ? `note old order (-webkit- line after backdrop-filter) is caught: ${oldOrder}` : "note old order no longer loses backdrop-filter in this lightningcss");
}

if (failed) {
  console.error(`frost-check: ${failed} failed`);
  process.exit(1);
}
console.log("frost-check: all passed");
