// fx-ops-check.ts checks the inspector's clip edits (src/trim/fx/ops.ts):
// effect stack order, values and keyframes, the green screen toggle, the
// pinned Adjust and speed curves. Run: bun scripts/fx-ops-check.ts
import { migrateProject } from "../src/trim/migrate";
import {
  addEffect,
  animAt,
  clearKeys,
  ensureAdjust,
  moveEffect,
  moveKey,
  newEffect,
  removeEffect,
  setGreenScreen,
  setKeyColor,
  setKeyEase,
  setSpeedCurve,
  setValue,
  toggleKey,
  valueOf,
} from "../src/trim/fx/ops";
import type { Clip, Project } from "../src/trim/project";
import { presetCurve } from "../src/trim/speed";

let failed = 0;
function check(name: string, ok: boolean, got?: unknown) {
  if (!ok) {
    failed++;
    console.error(`FAIL ${name}${got === undefined ? "" : `: ${JSON.stringify(got)}`}`);
  }
}

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
  chroma: { color: "#00ff00", similarity: 0.3, blend: 0.1 },
  filters: { brightness: 0.1, contrast: 1, saturation: 1, look: "none", blur: 0 },
};
const v2: Project = {
  version: 2,
  id: "proj-1",
  name: "t",
  canvas: { width: 1920, height: 1080, fps: 30 },
  media: [],
  tracks: [{ id: "vt", kind: "video", muted: false, hidden: false, locked: false, clips: [clip] }],
  texts: [],
  crop: null,
};
const get = (p: Project) => p.tracks[0].clips[0];
const types = (p: Project) => get(p).effects!.map((e) => e.type).join(",");

// Migration then stack ops.
let p = migrateProject(v2);
check("migrated", types(p) === "chromaKey,adjust", types(p));
const blur = newEffect("blur", { amount: 5 });
p = addEffect(p, "c1", blur);
check("add on top", types(p) === "chromaKey,adjust,blur", types(p));
p = moveEffect(p, "c1", blur.id, 0);
check("move to bottom", types(p) === "blur,chromaKey,adjust", types(p));
check("unknown type refused", addEffect(p, "c1", newEffect("nope")) === p);
p = removeEffect(p, "c1", blur.id);
p = setGreenScreen(p, "c1", false);
check("green off", types(p) === "adjust", types(p));
p = setKeyColor(p, "c1", "#0000ff");
check("eyedropper turns green on first", types(p) === "chromaKey,adjust" && get(p).effects![0].params.color === "#0000ff", get(p).effects);

// A v2-shaped clip becomes v3 on its first effect edit.
const q = setGreenScreen(v2, "c1", false);
check("v2 clip converted", !get(q).chroma && types(q) === "adjust", get(q));

// ensureAdjust goes after a leading green screen.
let r = setGreenScreen(migrateProject({ ...v2, tracks: [{ ...v2.tracks[0], clips: [{ ...clip, filters: { ...clip.filters, brightness: 0 } }] }] }), "c1", true);
check("no adjust yet", types(r) === "chromaKey", types(r));
let id: string | undefined;
[r, id] = ensureAdjust(r, "c1");
check("adjust after key", types(r) === "chromaKey,adjust" && !!id, types(r));

// Values and keyframes on an effect param.
const adj = get(p).effects!.find((e) => e.type === "adjust")!;
const ref = { fx: adj.id, param: "exposure" };
p = setValue(p, "c1", ref, 9, 0);
check("clamped static value", valueOf(get(p), ref, 0) === 3, valueOf(get(p), ref, 0));
p = toggleKey(p, "c1", ref, 1);
p = setValue(p, "c1", ref, -1, 3);
check("two keys", get(p).effects!.find((e) => e.id === adj.id)!.keys!.exposure.length === 2);
check("interpolates", Math.abs((valueOf(get(p), ref, 2) as number) - 1) < 1e-9, valueOf(get(p), ref, 2));
p = setKeyEase(p, "c1", ref, 1, "hold");
check("hold", valueOf(get(p), ref, 2.9) === 3, valueOf(get(p), ref, 2.9));
p = moveKey(p, "c1", ref, 3, 4, true);
check("alt-drag copies", get(p).effects!.find((e) => e.id === adj.id)!.keys!.exposure.length === 3);
p = clearKeys(p, "c1", ref, 4);
check("clear keeps the value", !get(p).effects!.find((e) => e.id === adj.id)!.keys && valueOf(get(p), ref, 0) === -1, valueOf(get(p), ref, 0));

// Anim targets: transform keyframes, rotation kept through updateClip.
p = setValue(p, "c1", { anim: "rotation" }, 30, 0);
p = setValue(p, "c1", { anim: "x" }, 0.2, 0);
check("rotation kept", get(p).transform.rotation === 30 && get(p).transform.x === 0.2, get(p).transform);
p = toggleKey(p, "c1", { anim: "opacity" }, 0);
p = setValue(p, "c1", { anim: "opacity" }, 0, 2);
check("opacity keyed", Math.abs(animAt(get(p), 1).opacity - 0.5) < 1e-9, animAt(get(p), 1));
p = toggleKey(p, "c1", { anim: "opacity" }, 0);
p = toggleKey(p, "c1", { anim: "opacity" }, 2);
check("last key removed leaves the value", !get(p).anim && get(p).transform.opacity === 0, get(p));

// Speed curves (presets and fitting are speed.ts; setSpeedCurve fits through updateClip).
const pre = presetCurve("montage", 8);
check("preset spans the clip", pre.points[0].s === 0 && pre.points[pre.points.length - 1].s === 8 && pre.preset === "montage");
p = setSpeedCurve(p, "c1", { points: [{ s: 5, v: 50 }, { s: -1, v: 0 }], audio: "follow" });
check("set curve is fitted to the clip", !!get(p).speedCurve && get(p).speedCurve!.points[0].s === 0 && Math.max(...get(p).speedCurve!.points.map((q) => q.v)) <= 10, get(p).speedCurve);

if (failed) {
  console.error(`${failed} check(s) failed`);
  process.exit(1);
}
console.log("fx ops: all checks passed");
