import { newId } from "@/trim/edl";
import { clampParam, defOf, resolveParams } from "@/trim/fx/registry";
import { paramDefault } from "@/trim/fx/def";
import { removeKey, setKey, valueAt } from "@/trim/fx/keyframes";
import { clipEffects } from "@/trim/fx/stack";
import {
  DEFAULT_FILTERS,
  findClip,
  updateClip,
  type AnimTarget,
  type Clip,
  type Ease,
  type EffectInstance,
  type Keyframe,
  type ParamValue,
  type Project,
  type SpeedCurve,
} from "@/trim/project";

// ops.ts holds the v3 clip edits the inspector, the timeline's keyframe lane
// and the player handles make (docs/video-editor-v3.md, Inspector UX): the
// effect stack (add, remove, reorder, enable, reset, duplicate), effect
// params, keyframes on effect params and on the anim targets (transform and
// volume), the green screen toggle, and the speed curve. Each is a pure
// Project → Project function, so agent commands can call the same ones.
//
// Keyframe times are clip-local timeline seconds (playhead − clip.start).
// Setting a value that has keyframes sets (or adds) the key at that time;
// a value without keyframes is the static value.

// KeyRef names one keyframable value of a clip: an effect param or an anim
// target (x, y, scale, opacity, rotation, volume).
export type KeyRef = { fx: string; param: string } | { anim: AnimTarget };

const EPS = 1e-3;

// writeClip replaces a clip with fn(clip), skipping locked tracks. For edits
// that don't change the clip's length (effects, keyframes), which
// updateClip's clamps don't cover.
function writeClip(p: Project, clipId: string, fn: (c: Clip) => Clip): Project {
  const hit = findClip(p, clipId);
  if (!hit || hit.track.locked) return p;
  const next = fn(hit.clip);
  if (next === hit.clip) return p;
  const clips = hit.track.clips.slice();
  clips[hit.index] = next;
  return { ...p, tracks: p.tracks.map((t) => (t.id === hit.track.id ? { ...t, clips } : t)) };
}

// --- Effect stack ------------------------------------------------------------

// newEffect is a fresh instance of type with params at their defaults.
export function newEffect(type: string, params: Record<string, ParamValue> = {}): EffectInstance {
  return { id: newId("fx"), type, enabled: true, params };
}

// editEffects rewrites a clip's stack. A clip still in v2 shape (chroma and
// filters) becomes v3 on its first effect edit.
export function editEffects(p: Project, clipId: string, fn: (fx: EffectInstance[], c: Clip) => EffectInstance[]): Project {
  return writeClip(p, clipId, (c) => {
    const cur = clipEffects(c);
    const next = fn(cur, c);
    if (next === cur) return c;
    if (c.effects) return { ...c, effects: next };
    const { chroma: _chroma, ...rest } = c;
    return { ...rest, filters: { ...DEFAULT_FILTERS }, effects: next };
  });
}

function editEffect(p: Project, clipId: string, fxId: string, fn: (e: EffectInstance) => EffectInstance): Project {
  return editEffects(p, clipId, (fx) => {
    const i = fx.findIndex((e) => e.id === fxId);
    if (i < 0) return fx;
    const n = fn(fx[i]);
    if (n === fx[i]) return fx;
    const out = fx.slice();
    out[i] = n;
    return out;
  });
}

// addEffect puts e on the clip's stack at index (default: the top, so it
// runs last). Unknown types are refused.
export function addEffect(p: Project, clipId: string, e: EffectInstance, index?: number): Project {
  if (!defOf(e.type)) return p;
  return editEffects(p, clipId, (fx) => {
    const out = fx.slice();
    out.splice(index === undefined ? out.length : Math.max(0, Math.min(out.length, index)), 0, e);
    return out;
  });
}

export function removeEffect(p: Project, clipId: string, fxId: string): Project {
  return editEffects(p, clipId, (fx) => (fx.some((e) => e.id === fxId) ? fx.filter((e) => e.id !== fxId) : fx));
}

// moveEffect moves an effect to index in the stack (0 runs first).
export function moveEffect(p: Project, clipId: string, fxId: string, index: number): Project {
  return editEffects(p, clipId, (fx) => {
    const from = fx.findIndex((e) => e.id === fxId);
    const to = Math.max(0, Math.min(fx.length - 1, Math.round(index)));
    if (from < 0 || from === to) return fx;
    const out = fx.slice();
    const [e] = out.splice(from, 1);
    out.splice(to, 0, e);
    return out;
  });
}

export function setEffectEnabled(p: Project, clipId: string, fxId: string, enabled: boolean): Project {
  return editEffect(p, clipId, fxId, (e) => (e.enabled === enabled ? e : { ...e, enabled }));
}

