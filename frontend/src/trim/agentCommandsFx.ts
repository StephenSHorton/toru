import type { AgentCommandSpec, AgentHandler } from "@/lib/agent";
import type { EffectDef, ParamDef } from "@/trim/fx/def";
import { setKey } from "@/trim/fx/keyframes";
import { loadLut } from "@/trim/fx/luts";
import { addEffect, ANIM_RANGE, keyAt, moveEffect, newEffect, removeEffect, setEffectEnabled, setKeys, setValue, valueOf, type KeyRef } from "@/trim/fx/ops";
import { applyPreset, presetFromClip, refreshPresets, type PresetDoc } from "@/trim/fx/presets";
import { clampParam, defOf, defs, resolveParams } from "@/trim/fx/registry";
import { clipEffects } from "@/trim/fx/stack";
import { clipLength, findClip, type AnimTarget, type Clip, type Ease, type EffectInstance, type ParamValue, type Project, type Track } from "@/trim/project";

// The editor's effect agent commands (docs/video-editor-v3.md, Effect
// registry "Agent commands" and User presets): list the effect definitions
// as JSON Schema, edit a clip's effect stack, keyframe effect params and the
// clip's transform/volume, and save or apply saved filters. They call the
// same pure ops as the inspector (fx/ops.ts, fx/presets.ts), and
// agentCommands.ts registers them with its own helpers, so each is one undo
// step the person sees. Times are TIMELINE seconds like every editor
// command; keyframes are stored clip-local (t − clip start).

export interface FxCommandCtx {
  project: () => Project;
  playhead: () => number;
  reg: (name: string, spec: AgentCommandSpec, handler: AgentHandler) => void;
  edit: (fn: (p: Project) => Project, why: string) => Project;
  clipOr: (p: Project, clipId: string) => { track: Track; clip: Clip };
  obj: (props?: Record<string, unknown>, required?: string[]) => Record<string, unknown>;
  lockedNote: (locked: boolean) => string;
  selectClip: (id?: string) => void;
}

const ANIM_TARGETS = Object.keys(ANIM_RANGE) as AnimTarget[];
const EASES: Ease[] = ["linear", "in", "out", "inOut", "hold"];

// paramSchema is one param of a definition as JSON Schema.
export function paramSchema(p: ParamDef): Record<string, unknown> {
  const desc = [p.label, p.hint, p.animate === false ? "not keyframable" : "keyframable"].filter(Boolean).join(". ");
  switch (p.type) {
    case "number":
      return { type: "number", minimum: p.min, maximum: p.max, default: p.default, description: desc + (p.unit ? ` (${p.unit})` : p.display === "percent" ? " (1 = 100%)" : "") };
    case "boolean":
      return { type: "boolean", default: p.default, description: desc };
    case "color":
      return { type: "string", pattern: "^#[0-9a-fA-F]{6}$", default: p.default, description: desc + ' ("#rrggbb")' };
    case "enum":
      return { type: "string", enum: p.options.map((o) => o.id), default: p.default, description: `${desc}: ${p.options.map((o) => `${o.id} = ${o.label}`).join(", ")}` };
    case "point":
      return { type: "array", items: { type: "number", minimum: 0, maximum: 1 }, minItems: 2, maxItems: 2, default: p.default, description: desc + " ([x, y], 0..1 of the frame)" };
    case "file":
      return { type: "string", default: p.default, description: desc + (p.accept.includes(".cube") ? " (an imported LUT's id from preset.luts; import one with preset.importLut)" : "") };
  }
}

// defSchema is a whole definition as editor.effects lists it.
export function defSchema(d: EffectDef) {
  return {
    type: d.type,
    name: d.name,
    category: d.category,
    params: { type: "object", properties: Object.fromEntries(d.params.map((p) => [p.id, paramSchema(p)])), additionalProperties: false },
    keyframable: d.params.filter((p) => p.animate !== false).map((p) => p.id),
  };
}

