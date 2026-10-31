// fx-presets-check.ts checks saved filters and imported LUTs on the editor
// side: .cube parsing and trilinear sampling (src/trim/fx/cube.ts, the same
// cases as internal/fx/cube_test.go), and saving/applying presets
// (src/trim/fx/presets.ts): keyframe times as fractions, groups, replace.
// Run: bun scripts/fx-presets-check.ts
import { cubeFn, parseCube, sampleCube } from "../src/trim/fx/cube";
import { applyPreset, instancesOf, presetFromClip } from "../src/trim/fx/presets";
import { findClip, type Clip, type Project } from "../src/trim/project";

let failed = 0;
function check(name: string, ok: boolean, got?: unknown) {
  if (!ok) {
    failed++;
    console.error(`FAIL ${name}${got === undefined ? "" : `: ${JSON.stringify(got)}`}`);
  }
}
const near = (a: number, b: number, eps = 1e-6) => Math.abs(a - b) <= eps;

// --- cube ----------------------------------------------------------------------

function identity(n: number, header = ""): string {
  let s = header + `LUT_3D_SIZE ${n}\n`;
  for (let b = 0; b < n; b++) for (let g = 0; g < n; g++) for (let r = 0; r < n; r++) s += `${r / (n - 1)} ${g / (n - 1)} ${b / (n - 1)}\n`;
  return s;
}

const id5 = parseCube(identity(5, '# c\nTITLE "Teal"\n\nDOMAIN_MIN 0 0 0\nDOMAIN_MAX 1 1 1\n'));
check("title", id5.title === "Teal" && id5.size === 5, id5.title);
for (const c of [
  [0, 0, 0],
  [1, 1, 1],
  [0.3, 0.7, 0.11],
  [0.999, 0.5, 0.01],
  [-1, 2, 0.5],
] as [number, number, number][]) {
  const o = sampleCube(id5, c);
  for (let k = 0; k < 3; k++) check(`identity ${c}`, near(o[k], Math.max(0, Math.min(1, c[k]))), o);
}

let inv = "DOMAIN_MAX 2 2 2\nLUT_3D_SIZE 2\n";
for (let b = 0; b < 2; b++) for (let g = 0; g < 2; g++) for (let r = 0; r < 2; r++) inv += `${1 - r} ${1 - g} ${1 - b}\n`;
const invC = parseCube(inv);
const io = sampleCube(invC, [1, 0, 2]);
check("invert + domain", near(io[0], 0.5) && io[1] === 1 && io[2] === 0, io);
const half = cubeFn(invC, 0.5)!([0, 0, 0]);
check("intensity mixes", near(half[0], 0.5) && near(half[1], 0.5) && near(half[2], 0.5), half);
check("intensity 0 is no step", cubeFn(invC, 0) === null);

for (const [name, text] of Object.entries({
  "1D": "LUT_1D_SIZE 4\n0 0 0\n",
  "no size": "TITLE x\n",
  "too big": "LUT_3D_SIZE 200\n",
  short: "LUT_3D_SIZE 2\n0 0 0\n",
  long: identity(2) + "0 0 0\n",
  nan: "LUT_3D_SIZE 2\n" + "0 NaN 0\n".repeat(8),
  early: "0 0 0\nLUT_3D_SIZE 2\n",
})) {
  let threw = false;
  try {
    parseCube(text);
  } catch {
    threw = true;
  }
  check(`rejects ${name}`, threw);
}

// --- presets -------------------------------------------------------------------

const clip: Clip = {
  id: "c1",
  media: "m1",
  start: 10,
  in: 0,
  out: 8,
  speed: 1,
  volume: 1,
  muted: false,
  fadeIn: 0,
  fadeOut: 0,
  transform: { x: 0.5, y: 0.5, scale: 1, opacity: 1 },
  filters: { brightness: 0, contrast: 1, saturation: 1, look: "none", blur: 0 },
  effects: [
    { id: "fx1", type: "adjust", enabled: true, params: { exposure: 0.5 }, keys: { contrast: [{ t: 2, v: 1, ease: "linear" }, { t: 6, v: 1.4, ease: "in" }] } },
    { id: "fx2", type: "look", enabled: false, params: { look: "bw" } },
    { id: "fx3", type: "vignette-from-the-future", enabled: true, params: {} },
    { id: "fx4", type: "look", enabled: true, params: { look: "cool" }, group: "g", amount: 0.5 },
  ],
} as unknown as Clip;

const doc = presetFromClip(clip, "  Night ")!;
check("save keeps enabled, known effects", doc.effects.map((e) => e.type).join() === "adjust,look", doc.effects);
check("save trims the name", doc.name === "Night");
check("save strips ids and groups", !("id" in doc.effects[0]) && !("group" in doc.effects[1]));
const kt = doc.effects[0].keys!.contrast.map((k) => k.t);
check("keys as fractions of 8 s", near(kt[0], 0.25) && near(kt[1], 0.75), kt);
check("nothing to save", presetFromClip({ ...clip, effects: [] } as Clip, "x") === undefined);

const inst = instancesOf({ id: "pre-1", effects: doc.effects }, 4, "pre-1");
const it = inst[0].keys!.contrast.map((k) => k.t);
check("apply scales keys to 4 s", near(it[0], 1) && near(it[1], 3), it);
check("apply gives new ids", inst.every((e) => e.id.startsWith("fx") && e.id !== "fx1"), inst.map((e) => e.id));
check("apply tags the group", inst.every((e) => e.group === "pre-1"));

const project = {
  version: 3,
  id: "p",
  name: "p",
  canvas: { width: 1920, height: 1080, fps: 30 },
  media: [],
  tracks: [{ id: "v1", kind: "video", muted: false, hidden: false, locked: false, clips: [clip] }],
  texts: [],
  crop: null,
} as unknown as Project;
const pre = { id: "pre-1", effects: doc.effects };
const added = findClip(applyPreset(project, "c1", pre), "c1")!.clip.effects!;
check("append keeps the stack", added.length === 6 && added[0].id === "fx1", added.map((e) => e.type));
check("append groups", added.slice(4).every((e) => e.group === "pre-1"));
const replaced = findClip(applyPreset(project, "c1", pre, { replace: true }), "c1")!.clip.effects!;
check("replace swaps the stack", replaced.length === 2 && replaced.every((e) => !e.group), replaced);
const single = findClip(applyPreset(project, "c1", { id: "builtin-bw", effects: [{ type: "look", enabled: true, params: { look: "bw" } }] }), "c1")!.clip.effects!;
check("one effect isn't grouped", single.length === 5 && !single[4].group, single[4]);
check("unknown types only: no change", applyPreset(project, "c1", { id: "x", effects: [{ type: "nope", enabled: true, params: {} }] }) === project);
const locked = { ...project, tracks: [{ ...project.tracks[0], locked: true }] } as Project;
check("locked track: no change", applyPreset(locked, "c1", pre) === locked);

if (failed) {
  console.error(`${failed} check(s) failed`);
  process.exit(1);
}
console.log("fx-presets-check: ok");