// resetEffect puts every param (or only those in params) back to its
// default and drops their keyframes.
export function resetEffect(p: Project, clipId: string, fxId: string, params?: string[]): Project {
  return editEffect(p, clipId, fxId, (e) => {
    if (!params) return { ...e, params: {}, keys: undefined };
    const next = { ...e, params: { ...e.params }, keys: e.keys ? { ...e.keys } : undefined };
    for (const id of params) {
      delete next.params[id];
      if (next.keys) delete next.keys[id];
    }
    if (next.keys && !Object.keys(next.keys).length) next.keys = undefined;
    return next;
  });
}

// duplicateEffect puts a copy (new id) right above the original.
export function duplicateEffect(p: Project, clipId: string, fxId: string): Project {
  return editEffects(p, clipId, (fx) => {
    const i = fx.findIndex((e) => e.id === fxId);
    if (i < 0) return fx;
    const out = fx.slice();
    out.splice(i + 1, 0, { ...structuredClone(fx[i]), id: newId("fx") });
    return out;
  });
}

// setEffectAmount sets a group's intensity (0..1) on every instance of it.
export function setGroupAmount(p: Project, clipId: string, group: string, amount: number): Project {
  const a = Math.max(0, Math.min(1, amount));
  return editEffects(p, clipId, (fx) => (fx.some((e) => e.group === group) ? fx.map((e) => (e.group === group ? { ...e, amount: a } : e)) : fx));
}

// ungroup turns a group back into separate cards (each keeps its own params;
// the intensity is dropped).
export function ungroup(p: Project, clipId: string, group: string): Project {
  return editEffects(p, clipId, (fx) => (fx.some((e) => e.group === group) ? fx.map((e) => (e.group === group ? { ...e, group: undefined, amount: undefined } : e)) : fx));
}

// --- The pinned Adjust and the green screen -----------------------------------

// pinnedAdjust is the clip's Adjust tab instance: its first adjust effect
// that isn't part of a saved filter's group (those stay in the group's
// card). (A migrated clip with a look has its adjust after the look, for an
// exact match with v2; it stays where it is.)
export function pinnedAdjust(fx: EffectInstance[]): EffectInstance | undefined {
  return fx.find((e) => e.type === "adjust" && !e.group);
}

// ensureAdjust gives the clip an adjust effect if it has none: first in the
// stack, after a leading green screen (keying reads the recorded colours,
// so an exposure or temperature change mustn't move the key colour).
// Returns the project and the adjust's id.
export function ensureAdjust(p: Project, clipId: string): [Project, string | undefined] {
  const hit = findClip(p, clipId);
  if (!hit) return [p, undefined];
  const have = pinnedAdjust(clipEffects(hit.clip));
  if (have) return [p, have.id];
  const e = newEffect("adjust");
  const fx = clipEffects(hit.clip);
  let at = 0;
  while (at < fx.length && fx[at].type === "chromaKey") at++;
  return [addEffect(p, clipId, e, at), e.id];
}

export function greenScreen(fx: EffectInstance[]): EffectInstance | undefined {
  return fx.find((e) => e.type === "chromaKey");
}

// setGreenScreen turns the green screen on (a chromaKey first in the stack,
// where v2 keyed) or off (every chromaKey removed).
export function setGreenScreen(p: Project, clipId: string, on: boolean): Project {
  return editEffects(p, clipId, (fx) => {
    const has = fx.some((e) => e.type === "chromaKey");
    if (on === has) return fx;
    return on ? [newEffect("chromaKey"), ...fx] : fx.filter((e) => e.type !== "chromaKey");
  });
}

// setKeyColor sets the green screen's key colour (the player eyedropper),
// turning the green screen on if it's off. With keyframes on the colour, it
// keys it at t.
export function setKeyColor(p: Project, clipId: string, color: string, t = 0): Project {
  let q = setGreenScreen(p, clipId, true);
  const fx = findClip(q, clipId) ? clipEffects(findClip(q, clipId)!.clip) : [];
  const g = greenScreen(fx);
  if (g) q = setValue(q, clipId, { fx: g.id, param: "color" }, color, t);
  return q;
}

// --- Values and keyframes -------------------------------------------------------

function keysOf(c: Clip, ref: KeyRef): Keyframe[] | undefined {
  if ("anim" in ref) return c.anim?.[ref.anim];
  return clipEffects(c).find((e) => e.id === ref.fx)?.keys?.[ref.param];
}

