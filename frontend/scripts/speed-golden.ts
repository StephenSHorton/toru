// speed-golden writes (or with --check, verifies) the speed-ramp golden
// table, internal/vid/testdata/speed_golden.json, from src/trim/speed.ts.
// internal/vid/speed_test.go checks the Go maths against the same file, so
// the preview and the export map time the same way. Hand-worked cases are
// checked against their closed forms first.
//
//   bun scripts/speed-golden.ts           (write)
//   bun scripts/speed-golden.ts --check   (verify, exit 1 on a mismatch)
import { readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { normCurve, outAt, outLength, presetCurve, RAMP_PRESETS, speedAt, srcAt, audioAudibleAt } from "../src/trim/speed";
import type { SpeedCurve } from "../src/trim/project";

const file = path.resolve(import.meta.dir, "../../internal/vid/testdata/speed_golden.json");
let failed = 0;
const close = (a: number, b: number, tol = 1e-9) => Math.abs(a - b) <= tol * Math.max(1, Math.abs(b));
function check(name: string, ok: boolean, detail?: unknown) {
  if (!ok) {
    failed++;
    console.error(`FAIL ${name}`, detail === undefined ? "" : JSON.stringify(detail));
  }
}

// Hand-worked closed forms.
{
  const flat = normCurve({ points: [{ s: 0, v: 2 }, { s: 4, v: 2 }], audio: "follow" }, 4);
  check("k=0: 4 s at 2x is 2 s", close(outLength(flat), 2));
  check("k=0: inverse", close(srcAt(flat, 1.5), 3));
  // 1x → 3x over 2 s: k = 1, T = ln(3).
  const up = normCurve({ points: [{ s: 0, v: 1 }, { s: 2, v: 3 }], audio: "follow" }, 2);
  check("ramp 1→3: length ln 3", close(outLength(up), Math.log(3)));
  check("ramp 1→3: T(1) = ln 2", close(outAt(up, 1), Math.log(2)));
  check("ramp 1→3: s(ln 2) = 1", close(srcAt(up, Math.log(2)), 1));
  check("ramp 1→3: v(1) = 2", close(speedAt(up, 1), 2));
  // 0.1x for 1 s source = 10 s on the timeline.
  const slow = normCurve({ points: [{ s: 0, v: 0.1 }], audio: "follow" }, 1);
  check("one point holds: 1 s at 0.1x is 10 s", close(outLength(slow), 10));
  // Speeds clamp to 0.1..10 and points outside the range are pulled in.
  const wild = normCurve({ points: [{ s: -1, v: 50 }, { s: 9, v: 0.01 }], audio: "follow" }, 3);
  check("clamps", wild.points.length === 2 && wild.points[0].v === 10 && wild.points[1].v === 0.1 && wild.points[1].s === 3, wild);
  check("mute: silent off 1x", !audioAudibleAt({ ...up, audio: "mute" }, 1) && audioAudibleAt({ ...up, audio: "mute" }, 0));
  check("follow: 0.2x silent, 4x heard", !audioAudibleAt(slow, 0.5) && audioAudibleAt(normCurve({ points: [{ s: 0, v: 4 }], audio: "follow" }, 1), 0.5));
}

// A small seeded PRNG (mulberry32) so the table is stable.
function rng(seed: number) {
  return () => {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

type Case = {
  name: string;
  srcLen: number;
  points: { s: number; v: number }[];
  length: number;
  at: { s: number; T: number; v: number }[]; // outAt / speedAt at s
  inv: { T: number; s: number }[]; // srcAt at T
};

function caseOf(name: string, c: SpeedCurve, srcLen: number, r: () => number): Case {
  const n = normCurve(c, srcLen);
  const L = outLength(n);
  const ss = [0, srcLen, ...n.points.map((p) => p.s), ...Array.from({ length: 8 }, () => r() * srcLen)];
  const Ts = [0, L, L + 1, -0.5, ...Array.from({ length: 8 }, () => r() * L)];
  return {
    name,
    srcLen,
    points: n.points,
    length: L,
    at: ss.map((s) => ({ s, T: outAt(n, s), v: speedAt(n, s) })),
    inv: Ts.map((T) => ({ T, s: srcAt(n, T) })),
  };
}

const r = rng(0x5eed);
const cases: Case[] = [];
for (const len of [1, 3.25, 10])
  for (const p of RAMP_PRESETS) cases.push(caseOf(`${p.id} ${len}s`, presetCurve(p.id, len), len, r));
cases.push(caseOf("flat 2x", { points: [{ s: 0, v: 2 }, { s: 4, v: 2 }], audio: "follow" }, 4, r));
cases.push(caseOf("1 to 3", { points: [{ s: 0, v: 1 }, { s: 2, v: 3 }], audio: "follow" }, 2, r));
for (let i = 0; i < 40; i++) {
  const len = 0.5 + r() * 20;
  const n = 2 + Math.floor(r() * 6);
  const points = Array.from({ length: n }, () => ({ s: r() * len, v: 0.1 * Math.pow(100, r()) }));
  // Some segments flat (k = 0), the trap the spec calls out.
  if (i % 4 === 0 && points.length > 2) points[1].v = points[2].v;
  cases.push(caseOf(`random ${i}`, { points, audio: "follow" }, len, r));
}

if (process.argv.includes("--check")) {
  const golden: { cases: Case[] } = JSON.parse(readFileSync(file, "utf8"));
  for (const g of golden.cases) {
    const n = normCurve({ points: g.points, audio: "follow" }, g.srcLen);
    check(`${g.name}: length`, close(outLength(n), g.length), [outLength(n), g.length]);
    for (const a of g.at) check(`${g.name}: T(${a.s})`, close(outAt(n, a.s), a.T) && close(speedAt(n, a.s), a.v));
    for (const a of g.inv) check(`${g.name}: s(${a.T})`, close(srcAt(n, a.T), a.s));
  }
  check("golden has cases", golden.cases.length > 50);
  console.log(failed ? `${failed} failed` : `speed golden: ${golden.cases.length} cases match`);
} else if (!failed) {
  writeFileSync(file, JSON.stringify({ note: "Written by frontend/scripts/speed-golden.ts; checked by internal/vid/speed_test.go.", cases }, null, 1) + "\n");
  console.log(`wrote ${cases.length} cases to ${file}`);
}
process.exit(failed ? 1 : 0);