// checkParams validates params against a definition: unknown ids and
// unreadable values fail with what's allowed; numbers out of range are
// clamped (as the inspector does).
export function checkParams(def: EffectDef, params: unknown): Record<string, ParamValue> {
  if (params === undefined || params === null) return {};
  if (typeof params !== "object" || Array.isArray(params)) throw new Error("params must be an object of param id → value");
  const out: Record<string, ParamValue> = {};
  for (const [id, v] of Object.entries(params as Record<string, unknown>)) {
    const pd = def.params.find((p) => p.id === id);
    if (!pd) throw new Error(`${def.type} has no param "${id}" (it has: ${def.params.map((p) => p.id).join(", ")})`);
    const val = clampParam(pd, v);
    if (val === undefined) throw new Error(`${def.type}.${id}: ${JSON.stringify(v)} isn't a valid ${pd.type}${pd.type === "enum" ? ` (one of ${pd.options.map((o) => o.id).join(", ")})` : ""}`);
    out[id] = val;
  }
  return out;
}

// brief is an effect as the commands return it: its params resolved
// (defaults filled in) and its keyframes.
function brief(e: EffectInstance) {
  const def = defOf(e.type);
  return {
    id: e.id,
    type: e.type,
    enabled: e.enabled,
    params: def ? resolveParams(def, e.params) : e.params,
    ...(e.keys ? { keys: e.keys } : {}),
    ...(e.group ? { group: e.group, amount: e.amount ?? 1 } : {}),
  };
}

