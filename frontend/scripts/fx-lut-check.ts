// fx-lut-check holds the preview's colour LUTs (src/trim/fx/lut.ts, through
// each effect's colour steps) to internal/fx/testdata/lut_golden.json, the
// table the export's Go twin (internal/fx) must reproduce too, so the
// preview's sampler3D and the export's lut3d read the same numbers.
//
//   bun scripts/fx-lut-check.ts           check against the golden file
//   bun scripts/fx-lut-check.ts --write   rewrite it (after a deliberate
//                                         change to the colour maths)
import fs from "node:fs";
import path from "node:path";
import { defOf, resolveParams } from "../src/trim/fx/registry";
import { generateLut, LUT_SIZE, type ColorFn } from "../src/trim/fx/lut";
import { shaders } from "../src/trim/fx/types";

const FILE = path.resolve(import.meta.dir, "../../internal/fx/testdata/lut_golden.json");
const TOL = 1e-9;

type Case = { name: string; stack: { type: string; params: Record<string, unknown> }[] };

// mulberry32, as keyframes-golden uses: the same random cases every run.
function rng(seed: number) {
  return () => {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function cases(): Case[] {
  const out: Case[] = [{ name: "adjust defaults (identity)", stack: [{ type: "adjust", params: {} }] }];
  const adjust = defOf("adjust")!;
  for (const p of adjust.params) {
    if (p.type !== "number" || p.id === "sharpen") continue;
    out.push({ name: `adjust ${p.id} min`, stack: [{ type: "adjust", params: { [p.id]: p.min } }] });
    out.push({ name: `adjust ${p.id} max`, stack: [{ type: "adjust", params: { [p.id]: p.max } }] });
  }
  for (const look of ["bw", "sepia", "vintage", "cool", "warm"]) {
    out.push({ name: `look ${look}`, stack: [{ type: "look", params: { look } }] });
    out.push({ name: `look ${look} sat 1.7 amount 0.6`, stack: [{ type: "look", params: { look, saturation: 1.7, amount: 0.6 } }] });
  }
  const r = rng(82);
  for (let i = 0; i < 12; i++) {
    const params: Record<string, unknown> = {};
    for (const p of adjust.params) if (p.type === "number" && p.id !== "sharpen" && r() < 0.6) params[p.id] = p.min + (p.max - p.min) * r();
    const looks = ["none", "bw", "sepia", "vintage", "cool", "warm"];
    out.push({
      name: `random ${i}: adjust, look, adjust`,
      stack: [
        { type: "adjust", params },
        { type: "look", params: { look: looks[i % looks.length], saturation: 3 * r(), amount: r() } },
        { type: "adjust", params: { contrast: 0.5 + r(), brightness: 0.6 * r() - 0.3 } },
      ],
    });
  }
  return out;
}

// fnsOf is a stack's colour functions, through the same shader modules the
// renderer uses.
function fnsOf(c: Case): ColorFn[] {
  const fns: ColorFn[] = [];
  for (const e of c.stack) {
    const def = defOf(e.type)!;
    for (const s of shaders[e.type].steps(resolveParams(def, e.params), { w: 1920, h: 1080, t: 0 })) {
      if (s.kind !== "color") throw new Error(`${c.name}: ${e.type} has a non-colour step`);
      fns.push(s.fn);
    }
  }
  return fns;
}

// Grid points every golden case records (index into the LUT, red fastest):
// the corners, the middle, and a fixed random spread.
const N = LUT_SIZE;
const SAMPLES = (() => {
  const idx = (r: number, g: number, b: number) => (b * N + g) * N + r;
  const s = new Set<number>([idx(0, 0, 0), idx(N - 1, N - 1, N - 1), idx(N - 1, 0, 0), idx(0, N - 1, 0), idx(0, 0, N - 1), idx(16, 16, 16)]);
  const r = rng(33);
  while (s.size < 64) s.add(Math.floor(r() * N * N * N));
  return [...s].sort((a, b) => a - b);
})();

function table(c: Case) {
  const lut = generateLut(fnsOf(c));
  const sum = [0, 0, 0];
  for (let i = 0; i < lut.length; i += 3) for (let k = 0; k < 3; k++) sum[k] += lut[i + k];
  return { samples: SAMPLES.map((i) => [i, lut[i * 3], lut[i * 3 + 1], lut[i * 3 + 2]]), sum };
}

const all = cases().map((c) => ({ ...c, ...table(c) }));
if (process.argv.includes("--write")) {
  fs.mkdirSync(path.dirname(FILE), { recursive: true });
  // One case per line keeps the file short and its diffs readable.
  const head = JSON.stringify({ size: N, note: "frontend/scripts/fx-lut-check.ts --write regenerates this" }).slice(0, -1);
  fs.writeFileSync(FILE, `${head},"cases":[\n${all.map((c) => JSON.stringify(c)).join(",\n")}\n]}\n`);
  console.log(`wrote ${all.length} cases to ${FILE}`);
  process.exit(0);
}

const golden = JSON.parse(fs.readFileSync(FILE, "utf8")) as { size: number; cases: typeof all };
let failed = 0;
if (golden.size !== N || golden.cases.length !== all.length) {
  console.log(`golden has size ${golden.size} and ${golden.cases.length} cases; this code makes ${N} and ${all.length} (run with --write?)`);
  process.exit(1);
}
for (const [i, g] of golden.cases.entries()) {
  const c = all[i];
  let worst = 0;
  for (const [j, s] of g.samples.entries()) for (let k = 1; k < 4; k++) worst = Math.max(worst, Math.abs(s[k] - c.samples[j][k]));
  for (let k = 0; k < 3; k++) worst = Math.max(worst, Math.abs(g.sum[k] - c.sum[k]) / (N * N * N));
  const ok = g.name === c.name && worst <= TOL;
  if (!ok) failed++;
  if (!ok || process.env.VERBOSE) console.log(`${ok ? "ok  " : "FAIL"} ${g.name}  worst ${worst.toExponential(2)}`);
}
console.log(failed ? `${failed} of ${all.length} LUT cases differ` : `all ${all.length} LUT cases match`);
process.exit(failed ? 1 : 0);