// baseOf is a value's static (un-keyed) value.
function baseOf(c: Clip, ref: KeyRef): ParamValue | undefined {
  if ("anim" in ref) return animBase(c, ref.anim);
  const e = clipEffects(c).find((x) => x.id === ref.fx);
  const def = e && defOf(e.type);
  return def ? resolveParams(def, e.params)[ref.param] : undefined;
}

function animBase(c: Clip, target: AnimTarget): number {
  if (target === "volume") return c.volume;
  if (target === "rotation") return c.transform.rotation ?? 0;
  return c.transform[target];
}

// ANIM_RANGE is each anim target's range (as updateClip clamps them).
export const ANIM_RANGE: Record<AnimTarget, [number, number]> = {
  x: [-1, 2],
  y: [-1, 2],
  scale: [0.01, 10],
  opacity: [0, 1],
  rotation: [-360, 360],
  volume: [0, 2],
};

function clampValue(c: Clip, ref: KeyRef, v: ParamValue): ParamValue | undefined {
  if ("anim" in ref) {
    const [lo, hi] = ANIM_RANGE[ref.anim];
    return typeof v === "number" && Number.isFinite(v) ? Math.max(lo, Math.min(hi, v)) : undefined;
  }
  const e = clipEffects(c).find((x) => x.id === ref.fx);
  const pd = e && defOf(e.type)?.params.find((q) => q.id === ref.param);
  return pd ? clampParam(pd, v) : undefined;
}

// valueOf is a value at clip-local time t (keyframes applied).
export function valueOf(c: Clip, ref: KeyRef, t: number): ParamValue | undefined {
  const base = baseOf(c, ref);
  if (base === undefined) return undefined;
  return valueAt(keysOf(c, ref), t, base);
}

// animAt is a clip's transform and volume at clip-local time t.
export function animAt(c: Clip, t: number): { x: number; y: number; scale: number; opacity: number; rotation: number; volume: number } {
  const v = (k: AnimTarget) => valueAt(c.anim?.[k], t, animBase(c, k)) as number;
  return { x: v("x"), y: v("y"), scale: v("scale"), opacity: v("opacity"), rotation: v("rotation"), volume: v("volume") };
}

function writeKeys(c: Clip, ref: KeyRef, keys: Keyframe[] | undefined): Clip {
  if ("anim" in ref) {
    const anim = { ...(c.anim ?? {}) };
    if (keys?.length) anim[ref.anim] = keys;
    else delete anim[ref.anim];
    return { ...c, anim: Object.keys(anim).length ? anim : undefined };
  }
  return c; // effect keys go through editEffect
}

// setKeys replaces one value's keyframes (undefined = none).
export function setKeys(p: Project, clipId: string, ref: KeyRef, keys: Keyframe[] | undefined): Project {
  if ("anim" in ref) return writeClip(p, clipId, (c) => writeKeys(c, ref, keys));
  return editEffect(p, clipId, ref.fx, (e) => {
    const all = { ...(e.keys ?? {}) };
    if (keys?.length) all[ref.param] = keys;
    else delete all[ref.param];
    return { ...e, keys: Object.keys(all).length ? all : undefined };
  });
}

// setValue sets a value: its key at t when it has keyframes, else its static
// value. Out-of-range values are clamped; unreadable ones ignored.
export function setValue(p: Project, clipId: string, ref: KeyRef, v: ParamValue, t: number): Project {
  const hit = findClip(p, clipId);
  if (!hit || hit.track.locked) return p;
  const c = hit.clip;
  const val = clampValue(c, ref, v);
  if (val === undefined) return p;
  const keys = keysOf(c, ref);
  if (keys?.length) return setKeys(p, clipId, ref, setKey(keys, t, val));
  if ("anim" in ref) return setAnimBase(p, clipId, ref.anim, val as number);
  return editEffect(p, clipId, ref.fx, (e) => ({ ...e, params: { ...e.params, [ref.param]: val } }));
}

function setAnimBase(p: Project, clipId: string, target: AnimTarget, v: number): Project {
  const c = findClip(p, clipId)?.clip;
  if (!c) return p;
  if (target === "volume") return updateClip(p, clipId, { volume: v });
  if (target === "rotation") return writeClip(p, clipId, (x) => ({ ...x, transform: { ...x.transform, rotation: v } }));
  // updateClip rebuilds the transform from x/y/scale/opacity; keep rotation.
  const rot = c.transform.rotation;
  const q = updateClip(p, clipId, { transform: { ...c.transform, [target]: v } });
  return rot === undefined ? q : writeClip(q, clipId, (x) => (x.transform.rotation === rot ? x : { ...x, transform: { ...x.transform, rotation: rot } }));
}

