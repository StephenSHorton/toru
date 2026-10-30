// keyframes-golden writes internal/fx/testdata/keyframes_golden.json: anchor
// cases with hand-checked answers plus seeded random cases, each with the
// value valueAt (src/trim/fx/keyframes.ts) gives. keyframes_test.go (Go) and
// project-check.ts both check against it, so the preview and the export
// animate the same way. Rerun only when the keyframe maths changes on
// purpose, and say so in the PR.
//
//   bun scripts/keyframes-golden.ts
import fs from "node:fs";
import path from "node:path";
import { valueAt } from "../src/trim/fx/keyframes";
import type { Ease, Keyframe, ParamValue } from "../src/trim/project";

type Case = { name: string; keys: Keyframe[]; t: number; base: ParamValue; want: ParamValue };
const k = (t: number, v: ParamValue, ease: Ease = "linear"): Keyframe => ({ t, v, ease });

// Anchors: the answer is written by hand, and checked against valueAt below.
const anchors: Case[] = [
  { name: "no keys gives base", keys: [], t: 3, base: 0.7, want: 0.7 },
  { name: "before first key", keys: [k(1, 10), k(3, 20)], t: 0, base: 0, want: 10 },
  { name: "on first key", keys: [k(1, 10), k(3, 20)], t: 1, base: 0, want: 10 },
  { name: "after last key", keys: [k(1, 10), k(3, 20)], t: 9, base: 0, want: 20 },
  { name: "linear midpoint", keys: [k(1, 10), k(3, 20)], t: 2, base: 0, want: 15 },
  { name: "ease in at half is 1/8", keys: [k(0, 0, "in"), k(2, 8)], t: 1, base: 0, want: 1 },
  { name: "ease out at half is 7/8", keys: [k(0, 0, "out"), k(2, 8)], t: 1, base: 0, want: 7 },
  { name: "inOut at half is half", keys: [k(0, 0, "inOut"), k(2, 8)], t: 1, base: 0, want: 4 },
  { name: "inOut at quarter is 1/16", keys: [k(0, 0, "inOut"), k(4, 16)], t: 1, base: 0, want: 1 },
  { name: "hold keeps the first value", keys: [k(0, 5, "hold"), k(2, 9)], t: 1.999, base: 0, want: 5 },
  { name: "hold reaches the next key on time", keys: [k(0, 5, "hold"), k(2, 9)], t: 2, base: 0, want: 9 },
  { name: "third segment", keys: [k(0, 0), k(1, 10), k(2, 0), k(4, 100)], t: 3, base: 0, want: 50 },
  { name: "colour midpoint per channel", keys: [k(0, "#000000"), k(2, "#ff8040")], t: 1, base: "#000000", want: "#804020" },
  { name: "point midpoint", keys: [k(0, [0, 0]), k(1, [1, 0.5])], t: 0.5, base: [0, 0], want: [0.5, 0.25] },
  { name: "boolean holds until the next key", keys: [k(0, false), k(1, true)], t: 0.99, base: false, want: false },
  { name: "enum jumps at the next key", keys: [k(0, "smear"), k(1, "wrap")], t: 1, base: "smear", want: "wrap" },
  { name: "two keys at one time", keys: [k(1, 3), k(1, 7)], t: 1, base: 0, want: 3 },
  { name: "negative numbers", keys: [k(0, -1), k(1, 1)], t: 0.25, base: 0, want: -0.5 },
];

// Seeded random cases (mulberry32), so reruns give the same file.
let seed = 0x7072;
const rnd = () => {
  seed = (seed + 0x6d2b79f5) | 0;
  let x = Math.imul(seed ^ (seed >>> 15), 1 | seed);
  x = (x + Math.imul(x ^ (x >>> 7), 61 | x)) ^ x;
  return ((x ^ (x >>> 14)) >>> 0) / 4294967296;
};
const eases: Ease[] = ["linear", "in", "out", "inOut", "hold"];
const pick = <T>(xs: T[]) => xs[Math.floor(rnd() * xs.length)];
const round = (n: number, d = 4) => Math.round(n * 10 ** d) / 10 ** d;
const randomValue = (kind: string): ParamValue =>
  kind === "number" ? round(rnd() * 200 - 100) : kind === "point" ? [round(rnd()), round(rnd())] : "#" + Math.floor(rnd() * 0xffffff).toString(16).padStart(6, "0");
const random: Case[] = [];
for (let i = 0; i < 120; i++) {
  const kind = pick(["number", "number", "number", "point", "color"]);
  const n = 1 + Math.floor(rnd() * 5);
  const ts = Array.from({ length: n }, () => round(rnd() * 10, 3)).sort((a, b) => a - b);
  const keys = ts.map((t) => k(t, randomValue(kind), pick(eases)));
  const t = round(rnd() * 12 - 1, 3);
  random.push({ name: `random ${i} (${kind}, ${n} keys)`, keys, t, base: randomValue(kind), want: valueAt(keys, t, randomValue(kind)) });
}
// base must be the same value the case stores
for (const c of random) c.want = valueAt(c.keys, c.t, c.base);

const same = (a: ParamValue, b: ParamValue) => JSON.stringify(a) === JSON.stringify(b) || (typeof a === "number" && typeof b === "number" && Math.abs(a - b) < 1e-9);
let bad = 0;
for (const c of anchors) {
  const got = valueAt(c.keys, c.t, c.base);
  if (!same(got, c.want)) {
    console.log(`FAIL anchor ${c.name}: got ${JSON.stringify(got)}, want ${JSON.stringify(c.want)}`);
    bad++;
  }
}
if (bad) process.exit(1);
const out = path.resolve(import.meta.dir, "../../internal/fx/testdata/keyframes_golden.json");
fs.writeFileSync(out, JSON.stringify({ cases: [...anchors, ...random] }, null, 1) + "\n");
console.log(`wrote ${anchors.length} anchors + ${random.length} random cases to ${out}`);