export function registerFxCommands(x: FxCommandCtx) {
  const { reg, edit, clipOr, obj, lockedNote } = x;
  const visual = (hit: { track: Track }) => {
    if (hit.track.kind !== "video") throw new Error("effects are for clips on video tracks");
  };
  const fxOr = (c: Clip, effectId: string) => {
    const e = clipEffects(c).find((f) => f.id === String(effectId ?? ""));
    if (!e) throw new Error(`no effect "${effectId}" on that clip (editor.get lists its effects)`);
    return e;
  };
  const stackOf = (p: Project, clipId: string) => {
    const c = findClip(p, clipId)!.clip;
    return { clipId, effects: clipEffects(c).map(brief) };
  };
  // localT is a timeline time (default: the playhead) as clip-local time,
  // clamped into the clip.
  const localT = (c: Clip, t: unknown) => {
    const at = t === undefined || t === null ? x.playhead() : Number(t);
    if (!Number.isFinite(at)) throw new Error("t must be seconds on the timeline");
    return Math.max(0, Math.min(clipLength(c), at - c.start));
  };
  // refOf names a keyframable value from { effectId, param } or { target }.
  const refOf = (c: Clip, input: { effectId?: string; param?: string; target?: string }): { ref: KeyRef; pd?: ParamDef; label: string } => {
    if (input.target !== undefined) {
      if (!ANIM_TARGETS.includes(input.target as AnimTarget)) throw new Error(`target must be one of ${ANIM_TARGETS.join(", ")}`);
      return { ref: { anim: input.target as AnimTarget }, label: input.target };
    }
    if (!input.effectId || !input.param) throw new Error("give effectId and param (an effect's param), or target (x, y, scale, opacity, rotation, volume)");
    const e = fxOr(c, input.effectId);
    const def = defOf(e.type);
    const pd = def?.params.find((p) => p.id === input.param);
    if (!def || !pd) throw new Error(`${e.type} has no param "${input.param}"${def ? ` (it has: ${def.params.map((p) => p.id).join(", ")})` : ""}`);
    if (pd.animate === false) throw new Error(`${e.type}.${pd.id} can't be keyframed`);
    return { ref: { fx: e.id, param: pd.id }, pd, label: `${e.type}.${pd.id}` };
  };
  const keysOf = (c: Clip, ref: KeyRef) => ("anim" in ref ? c.anim?.[ref.anim] : clipEffects(c).find((e) => e.id === ref.fx)?.keys?.[ref.param]);
  const preloadLuts = (effects: { type: string; params?: Record<string, unknown> }[]) =>
    Promise.all(effects.filter((e) => e.type === "lut" && typeof e.params?.lut === "string" && e.params.lut).map((e) => loadLut(e.params!.lut as string)));

  const clipProp = { clipId: { type: "string", description: "From editor.get." } };
  const effectProp = { effectId: { type: "string", description: "An effect's id (editor.get lists each clip's effects)." } };

  reg(
    "editor.effects",
    {
      description:
        "Every effect type a clip can have, with its params as JSON Schema (ranges, defaults, options) and which params can be keyframed. Use the types with editor.addEffect. A clip's effects run bottom to top: effects[0] first.",
      input: obj(),
    },
    () => ({ effects: defs.map(defSchema) }),
  );

  reg(
    "editor.addEffect",
    {
      description:
        "Add an effect to a clip's stack: at the top (runs last) or at index (0 runs first). Params not given take their defaults; numbers are clamped to their range. For a LUT, use type lut with params.lut = an id from preset.luts. Returns the effect's id and the clip's stack.",
      input: obj(
        {
          ...clipProp,
          type: { type: "string", enum: defs.map((d) => d.type), description: "An effect type from editor.effects." },
          params: { type: "object", description: "Param id → value (see editor.effects)." },
          index: { type: "integer", minimum: 0, description: "Stack position; default the top." },
        },
        ["clipId", "type"],
      ),
    },
    async (input: { clipId: string; type: string; params?: unknown; index?: number }) => {
      const hit = clipOr(x.project(), input.clipId);
      visual(hit);
      const def = defOf(String(input.type ?? ""));
      if (!def) throw new Error(`unknown effect type "${input.type}" (editor.effects lists them)`);
      const params = checkParams(def, input.params);
      if (def.type === "lut" && params.lut) {
        if (!(await loadLut(params.lut as string))) throw new Error(`no imported LUT "${params.lut}" (preset.luts lists them; preset.importLut adds one)`);
      }
      const e = newEffect(def.type, params);
      const after = edit((p) => addEffect(p, hit.clip.id, e, input.index === undefined ? undefined : Number(input.index)), "couldn't add it" + lockedNote(hit.track.locked));
      x.selectClip(hit.clip.id);
      return { effectId: e.id, ...stackOf(after, hit.clip.id) };
    },
  );

  reg(
    "editor.setEffect",
    {
      description:
        "Change an effect's params (only those given) and/or turn it on or off. A param that has keyframes gets a key at time t (timeline seconds, default the playhead) instead of a new static value. Numbers are clamped to their range.",
      input: obj(
        {
          ...clipProp,
          ...effectProp,
          params: { type: "object", description: "Param id → value (see editor.effects)." },
          enabled: { type: "boolean", description: "On or off." },
          t: { type: "number", description: "Timeline seconds for keyframed params (default: the playhead)." },
        },
        ["clipId", "effectId"],
      ),
    },
    async (input: { clipId: string; effectId: string; params?: unknown; enabled?: boolean; t?: number }) => {
      const hit = clipOr(x.project(), input.clipId);
      const e = fxOr(hit.clip, input.effectId);
      const def = defOf(e.type);
      if (!def) throw new Error(`${e.type} needs a newer Toru`);
      const params = checkParams(def, input.params);
      if (!Object.keys(params).length && typeof input.enabled !== "boolean") throw new Error("nothing to change: pass params and/or enabled");
      if (def.type === "lut" && params.lut && !(await loadLut(params.lut as string))) throw new Error(`no imported LUT "${params.lut}" (preset.luts lists them)`);
      const t = localT(hit.clip, input.t);
      const after = edit((p) => {
        let q = p;
        for (const [id, v] of Object.entries(params)) q = setValue(q, hit.clip.id, { fx: e.id, param: id }, v, t);
        if (typeof input.enabled === "boolean") q = setEffectEnabled(q, hit.clip.id, e.id, input.enabled);
        return q;
      }, "those are already its settings" + lockedNote(hit.track.locked));
      const n = clipEffects(findClip(after, hit.clip.id)!.clip).find((f) => f.id === e.id)!;
      return brief(n);
    },
  );

  reg(
    "editor.removeEffect",
    { description: "Remove an effect from a clip's stack.", input: obj({ ...clipProp, ...effectProp }, ["clipId", "effectId"]) },
    (input: { clipId: string; effectId: string }) => {
      const hit = clipOr(x.project(), input.clipId);
      const e = fxOr(hit.clip, input.effectId);
      const after = edit((p) => removeEffect(p, hit.clip.id, e.id), "couldn't remove it" + lockedNote(hit.track.locked));
      return { removed: e.id, ...stackOf(after, hit.clip.id) };
    },
  );

  reg(
    "editor.moveEffect",
    {
      description: "Move an effect to index in its clip's stack (0 runs first, so it sits under the others).",
      input: obj({ ...clipProp, ...effectProp, index: { type: "integer", minimum: 0 } }, ["clipId", "effectId", "index"]),
    },
    (input: { clipId: string; effectId: string; index: number }) => {
      const hit = clipOr(x.project(), input.clipId);
      const e = fxOr(hit.clip, input.effectId);
      const index = Number(input.index);
      if (!Number.isInteger(index) || index < 0) throw new Error("index must be an integer >= 0");
      const after = edit((p) => moveEffect(p, hit.clip.id, e.id, index), "it's already there" + lockedNote(hit.track.locked));
      return stackOf(after, hit.clip.id);
    },
  );

  const keyTarget = {
    effectId: { type: "string", description: "The effect whose param to key (with param)." },
    param: { type: "string", description: "The param id (see editor.effects' keyframable)." },
    target: { type: "string", enum: ANIM_TARGETS, description: "Or a clip property instead of an effect param: x, y, scale, opacity, rotation (degrees) or volume." },
  };

  reg(
    "editor.setKeyframe",
    {
      description:
        "Add (or replace) a keyframe at time t (timeline seconds, default the playhead) on an effect param or a clip property (target). value defaults to the current value there; ease shapes the motion from this key to the next (linear, in, out, inOut, hold). The first key turns keyframing on for that value. Keys are stored relative to the clip's start, so they move with it.",
      input: obj(
        {
          ...clipProp,
          ...keyTarget,
          t: { type: "number", description: "Timeline seconds (default: the playhead)." },
          value: { description: "The value at t (default: the current value)." },
          ease: { type: "string", enum: EASES },
        },
        ["clipId"],
      ),
    },
    (input: { clipId: string; effectId?: string; param?: string; target?: string; t?: number; value?: unknown; ease?: string }) => {
      const hit = clipOr(x.project(), input.clipId);
      const { ref, pd, label } = refOf(hit.clip, input);
      if (input.ease !== undefined && !EASES.includes(input.ease as Ease)) throw new Error(`ease must be one of ${EASES.join(", ")}`);
      const t = localT(hit.clip, input.t);
      let v: ParamValue | undefined;
      if (input.value === undefined) v = valueOf(hit.clip, ref, t);
      else if (pd) v = clampParam(pd, input.value);
      else if (typeof input.value === "number" && Number.isFinite(input.value)) {
        const [lo, hi] = ANIM_RANGE[(ref as { anim: AnimTarget }).anim];
        v = Math.max(lo, Math.min(hi, input.value));
      }
      if (v === undefined) throw new Error(`${JSON.stringify(input.value)} isn't a valid value for ${label}`);
      const after = edit((p) => setKeys(p, hit.clip.id, ref, setKey(keysOf(hit.clip, ref), t, v!, input.ease as Ease | undefined)), "that keyframe is already there" + lockedNote(hit.track.locked));
      const keys = keysOf(findClip(after, hit.clip.id)!.clip, ref) ?? [];
      return { clipId: hit.clip.id, value: label, t: hit.clip.start + t, keys: keys.map((k) => ({ ...k, timelineT: hit.clip.start + k.t })) };
    },
  );

  reg(
    "editor.removeKeyframe",
    {
      description:
        "Remove the keyframe at time t (timeline seconds, default the playhead) from an effect param or clip property, or all of them with all: true. When the last key goes, the value it had there stays as the static value.",
      input: obj({ ...clipProp, ...keyTarget, t: { type: "number", description: "Timeline seconds (default: the playhead)." }, all: { type: "boolean", description: "Remove every key of that value." } }, ["clipId"]),
    },
    (input: { clipId: string; effectId?: string; param?: string; target?: string; t?: number; all?: boolean }) => {
      const hit = clipOr(x.project(), input.clipId);
      const { ref, label } = refOf(hit.clip, input);
      const keys = keysOf(hit.clip, ref);
      if (!keys?.length) throw new Error(`${label} has no keyframes`);
      const t = localT(hit.clip, input.t);
      const hitKey = input.all ? undefined : keyAt(keys, t);
      if (!input.all && !hitKey) throw new Error(`no ${label} keyframe at ${(hit.clip.start + t).toFixed(3)}s (keys at ${keys.map((k) => (hit.clip.start + k.t).toFixed(3)).join(", ")})`);
      const left = input.all ? [] : keys.filter((k) => k !== hitKey);
      // The last key going away leaves its value as the static value.
      const keep = left.length ? undefined : valueOf(hit.clip, ref, input.all ? t : hitKey!.t);
      const after = edit((p) => {
        let q = setKeys(p, hit.clip.id, ref, left.length ? left : undefined);
        if (keep !== undefined) q = setValue(q, hit.clip.id, ref, keep, 0);
        return q;
      }, "couldn't remove it" + lockedNote(hit.track.locked));
      return { clipId: hit.clip.id, value: label, keys: (keysOf(findClip(after, hit.clip.id)!.clip, ref) ?? []).map((k) => ({ ...k, timelineT: hit.clip.start + k.t })) };
    },
  );

  reg(
    "editor.applyPreset",
    {
      description:
        "Put a saved filter or built-in look (ids from preset.list) on a clip: added on top of its effects as one group with an intensity, or with replace: true, as the clip's whole effect stack.",
      input: obj({ ...clipProp, presetId: { type: "string", description: "From preset.list." }, replace: { type: "boolean", description: "Replace the clip's effects instead of adding." } }, ["clipId", "presetId"]),
    },
    async (input: { clipId: string; presetId: string; replace?: boolean }) => {
      const hit = clipOr(x.project(), input.clipId);
      visual(hit);
      const { PresetService } = await import("@/lib/api");
      const pre = (await PresetService.Get(String(input.presetId ?? ""))) as unknown as PresetDoc;
      await preloadLuts(pre.effects);
      const after = edit((p) => applyPreset(p, hit.clip.id, pre, { replace: !!input.replace }), "that filter has no effects this Toru knows" + lockedNote(hit.track.locked));
      x.selectClip(hit.clip.id);
      return { preset: pre.name, ...stackOf(after, hit.clip.id) };
    },
  );

  reg(
    "editor.savePreset",
    {
      description: "Save a clip's enabled effects as a new filter in My filters (keyframe times are kept relative to the clip's length). Returns its id.",
      input: obj({ ...clipProp, name: { type: "string", minLength: 1, maxLength: 80 } }, ["clipId", "name"]),
    },
    async (input: { clipId: string; name: string }) => {
      const hit = clipOr(x.project(), input.clipId);
      const name = String(input.name ?? "").trim();
      if (!name) throw new Error("name is required");
      const doc = presetFromClip(hit.clip, name);
      if (!doc) throw new Error("that clip has no effects to save");
      const { PresetService } = await import("@/lib/api");
      const saved = await PresetService.Save(JSON.stringify(doc));
      void refreshPresets();
      return { id: saved.id, name: saved.name, effects: saved.effects?.length ?? 0 };
    },
  );
}