// keyAt is the keyframe within 1 ms of t, if any.
export function keyAt(keys: Keyframe[] | undefined, t: number): Keyframe | undefined {
  return keys?.find((k) => Math.abs(k.t - t) < EPS);
}

// toggleKey adds a key at t holding the current value there, or removes the
// key at t. Turning keyframing on records the value at the playhead.
export function toggleKey(p: Project, clipId: string, ref: KeyRef, t: number): Project {
  const c = findClip(p, clipId)?.clip;
  if (!c) return p;
  const keys = keysOf(c, ref);
  if (keyAt(keys, t)) {
    const left = removeKey(keys, t);
    // The last key going away leaves the value it had as the static value.
    if (!left) {
      const v = valueOf(c, ref, t);
      let q = setKeys(p, clipId, ref, undefined);
      if (v !== undefined) q = setValue(q, clipId, ref, v, t);
      return q;
    }
    return setKeys(p, clipId, ref, left);
  }
  const v = valueOf(c, ref, t);
  return v === undefined ? p : setKeys(p, clipId, ref, setKey(keys, Math.max(0, t), v));
}

// clearKeys drops a value's keyframes, keeping its value at t as the static
// value.
export function clearKeys(p: Project, clipId: string, ref: KeyRef, t: number): Project {
  const c = findClip(p, clipId)?.clip;
  const v = c && valueOf(c, ref, t);
  let q = setKeys(p, clipId, ref, undefined);
  if (v !== undefined) q = setValue(q, clipId, ref, v, t);
  return q;
}

// setKeyEase sets the ease of the key at t (the segment from it to the next).
export function setKeyEase(p: Project, clipId: string, ref: KeyRef, t: number, e: Ease): Project {
  const c = findClip(p, clipId)?.clip;
  const keys = c && keysOf(c, ref);
  if (!keys || !keyAt(keys, t)) return p;
  return setKeys(p, clipId, ref, keys.map((k) => (Math.abs(k.t - t) < EPS ? { ...k, ease: e } : k)));
}

// moveKey moves (or with copy, copies) the key at from to time to, replacing
// a key already there.
export function moveKey(p: Project, clipId: string, ref: KeyRef, from: number, to: number, copy = false): Project {
  const c = findClip(p, clipId)?.clip;
  const keys = c && keysOf(c, ref);
  const k = keyAt(keys, from);
  if (!keys || !k) return p;
  const rest = copy ? keys : keys.filter((x) => x !== k);
  return setKeys(p, clipId, ref, setKey(rest, Math.max(0, to), k.v, k.ease));
}

// allKeys lists every keyframed value of a clip with its keys (the
// timeline's keyframe lane).
export function allKeys(c: Clip): { ref: KeyRef; label: string; keys: Keyframe[] }[] {
  const out: { ref: KeyRef; label: string; keys: Keyframe[] }[] = [];
  for (const [target, keys] of Object.entries(c.anim ?? {}) as [AnimTarget, Keyframe[]][]) {
    if (keys?.length) out.push({ ref: { anim: target }, label: ANIM_LABEL[target], keys });
  }
  for (const e of clipEffects(c)) {
    const def = defOf(e.type);
    for (const [param, keys] of Object.entries(e.keys ?? {})) {
      if (!keys?.length) continue;
      const label = def?.params.find((q) => q.id === param)?.label ?? param;
      out.push({ ref: { fx: e.id, param }, label: `${def?.name ?? e.type}: ${label}`, keys });
    }
  }
  return out;
}

export const ANIM_LABEL: Record<AnimTarget, string> = { x: "Position X", y: "Position Y", scale: "Size", opacity: "Opacity", rotation: "Rotation", volume: "Volume" };

// paramIsDefault reports whether an effect param is at its default with no
// keyframes (so its reset control can hide).
export function paramIsDefault(e: EffectInstance, param: string): boolean {
  const pd = defOf(e.type)?.params.find((q) => q.id === param);
  if (!pd) return true;
  if (e.keys?.[param]?.length) return false;
  const v = clampParam(pd, e.params[param]);
  return v === undefined || JSON.stringify(v) === JSON.stringify(paramDefault(pd));
}

// --- Speed curve ------------------------------------------------------------------

// setSpeedCurve sets (or with undefined, removes) a clip's speed curve.
// updateClip fits it to the clip (normCurve in speed.ts). Presets live in
// speed.ts (RAMP_PRESETS, presetCurve).
export function setSpeedCurve(p: Project, clipId: string, curve: SpeedCurve | undefined): Project {
  const c = findClip(p, clipId)?.clip;
  if (!c || c.hold) return p;
  return updateClip(p, clipId, { speedCurve: curve });
}
